package contextbuilder

import (
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

func TestPrepare(t *testing.T) {
	toolCall := session.ToolCall{ID: "call-1", ToolID: "tool-1", Name: "search", Arguments: map[string]any{"q": "go"}}
	text := func(value string) *string { return &value }

	tests := []struct {
		name       string
		transcript []session.SessionRecord
		bundles    []contextprovider.ContextBundle
		want       []modelinvocation.ModelMessage
		wantNote   string
		wantError  string
	}{
		{
			name:       "simple user message",
			transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: text("hello")}},
			want:       []modelinvocation.ModelMessage{{Role: modelinvocation.RoleUser, Content: "hello"}},
		},
		{
			name:       "assistant message with text",
			transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "assistant", Text: text("answer")}},
			want:       []modelinvocation.ModelMessage{{Role: modelinvocation.RoleAssistant, Content: "answer"}},
		},
		{
			name:       "tool call record",
			transcript: []session.SessionRecord{{Kind: session.RecordToolCall, ToolCalls: []session.ToolCall{toolCall}}},
			want:       []modelinvocation.ModelMessage{{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", ToolID: "tool-1", Name: "search", Arguments: map[string]any{"q": "go"}}}}, {Role: modelinvocation.RoleTool, CallID: "call-1", Content: missingToolResultText}},
			wantNote:   ReasonMissingToolResult,
		},
		{
			name:       "tool result record",
			transcript: []session.SessionRecord{{Kind: session.RecordToolCall, ToolCalls: []session.ToolCall{toolCall}}, {Kind: session.RecordToolResult, CallID: "call-1", Text: text("found")}},
			want:       []modelinvocation.ModelMessage{{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", ToolID: "tool-1", Name: "search", Arguments: map[string]any{"q": "go"}}}}, {Role: modelinvocation.RoleTool, CallID: "call-1", Content: "found"}},
		},
		{
			name:       "system note",
			transcript: []session.SessionRecord{{Kind: session.RecordSystemNote}},
			wantNote:   ReasonEmptyTurn,
		},
		{
			name:       "missing tool result",
			transcript: []session.SessionRecord{{Kind: session.RecordToolCall, ToolCalls: []session.ToolCall{toolCall}}},
			want:       []modelinvocation.ModelMessage{{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", ToolID: "tool-1", Name: "search", Arguments: map[string]any{"q": "go"}}}}, {Role: modelinvocation.RoleTool, CallID: "call-1", Content: missingToolResultText}},
			wantNote:   ReasonMissingToolResult,
		},
		{
			name:       "orphaned tool result",
			transcript: []session.SessionRecord{{Kind: session.RecordToolResult, CallID: "unknown", Text: text("late")}},
			wantNote:   ReasonOrphanedToolResult,
		},
		{
			name:      "empty transcript",
			wantError: FailureInvalidRequest,
		},
		{
			name:       "missing prefix",
			transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: text("hello")}},
			wantError:  FailureInvalidRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService()
			request := PrepareRequest{ID: "request-1", Prefix: BuiltPrefix{SystemPrompt: "system"}, Transcript: test.transcript, ContextBundles: test.bundles}
			if test.name == "missing prefix" {
				request.Prefix.SystemPrompt = ""
			}
			got, err := service.Prepare(request)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(test.want) > 0 && !messagesEqual(got.Input.Messages, test.want) {
				t.Fatalf("messages = %+v, want %+v", got.Input.Messages, test.want)
			}
			if test.wantNote != "" && !hasReason(got.Normalization, test.wantNote) {
				t.Fatalf("normalization = %+v, want reason %q", got.Normalization, test.wantNote)
			}
		})
	}
}

func TestPreparePerCallContext(t *testing.T) {
	text := "hello"
	request := PrepareRequest{
		Prefix:     BuiltPrefix{SystemPrompt: "system"},
		Transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: &text}},
		ContextBundles: []contextprovider.ContextBundle{
			{PerCall: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
				contextprovider.SlotMemory: {{ID: "one", Content: "first"}},
			}}},
			{PerCall: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
				contextprovider.SlotMemory: {{ID: "two", Content: "second"}},
			}}},
		},
	}

	got, err := NewService().Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	content := got.Input.Messages[0].Content
	for _, want := range []string{"<per_call_context slot=\"memory\">", "id=\"one\"", "first", "id=\"two\"", "second"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
}

func TestPreparePreservesSystemPrompt(t *testing.T) {
	text := "hello"
	want := "  system\nwith exact spacing  "
	got, err := NewService().Prepare(PrepareRequest{
		Prefix:     BuiltPrefix{SystemPrompt: want},
		Transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: &text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Input.System != want {
		t.Fatalf("system = %q, want %q", got.Input.System, want)
	}
}

func hasReason(notes []NormalizationNote, reason string) bool {
	for _, note := range notes {
		if note.Reason == reason {
			return true
		}
	}
	return false
}

func messagesEqual(got, want []modelinvocation.ModelMessage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content || got[i].CallID != want[i].CallID || len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			return false
		}
		for j := range got[i].ToolCalls {
			if got[i].ToolCalls[j].ID != want[i].ToolCalls[j].ID || got[i].ToolCalls[j].ToolID != want[i].ToolCalls[j].ToolID || got[i].ToolCalls[j].Name != want[i].ToolCalls[j].Name {
				return false
			}
		}
	}
	return true
}
