package contextrenderer

import (
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const (
	CapabilityName  = "context_renderer"
	ContractVersion = "context_renderer.v0.3"
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

// RenderRequest asks the renderer to produce a per-turn ModelInput from the
// session transcript, the provider's current dynamic context response, and the
// session-scoped config.
//
// Transcript is required and non-empty. DynamicContext and Config are
// pointers so the implementation can distinguish "missing" from "zero": nil is
// invalid_request, while an empty candidate list and a zero config are valid.
type RenderRequest struct {
	ID             string                           `json:"id"`
	SessionID      string                           `json:"session_id,omitempty"`
	Transcript     []session.SessionRecord          `json:"transcript"`
	DynamicContext *contextprovider.ContextResponse `json:"dynamic_context"`
	Config         *Config                          `json:"config"`
}

// Config is the session-scoped material slot the caller holds and supplies to
// every render call. Its shape is pairing policy between the kernel and the
// reference renderer, not contract surface.
type Config struct {
	Material []MaterialSection           `json:"material"`
	Tools    *toolinvocation.ToolCatalog `json:"tools,omitempty"`
	Model    string                      `json:"model,omitempty"`
}

// MaterialSection is one stable-material section: a name and its content,
// rendered as a <name>content</name> block in the system prompt in config
// order.
type MaterialSection struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// RenderResult is the successful output of render: the assembled ModelInput
// and the content-derived ID of its system prompt.
type RenderResult struct {
	RequestID      string                     `json:"request_id"`
	Input          modelinvocation.ModelInput `json:"input"`
	SystemPromptID string                     `json:"system_prompt_id"`
}

// ContextRendererFailure is the terminal payload when render fails.
type ContextRendererFailure struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Failure codes.
const (
	FailureInvalidRequest = "invalid_request"
	FailureTemplateError  = "template_error"
	FailureInternalError  = "internal_error"
)
