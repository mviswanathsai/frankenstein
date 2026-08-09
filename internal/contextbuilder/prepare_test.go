package contextbuilder

import (
	"encoding/json"
	"testing"
	"time"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
)

func textPtr(s string) *string {
	return &s
}

func makeRecord(kind session.RecordKind, role string, text string, toolCalls []session.ToolCall, callID string) session.SessionRecord {
	return session.SessionRecord{
		ID:        "r-" + string(kind),
		Seq:       1,
		Kind:      kind,
		Role:      role,
		Text:      textPtr(text),
		ToolCalls: toolCalls,
		CallID:    callID,
		CreatedAt: time.Now(),
	}
}

func makeBundle(slot contextprovider.ContextSlot, candidates ...contextprovider.ContextCandidate) contextprovider.ContextBundle {
	return contextprovider.ContextBundle{
		RequestID:  "bundle-req",
		ProviderID: "test-provider",
		Retained:   contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{}},
		PerCall: contextprovider.ContextCollection{
			Buckets: contextprovider.ContextBuckets{
				slot: candidates,
			},
		},
	}
}

func TestPrepare(t *testing.T) {
	s := NewService()

	t.Run("simple user message", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-1",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-1",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hello", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Input.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result.Input.Messages))
		}
		msg := result.Input.Messages[0]
		if msg.Role != modelinvocation.RoleUser {
			t.Errorf("expected role user, got %s", msg.Role)
		}
		if msg.Content != "Hello" {
			t.Errorf("expected content 'Hello', got %q", msg.Content)
		}
		if len(result.Normalization) != 0 {
			t.Errorf("expected 0 notes, got %d", len(result.Normalization))
		}
	})

	t.Run("assistant message with text", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-2",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-2",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "assistant", "Sure!", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if msg.Role != modelinvocation.RoleAssistant {
			t.Errorf("expected role assistant, got %s", msg.Role)
		}
		if msg.Content != "Sure!" {
			t.Errorf("expected content 'Sure!', got %q", msg.Content)
		}
	})

	t.Run("assistant message with tool_calls", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-3",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-3",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				{
					ID:   "r-msg",
					Seq:  1,
					Kind: session.RecordMessage,
					Role: "assistant",
					Text: textPtr("Using tool."),
					ToolCalls: []session.ToolCall{
						{ID: "tc-1", Name: "weather", Arguments: map[string]any{"city": "NYC"}},
					},
					CreatedAt: time.Now(),
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if msg.Role != modelinvocation.RoleAssistant {
			t.Errorf("expected role assistant, got %s", msg.Role)
		}
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		if msg.ToolCalls[0].ID != "tc-1" {
			t.Errorf("expected tool call ID 'tc-1', got %q", msg.ToolCalls[0].ID)
		}
		if msg.ToolCalls[0].Name != "weather" {
			t.Errorf("expected tool name 'weather', got %q", msg.ToolCalls[0].Name)
		}
	})

	t.Run("tool call record", func(t *testing.T) {
		// Include a matching tool_result so repair does not synthesize.
		result, err := s.Prepare(PrepareRequest{
			ID: "req-4",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-4",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Go", nil, ""),
				{
					ID:   "r-tc",
					Seq:  2,
					Kind: session.RecordToolCall,
					ToolCalls: []session.ToolCall{
						{ID: "tc-2", Name: "read", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
				makeRecord(session.RecordToolResult, "", "file contents", nil, "tc-2"),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// messages: user, assistant(tool_call), tool(result)
		if len(result.Input.Messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result.Input.Messages))
		}
		msg := result.Input.Messages[1]
		if msg.Role != modelinvocation.RoleAssistant {
			t.Errorf("expected assistant role, got %s", msg.Role)
		}
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		if msg.ToolCalls[0].ID != "tc-2" {
			t.Errorf("expected tool call ID 'tc-2', got %q", msg.ToolCalls[0].ID)
		}
	})

	t.Run("tool result record", func(t *testing.T) {
		// Include a matching tool_call so the result is not orphaned.
		result, err := s.Prepare(PrepareRequest{
			ID: "req-5",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-5",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				{
					ID:   "r-tc-pair",
					Seq:  1,
					Kind: session.RecordToolCall,
					Role: "assistant",
					ToolCalls: []session.ToolCall{
						{ID: "call-abc", Name: "weather", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
				makeRecord(session.RecordToolResult, "", "tool output", nil, "call-abc"),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// messages: assistant(tool_call), tool(result)
		if len(result.Input.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(result.Input.Messages))
		}
		msg := result.Input.Messages[1]
		if msg.Role != modelinvocation.RoleTool {
			t.Errorf("expected role tool, got %s", msg.Role)
		}
		if msg.CallID != "call-abc" {
			t.Errorf("expected CallID 'call-abc', got %q", msg.CallID)
		}
		if msg.Content != "tool output" {
			t.Errorf("expected content 'tool output', got %q", msg.Content)
		}
	})

	t.Run("system note dropped", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-6",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-6",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hi", nil, ""),
				{
					ID:        "r-note",
					Seq:       2,
					Kind:      session.RecordSystemNote,
					Text:      textPtr("system note"),
					CreatedAt: time.Now(),
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Input.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result.Input.Messages))
		}
		if result.Input.Messages[0].Role != modelinvocation.RoleUser {
			t.Errorf("expected user message, got %s", result.Input.Messages[0].Role)
		}
		found := false
		for _, n := range result.Normalization {
			if n.Action == ActionDropped && n.TranscriptIndex == 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dropped note for transcript index 1, got notes: %+v", result.Normalization)
		}
	})

	t.Run("missing tool result synthesized", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-7",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-7",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Call tool", nil, ""),
				{
					ID:   "r-1",
					Seq:  2,
					Kind: session.RecordMessage,
					Role: "assistant",
					ToolCalls: []session.ToolCall{
						{ID: "tc-3", Name: "search", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Messages: user, assistant (tool call), synthesized tool result.
		if len(result.Input.Messages) < 3 {
			t.Fatalf("expected at least 3 messages, got %d", len(result.Input.Messages))
		}
		synth := result.Input.Messages[2]
		if synth.Role != modelinvocation.RoleTool {
			t.Errorf("expected tool role, got %s", synth.Role)
		}
		if synth.CallID != "tc-3" {
			t.Errorf("expected CallID 'tc-3', got %q", synth.CallID)
		}
		if synth.Content != "Tool result not available." {
			t.Errorf("expected synthesized content, got %q", synth.Content)
		}
		found := false
		for _, n := range result.Normalization {
			if n.Action == ActionSynthesized && n.Reason == ReasonMissingToolResult {
				found = true
			}
		}
		if !found {
			t.Errorf("expected synthesized/missing_tool_result note, got %+v", result.Normalization)
		}
	})

	t.Run("orphaned tool result dropped", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-8",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-8",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hi", nil, ""),
				makeRecord(session.RecordToolResult, "", "orphan result", nil, "orphan-id"),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The orphaned tool result should have been dropped.
		if len(result.Input.Messages) != 1 {
			t.Fatalf("expected 1 message (orphan dropped), got %d", len(result.Input.Messages))
		}
		if result.Input.Messages[0].Role != modelinvocation.RoleUser {
			t.Errorf("expected user message, got %s", result.Input.Messages[0].Role)
		}
		found := false
		for _, n := range result.Normalization {
			if n.Action == ActionDropped && n.Reason == ReasonOrphanedToolResult {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dropped/orphaned_tool_result note, got %+v", result.Normalization)
		}
	})

	t.Run("per-call context injection into user message", func(t *testing.T) {
		bundle := makeBundle(contextprovider.SlotMemory,
			contextprovider.ContextCandidate{ID: "cand-1", Content: "Remember this."},
		)
		result, err := s.Prepare(PrepareRequest{
			ID: "req-9",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-9",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hello", nil, ""),
			},
			ContextBundles: []contextprovider.ContextBundle{bundle},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if msg.Role != modelinvocation.RoleUser {
			t.Errorf("expected user, got %s", msg.Role)
		}
		// Content should contain both the original message and the injected block.
		if msg.Content[:5] != "Hello" {
			t.Errorf("expected content to start with 'Hello', got %q", msg.Content)
		}
		if !contains(msg.Content, `<per_call_context slot="memory"`) {
			t.Errorf("expected injected per_call_context block, got %q", msg.Content)
		}
		if !contains(msg.Content, `<candidate id="cand-1">Remember this.</candidate>`) {
			t.Errorf("expected candidate content, got %q", msg.Content)
		}
	})

	t.Run("multiple context bundles merged", func(t *testing.T) {
		b1 := makeBundle(contextprovider.SlotMemory,
			contextprovider.ContextCandidate{ID: "cand-1", Content: "Memory A."},
		)
		b2 := makeBundle(contextprovider.SlotSkills,
			contextprovider.ContextCandidate{ID: "cand-2", Content: "Skill B."},
		)
		result, err := s.Prepare(PrepareRequest{
			ID: "req-10",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-10",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Question", nil, ""),
			},
			ContextBundles: []contextprovider.ContextBundle{b1, b2},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if !contains(msg.Content, `<per_call_context slot="memory"`) {
			t.Errorf("expected memory slot, got %q", msg.Content)
		}
		if !contains(msg.Content, `<per_call_context slot="skills"`) {
			t.Errorf("expected skills slot, got %q", msg.Content)
		}
		if !contains(msg.Content, `Memory A.`) {
			t.Errorf("expected Memory A., got %q", msg.Content)
		}
		if !contains(msg.Content, `Skill B.`) {
			t.Errorf("expected Skill B., got %q", msg.Content)
		}
	})

	t.Run("no user message - context not injected", func(t *testing.T) {
		bundle := makeBundle(contextprovider.SlotMemory,
			contextprovider.ContextCandidate{ID: "cand-1", Content: "Should not appear."},
		)
		result, err := s.Prepare(PrepareRequest{
			ID: "req-11",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-11",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "assistant", "No user here", nil, ""),
			},
			ContextBundles: []contextprovider.ContextBundle{bundle},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if contains(msg.Content, "per_call_context") {
			t.Errorf("expected no context injection without user message, got %q", msg.Content)
		}
	})

	t.Run("empty transcript error", func(t *testing.T) {
		_, err := s.Prepare(PrepareRequest{
			ID: "req-12",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-12",
				SystemPrompt: "You are helpful.",
			},
			Transcript: nil,
		})
		if err == nil {
			t.Fatal("expected error for empty transcript")
		}
		cf, ok := err.(*ContextBuilderFailure)
		if !ok {
			t.Fatalf("expected ContextBuilderFailure, got %T", err)
		}
		if cf.Code != FailureInvalidRequest {
			t.Errorf("expected code %q, got %q", FailureInvalidRequest, cf.Code)
		}
	})

	t.Run("empty transcript slice error", func(t *testing.T) {
		_, err := s.Prepare(PrepareRequest{
			ID: "req-13",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-13",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{},
		})
		if err == nil {
			t.Fatal("expected error for empty transcript slice")
		}
	})

	t.Run("missing prefix error", func(t *testing.T) {
		_, err := s.Prepare(PrepareRequest{
			ID: "req-14",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-14",
				SystemPrompt: "", // empty system prompt
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hi", nil, ""),
			},
		})
		if err == nil {
			t.Fatal("expected error for missing prefix")
		}
		cf, ok := err.(*ContextBuilderFailure)
		if !ok {
			t.Fatalf("expected ContextBuilderFailure, got %T", err)
		}
		if cf.Code != FailureInvalidRequest {
			t.Errorf("expected code %q, got %q", FailureInvalidRequest, cf.Code)
		}
	})

	t.Run("prefix.system_prompt echoed verbatim", func(t *testing.T) {
		sysPrompt := "You are a strict assistant.\nDo no harm.\nBe brief."
		result, err := s.Prepare(PrepareRequest{
			ID: "req-15",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-15",
				SystemPrompt: sysPrompt,
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hi", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Input.System != sysPrompt {
			t.Errorf("expected system prompt verbatim:\n%q\ngot:\n%q", sysPrompt, result.Input.System)
		}
	})

	t.Run("tool results remain separate from user messages", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-16",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-16",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Call weather", nil, ""),
				{
					ID:   "r-assist",
					Seq:  2,
					Kind: session.RecordMessage,
					Role: "assistant",
					ToolCalls: []session.ToolCall{
						{ID: "tc-4", Name: "weather", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
				makeRecord(session.RecordToolResult, "", "Sunny, 72F", nil, "tc-4"),
				makeRecord(session.RecordMessage, "user", "Thanks", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Input.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(result.Input.Messages))
		}
		// Messages: user, assistant(tool_call), tool(result), user
		if result.Input.Messages[0].Role != modelinvocation.RoleUser {
			t.Errorf("msg 0: expected user, got %s", result.Input.Messages[0].Role)
		}
		if result.Input.Messages[1].Role != modelinvocation.RoleAssistant {
			t.Errorf("msg 1: expected assistant, got %s", result.Input.Messages[1].Role)
		}
		if result.Input.Messages[2].Role != modelinvocation.RoleTool {
			t.Errorf("msg 2: expected tool, got %s", result.Input.Messages[2].Role)
		}
		if result.Input.Messages[2].CallID != "tc-4" {
			t.Errorf("msg 2: expected CallID 'tc-4', got %q", result.Input.Messages[2].CallID)
		}
		if result.Input.Messages[3].Role != modelinvocation.RoleUser {
			t.Errorf("msg 3: expected user, got %s", result.Input.Messages[3].Role)
		}
		// Tool result content not merged into user message.
		if contains(result.Input.Messages[3].Content, "Sunny") {
			t.Errorf("user message should not contain tool result content, got %q", result.Input.Messages[3].Content)
		}
	})

	t.Run("assistant with nil text and tool calls still emitted", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-17",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-17",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Go", nil, ""),
				{
					ID:   "r-2",
					Seq:  2,
					Kind: session.RecordMessage,
					Role: "assistant",
					// Text is nil (no text)
					ToolCalls: []session.ToolCall{
						{ID: "tc-x", Name: "read", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
				makeRecord(session.RecordToolResult, "", "output", nil, "tc-x"),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// messages: user, assistant(tool_calls), tool(result)
		if len(result.Input.Messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result.Input.Messages))
		}
		msg := result.Input.Messages[1]
		if msg.Role != modelinvocation.RoleAssistant {
			t.Errorf("expected assistant, got %s", msg.Role)
		}
		if len(msg.ToolCalls) != 1 {
			t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		if msg.Content != "" {
			t.Errorf("expected empty content, got %q", msg.Content)
		}
	})

	t.Run("empty assistant turn dropped", func(t *testing.T) {
		rec := makeRecord(session.RecordMessage, "assistant", "", nil, "")
		rec.Text = nil // explicit nil to ensure it's truly empty
		result, err := s.Prepare(PrepareRequest{
			ID: "req-18",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-18",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Hi", nil, ""),
				rec,
				makeRecord(session.RecordMessage, "user", "Again", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Input.Messages) != 2 {
			t.Fatalf("expected 2 messages (empty assistant dropped), got %d", len(result.Input.Messages))
		}
		found := false
		for _, n := range result.Normalization {
			if n.TranscriptIndex == 1 && n.Action == ActionDropped && n.Reason == ReasonEmptyTurn {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dropped/empty_turn note for index 1, got %+v", result.Normalization)
		}
	})

	t.Run("tool_call record with text", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-19",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-19",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				{
					ID:   "r-tc-text",
					Seq:  1,
					Kind: session.RecordToolCall,
					Text: textPtr("Let me read that file."),
					ToolCalls: []session.ToolCall{
						{ID: "tc-text", Name: "read", Arguments: map[string]any{}},
					},
					CreatedAt: time.Now(),
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := result.Input.Messages[0]
		if msg.Content != "Let me read that file." {
			t.Errorf("expected text content, got %q", msg.Content)
		}
		if len(msg.ToolCalls) != 1 {
			t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
	})

	t.Run("tool_call to toolinvocation.ToolCall mapping", func(t *testing.T) {
		// Verify that session.ToolCall fields map correctly to toolinvocation.ToolCall.
		result, err := s.Prepare(PrepareRequest{
			ID: "req-20",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-20",
				SystemPrompt: "You are helpful.",
			},
			Transcript: []session.SessionRecord{
				{
					ID:   "r-map",
					Seq:  1,
					Kind: session.RecordToolCall,
					ToolCalls: []session.ToolCall{
						{
							ID:                 "tc-map",
							ToolID:             "tool-123",
							DefinitionRevision: "rev-A",
							Name:               "search",
							Arguments:          map[string]any{"query": "test"},
						},
					},
					CreatedAt: time.Now(),
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tc := result.Input.Messages[0].ToolCalls[0]
		if tc.ID != "tc-map" {
			t.Errorf("expected ID 'tc-map', got %q", tc.ID)
		}
		if tc.ToolID != "tool-123" {
			t.Errorf("expected ToolID 'tool-123', got %q", tc.ToolID)
		}
		if tc.DefinitionRevision != "rev-A" {
			t.Errorf("expected DefinitionRevision 'rev-A', got %q", tc.DefinitionRevision)
		}
		if tc.Name != "search" {
			t.Errorf("expected Name 'search', got %q", tc.Name)
		}
		if tc.Arguments["query"] != "test" {
			t.Errorf("expected Arguments query 'test', got %v", tc.Arguments["query"])
		}
	})

	t.Run("JSON roundtrip of BuiltContext", func(t *testing.T) {
		result, err := s.Prepare(PrepareRequest{
			ID: "req-21",
			Prefix: BuiltPrefix{
				RequestID:    "prefix-21",
				SystemPrompt: "System goes here.",
			},
			Transcript: []session.SessionRecord{
				makeRecord(session.RecordMessage, "user", "Test", nil, ""),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		var out BuiltContext
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if out.Input.System != "System goes here." {
			t.Errorf("system not preserved, got %q", out.Input.System)
		}
		if len(out.Input.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(out.Input.Messages))
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
