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

func TestServiceFullPipeline(t *testing.T) {
	service := NewService()
	allocation, err := service.Estimate(EstimateRequest{
		ID:                  "estimate-1",
		Model:               "test-model",
		ContextWindowTokens: 128 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	bundles := []contextprovider.ContextBundle{{
		Retained: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
			contextprovider.SlotProjectInstructions: {{ID: "project", Content: "Follow the project instructions."}},
			contextprovider.SlotSkills:              {{ID: "skills", Content: "Use Go conventions."}},
		}},
	}}
	catalog := &toolinvocation.ToolCatalog{Tools: []toolinvocation.ToolDefinition{
		{Name: "search", Description: "Search the workspace."},
		{Name: "read", Description: "Read a file."},
	}}
	prefix, err := service.Assemble(AssembleRequest{
		ID: "assemble-1", Model: "test-model", ContextBundles: bundles, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}

	call := session.ToolCall{ID: "call-1", ToolID: "search-1", Name: "search", Arguments: map[string]any{"query": "context"}}
	transcript := []session.SessionRecord{
		{Kind: session.RecordMessage, Role: "user", Text: stringPtr("Find the context.")},
		{Kind: session.RecordMessage, Role: "assistant", Text: stringPtr("I will search.")},
		{Kind: session.RecordToolCall, ToolCalls: []session.ToolCall{call}},
		{Kind: session.RecordToolResult, CallID: "call-1", Text: stringPtr("Found it.")},
	}
	built, err := service.Prepare(PrepareRequest{ID: "prepare-1", Prefix: prefix, Transcript: transcript})
	if err != nil {
		t.Fatal(err)
	}

	if built.Input.System != prefix.SystemPrompt {
		t.Fatalf("system = %q, want prefix system prompt %q", built.Input.System, prefix.SystemPrompt)
	}
	want := []modelinvocation.ModelMessage{
		{Role: modelinvocation.RoleUser, Content: "Find the context."},
		{Role: modelinvocation.RoleAssistant, Content: "I will search."},
		{Role: modelinvocation.RoleAssistant, ToolCalls: []toolinvocation.ToolCall{{
			ID: "call-1", ToolID: "search-1", Name: "search", Arguments: map[string]any{"query": "context"},
		}}},
		{Role: modelinvocation.RoleTool, CallID: "call-1", Content: "Found it."},
	}
	if !reflect.DeepEqual(built.Input.Messages, want) {
		t.Fatalf("messages = %+v, want %+v", built.Input.Messages, want)
	}
	if len(prefix.SystemPrompt) > allocation.SystemPromptTokens {
		t.Fatalf("system prompt length = %d, allocation = %d", len(prefix.SystemPrompt), allocation.SystemPromptTokens)
	}
}

func TestServiceMinimalPipeline(t *testing.T) {
	service := NewService()
	allocation, err := service.Estimate(EstimateRequest{ID: "estimate-1", Model: "test-model", ContextWindowTokens: 128 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := service.Assemble(AssembleRequest{ID: "assemble-1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	built, err := service.Prepare(PrepareRequest{
		ID: "prepare-1", Prefix: prefix,
		Transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: stringPtr("hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocation.SystemPromptTokens <= 0 || built.Input.System == "" || len(built.Input.Messages) != 1 {
		t.Fatalf("minimal pipeline produced invalid output: allocation=%+v built=%+v", allocation, built)
	}
}

func TestServiceToolCallNormalizationEndToEnd(t *testing.T) {
	call := session.ToolCall{ID: "call-7", ToolID: "lookup-1", Name: "lookup"}
	prefix, err := NewService().Assemble(AssembleRequest{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewService().Prepare(PrepareRequest{
		Prefix: prefix,
		Transcript: []session.SessionRecord{
			{Kind: session.RecordToolCall, ToolCalls: []session.ToolCall{call}},
			{Kind: session.RecordToolResult, CallID: "call-7", Text: stringPtr("result")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Input.Messages) != 2 {
		t.Fatalf("messages = %+v, want tool call and result", built.Input.Messages)
	}
	result := built.Input.Messages[1]
	if result.Role != modelinvocation.RoleTool || result.CallID != "call-7" || result.Content != "result" {
		t.Fatalf("tool result = %+v, want role=tool call_id=call-7", result)
	}
}

func TestServicePerCallContextInjectionEndToEnd(t *testing.T) {
	prefix, err := NewService().Assemble(AssembleRequest{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewService().Prepare(PrepareRequest{
		Prefix:     prefix,
		Transcript: []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: stringPtr("help me")}},
		ContextBundles: []contextprovider.ContextBundle{{PerCall: contextprovider.ContextCollection{
			Buckets: contextprovider.ContextBuckets{
				contextprovider.SlotMemory: {{ID: "memory-1", Content: "The user prefers concise answers."}},
			},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := built.Input.Messages[0].Content
	if !strings.Contains(content, "The user prefers concise answers.") || !strings.Contains(content, `slot="memory"`) {
		t.Fatalf("user content = %q, missing injected per-call context", content)
	}
}

func TestServicePipelineByteStable(t *testing.T) {
	request := AssembleRequest{
		ID: "assemble-1", Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{{Retained: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
			contextprovider.SlotSkills:              {{ID: "skill", Content: "Be concise."}},
			contextprovider.SlotProjectInstructions: {{ID: "project", Content: "Use the repository rules."}},
		}}}},
		Catalog: &toolinvocation.ToolCatalog{Tools: []toolinvocation.ToolDefinition{{Name: "read", Description: "Read files."}}},
	}
	service := NewService()
	first, err := service.Assemble(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Assemble(request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("prefixes differ: first=%+v second=%+v", first, second)
	}
	transcript := []session.SessionRecord{{Kind: session.RecordMessage, Role: "user", Text: stringPtr("hello")}}
	firstBuilt, err := service.Prepare(PrepareRequest{Prefix: first, Transcript: transcript})
	if err != nil {
		t.Fatal(err)
	}
	secondBuilt, err := service.Prepare(PrepareRequest{Prefix: second, Transcript: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstBuilt, secondBuilt) {
		t.Fatalf("built contexts differ: first=%+v second=%+v", firstBuilt, secondBuilt)
	}
}

func TestServiceEstimateAssembleBudgetRelationship(t *testing.T) {
	service := NewService()
	allocation, err := service.Estimate(EstimateRequest{
		ID: "estimate-1", Model: "test-model", ContextWindowTokens: 128 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := service.Assemble(AssembleRequest{
		ID:    "assemble-1",
		Model: "test-model",
		ContextBundles: []contextprovider.ContextBundle{{Retained: contextprovider.ContextCollection{Buckets: contextprovider.ContextBuckets{
			contextprovider.SlotProjectInstructions: {{ID: "project", Content: "Keep the system prompt deterministic."}},
		}}}},
		Catalog: &toolinvocation.ToolCatalog{Tools: []toolinvocation.ToolDefinition{{Name: "read", Description: "Read a file."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix.SystemPrompt) > allocation.SystemPromptTokens {
		t.Fatalf("system prompt length = %d, want at most allocation %d", len(prefix.SystemPrompt), allocation.SystemPromptTokens)
	}
}

func stringPtr(value string) *string { return &value }
