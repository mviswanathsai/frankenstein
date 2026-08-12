package kernel

import (
	"context"
	"strconv"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// innerLoopResult is the outcome of one model-call loop. The caller
// materializes newRecords into the session and owns the terminal turn event;
// the loop never appends to Session itself.
type innerLoopResult struct {
	exitReason   ExitReason
	finalContent string
	newRecords   []session.SessionRecord
}

// runInnerLoop executes the model-call loop for one turn: prepare, invoke,
// dispatch on stop reason, execute tools, and repeat until an exit condition
// fires or the turn budget is exhausted. Tool results are returned as
// newRecords for the caller to persist; the loop does not touch Session,
// emit the terminal turn event, or apply compression or memory policies.
func runInnerLoop(
	ctx context.Context,
	cfg Config,
	tools ToolInvoker,
	model ModelInvoker,
	builder ContextBuilder,
	observer TurnObserver,
	sessionID string,
	turnID string,
	resolvedModel string,
	prefix contextbuilder.BuiltPrefix,
	transcript []session.SessionRecord,
	activeCatalog *toolinvocation.ToolCatalog,
	outputBudget int,
	bundles []contextprovider.ContextBundle,
) innerLoopResult {
	currentBudget := outputBudget
	maxOutputRetries := 0
	var newRecords []session.SessionRecord

	for iter := 1; iter <= cfg.TurnBudget; iter++ {
		iterStr := strconv.Itoa(iter)

		// Prepare the model input from the prefix, the current transcript,
		// and any per-turn context bundles.
		builtCtx, err := builder.Prepare(contextbuilder.PrepareRequest{
			ID:             "prep_" + turnID + "_" + iterStr,
			SessionID:      sessionID,
			TurnID:         turnID,
			Prefix:         prefix,
			Transcript:     transcript,
			ContextBundles: bundles,
		})
		if err != nil {
			return innerLoopResult{exitReason: ExitInternalError, newRecords: newRecords}
		}

		// Invoke the model with the assembled input and the active catalog.
		req := modelinvocation.ModelInvocationRequest{
			ID:        "inv_" + turnID + "_" + iterStr,
			SessionID: sessionID,
			TurnID:    turnID,
			Model:     resolvedModel,
			Input:     builtCtx.Input,
			Catalog:   activeCatalog,
		}
		if currentBudget > 0 {
			req.MaxOutputTokens = &currentBudget
		}

		result, failure := invokeWithRetry(ctx, cfg, model, req)
		if failure != nil {
			return innerLoopResult{exitReason: ExitModelError, newRecords: newRecords}
		}

		// Stream the model's output as it arrives (v0 delivers it in one shot).
		if result.Content != "" && observer != nil {
			observer.OnModelContent(result.Content)
		}
		if result.Reasoning != "" && observer != nil {
			observer.OnReasoning(result.Reasoning)
		}

		// Dispatch on the stop reason.
		switch result.StopReason {
		case modelinvocation.StopEndTurn:
			if len(result.ToolCalls) == 0 {
				return innerLoopResult{
					exitReason:   ExitCompleted,
					finalContent: result.Content,
					newRecords:   newRecords,
				}
			}
		case modelinvocation.StopToolCalls:
			// Continue to the empty-response check and tool execution.
		case modelinvocation.StopMaxOutput:
			newBudget, shouldRetry := maxOutputPolicy(cfg, currentBudget, maxOutputRetries)
			if shouldRetry {
				maxOutputRetries++
				currentBudget = newBudget
				continue
			}
			return innerLoopResult{exitReason: ExitModelError, newRecords: newRecords}
		case modelinvocation.StopContentFilter:
			return innerLoopResult{exitReason: ExitModelError, newRecords: newRecords}
		}

		// Accept an empty response as a finished turn.
		if emptyResponsePolicy(result) {
			return innerLoopResult{exitReason: ExitCompleted, newRecords: newRecords}
		}

		// Nothing to execute: loop again (bounded by the turn budget).
		if len(result.ToolCalls) == 0 {
			continue
		}

		// Stream tool-call starts before execution.
		if observer != nil {
			for _, tc := range result.ToolCalls {
				observer.OnToolCallStart(tc.Name, tc.Arguments)
			}
		}

		// Execute the batch against the active catalog.
		execResult, execFailure := tools.Execute(ctx, toolinvocation.ToolExecutionRequest{
			ID:        "exec_" + turnID + "_" + iterStr,
			CatalogID: activeCatalog.ID,
			SessionID: sessionID,
			TurnID:    turnID,
			Calls:     result.ToolCalls,
		})
		if execFailure != nil {
			// Persist any partial results so they are not lost.
			newRecords = append(newRecords, buildToolResultRecords(execFailure.Results)...)
			return innerLoopResult{exitReason: ExitToolError, newRecords: newRecords}
		}

		// Stream results and append them to the running transcript.
		if observer != nil {
			for _, r := range execResult.Results {
				observer.OnToolResult(r)
			}
		}
		toolRecords := buildToolResultRecords(execResult.Results)
		transcript = append(transcript, toolRecords...)
		newRecords = append(newRecords, toolRecords...)

		if toolResultRequestsStop(execResult.Results) {
			return innerLoopResult{exitReason: ExitToolStopRequested, newRecords: newRecords}
		}

		// Adopt a catalog transition, or refresh the catalog when the
		// transition is stale. A failed refresh keeps the current catalog.
		if execResult.CatalogTransition != nil {
			if execResult.CatalogTransition.BaseCatalogID == activeCatalog.ID {
				activeCatalog = &execResult.CatalogTransition.Catalog
			} else if listed, _ := tools.ListTools(ctx, toolinvocation.ToolCatalogRequest{
				ID:        "list_" + turnID + "_" + iterStr,
				SessionID: sessionID,
				TurnID:    turnID,
			}); listed != nil {
				activeCatalog = &listed.Catalog
			}
		}
	}

	return innerLoopResult{exitReason: ExitBudgetExhausted, newRecords: newRecords}
}

// invokeWithRetry calls the model, retrying retryable failures up to
// cfg.MaxRetries times. Returns the first successful result, or the last
// failure. Retries reuse the same request: same provider, model, and input.
func invokeWithRetry(ctx context.Context, cfg Config, model ModelInvoker, req modelinvocation.ModelInvocationRequest) (*modelinvocation.ModelInvocationResult, *modelinvocation.ModelInvocationFailure) {
	var lastFailure *modelinvocation.ModelInvocationFailure
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result, failure := model.Invoke(ctx, req)
		if failure == nil {
			return result, nil
		}
		lastFailure = failure
		if !modelCallRetryable(failure) || ctx.Err() != nil {
			break
		}
	}
	return nil, lastFailure
}
