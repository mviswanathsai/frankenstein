package contextbuilder

import (
	"testing"
	"time"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// TestFullHappyPath exercises the complete estimate → assemble → prepare
// pipeline with realistic inputs.
func TestFullHappyPath(t *testing.T) {
	svc := NewService()

	// Phase 1: Estimate
	alloc, err := svc.Estimate(EstimateRequest{
		ID:                  "happy-path",
		Model:               "gpt-4",
		ContextWindowTokens: 128000,
	})
	if err != nil {
		t.Fatalf("Estimate error: %v", err)
	}
	if alloc.SystemPromptTokens <= 0 {
		t.Errorf("expected positive system_prompt_tokens, got %d", alloc.SystemPromptTokens)
	}

	// Phase 2: Assemble
	bundles := []contextprovider.ContextBundle{
		{
			RequestID:  "bundle-1",
			ProviderID: "provider-1",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotProjectInstructions: {
						{ID: "inst-1", Content: "Always respond in JSON."},
						{ID: "inst-2", Content: "Be concise."},
					},
					contextprovider.SlotSkills: {
						{ID: "skill-1", Content: "go: run Go tests"},
					},
				},
			},
		},
	}
	catalog := &toolinvocation.ToolCatalog{
		ID: "cat-1",
		Tools: []toolinvocation.ToolDefinition{
			{ID: "t1", Name: "bash", Description: "Run a shell command"},
			{ID: "t2", Name: "read", Description: "Read a file"},
		},
	}

	prefix, err := svc.Assemble(AssembleRequest{
		ID:             "happy-path-assemble",
		SessionID:      "sess-1",
		Model:          "gpt-4",
		ContextBundles: bundles,
		Catalog:        catalog,
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}
	if prefix.SystemPrompt == "" {
		t.Fatal("assemble returned empty system prompt")
	}
	if len(prefix.SystemPromptID) != 16 {
		t.Fatalf("SystemPromptID length = %d, want 16", len(prefix.SystemPromptID))
	}

	// Phase 3: Prepare — build a realistic transcript
	transcript := []session.SessionRecord{
		{
			ID:        "rec-1",
			Seq:       1,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("What files are in /tmp?"),
			CreatedAt: time.Now(),
		},
		{
			ID:   "rec-2",
			Seq:  2,
			Kind: session.RecordMessage,
			Role: "assistant",
			Text: textPtr("Let me check that for you."),
			ToolCalls: []session.ToolCall{
				{ID: "call-1", Name: "bash", Arguments: map[string]any{"command": "ls /tmp"}},
			},
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-3",
			Seq:       3,
			Kind:      session.RecordToolResult,
			CallID:    "call-1",
			Text:      textPtr("file1.txt\nfile2.log\n"),
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-4",
			Seq:       4,
			Kind:      session.RecordMessage,
			Role:      "assistant",
			Text:      textPtr("There are 2 files: file1.txt and file2.log."),
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-5",
			Seq:       5,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("Now read file1.txt."),
			CreatedAt: time.Now(),
		},
	}

	built, err := svc.Prepare(PrepareRequest{
		ID:         "happy-path-prepare",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		Prefix:     prefix,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}

	// Verify system prompt is passed through verbatim.
	if built.Input.System != prefix.SystemPrompt {
		t.Errorf("System = %q, want %q", built.Input.System, prefix.SystemPrompt)
	}

	// Verify normalized messages: user → assistant(tool_call) → tool → assistant → user
	if len(built.Input.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(built.Input.Messages))
	}

	expectedRoles := []modelinvocation.ModelMessageRole{
		modelinvocation.RoleUser,
		modelinvocation.RoleAssistant,
		modelinvocation.RoleTool,
		modelinvocation.RoleAssistant,
		modelinvocation.RoleUser,
	}
	for i, want := range expectedRoles {
		if built.Input.Messages[i].Role != want {
			t.Errorf("message[%d].Role = %q, want %q", i, built.Input.Messages[i].Role, want)
		}
	}

	// The tool message should have the correct CallID.
	toolMsg := built.Input.Messages[2]
	if toolMsg.CallID != "call-1" {
		t.Errorf("tool message CallID = %q, want call-1", toolMsg.CallID)
	}

	// Verify the second assistant message has the text content.
	if built.Input.Messages[3].Content != "There are 2 files: file1.txt and file2.log." {
		t.Errorf("assistant content = %q, want 'There are 2 files: file1.txt and file2.log.'", built.Input.Messages[3].Content)
	}

	// Verify the last user message has its text.
	if built.Input.Messages[4].Content != "Now read file1.txt." {
		t.Errorf("last user content = %q, want 'Now read file1.txt.'", built.Input.Messages[4].Content)
	}
}

// TestEmptyMinimalFlow exercises the pipeline with the smallest possible inputs.
func TestEmptyMinimalFlow(t *testing.T) {
	svc := NewService()

	// Estimate with minimal window
	alloc, err := svc.Estimate(EstimateRequest{
		ID:                  "minimal",
		Model:               "test-model",
		ContextWindowTokens: 4096,
	})
	if err != nil {
		t.Fatalf("Estimate error: %v", err)
	}
	if alloc.SystemPromptTokens < 0 {
		t.Errorf("expected non-negative system_prompt_tokens, got %d", alloc.SystemPromptTokens)
	}

	// Assemble with no bundles or catalog
	prefix, err := svc.Assemble(AssembleRequest{
		ID:    "minimal-assemble",
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}
	if prefix.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("SystemPrompt = %q, want default", prefix.SystemPrompt)
	}

	// Prepare with a single user message
	transcript := []session.SessionRecord{
		{
			ID:        "rec-1",
			Seq:       1,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("Hello"),
			CreatedAt: time.Now(),
		},
	}

	built, err := svc.Prepare(PrepareRequest{
		ID:         "minimal-prepare",
		Prefix:     prefix,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}

	if built.Input.System != prefix.SystemPrompt {
		t.Errorf("System = %q, want %q", built.Input.System, prefix.SystemPrompt)
	}
	if len(built.Input.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(built.Input.Messages))
	}
	if built.Input.Messages[0].Role != modelinvocation.RoleUser {
		t.Errorf("expected user role, got %s", built.Input.Messages[0].Role)
	}
	if built.Input.Messages[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", built.Input.Messages[0].Content)
	}
}

// TestToolCallNormalizationE2E verifies that tool_call and tool_result records
// produce correctly normalized messages with the right roles and CallIDs.
func TestToolCallNormalizationE2E(t *testing.T) {
	svc := NewService()

	transcript := []session.SessionRecord{
		{
			ID:        "rec-1",
			Seq:       1,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("Search for weather in NYC"),
			CreatedAt: time.Now(),
		},
		{
			ID:   "rec-2",
			Seq:  2,
			Kind: session.RecordToolCall,
			ToolCalls: []session.ToolCall{
				{ID: "call-weather", Name: "weather", Arguments: map[string]any{"city": "NYC"}},
			},
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-3",
			Seq:       3,
			Kind:      session.RecordToolResult,
			CallID:    "call-weather",
			Text:      textPtr("Sunny, 72F"),
			CreatedAt: time.Now(),
		},
		{
			ID:   "rec-4",
			Seq:  4,
			Kind: session.RecordToolCall,
			ToolCalls: []session.ToolCall{
				{ID: "call-search", Name: "web_search", Arguments: map[string]any{"query": "NYC news"}},
			},
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-5",
			Seq:       5,
			Kind:      session.RecordToolResult,
			CallID:    "call-search",
			Text:      textPtr("No major news today."),
			CreatedAt: time.Now(),
		},
	}

	prefix, err := svc.Assemble(AssembleRequest{
		ID:    "norm-assemble",
		Model: "gpt-4",
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}

	built, err := svc.Prepare(PrepareRequest{
		ID:         "norm-prepare",
		Prefix:     prefix,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}

	// Expected: user, assistant(tool_call weather), tool(call-weather),
	//           assistant(tool_call search), tool(call-search)
	if len(built.Input.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(built.Input.Messages))
	}

	// Check the first tool call → assistant message
	assist1 := built.Input.Messages[1]
	if assist1.Role != modelinvocation.RoleAssistant {
		t.Errorf("message[1] role = %q, want assistant", assist1.Role)
	}
	if len(assist1.ToolCalls) != 1 {
		t.Fatalf("message[1] tool calls = %d, want 1", len(assist1.ToolCalls))
	}
	if assist1.ToolCalls[0].ID != "call-weather" {
		t.Errorf("message[1] tool call ID = %q, want call-weather", assist1.ToolCalls[0].ID)
	}
	if assist1.ToolCalls[0].Name != "weather" {
		t.Errorf("message[1] tool call name = %q, want weather", assist1.ToolCalls[0].Name)
	}

	// Check the first tool result → tool message
	tool1 := built.Input.Messages[2]
	if tool1.Role != modelinvocation.RoleTool {
		t.Errorf("message[2] role = %q, want tool", tool1.Role)
	}
	if tool1.CallID != "call-weather" {
		t.Errorf("message[2] CallID = %q, want call-weather", tool1.CallID)
	}
	if tool1.Content != "Sunny, 72F" {
		t.Errorf("message[2] content = %q, want 'Sunny, 72F'", tool1.Content)
	}

	// Check the second tool result → tool message
	tool2 := built.Input.Messages[4]
	if tool2.Role != modelinvocation.RoleTool {
		t.Errorf("message[4] role = %q, want tool", tool2.Role)
	}
	if tool2.CallID != "call-search" {
		t.Errorf("message[4] CallID = %q, want call-search", tool2.CallID)
	}
	if tool2.Content != "No major news today." {
		t.Errorf("message[4] content = %q, want 'No major news today.'", tool2.Content)
	}
}

// TestPerCallContextInjectionE2E verifies that per-call context from
// context_bundles is injected into the last user message.
func TestPerCallContextInjectionE2E(t *testing.T) {
	svc := NewService()

	bundles := []contextprovider.ContextBundle{
		{
			RequestID:  "bundle-1",
			ProviderID: "provider-1",
			Retained:   contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{}},
			PerCall: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotMemory: {
						{ID: "mem-1", Content: "The user's name is Alice."},
						{ID: "mem-2", Content: "Alice prefers dark mode."},
					},
					contextprovider.SlotSessionFact: {
						{ID: "fact-1", Content: "Current project: frankenstein."},
					},
				},
			},
		},
	}

	prefix, err := svc.Assemble(AssembleRequest{
		ID:    "perc-assemble",
		Model: "gpt-4",
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}

	// Multiple turns, the last one being a user message.
	transcript := []session.SessionRecord{
		{
			ID:        "rec-1",
			Seq:       1,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("What is my name?"),
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-2",
			Seq:       2,
			Kind:      session.RecordMessage,
			Role:      "assistant",
			Text:      textPtr("Let me think..."),
			CreatedAt: time.Now(),
		},
		{
			ID:        "rec-3",
			Seq:       3,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("And what project are we on?"),
			CreatedAt: time.Now(),
		},
	}

	built, err := svc.Prepare(PrepareRequest{
		ID:             "perc-prepare",
		Prefix:         prefix,
		Transcript:     transcript,
		ContextBundles: bundles,
	})
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}

	// Find the last user message — should be message[2].
	if len(built.Input.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(built.Input.Messages))
	}
	lastUser := built.Input.Messages[2]
	if lastUser.Role != modelinvocation.RoleUser {
		t.Fatalf("last message role = %q, want user", lastUser.Role)
	}

	// Content should start with the user's original text.
	if len(lastUser.Content) < len("And what project are we on?") {
		t.Fatalf("last user content too short: %q", lastUser.Content)
	}
	if lastUser.Content[:len("And what project are we on?")] != "And what project are we on?" {
		t.Errorf("last user content does not start with original text: %q", lastUser.Content[:30])
	}

	// Verify both per-call context blocks are present.
	if !contains(lastUser.Content, `<per_call_context slot="memory"`) {
		t.Error("missing per_call_context slot=memory block")
	}
	if !contains(lastUser.Content, `<per_call_context slot="session_fact"`) {
		t.Error("missing per_call_context slot=session_fact block")
	}

	// Verify the actual candidate content is injected.
	if !contains(lastUser.Content, "The user's name is Alice.") {
		t.Error("missing memory candidate 'The user's name is Alice.'")
	}
	if !contains(lastUser.Content, "Alice prefers dark mode.") {
		t.Error("missing memory candidate 'Alice prefers dark mode.'")
	}
	if !contains(lastUser.Content, "Current project: frankenstein.") {
		t.Error("missing session_fact candidate 'Current project: frankenstein.'")
	}

	// Verify the first user message is NOT modified.
	firstUser := built.Input.Messages[0]
	if contains(firstUser.Content, "per_call_context") {
		t.Errorf("first user message should not have per_call_context, got %q", firstUser.Content)
	}
}

// TestByteStableAssembleAcrossPipeline verifies that Assemble is byte-stable
// and that Prepare preserves determinism across identical prefixes.
func TestByteStableAssembleAcrossPipeline(t *testing.T) {
	svc := NewService()

	bundles := []contextprovider.ContextBundle{
		{
			RequestID:  "bundle-1",
			ProviderID: "provider-1",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotProjectInstructions: {
						{ID: "inst-1", Content: "Always respond in JSON."},
					},
					contextprovider.SlotSkills: {
						{ID: "skill-1", Content: "go: run Go tests"},
					},
				},
			},
		},
	}
	catalog := &toolinvocation.ToolCatalog{
		ID: "cat-1",
		Tools: []toolinvocation.ToolDefinition{
			{ID: "t1", Name: "bash", Description: "Run a shell command"},
		},
	}

	req := AssembleRequest{
		ID:             "stable-assemble",
		SessionID:      "sess-1",
		Model:          "gpt-4",
		ContextBundles: bundles,
		Catalog:        catalog,
	}

	// Call Assemble twice.
	prefix1, err := svc.Assemble(req)
	if err != nil {
		t.Fatalf("Assemble call 1 error: %v", err)
	}
	prefix2, err := svc.Assemble(req)
	if err != nil {
		t.Fatalf("Assemble call 2 error: %v", err)
	}

	if prefix1.SystemPrompt != prefix2.SystemPrompt {
		t.Fatalf("Assemble not byte-stable:\ncall1: %q\ncall2: %q", prefix1.SystemPrompt, prefix2.SystemPrompt)
	}
	if prefix1.SystemPromptID != prefix2.SystemPromptID {
		t.Fatalf("SystemPromptID mismatch: %q vs %q", prefix1.SystemPromptID, prefix2.SystemPromptID)
	}

	// Now call Prepare with each prefix and verify identical output.
	transcript := []session.SessionRecord{
		{
			ID:        "rec-1",
			Seq:       1,
			Kind:      session.RecordMessage,
			Role:      "user",
			Text:      textPtr("Hello"),
			CreatedAt: time.Now(),
		},
	}

	built1, err := svc.Prepare(PrepareRequest{
		ID:         "stable-prepare-1",
		Prefix:     prefix1,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare call 1 error: %v", err)
	}

	built2, err := svc.Prepare(PrepareRequest{
		ID:         "stable-prepare-2",
		Prefix:     prefix2,
		Transcript: transcript,
	})
	if err != nil {
		t.Fatalf("Prepare call 2 error: %v", err)
	}

	// Both outputs should be identical.
	if built1.Input.System != built2.Input.System {
		t.Errorf("system prompt differs: %q vs %q", built1.Input.System, built2.Input.System)
	}
	if len(built1.Input.Messages) != len(built2.Input.Messages) {
		t.Fatalf("message count differs: %d vs %d", len(built1.Input.Messages), len(built2.Input.Messages))
	}
	for i := range built1.Input.Messages {
		m1, m2 := built1.Input.Messages[i], built2.Input.Messages[i]
		if m1.Role != m2.Role || m1.Content != m2.Content ||
			m1.CallID != m2.CallID || len(m1.ToolCalls) != len(m2.ToolCalls) {
			t.Errorf("message[%d] differs:\n  call1: %+v\n  call2: %+v", i, m1, m2)
		}
	}
}

// TestEstimateAssembleBudget verifies that the assembled system prompt fits
// within the budget allocated by Estimate.
func TestEstimateAssembleBudget(t *testing.T) {
	svc := NewService()

	// Use a window small enough to make the budget test meaningful.
	alloc, err := svc.Estimate(EstimateRequest{
		ID:                  "budget",
		Model:               "gpt-4",
		ContextWindowTokens: 128000,
	})
	if err != nil {
		t.Fatalf("Estimate error: %v", err)
	}

	if alloc.SystemPromptTokens <= 0 {
		t.Fatal("expected positive system_prompt_tokens budget")
	}

	// Assemble with a realistic amount of content.
	bundles := []contextprovider.ContextBundle{
		{
			RequestID:  "bundle-1",
			ProviderID: "provider-1",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotProjectInstructions: {
						{ID: "inst-1", Content: "Always respond concisely."},
					},
					contextprovider.SlotSkills: {
						{ID: "skill-1", Content: "go build: compiles the project"},
						{ID: "skill-2", Content: "go test: runs tests"},
					},
					contextprovider.SlotMemory: {
						{ID: "mem-1", Content: "User prefers short answers."},
					},
				},
			},
		},
	}

	catalog := &toolinvocation.ToolCatalog{
		ID: "cat-1",
		Tools: []toolinvocation.ToolDefinition{
			{ID: "t1", Name: "bash", Description: "Run a shell command"},
			{ID: "t2", Name: "read", Description: "Read a file's contents"},
			{ID: "t3", Name: "write", Description: "Write content to a file"},
		},
	}

	prefix, err := svc.Assemble(AssembleRequest{
		ID:             "budget-assemble",
		Model:          "gpt-4",
		ContextBundles: bundles,
		Catalog:        catalog,
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}

	// len() is a rough character-count proxy for token count.
	// A token is roughly 4 characters on average, so a len() check is
	// stricter than necessary and gives confidence the budget is respected.
	systemLen := len(prefix.SystemPrompt)
	if systemLen > alloc.SystemPromptTokens {
		t.Errorf("assembled system prompt length %d exceeds system_prompt_tokens budget %d",
			systemLen, alloc.SystemPromptTokens)
	}

	// The system prompt should be well within the budget — sanity check.
	// The allocation for a 128K window gives 2048 tokens budget.
	// A character-length of a few hundred is easily within that.
	t.Logf("system_prompt_tokens budget: %d, assembled prompt len(): %d",
		alloc.SystemPromptTokens, systemLen)
}
