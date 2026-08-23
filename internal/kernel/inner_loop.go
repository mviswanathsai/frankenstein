package kernel

import (
	"context"
	"strconv"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/contextrenderer"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// innerLoopResult is the outcome of one model-call loop. The caller
// materializes newRecords into the session and owns the terminal turn event;
// the loop never appends to Session itself.
type innerLoopResult struct {
	exitReason   ExitReason
	finalContent string
	newRecords   []session.SessionRecord
}

// runInnerLoop executes the model-call loop for one turn: render, invoke,
// dispatch on stop reason, execute tools, and repeat until an exit condition
// fires or the turn budget is exhausted. Tool results are returned as
// newRecords for the caller to persist; the loop does not touch Session,
// emit the terminal turn event, or apply compression or memory policies.
func runInnerLoop(
	ctx context.Context,
	cfg Config,
	tools ToolInvoker,
	model ModelInvoker,
	renderer ContextRenderer,
	observer TurnObserver,
	sessionID string,
	turnID string,
	resolvedModel string,
	rendererConfig contextrenderer.Config,
	transcript []session.SessionRecord,
	dynamic *contextprovider.ContextResponse,
) innerLoopResult {
	var newRecords []session.SessionRecord

	// The invocation catalog starts as the session-frozen catalog held in
	// config. It changes only through catalog transitions mid-turn.
	activeCatalog := rendererConfig.Tools

	for iter := 1; iter <= cfg.TurnBudget; iter++ {
		iterStr := strconv.Itoa(iter)

		// Render the model input from the session config, the current
		// transcript, and the per-turn dynamic context response.
		rendered, err := renderer.Render(contextrenderer.RenderRequest{
			ID:             "rnd_" + turnID + "_" + iterStr,
			SessionID:      sessionID,
			Transcript:     transcript,
			DynamicContext: dynamic,
			Config:         &rendererConfig,
		})
		if err != nil {
			return innerLoopResult{exitReason: ExitInternalError, newRecords: newRecords}
		}

		// Invoke the model with the rendered input and the active catalog.
		req := modelinvocation.ModelInvocationRequest{
			ID:        "inv_" + turnID + "_" + iterStr,
			SessionID: sessionID,
			TurnID:    turnID,
			Model:     resolvedModel,
			Input:     rendered.Input,
			Catalog:   activeCatalog,
		}

		result, failure := invokeWithRetry(ctx, cfg, model, req)
		if failure != nil {
			return innerLoopResult{exitReason: ExitModelError, newRecords: newRecords}
		}

		// Stream the model's output as it arrives (v0 delivers it in one shot).
		if result.Content != "" && observer != nil {
			observer.OnModelContent(result.Content)
		}
		if result.Reasoning != "" && observer != nil {
			observer.OnReasoning(result.Reasoning)
		}

		// Dispatch on the stop reason.
		switch result.StopReason {
		case modelinvocation.StopEndTurn:
			if len(result.ToolCalls) == 0 {
				return innerLoopResult{
					exitReason:   ExitCompleted,
					finalContent: result.Content,
					newRecords:   newRecords,
				}
			}
		case modelinvocation.StopToolCalls:
			// Continue to the empty-response check and tool execution.
		case modelinvocation.StopMaxOutput, modelinvocation.StopContentFilter:
			return innerLoopResult{exitReason: ExitModelError, newRecords: newRecords}
		}

		// Accept an empty response as a finished turn.
		if emptyResponsePolicy(result) {
			return innerLoopResult{exitReason: ExitCompleted, newRecords: newRecords}
		}

		// Nothing to execute: loop again (bounded by the turn budget).
		if len(result.ToolCalls) == 0 {
			continue
		}

		// Stream tool-call starts before execution.
		if observer != nil {
			for _, tc := range result.ToolCalls {
				observer.OnToolCallStart(tc.Name, tc.Arguments)
			}
		}

		// Execute the batch against the active catalog.
		execResult, execFailure := tools.Execute(ctx, toolinvocation.ToolExecutionRequest{
			ID:        "exec_" + turnID + "_" + iterStr,
			CatalogID: activeCatalog.ID,
			SessionID: sessionID,
			TurnID:    turnID,
			Calls:     result.ToolCalls,
		})
		if execFailure != nil {
			// Persist any partial results so they are not lost.
			newRecords = append(newRecords, buildToolResultRecords(execFailure.Results)...)
			return innerLoopResult{exitReason: ExitToolError, newRecords: newRecords}
		}

		// Stream results and append them to the running transcript.
		if observer != nil {
			for _, r := range execResult.Results {
				observer.OnToolResult(r)
			}
		}
		toolRecords := buildToolResultRecords(execResult.Results)
		transcript = append(transcript, toolRecords...)
		newRecords = append(newRecords, toolRecords...)

		if toolResultRequestsStop(execResult.Results) {
			return innerLoopResult{exitReason: ExitToolStopRequested, newRecords: newRecords}
		}

		// Adopt a catalog transition, or refresh the catalog when the
		// transition is stale. A failed refresh keeps the current catalog.
		if execResult.CatalogTransition != nil {
			if execResult.CatalogTransition.BaseCatalogID == activeCatalog.ID {
				activeCatalog = &execResult.CatalogTransition.Catalog
			} else if listed, _ := tools.ListTools(ctx, toolinvocation.ToolCatalogRequest{
				ID:        "list_" + turnID + "_" + iterStr,
				SessionID: sessionID,
				TurnID:    turnID,
			}); listed != nil {
				activeCatalog = &listed.Catalog
			}
		}
	}

	return innerLoopResult{exitReason: ExitBudgetExhausted, newRecords: newRecords}
}

// invokeWithRetry calls the model, retrying retryable failures up to
// cfg.MaxRetries times. Returns the first successful result, or the last
// failure. Retries reuse the same request: same provider, model, and input.
func invokeWithRetry(ctx context.Context, cfg Config, model ModelInvoker, req modelinvocation.ModelInvocationRequest) (*modelinvocation.ModelInvocationResult, *modelinvocation.ModelInvocationFailure) {
	var lastFailure *modelinvocation.ModelInvocationFailure
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result, failure := model.Invoke(ctx, req)
		if failure == nil {
			return result, nil
		}
		lastFailure = failure
		if !modelCallRetryable(failure) || ctx.Err() != nil {
			break
		}
	}
	return nil, lastFailure
}
