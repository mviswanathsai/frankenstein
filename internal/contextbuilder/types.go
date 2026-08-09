package contextbuilder

import (
	"fmt"

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

// ---- estimate ----

type EstimateRequest struct {
	ID                  string         `json:"id"`
	Model               string         `json:"model"`
	ContextWindowTokens int            `json:"context_window_tokens"`
	Stub                TranscriptStub `json:"stub"`
}

type TranscriptStub struct {
	MessageCount    int `json:"message_count"`
	EstimatedTokens int `json:"estimated_tokens"`
}

type Allocation struct {
	RequestID            string `json:"request_id"`
	SystemPromptTokens   int    `json:"system_prompt_tokens"`
	MaxToolsTokens       int    `json:"max_tools_tokens"`
	MaxContextTokens     int    `json:"max_context_tokens"`
	MaxTranscriptTokens  int    `json:"max_transcript_tokens"`
	OutputReservation    int    `json:"output_reservation"`
}

// ---- assemble ----

type AssembleRequest struct {
	ID             string                          `json:"id"`
	SessionID      string                          `json:"session_id,omitempty"`
	Model          string                          `json:"model"`
	ContextBundles []contextprovider.ContextBundle `json:"context_bundles"`
	Catalog        *toolinvocation.ToolCatalog     `json:"catalog,omitempty"`
}

type BuiltPrefix struct {
	RequestID      string `json:"request_id"`
	SystemPrompt   string `json:"system_prompt"`
	SystemPromptID string `json:"system_prompt_id"`
}

// ---- prepare ----

type PrepareRequest struct {
	ID             string                          `json:"id"`
	SessionID      string                          `json:"session_id,omitempty"`
	TurnID         string                          `json:"turn_id,omitempty"`
	Prefix         BuiltPrefix                     `json:"prefix"`
	Transcript     []session.SessionRecord         `json:"transcript"`
	ContextBundles []contextprovider.ContextBundle `json:"context_bundles,omitempty"`
}

type BuiltContext struct {
	Input         modelinvocation.ModelInput `json:"input"`
	Normalization []NormalizationNote        `json:"normalization"`
}

// ---- normalization notes ----

type NormalizationNote struct {
	TranscriptIndex int    `json:"transcript_index"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	SynthesizedText string `json:"synthesized_text,omitempty"`
}

// Action constants for NormalizationNote.
const (
	ActionDropped     = "dropped"
	ActionSynthesized = "synthesized"
	ActionTruncated   = "truncated"
	ActionMerged      = "merged"
)

// Reason constants for NormalizationNote.
const (
	ReasonMissingToolResult   = "missing_tool_result"
	ReasonIncompleteReasoning = "incomplete_reasoning"
	ReasonMidStreamAbort      = "mid_stream_abort"
	ReasonOrphanedToolResult  = "orphaned_tool_result"
	ReasonRoleAlternation     = "role_alternation"
	ReasonEmptyTurn           = "empty_turn"
)

// ---- failure ----

type ContextBuilderFailure struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Error implements the error interface.
func (f *ContextBuilderFailure) Error() string {
	if f.Message != "" {
		return fmt.Sprintf("[%s] %s: %s", f.Code, f.RequestID, f.Message)
	}
	return fmt.Sprintf("[%s] %s", f.Code, f.RequestID)
}

// Failure codes.
const (
	FailureInvalidRequest     = "invalid_request"
	FailureTemplateError      = "template_error"
	FailureNormalizationError = "normalization_error"
	FailureInternalError      = "internal_error"
)
