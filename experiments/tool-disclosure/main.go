package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"frankenstein/internal/toolinvocation"
)

type caseFile []experimentCase

type experimentCase struct {
	ID                    string         `json:"id"`
	Prompt                string         `json:"prompt"`
	ExpectedFinalContains string         `json:"expected_final_contains"`
	Tools                 []caseTool     `json:"tools"`
	ExpectedBackendCalls  []expectedCall `json:"expected_backend_calls"`
}

type caseTool struct {
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Schema            map[string]any `json:"schema"`
	Response          string         `json:"response"`
	RuntimeAvailable  *bool          `json:"runtime_available,omitempty"`
	ExpectedArguments map[string]any `json:"expected_arguments,omitempty"`
}

type expectedCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type trialMeasurement struct {
	CaseID                      string                   `json:"case"`
	ConditionID                 string                   `json:"condition,omitempty"`
	Repetition                  int                      `json:"repetition"`
	Strategy                    string                   `json:"strategy"`
	Model                       string                   `json:"model"`
	SystemFingerprint           string                   `json:"system_fingerprint,omitempty"`
	ModelCallCount              int                      `json:"model_call_count"`
	ModelCalls                  []modelCallMeasurement   `json:"model_calls"`
	FirstActionCategory         string                   `json:"first_action_category,omitempty"`
	ToolDescribeBeforeEffective bool                     `json:"tool_describe_before_first_effective_attempt,omitempty"`
	ToolSearchBeforeEffective   bool                     `json:"tool_search_before_first_effective_attempt,omitempty"`
	SameRoundDescribeEffective  bool                     `json:"same_round_describe_plus_effective,omitempty"`
	EffectiveTargetAttempts     []effectiveTargetAttempt `json:"effective_target_attempts"`
	FirstAttemptSchemaValid     *bool                    `json:"first_effective_attempt_schema_valid,omitempty"`
	FirstAttemptExact           *bool                    `json:"first_effective_attempt_exact,omitempty"`
	EventualExactBackendSuccess bool                     `json:"eventual_exact_backend_success"`
	EffectiveTargetAttemptCount int                      `json:"effective_target_attempt_count"`
	ValidationFailureCount      int                      `json:"validation_failure_count"`
	CorrectiveModelLoops        int                      `json:"corrective_model_loops_after_first_effective_attempt"`
	MalformedProxyEnvelopeCount int                      `json:"malformed_proxy_envelope_count,omitempty"`
	InvalidTargetArgumentCount  int                      `json:"invalid_decoded_target_argument_count,omitempty"`
	EstimatedInterveningTokens  int                      `json:"estimated_intervening_token_distance,omitempty"`
	ListToolsRequestCount       int                      `json:"list_tools_request_count"`
	ExecuteRequestCount         int                      `json:"execute_request_count"`
	ListToolsDurationNS         int64                    `json:"list_tools_duration_ns"`
	ExecuteDurationNS           int64                    `json:"execute_duration_ns"`
	CatalogTransitionCount      int                      `json:"catalog_transition_count"`
	TotalTrialLatencyMS         int64                    `json:"total_trial_latency_ms"`
	DescribeToEffectiveCallMS   *int64                   `json:"describe_to_effective_call_ms,omitempty"`
	NormalizedToolSequence      []string                 `json:"normalized_tool_sequence"`
	ArgumentValidationFailures  int                      `json:"argument_validation_failures"`
	EffectiveBackendCalls       []expectedCall           `json:"effective_backend_calls"`
	DistinctToolsUsed           int                      `json:"distinct_tools_used"`
	RepeatedCallsByTool         map[string]int           `json:"repeated_calls_by_tool"`
	TaskSuccess                 bool                     `json:"task_success"`
	FailureClassification       string                   `json:"failure_classification,omitempty"`
	FinalContent                string                   `json:"final_content,omitempty"`
	ProxyDispatchAttemptedCount int                      `json:"proxy_dispatch_attempted_count,omitempty"`
	CallStartedCount            int                      `json:"call_started_count"`
}

type modelCallMeasurement struct {
	Index                     int      `json:"index"`
	SystemFingerprint         string   `json:"system_fingerprint,omitempty"`
	ActiveCatalogID           string   `json:"active_catalog_id"`
	ProviderToolBundleSHA256  string   `json:"provider_tool_bundle_sha256"`
	ProviderToolBundleChanged bool     `json:"provider_tool_bundle_changed"`
	RequestedToolNames        []string `json:"requested_tool_names"`
	PromptTokens              int      `json:"prompt_tokens"`
	PromptCacheHitTokens      int      `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens     int      `json:"prompt_cache_miss_tokens"`
	CompletionTokens          int      `json:"completion_tokens"`
	ReasoningTokens           int      `json:"reasoning_tokens"`
	FinishReason              string   `json:"finish_reason"`
	LatencyMS                 int64    `json:"latency_ms"`
}

type effectiveTargetAttempt struct {
	ModelRound               int    `json:"model_round"`
	TargetName               string `json:"effective_target_name"`
	Arguments                any    `json:"attempted_effective_arguments,omitempty"`
	SchemaValid              bool   `json:"schema_valid"`
	ValidationFailureCode    string `json:"validation_failure_code,omitempty"`
	ValidationFailureMessage string `json:"validation_failure_message,omitempty"`
	ExactArguments           bool   `json:"exact_arguments"`
	BackendReached           bool   `json:"backend_reached"`
	MalformedProxyEnvelope   bool   `json:"malformed_proxy_envelope,omitempty"`
}

type recorder struct {
	mu             sync.Mutex
	starts         []toolinvocation.ToolCallStarted
	proxyAttempts  []toolinvocation.ToolProxyDispatchAttempted
	backendCalls   []backendCall
	firstDescribe  *time.Time
	firstEffective *time.Time
	currentRound   int
}

type backendCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	At        time.Time      `json:"-"`
	Round     int            `json:"-"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	casesPath := flag.String("cases", filepath.Join("experiments", "tool-disclosure", "cases.json"), "case JSON path")
	caseID := flag.String("case", "", "optional single case id")
	casePrefix := flag.String("case-prefix", "", "optional case id prefix")
	delayedReuse := flag.Bool("delayed-reuse", false, "run delayed tool-reuse experiment")
	repetitions := flag.Int("reps", 1, "paired repetitions; increase explicitly for full runs")
	maxRounds := flag.Int("max-rounds", 8, "maximum model calls per trial")
	outPath := flag.String("out", "", "JSONL output path; stdout when empty")
	flag.Parse()
	if *repetitions < 1 {
		return errors.New("-reps must be >= 1")
	}
	if err := loadDotEnv(".env"); err != nil {
		return err
	}
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		return errors.New("DEEPSEEK_API_KEY is required")
	}
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-v4-flash"
	}
	var out io.Writer = os.Stdout
	var file *os.File
	var err error
	if *outPath != "" {
		file, err = os.Create(*outPath)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	client := deepSeekClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), model: model, http: http.DefaultClient}
	encoder := json.NewEncoder(out)
	if *delayedReuse {
		return runDelayedReuseExperiment(context.Background(), client, *repetitions, *maxRounds, encoder)
	}
	cases, err := loadCases(*casesPath, *caseID, *casePrefix)
	if err != nil {
		return err
	}
	for rep := 0; rep < *repetitions; rep++ {
		for _, tc := range cases {
			order := []toolinvocation.DiscoveryStrategy{toolinvocation.DiscoveryDirect, toolinvocation.DiscoveryProxy}
			if rep%2 == 1 {
				order[0], order[1] = order[1], order[0]
			}
			for _, strategy := range order {
				measurement := runTrial(context.Background(), client, tc, rep, strategy, *maxRounds)
				if err := encoder.Encode(measurement); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func runDelayedReuseExperiment(ctx context.Context, client deepSeekClient, repetitions int, maxRounds int, encoder *json.Encoder) error {
	var fingerprint string
	recentPromptTokens := map[string]int{}
	for rep := 0; rep < repetitions; rep++ {
		for _, condition := range delayedReuseConditions() {
			order := []toolinvocation.DiscoveryStrategy{toolinvocation.DiscoveryDirect, toolinvocation.DiscoveryProxy}
			if rep%2 == 1 {
				order[0], order[1] = order[1], order[0]
			}
			for _, strategy := range order {
				measurement := runDelayedReuseTrial(ctx, client, condition, rep, strategy, maxRounds)
				if measurement.SystemFingerprint != "" {
					if fingerprint == "" {
						fingerprint = measurement.SystemFingerprint
					} else if measurement.SystemFingerprint != fingerprint {
						return fmt.Errorf("system_fingerprint changed from %s to %s", fingerprint, measurement.SystemFingerprint)
					}
				}
				if condition.ID == "recent" && len(measurement.ModelCalls) > 0 {
					recentPromptTokens[delayedReuseBaselineKey(rep, strategy)] = measurement.ModelCalls[0].PromptTokens
				} else if len(measurement.ModelCalls) > 0 {
					measurement.EstimatedInterveningTokens = max(0, measurement.ModelCalls[0].PromptTokens-recentPromptTokens[delayedReuseBaselineKey(rep, strategy)])
				}
				if err := encoder.Encode(measurement); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func delayedReuseBaselineKey(rep int, strategy toolinvocation.DiscoveryStrategy) string {
	return fmt.Sprintf("%d:%s", rep, strategyName(strategy))
}

func runTrial(ctx context.Context, client deepSeekClient, tc experimentCase, repetition int, strategy toolinvocation.DiscoveryStrategy, maxRounds int) trialMeasurement {
	start := time.Now()
	rec := &recorder{}
	service, err := newTrialService(tc, strategy, rec)
	if err != nil {
		return trialMeasurement{CaseID: tc.ID, Repetition: repetition, Strategy: string(strategy), Model: client.model, TaskSuccess: false, FailureClassification: err.Error()}
	}
	listStart := time.Now()
	listed, failure := service.ListTools(ctx, toolinvocation.ToolCatalogRequest{ID: "list-" + tc.ID})
	listDuration := time.Since(listStart)
	if failure != nil {
		return trialMeasurement{CaseID: tc.ID, Repetition: repetition, Strategy: string(strategy), Model: client.model, TaskSuccess: false, FailureClassification: failure.Code}
	}
	activeCatalog := listed.Catalog
	messages := []chatMessage{{Role: "user", Content: tc.Prompt}}
	measurement := trialMeasurement{
		CaseID:                tc.ID,
		Repetition:            repetition,
		Strategy:              strategyName(strategy),
		Model:                 client.model,
		RepeatedCallsByTool:   map[string]int{},
		ListToolsRequestCount: 1,
		ListToolsDurationNS:   listDuration.Nanoseconds(),
	}
	var finalContent string
	var previousBundleDigest string
	for round := 0; round < maxRounds; round++ {
		rec.setRound(round + 1)
		providerTools := encodeProviderTools(activeCatalog)
		bundleDigest := digestJSON(providerTools)
		callStart := time.Now()
		response, err := client.chat(ctx, messages, providerTools)
		latency := time.Since(callStart)
		callMeasurement := modelCallMeasurement{
			Index:                     round + 1,
			ActiveCatalogID:           activeCatalog.ID,
			ProviderToolBundleSHA256:  bundleDigest,
			ProviderToolBundleChanged: round > 0 && bundleDigest != previousBundleDigest,
			FinishReason:              response.FinishReason,
			LatencyMS:                 latency.Milliseconds(),
		}
		previousBundleDigest = bundleDigest
		if err != nil {
			measurement.ModelCalls = append(measurement.ModelCalls, callMeasurement)
			measurement.FailureClassification = err.Error()
			break
		}
		measurement.SystemFingerprint = response.SystemFingerprint
		callMeasurement.SystemFingerprint = response.SystemFingerprint
		callMeasurement.RequestedToolNames = requestedToolNames(response.Message.ToolCalls)
		callMeasurement.PromptTokens = response.Usage.PromptTokens
		callMeasurement.PromptCacheHitTokens = response.Usage.PromptCacheHitTokens
		callMeasurement.PromptCacheMissTokens = response.Usage.PromptCacheMissTokens
		if callMeasurement.PromptCacheHitTokens == 0 && response.Usage.PromptTokensDetails.CachedTokens > 0 {
			callMeasurement.PromptCacheHitTokens = response.Usage.PromptTokensDetails.CachedTokens
			callMeasurement.PromptCacheMissTokens = max(0, response.Usage.PromptTokens-callMeasurement.PromptCacheHitTokens)
		}
		callMeasurement.CompletionTokens = response.Usage.CompletionTokens
		callMeasurement.ReasoningTokens = response.Usage.CompletionTokensDetails.ReasoningTokens
		measurement.ModelCalls = append(measurement.ModelCalls, callMeasurement)
		messages = append(messages, response.Message.asHistory())
		if len(response.Message.ToolCalls) == 0 {
			finalContent = response.Message.content()
			break
		}
		calls, normalizeFailure := normalizeToolCalls(response.Message.ToolCalls, activeCatalog)
		if normalizeFailure != "" {
			measurement.FailureClassification = normalizeFailure
			break
		}
		execStart := time.Now()
		execResult, execFailure := service.Execute(ctx, toolinvocation.ToolExecutionRequest{
			ID:             fmt.Sprintf("%s-%s-%d-%d", tc.ID, strategyName(strategy), repetition, round),
			IdempotencyKey: fmt.Sprintf("%s-%s-%d-%d", tc.ID, strategyName(strategy), repetition, round),
			CatalogID:      activeCatalog.ID,
			SessionID:      "experiment-session",
			TurnID:         fmt.Sprintf("turn-%s-%d", tc.ID, repetition),
			Calls:          calls,
		})
		measurement.ExecuteRequestCount++
		measurement.ExecuteDurationNS += time.Since(execStart).Nanoseconds()
		if execFailure != nil {
			measurement.FailureClassification = execFailure.Code
			break
		}
		measurement.recordEffectiveAttempts(strategy, calls, execResult.Results, tc.ExpectedBackendCalls, round+1)
		for _, toolResult := range execResult.Results {
			measurement.NormalizedToolSequence = append(measurement.NormalizedToolSequence, toolResult.Name)
			if toolResult.Failure != nil && toolResult.Failure.Code == toolinvocation.FailureInvalidArguments {
				measurement.ArgumentValidationFailures++
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: toolResult.CallID, Content: toolResult.Text})
		}
		if strategy == toolinvocation.DiscoveryDirect && execResult.CatalogTransition != nil && execResult.CatalogTransition.BaseCatalogID == activeCatalog.ID {
			measurement.CatalogTransitionCount++
			activeCatalog = execResult.CatalogTransition.Catalog
		}
	}
	rec.fill(&measurement)
	measurement.ModelCallCount = len(measurement.ModelCalls)
	measurement.TotalTrialLatencyMS = time.Since(start).Milliseconds()
	measurement.FinalContent = finalContent
	measurement.finalizeEffectiveAttemptMetrics()
	applyFinalScore(tc, strategy, &measurement, finalContent)
	return measurement
}

type delayedReuseCondition struct {
	ID           string
	Kind         string
	TargetTokens int
}

type delayedSetup struct {
	Service       *toolinvocation.Service
	Recorder      *recorder
	ActiveCatalog toolinvocation.ToolCatalog
	Messages      []chatMessage
	Case          experimentCase
}

func delayedReuseConditions() []delayedReuseCondition {
	return []delayedReuseCondition{
		{ID: "recent"},
		{ID: "neutral-32k", Kind: "neutral", TargetTokens: 32000},
		{ID: "interference-32k", Kind: "interference", TargetTokens: 32000},
		{ID: "neutral-128k", Kind: "neutral", TargetTokens: 128000},
		{ID: "interference-128k", Kind: "interference", TargetTokens: 128000},
	}
}

func runDelayedReuseTrial(ctx context.Context, client deepSeekClient, condition delayedReuseCondition, repetition int, strategy toolinvocation.DiscoveryStrategy, maxRounds int) trialMeasurement {
	start := time.Now()
	setup, err := buildDelayedReuseSetup(ctx, strategy)
	measurement := trialMeasurement{
		CaseID:              "delayed-reuse",
		ConditionID:         condition.ID,
		Repetition:          repetition,
		Strategy:            strategyName(strategy),
		Model:               client.model,
		RepeatedCallsByTool: map[string]int{},
	}
	if err != nil {
		measurement.FailureClassification = err.Error()
		return measurement
	}
	activeCatalog := setup.ActiveCatalog
	messages := append([]chatMessage{}, setup.Messages...)
	messages = append(messages, delayedFillerMessages(condition)...)
	messages = append(messages, chatMessage{Role: "user", Content: delayedReuseFinalPrompt()})

	var previousBundleDigest string
	var finalContent string
	actionState := delayedActionState{}
	for round := 0; round < maxRounds; round++ {
		setup.Recorder.setRound(round + 1)
		providerTools := encodeProviderTools(activeCatalog)
		bundleDigest := digestJSON(providerTools)
		callStart := time.Now()
		response, err := client.chat(ctx, messages, providerTools)
		latency := time.Since(callStart)
		callMeasurement := modelCallMeasurement{
			Index:                     round + 1,
			ActiveCatalogID:           activeCatalog.ID,
			ProviderToolBundleSHA256:  bundleDigest,
			ProviderToolBundleChanged: round > 0 && bundleDigest != previousBundleDigest,
			FinishReason:              response.FinishReason,
			LatencyMS:                 latency.Milliseconds(),
		}
		previousBundleDigest = bundleDigest
		if err != nil {
			measurement.ModelCalls = append(measurement.ModelCalls, callMeasurement)
			measurement.FailureClassification = err.Error()
			break
		}
		if measurement.SystemFingerprint == "" {
			measurement.SystemFingerprint = response.SystemFingerprint
		} else if response.SystemFingerprint != "" && response.SystemFingerprint != measurement.SystemFingerprint {
			measurement.FailureClassification = "system_fingerprint_changed"
			break
		}
		callMeasurement.SystemFingerprint = response.SystemFingerprint
		callMeasurement.RequestedToolNames = requestedToolNames(response.Message.ToolCalls)
		callMeasurement.PromptTokens = response.Usage.PromptTokens
		callMeasurement.PromptCacheHitTokens = response.Usage.PromptCacheHitTokens
		callMeasurement.PromptCacheMissTokens = response.Usage.PromptCacheMissTokens
		if callMeasurement.PromptCacheHitTokens == 0 && response.Usage.PromptTokensDetails.CachedTokens > 0 {
			callMeasurement.PromptCacheHitTokens = response.Usage.PromptTokensDetails.CachedTokens
			callMeasurement.PromptCacheMissTokens = max(0, response.Usage.PromptTokens-callMeasurement.PromptCacheHitTokens)
		}
		callMeasurement.CompletionTokens = response.Usage.CompletionTokens
		callMeasurement.ReasoningTokens = response.Usage.CompletionTokensDetails.ReasoningTokens
		measurement.ModelCalls = append(measurement.ModelCalls, callMeasurement)
		messages = append(messages, response.Message.asHistory())
		if len(response.Message.ToolCalls) == 0 {
			if !actionState.firstActionStored {
				measurement.FirstActionCategory = "no_tool"
				actionState.firstActionStored = true
			}
			finalContent = response.Message.content()
			break
		}
		calls, normalizeFailure := normalizeToolCalls(response.Message.ToolCalls, activeCatalog)
		if normalizeFailure != "" {
			measurement.FailureClassification = normalizeFailure
			break
		}
		updateDelayedActionMetrics(&measurement, &actionState, strategy, calls, delayedReuseToolName())
		execStart := time.Now()
		execResult, execFailure := setup.Service.Execute(ctx, toolinvocation.ToolExecutionRequest{
			ID:             fmt.Sprintf("delayed-%s-%s-%d-%d", condition.ID, strategyName(strategy), repetition, round),
			IdempotencyKey: fmt.Sprintf("delayed-%s-%s-%d-%d", condition.ID, strategyName(strategy), repetition, round),
			CatalogID:      activeCatalog.ID,
			SessionID:      "delayed-reuse-session",
			TurnID:         fmt.Sprintf("turn-%s-%d", condition.ID, repetition),
			Calls:          calls,
		})
		measurement.ExecuteRequestCount++
		measurement.ExecuteDurationNS += time.Since(execStart).Nanoseconds()
		if execFailure != nil {
			measurement.FailureClassification = execFailure.Code
			break
		}
		measurement.recordEffectiveAttempts(strategy, calls, execResult.Results, setup.Case.ExpectedBackendCalls, round+1)
		for _, toolResult := range execResult.Results {
			measurement.NormalizedToolSequence = append(measurement.NormalizedToolSequence, toolResult.Name)
			if toolResult.Failure != nil && toolResult.Failure.Code == toolinvocation.FailureInvalidArguments {
				measurement.ArgumentValidationFailures++
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: toolResult.CallID, Content: toolResult.Text})
		}
		if strategy == toolinvocation.DiscoveryDirect && execResult.CatalogTransition != nil && execResult.CatalogTransition.BaseCatalogID == activeCatalog.ID {
			measurement.CatalogTransitionCount++
			activeCatalog = execResult.CatalogTransition.Catalog
		}
	}
	setup.Recorder.fill(&measurement)
	measurement.ModelCallCount = len(measurement.ModelCalls)
	measurement.TotalTrialLatencyMS = time.Since(start).Milliseconds()
	measurement.FinalContent = finalContent
	measurement.finalizeEffectiveAttemptMetrics()
	applyFinalScore(setup.Case, strategy, &measurement, finalContent)
	return measurement
}

func buildDelayedReuseSetup(ctx context.Context, strategy toolinvocation.DiscoveryStrategy) (delayedSetup, error) {
	tc := delayedReuseCase()
	rec := &recorder{}
	service, err := newTrialService(tc, strategy, rec)
	if err != nil {
		return delayedSetup{}, err
	}
	listStart := time.Now()
	listed, failure := service.ListTools(ctx, toolinvocation.ToolCatalogRequest{ID: "delayed-setup-list-" + strategyName(strategy)})
	_ = time.Since(listStart)
	if failure != nil {
		return delayedSetup{}, errors.New(failure.Code)
	}
	activeCatalog := listed.Catalog
	messages := []chatMessage{{Role: "user", Content: delayedReuseSetupPrompt()}}

	var errExec error
	messages, activeCatalog, errExec = appendCanonicalToolTurn(ctx, service, messages, activeCatalog, "setup-search", "tool_search", map[string]any{"query": "customer profile submit"}, false)
	if errExec != nil {
		return delayedSetup{}, errExec
	}
	messages, activeCatalog, errExec = appendCanonicalToolTurn(ctx, service, messages, activeCatalog, "setup-describe", "tool_describe", map[string]any{"name": delayedReuseToolName()}, false)
	if errExec != nil {
		return delayedSetup{}, errExec
	}
	if strategy == toolinvocation.DiscoveryDirect {
		messages, activeCatalog, errExec = appendCanonicalToolTurn(ctx, service, messages, activeCatalog, "setup-load", "tool_load", map[string]any{"name": delayedReuseToolName()}, true)
		if errExec != nil {
			return delayedSetup{}, errExec
		}
		messages, activeCatalog, errExec = appendCanonicalToolTurn(ctx, service, messages, activeCatalog, "setup-effective", delayedReuseToolName(), delayedReuseSetupArguments(), false)
	} else {
		messages, activeCatalog, errExec = appendCanonicalToolTurn(ctx, service, messages, activeCatalog, "setup-proxy", "tool_call", map[string]any{"name": delayedReuseToolName(), "arguments": delayedReuseSetupArguments()}, false)
	}
	if errExec != nil {
		return delayedSetup{}, errExec
	}
	messages = append(messages, chatMessage{Role: "assistant", Content: "Submitted the customer profile for CUST-Alpha-7."})
	rec.reset()
	return delayedSetup{Service: service, Recorder: rec, ActiveCatalog: activeCatalog, Messages: messages, Case: tc}, nil
}

func appendCanonicalToolTurn(ctx context.Context, service *toolinvocation.Service, messages []chatMessage, catalog toolinvocation.ToolCatalog, callID string, name string, args map[string]any, catalogChanging bool) ([]chatMessage, toolinvocation.ToolCatalog, error) {
	call := callForCatalog(catalog, callID, name, args)
	argJSON, _ := json.Marshal(args)
	messages = append(messages, chatMessage{Role: "assistant", ToolCalls: []providerToolCall{{
		ID: callID, Type: "function", Function: providerToolFunction{Name: name, Arguments: string(argJSON)},
	}}})
	req := toolinvocation.ToolExecutionRequest{
		ID:             "setup-" + callID,
		IdempotencyKey: "setup-" + callID,
		CatalogID:      catalog.ID,
		SessionID:      "delayed-setup-session",
		TurnID:         "delayed-setup-turn",
		Calls:          []toolinvocation.ToolCall{call},
	}
	if catalogChanging {
		req.SessionID = "delayed-setup-session"
		req.TurnID = "delayed-setup-turn"
	}
	result, failure := service.Execute(ctx, req)
	if failure != nil {
		return messages, catalog, errors.New(failure.Code)
	}
	if len(result.Results) != 1 {
		return messages, catalog, errors.New("setup returned unexpected result count")
	}
	messages = append(messages, chatMessage{Role: "tool", ToolCallID: callID, Content: result.Results[0].Text})
	if result.CatalogTransition != nil && result.CatalogTransition.BaseCatalogID == catalog.ID {
		catalog = result.CatalogTransition.Catalog
	}
	return messages, catalog, nil
}

func callForCatalog(catalog toolinvocation.ToolCatalog, id, name string, args map[string]any) toolinvocation.ToolCall {
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
	return toolinvocation.ToolCall{ID: id, Name: name, Arguments: args}
}

func delayedReuseCase() experimentCase {
	return experimentCase{
		ID:                    "delayed-reuse",
		Prompt:                delayedReuseFinalPrompt(),
		ExpectedFinalContains: "SENTINEL:DELAYED-REUSE",
		Tools: []caseTool{{
			Name:        delayedReuseToolName(),
			Description: "Submit one customer profile. Use a customer object with id, name, and nested address.",
			Schema:      delayedReuseSchema(),
			Response:    "customer profile accepted",
		}},
		ExpectedBackendCalls: []expectedCall{{Name: delayedReuseToolName(), Arguments: delayedReuseDelayedArguments()}},
	}
}

func delayedReuseToolName() string {
	return "submit_customer_profile"
}

func delayedReuseSetupPrompt() string {
	return "Find the hidden tool for submitting customer profiles. Submit customer profile CUST-Alpha-7 for customer name Ada Lovelace. The address is street 12 Analytical Engine Way, city London, postal_code NW1-6XE, country UK."
}

func delayedReuseFinalPrompt() string {
	return "Please submit the new customer profile for customer id CUST-Delta-8, customer name Grace Hopper, address street 44 Compiler Court, city Arlington, postal_code 22201, country US. After it is submitted, finish with SENTINEL:DELAYED-REUSE."
}

func delayedReuseSetupArguments() map[string]any {
	return map[string]any{"customer": map[string]any{"id": "CUST-Alpha-7", "name": "Ada Lovelace", "address": map[string]any{"street": "12 Analytical Engine Way", "city": "London", "postal_code": "NW1-6XE", "country": "UK"}}}
}

func delayedReuseDelayedArguments() map[string]any {
	return map[string]any{"customer": map[string]any{"id": "CUST-Delta-8", "name": "Grace Hopper", "address": map[string]any{"street": "44 Compiler Court", "city": "Arlington", "postal_code": "22201", "country": "US"}}}
}

func delayedReuseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"customer": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id":   map[string]any{"type": "string"},
					"name": map[string]any{"type": "string"},
					"address": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"street":      map[string]any{"type": "string"},
							"city":        map[string]any{"type": "string"},
							"postal_code": map[string]any{"type": "string"},
							"country":     map[string]any{"type": "string"},
						},
						"required": []any{"street", "city", "postal_code", "country"},
					},
				},
				"required": []any{"id", "name", "address"},
			},
		},
		"required": []any{"customer"},
	}
}

func newTrialService(tc experimentCase, strategy toolinvocation.DiscoveryStrategy, rec *recorder) (*toolinvocation.Service, error) {
	options := toolinvocation.Options{
		DiscoveryStrategy:        strategy,
		AcknowledgeCallStarted:   rec.ackCallStarted,
		AcknowledgeProxyDispatch: rec.ackProxyDispatch,
	}
	var regs []toolinvocation.Registration
	for _, tool := range tc.Tools {
		schema, _ := json.Marshal(tool.Schema)
		runtimeAvailable := true
		if tool.RuntimeAvailable != nil {
			runtimeAvailable = *tool.RuntimeAvailable
		}
		tool := tool
		regs = append(regs, toolinvocation.Registration{
			Provider: "experiment", ProviderVersion: "0", LocalName: tool.Name,
			Name: tool.Name, Description: tool.Description, InputSchema: schema, Discoverable: true,
			RuntimeAvailable: func(context.Context) bool { return runtimeAvailable },
			Backend: func(_ context.Context, req toolinvocation.BackendRequest) toolinvocation.BackendResult {
				rec.recordBackend(tool.Name, req.Arguments)
				return toolinvocation.BackendResult{Text: tool.Response, SideEffect: toolinvocation.SideEffectNone}
			},
		})
	}
	return toolinvocation.NewService(options, regs...)
}

func (r *recorder) ackCallStarted(_ context.Context, event toolinvocation.ToolCallStarted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.starts = append(r.starts, event)
	if event.Name == "tool_describe" && r.firstDescribe == nil {
		r.firstDescribe = &now
	}
	return nil
}

func (r *recorder) ackProxyDispatch(_ context.Context, event toolinvocation.ToolProxyDispatchAttempted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proxyAttempts = append(r.proxyAttempts, event)
	return nil
}

func (r *recorder) setRound(round int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentRound = round
}

func (r *recorder) recordBackend(name string, args map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.firstEffective == nil {
		r.firstEffective = &now
	}
	r.backendCalls = append(r.backendCalls, backendCall{Name: name, Arguments: cloneMap(args), At: now, Round: r.currentRound})
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = nil
	r.proxyAttempts = nil
	r.backendCalls = nil
	r.firstDescribe = nil
	r.firstEffective = nil
	r.currentRound = 0
}

func (r *recorder) fill(m *trialMeasurement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m.CallStartedCount = len(r.starts)
	m.ProxyDispatchAttemptedCount = len(r.proxyAttempts)
	seen := map[string]bool{}
	for _, call := range r.backendCalls {
		m.EffectiveBackendCalls = append(m.EffectiveBackendCalls, expectedCall{Name: call.Name, Arguments: call.Arguments})
		seen[call.Name] = true
		m.RepeatedCallsByTool[call.Name]++
	}
	m.DistinctToolsUsed = len(seen)
	if r.firstDescribe != nil && r.firstEffective != nil {
		delta := r.firstEffective.Sub(*r.firstDescribe).Milliseconds()
		m.DescribeToEffectiveCallMS = &delta
	}
}

type delayedActionState struct {
	seenDescribe      bool
	seenSearch        bool
	seenEffective     bool
	firstActionStored bool
}

func updateDelayedActionMetrics(m *trialMeasurement, state *delayedActionState, strategy toolinvocation.DiscoveryStrategy, calls []toolinvocation.ToolCall, targetName string) {
	if !state.firstActionStored {
		m.FirstActionCategory = classifyFirstAction(strategy, calls, targetName)
		state.firstActionStored = true
	}
	roundHasDescribe := hasToolCall(calls, "tool_describe")
	roundHasSearch := hasToolCall(calls, "tool_search")
	roundHasEffective := hasEffectiveCall(strategy, calls, targetName)
	if roundHasEffective && !state.seenEffective {
		m.ToolDescribeBeforeEffective = state.seenDescribe
		m.ToolSearchBeforeEffective = state.seenSearch
		m.SameRoundDescribeEffective = roundHasDescribe
		state.seenEffective = true
	}
	if roundHasDescribe {
		state.seenDescribe = true
	}
	if roundHasSearch {
		state.seenSearch = true
	}
}

func classifyFirstAction(strategy toolinvocation.DiscoveryStrategy, calls []toolinvocation.ToolCall, targetName string) string {
	if len(calls) == 0 {
		return "no_tool"
	}
	hasDescribe := hasToolCall(calls, "tool_describe")
	hasEffective := hasEffectiveCall(strategy, calls, targetName)
	if hasDescribe && hasEffective {
		return "same_round_describe_plus_effective"
	}
	if hasEffective {
		if strategy == toolinvocation.DiscoveryProxy {
			return "proxy_reuse"
		}
		return "direct_effective_invocation"
	}
	if hasDescribe {
		return "describe"
	}
	if hasToolCall(calls, "tool_search") {
		return "search"
	}
	return "other_tool"
}

func hasToolCall(calls []toolinvocation.ToolCall, name string) bool {
	for _, call := range calls {
		if call.Name == name {
			return true
		}
	}
	return false
}

func hasEffectiveCall(strategy toolinvocation.DiscoveryStrategy, calls []toolinvocation.ToolCall, targetName string) bool {
	for _, call := range calls {
		if strategy == toolinvocation.DiscoveryProxy && call.Name == "tool_call" {
			name, _ := call.Arguments["name"].(string)
			if strings.TrimSpace(name) == targetName {
				return true
			}
			continue
		}
		if call.Name == targetName {
			return true
		}
	}
	return false
}

func (m *trialMeasurement) recordEffectiveAttempts(strategy toolinvocation.DiscoveryStrategy, calls []toolinvocation.ToolCall, results []toolinvocation.ToolResult, expected []expectedCall, round int) {
	for i, call := range calls {
		if i >= len(results) {
			return
		}
		if !isEffectiveAttempt(strategy, call.Name) {
			continue
		}
		toolResult := results[i]
		attempt := effectiveTargetAttempt{
			ModelRound:     round,
			TargetName:     toolResult.Name,
			Arguments:      call.Arguments,
			SchemaValid:    true,
			ExactArguments: false,
			BackendReached: toolResult.Failure == nil,
		}
		if strategy == toolinvocation.DiscoveryProxy && call.Name == "tool_call" {
			targetName, ok := call.Arguments["name"].(string)
			if ok {
				attempt.TargetName = strings.TrimSpace(targetName)
			} else {
				attempt.MalformedProxyEnvelope = true
				attempt.TargetName = toolResult.Name
			}
			nested, ok := call.Arguments["arguments"].(map[string]any)
			if ok {
				attempt.Arguments = nested
			} else {
				attempt.Arguments = nil
				attempt.MalformedProxyEnvelope = true
			}
			if toolResult.Name != "" && toolResult.Name != "tool_call" {
				attempt.TargetName = toolResult.Name
			}
		}
		if toolResult.Failure != nil {
			attempt.ValidationFailureCode = toolResult.Failure.Code
			attempt.ValidationFailureMessage = toolResult.Text
			if toolResult.Failure.Code == toolinvocation.FailureInvalidArguments {
				attempt.SchemaValid = false
				m.ValidationFailureCount++
			}
		}
		if attempt.MalformedProxyEnvelope {
			attempt.SchemaValid = false
			m.MalformedProxyEnvelopeCount++
		}
		if !attempt.MalformedProxyEnvelope && toolResult.Failure != nil && toolResult.Failure.Code == toolinvocation.FailureInvalidArguments {
			m.InvalidTargetArgumentCount++
		}
		attempt.ExactArguments = exactAttempt(attempt.TargetName, attempt.Arguments, expected)
		m.EffectiveTargetAttempts = append(m.EffectiveTargetAttempts, attempt)
	}
	m.EffectiveTargetAttemptCount = len(m.EffectiveTargetAttempts)
}

func (m *trialMeasurement) finalizeEffectiveAttemptMetrics() {
	m.EffectiveTargetAttemptCount = len(m.EffectiveTargetAttempts)
	if len(m.EffectiveTargetAttempts) == 0 {
		return
	}
	first := m.EffectiveTargetAttempts[0]
	m.FirstAttemptSchemaValid = boolPtr(first.SchemaValid)
	m.FirstAttemptExact = boolPtr(first.ExactArguments)
	for _, attempt := range m.EffectiveTargetAttempts {
		if attempt.BackendReached && attempt.ExactArguments {
			m.EventualExactBackendSuccess = true
			m.CorrectiveModelLoops = attempt.ModelRound - first.ModelRound
			return
		}
	}
	m.CorrectiveModelLoops = max(0, m.ModelCallCount-first.ModelRound)
}

func isEffectiveAttempt(strategy toolinvocation.DiscoveryStrategy, name string) bool {
	if strategy == toolinvocation.DiscoveryProxy && name == "tool_call" {
		return true
	}
	return name != "tool_search" && name != "tool_describe" && name != "tool_load" && name != "tool_call"
}

func exactAttempt(name string, args any, expected []expectedCall) bool {
	for _, call := range expected {
		if call.Name == name && digestJSON(args) == digestJSON(call.Arguments) {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}

func delayedFillerMessages(condition delayedReuseCondition) []chatMessage {
	if condition.TargetTokens <= 0 {
		return nil
	}
	content := buildDelayedFiller(condition)
	return []chatMessage{
		{Role: "user", Content: "Continue the project discussion with the following notes and status history."},
		{Role: "assistant", Content: content},
	}
}

func buildDelayedFiller(condition delayedReuseCondition) string {
	targetChars := condition.TargetTokens * 4
	var b strings.Builder
	for i := 0; b.Len() < targetChars; i++ {
		if condition.Kind == "interference" {
			fmt.Fprintf(&b, "Project note %04d: reviewed structured records for unrelated workflows.\n", i)
			fmt.Fprintf(&b, `{"record_id":"REC-%04d","customer":{"id":"CUST-Noise-%04d","full_name":"Pat Example %04d","mailing_address":{"line1":"%d Market Loop","city":"Exampleton","postal":"%05d","country":"CA"}},"tool_wrapper":{"function":"archive_customer_snapshot","payload":{"customer_profile":{"id":"ALT-%04d"}}},"order":{"order_id":"ORD-Noise-%04d","items":[{"sku":"NOISE-%04d","qty":%d,"price":%.2f}]}}`+"\n", i, i, i, i+10, 10000+i%80000, i, i, i, (i%7)+1, float64(i%100)+0.75)
			fmt.Fprintf(&b, "Related but irrelevant tool description %04d: customer_profile_archive accepts wrapper profile_data, not customer. Status: documented only.\n\n", i)
			continue
		}
		fmt.Fprintf(&b, "Project update %04d: merged routine documentation edits, checked CI logs, and summarized deployment status for unrelated services.\n", i)
		fmt.Fprintf(&b, "Status report %04d: queue depth normal, cache warm, test shard %d completed, operator note says no task-specific data was discussed here.\n", i, i%9)
		fmt.Fprintf(&b, "Log excerpt %04d: INFO worker=%02d component=build duration_ms=%d message=\"completed ordinary maintenance step\"\n\n", i, i%13, 100+i%500)
	}
	return b.String()
}

func normalizeToolCalls(providerCalls []providerToolCall, catalog toolinvocation.ToolCatalog) ([]toolinvocation.ToolCall, string) {
	defs := map[string]toolinvocation.ToolDefinition{}
	for _, def := range catalog.Tools {
		defs[def.Name] = def
	}
	calls := make([]toolinvocation.ToolCall, 0, len(providerCalls))
	for _, providerCall := range providerCalls {
		var args map[string]any
		if err := decodeJSON([]byte(providerCall.Function.Arguments), &args); err != nil {
			return nil, "malformed_tool_arguments"
		}
		call := toolinvocation.ToolCall{ID: providerCall.ID, Name: providerCall.Function.Name, Arguments: args}
		if def, ok := defs[providerCall.Function.Name]; ok {
			call.ToolID = def.ID
			call.DefinitionRevision = def.Revision
		}
		calls = append(calls, call)
	}
	return calls, ""
}

func requestedToolNames(providerCalls []providerToolCall) []string {
	names := make([]string, 0, len(providerCalls))
	for _, call := range providerCalls {
		names = append(names, call.Function.Name)
	}
	return names
}

func applyFinalScore(tc experimentCase, strategy toolinvocation.DiscoveryStrategy, m *trialMeasurement, finalContent string) {
	if m.FailureClassification != "" {
		m.TaskSuccess = false
		return
	}
	m.TaskSuccess, m.FailureClassification = scoreTrial(tc, strategy, *m, finalContent)
}

func scoreTrial(tc experimentCase, strategy toolinvocation.DiscoveryStrategy, m trialMeasurement, finalContent string) (bool, string) {
	if !strings.Contains(finalContent, tc.ExpectedFinalContains) {
		return false, "missing_expected_final_sentinel"
	}
	if !equalCalls(tc.ExpectedBackendCalls, m.EffectiveBackendCalls) {
		return false, "backend_call_mismatch"
	}
	if strategy == toolinvocation.DiscoveryProxy {
		proxyCalls := 0
		for _, name := range m.NormalizedToolSequence {
			if name != "tool_search" && name != "tool_describe" {
				proxyCalls++
			}
		}
		if proxyCalls > 0 && m.ProxyDispatchAttemptedCount == 0 {
			return false, "missing_proxy_dispatch_attempt"
		}
	}
	return true, ""
}

func equalCalls(expected, got []expectedCall) bool {
	if len(expected) != len(got) {
		return false
	}
	for i := range expected {
		if expected[i].Name != got[i].Name || digestJSON(expected[i].Arguments) != digestJSON(got[i].Arguments) {
			return false
		}
	}
	return true
}

type deepSeekClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func (c deepSeekClient) chat(ctx context.Context, messages []chatMessage, tools []providerTool) (chatResponse, error) {
	body := chatRequest{
		Model:           c.model,
		Messages:        messages,
		Tools:           tools,
		ToolChoice:      "auto",
		Temperature:     0,
		ReasoningEffort: "high",
		Thinking:        map[string]string{"type": "enabled"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return chatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return chatResponse{}, err
	}
	req.Header.Set("authorization", "Bearer "+c.apiKey)
	req.Header.Set("content-type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return chatResponse{}, err
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return chatResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return chatResponse{}, fmt.Errorf("deepseek status %d: %s", resp.StatusCode, trimForError(respRaw))
	}
	var decoded chatResponse
	if err := json.Unmarshal(respRaw, &decoded); err != nil {
		return chatResponse{}, err
	}
	if len(decoded.Choices) == 0 {
		return chatResponse{}, errors.New("deepseek returned no choices")
	}
	decoded.Message = decoded.Choices[0].Message
	decoded.FinishReason = decoded.Choices[0].FinishReason
	return decoded, nil
}

type chatRequest struct {
	Model           string            `json:"model"`
	Messages        []chatMessage     `json:"messages"`
	Tools           []providerTool    `json:"tools,omitempty"`
	ToolChoice      string            `json:"tool_choice"`
	Temperature     int               `json:"temperature"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Thinking        map[string]string `json:"thinking"`
}

type chatMessage struct {
	Role             string             `json:"role"`
	Content          string             `json:"content"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []providerToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
}

type chatResponse struct {
	SystemFingerprint string          `json:"system_fingerprint"`
	Choices           []choice        `json:"choices"`
	Usage             usage           `json:"usage"`
	Message           responseMessage `json:"-"`
	FinishReason      string          `json:"-"`
}

type choice struct {
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type responseMessage struct {
	Role             string             `json:"role"`
	Content          *string            `json:"content"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []providerToolCall `json:"tool_calls,omitempty"`
}

func (m responseMessage) content() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

func (m responseMessage) asHistory() chatMessage {
	return chatMessage{
		Role:             "assistant",
		Content:          m.content(),
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
	}
}

type providerToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function providerToolFunction `json:"function"`
}

type providerToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type usage struct {
	PromptTokens            int `json:"prompt_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type providerTool struct {
	Type     string           `json:"type"`
	Function providerFunction `json:"function"`
}

type providerFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func encodeProviderTools(catalog toolinvocation.ToolCatalog) []providerTool {
	tools := make([]providerTool, 0, len(catalog.Tools))
	for _, def := range catalog.Tools {
		tools = append(tools, providerTool{
			Type: "function",
			Function: providerFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.InputSchema,
			},
		})
	}
	return tools
}

func loadCases(path string, only string, prefix string) ([]experimentCase, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []experimentCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return nil, err
	}
	var out []experimentCase
	for _, tc := range cases {
		if only != "" && tc.ID != only {
			continue
		}
		if prefix != "" && !strings.HasPrefix(tc.ID, prefix) {
			continue
		}
		if only == "" || tc.ID == only {
			out = append(out, tc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no cases matched case=%q prefix=%q", only, prefix)
	}
	return out, nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func digestJSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func decodeJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func cloneMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func strategyName(strategy toolinvocation.DiscoveryStrategy) string {
	if strategy == toolinvocation.DiscoveryProxy {
		return "proxy"
	}
	return "direct"
}

func trimForError(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 600 {
		return text[:600] + "..."
	}
	return text
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
