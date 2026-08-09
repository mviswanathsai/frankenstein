package contextbuilder

import (
	"fmt"
	"sort"
	"strings"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const missingToolResultText = "Tool result not available."

// Prepare normalizes session records into the provider-neutral model input.
func (s *Service) Prepare(req PrepareRequest) (BuiltContext, error) {
	if req.Prefix.SystemPrompt == "" {
		return BuiltContext{}, fmt.Errorf("%s: missing prefix", FailureInvalidRequest)
	}
	if len(req.Transcript) == 0 {
		return BuiltContext{}, fmt.Errorf("%s: empty transcript", FailureInvalidRequest)
	}

	messages := make([]modelinvocation.ModelMessage, 0, len(req.Transcript))
	notes := make([]NormalizationNote, 0)
	for i, record := range req.Transcript {
		text := ""
		if record.Text != nil {
			text = *record.Text
		}

		switch record.Kind {
		case session.RecordMessage:
			switch record.Role {
			case string(modelinvocation.RoleUser):
				messages = append(messages, modelinvocation.ModelMessage{
					Role: modelinvocation.RoleUser, Content: text,
				})
			case string(modelinvocation.RoleAssistant):
				if record.Text == nil && len(record.ToolCalls) == 0 {
					notes = append(notes, droppedNote(i, ReasonEmptyTurn))
					continue
				}
				messages = append(messages, modelinvocation.ModelMessage{
					Role:      modelinvocation.RoleAssistant,
					Content:   text,
					ToolCalls: mapToolCalls(record.ToolCalls),
				})
			case string(modelinvocation.RoleTool):
				messages = append(messages, modelinvocation.ModelMessage{
					Role: modelinvocation.RoleTool, Content: text, CallID: record.CallID,
				})
			}
		case session.RecordToolCall:
			messages = append(messages, modelinvocation.ModelMessage{
				Role:      modelinvocation.RoleAssistant,
				Content:   text,
				ToolCalls: mapToolCalls(record.ToolCalls),
			})
		case session.RecordToolResult:
			messages = append(messages, modelinvocation.ModelMessage{
				Role: modelinvocation.RoleTool, Content: text, CallID: record.CallID,
			})
		case session.RecordSystemNote:
			notes = append(notes, droppedNote(i, ReasonEmptyTurn))
		}
	}

	messages, repairNotes := repairToolTurns(messages)
	notes = append(notes, repairNotes...)
	injectPerCallContext(messages, req.ContextBundles)

	return BuiltContext{
		Input: modelinvocation.ModelInput{
			System:   req.Prefix.SystemPrompt,
			Messages: messages,
		},
		Normalization: notes,
	}, nil
}

func mapToolCalls(calls []session.ToolCall) []toolinvocation.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	mapped := make([]toolinvocation.ToolCall, 0, len(calls))
	for _, call := range calls {
		mapped = append(mapped, toolinvocation.ToolCall{
			ID: call.ID, ToolID: call.ToolID, DefinitionRevision: call.DefinitionRevision,
			Name: call.Name, Arguments: call.Arguments,
		})
	}
	return mapped
}

func droppedNote(index int, reason string) NormalizationNote {
	return NormalizationNote{TranscriptIndex: index, Action: ActionDropped, Reason: reason}
}

func repairToolTurns(messages []modelinvocation.ModelMessage) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	callIDs := make(map[string]struct{})
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			callIDs[call.ID] = struct{}{}
		}
	}

	resultIDs := make(map[string]struct{})
	for _, message := range messages {
		if message.Role == modelinvocation.RoleTool {
			if _, ok := callIDs[message.CallID]; ok {
				resultIDs[message.CallID] = struct{}{}
			}
		}
	}

	repaired := make([]modelinvocation.ModelMessage, 0, len(messages))
	notes := make([]NormalizationNote, 0)
	for _, message := range messages {
		if message.Role == modelinvocation.RoleTool {
			if _, ok := callIDs[message.CallID]; !ok {
				notes = append(notes, droppedNote(-1, ReasonOrphanedToolResult))
				continue
			}
			repaired = append(repaired, message)
			continue
		}

		repaired = append(repaired, message)
		if message.Role != modelinvocation.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for _, call := range message.ToolCalls {
			if _, ok := resultIDs[call.ID]; ok {
				continue
			}
			notes = append(notes, NormalizationNote{
				TranscriptIndex: -1,
				Action:          ActionSynthesized,
				Reason:          ReasonMissingToolResult,
				SynthesizedText: missingToolResultText,
			})
			repaired = append(repaired, modelinvocation.ModelMessage{
				Role: modelinvocation.RoleTool, CallID: call.ID, Content: missingToolResultText,
			})
		}
	}
	return repaired, notes
}

func injectPerCallContext(messages []modelinvocation.ModelMessage, bundles []contextprovider.ContextBundle) {
	lastUser := -1
	for i := range messages {
		if messages[i].Role == modelinvocation.RoleUser {
			lastUser = i
		}
	}
	if lastUser < 0 {
		return
	}

	var blocks []string
	for _, bundle := range bundles {
		slots := make([]string, 0, len(bundle.PerCall.Buckets))
		for slot := range bundle.PerCall.Buckets {
			slots = append(slots, string(slot))
		}
		sort.Strings(slots)
		for _, slot := range slots {
			for _, candidate := range bundle.PerCall.Buckets[contextprovider.ContextSlot(slot)] {
				blocks = append(blocks, fmt.Sprintf(
					"<per_call_context slot=\"%s\">\n<candidate id=\"%s\">%s</candidate>\n</per_call_context>",
					slot, candidate.ID, candidate.Content,
				))
			}
		}
	}
	if len(blocks) > 0 {
		messages[lastUser].Content += "\n\n" + strings.Join(blocks, "\n\n")
	}
}
