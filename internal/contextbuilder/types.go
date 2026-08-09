package contextbuilder

import (
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const (
	CapabilityName  = "context_builder"
	ContractVersion = "context_builder.v0"
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

// EstimateRequest asks the builder to divide the model's context window
// among the session-scoped inputs.
type EstimateRequest struct {
	ID                  string         `json:"id"`
	Model               string         `json:"model"`
	ContextWindowTokens int            `json:"context_window_tokens"`
	Stub                TranscriptStub `json:"stub"`
}

// TranscriptStub is a lightweight transcript summary the builder uses to
// reserve space for the messages Session will materialize.
type TranscriptStub struct {
	MessageCount    int `json:"message_count"`
	EstimatedTokens int `json:"estimated_tokens"`
}

// Allocation is the token budget division for one context window.
type Allocation struct {
	RequestID           string `json:"request_id"`
	SystemPromptTokens  int    `json:"system_prompt_tokens"`
	MaxToolsTokens      int    `json:"max_tools_tokens"`
	MaxContextTokens    int    `json:"max_context_tokens"`
	MaxTranscriptTokens int    `json:"max_transcript_tokens"`
	OutputReservation   int    `json:"output_reservation"`
}

// AssembleRequest asks the builder to assemble the system prompt from
// context bundles and the tool catalog.
type AssembleRequest struct {
	ID             string                          `json:"id"`
	SessionID      string                          `json:"session_id,omitempty"`
	Model          string                          `json:"model"`
	ContextBundles []contextprovider.ContextBundle `json:"context_bundles"`
	Catalog        *toolinvocation.ToolCatalog     `json:"catalog,omitempty"`
}

// BuiltPrefix is the assembled system prompt, echoed verbatim into
// ModelInput.system.
type BuiltPrefix struct {
	RequestID      string `json:"request_id"`
	SystemPrompt   string `json:"system_prompt"`
	SystemPromptID string `json:"system_prompt_id"`
}

// PrepareRequest asks the builder to normalize the transcript and inject
// per-call context into a ModelInput ready for Model Invocation.
type PrepareRequest struct {
	ID             string                          `json:"id"`
	SessionID      string                          `json:"session_id,omitempty"`
	TurnID         string                          `json:"turn_id,omitempty"`
	Prefix         BuiltPrefix                     `json:"prefix"`
	Transcript     []session.SessionRecord         `json:"transcript"`
	ContextBundles []contextprovider.ContextBundle `json:"context_bundles,omitempty"`
}

// BuiltContext is the assembled input and the evidence of every structural
// change the builder made to the transcript.
type BuiltContext struct {
	Input         modelinvocation.ModelInput `json:"input"`
	Normalization []NormalizationNote        `json:"normalization"`
}

// NormalizationNote records one transform applied to the transcript.
type NormalizationNote struct {
	TranscriptIndex int    `json:"transcript_index"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	SynthesizedText string `json:"synthesized_text,omitempty"`
}

// Action constants describe what the builder did to the transcript.
const (
	ActionDropped     = "dropped"
	ActionSynthesized = "synthesized"
	ActionTruncated   = "truncated"
	ActionMerged      = "merged"
)

// Reason constants describe why a transform was applied.
const (
	ReasonMissingToolResult   = "missing_tool_result"
	ReasonIncompleteReasoning = "incomplete_reasoning"
	ReasonMidStreamAbort      = "mid_stream_abort"
	ReasonOrphanedToolResult  = "orphaned_tool_result"
	ReasonRoleAlternation     = "role_alternation"
	ReasonEmptyTurn           = "empty_turn"
)

// ContextBuilderFailure is the terminal payload when an action fails.
type ContextBuilderFailure struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Failure codes.
const (
	FailureInvalidRequest     = "invalid_request"
	FailureTemplateError      = "template_error"
	FailureNormalizationError = "normalization_error"
	FailureInternalError      = "internal_error"
)
