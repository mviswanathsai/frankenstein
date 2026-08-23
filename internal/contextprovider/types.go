package contextprovider

import (
	"frankenstein/internal/session"
	"frankenstein/internal/touchedpath"
)

const (
	CapabilityName    = "context_provider"
	ContractVersion   = "context_provider.v0.2"
	DefaultProviderID = "frankenstein.context-provider.go"
)

type ContractInfo struct {
	Capability      string `json:"capability"`
	ContractVersion string `json:"contract_version"`
	ProviderID      string `json:"provider_id"`
}

func Info() ContractInfo {
	return ContractInfo{
		Capability:      CapabilityName,
		ContractVersion: ContractVersion,
		ProviderID:      DefaultProviderID,
	}
}

// MetadataKeySlot is the advisory candidate-metadata key carrying a slot
// convention value. Metadata is non-normative: no behavior in this package
// or in the contract depends on any key.
const MetadataKeySlot = "slot"

// Slot convention values emitted by the reference provider. The vocabulary is
// an implementation-defined pairing convention between providers and
// renderers, not contract surface: consumers take the union of what is
// offered, and unknown values are hints rather than errors.
const (
	SlotIdentity            = "identity"
	SlotUserProfile         = "user_profile"
	SlotProjectInstructions = "project_instructions"
	SlotMemory              = "memory"
	SlotSkills              = "skills"
	SlotUnknown             = "unknown"
)

// RuntimeFacts carries small runtime facts the caller already knows. cwd is
// the caller-resolved working directory: it is only the base for resolving
// relative input paths and never grants filesystem access.
type RuntimeFacts struct {
	CWD string `json:"cwd,omitempty"`
}

// WorkspaceRoot is one absolute directory granted as a filesystem read
// boundary for a single request. An empty root list grants no filesystem
// reads beyond what is reachable under cwd.
type WorkspaceRoot struct {
	Path string `json:"path"`
}

// DynamicContextRequest is the input payload of get_dynamic_context.
//
// Transcript is an optional window of session records supplied as request
// evidence; the caller chooses the window and there is no default
// full-history pass. Refs are pre-resolved references the provider must
// account for. TouchedPaths are request-time evidence from the runtime or
// tool executor and are not session state. Reason is optional free-form
// evidence explaining why the action was invoked; providers are not required
// to branch on it, and unknown reasons must be accepted.
type DynamicContextRequest struct {
	ID             string                    `json:"id"`
	SessionID      string                    `json:"session_id,omitempty"`
	Transcript     []session.SessionRecord   `json:"transcript,omitempty"`
	Refs           []session.ContextRef      `json:"refs,omitempty"`
	TouchedPaths   []touchedpath.TouchedPath `json:"touched_paths,omitempty"`
	Reason         string                    `json:"reason,omitempty"`
	Runtime        *RuntimeFacts             `json:"runtime,omitempty"`
	WorkspaceRoots []WorkspaceRoot           `json:"workspace_roots"`
}

// StableContextRequest is the input payload of get_stable_context. It is the
// dynamic-request floor minus transcript, refs, touched paths, and reason:
// stable material is what is discoverable at session start from the granted
// boundary alone.
type StableContextRequest struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id,omitempty"`
	Runtime        *RuntimeFacts   `json:"runtime,omitempty"`
	WorkspaceRoots []WorkspaceRoot `json:"workspace_roots"`
}

// ContextResponse is the shared successful output payload of both actions.
// Candidates is an ordered list — ordering communicates the provider's
// relative preference across the whole list — and may be empty. Failures is
// a plain-text accounting of input refs the provider did not dereference,
// in input order, and may be empty. Both slices are non-nil.
type ContextResponse struct {
	RequestID  string             `json:"request_id"`
	Candidates []ContextCandidate `json:"candidates"`
	Failures   []string           `json:"failures"`
}

// ContextCandidate is one piece of provider-prepared content offered for
// renderer consideration. Content is required and non-empty. Refs identify
// source material and never substitute for content. Metadata is the advisory
// extension seam; no contract guarantee depends on it.
type ContextCandidate struct {
	ID       string               `json:"id"`
	Metadata map[string]any       `json:"metadata,omitempty"`
	Content  string               `json:"content"`
	Refs     []session.ContextRef `json:"refs,omitempty"`
}

// ContextFailure is the terminal failure output payload of both actions.
// Retryable follows read-style semantics: when the service cannot determine
// retryability it reports true. Only deterministic request-shape and limit
// failures report false.
type ContextFailure struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

func emptyResponse(requestID string) *ContextResponse {
	return &ContextResponse{
		RequestID:  requestID,
		Candidates: []ContextCandidate{},
		Failures:   []string{},
	}
}

// slotMetadata builds the advisory metadata map carrying a slot convention
// value under MetadataKeySlot.
func slotMetadata(slot string) map[string]any {
	return map[string]any{MetadataKeySlot: slot}
}
