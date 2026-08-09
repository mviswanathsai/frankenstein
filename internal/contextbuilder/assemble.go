package contextbuilder

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"text/template"

	"frankenstein/internal/contextprovider"
)

// assembleData is the template-friendly data structure prepared from
// AssembleRequest before template execution.
type assembleData struct {
	Model string
	Tools []toolDefStub
	Slots []slotGroup
}

// toolDefStub carries the name and description of a tool for the template.
type toolDefStub struct {
	Name        string
	Description string
}

// slotGroup is a named group of context candidates, used by the template to
// render XML-delimited blocks per context slot.
type slotGroup struct {
	Name  string
	Items []contextprovider.ContextCandidate
}

// assembleTemplate is the reference default template for context_builder.v0.
// The template produces byte-stable output: slots are sorted alphabetically
// before template execution, candidates within a slot preserve bundle order,
// and tools preserve catalog order.
//
// Whitespace trimming ({{- and -}}) keeps the output clean:
// blank lines only appear when the corresponding blocks (slots or tools)
// contain content.
const assembleTemplate = `You are a helpful assistant.{{- range .Slots}}

<{{.Name}}>
{{- range .Items}}
<candidate id="{{.ID}}">
{{.Content}}
</candidate>
{{- end}}
</{{.Name}}>
{{- end}}{{if .Tools}}

<available_tools>
{{- range .Tools}}
- {{.Name}}: {{.Description}}
{{- end}}
</available_tools>
{{- end}}`

// Assemble builds a session-scoped system prompt from retained context and a
// tool catalog. It is byte-stable: identical inputs produce identical output.
func (s *Service) Assemble(req AssembleRequest) (BuiltPrefix, error) {
	if req.Model == "" {
		return BuiltPrefix{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureInvalidRequest,
			Message:   "model is required",
			Retryable: false,
		}
	}

	// Merge retained candidates across all bundles, grouped by slot.
	// Bundle order is preserved: candidates from earlier bundles appear
	// before candidates from later bundles within the same slot.
	slotMap := make(map[string][]contextprovider.ContextCandidate)
	for _, bundle := range req.ContextBundles {
		for slot, candidates := range bundle.Retained.Buckets {
			slotMap[string(slot)] = append(slotMap[string(slot)], candidates...)
		}
	}

	// Sort slots alphabetically for deterministic template output.
	slots := make([]slotGroup, 0, len(slotMap))
	for name, items := range slotMap {
		slots = append(slots, slotGroup{Name: name, Items: items})
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].Name < slots[j].Name
	})

	// Extract tool stubs in catalog order.
	var tools []toolDefStub
	if req.Catalog != nil {
		tools = make([]toolDefStub, 0, len(req.Catalog.Tools))
		for _, td := range req.Catalog.Tools {
			tools = append(tools, toolDefStub{
				Name:        td.Name,
				Description: td.Description,
			})
		}
	}

	data := assembleData{
		Model: req.Model,
		Tools: tools,
		Slots: slots,
	}

	// Render template.
	tmpl, err := template.New("assemble").Parse(assembleTemplate)
	if err != nil {
		return BuiltPrefix{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureTemplateError,
			Message:   fmt.Sprintf("template parse error: %v", err),
			Retryable: false,
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return BuiltPrefix{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureTemplateError,
			Message:   fmt.Sprintf("template execute error: %v", err),
			Retryable: false,
		}
	}

	systemPrompt := buf.String()

	// Compute system_prompt_id as first 16 hex chars of SHA-256.
	hash := sha256.Sum256([]byte(systemPrompt))
	promptID := fmt.Sprintf("%x", hash)[:16]

	return BuiltPrefix{
		RequestID:      req.ID,
		SystemPrompt:   systemPrompt,
		SystemPromptID: promptID,
	}, nil
}
