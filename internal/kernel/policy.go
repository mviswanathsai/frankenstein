package kernel

import (
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// sessionBudgetExceeded checks cumulative token usage against the kernel's
// session-level limit. Returns true if limit is non-zero and exceeded.
func sessionBudgetExceeded(cfg Config, usage session.SessionUsage) bool {
	if cfg.SessionTokenLimit == 0 {
		return false
	}
	return usage.TotalInputTokens+usage.TotalOutputTokens > cfg.SessionTokenLimit
}

// modelCallRetryable returns true if the failure is classified as retryable.
func modelCallRetryable(failure *modelinvocation.ModelInvocationFailure) bool {
	if failure == nil {
		return false
	}
	return failure.Retryable
}

// emptyResponsePolicy decides what to do with a successful result that has
// no content and no tool calls. Returns true to end the turn ("completed").
// In v0, always returns true (accept as completed, matching Pi).
func emptyResponsePolicy(result *modelinvocation.ModelInvocationResult) bool {
	if result == nil {
		return true
	}
	return result.Content == "" && len(result.ToolCalls) == 0
}

// maxOutputPolicy decides whether to retry on max_output truncation and
// returns the new output budget. Returns (newBudget, shouldRetry).
// Raises by cfg.OutputBudgetRaise fraction each attempt.
func maxOutputPolicy(cfg Config, currentBudget int, attempt int) (int, bool) {
	if attempt >= cfg.MaxOutputRetries {
		return currentBudget, false
	}
	newBudget := currentBudget + int(float64(currentBudget)*cfg.OutputBudgetRaise)
	return newBudget, true
}

// toolResultRequestsStop checks if any ToolResult carries stop_requested.
func toolResultRequestsStop(results []toolinvocation.ToolResult) bool {
	for _, r := range results {
		if r.StopRequested {
			return true
		}
	}
	return false
}

// buildToolResultRecords converts ToolResults into SessionRecords for appending.
// Each record gets kind: tool_result, role: tool.
func buildToolResultRecords(turnID string, results []toolinvocation.ToolResult) []session.SessionRecord {
	records := make([]session.SessionRecord, 0, len(results))
	for _, r := range results {
		text := r.Text
		records = append(records, session.SessionRecord{
			TurnID:    turnID,
			Kind:      session.RecordToolResult,
			Role:      "tool",
			CallID:    r.CallID,
			Text:      &text,
			Refs:      r.Refs,
			ToolCalls: nil,
		})
	}
	return records
}

// buildAssistantRecord creates the final assistant message session record.
// kind: message, role: assistant.
func buildAssistantRecord(turnID string, content string) session.SessionRecord {
	return session.SessionRecord{
		TurnID: turnID,
		Kind:   session.RecordMessage,
		Role:   "assistant",
		Text:   &content,
	}
}
