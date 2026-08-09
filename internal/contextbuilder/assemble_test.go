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

// sampleBundles returns two bundles whose retained context exercises slot
// merging across bundles: identity arrives only from the second bundle, while
// project_instructions arrives from both.
func sampleBundles() []contextprovider.ContextBundle {
	return []contextprovider.ContextBundle{
		{
			RequestID:  "bundle-1",
			ProviderID: "provider-1",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotProjectInstructions: {
						{ID: "pi-1", Content: "Follow the contract."},
					},
				},
			},
		},
		{
			RequestID:  "bundle-2",
			ProviderID: "provider-2",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotIdentity: {
						{ID: "id-1", Content: "You are Frank."},
						{ID: "id-2", Content: "You are calm."},
					},
					contextprovider.SlotProjectInstructions: {
						{ID: "pi-2", Content: "Write plain prose."},
					},
				},
			},
		},
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

	t.Run("retained context renders in deterministic XML blocks", func(t *testing.T) {
		got, err := service.Assemble(AssembleRequest{
			ID:             "req-context",
			Model:          "claude-sonnet-4",
			ContextBundles: sampleBundles(),
		})
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		want := "You are a helpful assistant.\n\n" +
			"\n<identity>\n" +
			"\n<candidate id=\"id-1\">\n" +
			"You are Frank.\n" +
			"</candidate>\n" +
			"\n<candidate id=\"id-2\">\n" +
			"You are calm.\n" +
			"</candidate>\n" +
			"\n</identity>\n" +
			"\n<project_instructions>\n" +
			"\n<candidate id=\"pi-1\">\n" +
			"Follow the contract.\n" +
			"</candidate>\n" +
			"\n<candidate id=\"pi-2\">\n" +
			"Write plain prose.\n" +
			"</candidate>\n" +
			"\n</project_instructions>\n" +
			"\n"
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
		want := "You are a helpful assistant.\n\n\n\n" +
			"<available_tools>\n" +
			"\n- read_file: Read a file.\n" +
			"\n- run_shell: Run a shell command.\n" +
			"\n</available_tools>\n"
		if got.SystemPrompt != want {
			t.Errorf("SystemPrompt mismatch:\n got: %q\nwant: %q", got.SystemPrompt, want)
		}
	})

	t.Run("byte stability", func(t *testing.T) {
		req := AssembleRequest{
			ID:             "req-stable",
			Model:          "claude-sonnet-4",
			ContextBundles: sampleBundles(),
			Catalog:        sampleCatalog(),
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
			ID:             "req-id",
			Model:          "claude-sonnet-4",
			ContextBundles: sampleBundles(),
		}
		got, err := service.Assemble(req)
		if err != nil {
			t.Fatalf("Assemble() error = %v, want nil", err)
		}
		if !hex16.MatchString(got.SystemPromptID) {
			t.Errorf("SystemPromptID = %q, want 16 hex characters", got.SystemPromptID)
		}

		changed := req
		changed.ContextBundles = append(changed.ContextBundles, contextprovider.ContextBundle{
			RequestID:  "bundle-3",
			ProviderID: "provider-3",
			Retained: contextprovider.ContextCollection{
				Buckets: contextprovider.ContextBuckets{
					contextprovider.SlotMemory: {
						{ID: "mem-1", Content: "Remember the plan."},
					},
				},
			},
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
