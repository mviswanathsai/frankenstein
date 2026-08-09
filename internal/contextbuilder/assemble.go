package contextbuilder

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"text/template"

	"frankenstein/internal/contextprovider"
)

const assembleTemplate = `You are a helpful assistant.

{{range .Slots}}
<{{.Name}}>
{{range .Items}}
<candidate id="{{.ID}}">
{{.Content}}
</candidate>
{{end}}
</{{.Name}}>
{{end}}
{{if .Tools}}
<available_tools>
{{range .Tools}}
- {{.Name}}: {{.Description}}
{{end}}
</available_tools>
{{end}}`

type assembleData struct {
	Model string
	Tools []toolDefStub
	Slots []slotGroup
}

type toolDefStub struct {
	Name        string
	Description string
}

type slotGroup struct {
	Name  string
	Items []contextprovider.ContextCandidate
}

func (s *Service) Assemble(req AssembleRequest) (BuiltPrefix, error) {
	if req.Model == "" {
		return BuiltPrefix{}, fmt.Errorf("%s: model is required", FailureInvalidRequest)
	}

	bySlot := make(map[string][]contextprovider.ContextCandidate)
	for _, bundle := range req.ContextBundles {
		for slot, candidates := range bundle.Retained.Buckets {
			name := string(slot)
			bySlot[name] = append(bySlot[name], candidates...)
		}
	}

	slots := make([]slotGroup, 0, len(bySlot))
	for name, items := range bySlot {
		slots = append(slots, slotGroup{Name: name, Items: items})
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].Name < slots[j].Name
	})

	data := assembleData{
		Model: req.Model,
		Slots: slots,
	}
	if req.Catalog != nil {
		data.Tools = make([]toolDefStub, 0, len(req.Catalog.Tools))
		for _, tool := range req.Catalog.Tools {
			data.Tools = append(data.Tools, toolDefStub{
				Name:        tool.Name,
				Description: tool.Description,
			})
		}
	}

	tmpl, err := template.New("system-prompt").Parse(assembleTemplate)
	if err != nil {
		return BuiltPrefix{}, fmt.Errorf("%s: %w", FailureTemplateError, err)
	}

	var rendered []byte
	buf := &byteBuffer{buf: &rendered}
	if err := tmpl.Execute(buf, data); err != nil {
		return BuiltPrefix{}, fmt.Errorf("%s: %w", FailureTemplateError, err)
	}

	systemPrompt := string(rendered)
	hash := sha256.Sum256([]byte(systemPrompt))
	return BuiltPrefix{
		RequestID:      req.ID,
		SystemPrompt:   systemPrompt,
		SystemPromptID: fmt.Sprintf("%x", hash)[:16],
	}, nil
}

// byteBuffer keeps template execution allocation-free beyond the rendered output.
type byteBuffer struct {
	buf *[]byte
}

func (b *byteBuffer) Write(p []byte) (int, error) {
	*b.buf = append(*b.buf, p...)
	return len(p), nil
}
