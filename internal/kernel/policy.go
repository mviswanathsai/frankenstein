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

// toolResultRequestsStop checks if any ToolResult carries stop_requested.
func toolResultRequestsStop(results []toolinvocation.ToolResult) bool {
	for _, r := range results {
		if r.StopRequested {
			return true
		}
	}
	return false
}

// buildToolResultRecords converts ToolResults into SessionRecords for
// appending via write_record. Record identity and turn grouping are owned by
// the session service, so the kernel leaves id and turn_id empty.
func buildToolResultRecords(results []toolinvocation.ToolResult) []session.SessionRecord {
	records := make([]session.SessionRecord, 0, len(results))
	for _, r := range results {
		text := r.Text
		records = append(records, session.SessionRecord{
			Kind:   session.RecordToolResult,
			CallID: r.CallID,
			Text:   &text,
			Refs:   r.Refs,
		})
	}
	return records
}
