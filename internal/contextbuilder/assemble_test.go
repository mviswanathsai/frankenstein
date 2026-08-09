package contextbuilder

import (
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/toolinvocation"
)

func TestAssembleEmptyRequest(t *testing.T) {
	prefix, err := NewService().Assemble(AssembleRequest{ID: "request-1", Model: "test-model"})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(prefix.SystemPrompt, "You are a helpful assistant.") {
		t.Fatalf("system prompt = %q, missing default assistant text", prefix.SystemPrompt)
	}
}

func TestAssembleRetainedContext(t *testing.T) {
	req := AssembleRequest{
		ID:    "request-1",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{{
			Retained: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
				contextprovider.SlotSkills: {{ID: "skill-1", Content: "Use concise answers."}},
			}},
		}},
	}

	prefix, err := NewService().Assemble(req)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	for _, want := range []string{"<skills>", `<candidate id="skill-1">`, "Use concise answers.", "</skills>"} {
		if !strings.Contains(prefix.SystemPrompt, want) {
			t.Errorf("system prompt = %q, missing %q", prefix.SystemPrompt, want)
		}
	}
}

func TestAssembleCatalog(t *testing.T) {
	prefix, err := NewService().Assemble(AssembleRequest{
		ID:    "request-1",
		Model: "test-model",
		Catalog: &toolinvocation.ToolCatalog{Tools: []toolinvocation.ToolDefinition{
			{Name: "first_tool", Description: "First description"},
			{Name: "second_tool", Description: "Second description"},
		}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	first := strings.Index(prefix.SystemPrompt, "first_tool")
	second := strings.Index(prefix.SystemPrompt, "second_tool")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("tools are not in catalog order: %q", prefix.SystemPrompt)
	}
	if !strings.Contains(prefix.SystemPrompt, "First description") || !strings.Contains(prefix.SystemPrompt, "Second description") {
		t.Fatalf("system prompt = %q, missing tool descriptions", prefix.SystemPrompt)
	}
}

func TestAssembleByteStability(t *testing.T) {
	req := AssembleRequest{
		ID:    "request-1",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{{
			Retained: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
				contextprovider.SlotSkills:              {{ID: "skill", Content: "skill"}},
				contextprovider.SlotIdentity:            {{ID: "identity", Content: "identity"}},
				contextprovider.SlotSessionFact:         {{ID: "fact", Content: "fact"}},
				contextprovider.SlotProjectInstructions: {{ID: "project", Content: "project"}},
			}},
		}},
		Catalog: &toolinvocation.ToolCatalog{Tools: []toolinvocation.ToolDefinition{{Name: "tool", Description: "description"}}},
	}

	service := NewService()
	first, err := service.Assemble(req)
	if err != nil {
		t.Fatalf("first Assemble() error = %v", err)
	}
	second, err := service.Assemble(req)
	if err != nil {
		t.Fatalf("second Assemble() error = %v", err)
	}
	if first != second {
		t.Fatalf("identical requests produced different prefixes:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestAssembleSystemPromptIDLength(t *testing.T) {
	prefix, err := NewService().Assemble(AssembleRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(prefix.SystemPromptID) != 16 {
		t.Fatalf("system prompt ID length = %d, want 16", len(prefix.SystemPromptID))
	}
}

func TestAssembleMissingModel(t *testing.T) {
	_, err := NewService().Assemble(AssembleRequest{})
	if err == nil {
		t.Fatal("Assemble() error = nil, want invalid_request")
	}
	if !strings.Contains(err.Error(), FailureInvalidRequest) {
		t.Fatalf("Assemble() error = %v, want %q", err, FailureInvalidRequest)
	}
}
