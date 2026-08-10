package toolinvocation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProgressiveDisclosureDirectFlow(t *testing.T) {
	ctx := context.Background()
	var starts []ToolCallStarted
	service := newTestService(t, testAck(&starts), echoRegistration("echo"))

	listed, failure := service.ListTools(ctx, ToolCatalogRequest{ID: "list"})
	if failure != nil {
		t.Fatalf("ListTools() failure = %+v", failure)
	}
	if got := toolNames(listed.Catalog); strings.Join(got, ",") != "tool_search,tool_describe,tool_load" {
		t.Fatalf("initial catalog names = %v", got)
	}
	baseID := listed.Catalog.ID

	search := callFor(t, listed.Catalog, "search", "tool_search", map[string]any{"query": "ech"})
	searchResult, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-search",
		IdempotencyKey: "idem-search",
		CatalogID:      baseID,
		Calls:          []ToolCall{search},
	})
	if execFailure != nil {
		t.Fatalf("Execute(search) failure = %+v", execFailure)
	}
	if searchResult.CatalogTransition != nil {
		t.Fatalf("search returned transition: %+v", searchResult.CatalogTransition)
	}
	if !strings.Contains(searchResult.Results[0].Text, "echo") {
		t.Fatalf("search text = %q, want echo", searchResult.Results[0].Text)
	}

	describe := callFor(t, listed.Catalog, "describe", "tool_describe", map[string]any{"name": "echo"})
	describeResult, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-describe",
		IdempotencyKey: "idem-describe",
		CatalogID:      baseID,
		Calls:          []ToolCall{describe},
	})
	if execFailure != nil {
		t.Fatalf("Execute(describe) failure = %+v", execFailure)
	}
	if describeResult.CatalogTransition != nil {
		t.Fatalf("describe returned transition: %+v", describeResult.CatalogTransition)
	}
	if !strings.Contains(describeResult.Results[0].Text, `"text"`) {
		t.Fatalf("describe text = %q, want schema text property", describeResult.Results[0].Text)
	}
	if describeResult.Results[0].DescribedTool == nil || describeResult.Results[0].DescribedTool.Name != "echo" {
		t.Fatalf("described_tool = %+v, want echo definition", describeResult.Results[0].DescribedTool)
	}

	load := callFor(t, listed.Catalog, "load", "tool_load", map[string]any{"name": "echo"})
	loadResult, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-load",
		IdempotencyKey: "idem-load",
		CatalogID:      baseID,
		SessionID:      "sess",
		TurnID:         "turn",
		Calls:          []ToolCall{load},
	})
	if execFailure != nil {
		t.Fatalf("Execute(load) failure = %+v", execFailure)
	}
	if loadResult.CatalogTransition == nil {
		t.Fatalf("load transition = nil")
	}
	if loadResult.CatalogTransition.BaseCatalogID != baseID {
		t.Fatalf("transition base = %q, want %q", loadResult.CatalogTransition.BaseCatalogID, baseID)
	}
	loaded := loadResult.CatalogTransition.Catalog
	if got := toolNames(loaded); strings.Join(got, ",") != "tool_search,tool_describe,tool_load,echo" {
		t.Fatalf("loaded catalog names = %v", got)
	}

	echo := callFor(t, loaded, "echo-call", "echo", map[string]any{"text": "hello"})
	echoResult, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-echo",
		IdempotencyKey: "idem-echo",
		CatalogID:      loaded.ID,
		Calls:          []ToolCall{echo},
	})
	if execFailure != nil {
		t.Fatalf("Execute(echo) failure = %+v", execFailure)
	}
	if echoResult.Results[0].Status != ResultSucceeded || echoResult.Results[0].SideEffect != SideEffectNone {
		t.Fatalf("echo result = %+v, want succeeded + none", echoResult.Results[0])
	}
	if echoResult.Results[0].Text != "echo: hello" {
		t.Fatalf("echo text = %q", echoResult.Results[0].Text)
	}
	if len(starts) != 4 {
		t.Fatalf("call_started count = %d, want 4", len(starts))
	}
}

func TestToolDescribeStructuredEvidence(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, echoRegistration("echo"))
	catalog := mustList(t, service)
	describe := callFor(t, catalog, "describe", "tool_describe", map[string]any{"name": "echo"})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "idem",
		CatalogID:      catalog.ID,
		Calls:          []ToolCall{describe},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	got := result.Results[0]
	if got.ToolID != describe.ToolID || got.Name != "tool_describe" {
		t.Fatalf("describe result identity = %+v", got)
	}
	if got.DescribedTool == nil {
		t.Fatalf("described_tool = nil")
	}
	if got.DescribedTool.ID != "test:0:echo" || got.DescribedTool.Revision == "" || got.DescribedTool.Name != "echo" || got.DescribedTool.Description != "Echo input text." || !strings.Contains(string(got.DescribedTool.InputSchema), `"text"`) {
		t.Fatalf("described_tool = %+v, want canonical echo definition", got.DescribedTool)
	}
}

func TestFailedToolDescribeHasNoStructuredEvidence(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, echoRegistration("echo"))
	catalog := mustList(t, service)
	describe := callFor(t, catalog, "describe", "tool_describe", map[string]any{"name": "missing"})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "idem",
		CatalogID:      catalog.ID,
		Calls:          []ToolCall{describe},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if got := result.Results[0]; got.Failure == nil || got.DescribedTool != nil {
		t.Fatalf("result = %+v, want failed without described_tool", got)
	}
}

func TestBackendCannotForgeDescriptionEvidence(t *testing.T) {
	ctx := context.Background()
	forged := ToolDefinition{ID: "fake", Revision: "fake", Name: "fake", Description: "fake", InputSchema: json.RawMessage(`{"type":"object"}`)}
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "forge",
		Name: "forge", Description: "Forge evidence.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			return BackendResult{Text: "forged", DescribedTool: &forged}
		},
	})
	catalog := mustList(t, service)
	call := callFor(t, catalog, "forge", "forge", map[string]any{"value": "x"})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "idem",
		CatalogID:      catalog.ID,
		Calls:          []ToolCall{call},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	got := result.Results[0]
	if got.Failure == nil || got.Failure.Code != FailureMalformedResult || got.DescribedTool != nil {
		t.Fatalf("result = %+v, want malformed without described_tool", got)
	}
}

func TestIdempotencyReplayPreservesDescribedTool(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, echoRegistration("echo"))
	catalog := mustList(t, service)
	describe := callFor(t, catalog, "describe", "tool_describe", map[string]any{"name": "echo"})
	req := ToolExecutionRequest{ID: "exec", IdempotencyKey: "idem", CatalogID: catalog.ID, Calls: []ToolCall{describe}}

	first, failure := service.Execute(ctx, req)
	if failure != nil {
		t.Fatalf("first Execute() failure = %+v", failure)
	}
	second, failure := service.Execute(ctx, req)
	if failure != nil {
		t.Fatalf("second Execute() failure = %+v", failure)
	}
	if first.Results[0].DescribedTool == nil || second.Results[0].DescribedTool == nil || first.Results[0].DescribedTool.Revision != second.Results[0].DescribedTool.Revision {
		t.Fatalf("described_tool replay mismatch: first=%+v second=%+v", first.Results[0].DescribedTool, second.Results[0].DescribedTool)
	}
}

func TestDescribedToolSchemaDoesNotAliasReturnedOrRegistrationStorage(t *testing.T) {
	ctx := context.Background()
	schema := []byte(`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string"}},"required":["value"]}`)
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "alias",
		Name: "alias", Description: "Alias test.",
		InputSchema:      schema,
		Discoverable:     true,
		InitiallyVisible: true,
		Backend:          func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "ok"} },
	})
	schema[0] = '['
	catalog := mustList(t, service)
	describe := callFor(t, catalog, "describe", "tool_describe", map[string]any{"name": "alias"})

	first, failure := service.Execute(ctx, ToolExecutionRequest{ID: "first", IdempotencyKey: "first", CatalogID: catalog.ID, Calls: []ToolCall{describe}})
	if failure != nil {
		t.Fatalf("first Execute() failure = %+v", failure)
	}
	first.Results[0].DescribedTool.InputSchema[0] = '['
	second, failure := service.Execute(ctx, ToolExecutionRequest{ID: "second", IdempotencyKey: "second", CatalogID: catalog.ID, Calls: []ToolCall{describe}})
	if failure != nil {
		t.Fatalf("second Execute() failure = %+v", failure)
	}
	if second.Results[0].DescribedTool.InputSchema[0] != '{' {
		t.Fatalf("second described schema aliased mutable storage: %s", second.Results[0].DescribedTool.InputSchema)
	}
}

func TestStableIDOutputIsUnchanged(t *testing.T) {
	raw := []byte(`{"name":"echo","description":"Echo input text."}`)
	if got, want := StableID("toolcat", raw), stableID("toolcat", raw); got != want {
		t.Fatalf("StableID() = %q, want %q", got, want)
	}
}

func TestProgressiveDisclosureProxyFlow(t *testing.T) {
	ctx := context.Background()
	var starts []ToolCallStarted
	var attempts []ToolProxyDispatchAttempted
	service := newProxyTestService(t, testAck(&starts), testProxyAck(&attempts), echoRegistration("echo"))

	listed := mustList(t, service)
	if got := strings.Join(toolNames(listed), ","); got != "tool_search,tool_describe,tool_call" {
		t.Fatalf("proxy catalog names = %s", got)
	}

	proxy := callFor(t, listed, "proxy-call", "tool_call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "hello"},
	})
	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-proxy",
		IdempotencyKey: "idem-proxy",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{proxy},
	})
	if failure != nil {
		t.Fatalf("Execute(proxy) failure = %+v", failure)
	}
	if result.CatalogTransition != nil {
		t.Fatalf("proxy returned transition: %+v", result.CatalogTransition)
	}
	got := result.Results[0]
	if got.CallID != "proxy-call" || got.Name != "echo" || got.ToolID == "" || got.Text != "echo: hello" {
		t.Fatalf("proxy result = %+v, want original call id and effective target", got)
	}
	if len(attempts) != 1 || attempts[0].RequestedTargetName != "echo" || attempts[0].EffectiveToolID != got.ToolID {
		t.Fatalf("proxy attempts = %+v", attempts)
	}
	if len(starts) != 1 || starts[0].Name != "echo" || starts[0].ToolID != got.ToolID {
		t.Fatalf("call_started = %+v, want effective target", starts)
	}
}

func TestProxyAttemptRecorderFailurePreventsBackendDispatch(t *testing.T) {
	ctx := context.Background()
	backendCalled := false
	service := newProxyTestService(t,
		func(context.Context, ToolCallStarted) error { return nil },
		func(context.Context, ToolProxyDispatchAttempted) error { return errors.New("recorder down") },
		Registration{
			Provider: "test", ProviderVersion: "0", LocalName: "mutate",
			Name: "mutate", Description: "Mutate test state.",
			InputSchema:  objectSchema(`"value":{"type":"string"}`, "value"),
			Discoverable: true,
			Backend: func(context.Context, BackendRequest) BackendResult {
				backendCalled = true
				return BackendResult{Text: "mutated", SideEffect: SideEffectApplied}
			},
		},
	)
	listed := mustList(t, service)
	proxy := callFor(t, listed, "proxy-call", "tool_call", map[string]any{
		"name":      "mutate",
		"arguments": map[string]any{"value": "x"},
	})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{proxy},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if backendCalled {
		t.Fatalf("backend was called despite failed proxy attempt recorder")
	}
	got := result.Results[0]
	if got.Name != "mutate" || got.Failure == nil || got.Failure.Code != FailureProxyDispatchUnrecorded {
		t.Fatalf("result = %+v, want effective-target proxy recorder failure", got)
	}
}

func TestProxyAttemptEmittedForUnknownAndInvalidNestedArguments(t *testing.T) {
	ctx := context.Background()
	var attempts []ToolProxyDispatchAttempted
	backendCalled := false
	service := newProxyTestService(t, func(context.Context, ToolCallStarted) error { return nil }, testProxyAck(&attempts), Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "strict",
		Name: "strict", Description: "Strict target.",
		InputSchema:  objectSchema(`"value":{"type":"string"}`, "value"),
		Discoverable: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			backendCalled = true
			return BackendResult{Text: "ok"}
		},
	})
	listed := mustList(t, service)
	unknown := callFor(t, listed, "unknown", "tool_call", map[string]any{
		"name":      "missing",
		"arguments": map[string]any{"value": "x"},
	})
	invalid := callFor(t, listed, "invalid", "tool_call", map[string]any{
		"name":      "strict",
		"arguments": map[string]any{"value": 1},
	})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{unknown, invalid},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if backendCalled {
		t.Fatalf("backend was called for invalid proxy cases")
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want two", attempts)
	}
	if attempts[0].RequestedTargetName != "missing" || attempts[0].EffectiveToolID != "" {
		t.Fatalf("unknown attempt = %+v", attempts[0])
	}
	if attempts[1].RequestedTargetName != "strict" || attempts[1].EffectiveToolID == "" {
		t.Fatalf("invalid-args attempt = %+v", attempts[1])
	}
	if result.Results[0].Failure.Code != FailureUnknownTool || result.Results[1].Failure.Code != FailureInvalidArguments {
		t.Fatalf("results = %+v", result.Results)
	}
}

func TestProxyOuterSchemaValidationAfterAttemptPreventsBackend(t *testing.T) {
	ctx := context.Background()
	var attempts []ToolProxyDispatchAttempted
	backendCalled := false
	service := newProxyTestService(t, func(context.Context, ToolCallStarted) error { return nil }, testProxyAck(&attempts), Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "strict",
		Name: "strict", Description: "Strict target.",
		InputSchema:  objectSchema(`"value":{"type":"string"}`, "value"),
		Discoverable: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			backendCalled = true
			return BackendResult{Text: "ok"}
		},
	})
	listed := mustList(t, service)
	extra := callFor(t, listed, "extra", "tool_call", map[string]any{
		"name":       "strict",
		"arguments":  map[string]any{"value": "x"},
		"unexpected": true,
	})
	missingArguments := callFor(t, listed, "missing-args", "tool_call", map[string]any{
		"name": "strict",
	})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{extra, missingArguments},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if backendCalled {
		t.Fatalf("backend was called for invalid outer proxy arguments")
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want two parseable-name attempts", attempts)
	}
	for i, got := range result.Results {
		if got.Name != "strict" || got.Failure == nil || got.Failure.Code != FailureInvalidArguments {
			t.Fatalf("result[%d] = %+v, want effective-target invalid_arguments", i, got)
		}
	}
}

func TestProxyUnparseableNameDoesNotEmitAttempt(t *testing.T) {
	ctx := context.Background()
	var attempts []ToolProxyDispatchAttempted
	service := newProxyTestService(t, func(context.Context, ToolCallStarted) error { return nil }, testProxyAck(&attempts), echoRegistration("echo"))
	listed := mustList(t, service)
	call := callFor(t, listed, "bad-name", "tool_call", map[string]any{
		"name":      123,
		"arguments": map[string]any{"text": "hello"},
	})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{call},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %+v, want none", attempts)
	}
	if got := result.Results[0].Failure; got == nil || got.Code != FailureInvalidArguments {
		t.Fatalf("result = %+v, want invalid_arguments", result.Results[0])
	}
}

func TestTransientUnavailabilityDoesNotChangeCatalog(t *testing.T) {
	ctx := context.Background()
	available := true
	backendCalled := false
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "sometimes",
		Name: "sometimes", Description: "Temporarily unavailable tool.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Discoverable:     true,
		RuntimeAvailable: func(context.Context) bool { return available },
		Backend: func(context.Context, BackendRequest) BackendResult {
			backendCalled = true
			return BackendResult{Text: "ok"}
		},
	})
	first, failure := service.ListTools(ctx, ToolCatalogRequest{ID: "first"})
	if failure != nil {
		t.Fatalf("ListTools() failure = %+v", failure)
	}
	available = false
	second, failure := service.ListTools(ctx, ToolCatalogRequest{ID: "second"})
	if failure != nil {
		t.Fatalf("second ListTools() failure = %+v", failure)
	}
	if first.Catalog.ID != second.Catalog.ID || strings.Join(toolNames(second.Catalog), ",") != "tool_search,tool_describe,tool_load,sometimes" {
		t.Fatalf("catalog changed across outage: first=%+v second=%+v", first.Catalog, second.Catalog)
	}
	call := callFor(t, second.Catalog, "call", "sometimes", map[string]any{"value": "x"})
	result, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "exec", CatalogID: second.Catalog.ID, Calls: []ToolCall{call},
	})
	if execFailure != nil {
		t.Fatalf("Execute() failure = %+v", execFailure)
	}
	if got := result.Results[0].Failure; got == nil || got.Code != FailureToolUnavailable || backendCalled {
		t.Fatalf("unavailable result = %+v, backendCalled=%v", result.Results[0], backendCalled)
	}
}

func TestTemporarilyUnavailableToolCanBeLoaded(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "later",
		Name: "later", Description: "Eligible but temporarily unavailable.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		Discoverable:     true,
		RuntimeAvailable: func(context.Context) bool { return false },
		Backend:          func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "ok"} },
	})
	listed := mustList(t, service)
	load := callFor(t, listed, "load", "tool_load", map[string]any{"name": "later"})
	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "load", IdempotencyKey: "load", CatalogID: listed.ID,
		SessionID: "sess", TurnID: "turn", Calls: []ToolCall{load},
	})
	if failure != nil {
		t.Fatalf("Execute(load) failure = %+v", failure)
	}
	if result.CatalogTransition == nil || strings.Join(toolNames(result.CatalogTransition.Catalog), ",") != "tool_search,tool_describe,tool_load,later" {
		t.Fatalf("load transition = %+v", result.CatalogTransition)
	}
}

func TestFailedLoadDoesNotTransition(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, echoRegistration("echo"))
	listed := mustList(t, service)
	load := callFor(t, listed, "missing", "tool_load", map[string]any{"name": "missing"})
	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "missing", IdempotencyKey: "missing", CatalogID: listed.ID,
		SessionID: "sess", TurnID: "turn", Calls: []ToolCall{load},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if result.CatalogTransition != nil {
		t.Fatalf("failed load returned transition: %+v", result.CatalogTransition)
	}
}

func TestCallStartedAckFailurePreventsBackendDispatch(t *testing.T) {
	ctx := context.Background()
	called := false
	service := newTestService(t, func(context.Context, ToolCallStarted) error {
		return errors.New("event store down")
	}, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "mutate",
		Name: "mutate", Description: "Mutate test state.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			called = true
			return BackendResult{Text: "mutated", SideEffect: SideEffectApplied}
		},
	})
	listed := mustList(t, service)
	call := callFor(t, listed, "call", "mutate", map[string]any{"value": "x"})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "idem",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{call},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if called {
		t.Fatalf("backend was called despite failed call_started ack")
	}
	got := result.Results[0]
	if got.Failure == nil || got.Failure.Code != FailureCallStartedUnacknowledged || got.SideEffect != SideEffectNone {
		t.Fatalf("result = %+v, want unacknowledged failure + no side effect", got)
	}
}

func TestMalformedBackendResultPreservesSideEffectEvidence(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "malformed",
		Name: "malformed", Description: "Return a malformed result.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			return BackendResult{SideEffect: SideEffectApplied}
		},
	})
	listed := mustList(t, service)
	call := callFor(t, listed, "call", "malformed", map[string]any{"value": "x"})
	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{call},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	got := result.Results[0]
	if got.Failure == nil || got.Failure.Code != FailureMalformedResult || got.SideEffect != SideEffectApplied {
		t.Fatalf("result = %+v, want malformed_result + applied", got)
	}
}

func TestBackendPanicBecomesUnknownSideEffect(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "panic",
		Name: "panic", Description: "Panic after starting.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			panic("boom")
		},
	})
	listed := mustList(t, service)
	call := callFor(t, listed, "call", "panic", map[string]any{"value": "x"})
	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "exec", IdempotencyKey: "idem", CatalogID: listed.ID, Calls: []ToolCall{call},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	got := result.Results[0]
	if got.Failure == nil || got.Failure.Code != FailureBackendFailed || got.SideEffect != SideEffectUnknown {
		t.Fatalf("result = %+v, want backend_failed + unknown", got)
	}
}

func TestNestedSchemaValidation(t *testing.T) {
	tests := []struct {
		name       string
		schema     []byte
		args       map[string]any
		wantStatus ToolResultStatus
		wantError  string
	}{
		{
			name: "valid nested object",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"customer":{"type":"object","additionalProperties":false,"properties":{
					"id":{"type":"string"},
					"address":{"type":"object","additionalProperties":false,"properties":{"city":{"type":"string"},"zip":{"type":"integer"}},"required":["city","zip"]}
				},"required":["id","address"]}
			},"required":["customer"]}`),
			args:       map[string]any{"customer": map[string]any{"id": "C-7", "address": map[string]any{"city": "Paris", "zip": 75001}}},
			wantStatus: ResultSucceeded,
		},
		{
			name: "missing nested required property",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"customer":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"address":{"type":"object","additionalProperties":false,"properties":{"city":{"type":"string"}},"required":["city"]}},"required":["id","address"]}
			},"required":["customer"]}`),
			args:      map[string]any{"customer": map[string]any{"id": "C-7", "address": map[string]any{}}},
			wantError: `missing required argument "customer.address.city"`,
		},
		{
			name: "nested additionalProperties false",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"customer":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"]}
			},"required":["customer"]}`),
			args:      map[string]any{"customer": map[string]any{"id": "C-7", "extra": true}},
			wantError: `unknown argument "customer.extra"`,
		},
		{
			name: "valid array of objects",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"order":{"type":"object","additionalProperties":false,"properties":{
					"items":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"sku":{"type":"string"},"quantity":{"type":"integer"}},"required":["sku","quantity"]}}
				},"required":["items"]}
			},"required":["order"]}`),
			args:       map[string]any{"order": map[string]any{"items": []any{map[string]any{"sku": "SKU-1", "quantity": 2}, map[string]any{"sku": "SKU-2", "quantity": 3}}}},
			wantStatus: ResultSucceeded,
		},
		{
			name: "invalid field inside array element",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"order":{"type":"object","additionalProperties":false,"properties":{
					"items":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"sku":{"type":"string"},"quantity":{"type":"integer"}},"required":["sku","quantity"]}}
				},"required":["items"]}
			},"required":["order"]}`),
			args:      map[string]any{"order": map[string]any{"items": []any{map[string]any{"sku": "SKU-1", "quantity": 2}, map[string]any{"sku": "SKU-2", "quantity": "3"}}}},
			wantError: `argument "order.items[1].quantity" must be integer`,
		},
		{
			name: "incorrect deeply nested type",
			schema: []byte(`{"type":"object","additionalProperties":false,"properties":{
				"order":{"type":"object","additionalProperties":false,"properties":{"delivery":{"type":"object","additionalProperties":false,"properties":{"priority":{"type":"boolean"}},"required":["priority"]}},"required":["delivery"]}
			},"required":["order"]}`),
			args:      map[string]any{"order": map[string]any{"delivery": map[string]any{"priority": "true"}}},
			wantError: `argument "order.delivery.priority" must be boolean`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
				Provider: "test", ProviderVersion: "0", LocalName: "nested",
				Name: "nested", Description: "Nested schema target.",
				InputSchema:      tt.schema,
				InitiallyVisible: true,
				Backend: func(context.Context, BackendRequest) BackendResult {
					return BackendResult{Text: "ok", SideEffect: SideEffectNone}
				},
			})
			catalog := mustList(t, service)
			result, failure := service.Execute(context.Background(), ToolExecutionRequest{
				ID:             "exec",
				IdempotencyKey: "idem",
				CatalogID:      catalog.ID,
				Calls:          []ToolCall{callFor(t, catalog, "call", "nested", tt.args)},
			})
			if failure != nil {
				t.Fatalf("Execute() failure = %+v", failure)
			}
			got := result.Results[0]
			if tt.wantStatus == ResultSucceeded {
				if got.Status != ResultSucceeded {
					t.Fatalf("result = %+v, want succeeded", got)
				}
				return
			}
			if got.Failure == nil || got.Failure.Code != FailureInvalidArguments || got.Text != tt.wantError {
				t.Fatalf("result = %+v, want invalid_arguments %q", got, tt.wantError)
			}
		})
	}
}

func TestUnsupportedNestedSchemaRejectedAtRegistration(t *testing.T) {
	tests := []struct {
		name   string
		schema []byte
	}{
		{
			name:   "unsupported enum",
			schema: []byte(`{"type":"object","properties":{"value":{"type":"string","enum":["a"]}}}`),
		},
		{
			name:   "array requires items",
			schema: []byte(`{"type":"object","properties":{"values":{"type":"array"}}}`),
		},
		{
			name:   "tuple array rejected",
			schema: []byte(`{"type":"object","properties":{"values":{"type":"array","items":[{"type":"string"}]}}}`),
		},
		{
			name:   "nested bounds rejected",
			schema: []byte(`{"type":"object","properties":{"values":{"type":"array","items":{"type":"string"},"minItems":1}}}`),
		},
		{
			name:   "null items on object rejected",
			schema: []byte(`{"type":"object","items":null}`),
		},
		{
			name:   "null object keyword on scalar rejected",
			schema: []byte(`{"type":"object","properties":{"value":{"type":"string","properties":null}}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(Options{AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error { return nil }}, Registration{
				Provider: "test", ProviderVersion: "0", LocalName: "bad",
				Name: "bad", Description: "Bad schema.",
				InputSchema:      tt.schema,
				InitiallyVisible: true,
				Backend:          func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "ok"} },
			})
			if err == nil {
				t.Fatalf("NewService() error = nil, want schema rejection")
			}
		})
	}
}

func TestSchemaValidationErrorsAreDeterministic(t *testing.T) {
	schema, _, err := compileSchema([]byte(`{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"},"z":{"type":"string"}},"required":["z","a"]}`))
	if err != nil {
		t.Fatalf("compileSchema() error = %v", err)
	}
	for range 100 {
		if got := schema.validate(map[string]any{}); got == nil || got.Error() != `missing required argument "a"` {
			t.Fatalf("required error = %v", got)
		}
		if got := schema.validate(map[string]any{"a": "ok", "z": "ok", "y": true, "b": true}); got == nil || got.Error() != `unknown argument "b"` {
			t.Fatalf("unknown error = %v", got)
		}
	}
}

func TestIdempotencyReplaysSamePayloadAndRejectsConflict(t *testing.T) {
	ctx := context.Background()
	calls := 0
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "count",
		Name: "count", Description: "Count backend calls.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			calls++
			return BackendResult{Text: "counted", SideEffect: SideEffectApplied}
		},
	})
	listed := mustList(t, service)
	req := ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "same",
		CatalogID:      listed.ID,
		SessionID:      "sess",
		Calls:          []ToolCall{callFor(t, listed, "call", "count", map[string]any{"value": "x"})},
	}
	if _, failure := service.Execute(ctx, req); failure != nil {
		t.Fatalf("first Execute() failure = %+v", failure)
	}
	if _, failure := service.Execute(ctx, req); failure != nil {
		t.Fatalf("second Execute() failure = %+v", failure)
	}
	if calls != 1 {
		t.Fatalf("backend calls = %d, want 1", calls)
	}

	conflict := req
	conflict.Calls[0].Arguments["value"] = "different"
	_, failure := service.Execute(ctx, conflict)
	if failure == nil || failure.Code != FailureIdempotencyConflict {
		t.Fatalf("conflict failure = %+v, want %s", failure, FailureIdempotencyConflict)
	}
	if failure.Retryable {
		t.Fatalf("idempotency conflict was marked retryable")
	}
	if calls != 1 {
		t.Fatalf("backend calls after conflict = %d, want 1", calls)
	}
}

func TestValidationFailuresDoNotDispatch(t *testing.T) {
	ctx := context.Background()
	calls := 0
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil }, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "strict",
		Name: "strict", Description: "Strict args.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			calls++
			return BackendResult{Text: "ok", SideEffect: SideEffectNone}
		},
	})
	listed := mustList(t, service)
	valid := callFor(t, listed, "bad", "strict", map[string]any{"value": 3})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-invalid",
		IdempotencyKey: "idem-invalid",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{valid},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if calls != 0 {
		t.Fatalf("backend calls = %d, want 0", calls)
	}
	if got := result.Results[0].Failure.Code; got != FailureInvalidArguments {
		t.Fatalf("failure code = %q, want %q", got, FailureInvalidArguments)
	}

	stale := callFor(t, listed, "stale", "strict", map[string]any{"value": "x"})
	stale.DefinitionRevision = "old"
	result, failure = service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-stale",
		IdempotencyKey: "idem-stale",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{stale},
	})
	if failure != nil {
		t.Fatalf("Execute(stale) failure = %+v", failure)
	}
	if got := result.Results[0].Failure.Code; got != FailureStaleToolDefinition {
		t.Fatalf("failure code = %q, want %q", got, FailureStaleToolDefinition)
	}
	if calls != 0 {
		t.Fatalf("backend calls after stale = %d, want 0", calls)
	}
}

func TestCatalogCacheEvictionMakesLoadUnavailable(t *testing.T) {
	ctx := context.Background()
	service := newTestServiceWithOptions(t, Options{
		CatalogCacheCapacity:   1,
		AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error { return nil },
	}, echoRegistration("echo"))

	first := mustList(t, service)
	if err := service.Register(Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "visible",
		Name: "visible", Description: "Newly registered visible tool.",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend:          func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "visible"} },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	second, failure := service.ListTools(ctx, ToolCatalogRequest{ID: "second"})
	if failure != nil {
		t.Fatalf("second ListTools() failure = %+v", failure)
	}
	if second.Catalog.ID == first.ID {
		t.Fatalf("second catalog id matched first; eviction setup did not produce a new catalog")
	}
	load := callFor(t, first, "load", "tool_load", map[string]any{"name": "echo"})
	result, execFailure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec-load",
		IdempotencyKey: "idem-load",
		CatalogID:      first.ID,
		SessionID:      "sess",
		TurnID:         "turn",
		Calls:          []ToolCall{load},
	})
	if execFailure != nil {
		t.Fatalf("Execute(load) failure = %+v", execFailure)
	}
	if got := result.Results[0].Failure.Code; got != FailureCatalogUnavailable {
		t.Fatalf("load failure code = %q, want %q", got, FailureCatalogUnavailable)
	}
	if result.CatalogTransition != nil {
		t.Fatalf("evicted base produced transition: %+v", result.CatalogTransition)
	}
}

func TestCatalogChangingRequestsRequireAndSerializeLineage(t *testing.T) {
	ctx := context.Background()
	block := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	ack := func(context.Context, ToolCallStarted) error {
		startOnce.Do(func() { close(started) })
		<-block
		return nil
	}
	service := newTestService(t, ack, echoRegistration("echo"))
	listed := mustList(t, service)
	load := callFor(t, listed, "load", "tool_load", map[string]any{"name": "echo"})

	_, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "missing-lineage",
		IdempotencyKey: "missing-lineage",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{load},
	})
	if failure == nil || failure.Code != FailureInvalidRequest {
		t.Fatalf("missing lineage failure = %+v, want invalid_request", failure)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = service.Execute(ctx, ToolExecutionRequest{
			ID:             "first",
			IdempotencyKey: "first",
			CatalogID:      listed.ID,
			SessionID:      "sess",
			TurnID:         "turn",
			Calls:          []ToolCall{load},
		})
	}()
	<-started

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		second := load
		second.ID = "load-2"
		_, _ = service.Execute(ctx, ToolExecutionRequest{
			ID:             "second",
			IdempotencyKey: "second",
			CatalogID:      listed.ID,
			SessionID:      "sess",
			TurnID:         "turn",
			Calls:          []ToolCall{second},
		})
	}()

	select {
	case <-secondDone:
		t.Fatalf("second catalog-changing request was not serialized")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	<-done
	<-secondDone
}

func TestRegistrationRejectsUnsupportedSchemasAndBadIDs(t *testing.T) {
	_, err := NewService(Options{AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error { return nil }}, Registration{
		Provider: "test:bad", ProviderVersion: "0", LocalName: "x",
		Name: "x", Description: "bad id",
		InputSchema: objectSchema(`"value":{"type":"string"}`, "value"),
		Backend:     func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "x"} },
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain ':'") {
		t.Fatalf("bad id error = %v", err)
	}

	_, err = NewService(Options{AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error { return nil }}, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "x",
		Name: "x", Description: "unsupported schema",
		InputSchema: []byte(`{"type":"object","properties":{"value":{"type":"string","description":"not in subset"}}}`),
		Backend:     func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "x"} },
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported schema keyword "description"`) {
		t.Fatalf("unsupported schema error = %v", err)
	}

	_, err = NewService(Options{AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error { return nil }}, Registration{
		Provider: "test", ProviderVersion: "0", LocalName: "trailing",
		Name: "trailing", Description: "trailing JSON",
		InputSchema: []byte(`{"type":"object"}{"type":"object"}`),
		Backend:     func(context.Context, BackendRequest) BackendResult { return BackendResult{Text: "x"} },
	})
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing schema error = %v", err)
	}
}

func TestRequestValidationAndOrdering(t *testing.T) {
	ctx := context.Background()
	var seen []string
	var mu sync.Mutex
	service := newTestService(t, func(context.Context, ToolCallStarted) error { return nil },
		orderedRegistration("a", &mu, &seen),
		orderedRegistration("b", &mu, &seen),
	)
	listed := mustList(t, service)
	a := callFor(t, listed, "a-call", "a", map[string]any{"value": "1"})
	b := callFor(t, listed, "b-call", "b", map[string]any{"value": "2"})

	result, failure := service.Execute(ctx, ToolExecutionRequest{
		ID:             "exec",
		IdempotencyKey: "idem",
		CatalogID:      listed.ID,
		Mode:           ExecutionAllowParallel,
		Calls:          []ToolCall{a, b},
	})
	if failure != nil {
		t.Fatalf("Execute() failure = %+v", failure)
	}
	if result.Results[0].CallID != "a-call" || result.Results[1].CallID != "b-call" {
		t.Fatalf("result order = %+v", result.Results)
	}
	if strings.Join(seen, ",") != "a,b" {
		t.Fatalf("backend order = %v, want a,b", seen)
	}

	dup := b
	dup.ID = a.ID
	_, failure = service.Execute(ctx, ToolExecutionRequest{
		ID:             "dup",
		IdempotencyKey: "dup",
		CatalogID:      listed.ID,
		Calls:          []ToolCall{a, dup},
	})
	if failure == nil || failure.Code != FailureDuplicateCallID {
		t.Fatalf("duplicate failure = %+v, want duplicate_call_id", failure)
	}
}

func newTestService(t *testing.T, ack CallStartedAcknowledger, regs ...Registration) *Service {
	t.Helper()
	return newTestServiceWithOptions(t, Options{AcknowledgeCallStarted: ack}, regs...)
}

func newTestServiceWithOptions(t *testing.T, opts Options, regs ...Registration) *Service {
	t.Helper()
	service, err := NewService(opts, regs...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func newProxyTestService(t *testing.T, ack CallStartedAcknowledger, proxyAck ProxyDispatchAcknowledger, regs ...Registration) *Service {
	t.Helper()
	return newTestServiceWithOptions(t, Options{
		DiscoveryStrategy:        DiscoveryProxy,
		AcknowledgeCallStarted:   ack,
		AcknowledgeProxyDispatch: proxyAck,
	}, regs...)
}

func testAck(starts *[]ToolCallStarted) CallStartedAcknowledger {
	return func(_ context.Context, event ToolCallStarted) error {
		*starts = append(*starts, event)
		return nil
	}
}

func testProxyAck(attempts *[]ToolProxyDispatchAttempted) ProxyDispatchAcknowledger {
	return func(_ context.Context, event ToolProxyDispatchAttempted) error {
		*attempts = append(*attempts, event)
		return nil
	}
}

func echoRegistration(name string) Registration {
	return Registration{
		Provider: "test", ProviderVersion: "0", LocalName: name,
		Name: name, Description: "Echo input text.",
		InputSchema:  objectSchema(`"text":{"type":"string"}`, "text"),
		Discoverable: true,
		Backend: func(_ context.Context, req BackendRequest) BackendResult {
			return BackendResult{Text: "echo: " + req.Arguments["text"].(string), SideEffect: SideEffectNone}
		},
	}
}

func orderedRegistration(name string, mu *sync.Mutex, seen *[]string) Registration {
	return Registration{
		Provider: "test", ProviderVersion: "0", LocalName: name,
		Name: name, Description: "Ordered backend " + name + ".",
		InputSchema:      objectSchema(`"value":{"type":"string"}`, "value"),
		InitiallyVisible: true,
		Backend: func(context.Context, BackendRequest) BackendResult {
			mu.Lock()
			*seen = append(*seen, name)
			mu.Unlock()
			return BackendResult{Text: name, SideEffect: SideEffectNone}
		},
	}
}

func objectSchema(properties string, required ...string) []byte {
	var quoted []string
	for _, name := range required {
		quoted = append(quoted, `"`+name+`"`)
	}
	return []byte(`{"additionalProperties":false,"properties":{` + properties + `},"required":[` + strings.Join(quoted, ",") + `],"type":"object"}`)
}

func mustList(t *testing.T, service *Service) ToolCatalog {
	t.Helper()
	listed, failure := service.ListTools(context.Background(), ToolCatalogRequest{ID: "list-" + time.Now().String()})
	if failure != nil {
		t.Fatalf("ListTools() failure = %+v", failure)
	}
	return listed.Catalog
}

func callFor(t *testing.T, catalog ToolCatalog, id, name string, args map[string]any) ToolCall {
	t.Helper()
	for _, def := range catalog.Tools {
		if def.Name == name {
			return ToolCall{
				ID:                 id,
				ToolID:             def.ID,
				DefinitionRevision: def.Revision,
				Name:               def.Name,
				Arguments:          args,
			}
		}
	}
	t.Fatalf("catalog missing tool %q in %+v", name, toolNames(catalog))
	return ToolCall{}
}

func toolNames(catalog ToolCatalog) []string {
	names := make([]string, 0, len(catalog.Tools))
	for _, def := range catalog.Tools {
		names = append(names, def.Name)
	}
	return names
}
