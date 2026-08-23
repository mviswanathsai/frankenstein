package contextbuilder

import (
	"reflect"
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// TestPipelineHappyPath exercises the full estimate -> assemble -> prepare
// pipeline on a realistic transcript: stable candidates become the system
// prompt, the catalog renders tool awareness, and the transcript normalizes
// into clean model-facing messages.
func TestPipelineHappyPath(t *testing.T) {
	service := NewService()

	// Estimate: divide a 128K window.
	alloc, err := service.Estimate(EstimateRequest{
		ID:                  "req-estimate",
		Model:               "claude-sonnet-4",
		ContextWindowTokens: 131072,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v, want nil", err)
	}
	if alloc.RequestID != "req-estimate" {
		t.Errorf("allocation RequestID = %q, want %q", alloc.RequestID, "req-estimate")
	}
	if alloc.SystemPromptTokens <= 0 {
		t.Errorf("SystemPromptTokens = %d, want > 0", alloc.SystemPromptTokens)
	}

	// Assemble: stable candidates plus the tool catalog.
	candidates := []contextprovider.ContextCandidate{
		{ID: "pi-1", Metadata: slotMeta(contextprovider.SlotProjectInstructions), Content: "Follow the contract."},
		{ID: "sk-1", Metadata: slotMeta(contextprovider.SlotSkills), Content: "You know Go."},
	}
	prefix, err := service.Assemble(AssembleRequest{
		ID:               "req-assemble",
		Model:            "claude-sonnet-4",
		StableCandidates: candidates,
		Catalog:          sampleCatalog(),
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v, want nil", err)
	}
	if prefix.RequestID != "req-assemble" {
		t.Errorf("prefix RequestID = %q, want %q", prefix.RequestID, "req-assemble")
	}
	for _, want := range []string{
		"You are a helpful assistant.",
		"Follow the contract.",
		"You know Go.",
		"read_file",
		"run_shell",
	} {
		if !strings.Contains(prefix.SystemPrompt, want) {
			t.Errorf("SystemPrompt missing %q", want)
		}
	}
	if !hex16.MatchString(prefix.SystemPromptID) {
		t.Errorf("SystemPromptID = %q, want 16 hex characters", prefix.SystemPromptID)
	}

	// Prepare: a transcript with one of each record kind.
	transcript := []session.SessionRecord{
		userText("What files are in the repo?"),
		assistantText("I'll look for them."),
		rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{
			ID:        "call-1",
			ToolID:    "t1",
			Name:      "read_file",
			Arguments: map[string]any{"path": "README.md"},
		}}),
		rec(session.RecordToolResult, "", strPtr("README.md, go.mod"), "call-1", nil),
	}
	built, err := service.Prepare(PrepareRequest{
		ID:         "req-prepare",
		SessionID:  "session-1",
		TurnID:     "turn-1",
		Prefix:     prefix,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v, want nil", err)
	}

	if built.Input.System != prefix.SystemPrompt {
		t.Errorf("Input.System = %q, want prefix.system_prompt %q", built.Input.System, prefix.SystemPrompt)
	}
	wantMsgs := []modelinvocation.ModelMessage{
		{Role: modelinvocation.RoleUser, Content: "What files are in the repo?"},
		{Role: modelinvocation.RoleAssistant, Content: "I'll look for them."},
		{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{
			ID:        "call-1",
			ToolID:    "t1",
			Name:      "read_file",
			Arguments: map[string]any{"path": "README.md"},
		}}},
		{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "README.md, go.mod"},
	}
	if !reflect.DeepEqual(built.Input.Messages, wantMsgs) {
		t.Errorf("Input.Messages mismatch:\n got: %+v\nwant: %+v", built.Input.Messages, wantMsgs)
	}
	if len(built.Normalization) != 0 {
		t.Errorf("Normalization = %+v, want empty for a clean transcript", built.Normalization)
	}
}

// TestPipelineEmptyMinimal runs the pipeline with no stable candidates, no
// catalog, and a single-message transcript. Every stage must produce valid
// output.
func TestPipelineEmptyMinimal(t *testing.T) {
	service := NewService()

	alloc, err := service.Estimate(EstimateRequest{
		ID:                  "req-estimate",
		Model:               "claude-sonnet-4",
		ContextWindowTokens: 131072,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v, want nil", err)
	}
	if alloc.SystemPromptTokens <= 0 {
		t.Errorf("SystemPromptTokens = %d, want > 0", alloc.SystemPromptTokens)
	}

	prefix, err := service.Assemble(AssembleRequest{
		ID:    "req-assemble",
		Model: "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v, want nil", err)
	}
	if !strings.Contains(prefix.SystemPrompt, "You are a helpful assistant.") {
		t.Errorf("SystemPrompt = %q, want it to contain the assistant preamble", prefix.SystemPrompt)
	}
	if !hex16.MatchString(prefix.SystemPromptID) {
		t.Errorf("SystemPromptID = %q, want 16 hex characters", prefix.SystemPromptID)
	}

	built, err := service.Prepare(PrepareRequest{
		ID:         "req-prepare",
		Prefix:     prefix,
		Transcript: []session.SessionRecord{userText("hello")},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v, want nil", err)
	}
	if built.Input.System != prefix.SystemPrompt {
		t.Errorf("Input.System = %q, want prefix.system_prompt %q", built.Input.System, prefix.SystemPrompt)
	}
	wantMsgs := []modelinvocation.ModelMessage{{Role: modelinvocation.RoleUser, Content: "hello"}}
	if !reflect.DeepEqual(built.Input.Messages, wantMsgs) {
		t.Errorf("Input.Messages mismatch:\n got: %+v\nwant: %+v", built.Input.Messages, wantMsgs)
	}
}

// TestPipelineToolCallNormalization verifies end-to-end that a tool_call and
// its tool_result normalize to an assistant message with canonical ToolCalls
// and a tool-role message with the matching CallID.
func TestPipelineToolCallNormalization(t *testing.T) {
	service := NewService()

	prefix, err := service.Assemble(AssembleRequest{
		ID:    "req-assemble",
		Model: "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v, want nil", err)
	}

	call := session.ToolCall{
		ID:                 "call-42",
		ToolID:             "t-7",
		DefinitionRevision: "rev-2",
		Name:               "run_shell",
		Arguments:          map[string]any{"cmd": "ls -la"},
	}
	built, err := service.Prepare(PrepareRequest{
		ID:     "req-prepare",
		Prefix: prefix,
		Transcript: []session.SessionRecord{
			rec(session.RecordToolCall, "", nil, "", []session.ToolCall{call}),
			rec(session.RecordToolResult, "", strPtr("total 8"), "call-42", nil),
		},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v, want nil", err)
	}

	wantMsgs := []modelinvocation.ModelMessage{
		{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{
			ID:                 "call-42",
			ToolID:             "t-7",
			DefinitionRevision: "rev-2",
			Name:               "run_shell",
			Arguments:          map[string]any{"cmd": "ls -la"},
		}}},
		{Role: modelinvocation.RoleTool, CallID: "call-42", Content: "total 8"},
	}
	if !reflect.DeepEqual(built.Input.Messages, wantMsgs) {
		t.Errorf("Input.Messages mismatch:\n got: %+v\nwant: %+v", built.Input.Messages, wantMsgs)
	}
	if built.Input.Messages[1].CallID != "call-42" {
		t.Errorf("tool message CallID = %q, want %q", built.Input.Messages[1].CallID, "call-42")
	}
	if len(built.Normalization) != 0 {
		t.Errorf("Normalization = %+v, want empty for an answered tool call", built.Normalization)
	}
}

// TestPipelinePerCallContextInjection verifies that per-call dynamic context
// is injected into the last user message as XML blocks.
func TestPipelinePerCallContextInjection(t *testing.T) {
	service := NewService()

	prefix, err := service.Assemble(AssembleRequest{
		ID:    "req-assemble",
		Model: "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v, want nil", err)
	}

	built, err := service.Prepare(PrepareRequest{
		ID:     "req-prepare",
		Prefix: prefix,
		Transcript: []session.SessionRecord{
			userText("What should I do next?"),
		},
		Dynamic: []contextprovider.ContextResponse{{
			RequestID: "bundle-1",
			Candidates: []contextprovider.ContextCandidate{
				{ID: "mem-1", Metadata: slotMeta(contextprovider.SlotMemory), Content: "Remember the plan."},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v, want nil", err)
	}

	if len(built.Input.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(built.Input.Messages))
	}
	want := "What should I do next?\n" +
		"<per_call_context slot=\"memory\">\n" +
		"<candidate id=\"mem-1\">Remember the plan.</candidate>\n" +
		"</per_call_context>"
	if built.Input.Messages[0].Content != want {
		t.Errorf("user message content mismatch:\n got: %q\nwant: %q", built.Input.Messages[0].Content, want)
	}
}

// TestPipelineAssembleByteStable verifies that identical Assemble inputs
// produce identical prefixes, and that preparing from either prefix produces
// an identical BuiltContext.
func TestPipelineAssembleByteStable(t *testing.T) {
	service := NewService()

	req := AssembleRequest{
		ID:               "req-assemble",
		Model:            "claude-sonnet-4",
		StableCandidates: sampleCandidates(),
		Catalog:          sampleCatalog(),
	}
	first, err := service.Assemble(req)
	if err != nil {
		t.Fatalf("first Assemble() error = %v, want nil", err)
	}
	second, err := service.Assemble(req)
	if err != nil {
		t.Fatalf("second Assemble() error = %v, want nil", err)
	}
	if first != second {
		t.Errorf("Assemble() not byte-stable:\n first: %+v\nsecond: %+v", first, second)
	}

	transcript := []session.SessionRecord{
		userText("check the tests"),
		rec(session.RecordToolCall, "", nil, "", []session.ToolCall{{ID: "call-1", Name: "run_shell"}}),
		rec(session.RecordToolResult, "", strPtr("all pass"), "call-1", nil),
	}
	dynamic := []contextprovider.ContextResponse{{
		RequestID: "bundle-1",
		Candidates: []contextprovider.ContextCandidate{
			{ID: "mem-1", Metadata: slotMeta(contextprovider.SlotMemory), Content: "Fact one."},
		},
	}}
	p1, err := service.Prepare(PrepareRequest{
		ID:         "req-prepare",
		Prefix:     first,
		Transcript: transcript,
		Dynamic:    dynamic,
	})
	if err != nil {
		t.Fatalf("Prepare(first) error = %v, want nil", err)
	}
	p2, err := service.Prepare(PrepareRequest{
		ID:         "req-prepare",
		Prefix:     second,
		Transcript: transcript,
		Dynamic:    dynamic,
	})
	if err != nil {
		t.Fatalf("Prepare(second) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Errorf("Prepare() output not identical for byte-stable prefixes:\n first: %+v\nsecond: %+v", p1, p2)
	}
}

// TestPipelineEstimateAssembleBudget verifies the assembled system prompt
// stays inside the system prompt budget Estimate reserved. len() is a rough
// character-count proxy, not a token count.
func TestPipelineEstimateAssembleBudget(t *testing.T) {
	service := NewService()

	alloc, err := service.Estimate(EstimateRequest{
		ID:                  "req-estimate",
		Model:               "claude-sonnet-4",
		ContextWindowTokens: 131072,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v, want nil", err)
	}
	if alloc.SystemPromptTokens <= 0 {
		t.Fatalf("SystemPromptTokens = %d, want > 0", alloc.SystemPromptTokens)
	}

	prefix, err := service.Assemble(AssembleRequest{
		ID:               "req-assemble",
		Model:            "claude-sonnet-4",
		StableCandidates: sampleCandidates(),
		Catalog:          sampleCatalog(),
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v, want nil", err)
	}

	if got := len(prefix.SystemPrompt); got > alloc.SystemPromptTokens {
		t.Errorf("system prompt length %d exceeds system prompt budget %d", got, alloc.SystemPromptTokens)
	}
}
