package contextbuilder

import (
	"errors"
	"reflect"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

func strPtr(s string) *string { return &s }

// rec builds a SessionRecord with the fields Prepare reads.
func rec(kind session.RecordKind, role string, text *string, callID string, calls []session.ToolCall) session.SessionRecord {
	return session.SessionRecord{
		ID:        "r",
		Seq:       0,
		Kind:      kind,
		Role:      role,
		Text:      text,
		CallID:    callID,
		ToolCalls: calls,
	}
}

func userText(s string) session.SessionRecord {
	return rec(session.RecordMessage, string(modelinvocation.RoleUser), strPtr(s), "", nil)
}

func assistantText(s string) session.SessionRecord {
	return rec(session.RecordMessage, string(modelinvocation.RoleAssistant), strPtr(s), "", nil)
}

func pref(p string) BuiltPrefix { return BuiltPrefix{SystemPrompt: p} }

// slotMeta builds the advisory candidate metadata map carrying a slot
// convention value under contextprovider.MetadataKeySlot.
func slotMeta(slot string) map[string]any {
	return map[string]any{contextprovider.MetadataKeySlot: slot}
}

func TestPrepare(t *testing.T) {
	// A tool call exercising every mapped field, in both local and canonical
	// shapes.
	toolCall1 := session.ToolCall{
		ID:                 "call-1",
		ToolID:             "t-1",
		DefinitionRevision: "rev-3",
		Name:               "read_file",
		Arguments:          map[string]any{"path": "/a"},
	}
	mappedToolCall1 := toolinvocation.ToolCall{
		ID:                 "call-1",
		ToolID:             "t-1",
		DefinitionRevision: "rev-3",
		Name:               "read_file",
		Arguments:          map[string]any{"path": "/a"},
	}

	tests := []struct {
		name       string
		prefix     BuiltPrefix
		transcript []session.SessionRecord
		dynamic    []contextprovider.ContextResponse
		wantErr    string // expected failure message; empty means success
		wantMsgs   []modelinvocation.ModelMessage
		wantNotes  []NormalizationNote
	}{
		{
			name:       "simple user message",
			prefix:     pref("You are Frank."),
			transcript: []session.SessionRecord{userText("hello")},
			wantMsgs:   []modelinvocation.ModelMessage{{Role: modelinvocation.RoleUser, Content: "hello"}},
			wantNotes:  []NormalizationNote{},
		},
		{
			name:       "user message without text",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{rec(session.RecordMessage, string(modelinvocation.RoleUser), nil, "", nil)},
			wantMsgs:   []modelinvocation.ModelMessage{{Role: modelinvocation.RoleUser, Content: ""}},
			wantNotes:  []NormalizationNote{},
		},
		{
			name:       "assistant message with text",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{assistantText("I will check.")},
			wantMsgs:   []modelinvocation.ModelMessage{{Role: modelinvocation.RoleAssistant, Content: "I will check."}},
			wantNotes:  []NormalizationNote{},
		},
		{
			name:       "empty assistant message dropped",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{rec(session.RecordMessage, string(modelinvocation.RoleAssistant), nil, "", nil)},
			wantMsgs:   []modelinvocation.ModelMessage{},
			wantNotes:  []NormalizationNote{{TranscriptIndex: 0, Action: ActionDropped, Reason: ReasonEmptyTurn}},
		},
		{
			name:   "assistant message carries tool calls",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordMessage, string(modelinvocation.RoleAssistant), strPtr("checking"), "", []session.ToolCall{{ID: "call-1", Name: "ls"}}),
				rec(session.RecordMessage, string(modelinvocation.RoleTool), strPtr("out"), "call-1", nil),
			},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, Content: "checking", ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "ls"}}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "out"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:   "tool call record",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", nil, "", []session.ToolCall{toolCall1}),
				rec(session.RecordToolResult, "", strPtr("file contents"), "call-1", nil),
			},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{mappedToolCall1}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "file contents"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:   "tool call record carries text",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", strPtr("let me check"), "", []session.ToolCall{{ID: "call-1", Name: "read_file"}}),
				rec(session.RecordToolResult, "", strPtr("contents"), "call-1", nil),
			},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, Content: "let me check", ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "read_file"}}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "contents"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:   "tool result record",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "ls"}}),
				rec(session.RecordToolResult, "", strPtr("out"), "call-1", nil),
			},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "ls"}}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "out"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:       "system note dropped",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{rec(session.RecordSystemNote, "", nil, "", nil)},
			wantMsgs:   []modelinvocation.ModelMessage{},
			wantNotes:  []NormalizationNote{{TranscriptIndex: 0, Action: ActionDropped, Reason: ReasonEmptyTurn}},
		},
		{
			name:   "missing tool result synthesized",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "read_file"}}),
			},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "read_file"}}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "Tool result not available."},
			},
			wantNotes: []NormalizationNote{{
				TranscriptIndex: -1,
				Action:          ActionSynthesized,
				Reason:          ReasonMissingToolResult,
				SynthesizedText: "Tool result not available.",
			}},
		},
		{
			name:       "orphaned tool result dropped",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{rec(session.RecordToolResult, "", strPtr("result"), "call-x", nil)},
			wantMsgs:   []modelinvocation.ModelMessage{},
			wantNotes:  []NormalizationNote{{TranscriptIndex: 0, Action: ActionDropped, Reason: ReasonOrphanedToolResult}},
		},
		{
			name:       "per call context injected into user message",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{userText("What should I do next?")},
			dynamic: []contextprovider.ContextResponse{{
				RequestID: "bundle-1",
				Candidates: []contextprovider.ContextCandidate{
					{ID: "abc123", Metadata: slotMeta(contextprovider.SlotMemory), Content: "Remember the plan."},
				},
			}},
			wantMsgs: []modelinvocation.ModelMessage{{
				Role: modelinvocation.RoleUser,
				Content: "What should I do next?\n" +
					"<per_call_context slot=\"memory\">\n" +
					"<candidate id=\"abc123\">Remember the plan.</candidate>\n" +
					"</per_call_context>",
			}},
			wantNotes: []NormalizationNote{},
		},
		{
			name:       "multiple dynamic responses merged",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{userText("Go.")},
			dynamic: []contextprovider.ContextResponse{
				{
					RequestID: "bundle-1",
					Candidates: []contextprovider.ContextCandidate{
						{ID: "m1", Metadata: slotMeta(contextprovider.SlotMemory), Content: "fact one"},
					},
				},
				{
					RequestID: "bundle-2",
					Candidates: []contextprovider.ContextCandidate{
						{ID: "m2", Metadata: slotMeta(contextprovider.SlotMemory), Content: "fact two"},
						{ID: "s1", Metadata: slotMeta(contextprovider.SlotSkills), Content: "skill one"},
					},
				},
			},
			wantMsgs: []modelinvocation.ModelMessage{{
				Role: modelinvocation.RoleUser,
				Content: "Go.\n" +
					"<per_call_context slot=\"memory\">\n" +
					"<candidate id=\"m1\">fact one</candidate>\n" +
					"</per_call_context>\n" +
					"<per_call_context slot=\"memory\">\n" +
					"<candidate id=\"m2\">fact two</candidate>\n" +
					"</per_call_context>\n" +
					"<per_call_context slot=\"skills\">\n" +
					"<candidate id=\"s1\">skill one</candidate>\n" +
					"</per_call_context>",
			}},
			wantNotes: []NormalizationNote{},
		},
		{
			name:       "context injected into last user message",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{userText("first"), assistantText("ok"), userText("last")},
			dynamic: []contextprovider.ContextResponse{{
				RequestID: "bundle-1",
				Candidates: []contextprovider.ContextCandidate{
					{ID: "m1", Metadata: slotMeta(contextprovider.SlotMemory), Content: "fact one"},
				},
			}},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "first"},
				{Role: modelinvocation.RoleAssistant, Content: "ok"},
				{Role: modelinvocation.RoleUser, Content: "last\n" +
					"<per_call_context slot=\"memory\">\n" +
					"<candidate id=\"m1\">fact one</candidate>\n" +
					"</per_call_context>"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:   "no user message skips injection",
			prefix: pref("p"),
			transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "ls"}}),
				rec(session.RecordToolResult, "", strPtr("out"), "call-1", nil),
			},
			dynamic: []contextprovider.ContextResponse{{
				RequestID: "bundle-1",
				Candidates: []contextprovider.ContextCandidate{
					{ID: "m1", Metadata: slotMeta(contextprovider.SlotMemory), Content: "fact one"},
				},
			}},
			wantMsgs: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "ls"}}},
				{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "out"},
			},
			wantNotes: []NormalizationNote{},
		},
		{
			name:       "empty transcript rejected",
			prefix:     pref("p"),
			transcript: []session.SessionRecord{},
			wantErr:    "transcript must not be empty",
		},
		{
			name:       "missing prefix rejected",
			transcript: []session.SessionRecord{userText("hello")},
			wantErr:    "prefix.system_prompt is required",
		},
		{
			name:       "system prompt echoed verbatim",
			prefix:     pref("You are Frank.\n<session>\n#1\nplain text\n"),
			transcript: []session.SessionRecord{userText("hello")},
			wantMsgs:   []modelinvocation.ModelMessage{{Role: modelinvocation.RoleUser, Content: "hello"}},
			wantNotes:  []NormalizationNote{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{}
			req := PrepareRequest{
				ID:         "req-prepare",
				Prefix:     tt.prefix,
				Transcript: tt.transcript,
				Dynamic:    tt.dynamic,
			}
			got, err := service.Prepare(req)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Prepare() error = nil, want invalid_request %q", tt.wantErr)
				}
				var fail ContextBuilderFailure
				if !errors.As(err, &fail) {
					t.Fatalf("Prepare() error %v does not wrap ContextBuilderFailure", err)
				}
				if fail.Code != FailureInvalidRequest {
					t.Errorf("failure code = %q, want %q", fail.Code, FailureInvalidRequest)
				}
				if fail.Message != tt.wantErr {
					t.Errorf("failure message = %q, want %q", fail.Message, tt.wantErr)
				}
				if fail.Retryable {
					t.Errorf("failure %q should not be retryable", fail.Code)
				}
				return
			}

			if err != nil {
				t.Fatalf("Prepare() error = %v, want nil", err)
			}
			if got.Input.System != req.Prefix.SystemPrompt {
				t.Errorf("Input.System = %q, want prefix.system_prompt echoed verbatim %q", got.Input.System, req.Prefix.SystemPrompt)
			}
			if !reflect.DeepEqual(got.Input.Messages, tt.wantMsgs) {
				t.Errorf("Input.Messages mismatch:\n got: %+v\nwant: %+v", got.Input.Messages, tt.wantMsgs)
			}
			if !reflect.DeepEqual(got.Normalization, tt.wantNotes) {
				t.Errorf("Normalization mismatch:\n got: %+v\nwant: %+v", got.Normalization, tt.wantNotes)
			}
		})
	}
}
