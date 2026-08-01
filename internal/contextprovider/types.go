package contextprovider

import (
	"encoding/json"

	"frankenstein/internal/session"
)

const (
	CapabilityName    = "context_provider"
	ContractVersion   = "context_provider.v0.1"
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

type ContextSlot string

const (
	SlotIdentity            ContextSlot = "identity"
	SlotUserProfile         ContextSlot = "user_profile"
	SlotProjectInstructions ContextSlot = "project_instructions"
	SlotSessionFact         ContextSlot = "session_fact"
	SlotMemory              ContextSlot = "memory"
	SlotSkills              ContextSlot = "skills"
	SlotToolGuidance        ContextSlot = "tool_guidance"
	SlotUnknown             ContextSlot = "unknown"
)

type RuntimeFacts struct {
	CWD         string `json:"cwd,omitempty"`
	CurrentDate string `json:"current_date,omitempty"`
}

type WorkspaceRoot struct {
	Path string `json:"path"`
}

type TouchedPath struct {
	Path      string                     `json:"path"`
	Source    string                     `json:"source,omitempty"`
	Operation string                     `json:"operation,omitempty"`
	Metadata  map[string]json.RawMessage `json:"metadata,omitempty"`
}

type ContextInitializeRequest struct {
	ID             string               `json:"id"`
	SessionID      string               `json:"session_id,omitempty"`
	Runtime        RuntimeFacts         `json:"runtime"`
	WorkspaceRoots []WorkspaceRoot      `json:"workspace_roots"`
	Refs           []session.ContextRef `json:"refs,omitempty"`
}

type ContextRequest struct {
	ID               string                 `json:"id"`
	SessionID        string                 `json:"session_id,omitempty"`
	TriggeringRecord *session.SessionRecord `json:"triggering_record,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	Runtime          *RuntimeFacts          `json:"runtime,omitempty"`
	WorkspaceRoots   []WorkspaceRoot        `json:"workspace_roots"`
	TouchedPaths     []TouchedPath          `json:"touched_paths,omitempty"`
}

type ContextBundle struct {
	RequestID  string            `json:"request_id"`
	ProviderID string            `json:"provider_id"`
	Retained   ContextCollection `json:"retained"`
	PerCall    ContextCollection `json:"per_call"`
	Failures   []string          `json:"failures"`
}

type ContextCollection struct {
	Buckets    ContextBuckets     `json:"buckets"`
	Referenced []ContextCandidate `json:"referenced"`
}

type ContextBuckets map[ContextSlot][]ContextCandidate

type ContextCandidate struct {
	ID      string               `json:"id"`
	Content string               `json:"content"`
	Refs    []session.ContextRef `json:"refs,omitempty"`
}

type ContextFailure struct {
	RequestID  string `json:"request_id,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message,omitempty"`
	Retryable  *bool  `json:"retryable,omitempty"`
}

func emptyBundle(requestID, providerID string) *ContextBundle {
	return &ContextBundle{
		RequestID:  requestID,
		ProviderID: providerID,
		Retained:   emptyCollection(),
		PerCall:    emptyCollection(),
		Failures:   []string{},
	}
}

func emptyCollection() ContextCollection {
	return ContextCollection{
		Buckets:    ContextBuckets{},
		Referenced: []ContextCandidate{},
	}
}
