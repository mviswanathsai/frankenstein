package contextrenderer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"text/template"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// renderTemplateText is the fixed system prompt template. It is parsed once at
// init; the source is a constant, so execution is deterministic and no map
// iteration ever affects the output. Shape: the opener, one <name>content</name>
// block per material section in config order, then an <available_tools> block
// in catalog order only when Tools is non-nil.
const renderTemplateText = `You are a helpful assistant.{{- range .Material}}

<{{.Name}}>{{.Content}}</{{.Name}}>{{end}}{{if .Tools}}

<available_tools>
{{- range .Tools}}
- {{.Name}}: {{.Description}}
{{- end}}
</available_tools>
{{- end}}`

// renderTemplate is the parsed form of renderTemplateText.
var renderTemplate = template.Must(template.New("system_prompt").Parse(renderTemplateText))

// renderTemplateData is the template-friendly view of a Config. The model is
// deliberately absent: config carries it, but the default template does not
// render it.
type renderTemplateData struct {
	Material []MaterialSection
	Tools    []toolDefStub
}

// toolDefStub carries the tool-awareness text the template renders. The full
// input schema travels separately with the catalog.
type toolDefStub struct {
	Name        string
	Description string
}

// Render derives the system prompt and its content-derived ID from config,
// normalizes the transcript into model-facing messages, and renders the
// dynamic context candidates into the model messages. It never mutates its
// inputs; transforms happen only in the ModelInput it returns.
func (s *Service) Render(req RenderRequest) (RenderResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return RenderResult{}, invalidRequest(req.ID, "id is required")
	}
	if len(req.Transcript) == 0 {
		return RenderResult{}, invalidRequest(req.ID, "transcript must not be empty")
	}
	if req.DynamicContext == nil {
		return RenderResult{}, invalidRequest(req.ID, "dynamic_context is required")
	}
	if req.Config == nil {
		return RenderResult{}, invalidRequest(req.ID, "config is required")
	}

	systemPrompt, err := deriveSystemPrompt(req.ID, *req.Config)
	if err != nil {
		return RenderResult{}, err
	}

	msgs := normalizeTranscript(req.Transcript)
	msgs = repairBrokenTurns(msgs)
	injectCandidates(msgs, req.DynamicContext)

	sum := sha256.Sum256([]byte(systemPrompt))
	return RenderResult{
		RequestID:      req.ID,
		Input:          modelinvocation.ModelInput{System: systemPrompt, Messages: msgs},
		SystemPromptID: hex.EncodeToString(sum[:])[:16],
	}, nil
}

// deriveSystemPrompt renders the system prompt from config through the fixed
// template. It returns the full prompt text and a template_error failure when
// the template cannot execute.
func deriveSystemPrompt(requestID string, config Config) (string, error) {
	data := renderTemplateData{Material: config.Material}
	if config.Tools != nil {
		data.Tools = toolStubs(config.Tools)
	}
	var rendered strings.Builder
	if err := renderTemplate.Execute(&rendered, data); err != nil {
		return "", templateError(requestID, fmt.Sprintf("render system prompt template: %v", err))
	}
	return rendered.String(), nil
}

// toolStubs derives the template's tool-awareness text from the catalog,
// preserving catalog order.
func toolStubs(catalog *toolinvocation.ToolCatalog) []toolDefStub {
	stubs := make([]toolDefStub, 0, len(catalog.Tools))
	for _, def := range catalog.Tools {
		stubs = append(stubs, toolDefStub{Name: def.Name, Description: def.Description})
	}
	return stubs
}

// normalizeTranscript converts each SessionRecord into model-facing messages.
// Records with nothing model-facing to say (system notes, empty assistant
// turns, unknown kinds or roles, and the obsolete role=tool message record)
// are dropped. No normalization note is produced: transforms are applied, not
// recorded.
func normalizeTranscript(records []session.SessionRecord) []modelinvocation.ModelMessage {
	msgs := make([]modelinvocation.ModelMessage, 0, len(records))
	for _, rec := range records {
		switch rec.Kind {
		case session.RecordMessage:
			switch rec.Role {
			case string(modelinvocation.RoleUser):
				msgs = append(msgs, modelinvocation.ModelMessage{
					Role:    modelinvocation.RoleUser,
					Content: textOrEmpty(rec.Text),
				})
			case string(modelinvocation.RoleAssistant):
				if rec.Text == nil && len(rec.ToolCalls) == 0 {
					continue // empty assistant turn
				}
				msgs = append(msgs, modelinvocation.ModelMessage{
					Role:      modelinvocation.RoleAssistant,
					Content:   textOrEmpty(rec.Text),
					ToolCalls: mapToolCalls(rec.ToolCalls),
				})
			default:
				// A message record with an unknown role cannot be mapped to a
				// model-facing message. session.v0.3 has no tool-role messages
				// (tool results are their own record kind), so this also drops
				// the obsolete role=tool case.
			}
		case session.RecordToolCall:
			msg := modelinvocation.ModelMessage{
				Role:      modelinvocation.RoleAssistant,
				ToolCalls: mapToolCalls(rec.ToolCalls),
			}
			if rec.Text != nil {
				msg.Content = *rec.Text
			}
			msgs = append(msgs, msg)
		case session.RecordToolResult:
			msgs = append(msgs, modelinvocation.ModelMessage{
				Role:    modelinvocation.RoleTool,
				Content: textOrEmpty(rec.Text),
				CallID:  rec.CallID,
			})
		case session.RecordSystemNote:
			// System notes are scaffolding, never model input.
		default:
			// An unknown record kind cannot be mapped to a model-facing
			// message.
		}
	}
	return msgs
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

// missingToolResultText is the placeholder synthesized when a tool call was
// never answered in the transcript.
const missingToolResultText = "Tool result not available."

// repairBrokenTurns makes every tool call answered and every tool result
// referenced. Unanswered calls get a synthesized placeholder result inserted
// directly after their assistant message; tool results whose CallID matches no
// call are dropped.
func repairBrokenTurns(msgs []modelinvocation.ModelMessage) []modelinvocation.ModelMessage {
	called := calledIDs(msgs)
	answered := answeredIDs(msgs)

	// Pass 1: synthesize a result for every tool call that was never answered,
	// inserted directly after the assistant message that made the call.
	repaired := make([]modelinvocation.ModelMessage, 0, len(msgs))
	for _, msg := range msgs {
		repaired = append(repaired, msg)
		if msg.Role != modelinvocation.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if _, ok := answered[tc.ID]; ok {
				continue
			}
			repaired = append(repaired, modelinvocation.ModelMessage{
				Role:    modelinvocation.RoleTool,
				CallID:  tc.ID,
				Content: missingToolResultText,
			})
		}
	}

	// Pass 2: drop tool results whose CallID matches no tool call. Synthesized
	// results always answer the call they follow, so they survive this pass.
	out := make([]modelinvocation.ModelMessage, 0, len(repaired))
	for _, msg := range repaired {
		if msg.Role == modelinvocation.RoleTool {
			if _, ok := called[msg.CallID]; !ok {
				continue
			}
		}
		out = append(out, msg)
	}
	return out
}

// calledIDs collects every tool call ID referenced by assistant messages.
func calledIDs(msgs []modelinvocation.ModelMessage) map[string]struct{} {
	called := make(map[string]struct{})
	for _, msg := range msgs {
		if msg.Role != modelinvocation.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls {
			called[tc.ID] = struct{}{}
		}
	}
	return called
}

// answeredIDs collects the CallID of every tool result message.
func answeredIDs(msgs []modelinvocation.ModelMessage) map[string]struct{} {
	answered := make(map[string]struct{})
	for _, msg := range msgs {
		if msg.Role == modelinvocation.RoleTool {
			answered[msg.CallID] = struct{}{}
		}
	}
	return answered
}

// injectCandidates appends the dynamic response's candidates to the last
// user-role message, in provider order, each as
// <candidate id="...">content</candidate> inside one <context>…</context>
// block. With no user message, or an empty candidate list, nothing is
// injected. Failures are ignored in v0.
func injectCandidates(msgs []modelinvocation.ModelMessage, dynamic *contextprovider.ContextResponse) {
	if dynamic == nil || len(dynamic.Candidates) == 0 {
		return
	}
	idx := lastUserIndex(msgs)
	if idx < 0 {
		return
	}
	block := renderCandidateBlock(dynamic.Candidates)
	if block == "" {
		return
	}
	if msgs[idx].Content != "" {
		msgs[idx].Content += "\n" + block
	} else {
		msgs[idx].Content = block
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

// renderCandidateBlock renders candidates in provider order inside a single
// <context>…</context> block.
func renderCandidateBlock(candidates []contextprovider.ContextCandidate) string {
	var sb strings.Builder
	sb.WriteString("<context>")
	for _, c := range candidates {
		sb.WriteString("\n<candidate id=\"")
		sb.WriteString(c.ID)
		sb.WriteString("\">")
		sb.WriteString(c.Content)
		sb.WriteString("</candidate>")
	}
	sb.WriteString("\n</context>")
	return sb.String()
}

// invalidRequest builds an error wrapping a non-retryable invalid_request
// failure, recoverable with errors.As.
func invalidRequest(requestID, message string) error {
	return fmt.Errorf("%w", ContextRendererFailure{
		RequestID: requestID,
		Code:      FailureInvalidRequest,
		Message:   message,
		Retryable: false,
	})
}

// templateError builds an error wrapping a non-retryable template_error
// failure, recoverable with errors.As.
func templateError(requestID, message string) error {
	return fmt.Errorf("%w", ContextRendererFailure{
		RequestID: requestID,
		Code:      FailureTemplateError,
		Message:   message,
		Retryable: false,
	})
}

// Error lets a ContextRendererFailure be wrapped and recovered with errors.As.
func (f ContextRendererFailure) Error() string {
	return fmt.Sprintf("context_renderer failure %q (request %q)", f.Code, f.RequestID)
}
