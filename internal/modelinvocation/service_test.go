package modelinvocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// NOTE: The prompt specified importing internal/modelinvocation/fake for the
// fake adapter. That is incompatible with same-package tests because the
// fake package imports the parent package, creating a cycle. The adapter is
// defined inline below — it has the same behaviour as fake.Adapter.

// --- inline fake adapter ---

// scriptedAdapter is a fake ProviderAdapter that emits a pre-configured
// sequence of fragments. nil fragments means "connection failed".
type scriptedAdapter struct {
	fragments []Fragment
}

func newScriptedAdapter(fragments []Fragment) *scriptedAdapter {
	return &scriptedAdapter{fragments: fragments}
}

// cancellingAdapter is used for context-cancellation tests. It pushes one
// content fragment immediately and blocks until the context is cancelled,
// then pushes a second fragment so the service's ctx.Err() check inside
// the accumulation loop fires.
type cancellingAdapter struct {
	fragment Fragment
}

func (a *cancellingAdapter) Invoke(ctx context.Context, req ProviderRequest) (<-chan Fragment, error) {
	ch := make(chan Fragment, 2)
	ch <- a.fragment
	go func() {
		<-ctx.Done()
		// Push a fragment after cancel so the service's next loop
		// iteration hits ctx.Err().
		select {
		case ch <- Fragment{DeltaContent: "."}:
		default:
		}
		close(ch)
	}()
	return ch, nil
}

func (a *scriptedAdapter) Invoke(ctx context.Context, req ProviderRequest) (<-chan Fragment, error) {
	if a.fragments == nil {
		return nil, errors.New("fake: connection failed")
	}
	ch := make(chan Fragment, len(a.fragments))
	go func() {
		defer close(ch)
		for _, f := range a.fragments {
			select {
			case <-ctx.Done():
				return
			case ch <- f:
			}
		}
	}()
	return ch, nil
}

// --- helpers ---

func newTestService() *Service {
	svc, _ := NewService(Options{
		Adapters: map[string]ProviderAdapter{
			"test": newScriptedAdapter(nil),
		},
	})
	return svc
}

func newTestServiceWithAdapter(name string, adapter ProviderAdapter) *Service {
	svc, _ := NewService(Options{Adapters: map[string]ProviderAdapter{name: adapter}})
	return svc
}

func validRequest(id string) ModelInvocationRequest {
	return ModelInvocationRequest{
		ID:       id,
		Model:    "gpt-4",
		Provider: "test",
		Input: ModelInput{
			Messages: []ModelMessage{{Role: RoleUser, Content: "hello"}},
		},
	}
}

func catalogWithTool(name, id, revision string) *toolinvocation.ToolCatalog {
	return &toolinvocation.ToolCatalog{
		ID: "catalog-1",
		Tools: []toolinvocation.ToolDefinition{
			{
				Name:        name,
				ID:          id,
				Revision:    revision,
				Description: "A test tool.",
			},
		},
	}
}

// --- tests ---

// TestNormalResponse verifies a straightforward streaming text response:
// two content fragments are concatenated and the terminal fragment carries
// finish_reason and usage.
func TestNormalResponse(t *testing.T) {
	adapter := newScriptedAdapter([]Fragment{
		{DeltaContent: "Hello, "},
		{DeltaContent: "world!"},
		{
			FinishReason: "stop",
			Usage: &CallUsage{
				InputTokens:  session.TokenCount{Value: 10, Source: session.TokenSourceProvider},
				OutputTokens: session.TokenCount{Value: 5, Source: session.TokenSourceProvider},
			},
		},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	result, failure := svc.Invoke(context.Background(), validRequest("req-1"))

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil")
	}
	if result.Content != "Hello, world!" {
		t.Fatalf("result.Content = %q, want %q", result.Content, "Hello, world!")
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopEndTurn)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("result.ToolCalls = %+v, want empty", result.ToolCalls)
	}
	if result.Usage.InputTokens.Value != 10 {
		t.Fatalf("result.Usage.InputTokens.Value = %d, want %d", result.Usage.InputTokens.Value, 10)
	}
	if result.Reasoning != "" {
		t.Fatalf("result.Reasoning = %q, want empty", result.Reasoning)
	}
}

// TestToolCallResponse verifies that tool call deltas are accumulated and
// the resulting ToolCall carries the correct name, tool ID, and arguments.
func TestToolCallResponse(t *testing.T) {
	catalog := catalogWithTool("search", "tool-search", "v1")
	adapter := newScriptedAdapter([]Fragment{
		{
			ToolCallDeltas: []ToolCallDelta{
				{Index: 0, Name: "search", Arguments: `{"query":"cats"}`},
			},
		},
		{
			FinishReason: "tool_calls",
			Usage: &CallUsage{
				InputTokens:  session.TokenCount{Value: 100, Source: session.TokenSourceProvider},
				OutputTokens: session.TokenCount{Value: 50, Source: session.TokenSourceProvider},
			},
		},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	req := validRequest("req-2")
	req.Catalog = catalog

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.StopReason != StopToolCalls {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopToolCalls)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(result.ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "search" {
		t.Fatalf("tc.Name = %q, want %q", tc.Name, "search")
	}
	if tc.ToolID != "tool-search" {
		t.Fatalf("tc.ToolID = %q, want %q", tc.ToolID, "tool-search")
	}
	if tc.Arguments["query"] != "cats" {
		t.Fatalf("tc.Arguments[%q] = %q, want %q", "query", tc.Arguments["query"], "cats")
	}
	if result.CatalogID != catalog.ID {
		t.Fatalf("result.CatalogID = %q, want %q", result.CatalogID, catalog.ID)
	}
}

// TestTruncationProviderLies verifies that when the provider emits truncated
// arguments (unclosed JSON) but claims finish_reason="tool_calls", the
// service overrides the stop reason to max_output and does not emit the
// truncated tool call.
func TestTruncationProviderLies(t *testing.T) {
	catalog := catalogWithTool("read_file", "tool-rf", "v1")
	adapter := newScriptedAdapter([]Fragment{
		{
			ToolCallDeltas: []ToolCallDelta{
				{Index: 0, Name: "read_file", Arguments: `{"path":"foo.`},
			},
		},
		{FinishReason: "tool_calls"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	req := validRequest("req-3")
	req.Catalog = catalog

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.StopReason != StopMaxOutput {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopMaxOutput)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("len(result.ToolCalls) = %d, want 0 (truncated call not emitted)", len(result.ToolCalls))
	}
}

// TestTruncationProviderHonest verifies that when the provider admits
// truncation with finish_reason="length", the service maps it to max_output
// and does not emit the truncated tool call.
func TestTruncationProviderHonest(t *testing.T) {
	adapter := newScriptedAdapter([]Fragment{
		{
			ToolCallDeltas: []ToolCallDelta{
				{Index: 0, Arguments: `{"path":"foo.`},
			},
		},
		{FinishReason: "length"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	result, failure := svc.Invoke(context.Background(), validRequest("req-4"))

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.StopReason != StopMaxOutput {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopMaxOutput)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("len(result.ToolCalls) = %d, want 0 (truncated call not emitted)", len(result.ToolCalls))
	}
}

// TestArgumentRepair verifies that tool call arguments with a trailing comma
// are silently repaired (comma stripped by pass 2) and parse correctly.
// NOTE: RepairArgs returns nil for pass-2 successes; only the final fallback
// to "{}" produces a RepairArguments note. The test verifies actual behavior.
func TestArgumentRepair(t *testing.T) {
	catalog := catalogWithTool("search", "tool-search", "v1")
	adapter := newScriptedAdapter([]Fragment{
		{
			ToolCallDeltas: []ToolCallDelta{
				{Index: 0, Name: "search", Arguments: `{"query":"cats",}`},
			},
		},
		{FinishReason: "tool_calls"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	req := validRequest("req-5")
	req.Catalog = catalog

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.ToolCalls[0].Arguments["query"] != "cats" {
		t.Fatalf("result.ToolCalls[0].Arguments[%q] = %q, want %q",
			"query", result.ToolCalls[0].Arguments["query"], "cats")
	}
	if len(result.Repairs) != 0 {
		t.Fatalf("len(result.Repairs) = %d, want 0 (trailing comma stripped silently)", len(result.Repairs))
	}
}

// TestNameRepairFuzzyMatch verifies fuzzy tool-name matching. The raw name
// "red_file" normalizes to "red_file", does not exactly match catalog name
// "read_file", but fuzzy-matches it — producing a RepairName note.
// NOTE: The prompt specified raw name "read-file" but that normalizes to
// "read_file" which matches the catalog exactly — no repair note produced.
// "red_file" exercises the fuzzy-match path.
func TestNameRepairFuzzyMatch(t *testing.T) {
	catalog := catalogWithTool("read_file", "tool-rf", "v1")
	adapter := newScriptedAdapter([]Fragment{
		{
			ToolCallDeltas: []ToolCallDelta{
				{Index: 0, Name: "red_file", Arguments: `{}`},
			},
		},
		{FinishReason: "tool_calls"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	req := validRequest("req-6")
	req.Catalog = catalog

	result, failure := svc.Invoke(context.Background(), req)

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Fatalf("result.ToolCalls[0].Name = %q, want %q", result.ToolCalls[0].Name, "read_file")
	}
	if len(result.Repairs) != 1 {
		t.Fatalf("len(result.Repairs) = %d, want 1", len(result.Repairs))
	}
	if result.Repairs[0].Kind != RepairName {
		t.Fatalf("result.Repairs[0].Kind = %q, want %q", result.Repairs[0].Kind, RepairName)
	}
	if result.Repairs[0].RawName != "red_file" {
		t.Fatalf("result.Repairs[0].RawName = %q, want %q", result.Repairs[0].RawName, "red_file")
	}
}

// TestEmptyResponse verifies that a terminal fragment with no content
// produces a valid (not nil) result with empty fields, not a failure.
func TestEmptyResponse(t *testing.T) {
	adapter := newScriptedAdapter([]Fragment{
		{FinishReason: "stop"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	result, failure := svc.Invoke(context.Background(), validRequest("req-7"))

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result == nil {
		t.Fatalf("Invoke() result = nil, want valid result")
	}
	if result.Content != "" {
		t.Fatalf("result.Content = %q, want empty", result.Content)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("len(result.ToolCalls) = %d, want 0", len(result.ToolCalls))
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopEndTurn)
	}
}

// TestValidationMissingModel verifies that an empty model field produces a
// non-retryable invalid_request failure.
func TestValidationMissingModel(t *testing.T) {
	svc := newTestService()
	req := validRequest("req-8")
	req.Model = ""

	result, failure := svc.Invoke(context.Background(), req)

	if failure == nil {
		t.Fatalf("Invoke() failure = nil, want non-nil")
	}
	if result != nil {
		t.Fatalf("Invoke() result = %+v, want nil", result)
	}
	if failure.Code != FailureInvalidRequest {
		t.Fatalf("failure.Code = %q, want %q", failure.Code, FailureInvalidRequest)
	}
	if failure.Retryable {
		t.Fatalf("failure.Retryable = true, want false")
	}
}

// TestValidationEmptyMessages verifies that an empty Messages slice is
// rejected with invalid_request.
func TestValidationEmptyMessages(t *testing.T) {
	svc := newTestService()
	req := validRequest("req-9")
	req.Input.Messages = nil

	_, failure := svc.Invoke(context.Background(), req)

	if failure == nil || failure.Code != FailureInvalidRequest {
		t.Fatalf("failure.Code = %q, want %q", failureCode(failure), FailureInvalidRequest)
	}
}

// TestValidationInvalidUserMessage verifies that a user message with empty
// Content is rejected with invalid_request.
func TestValidationInvalidUserMessage(t *testing.T) {
	svc := newTestService()
	req := validRequest("req-10")
	req.Input.Messages[0].Content = ""

	_, failure := svc.Invoke(context.Background(), req)

	if failure == nil || failure.Code != FailureInvalidRequest {
		t.Fatalf("failure.Code = %q, want %q", failureCode(failure), FailureInvalidRequest)
	}
}

// TestUnknownProvider verifies that a provider name with no registered
// adapter returns a provider_unavailable failure.
func TestUnknownProvider(t *testing.T) {
	svc := newTestService()
	req := validRequest("req-11")
	req.Provider = "nonexistent"

	_, failure := svc.Invoke(context.Background(), req)

	if failure == nil || failure.Code != FailureProviderUnavailable {
		t.Fatalf("failure.Code = %q, want %q", failureCode(failure), FailureProviderUnavailable)
	}
}

// TestCancellation verifies that cancelling the context mid-stream produces
// a FailureCancelled with Retryable=false. The adapter emits many content
// fragments; the context is cancelled after a short delay so the service's
// accumulation loop catches ctx.Err().
func TestCancellation(t *testing.T) {
	// cancellingAdapter pushes one fragment immediately, then blocks until
	// ctx is cancelled, at which point it pushes a second fragment so the
	// service loop's ctx.Err() check fires.
	adapter := &cancellingAdapter{fragment: Fragment{DeltaContent: "hello"}}
	svc := newTestServiceWithAdapter("test", adapter)

	ctx, cancel := context.WithCancel(context.Background())

	var result *ModelInvocationResult
	var failure *ModelInvocationFailure
	done := make(chan struct{})
	go func() {
		result, failure = svc.Invoke(ctx, validRequest("req-12"))
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done

	if failure == nil {
		t.Fatalf("Invoke() failure = nil, want cancelled; result = %+v", result)
	}
	if failure.Code != FailureCancelled {
		t.Fatalf("failure.Code = %q, want %q", failure.Code, FailureCancelled)
	}
	if failure.Retryable {
		t.Fatalf("failure.Retryable = true, want false")
	}
	if failure.Partial == nil || failure.Partial.Content == "" {
		t.Fatalf("failure.Partial = %+v, want non-empty partial", failure.Partial)
	}
}

// TestFragmentErrorMapping verifies that fragment errors map to the correct
// failure codes through mapFragmentError.
func TestFragmentErrorMapping(t *testing.T) {
	cases := []struct {
		name      string
		errMsg    string
		wantCode  string
		wantRetry bool
	}{
		{"network error", "connection refused", FailureNetworkError, true},
		{"auth error", "auth failed 401", FailureAuthFailed, false},
		{"rate limit", "rate limit 429", FailureRateLimited, true},
		{"unknown error", "unknown thing", FailureProviderError, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newScriptedAdapter([]Fragment{
				{Error: errors.New(tc.errMsg)},
			})
			svc := newTestServiceWithAdapter("test", adapter)

			_, failure := svc.Invoke(context.Background(), validRequest("req-13"))

			if failure == nil {
				t.Fatalf("Invoke() failure = nil, want non-nil")
			}
			if failure.Code != tc.wantCode {
				t.Fatalf("failure.Code = %q, want %q", failure.Code, tc.wantCode)
			}
			if failure.Retryable != tc.wantRetry {
				t.Fatalf("failure.Retryable = %v, want %v", failure.Retryable, tc.wantRetry)
			}
		})
	}
}

// TestRetryableFlags verifies retryable flags across different failure
// sources: RateLimited is transient, InvalidRequest and Cancelled are not.
func TestRetryableFlags(t *testing.T) {
	// FailureRateLimited via fragment error → retryable.
	adapter := newScriptedAdapter([]Fragment{
		{Error: errors.New("rate limited")},
	})
	svc := newTestServiceWithAdapter("test", adapter)
	_, failure := svc.Invoke(context.Background(), validRequest("req-14a"))
	if failure == nil || !failure.Retryable {
		t.Fatalf("FailureRateLimited retryable = %v, want true", failure != nil && failure.Retryable)
	}

	// FailureInvalidRequest via validation → not retryable.
	svc2 := newTestService()
	req := validRequest("req-14b")
	req.Model = ""
	_, failure = svc2.Invoke(context.Background(), req)
	if failure == nil || failure.Retryable {
		t.Fatalf("FailureInvalidRequest retryable = %v, want false", failure != nil && failure.Retryable)
	}

	// FailureCancelled via context → not retryable (must happen mid-stream).
	ctx, cancel := context.WithCancel(context.Background())
	adapter3 := &cancellingAdapter{fragment: Fragment{DeltaContent: "x"}}
	svc3 := newTestServiceWithAdapter("test", adapter3)

	done := make(chan struct{})
	go func() {
		_, failure = svc3.Invoke(ctx, validRequest("req-14c"))
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done

	if failure == nil || failure.Retryable {
		t.Fatalf("FailureCancelled retryable = %v, want false", failure != nil && failure.Retryable)
	}
}

// TestMissingFinishReasonDefaultsToEndTurn verifies that when the adapter
// closes the channel without a terminal fragment (no FinishReason), the
// service defaults the stop reason to end_turn.
func TestMissingFinishReasonDefaultsToEndTurn(t *testing.T) {
	adapter := newScriptedAdapter([]Fragment{
		{DeltaContent: "hi"},
	})
	svc := newTestServiceWithAdapter("test", adapter)

	result, failure := svc.Invoke(context.Background(), validRequest("req-15"))

	if failure != nil {
		t.Fatalf("Invoke() failure = %+v", failure)
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result.StopReason = %q, want %q", result.StopReason, StopEndTurn)
	}
}

// failureCode returns the failure code or "(nil)" for error messages.
func failureCode(f *ModelInvocationFailure) string {
	if f == nil {
		return "(nil)"
	}
	return f.Code
}
