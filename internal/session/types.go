package session

import (
	"encoding/json"
	"time"
)

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
	Seq int64  `json:"seq"`

	Kind RecordKind      `json:"kind"`
	Role string          `json:"role,omitempty"`
	Text string          `json:"text,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	CharCount int64      `json:"char_count"`
	Tokens    TokenCount `json:"tokens"`
}

type RecordKind string

const (
	RecordMessage    RecordKind = "message"
	RecordToolCall   RecordKind = "tool_call"
	RecordToolResult RecordKind = "tool_result"
	RecordSystemNote RecordKind = "system_note"
)

type CreateInput struct {
	Prompt string `json:"prompt"`
}

type ResumeInput struct {
	ID string `json:"id"`
}

type ReadInput struct {
	ID string `json:"id"`
}

type MaterializeInput struct {
	ID string `json:"id"`
}

type DeleteInput struct {
	ID string `json:"id"`
}

type MutateInput struct {
	ID             string       `json:"id,omitempty"`
	IdempotencyKey string       `json:"idempotency_key,omitempty"`
	Ops            []MutationOp `json:"ops"`
}

type MutationOpType string

const (
	MutationAppendRecord MutationOpType = "append_record"
	MutationSetMetadata  MutationOpType = "set_metadata"
	MutationSetUsage     MutationOpType = "set_usage"
)

type MutationOp struct {
	Type     MutationOpType   `json:"type"`
	Record   *SessionRecord   `json:"record,omitempty"`
	Metadata *SessionMetadata `json:"metadata,omitempty"`
	Usage    *SessionUsage    `json:"usage,omitempty"`
}

type ContinuationKind string

const (
	ContinuationOrderedRecords ContinuationKind = "ordered_records"
)

type MaterializedSession struct {
	SessionID string           `json:"session_id"`
	Version   int64            `json:"version"`
	State     SessionState     `json:"state"`
	Kind      ContinuationKind `json:"kind"`

	Metadata SessionMetadata `json:"metadata"`
	Usage    SessionUsage    `json:"usage"`
	Records  []SessionRecord `json:"records"`
}
