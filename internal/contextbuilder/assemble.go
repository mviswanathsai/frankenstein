package contextbuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/toolinvocation"
)

// assembleTemplateText is the default system prompt template. Slots are
// rendered in the order they appear in Slots; tools render in catalog order.
const assembleTemplateText = `You are a helpful assistant.{{- range .Slots}}

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

// assembleTemplate is the parsed form of assembleTemplateText. It is parsed
// once at init because the source is a constant; execution is deterministic.
var assembleTemplate = template.Must(template.New("system_prompt").Parse(assembleTemplateText))

// assembleData is the template-friendly view of an AssembleRequest. Map
// buckets are pre-processed into ordered slices so template iteration is
// deterministic.
type assembleData struct {
	Model string
	Tools []toolDefStub
	Slots []slotGroup
}

// toolDefStub carries the tool-awareness text the template renders. The full
// input schema travels separately with the catalog.
type toolDefStub struct {
	Name        string
	Description string
}

// slotGroup is one retained context slot and its candidates, ready for the
// template. Name is the raw ContextSlot string used as the XML tag.
type slotGroup struct {
	Name  string
	Items []contextprovider.ContextCandidate
}

// Assemble builds the byte-stable system prompt from context bundles and the
// tool catalog. Identical inputs produce an identical BuiltPrefix.
func (s *Service) Assemble(req AssembleRequest) (BuiltPrefix, error) {
	if req.Model == "" {
		return BuiltPrefix{}, invalidRequest(req.ID, "model is required")
	}

	data := assembleData{
		Model: req.Model,
		Slots: collectSlots(req.ContextBundles),
	}
	if req.Catalog != nil {
		data.Tools = toolStubs(req.Catalog)
	}

	var rendered strings.Builder
	if err := assembleTemplate.Execute(&rendered, data); err != nil {
		return BuiltPrefix{}, templateError(req.ID, fmt.Sprintf("render system prompt template: %v", err))
	}

	prompt := rendered.String()
	sum := sha256.Sum256([]byte(prompt))
	return BuiltPrefix{
		RequestID:      req.ID,
		SystemPrompt:   prompt,
		SystemPromptID: hex.EncodeToString(sum[:])[:16],
	}, nil
}

// collectSlots merges retained candidates from all bundles, grouped by slot.
// Candidates keep their order within each bundle; bundles are processed in
// request order. Slots are returned sorted alphabetically by name so the
// rendered prompt is deterministic despite map iteration order. Empty buckets
// contribute nothing.
func collectSlots(bundles []contextprovider.ContextBundle) []slotGroup {
	groups := make(map[string]*slotGroup)
	for _, bundle := range bundles {
		for slot, candidates := range bundle.Retained.Buckets {
			if len(candidates) == 0 {
				continue
			}
			group := groups[string(slot)]
			if group == nil {
				group = &slotGroup{Name: string(slot)}
				groups[string(slot)] = group
			}
			group.Items = append(group.Items, candidates...)
		}
	}

	slots := make([]slotGroup, 0, len(groups))
	for _, group := range groups {
		slots = append(slots, *group)
	}
	slices.SortFunc(slots, func(a, b slotGroup) int {
		return strings.Compare(a.Name, b.Name)
	})
	return slots
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

// templateError builds an error wrapping a non-retryable template_error
// failure, recoverable with errors.As.
func templateError(requestID, message string) error {
	return fmt.Errorf("%w", ContextBuilderFailure{
		RequestID: requestID,
		Code:      FailureTemplateError,
		Message:   message,
		Retryable: false,
	})
}
