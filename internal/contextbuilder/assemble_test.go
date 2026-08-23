package contextbuilder

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/toolinvocation"
)

// hex16 matches a 16-character lowercase hex string.
var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

// sampleCandidates returns stable candidates whose slot metadata exercises
// grouping: identity arrives only from the second candidate, while
// project_instructions arrives from both.
func sampleCandidates() []contextprovider.ContextCandidate {
	return []contextprovider.ContextCandidate{
		{ID: "pi-1", Metadata: slotMeta(contextprovider.SlotProjectInstructions), Content: "Follow the contract."},
		{ID: "id-1", Metadata: slotMeta(contextprovider.SlotIdentity), Content: "You are Frank."},
		{ID: "id-2", Metadata: slotMeta(contextprovider.SlotIdentity), Content: "You are calm."},
		{ID: "pi-2", Metadata: slotMeta(contextprovider.SlotProjectInstructions), Content: "Write plain prose."},
	}
}

func sampleCatalog() *toolinvocation.ToolCatalog {
	return &toolinvocation.ToolCatalog{
		ID: "catalog-1",
		Tools: []toolinvocation.ToolDefinition{
			{ID: "t1", Name: "read_file", Description: "Read a file."},
			{ID: "t2", Name: "run_shell", Description: "Run a shell command."},
		},
	}
}

func TestAssemble(t *testing.T) {
	service := &Service{}

	t.Run("empty request", func(t *testing.T) {
		got, err := service.Assemble(AssembleRequest{
			ID:    "req-empty",
			Model: "claude-sonnet-4",
		})
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		if got.RequestID != "req-empty" {
			t.Errorf("RequestID = %q, want %q", got.RequestID, "req-empty")
		}
		if !strings.Contains(got.SystemPrompt, "You are a helpful assistant.") {
			t.Errorf("SystemPrompt = %q, want it to contain the assistant preamble", got.SystemPrompt)
		}
	})

	t.Run("stable candidates render in deterministic XML blocks", func(t *testing.T) {
		got, err := service.Assemble(AssembleRequest{
			ID:               "req-context",
			Model:            "claude-sonnet-4",
			StableCandidates: sampleCandidates(),
		})
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		want := "You are a helpful assistant.\n\n" +
			"<identity>\n" +
			"<candidate id=\"id-1\">\n" +
			"You are Frank.\n" +
			"</candidate>\n" +
			"<candidate id=\"id-2\">\n" +
			"You are calm.\n" +
			"</candidate>\n" +
			"</identity>\n" +
			"\n<project_instructions>\n" +
			"<candidate id=\"pi-1\">\n" +
			"Follow the contract.\n" +
			"</candidate>\n" +
			"<candidate id=\"pi-2\">\n" +
			"Write plain prose.\n" +
			"</candidate>\n" +
			"</project_instructions>"
		if got.SystemPrompt != want {
			t.Errorf("SystemPrompt mismatch:\n got: %q\nwant: %q", got.SystemPrompt, want)
		}
	})

	t.Run("catalog tools render in catalog order", func(t *testing.T) {
		got, err := service.Assemble(AssembleRequest{
			ID:      "req-tools",
			Model:   "claude-sonnet-4",
			Catalog: sampleCatalog(),
		})
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		want := "You are a helpful assistant.\n\n" +
			"<available_tools>\n" +
			"- read_file: Read a file.\n" +
			"- run_shell: Run a shell command.\n" +
			"</available_tools>"
		if got.SystemPrompt != want {
			t.Errorf("SystemPrompt mismatch:\n got: %q\nwant: %q", got.SystemPrompt, want)
		}
	})

	t.Run("byte stability", func(t *testing.T) {
		req := AssembleRequest{
			ID:               "req-stable",
			Model:            "claude-sonnet-4",
			StableCandidates: sampleCandidates(),
			Catalog:          sampleCatalog(),
		}
		first, err := service.Assemble(req)
		if err != nil {
			t.Fatalf("first Assemble() error = %v, want nil", err)
		}
		second, err := service.Assemble(req)
		if err != nil {
			t.Fatalf("second Assemble() error = %v, want nil", err)
		}
		if first != second {
			t.Errorf("Assemble() not byte-stable:\n first: %+v\nsecond: %+v", first, second)
		}
	})

	t.Run("system_prompt_id is a 16-character hex hash of the prompt", func(t *testing.T) {
		req := AssembleRequest{
			ID:               "req-id",
			Model:            "claude-sonnet-4",
			StableCandidates: sampleCandidates(),
		}
		got, err := service.Assemble(req)
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		if !hex16.MatchString(got.SystemPromptID) {
			t.Errorf("SystemPromptID = %q, want 16 hex characters", got.SystemPromptID)
		}

		changed := req
		changed.StableCandidates = append(changed.StableCandidates, contextprovider.ContextCandidate{
			ID:       "mem-1",
			Metadata: slotMeta(contextprovider.SlotMemory),
			Content:  "Remember the plan.",
		})
		other, err := service.Assemble(changed)
		if err != nil {
			t.Fatalf("Assemble(changed) error = %v, want nil", err)
		}
		if other.SystemPromptID == got.SystemPromptID {
			t.Errorf("SystemPromptID unchanged after content change: %q", got.SystemPromptID)
		}
	})

	t.Run("missing model", func(t *testing.T) {
		_, err := service.Assemble(AssembleRequest{ID: "req-nomodel"})
		if err == nil {
			t.Fatal("Assemble() error = nil, want invalid_request")
		}
		var fail ContextBuilderFailure
		if !errors.As(err, &fail) {
			t.Fatalf("Assemble() error %v does not wrap ContextBuilderFailure", err)
		}
		if fail.Code != FailureInvalidRequest {
			t.Errorf("failure code = %q, want %q", fail.Code, FailureInvalidRequest)
		}
		if fail.Retryable {
			t.Errorf("failure %q should not be retryable", fail.Code)
		}
	})
}
