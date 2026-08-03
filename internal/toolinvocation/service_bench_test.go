package toolinvocation

import (
	"context"
	"strconv"
	"testing"
)

func BenchmarkSteadyStateDirectTargetExecution(b *testing.B) {
	ctx := context.Background()
	service := benchService(b, DiscoveryDirect)
	base := benchList(b, service)
	loaded := benchLoadEcho(b, ctx, service, base, "setup")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		call := benchCall(b, loaded, "echo", "echo", map[string]any{"text": "hello"})
		req := ToolExecutionRequest{
			ID: "exec", IdempotencyKey: "direct-steady-" + strconv.Itoa(i), CatalogID: loaded.ID, Calls: []ToolCall{call},
		}
		if _, failure := service.Execute(ctx, req); failure != nil {
			b.Fatalf("execute failed: %+v", failure)
		}
	}
}

func BenchmarkSteadyStateProxyTargetExecution(b *testing.B) {
	ctx := context.Background()
	service := benchService(b, DiscoveryProxy)
	catalog := benchList(b, service)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		call := benchProxyEchoCall(b, catalog)
		req := ToolExecutionRequest{
			ID: "exec", IdempotencyKey: "proxy-steady-" + strconv.Itoa(i), CatalogID: catalog.ID, Calls: []ToolCall{call},
		}
		if _, failure := service.Execute(ctx, req); failure != nil {
			b.Fatalf("execute failed: %+v", failure)
		}
	}
}

func BenchmarkOneShotDirectLoadThenTargetExecution(b *testing.B) {
	ctx := context.Background()
	service := benchService(b, DiscoveryDirect)
	base := benchList(b, service)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded := benchLoadEcho(b, ctx, service, base, "oneshot-"+strconv.Itoa(i))
		call := benchCall(b, loaded, "echo", "echo", map[string]any{"text": "hello"})
		req := ToolExecutionRequest{
			ID: "exec", IdempotencyKey: "direct-oneshot-exec-" + strconv.Itoa(i), CatalogID: loaded.ID, Calls: []ToolCall{call},
		}
		if _, failure := service.Execute(ctx, req); failure != nil {
			b.Fatalf("execute failed: %+v", failure)
		}
	}
}

func BenchmarkOneShotProxyTargetExecution(b *testing.B) {
	ctx := context.Background()
	service := benchService(b, DiscoveryProxy)
	catalog := benchList(b, service)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		call := benchProxyEchoCall(b, catalog)
		req := ToolExecutionRequest{
			ID: "exec", IdempotencyKey: "proxy-oneshot-" + strconv.Itoa(i), CatalogID: catalog.ID, Calls: []ToolCall{call},
		}
		if _, failure := service.Execute(ctx, req); failure != nil {
			b.Fatalf("execute failed: %+v", failure)
		}
	}
}

func benchService(b *testing.B, strategy DiscoveryStrategy) *Service {
	b.Helper()
	service, err := NewService(Options{
		DiscoveryStrategy: strategy,
		AcknowledgeCallStarted: func(context.Context, ToolCallStarted) error {
			return nil
		},
		AcknowledgeProxyDispatch: func(context.Context, ToolProxyDispatchAttempted) error {
			return nil
		},
	}, echoRegistration("echo"))
	if err != nil {
		b.Fatalf("NewService() error = %v", err)
	}
	return service
}

func benchList(b *testing.B, service *Service) ToolCatalog {
	b.Helper()
	listed, failure := service.ListTools(context.Background(), ToolCatalogRequest{ID: "list"})
	if failure != nil {
		b.Fatalf("ListTools() failure = %+v", failure)
	}
	return listed.Catalog
}

func benchLoadEcho(b *testing.B, ctx context.Context, service *Service, base ToolCatalog, suffix string) ToolCatalog {
	b.Helper()
	load := benchCall(b, base, "load", "tool_load", map[string]any{"name": "echo"})
	loadedResult, failure := service.Execute(ctx, ToolExecutionRequest{
		ID: "load", IdempotencyKey: "direct-load-" + suffix, CatalogID: base.ID, SessionID: "sess", TurnID: "turn", Calls: []ToolCall{load},
	})
	if failure != nil || loadedResult.CatalogTransition == nil {
		b.Fatalf("load failed: result=%+v failure=%+v", loadedResult, failure)
	}
	return loadedResult.CatalogTransition.Catalog
}

func benchProxyEchoCall(b *testing.B, catalog ToolCatalog) ToolCall {
	b.Helper()
	return benchCall(b, catalog, "proxy", "tool_call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "hello"},
	})
}

func benchCall(b *testing.B, catalog ToolCatalog, id, name string, args map[string]any) ToolCall {
	b.Helper()
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
	b.Fatalf("missing tool %q", name)
	return ToolCall{}
}
