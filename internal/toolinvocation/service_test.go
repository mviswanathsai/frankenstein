package toolinvocation

import (
	"context"
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
	if err == nil || !strings.Contains(err.Error(), "unsupported property schema keyword") {
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

func testAck(starts *[]ToolCallStarted) CallStartedAcknowledger {
	return func(_ context.Context, event ToolCallStarted) error {
		*starts = append(*starts, event)
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
