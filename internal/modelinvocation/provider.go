package modelinvocation

import (
	"context"

	"frankenstein/internal/toolinvocation"
)

// Fragment is a piece of a streaming model response. The adapter pushes
// fragments onto a channel as it parses the SSE stream; the service
// accumulates them into a ModelInvocationResult.
//
// A nil Fragment (channel close) signals the stream ended cleanly.
// A Fragment with a non-empty FinishReason is the terminal fragment
// and carries the final Usage payload.
// A Fragment with a non-nil Error is a mid-stream failure; the adapter
// closes the channel after emitting it.
type Fragment struct {
	DeltaContent   string
	DeltaReasoning string
	ToolCallDeltas []ToolCallDelta
	FinishReason   string     // "" until the terminal fragment
	Usage          *CallUsage  // nil until the terminal fragment
	Error          error      // non-nil when a transport or provider error occurs mid-stream
}

// ToolCallDelta is a piece of a tool call that arrives over the stream.
// The service concatenates deltas with the same Index to reconstruct
// the full ToolCall.
type ToolCallDelta struct {
	Index     int    // which tool call index this delta belongs to
	ID        string // tool call ID (may arrive before or alongside name/arguments)
	Name      string // tool name fragment
	Arguments string // JSON argument fragment
}

// ProviderRequest is the assembled provider-agnostic request sent to the
// adapter. The service builds this from ModelInput, ToolCatalog, and
// provider configuration. Provider-specific quirks (header names, wire
// format, system prompt placement) live inside the adapter.
type ProviderRequest struct {
	Model     string
	Messages  []ModelMessage
	System    string                    // system prompt; adapter decides wire placement
	Catalog   *toolinvocation.ToolCatalog // nil for tool-less calls
	MaxTokens int                       // 0 means provider default
	APIKey    string
	BaseURL   string
}

// ProviderAdapter is the interface every provider adapter must satisfy.
//
// Invoke opens a streaming HTTP connection to the provider, parses SSE
// events, and pushes Fragment values onto the returned channel. The
// channel is closed when the stream ends or an error occurs. Context
// cancellation must close the underlying HTTP response body.
//
// If a transport or provider error occurs mid-stream, the adapter emits
// one Fragment with Error set, then closes the channel.
type ProviderAdapter interface {
	Invoke(ctx context.Context, req ProviderRequest) (<-chan Fragment, error)
}
