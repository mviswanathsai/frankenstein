package contextbuilder

import (
	"fmt"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// Prepare normalizes a transcript into a model input, repairing broken tool
// turns and injecting per-call context into the last user message.
func (s *Service) Prepare(req PrepareRequest) (BuiltContext, error) {
	if req.Prefix.SystemPrompt == "" {
		return BuiltContext{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureInvalidRequest,
			Message:   "missing prefix: system_prompt is required",
		}
	}
	if len(req.Transcript) == 0 {
		return BuiltContext{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureInvalidRequest,
			Message:   "empty transcript: at least one session record is required",
		}
	}

	var messages []modelinvocation.ModelMessage
	var notes []NormalizationNote

	// Phase 1: normalize each session record to one or more ModelMessage values.
	for i, rec := range req.Transcript {
		switch rec.Kind {
		case session.RecordMessage:
			m, n := normalizeMessage(rec, i)
			messages = append(messages, m...)
			notes = append(notes, n...)

		case session.RecordToolCall:
			m, n := normalizeToolCall(rec, i)
			messages = append(messages, m...)
			notes = append(notes, n...)

		case session.RecordToolResult:
			m, n := normalizeToolResult(rec, i)
			messages = append(messages, m...)
			notes = append(notes, n...)

		case session.RecordSystemNote:
			// system notes are dropped entirely.
			notes = append(notes, NormalizationNote{
				TranscriptIndex: i,
				Action:          ActionDropped,
				Reason:          ReasonEmptyTurn,
			})
		}
	}

	// Phase 2: repair broken tool turns.
	messages, repairNotes := repairBrokenTurns(messages)
	notes = append(notes, repairNotes...)

	// Phase 3: inject per-call context into the last user message.
	messages = injectPerCallContext(messages, req.ContextBundles)

	modelInput := modelinvocation.ModelInput{
		System:   req.Prefix.SystemPrompt,
		Messages: messages,
	}

	return BuiltContext{
		Input:         modelInput,
		Normalization: notes,
	}, nil
}

// textFromRecord returns the string value of rec.Text, or "" if nil.
func textFromRecord(rec session.SessionRecord) string {
	if rec.Text != nil {
		return *rec.Text
	}
	return ""
}

// mapToolCalls converts []session.ToolCall to []toolinvocation.ToolCall.
func mapToolCalls(stcs []session.ToolCall) []toolinvocation.ToolCall {
	if len(stcs) == 0 {
		return nil
	}
	out := make([]toolinvocation.ToolCall, len(stcs))
	for i, stc := range stcs {
		out[i] = toolinvocation.ToolCall{
			ID:                 stc.ID,
			ToolID:             stc.ToolID,
			DefinitionRevision: stc.DefinitionRevision,
			Name:               stc.Name,
			Arguments:          stc.Arguments,
		}
	}
	return out
}

// normalizeMessage handles kind=message records.
func normalizeMessage(rec session.SessionRecord, idx int) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	switch rec.Role {
	case "user":
		return []modelinvocation.ModelMessage{{
			Role:    modelinvocation.RoleUser,
			Content: textFromRecord(rec),
		}}, nil

	case "assistant":
		text := textFromRecord(rec)
		toolCalls := mapToolCalls(rec.ToolCalls)
		// Drop empty assistant turns (no text, no tool calls).
		if text == "" && len(toolCalls) == 0 {
			return nil, []NormalizationNote{{
				TranscriptIndex: idx,
				Action:          ActionDropped,
				Reason:          ReasonEmptyTurn,
			}}
		}
		return []modelinvocation.ModelMessage{{
			Role:      modelinvocation.RoleAssistant,
			Content:   text,
			ToolCalls: toolCalls,
		}}, nil

	case "tool":
		return []modelinvocation.ModelMessage{{
			Role:    modelinvocation.RoleTool,
			Content: textFromRecord(rec),
			CallID:  rec.CallID,
		}}, nil

	default:
		// unknown role treated as dropped.
		return nil, []NormalizationNote{{
			TranscriptIndex: idx,
			Action:          ActionDropped,
			Reason:          ReasonEmptyTurn,
		}}
	}
}

// normalizeToolCall handles kind=tool_call records.
func normalizeToolCall(rec session.SessionRecord, idx int) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	toolCalls := mapToolCalls(rec.ToolCalls)
	msg := modelinvocation.ModelMessage{
		Role:      modelinvocation.RoleAssistant,
		ToolCalls: toolCalls,
	}
	if rec.Text != nil {
		msg.Content = *rec.Text
	}
	return []modelinvocation.ModelMessage{msg}, nil
}

// normalizeToolResult handles kind=tool_result records.
func normalizeToolResult(rec session.SessionRecord, idx int) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	return []modelinvocation.ModelMessage{{
		Role:    modelinvocation.RoleTool,
		CallID:  rec.CallID,
		Content: textFromRecord(rec),
	}}, nil
}

// repairBrokenTurns walks normalized messages and repairs broken tool
// call/result pairs. Returns the repaired message list and any notes.
func repairBrokenTurns(messages []modelinvocation.ModelMessage) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	if len(messages) == 0 {
		return messages, nil
	}

	var notes []NormalizationNote

	// Phase A: drop orphaned tool results (CallID with no matching tool call).
	callIDsWithCalls := make(map[string]bool)
	for _, m := range messages {
		if m.Role == modelinvocation.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				callIDsWithCalls[tc.ID] = true
			}
		}
	}

	var filtered []modelinvocation.ModelMessage
	for _, m := range messages {
		if m.Role == modelinvocation.RoleTool && m.CallID != "" && !callIDsWithCalls[m.CallID] {
			notes = append(notes, NormalizationNote{
				TranscriptIndex: -1,
				Action:          ActionDropped,
				Reason:          ReasonOrphanedToolResult,
			})
			continue
		}
		filtered = append(filtered, m)
	}
	messages = filtered

	// Phase B: synthesize missing tool results.
	// Walk forward. For each assistant message with ToolCalls, collect all
	// CallIDs that don't appear in any subsequent tool message before the
	// next assistant message with ToolCalls.
	var repaired []modelinvocation.ModelMessage
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		repaired = append(repaired, m)

		if m.Role != modelinvocation.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}

		// Collect the set of call IDs introduced by this message.
		pending := make(map[string]bool)
		for _, tc := range m.ToolCalls {
			pending[tc.ID] = true
		}

		// Scan subsequent tool messages to find which call IDs are matched.
		for j := i + 1; j < len(messages); j++ {
			m2 := messages[j]
			if m2.Role == modelinvocation.RoleAssistant && len(m2.ToolCalls) > 0 {
				// Next assistant with tool calls — stop scanning.
				break
			}
			if m2.Role == modelinvocation.RoleTool && m2.CallID != "" {
				delete(pending, m2.CallID)
			}
		}

		// Synthesize tool result messages for unmatched call IDs, inserted
		// right after the current assistant message.
		for _, tc := range m.ToolCalls {
			if !pending[tc.ID] {
				continue
			}
			notes = append(notes, NormalizationNote{
				TranscriptIndex: -1,
				Action:          ActionSynthesized,
				Reason:          ReasonMissingToolResult,
				SynthesizedText: "Tool result not available.",
			})
			repaired = append(repaired, modelinvocation.ModelMessage{
				Role:    modelinvocation.RoleTool,
				CallID:  tc.ID,
				Content: "Tool result not available.",
			})
		}
	}

	return repaired, notes
}

// injectPerCallContext appends per-call context blocks to the LAST user
// message in the message list.
func injectPerCallContext(messages []modelinvocation.ModelMessage, bundles []contextprovider.ContextBundle) []modelinvocation.ModelMessage {
	if len(bundles) == 0 {
		return messages
	}

	// Find the last user message.
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == modelinvocation.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx == -1 {
		return messages
	}

	var block string
	for _, b := range bundles {
		for slot, candidates := range b.PerCall.Buckets {
			block += fmt.Sprintf("<per_call_context slot=%q>\n", slot)
			for _, c := range candidates {
				block += fmt.Sprintf("<candidate id=%q>%s</candidate>\n", c.ID, c.Content)
			}
			block += "</per_call_context>\n"
		}
	}

	messages[lastUserIdx].Content += block
	return messages
}
