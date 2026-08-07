package modelinvocation

import (
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const (
	CapabilityName  = "model_invocation"
	ContractVersion = "model_invocation.v0"
)

type ContractInfo struct {
	Capability      string `json:"capability"`
	ContractVersion string `json:"contract_version"`
}

func Info() ContractInfo {
	return ContractInfo{
		Capability:      CapabilityName,
		ContractVersion: ContractVersion,
	}
}

// ModelInvocationRequest carries one model call.
type ModelInvocationRequest struct {
	ID              string                    `json:"id"`
	SessionID       string                    `json:"session_id,omitempty"`
	TurnID          string                    `json:"turn_id,omitempty"`
	Model           string                    `json:"model"`
	Provider        string                    `json:"provider"`
	Input           ModelInput                `json:"input"`
	Catalog         *toolinvocation.ToolCatalog `json:"catalog,omitempty"`
	MaxOutputTokens *int                      `json:"max_output_tokens,omitempty"`
}

// ModelInvocationResult is the normalized output of a successful call.
type ModelInvocationResult struct {
	RequestID          string                  `json:"request_id"`
	Content            string                  `json:"content,omitempty"`
	Reasoning          string                  `json:"reasoning,omitempty"`
	ToolCalls          []toolinvocation.ToolCall `json:"tool_calls"`
	StopReason         StopReason              `json:"stop_reason"`
	Usage              CallUsage               `json:"usage"`
	CatalogID          string                  `json:"catalog_id,omitempty"`
	Model              string                  `json:"model"`
	ProviderResponseID string                  `json:"provider_response_id,omitempty"`
	Repairs            []RepairNote            `json:"repairs,omitempty"`
}

// ModelInvocationFailure is the terminal payload when the call does not
// produce a result. request_id is required (no omitempty).
type ModelInvocationFailure struct {
	RequestID string        `json:"request_id"`
	Code      string        `json:"code"`
	Message   string        `json:"message,omitempty"`
	Retryable bool          `json:"retryable"`
	Partial   *PartialOutput `json:"partial,omitempty"`
}

// PartialOutput carries accumulated text and reasoning when a call is
// cancelled mid-stream.
type PartialOutput struct {
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// ModelInput is the assembled provider-neutral input for one call.
type ModelInput struct {
	System   string         `json:"system,omitempty"`
	Messages []ModelMessage `json:"messages"`
}

// ModelMessage is a single provider-neutral message in the conversation.
type ModelMessage struct {
	Role      ModelMessageRole          `json:"role"`
	Content   string                    `json:"content,omitempty"`
	Reasoning string                    `json:"reasoning,omitempty"`
	ToolCalls []toolinvocation.ToolCall `json:"tool_calls,omitempty"`
	CallID    string                    `json:"call_id,omitempty"`
}

// ModelMessageRole identifies who sent the message.
type ModelMessageRole string

const (
	RoleUser      ModelMessageRole = "user"
	RoleAssistant ModelMessageRole = "assistant"
	RoleTool      ModelMessageRole = "tool"
)

// CallUsage records token consumption reported by the provider.
type CallUsage struct {
	InputTokens      session.TokenCount  `json:"input_tokens"`
	OutputTokens     session.TokenCount  `json:"output_tokens"`
	CacheReadTokens  *session.TokenCount `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *session.TokenCount `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  *session.TokenCount `json:"reasoning_tokens,omitempty"`
}

// RepairNote records model-output repair evidence.
type RepairNote struct {
	CallID  string    `json:"call_id"`
	Kind    RepairKind `json:"kind"`
	RawName string    `json:"raw_name,omitempty"`
}

// RepairKind identifies what was repaired.
type RepairKind string

const (
	RepairName      RepairKind = "name"
	RepairArguments RepairKind = "arguments"
)

// StopReason describes why generation ended.
type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopToolCalls     StopReason = "tool_calls"
	StopMaxOutput     StopReason = "max_output"
	StopContentFilter StopReason = "content_filter"
)

// Failure codes.
const (
	FailureInvalidRequest       = "invalid_request"
	FailureContextOverflow      = "context_overflow"
	FailureRateLimited          = "rate_limited"
	FailureProviderError        = "provider_error"
	FailureNetworkError         = "network_error"
	FailureAuthFailed           = "auth_failed"
	FailureContentFilter        = "content_filter"
	FailureMalformedResponse    = "malformed_response"
	FailureProviderUnavailable  = "provider_unavailable"
	FailureCancelled            = "cancelled"
)
