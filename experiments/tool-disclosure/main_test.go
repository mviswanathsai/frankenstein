package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"frankenstein/internal/toolinvocation"
)

func TestApplyFinalScorePreservesEarlierFailure(t *testing.T) {
	measurement := trialMeasurement{
		FailureClassification: "deepseek status 500",
		EffectiveBackendCalls: []expectedCall{{Name: "invoice_total", Arguments: map[string]any{"invoice_id": "inv-001"}}},
	}
	tc := experimentCase{
		ExpectedFinalContains: "TOTAL_OK",
		ExpectedBackendCalls:  []expectedCall{{Name: "invoice_total", Arguments: map[string]any{"invoice_id": "inv-001"}}},
	}

	applyFinalScore(tc, toolinvocation.DiscoveryDirect, &measurement, "TOTAL_OK")

	if measurement.TaskSuccess {
		t.Fatalf("TaskSuccess = true, want false")
	}
	if measurement.FailureClassification != "deepseek status 500" {
		t.Fatalf("FailureClassification = %q", measurement.FailureClassification)
	}
}

func TestRecordEffectiveAttemptsUsesProxyNestedArguments(t *testing.T) {
	measurement := trialMeasurement{}
	calls := []toolinvocation.ToolCall{{
		ID:   "proxy-1",
		Name: "tool_call",
		Arguments: map[string]any{
			"name":      "price_order",
			"arguments": map[string]any{"order_id": "ORDER-Beta-3"},
		},
	}}
	results := []toolinvocation.ToolResult{{
		CallID:     "proxy-1",
		Name:       "price_order",
		Status:     toolinvocation.ResultSucceeded,
		SideEffect: toolinvocation.SideEffectNone,
	}}
	expected := []expectedCall{{Name: "price_order", Arguments: map[string]any{"order_id": "ORDER-Beta-3"}}}

	measurement.recordEffectiveAttempts(toolinvocation.DiscoveryProxy, calls, results, expected, 3)
	measurement.ModelCallCount = 4
	measurement.finalizeEffectiveAttemptMetrics()

	if measurement.EffectiveTargetAttemptCount != 1 {
		t.Fatalf("attempt count = %d", measurement.EffectiveTargetAttemptCount)
	}
	attempt := measurement.EffectiveTargetAttempts[0]
	if attempt.TargetName != "price_order" || digestJSON(attempt.Arguments) != digestJSON(expected[0].Arguments) {
		t.Fatalf("attempt = %+v", attempt)
	}
	if !attempt.SchemaValid || !attempt.ExactArguments || !attempt.BackendReached {
		t.Fatalf("attempt = %+v, want valid exact backend reached", attempt)
	}
	if measurement.FirstAttemptSchemaValid == nil || !*measurement.FirstAttemptSchemaValid || measurement.FirstAttemptExact == nil || !*measurement.FirstAttemptExact || !measurement.EventualExactBackendSuccess {
		t.Fatalf("derived metrics = %+v", measurement)
	}
}

func TestRecordEffectiveAttemptsCountsCorrectionAfterInvalidFirstAttempt(t *testing.T) {
	measurement := trialMeasurement{}
	expected := []expectedCall{{Name: "price_order", Arguments: map[string]any{"order_id": "ORDER-Beta-3"}}}

	measurement.recordEffectiveAttempts(toolinvocation.DiscoveryDirect, []toolinvocation.ToolCall{{
		ID:        "bad",
		Name:      "price_order",
		Arguments: map[string]any{"order_id": 7},
	}}, []toolinvocation.ToolResult{{
		CallID: "bad",
		Name:   "price_order",
		Status: toolinvocation.ResultFailed,
		Text:   `argument "order_id" must be string`,
		Failure: &toolinvocation.ToolFailure{
			Code: toolinvocation.FailureInvalidArguments,
		},
	}}, expected, 2)
	measurement.recordEffectiveAttempts(toolinvocation.DiscoveryDirect, []toolinvocation.ToolCall{{
		ID:        "good",
		Name:      "price_order",
		Arguments: map[string]any{"order_id": "ORDER-Beta-3"},
	}}, []toolinvocation.ToolResult{{
		CallID: "good",
		Name:   "price_order",
		Status: toolinvocation.ResultSucceeded,
	}}, expected, 3)
	measurement.ModelCallCount = 4
	measurement.finalizeEffectiveAttemptMetrics()

	if measurement.FirstAttemptSchemaValid == nil || *measurement.FirstAttemptSchemaValid {
		t.Fatalf("first schema valid = %+v, want false", measurement.FirstAttemptSchemaValid)
	}
	if measurement.FirstAttemptExact == nil || *measurement.FirstAttemptExact {
		t.Fatalf("first exact = %+v, want false", measurement.FirstAttemptExact)
	}
	if !measurement.EventualExactBackendSuccess || measurement.CorrectiveModelLoops != 1 || measurement.ValidationFailureCount != 1 {
		t.Fatalf("derived metrics = %+v", measurement)
	}
}

func TestNestedCasesLoadAndRegister(t *testing.T) {
	path := "cases.json"
	if _, err := os.Stat(path); err != nil {
		path = "experiments/tool-disclosure/cases.json"
	}
	cases, err := loadCases(path, "", "nested-")
	if err != nil {
		t.Fatalf("loadCases() error = %v", err)
	}
	if len(cases) != 3 {
		t.Fatalf("nested case count = %d, want 3", len(cases))
	}
	for _, tc := range cases {
		if len(tc.ExpectedBackendCalls) != 1 {
			t.Fatalf("%s expected calls = %d, want 1", tc.ID, len(tc.ExpectedBackendCalls))
		}
		rec := &recorder{}
		if _, err := newTrialService(tc, toolinvocation.DiscoveryDirect, rec); err != nil {
			t.Fatalf("%s direct service error = %v", tc.ID, err)
		}
		if _, err := newTrialService(tc, toolinvocation.DiscoveryProxy, rec); err != nil {
			t.Fatalf("%s proxy service error = %v", tc.ID, err)
		}
	}
}

func TestCanonicalDirectHistoryHasLoadedCatalog(t *testing.T) {
	setup, err := buildDelayedReuseSetup(context.Background(), toolinvocation.DiscoveryDirect)
	if err != nil {
		t.Fatalf("buildDelayedReuseSetup() error = %v", err)
	}
	if !catalogHasTool(setup.ActiveCatalog, delayedReuseToolName()) {
		t.Fatalf("direct active catalog lacks %s: %v", delayedReuseToolName(), toolNamesForTest(setup.ActiveCatalog))
	}
	if !historyHasToolCall(setup.Messages, "tool_load") || !historyHasToolCall(setup.Messages, delayedReuseToolName()) {
		t.Fatalf("direct history missing load or effective call")
	}
	var m trialMeasurement
	setup.Recorder.fill(&m)
	if len(m.EffectiveBackendCalls) != 0 {
		t.Fatalf("setup backend calls leaked into recorder: %+v", m.EffectiveBackendCalls)
	}
}

func TestCanonicalProxyHistoryHasBridgeOnlyCatalog(t *testing.T) {
	setup, err := buildDelayedReuseSetup(context.Background(), toolinvocation.DiscoveryProxy)
	if err != nil {
		t.Fatalf("buildDelayedReuseSetup() error = %v", err)
	}
	if catalogHasTool(setup.ActiveCatalog, delayedReuseToolName()) {
		t.Fatalf("proxy active catalog includes effective target: %v", toolNamesForTest(setup.ActiveCatalog))
	}
	if !catalogHasTool(setup.ActiveCatalog, "tool_call") || !historyHasToolCall(setup.Messages, "tool_call") {
		t.Fatalf("proxy history/catalog missing bridge tool")
	}
	var m trialMeasurement
	setup.Recorder.fill(&m)
	if len(m.EffectiveBackendCalls) != 0 {
		t.Fatalf("setup backend calls leaked into recorder: %+v", m.EffectiveBackendCalls)
	}
}

func TestDelayedFirstActionClassification(t *testing.T) {
	target := delayedReuseToolName()
	tests := []struct {
		name     string
		strategy toolinvocation.DiscoveryStrategy
		calls    []toolinvocation.ToolCall
		want     string
	}{
		{name: "direct effective", strategy: toolinvocation.DiscoveryDirect, calls: []toolinvocation.ToolCall{{Name: target}}, want: "direct_effective_invocation"},
		{name: "proxy reuse", strategy: toolinvocation.DiscoveryProxy, calls: []toolinvocation.ToolCall{{Name: "tool_call", Arguments: map[string]any{"name": target}}}, want: "proxy_reuse"},
		{name: "describe", calls: []toolinvocation.ToolCall{{Name: "tool_describe"}}, want: "describe"},
		{name: "search", calls: []toolinvocation.ToolCall{{Name: "tool_search"}}, want: "search"},
		{name: "same round", strategy: toolinvocation.DiscoveryDirect, calls: []toolinvocation.ToolCall{{Name: "tool_describe"}, {Name: target}}, want: "same_round_describe_plus_effective"},
		{name: "other", calls: []toolinvocation.ToolCall{{Name: "other_tool"}}, want: "other_tool"},
		{name: "none", want: "no_tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyFirstAction(tt.strategy, tt.calls, target); got != tt.want {
				t.Fatalf("classifyFirstAction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDescribeBeforeEffectiveAndSameRoundClassification(t *testing.T) {
	target := delayedReuseToolName()
	state := delayedActionState{}
	var measurement trialMeasurement
	updateDelayedActionMetrics(&measurement, &state, toolinvocation.DiscoveryDirect, []toolinvocation.ToolCall{{Name: "tool_describe"}}, target)
	updateDelayedActionMetrics(&measurement, &state, toolinvocation.DiscoveryDirect, []toolinvocation.ToolCall{{Name: target}}, target)
	if !measurement.ToolDescribeBeforeEffective || measurement.SameRoundDescribeEffective {
		t.Fatalf("sequential describe metrics = %+v", measurement)
	}

	state = delayedActionState{}
	measurement = trialMeasurement{}
	updateDelayedActionMetrics(&measurement, &state, toolinvocation.DiscoveryDirect, []toolinvocation.ToolCall{{Name: "tool_describe"}, {Name: target}}, target)
	if measurement.ToolDescribeBeforeEffective || !measurement.SameRoundDescribeEffective {
		t.Fatalf("same-round metrics = %+v", measurement)
	}
}

func TestDelayedArgumentsDifferFromSetupAndAreExact(t *testing.T) {
	if digestJSON(delayedReuseSetupArguments()) == digestJSON(delayedReuseDelayedArguments()) {
		t.Fatalf("setup and delayed arguments are identical")
	}
	tc := delayedReuseCase()
	if len(tc.ExpectedBackendCalls) != 1 || !exactAttempt(delayedReuseToolName(), delayedReuseDelayedArguments(), tc.ExpectedBackendCalls) {
		t.Fatalf("delayed expected call mismatch: %+v", tc.ExpectedBackendCalls)
	}
}

func TestDelayedFillerDeterministic(t *testing.T) {
	a := buildDelayedFiller(delayedReuseCondition{ID: "neutral-32k", Kind: "neutral", TargetTokens: 32000})
	b := buildDelayedFiller(delayedReuseCondition{ID: "neutral-32k", Kind: "neutral", TargetTokens: 32000})
	c := buildDelayedFiller(delayedReuseCondition{ID: "neutral-128k", Kind: "neutral", TargetTokens: 128000})
	if a != b {
		t.Fatalf("filler is not deterministic")
	}
	if len(c) <= len(a)*3 {
		t.Fatalf("128k filler length = %d, 32k = %d", len(c), len(a))
	}
	if strings.Contains(a, delayedReuseToolName()) {
		t.Fatalf("neutral filler mentions target tool")
	}
}

func TestDelayedFinalScoringAfterRepair(t *testing.T) {
	measurement := trialMeasurement{RepeatedCallsByTool: map[string]int{}}
	expected := delayedReuseCase().ExpectedBackendCalls
	measurement.recordEffectiveAttempts(toolinvocation.DiscoveryProxy, []toolinvocation.ToolCall{{
		ID:   "bad",
		Name: "tool_call",
		Arguments: map[string]any{
			"name":      delayedReuseToolName(),
			"arguments": map[string]any{"id": "CUST-Delta-8"},
		},
	}}, []toolinvocation.ToolResult{{
		CallID: "bad",
		Name:   delayedReuseToolName(),
		Status: toolinvocation.ResultFailed,
		Text:   `missing required argument "customer"`,
		Failure: &toolinvocation.ToolFailure{
			Code: toolinvocation.FailureInvalidArguments,
		},
	}}, expected, 1)
	measurement.recordEffectiveAttempts(toolinvocation.DiscoveryProxy, []toolinvocation.ToolCall{{
		ID:   "good",
		Name: "tool_call",
		Arguments: map[string]any{
			"name":      delayedReuseToolName(),
			"arguments": delayedReuseDelayedArguments(),
		},
	}}, []toolinvocation.ToolResult{{
		CallID: "good",
		Name:   delayedReuseToolName(),
		Status: toolinvocation.ResultSucceeded,
	}}, expected, 3)
	measurement.EffectiveBackendCalls = expected
	measurement.ModelCallCount = 4
	measurement.finalizeEffectiveAttemptMetrics()
	applyFinalScore(delayedReuseCase(), toolinvocation.DiscoveryProxy, &measurement, "SENTINEL:DELAYED-REUSE")
	if !measurement.TaskSuccess || !measurement.EventualExactBackendSuccess || measurement.CorrectiveModelLoops != 2 || measurement.ValidationFailureCount != 1 {
		t.Fatalf("measurement = %+v", measurement)
	}
}

func catalogHasTool(catalog toolinvocation.ToolCatalog, name string) bool {
	for _, tool := range catalog.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func historyHasToolCall(messages []chatMessage, name string) bool {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == name {
				return true
			}
		}
	}
	return false
}

func toolNamesForTest(catalog toolinvocation.ToolCatalog) []string {
	names := make([]string, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		names = append(names, tool.Name)
	}
	return names
}
