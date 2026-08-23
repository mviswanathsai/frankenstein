package contextbuilder

import (
	"fmt"
	"slices"
	"strings"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// Prepare normalizes the session transcript into model-facing messages and
// assembles the ModelInput for one model call. The system prompt is echoed
// verbatim from the prefix; every structural change to the transcript is
// recorded as a normalization note.
func (s *Service) Prepare(req PrepareRequest) (BuiltContext, error) {
	if req.Prefix.SystemPrompt == "" {
		return BuiltContext{}, invalidRequest(req.ID, "prefix.system_prompt is required")
	}
	if len(req.Transcript) == 0 {
		return BuiltContext{}, invalidRequest(req.ID, "transcript must not be empty")
	}

	notes := []NormalizationNote{}
	normalized, notes := normalizeTranscript(req.Transcript, notes)
	msgs, notes := repairBrokenTurns(normalized, notes)
	injectPerCallContext(msgs, req.Dynamic)

	return BuiltContext{
		Input: modelinvocation.ModelInput{
			System:   req.Prefix.SystemPrompt,
			Messages: msgs,
		},
		Normalization: notes,
	}, nil
}

// normalizedMsg is one model-facing message plus the zero-based position of
// the transcript record that produced it. The index lets repair notes point
// back at the original transcript; synthesized messages carry -1.
type normalizedMsg struct {
	index int
	msg   modelinvocation.ModelMessage
}

// normalizeTranscript converts each SessionRecord into model-facing messages.
// Records with nothing model-facing to say (system notes, empty assistant
// turns, unknown kinds or roles) are dropped with a normalization note.
func normalizeTranscript(records []session.SessionRecord, notes []NormalizationNote) ([]normalizedMsg, []NormalizationNote) {
	msgs := make([]normalizedMsg, 0, len(records))
	for i, rec := range records {
		switch rec.Kind {
		case session.RecordMessage:
			switch rec.Role {
			case string(modelinvocation.RoleUser):
				msgs = append(msgs, normalizedMsg{
					index: i,
					msg: modelinvocation.ModelMessage{
						Role:    modelinvocation.RoleUser,
						Content: textOrEmpty(rec.Text),
					},
				})
			case string(modelinvocation.RoleAssistant):
				if rec.Text == nil && len(rec.ToolCalls) == 0 {
					notes = append(notes, droppedNote(i, ReasonEmptyTurn))
					continue
				}
				msgs = append(msgs, normalizedMsg{
					index: i,
					msg: modelinvocation.ModelMessage{
						Role:      modelinvocation.RoleAssistant,
						Content:   textOrEmpty(rec.Text),
						ToolCalls: mapToolCalls(rec.ToolCalls),
					},
				})
			case string(modelinvocation.RoleTool):
				msgs = append(msgs, normalizedMsg{
					index: i,
					msg: modelinvocation.ModelMessage{
						Role:    modelinvocation.RoleTool,
						Content: textOrEmpty(rec.Text),
						CallID:  rec.CallID,
					},
				})
			default:
				// A message record with an unknown role cannot be mapped to a
				// model-facing message. Dropping keeps the output valid.
				notes = append(notes, droppedNote(i, ReasonEmptyTurn))
			}
		case session.RecordToolCall:
			msg := modelinvocation.ModelMessage{
				Role:      modelinvocation.RoleAssistant,
				ToolCalls: mapToolCalls(rec.ToolCalls),
			}
			if rec.Text != nil {
				msg.Content = *rec.Text
			}
			msgs = append(msgs, normalizedMsg{index: i, msg: msg})
		case session.RecordToolResult:
			msgs = append(msgs, normalizedMsg{
				index: i,
				msg: modelinvocation.ModelMessage{
					Role:    modelinvocation.RoleTool,
					Content: textOrEmpty(rec.Text),
					CallID:  rec.CallID,
				},
			})
		case session.RecordSystemNote:
			// System notes are scaffolding, never model input.
			notes = append(notes, droppedNote(i, ReasonEmptyTurn))
		default:
			// An unknown record kind cannot be mapped to a model-facing
			// message. Dropping keeps the output valid.
			notes = append(notes, droppedNote(i, ReasonEmptyTurn))
		}
	}
	return msgs, notes
}

// droppedNote records that the transcript record at index was removed.
func droppedNote(index int, reason string) NormalizationNote {
	return NormalizationNote{
		TranscriptIndex: index,
		Action:          ActionDropped,
		Reason:          reason,
	}
}

// textOrEmpty dereferences a nullable record text, returning "" when absent.
func textOrEmpty(text *string) string {
	if text == nil {
		return ""
	}
	return *text
}

// mapToolCalls converts session-local tool calls to the canonical
// toolinvocation shape the model-facing messages carry. An absent call list
// stays nil so an assistant message without tool calls keeps its zero shape.
func mapToolCalls(calls []session.ToolCall) []toolinvocation.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	mapped := make([]toolinvocation.ToolCall, 0, len(calls))
	for _, stc := range calls {
		mapped = append(mapped, toolinvocation.ToolCall{
			ID:                 stc.ID,
			ToolID:             stc.ToolID,
			DefinitionRevision: stc.DefinitionRevision,
			Name:               stc.Name,
			Arguments:          stc.Arguments,
		})
	}
	return mapped
}

// repairBrokenTurns makes every tool call answered and every tool result
// referenced. Unanswered calls get a synthesized placeholder result inserted
// directly after their assistant message; tool results whose CallID matches no
// call are dropped. Both transforms are recorded as normalization notes.
func repairBrokenTurns(nmsgs []normalizedMsg, notes []NormalizationNote) ([]modelinvocation.ModelMessage, []NormalizationNote) {
	called := calledIDs(nmsgs)
	answered := answeredIDs(nmsgs)

	// Pass 1: synthesize a result for every tool call that was never answered,
	// inserted directly after the assistant message that made the call.
	repaired := make([]normalizedMsg, 0, len(nmsgs))
	for _, nm := range nmsgs {
		repaired = append(repaired, nm)
		if nm.msg.Role != modelinvocation.RoleAssistant || len(nm.msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range nm.msg.ToolCalls {
			if _, ok := answered[tc.ID]; ok {
				continue
			}
			notes = append(notes, NormalizationNote{
				TranscriptIndex: -1,
				Action:          ActionSynthesized,
				Reason:          ReasonMissingToolResult,
				SynthesizedText: missingToolResultText,
			})
			repaired = append(repaired, normalizedMsg{
				index: -1,
				msg: modelinvocation.ModelMessage{
					Role:    modelinvocation.RoleTool,
					CallID:  tc.ID,
					Content: missingToolResultText,
				},
			})
		}
	}

	// Pass 2: drop tool results whose CallID matches no tool call. Synthesized
	// results always answer the call they follow, so they survive this pass.
	out := make([]modelinvocation.ModelMessage, 0, len(repaired))
	for _, nm := range repaired {
		if nm.msg.Role == modelinvocation.RoleTool {
			if _, ok := called[nm.msg.CallID]; !ok {
				notes = append(notes, droppedNote(nm.index, ReasonOrphanedToolResult))
				continue
			}
		}
		out = append(out, nm.msg)
	}
	return out, notes
}

// missingToolResultText is the placeholder synthesized when a tool call was
// never answered in the transcript.
const missingToolResultText = "Tool result not available."

// calledIDs collects every tool call ID referenced by assistant messages.
func calledIDs(nmsgs []normalizedMsg) map[string]struct{} {
	called := make(map[string]struct{})
	for _, nm := range nmsgs {
		if nm.msg.Role != modelinvocation.RoleAssistant {
			continue
		}
		for _, tc := range nm.msg.ToolCalls {
			called[tc.ID] = struct{}{}
		}
	}
	return called
}

// answeredIDs collects the CallID of every tool result message.
func answeredIDs(nmsgs []normalizedMsg) map[string]struct{} {
	answered := make(map[string]struct{})
	for _, nm := range nmsgs {
		if nm.msg.Role == modelinvocation.RoleTool {
			answered[nm.msg.CallID] = struct{}{}
		}
	}
	return answered
}

// injectPerCallContext appends the per-call context of every dynamic response
// to the last user-role message. Responses are processed in request order;
// slot groups are sorted by name within each response so the appended text is
// deterministic. If there is no user message, nothing is injected.
func injectPerCallContext(msgs []modelinvocation.ModelMessage, responses []contextprovider.ContextResponse) {
	idx := lastUserIndex(msgs)
	if idx < 0 {
		return
	}
	blocks := perCallBlocks(responses)
	if blocks == "" {
		return
	}
	if msgs[idx].Content != "" {
		msgs[idx].Content += "\n" + blocks
	} else {
		msgs[idx].Content = blocks
	}
}

// lastUserIndex returns the index of the last user-role message, or -1.
func lastUserIndex(msgs []modelinvocation.ModelMessage) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == modelinvocation.RoleUser {
			return i
		}
	}
	return -1
}

// perCallBlocks renders the per-call context of all responses as XML-delimited
// blocks, one wrapper per slot group with its candidates inside:
//
//	<per_call_context slot="memory">
//	<candidate id="abc123">...content...</candidate>
//	</per_call_context>
//
// Response order is preserved; slot order within a response is alphabetical.
func perCallBlocks(responses []contextprovider.ContextResponse) string {
	var sb strings.Builder
	first := true
	for _, resp := range responses {
		for _, block := range perCallBlocksForCandidates(resp.Candidates) {
			if !first {
				sb.WriteByte('\n')
			}
			first = false
			sb.WriteString(block)
		}
	}
	return sb.String()
}

// perCallBlocksForCandidates renders one XML block per slot group, using the
// same grouping rule as assemble (absent or empty slot metadata groups under
// "context").
func perCallBlocksForCandidates(candidates []contextprovider.ContextCandidate) []string {
	groups := make(map[string][]contextprovider.ContextCandidate)
	for _, c := range candidates {
		name := candidateSlot(c)
		groups[name] = append(groups[name], c)
	}

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		return strings.Compare(a, b)
	})

	blocks := make([]string, 0, len(names))
	for _, name := range names {
		var sb strings.Builder
		fmt.Fprintf(&sb, "<per_call_context slot=\"%s\">\n", name)
		for _, c := range groups[name] {
			fmt.Fprintf(&sb, "<candidate id=\"%s\">%s</candidate>\n", c.ID, c.Content)
		}
		sb.WriteString("</per_call_context>")
		blocks = append(blocks, sb.String())
	}
	return blocks
}
