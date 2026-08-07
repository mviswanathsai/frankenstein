package openai

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/toolinvocation"
)

// testService creates a modelinvocation.Service backed by the real DeepSeek
// adapter. It skips the test if DEEPSEEK_API_KEY is not set.
func testService(t *testing.T) *modelinvocation.Service {
	t.Helper()
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	adapter := NewAdapter(apiKey, "https://api.deepseek.com")
	svc, err := modelinvocation.NewService(modelinvocation.Options{
		Adapters: map[string]modelinvocation.ProviderAdapter{"deepseek": adapter},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// simpleRequest builds a basic ModelInvocationRequest with the given model
// and provider.
func simpleRequest(model, provider string) modelinvocation.ModelInvocationRequest {
	return modelinvocation.ModelInvocationRequest{
		ID:       "test-1",
		Model:    model,
		Provider: provider,
		Input: modelinvocation.ModelInput{
			Messages: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "Say hello in exactly 3 words."},
			},
		},
	}
}

// TestSimpleChat is the most basic smoke test: one API call, verify the
// result shape (content, stop reason, usage).
func TestSimpleChat(t *testing.T) {
	svc := testService(t)
	req := simpleRequest("deepseek-v4-flash", "deepseek")

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil")
	}
	if result.Content == "" {
		t.Fatal("result.Content is empty")
	}
	if result.StopReason != modelinvocation.StopEndTurn {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, modelinvocation.StopEndTurn)
	}
	if result.Usage.InputTokens.Value <= 0 {
		t.Fatalf("result.Usage.InputTokens.Value = %d, want > 0", result.Usage.InputTokens.Value)
	}
	if result.Usage.OutputTokens.Value <= 0 {
		t.Fatalf("result.Usage.OutputTokens.Value = %d, want > 0", result.Usage.OutputTokens.Value)
	}
}

// TestToolCall verifies that a tool-using prompt produces tool calls with the
// correct stop reason, non-empty name, and matching catalog ID.
func TestToolCall(t *testing.T) {
	svc := testService(t)

	catalog := &toolinvocation.ToolCatalog{
		ID: "test-catalog",
		Tools: []toolinvocation.ToolDefinition{
			{
				ID:          "tool-weather",
				Revision:    "v1",
				Name:        "get_weather",
				Description: "Get the weather for a city",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
			},
		},
	}

	req := modelinvocation.ModelInvocationRequest{
		ID:       "test-tool",
		Model:    "deepseek-v4-flash",
		Provider: "deepseek",
		Input: modelinvocation.ModelInput{
			Messages: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "What's the weather in Paris? Use the tool."},
			},
		},
		Catalog: catalog,
	}

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil")
	}
	if result.StopReason != modelinvocation.StopToolCalls {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, modelinvocation.StopToolCalls)
	}
	if len(result.ToolCalls) == 0 {
		t.Fatal("result.ToolCalls is empty, want at least one tool call")
	}
	tc := result.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Fatalf("tc.Name = %q, want %q", tc.Name, "get_weather")
	}
	if result.CatalogID != catalog.ID {
		t.Fatalf("result.CatalogID = %q, want %q", result.CatalogID, catalog.ID)
	}
}

// TestReasoning verifies that DeepSeek V4's reasoning_content is accumulated
// into result.Reasoning.
func TestReasoning(t *testing.T) {
	svc := testService(t)

	req := modelinvocation.ModelInvocationRequest{
		ID:       "test-reasoning",
		Model:    "deepseek-v4-flash",
		Provider: "deepseek",
		Input: modelinvocation.ModelInput{
			Messages: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "If I have 3 apples and give away 2, then buy 5 more, how many do I have? Think step by step."},
			},
		},
	}

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil")
	}
	if result.Reasoning == "" {
		t.Fatal("result.Reasoning is empty, expected reasoning content from DeepSeek V4")
	}
}

// TestTruncation verifies that a very low max_tokens produces a StopMaxOutput
// stop reason.
func TestTruncation(t *testing.T) {
	svc := testService(t)

	maxTokens := 1
	req := modelinvocation.ModelInvocationRequest{
		ID:       "test-truncation",
		Model:    "deepseek-v4-flash",
		Provider: "deepseek",
		Input: modelinvocation.ModelInput{
			Messages: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "Write a long essay about the history of computing."},
			},
		},
		MaxOutputTokens: &maxTokens,
	}

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil")
	}
	if result.StopReason != modelinvocation.StopMaxOutput {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, modelinvocation.StopMaxOutput)
	}
}

// TestEmptyMessages verifies that empty input messages are rejected with
// FailureInvalidRequest. No API call is made.
func TestEmptyMessages(t *testing.T) {
	adapter := NewAdapter("", "https://api.deepseek.com")
	svc, err := modelinvocation.NewService(modelinvocation.Options{
		Adapters: map[string]modelinvocation.ProviderAdapter{"deepseek": adapter},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	req := modelinvocation.ModelInvocationRequest{
		ID:       "test-empty",
		Model:    "deepseek-v4-flash",
		Provider: "deepseek",
		Input: modelinvocation.ModelInput{
			Messages: nil,
		},
	}

	result, failure := svc.Invoke(context.Background(), req)

	if failure == nil {
		t.Fatalf("Invoke() failure = nil, want failure")
	}
	if result != nil {
		t.Fatalf("Invoke() result = %+v, want nil", result)
	}
	if failure.Code != modelinvocation.FailureInvalidRequest {
		t.Fatalf("failure.Code = %q, want %q", failure.Code, modelinvocation.FailureInvalidRequest)
	}
}

// TestMissingModel verifies that an empty model field is rejected with
// FailureInvalidRequest. No API call is made.
func TestMissingModel(t *testing.T) {
	adapter := NewAdapter("", "https://api.deepseek.com")
	svc, err := modelinvocation.NewService(modelinvocation.Options{
		Adapters: map[string]modelinvocation.ProviderAdapter{"deepseek": adapter},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	req := modelinvocation.ModelInvocationRequest{
		ID:       "test-no-model",
		Model:    "",
		Provider: "deepseek",
		Input: modelinvocation.ModelInput{
			Messages: []modelinvocation.ModelMessage{
				{Role: modelinvocation.RoleUser, Content: "hello"},
			},
		},
	}

	result, failure := svc.Invoke(context.Background(), req)

	if failure == nil {
		t.Fatalf("Invoke() failure = nil, want failure")
	}
	if result != nil {
		t.Fatalf("Invoke() result = %+v, want nil", result)
	}
	if failure.Code != modelinvocation.FailureInvalidRequest {
		t.Fatalf("failure.Code = %q, want %q", failure.Code, modelinvocation.FailureInvalidRequest)
	}
}
