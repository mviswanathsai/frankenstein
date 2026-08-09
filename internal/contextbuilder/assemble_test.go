package contextbuilder

import (
	"regexp"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/toolinvocation"
)

func TestAssembleEmptyRequest(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-1",
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if result.RequestID != "req-1" {
		t.Fatalf("RequestID = %q, want req-1", result.RequestID)
	}
	if result.SystemPromptID == "" {
		t.Fatalf("SystemPromptID is empty")
	}
	if len(result.SystemPromptID) != 16 {
		t.Fatalf("SystemPromptID length = %d, want 16", len(result.SystemPromptID))
	}

	// Prompt should contain the base text with no trailing whitespace.
	if result.SystemPrompt != "You are a helpful assistant." {
		t.Fatalf("SystemPrompt = %q, want %q", result.SystemPrompt, "You are a helpful assistant.")
	}
}

func TestAssembleWithBundles(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-2",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{
			{
				RequestID:  "bundle-1",
				ProviderID: "provider-1",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotMemory: []contextprovider.ContextCandidate{
							{ID: "c2", Content: "Remember this"},
						},
					},
				},
			},
			{
				RequestID:  "bundle-2",
				ProviderID: "provider-2",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotIdentity: []contextprovider.ContextCandidate{
							{ID: "c1", Content: "You are a bot"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Slots should be sorted alphabetically: identity before memory.
	expected := "You are a helpful assistant.\n\n<identity>\n<candidate id=\"c1\">\nYou are a bot\n</candidate>\n</identity>\n\n<memory>\n<candidate id=\"c2\">\nRemember this\n</candidate>\n</memory>"
	if result.SystemPrompt != expected {
		t.Fatalf("SystemPrompt:\ngot:  %q\nwant: %q", result.SystemPrompt, expected)
	}
}

func TestAssembleWithCatalog(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-3",
		Model: "test-model",
		Catalog: &toolinvocation.ToolCatalog{
			ID: "cat-1",
			Tools: []toolinvocation.ToolDefinition{
				{ID: "t1", Name: "bash", Description: "Run a command"},
				{ID: "t2", Name: "read", Description: "Read a file"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	expected := "You are a helpful assistant.\n\n<available_tools>\n- bash: Run a command\n- read: Read a file\n</available_tools>"
	if result.SystemPrompt != expected {
		t.Fatalf("SystemPrompt:\ngot:  %q\nwant: %q", result.SystemPrompt, expected)
	}
}

func TestAssembleByteStability(t *testing.T) {
	svc := NewService()
	req := AssembleRequest{
		ID:    "req-4",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{
			{
				RequestID:  "bundle-1",
				ProviderID: "provider-1",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotMemory: []contextprovider.ContextCandidate{
							{ID: "c2", Content: "Remember this"},
							{ID: "c3", Content: "Also this"},
						},
						contextprovider.SlotSkills: []contextprovider.ContextCandidate{
							{ID: "c4", Content: "test: run tests"},
						},
					},
				},
			},
		},
		Catalog: &toolinvocation.ToolCatalog{
			ID: "cat-1",
			Tools: []toolinvocation.ToolDefinition{
				{ID: "t1", Name: "bash", Description: "Run a command"},
				{ID: "t2", Name: "read", Description: "Read a file"},
			},
		},
	}

	result1, err := svc.Assemble(req)
	if err != nil {
		t.Fatalf("Assemble() first call error = %v", err)
	}
	result2, err := svc.Assemble(req)
	if err != nil {
		t.Fatalf("Assemble() second call error = %v", err)
	}

	if result1.SystemPrompt != result2.SystemPrompt {
		t.Fatalf("byte-stability violated:\nfirst:  %q\nsecond: %q", result1.SystemPrompt, result2.SystemPrompt)
	}
	if result1.SystemPromptID != result2.SystemPromptID {
		t.Fatalf("SystemPromptID mismatch: %q vs %q", result1.SystemPromptID, result2.SystemPromptID)
	}
}

func TestAssembleSystemPromptIDFormat(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-5",
		Model: "test-model",
		Catalog: &toolinvocation.ToolCatalog{
			ID: "cat-1",
			Tools: []toolinvocation.ToolDefinition{
				{ID: "t1", Name: "bash", Description: "Run a command"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	id := result.SystemPromptID
	if len(id) != 16 {
		t.Fatalf("SystemPromptID length = %d, want 16: %q", len(id), id)
	}

	hexRe := regexp.MustCompile(`^[0-9a-f]{16}$`)
	if !hexRe.MatchString(id) {
		t.Fatalf("SystemPromptID is not 16 hex chars: %q", id)
	}

	// Verify it's the real SHA-256 prefix: different inputs produce different IDs.
	result2, err := svc.Assemble(AssembleRequest{
		ID:    "req-6",
		Model: "a-different-model",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if result2.SystemPromptID == id {
		t.Fatalf("SystemPromptID should differ for different inputs, both got %q", id)
	}
}

func TestAssembleMissingModel(t *testing.T) {
	svc := NewService()
	_, err := svc.Assemble(AssembleRequest{
		ID: "req-err",
	})
	if err == nil {
		t.Fatalf("Assemble() with missing model should return an error")
	}

	cf, ok := err.(*ContextBuilderFailure)
	if !ok {
		t.Fatalf("error is not *ContextBuilderFailure: %T", err)
	}
	if cf.Code != FailureInvalidRequest {
		t.Fatalf("error code = %q, want %q", cf.Code, FailureInvalidRequest)
	}
	if cf.RequestID != "req-err" {
		t.Fatalf("error RequestID = %q, want req-err", cf.RequestID)
	}
	if cf.Message == "" {
		t.Fatalf("error message is empty")
	}
}

func TestAssembleMergesBundleOrder(t *testing.T) {
	// Verify that candidates from multiple bundles in the same slot preserve
	// bundle order: earlier bundle's candidates come first.
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-merge",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{
			{
				RequestID:  "bundle-1",
				ProviderID: "p1",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotMemory: []contextprovider.ContextCandidate{
							{ID: "first", Content: "bundle-1 item"},
						},
					},
				},
			},
			{
				RequestID:  "bundle-2",
				ProviderID: "p2",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotMemory: []contextprovider.ContextCandidate{
							{ID: "second", Content: "bundle-2 item"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// "first" should appear before "second" in the output.
	firstIdx := indexOf(result.SystemPrompt, `id="first"`)
	secondIdx := indexOf(result.SystemPrompt, `id="second"`)
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("both candidates not found in prompt:\n%s", result.SystemPrompt)
	}
	if firstIdx >= secondIdx {
		t.Fatalf("bundle-2 candidate appears before bundle-1 candidate (firstIdx=%d >= secondIdx=%d):\n%s", firstIdx, secondIdx, result.SystemPrompt)
	}
}

func TestAssembleSlotOrderDeterministic(t *testing.T) {
	// Verify that slots are sorted alphabetically regardless of bundle order.
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-sort",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{
			{
				RequestID: "bundle-1",
				Retained: contextprovider.ContextCollection{
					Buckets: contextprovider.ContextBuckets{
						contextprovider.SlotMemory:    {{ID: "m", Content: "mem"}},
						contextprovider.SlotSkills:    {{ID: "s", Content: "skill"}},
						contextprovider.SlotIdentity:  {{ID: "i", Content: "ident"}},
						contextprovider.SlotToolGuidance: {{ID: "tg", Content: "guidance"}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Slots should appear alphabetically:
	// identity < memory < skills < tool_guidance
	idIdx := indexOf(result.SystemPrompt, "<identity>")
	memIdx := indexOf(result.SystemPrompt, "<memory>")
	skillIdx := indexOf(result.SystemPrompt, "<skills>")
	tgIdx := indexOf(result.SystemPrompt, "<tool_guidance>")

	if idIdx < 0 || memIdx < 0 || skillIdx < 0 || tgIdx < 0 {
		t.Fatalf("expected slots not found in prompt:\n%s", result.SystemPrompt)
	}
	if !(idIdx < memIdx && memIdx < skillIdx && skillIdx < tgIdx) {
		t.Fatalf("slots not sorted alphabetically (identity=%d, memory=%d, skills=%d, tool_guidance=%d):\n%s",
			idIdx, memIdx, skillIdx, tgIdx, result.SystemPrompt)
	}
}

func TestAssembleToolsPreserveCatalogOrder(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:    "req-tool-order",
		Model: "test-model",
		Catalog: &toolinvocation.ToolCatalog{
			ID: "cat-1",
			Tools: []toolinvocation.ToolDefinition{
				{ID: "t3", Name: "zulu", Description: "Third"},
				{ID: "t1", Name: "alpha", Description: "First"},
				{ID: "t2", Name: "bravo", Description: "Second"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	zIdx := indexOf(result.SystemPrompt, "- zulu")
	aIdx := indexOf(result.SystemPrompt, "- alpha")
	bIdx := indexOf(result.SystemPrompt, "- bravo")

	if zIdx < 0 || aIdx < 0 || bIdx < 0 {
		t.Fatalf("tool names not found in prompt:\n%s", result.SystemPrompt)
	}
	// Catalog order: zulu, alpha, bravo
	if !(zIdx < aIdx && aIdx < bIdx) {
		t.Fatalf("tools not in catalog order (zulu=%d, alpha=%d, bravo=%d):\n%s",
			zIdx, aIdx, bIdx, result.SystemPrompt)
	}
}

func TestAssembleWithNilCatalog(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:      "req-nil-cat",
		Model:   "test-model",
		Catalog: nil,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if result.SystemPrompt != "You are a helpful assistant." {
		t.Fatalf("SystemPrompt with nil catalog = %q", result.SystemPrompt)
	}
}

func TestAssembleWithNilBundles(t *testing.T) {
	svc := NewService()
	result, err := svc.Assemble(AssembleRequest{
		ID:             "req-nil-bundles",
		Model:          "test-model",
		ContextBundles: nil,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if result.SystemPrompt != "You are a helpful assistant." {
		t.Fatalf("SystemPrompt with nil bundles = %q", result.SystemPrompt)
	}
}

// indexOf returns the position of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
