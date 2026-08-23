package toolinvocation

import (
	"encoding/json"

	"frankenstein/internal/session"
	"frankenstein/internal/touchedpath"
)

const (
	CapabilityName  = "tool_invocation"
	ContractVersion = "tool_invocation.v0"
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

type ToolCatalogRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

type ToolCatalog struct {
	ID    string           `json:"id"`
	Tools []ToolDefinition `json:"tools"`
}

type ToolCatalogListed struct {
	RequestID string      `json:"request_id"`
	Catalog   ToolCatalog `json:"catalog"`
}

type ToolCatalogFailure struct {
	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

type ToolDefinition struct {
	ID          string          `json:"id"`
	Revision    string          `json:"revision"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolExecutionMode string

const (
	ExecutionSequential    ToolExecutionMode = "sequential"
	ExecutionAllowParallel ToolExecutionMode = "allow_parallel"
)

type ToolExecutionRequest struct {
	ID             string            `json:"id"`
	IdempotencyKey string            `json:"idempotency_key"`
	CatalogID      string            `json:"catalog_id"`
	SessionID      string            `json:"session_id,omitempty"`
	TurnID         string            `json:"turn_id,omitempty"`
	Mode           ToolExecutionMode `json:"mode,omitempty"`
	Calls          []ToolCall        `json:"calls"`
}

type ToolCall struct {
	ID                 string         `json:"id"`
	ToolID             string         `json:"tool_id,omitempty"`
	DefinitionRevision string         `json:"definition_revision,omitempty"`
	Name               string         `json:"name"`
	Arguments          map[string]any `json:"arguments"`
}

type ToolExecutionResult struct {
	RequestID         string                 `json:"request_id"`
	Results           []ToolResult           `json:"results"`
	CatalogTransition *ToolCatalogTransition `json:"catalog_transition,omitempty"`
}

type ToolCatalogTransition struct {
	BaseCatalogID string      `json:"base_catalog_id"`
	Catalog       ToolCatalog `json:"catalog"`
}

type ToolExecutionFailure struct {
	RequestID         string       `json:"request_id,omitempty"`
	Code              string       `json:"code"`
	Message           string       `json:"message,omitempty"`
	Retryable         bool         `json:"retryable"`
	Results           []ToolResult `json:"results"`
	UnresolvedCallIDs []string     `json:"unresolved_call_ids"`
}

type ToolResultStatus string

const (
	ResultSucceeded ToolResultStatus = "succeeded"
	ResultFailed    ToolResultStatus = "failed"
	ResultDenied    ToolResultStatus = "denied"
	ResultCancelled ToolResultStatus = "cancelled"
	ResultTimedOut  ToolResultStatus = "timed_out"
	ResultUnknown   ToolResultStatus = "unknown"
)

type ToolSideEffect string

const (
	SideEffectNone    ToolSideEffect = "none"
	SideEffectApplied ToolSideEffect = "applied"
	SideEffectPartial ToolSideEffect = "partial"
	SideEffectUnknown ToolSideEffect = "unknown"
)

type ToolFailure struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

type ToolResult struct {
	CallID        string                    `json:"call_id"`
	ToolID        string                    `json:"tool_id,omitempty"`
	Name          string                    `json:"name"`
	Status        ToolResultStatus          `json:"status"`
	Text          string                    `json:"text"`
	Refs          []session.ContextRef      `json:"refs"`
	TouchedPaths  []touchedpath.TouchedPath `json:"touched_paths"`
	SideEffect    ToolSideEffect            `json:"side_effect"`
	StopRequested bool                      `json:"stop_requested,omitempty"`
	Failure       *ToolFailure              `json:"failure,omitempty"`
	DescribedTool *ToolDefinition           `json:"described_tool,omitempty"`
}

type ToolCallStarted struct {
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	ToolID    string `json:"tool_id"`
	Name      string `json:"name"`
}

type ToolProxyDispatchAttempted struct {
	RequestID                   string `json:"request_id"`
	CallID                      string `json:"call_id"`
	ProxyToolID                 string `json:"proxy_tool_id"`
	RequestedTargetName         string `json:"requested_target_name"`
	EffectiveToolID             string `json:"effective_tool_id,omitempty"`
	EffectiveDefinitionRevision string `json:"effective_definition_revision,omitempty"`
}

const (
	FailureUnknownTool               = "unknown_tool"
	FailureInvalidArguments          = "invalid_arguments"
	FailureCatalogUnavailable        = "catalog_unavailable"
	FailureToolUnavailable           = "tool_unavailable"
	FailureStaleToolDefinition       = "stale_tool_definition"
	FailureBackendFailed             = "backend_failed"
	FailureMalformedResult           = "malformed_result"
	FailureCancelled                 = "cancelled"
	FailureTimedOut                  = "timed_out"
	FailureOutcomeUnknown            = "outcome_unknown"
	FailureCallStartedUnacknowledged = "tool_invocation.call_started_unacknowledged"
	FailureProxyDispatchUnrecorded   = "tool_invocation.proxy_dispatch_unrecorded"

	FailureInvalidRequest        = "invalid_request"
	FailureMissingIdempotencyKey = "missing_idempotency_key"
	FailureDuplicateCallID       = "duplicate_call_id"
	FailureIdempotencyConflict   = "idempotency_conflict"
	FailureServiceUnavailable    = "service_unavailable"
	FailureInternal              = "internal_failure"
)
