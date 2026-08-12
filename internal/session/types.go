package session

import (
	"encoding/json"
	"time"
)

const (
	CapabilityName  = "session"
	ContractVersion = "session.v0.3"
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

type SessionState string

const (
	SessionActive  SessionState = "active"
	SessionDeleted SessionState = "deleted"
)

type TokenCount struct {
	Value  int64            `json:"value"`
	Source TokenCountSource `json:"source"`
}

type TokenCountSource string

const (
	TokenSourceCharEstimate TokenCountSource = "char_estimate"
	TokenSourceTokenizer    TokenCountSource = "tokenizer"
	TokenSourceProvider     TokenCountSource = "provider"
)

type Session struct {
	ID      string       `json:"id"`
	Version int64        `json:"version"`
	State   SessionState `json:"state"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	Metadata SessionMetadata `json:"metadata"`
	Usage    SessionUsage    `json:"usage"`
	Records  []SessionRecord `json:"records"`
}

type SessionMetadata struct {
	Title       string `json:"title,omitempty"`
	DisplayName string `json:"display_name,omitempty"`

	CWD string `json:"cwd,omitempty"`

	ModelProvider string `json:"model_provider,omitempty"`
	Model         string `json:"model,omitempty"`

	Custom map[string]json.RawMessage `json:"custom,omitempty"`
}

type SessionUsage struct {
	CharCount int64 `json:"char_count"`

	LastPromptTokens TokenCount `json:"last_prompt_tokens"`
	LastOutputTokens int64      `json:"last_output_tokens"`

	TotalInputTokens     int64 `json:"total_input_tokens"`
	TotalOutputTokens    int64 `json:"total_output_tokens"`
	TotalReasoningTokens int64 `json:"total_reasoning_tokens"`
	CacheReadTokens      int64 `json:"cache_read_tokens"`
	CacheWriteTokens     int64 `json:"cache_write_tokens"`

	ContextWindowTokens int64   `json:"context_window_tokens"`
	LastContextUsedPct  float64 `json:"last_context_used_pct"`

	APICallCount int64 `json:"api_call_count"`
}

type SessionRecord struct {
	ID  string `json:"id"`
	Seq int64  `json:"-"`

	TurnID    string       `json:"turn_id,omitempty"`
	Refs      []ContextRef `json:"refs,omitempty"`
	ToolCalls []ToolCall   `json:"tool_calls,omitempty"`
	CallID    string       `json:"call_id,omitempty"`

	Kind RecordKind      `json:"kind"`
	Role string          `json:"role,omitempty"`
	Text *string         `json:"text,omitempty"`
	Raw  json.RawMessage `json:"-"`

	CreatedAt time.Time `json:"created_at"`

	CharCount int64      `json:"-"`
	Tokens    TokenCount `json:"-"`
}

// ToolCall mirrors toolinvocation.ToolCall field-for-field. The session
// package must not import toolinvocation (toolinvocation imports session for
// ContextRef and TokenCount); the Context Builder maps between the two shapes.
type ToolCall struct {
	ID                 string         `json:"id"`
	ToolID             string         `json:"tool_id,omitempty"`
	DefinitionRevision string         `json:"definition_revision,omitempty"`
	Name               string         `json:"name"`
	Arguments          map[string]any `json:"arguments"`
}

type ContextRef struct {
	Kind     string                     `json:"kind"`
	Target   string                     `json:"target"`
	Label    string                     `json:"label,omitempty"`
	Range    *ContextRefRange           `json:"range,omitempty"`
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
}

type ContextRefRange struct {
	Unit  string `json:"unit,omitempty"`
	Start int64  `json:"start,omitempty"`
	End   int64  `json:"end,omitempty"`
}

type RecordKind string

const (
	RecordMessage    RecordKind = "message"
	RecordToolCall   RecordKind = "tool_call"
	RecordToolResult RecordKind = "tool_result"
	RecordSystemNote RecordKind = "system_note"
)

type CreateInput struct {
	Prompt   string          `json:"prompt"`
	Refs     []ContextRef    `json:"refs,omitempty"`
	Metadata SessionMetadata `json:"metadata,omitempty"`
}

type GetInput struct {
	ID string `json:"id"`
}

type DeleteInput struct {
	ID string `json:"id"`
}

type CreateResult struct {
	ID      string       `json:"id"`
	Version int64        `json:"version"`
	State   SessionState `json:"state"`
}

type DeleteResult struct {
	ID      string       `json:"id"`
	Version int64        `json:"version"`
	State   SessionState `json:"state"`
}

type WriteMessageInput struct {
	SessionID string       `json:"session_id"`
	Text      string       `json:"text"`
	Role      string       `json:"role"`
	Refs      []ContextRef `json:"refs,omitempty"`
}

type WriteToolCallInput struct {
	SessionID string         `json:"session_id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	CallID    string         `json:"call_id"`
	ToolID    string         `json:"tool_id,omitempty"`
	Refs      []ContextRef   `json:"refs,omitempty"`
}

type WriteToolResultInput struct {
	SessionID string       `json:"session_id"`
	Text      string       `json:"text"`
	CallID    string       `json:"call_id"`
	Refs      []ContextRef `json:"refs,omitempty"`
}

type WriteSystemNoteInput struct {
	SessionID string       `json:"session_id"`
	Text      string       `json:"text"`
	Refs      []ContextRef `json:"refs,omitempty"`
}

type WriteRecordInput struct {
	SessionID string        `json:"session_id"`
	Record    SessionRecord `json:"record"`
}

type WriteResult struct {
	ID       string       `json:"id"`
	RecordID string       `json:"record_id"`
	Version  int64        `json:"version"`
	State    SessionState `json:"state"`
}

type SetMetadataInput struct {
	SessionID string          `json:"session_id"`
	Metadata  SessionMetadata `json:"metadata"`
}

type SetUsageInput struct {
	SessionID string       `json:"session_id"`
	Usage     SessionUsage `json:"usage"`
}

type SetResult struct {
	ID      string       `json:"id"`
	Version int64        `json:"version"`
	State   SessionState `json:"state"`
}
