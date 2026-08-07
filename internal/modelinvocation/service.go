package modelinvocation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"frankenstein/internal/toolinvocation"
)

// Options configures the model invocation service.
type Options struct {
	// Adapters maps provider name to adapter instance. Required.
	Adapters map[string]ProviderAdapter
}

// Service implements the model_invocation capability. It orchestrates
// streaming provider calls, accumulates fragments into a normalized result,
// and performs name and argument repair on tool calls.
type Service struct {
	adapters map[string]ProviderAdapter
}

// NewService creates a Service. Returns an error if opts.Adapters is nil or
// empty.
func NewService(opts Options) (*Service, error) {
	if opts.Adapters == nil || len(opts.Adapters) == 0 {
		return nil, errors.New("modelinvocation: Adapters must be non-nil and non-empty")
	}
	return &Service{adapters: opts.Adapters}, nil
}

// Invoke runs the accumulation loop: validates the request, calls the
// provider adapter, accumulates streaming fragments, builds the result,
// and applies tool-call repair. It returns exactly one of result or failure
// as non-nil.
func (s *Service) Invoke(ctx context.Context, req ModelInvocationRequest) (*ModelInvocationResult, *ModelInvocationFailure) {
	// Step 1 — Validate request.
	if fail := validateRequest(req); fail != nil {
		return nil, fail
	}

	// Step 2 — Resolve adapter.
	adapter, ok := s.adapters[req.Provider]
	if !ok {
		return nil, &ModelInvocationFailure{
			RequestID: req.ID,
			Code:      FailureProviderUnavailable,
			Message:   fmt.Sprintf("unknown provider: %s", req.Provider),
			Retryable: false,
		}
	}

	// Step 3 — Build ProviderRequest.
	providerReq := buildProviderRequest(req)

	// Step 4 — Call adapter.
	fragments, err := adapter.Invoke(ctx, providerReq)
	if err != nil {
		return nil, &ModelInvocationFailure{
			RequestID: req.ID,
			Code:      FailureProviderError,
			Message:   err.Error(),
			Retryable: true,
		}
	}

	// Step 5 — Accumulation loop.
	accumulated := accumulateFragments(ctx, fragments)
	if accumulated.failure != nil {
		accumulated.failure.RequestID = req.ID
		return nil, accumulated.failure
	}

	// Step 6 — Build tool calls.
	builtToolCalls, repairs, stopOverride := buildToolCalls(
		accumulated.toolCalls,
		accumulated.finishReason,
		req.Catalog,
	)

	// Step 7 — Resolve stop reason.
	stopReason := resolveStopReason(accumulated.finishReason)
	if stopOverride {
		stopReason = StopMaxOutput
	}

	// Step 8 — Build result.
	result := &ModelInvocationResult{
		RequestID:  req.ID,
		Content:    accumulated.content.String(),
		Reasoning:  accumulated.reasoning.String(),
		ToolCalls:  builtToolCalls,
		StopReason: stopReason,
		Usage:      accumulated.usage,
		Model:      req.Model,
		Repairs:    repairs,
	}

	if req.Catalog != nil {
		result.CatalogID = req.Catalog.ID
	}

	return result, nil
}

// --- validation ---

func validateRequest(req ModelInvocationRequest) *ModelInvocationFailure {
	if req.Model == "" {
		return failure(req.ID, FailureInvalidRequest, "model is required", false)
	}
	if req.Provider == "" {
		return failure(req.ID, FailureInvalidRequest, "provider is required", false)
	}
	if len(req.Input.Messages) == 0 {
		return failure(req.ID, FailureInvalidRequest, "input.messages must be non-empty", false)
	}
	for i, msg := range req.Input.Messages {
		switch msg.Role {
		case RoleUser:
			if msg.Content == "" {
				return failure(req.ID, FailureInvalidRequest,
					fmt.Sprintf("input.messages[%d]: user message requires non-empty content", i), false)
			}
		case RoleAssistant:
			if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.Reasoning == "" {
				return failure(req.ID, FailureInvalidRequest,
					fmt.Sprintf("input.messages[%d]: assistant message requires at least one of content, tool_calls, or reasoning", i), false)
			}
		case RoleTool:
			if msg.CallID == "" || msg.Content == "" {
				return failure(req.ID, FailureInvalidRequest,
					fmt.Sprintf("input.messages[%d]: tool message requires non-empty call_id and content", i), false)
			}
		}
	}
	return nil
}

func failure(requestID, code, message string, retryable bool) *ModelInvocationFailure {
	return &ModelInvocationFailure{
		RequestID: requestID,
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
}

// --- provider request ---

func buildProviderRequest(req ModelInvocationRequest) ProviderRequest {
	maxTokens := 0
	if req.MaxOutputTokens != nil {
		maxTokens = *req.MaxOutputTokens
	}
	return ProviderRequest{
		Model:     req.Model,
		Messages:  req.Input.Messages,
		System:    req.Input.System,
		Catalog:   req.Catalog,
		MaxTokens: maxTokens,
		APIKey:    "",
		BaseURL:   "",
	}
}

// --- accumulation ---

// toolCallAcc accumulates streaming deltas for one tool call.
type toolCallAcc struct {
	index     int
	id        string
	name      strings.Builder
	arguments strings.Builder
}

// accumulation holds the accumulated state after the fragment loop.
type accumulation struct {
	content      strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*toolCallAcc
	finishReason string
	usage        CallUsage
	failure      *ModelInvocationFailure
}

func accumulateFragments(ctx context.Context, fragments <-chan Fragment) accumulation {
	acc := accumulation{
		toolCalls: make(map[int]*toolCallAcc),
	}

	for frag := range fragments {
		// Error in fragment.
		if frag.Error != nil {
			code := mapFragmentError(frag.Error)
			acc.failure = &ModelInvocationFailure{
				Code:      code,
				Message:   frag.Error.Error(),
				Retryable: isRetryable(code),
			}
			return acc
		}

		// Context cancellation.
		if ctx.Err() != nil {
			acc.failure = &ModelInvocationFailure{
				Code:      FailureCancelled,
				Message:   ctx.Err().Error(),
				Retryable: false,
				Partial: &PartialOutput{
					Content:   acc.content.String(),
					Reasoning: acc.reasoning.String(),
				},
			}
			return acc
		}

		// Accumulate text.
		if frag.DeltaContent != "" {
			acc.content.WriteString(frag.DeltaContent)
		}
		if frag.DeltaReasoning != "" {
			acc.reasoning.WriteString(frag.DeltaReasoning)
		}

		// Accumulate tool calls.
		for _, delta := range frag.ToolCallDeltas {
			tc, exists := acc.toolCalls[delta.Index]
			if !exists {
				tc = &toolCallAcc{index: delta.Index}
				acc.toolCalls[delta.Index] = tc
			}
			if delta.ID != "" && tc.id == "" {
				tc.id = delta.ID
			}
			if delta.Name != "" {
				tc.name.WriteString(delta.Name)
			}
			if delta.Arguments != "" {
				tc.arguments.WriteString(delta.Arguments)
			}
		}

		// Terminal fragment.
		if frag.FinishReason != "" {
			acc.finishReason = frag.FinishReason
		}
		if frag.Usage != nil {
			acc.usage = *frag.Usage
		}
	}

	return acc
}

// --- tool call building ---

// buildToolCalls processes accumulated tool call data into ToolCall values.
// It handles truncation detection, name repair, and argument repair.
// Returns the built tool calls, any repair notes, and whether the stop
// reason was overridden to max_output due to truncation.
func buildToolCalls(
	raw map[int]*toolCallAcc,
	finishReason string,
	catalog *toolinvocation.ToolCatalog,
) ([]toolinvocation.ToolCall, []RepairNote, bool) {
	if len(raw) == 0 {
		return nil, nil, false
	}

	// Sort by delta index.
	indices := make([]int, 0, len(raw))
	for idx := range raw {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	var calls []toolinvocation.ToolCall
	var repairs []RepairNote
	stopOverridden := false

	for resultIdx, deltaIdx := range indices {
		tc := raw[deltaIdx]
		args := tc.arguments.String()

		// Truncation detection.
		if isTruncated(args) {
			if finishReason != "length" && finishReason != "max_output" {
				stopOverridden = true
			}
			continue // do not emit truncated calls
		}

		// Assign sequential ID before repair so notes carry the correct call_id.
		callID := strconv.Itoa(resultIdx)

		// Name repair.
		canonicalName, nameNote := RepairToolName(callID, tc.name.String(), catalog)
		if nameNote != nil {
			repairs = append(repairs, *nameNote)
		}

		// Argument repair.
		repairedArgs, argsNote := RepairArgs(callID, args)
		if argsNote != nil {
			repairs = append(repairs, *argsNote)
		}

		// Parse arguments into map.
		var argsMap map[string]any
		if err := json.Unmarshal([]byte(repairedArgs), &argsMap); err != nil {
			argsMap = make(map[string]any)
		}

		// Look up catalog definition.
		def := findToolDef(catalog, canonicalName)

		var toolID, defRevision string
		if def != nil {
			toolID = def.ID
			defRevision = def.Revision
		}

		calls = append(calls, toolinvocation.ToolCall{
			ID:                 callID,
			ToolID:             toolID,
			DefinitionRevision: defRevision,
			Name:               canonicalName,
			Arguments:          argsMap,
		})
	}

	return calls, repairs, stopOverridden
}

// isTruncated returns true when the arguments string is not structurally
// closed. An empty string is not truncated (no arguments is valid). A
// non-empty string that does not end with } or ] after trimming whitespace
// is considered truncated.
func isTruncated(args string) bool {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return false
	}
	last := trimmed[len(trimmed)-1]
	return last != '}' && last != ']'
}

// findToolDef returns the catalog definition whose Name matches canonicalName.
func findToolDef(catalog *toolinvocation.ToolCatalog, canonicalName string) *toolinvocation.ToolDefinition {
	if catalog == nil {
		return nil
	}
	for i := range catalog.Tools {
		if catalog.Tools[i].Name == canonicalName {
			return &catalog.Tools[i]
		}
	}
	return nil
}

// --- stop reason mapping ---

func resolveStopReason(raw string) StopReason {
	switch raw {
	case "stop", "end_turn", "":
		return StopEndTurn
	case "tool_calls":
		return StopToolCalls
	case "length", "max_tokens":
		return StopMaxOutput
	case "content_filter":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// --- failure helpers ---

// mapFragmentError maps a fragment error to a failure code.
func mapFragmentError(err error) string {
	msg := strings.ToLower(err.Error())

	if strings.Contains(msg, "connection") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "eof") {
		return FailureNetworkError
	}

	if strings.Contains(msg, "rate") ||
		strings.Contains(msg, "429") {
		return FailureRateLimited
	}

	if strings.Contains(msg, "auth") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") {
		return FailureAuthFailed
	}

	return FailureProviderError
}

// isRetryable returns true for failure codes that are transient.
func isRetryable(code string) bool {
	switch code {
	case FailureRateLimited, FailureProviderError, FailureNetworkError, FailureMalformedResponse:
		return true
	default:
		return false
	}
}
