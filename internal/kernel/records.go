package kernel

import (
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// buildToolResultRecords converts ToolResults into SessionRecords for appending.
// Each record gets kind: tool_result, role: tool. The kernel calls this after
// tool execution to translate across the toolinvocation → session boundary.
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

// buildAssistantRecord creates the final assistant message session record
// after a turn completes. kind: message, role: assistant.
func buildAssistantRecord(turnID string, content string) session.SessionRecord {
	return session.SessionRecord{
		TurnID: turnID,
		Kind:   session.RecordMessage,
		Role:   "assistant",
		Text:   &content,
	}
}
