package contextrenderer

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// hex16 matches a 16-character lowercase hex string.
var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

func strPtr(s string) *string { return &s }

func slotMeta(slot string) map[string]any {
	return map[string]any{contextprovider.MetadataKeySlot: slot}
}

// rec builds a SessionRecord with the fields render reads.
func rec(kind session.RecordKind, role string, text *string, callID string, calls []session.ToolCall) session.SessionRecord {
	return session.SessionRecord{
		ID:        "r",
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

func sampleConfig() Config {
	return Config{
		Material: []MaterialSection{
			{Name: "identity", Content: "You are Frank."},
			{Name: "instructions", Content: "Follow the contract."},
		},
	}
}

func sampleCatalog() *toolinvocation.ToolCatalog {
	return &toolinvocation.ToolCatalog{
		ID: "catalog-1",
		Tools: []toolinvocation.ToolDefinition{
			{ID: "t1", Name: "read_file", Description: "Read a file."},
			{ID: "t2", Name: "run_shell", Description: "Run a shell command."},
		},
	}
}

func sampleDynamic() *contextprovider.ContextResponse {
	return &contextprovider.ContextResponse{
		RequestID: "ctx-1",
		Candidates: []contextprovider.ContextCandidate{
			{ID: "abc123", Content: "Remember the plan."},
			{ID: "def456", Content: "Also this."},
		},
	}
}

func TestRenderInvalidRequest(t *testing.T) {
	service := NewService()

	valid := RenderRequest{
		ID:             "req-1",
		Transcript:     []session.SessionRecord{userText("hello")},
		DynamicContext: &contextprovider.ContextResponse{},
		Config:         &Config{},
	}

	cases := []struct {
		name    string
		mutate  func(*RenderRequest)
		wantMsg string
	}{
		{
			name:    "missing id",
			mutate:  func(r *RenderRequest) { r.ID = "  " },
			wantMsg: "id is required",
		},
		{
			name:    "empty transcript",
			mutate:  func(r *RenderRequest) { r.Transcript = nil },
			wantMsg: "transcript must not be empty",
		},
		{
			name:    "nil dynamic context",
			mutate:  func(r *RenderRequest) { r.DynamicContext = nil },
			wantMsg: "dynamic_context is required",
		},
		{
			name:    "nil config",
			mutate:  func(r *RenderRequest) { r.Config = nil },
			wantMsg: "config is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			_, err := service.Render(req)
			if err == nil {
				t.Fatal("Render() error = nil, want invalid_request")
			}
			var fail ContextRendererFailure
			if !errors.As(err, &fail) {
				t.Fatalf("Render() error %v does not wrap ContextRendererFailure", err)
			}
			if fail.Code != FailureInvalidRequest {
				t.Errorf("failure code = %q, want %q", fail.Code, FailureInvalidRequest)
			}
			if fail.Message != tc.wantMsg {
				t.Errorf("failure message = %q, want %q", fail.Message, tc.wantMsg)
			}
			if fail.Retryable {
				t.Errorf("failure %q should not be retryable", fail.Code)
			}
		})
	}
}

func TestRenderSystemPrompt(t *testing.T) {
	service := NewService()

	t.Run("opener only", func(t *testing.T) {
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("hello")},
			DynamicContext: &contextprovider.ContextResponse{},
			Config:         &Config{},
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		if got.Input.System != "You are a helpful assistant." {
			t.Errorf("System = %q, want the opener alone", got.Input.System)
		}
	})

	t.Run("material sections and tools render deterministically", func(t *testing.T) {
		cfg := sampleConfig()
		cfg.Tools = sampleCatalog()
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("hello")},
			DynamicContext: &contextprovider.ContextResponse{},
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		want := "You are a helpful assistant.\n\n" +
			"<identity>You are Frank.</identity>\n" +
			"\n<instructions>Follow the contract.</instructions>\n" +
			"\n<available_tools>\n" +
			"- read_file: Read a file.\n" +
			"- run_shell: Run a shell command.\n" +
			"</available_tools>"
		if got.Input.System != want {
			t.Errorf("System mismatch:\n got: %q\nwant: %q", got.Input.System, want)
		}
	})

	t.Run("no tools renders no available_tools block", func(t *testing.T) {
		cfg := sampleConfig()
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("hello")},
			DynamicContext: &contextprovider.ContextResponse{},
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		if strings.Contains(got.Input.System, "available_tools") {
			t.Errorf("System = %q, must not contain available_tools", got.Input.System)
		}
		want := "You are a helpful assistant.\n\n" +
			"<identity>You are Frank.</identity>\n" +
			"\n<instructions>Follow the contract.</instructions>"
		if got.Input.System != want {
			t.Errorf("System mismatch:\n got: %q\nwant: %q", got.Input.System, want)
		}
	})
}

func TestRenderSystemPromptID(t *testing.T) {
	service := NewService()

	render := func(cfg Config) RenderResult {
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("hello")},
			DynamicContext: &contextprovider.ContextResponse{},
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		return got
	}

	base := render(sampleConfig())
	if !hex16.MatchString(base.SystemPromptID) {
		t.Errorf("SystemPromptID = %q, want 16 hex characters", base.SystemPromptID)
	}

	// Identical config → identical prompt and ID.
	again := render(sampleConfig())
	if again.Input.System != base.Input.System || again.SystemPromptID != base.SystemPromptID {
		t.Error("identical config produced a different prompt or ID")
	}

	// Changed material → different ID.
	changedMaterial := sampleConfig()
	changedMaterial.Material = append(changedMaterial.Material, MaterialSection{Name: "memory", Content: "Remember me."})
	if render(changedMaterial).SystemPromptID == base.SystemPromptID {
		t.Error("changed material produced the same SystemPromptID")
	}

	// Changed tools → different ID.
	changedTools := sampleConfig()
	changedTools.Tools = sampleCatalog()
	if render(changedTools).SystemPromptID == base.SystemPromptID {
		t.Error("changed tools produced the same SystemPromptID")
	}

	// The ID is a function of config alone, not transcript or dynamic context.
	withDynamic := RenderRequest{
		ID:             "req-1",
		Transcript:     []session.SessionRecord{userText("different turn")},
		DynamicContext: sampleDynamic(),
		Config:         func() *Config { c := sampleConfig(); return &c }(),
	}
	got, err := service.Render(withDynamic)
	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if got.SystemPromptID != base.SystemPromptID {
		t.Error("SystemPromptID varied with transcript/dynamic context")
	}
}

func TestRenderCandidateInjection(t *testing.T) {
	service := NewService()

	t.Run("candidates append to last user message in provider order", func(t *testing.T) {
		cfg := Config{}
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("first"), assistantText("ok"), userText("What next?")},
			DynamicContext: sampleDynamic(),
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		wantMsgs := []modelinvocation.ModelMessage{
			{Role: modelinvocation.RoleUser, Content: "first"},
			{Role: modelinvocation.RoleAssistant, Content: "ok"},
			{Role: modelinvocation.RoleUser, Content: "What next?\n" +
				"<context>\n" +
				"<candidate id=\"abc123\">Remember the plan.</candidate>\n" +
				"<candidate id=\"def456\">Also this.</candidate>\n" +
				"</context>"},
		}
		if !reflect.DeepEqual(got.Input.Messages, wantMsgs) {
			t.Errorf("Messages mismatch:\n got: %+v\nwant: %+v", got.Input.Messages, wantMsgs)
		}
	})

	t.Run("empty candidate list injects nothing", func(t *testing.T) {
		cfg := Config{}
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     []session.SessionRecord{userText("hello")},
			DynamicContext: &contextprovider.ContextResponse{Candidates: []contextprovider.ContextCandidate{}},
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		if len(got.Input.Messages) != 1 || got.Input.Messages[0].Content != "hello" {
			t.Errorf("Messages = %+v, want a single untouched user message", got.Input.Messages)
		}
	})

	t.Run("no user message injects nothing", func(t *testing.T) {
		cfg := Config{}
		got, err := service.Render(RenderRequest{
			ID: "req-1",
			Transcript: []session.SessionRecord{
				rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "ls"}}),
				rec(session.RecordToolResult, "", strPtr("out"), "call-1", nil),
			},
			DynamicContext: sampleDynamic(),
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		wantMsgs := []modelinvocation.ModelMessage{
			{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "ls"}}},
			{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "out"},
		}
		if !reflect.DeepEqual(got.Input.Messages, wantMsgs) {
			t.Errorf("Messages mismatch:\n got: %+v\nwant: %+v", got.Input.Messages, wantMsgs)
		}
	})

	t.Run("input transcript and response are not mutated", func(t *testing.T) {
		cfg := Config{}
		transcript := []session.SessionRecord{userText("hello")}
		originalTranscript := []session.SessionRecord{userText("hello")}
		dynamic := sampleDynamic()
		originalCandidates := append([]contextprovider.ContextCandidate(nil), dynamic.Candidates...)

		_, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     transcript,
			DynamicContext: dynamic,
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		if !reflect.DeepEqual(transcript, originalTranscript) {
			t.Error("Render mutated the input transcript")
		}
		if !reflect.DeepEqual(dynamic.Candidates, originalCandidates) {
			t.Error("Render mutated the dynamic response")
		}
	})
}

func TestRenderNormalization(t *testing.T) {
	service := NewService()

	render := func(transcript []session.SessionRecord) []modelinvocation.ModelMessage {
		cfg := Config{}
		got, err := service.Render(RenderRequest{
			ID:             "req-1",
			Transcript:     transcript,
			DynamicContext: &contextprovider.ContextResponse{},
			Config:         &cfg,
		})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		return got.Input.Messages
	}

	t.Run("system note dropped", func(t *testing.T) {
		msgs := render([]session.SessionRecord{rec(session.RecordSystemNote, "", nil, "", nil)})
		if len(msgs) != 0 {
			t.Errorf("Messages = %+v, want empty", msgs)
		}
	})

	t.Run("obsolete role=tool message dropped", func(t *testing.T) {
		msgs := render([]session.SessionRecord{rec(session.RecordMessage, string(modelinvocation.RoleTool), strPtr("out"), "call-1", nil)})
		if len(msgs) != 0 {
			t.Errorf("Messages = %+v, want empty", msgs)
		}
	})

	t.Run("missing tool result synthesized", func(t *testing.T) {
		msgs := render([]session.SessionRecord{
			rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "read_file"}}),
		})
		want := []modelinvocation.ModelMessage{
			{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "read_file"}}},
			{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "Tool result not available."},
		}
		if !reflect.DeepEqual(msgs, want) {
			t.Errorf("Messages mismatch:\n got: %+v\nwant: %+v", msgs, want)
		}
	})

	t.Run("orphaned tool result dropped", func(t *testing.T) {
		msgs := render([]session.SessionRecord{rec(session.RecordToolResult, "", strPtr("result"), "call-x", nil)})
		if len(msgs) != 0 {
			t.Errorf("Messages = %+v, want empty", msgs)
		}
	})

	t.Run("clean tool call and result normalize", func(t *testing.T) {
		msgs := render([]session.SessionRecord{
			rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "ls"}}),
			rec(session.RecordToolResult, "", strPtr("out"), "call-1", nil),
		})
		want := []modelinvocation.ModelMessage{
			{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{ID: "call-1", Name: "ls"}}},
			{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "out"},
		}
		if !reflect.DeepEqual(msgs, want) {
			t.Errorf("Messages mismatch:\n got: %+v\nwant: %+v", msgs, want)
		}
	})
}

func TestInfo(t *testing.T) {
	info := Info()
	if info.Capability != "context_renderer" {
		t.Errorf("Capability = %q, want context_renderer", info.Capability)
	}
	if info.ContractVersion != "context_renderer.v0.3" {
		t.Errorf("ContractVersion = %q, want context_renderer.v0.3", info.ContractVersion)
	}
}
