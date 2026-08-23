package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"frankenstein/internal/contextprovider"
)

func TestCLIVersion(t *testing.T) {
	stdout, stderr, code := runCLI(t, nil, "version")
	if code != 0 {
		t.Fatalf("version exit = %d stderr = %s", code, stderr)
	}
	var info contextprovider.ContractInfo
	if err := json.Unmarshal(stdout, &info); err != nil {
		t.Fatalf("unmarshal version: %v\n%s", err, stdout)
	}
	if info.Capability != contextprovider.CapabilityName || info.ContractVersion != contextprovider.ContractVersion {
		t.Fatalf("version info = %+v", info)
	}
	if info.ContractVersion != "context_provider.v0.2" {
		t.Fatalf("contract version = %q, want context_provider.v0.2", info.ContractVersion)
	}
}

func TestCLIGetStableContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("cli instructions"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	input := []byte(`{"id":"stable","runtime":{"cwd":` + quote(root) + `},"workspace_roots":[{"path":` + quote(root) + `}]}`)
	stdout, stderr, code := runCLI(t, input, "get-stable-context")
	if code != 0 {
		t.Fatalf("get-stable-context exit = %d stderr = %s stdout = %s", code, stderr, stdout)
	}
	var response contextprovider.ContextResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("unmarshal get-stable-context: %v\n%s", err, stdout)
	}
	if len(response.Candidates) == 0 {
		t.Fatalf("get-stable-context produced no candidates: %s", stdout)
	}
	slot, _ := response.Candidates[0].Metadata["slot"].(string)
	if slot != contextprovider.SlotProjectInstructions {
		t.Fatalf("candidates[0].metadata.slot = %q, want %q", slot, contextprovider.SlotProjectInstructions)
	}
	if !strings.Contains(response.Candidates[0].Content, "cli instructions") {
		t.Fatalf("get-stable-context output missing instructions: %s", response.Candidates[0].Content)
	}
}

func TestCLIGetDynamicContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("referenced note"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	input := []byte(`{"id":"ctx","runtime":{"cwd":` + quote(root) + `},"workspace_roots":[{"path":` + quote(root) + `}],"refs":[{"kind":"file","target":"note.txt"}]}`)
	stdout, stderr, code := runCLI(t, input, "get-dynamic-context")
	if code != 0 {
		t.Fatalf("get-dynamic-context exit = %d stderr = %s stdout = %s", code, stderr, stdout)
	}
	var response contextprovider.ContextResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("unmarshal get-dynamic-context: %v\n%s", err, stdout)
	}
	found := false
	for _, candidate := range response.Candidates {
		if strings.Contains(candidate.Content, "referenced note") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("referenced candidate missing from response: %s", stdout)
	}
}

func TestCLIInvalidRequest(t *testing.T) {
	root := t.TempDir()
	input := []byte(`{"id":"","runtime":{"cwd":` + quote(root) + `},"workspace_roots":[{"path":` + quote(root) + `}]}`)
	stdout, stderr, code := runCLI(t, input, "get-stable-context")
	if code == 0 {
		t.Fatalf("invalid request exit = 0 stderr = %s", stderr)
	}
	var failure contextprovider.ContextFailure
	if err := json.Unmarshal(stdout, &failure); err != nil {
		t.Fatalf("unmarshal failure: %v\n%s", err, stdout)
	}
	if failure.Code != contextprovider.FailureInvalidRequest {
		t.Fatalf("failure code = %q, want %q", failure.Code, contextprovider.FailureInvalidRequest)
	}
	if failure.Retryable {
		t.Fatalf("invalid request failure retryable = true, want false")
	}
}

func TestCLIUsageErrors(t *testing.T) {
	_, stderr, code := runCLI(t, nil, "nope")
	if code == 0 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("invalid command exit = %d stderr = %s", code, stderr)
	}

	_, stderr, code = runCLI(t, []byte(`{`), "get-stable-context")
	if code == 0 || !strings.Contains(stderr, "malformed json") {
		t.Fatalf("malformed json exit = %d stderr = %s", code, stderr)
	}
}

func runCLI(t *testing.T, stdin []byte, args ...string) ([]byte, string, int) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run cli: %v stderr=%s", err, stderr.String())
		}
	}
	return stdout.Bytes(), stderr.String(), code
}

func quote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
