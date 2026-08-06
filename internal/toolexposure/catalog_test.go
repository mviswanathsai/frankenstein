package toolexposure

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"frankenstein/internal/toolinvocation"
)

func TestSeedCopiesCallableDefinitionsInOrderWithExposureID(t *testing.T) {
	callable := testCallableCatalog(testDef("id:a", "rev:a", "a"), testDef("id:b", "rev:b", "b"))

	seeded, err := Seed(callable)
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if !strings.HasPrefix(seeded.ID, "toolexp_") {
		t.Fatalf("exposure id = %q", seeded.ID)
	}
	if got := toolNames(seeded.Tools); strings.Join(got, ",") != "a,b" {
		t.Fatalf("seed order = %v", got)
	}
	callable.Tools[0].InputSchema[0] = '['
	if seeded.Tools[0].InputSchema[0] != '{' {
		t.Fatalf("seed aliases callable schema")
	}
}

func TestRepeatedSeedContentHasSameID(t *testing.T) {
	first, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	second, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %s != %s", first.ID, second.ID)
	}
}

func TestAdvanceAppendRepeatAndReplace(t *testing.T) {
	base, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	appended, err := Advance(base, []toolinvocation.ToolDefinition{testDef("id:b", "rev:b", "b")})
	if err != nil {
		t.Fatalf("Advance(append) error = %v", err)
	}
	if got := toolNames(appended.Tools); strings.Join(got, ",") != "a,b" {
		t.Fatalf("append order = %v", got)
	}
	repeated, err := Advance(appended, []toolinvocation.ToolDefinition{testDef("id:b", "rev:b", "b")})
	if err != nil {
		t.Fatalf("Advance(repeat) error = %v", err)
	}
	if repeated.ID != appended.ID {
		t.Fatalf("repeat changed id: %s -> %s", appended.ID, repeated.ID)
	}
	replacement := testDef("id:a", "rev:a2", "a")
	replacement.Description = "Updated tool a."
	replacement.Revision = mustDefinitionRevision(t, replacement)
	replaced, err := Advance(appended, []toolinvocation.ToolDefinition{replacement})
	if err != nil {
		t.Fatalf("Advance(replace) error = %v", err)
	}
	if got := toolNames(replaced.Tools); strings.Join(got, ",") != "a,b" {
		t.Fatalf("replace order = %v", got)
	}
	if replaced.Tools[0].Revision != replacement.Revision || replaced.ID == appended.ID {
		t.Fatalf("replace did not update first revision/id: %+v", replaced)
	}
}

func TestAdvanceRejectsNameCollisionAndBadBase(t *testing.T) {
	base, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if _, err := Advance(base, []toolinvocation.ToolDefinition{testDef("id:b", "rev:b", "a")}); err == nil {
		t.Fatalf("Advance() error = nil, want name collision")
	}
	base.ID = "toolexp_bad"
	if _, err := Advance(base, []toolinvocation.ToolDefinition{testDef("id:b", "rev:b", "b")}); err == nil {
		t.Fatalf("Advance() error = nil, want bad base id")
	}
}

func TestInputOutputMutationDoesNotAlias(t *testing.T) {
	delivered := testDef("id:b", "rev:b", "b")
	base, _ := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	baseBytes := append([]byte(nil), base.Tools[0].InputSchema...)
	next, err := Advance(base, []toolinvocation.ToolDefinition{delivered})
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	delivered.InputSchema[0] = '['
	next.Tools[1].InputSchema[0] = '['
	again, err := Advance(base, []toolinvocation.ToolDefinition{testDef("id:b", "rev:b", "b")})
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if again.Tools[1].InputSchema[0] != '{' {
		t.Fatalf("catalog aliases caller/output mutation")
	}
	if !bytes.Equal(base.Tools[0].InputSchema, baseBytes) {
		t.Fatalf("Advance mutated base catalog definition bytes")
	}
}

func TestAdvanceAndDecodeRejectStaleDefinitionRevision(t *testing.T) {
	catalog, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	stale := catalog.Tools[0]
	stale.Description = "Changed without revising."
	if _, err := Advance(catalog, []toolinvocation.ToolDefinition{stale}); err == nil {
		t.Fatalf("Advance() error = nil, want stale revision rejection")
	}
	encoded, err := Encode(catalog)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	encoded = bytes.Replace(encoded, []byte(`"description":"Tool a."`), []byte(`"description":"Changed without revising."`), 1)
	if _, err := Decode(encoded); err == nil {
		t.Fatalf("Decode() error = nil, want stale revision rejection")
	}
}

func TestSeedRejectsSchemaToolInvocationRejects(t *testing.T) {
	def := testDef("id:bad", "rev:bad", "bad")
	def.InputSchema = json.RawMessage(`{"type":"object","properties":{"values":{"type":"array"}}}`)
	if _, err := Seed(toolinvocation.ToolCatalog{ID: "toolcat_invalid", Tools: []toolinvocation.ToolDefinition{def}}); err == nil {
		t.Fatalf("Seed() error = nil, want invalid schema rejection")
	}
}

func TestNilAndEmptyToolsEncodeIdentically(t *testing.T) {
	empty, err := catalogWithID([]toolinvocation.ToolDefinition{})
	if err != nil {
		t.Fatalf("catalogWithID() error = %v", err)
	}
	nilCatalog := ToolExposureCatalog{ID: empty.ID}
	emptyCatalog := ToolExposureCatalog{ID: empty.ID, Tools: []toolinvocation.ToolDefinition{}}
	nilEncoded, err := Encode(nilCatalog)
	if err != nil {
		t.Fatalf("Encode(nil) error = %v", err)
	}
	emptyEncoded, err := Encode(emptyCatalog)
	if err != nil {
		t.Fatalf("Encode(empty) error = %v", err)
	}
	if !bytes.Equal(nilEncoded, emptyEncoded) || nilCatalog.ID != emptyCatalog.ID {
		t.Fatalf("nil and empty catalogs differ: %s != %s", nilEncoded, emptyEncoded)
	}
}

func TestEquivalentCanonicalSchemaJSONProducesSameIdentity(t *testing.T) {
	a := testDef("id:a", "rev:a", "a")
	b := testDef("id:a", "rev:a", "a")
	a.InputSchema = json.RawMessage(`{"required":["value"],"properties":{"value":{"type":"string"}},"additionalProperties":false,"type":"object"}`)
	b.InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string"}},"required":["value"]}`)
	a.Revision = mustDefinitionRevision(t, a)
	b.Revision = mustDefinitionRevision(t, b)
	first, err := Seed(testCallableCatalog(a))
	if err != nil {
		t.Fatalf("Seed(first) error = %v", err)
	}
	second, err := Seed(testCallableCatalog(b))
	if err != nil {
		t.Fatalf("Seed(second) error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("canonical-equivalent schema ids differ: %s != %s", first.ID, second.ID)
	}
}

func TestEncodeDecodeRoundTripAndRejectsBadArtifacts(t *testing.T) {
	catalog, err := Seed(testCallableCatalog(testDef("id:a", "rev:a", "a")))
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	encoded, err := Encode(catalog)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.ID != catalog.ID || string(decoded.Tools[0].InputSchema) != string(catalog.Tools[0].InputSchema) {
		t.Fatalf("decoded = %+v, want %+v", decoded, catalog)
	}
	decoded.Tools[0].InputSchema[0] = '['
	if catalog.Tools[0].InputSchema[0] != '{' {
		t.Fatalf("decode aliases encoded catalog")
	}

	cases := map[string][]byte{
		"malformed":      []byte(`{`),
		"trailing":       append(encoded, []byte(` {}`)...),
		"wrong kind":     bytes.Replace(encoded, []byte(`tool_exposure_catalog`), []byte(`other_artifact`), 1),
		"wrong version":  bytes.Replace(encoded, []byte(`"version":"0"`), []byte(`"version":"1"`), 1),
		"digest changed": bytes.Replace(encoded, []byte(`"name":"a"`), []byte(`"name":"z"`), 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); err == nil {
				t.Fatalf("Decode() error = nil")
			}
		})
	}
}

func TestProxyDescribeAdvancesExposureWithoutChangingCallableCatalog(t *testing.T) {
	var starts []toolinvocation.ToolCallStarted
	var attempts []toolinvocation.ToolProxyDispatchAttempted
	service := newProxyTestService(t, testAck(&starts), testProxyAck(&attempts), echoRegistration("echo"))
	ctx := context.Background()

	listed, failure := service.ListTools(ctx, toolinvocation.ToolCatalogRequest{ID: "list"})
	if failure != nil {
		t.Fatalf("ListTools() failure = %+v", failure)
	}
	callable := listed.Catalog
	describe := callFor(t, callable, "describe", "tool_describe", map[string]any{"name": "echo"})
	describeResult, execFailure := service.Execute(ctx, toolinvocation.ToolExecutionRequest{
		ID:             "describe",
		IdempotencyKey: "describe",
		CatalogID:      callable.ID,
		Calls:          []toolinvocation.ToolCall{describe},
	})
	if execFailure != nil {
		t.Fatalf("Execute(describe) failure = %+v", execFailure)
	}
	after, failure := service.ListTools(ctx, toolinvocation.ToolCatalogRequest{ID: "after"})
	if failure != nil {
		t.Fatalf("ListTools(after) failure = %+v", failure)
	}
	if after.Catalog.ID != callable.ID || containsTool(after.Catalog.Tools, "echo") {
		t.Fatalf("callable catalog changed after describe: before=%+v after=%+v", callable, after.Catalog)
	}

	exposure, err := Seed(callable)
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if containsTool(exposure.Tools, "echo") {
		t.Fatalf("seed exposure already contains target")
	}
	exposure, err = Advance(exposure, []toolinvocation.ToolDefinition{*describeResult.Results[0].DescribedTool})
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if !containsTool(exposure.Tools, "echo") || containsTool(after.Catalog.Tools, "echo") {
		t.Fatalf("exposure/callable divergence failed: exposure=%+v callable=%+v", exposure, after.Catalog)
	}

	proxy := callFor(t, after.Catalog, "proxy", "tool_call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "hello"}})
	proxyResult, execFailure := service.Execute(ctx, toolinvocation.ToolExecutionRequest{
		ID:             "proxy",
		IdempotencyKey: "proxy",
		CatalogID:      after.Catalog.ID,
		Calls:          []toolinvocation.ToolCall{proxy},
	})
	if execFailure != nil {
		t.Fatalf("Execute(proxy) failure = %+v", execFailure)
	}
	if proxyResult.CatalogTransition != nil || proxyResult.Results[0].Name != "echo" || proxyResult.Results[0].Status != toolinvocation.ResultSucceeded {
		t.Fatalf("proxy result = %+v", proxyResult)
	}
}

func newProxyTestService(t *testing.T, ack toolinvocation.CallStartedAcknowledger, proxyAck toolinvocation.ProxyDispatchAcknowledger, regs ...toolinvocation.Registration) *toolinvocation.Service {
	t.Helper()
	service, err := toolinvocation.NewService(toolinvocation.Options{
		DiscoveryStrategy:        toolinvocation.DiscoveryProxy,
		AcknowledgeCallStarted:   ack,
		AcknowledgeProxyDispatch: proxyAck,
	}, regs...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func testAck(starts *[]toolinvocation.ToolCallStarted) toolinvocation.CallStartedAcknowledger {
	return func(_ context.Context, event toolinvocation.ToolCallStarted) error {
		*starts = append(*starts, event)
		return nil
	}
}

func testProxyAck(attempts *[]toolinvocation.ToolProxyDispatchAttempted) toolinvocation.ProxyDispatchAcknowledger {
	return func(_ context.Context, event toolinvocation.ToolProxyDispatchAttempted) error {
		*attempts = append(*attempts, event)
		return nil
	}
}

func echoRegistration(name string) toolinvocation.Registration {
	return toolinvocation.Registration{
		Provider: "test", ProviderVersion: "0", LocalName: name,
		Name: name, Description: "Echo input text.",
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"]}`),
		Discoverable: true,
		Backend: func(_ context.Context, req toolinvocation.BackendRequest) toolinvocation.BackendResult {
			return toolinvocation.BackendResult{Text: "echo: " + req.Arguments["text"].(string), SideEffect: toolinvocation.SideEffectNone}
		},
	}
}

func callFor(t *testing.T, catalog toolinvocation.ToolCatalog, id, name string, args map[string]any) toolinvocation.ToolCall {
	t.Helper()
	for _, def := range catalog.Tools {
		if def.Name == name {
			return toolinvocation.ToolCall{
				ID:                 id,
				ToolID:             def.ID,
				DefinitionRevision: def.Revision,
				Name:               def.Name,
				Arguments:          args,
			}
		}
	}
	t.Fatalf("catalog missing tool %q", name)
	return toolinvocation.ToolCall{}
}

func testDef(id, revision, name string) toolinvocation.ToolDefinition {
	def := toolinvocation.ToolDefinition{
		ID:          id,
		Revision:    revision,
		Name:        name,
		Description: "Tool " + name + ".",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string"}},"required":["value"]}`),
	}
	def.Revision = mustDefinitionRevisionValue(def)
	return def
}

func testCallableCatalog(defs ...toolinvocation.ToolDefinition) toolinvocation.ToolCatalog {
	id, err := toolinvocation.CatalogID(defs)
	if err != nil {
		panic(err)
	}
	return toolinvocation.ToolCatalog{ID: id, Tools: defs}
}

func mustDefinitionRevision(t *testing.T, def toolinvocation.ToolDefinition) string {
	t.Helper()
	revision, err := toolinvocation.DefinitionRevision(def)
	if err != nil {
		t.Fatalf("DefinitionRevision() error = %v", err)
	}
	return revision
}

func mustDefinitionRevisionValue(def toolinvocation.ToolDefinition) string {
	revision, err := toolinvocation.DefinitionRevision(def)
	if err != nil {
		panic(err)
	}
	return revision
}

func toolNames(defs []toolinvocation.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func containsTool(defs []toolinvocation.ToolDefinition, name string) bool {
	for _, def := range defs {
		if def.Name == name {
			return true
		}
	}
	return false
}
