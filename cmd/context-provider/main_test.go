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
}

func TestCLIInitialize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("cli instructions"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	input := []byte(`{"id":"init","runtime":{"cwd":` + quote(root) + `},"workspace_roots":[{"path":` + quote(root) + `}]}`)
	stdout, stderr, code := runCLI(t, input, "initialize")
	if code != 0 {
		t.Fatalf("initialize exit = %d stderr = %s stdout = %s", code, stderr, stdout)
	}
	var bundle contextprovider.ContextBundle
	if err := json.Unmarshal(stdout, &bundle); err != nil {
		t.Fatalf("unmarshal initialize: %v\n%s", err, stdout)
	}
	content := ""
	for _, candidate := range bundle.Retained.Buckets[contextprovider.SlotProjectInstructions] {
		content += candidate.Content
	}
	if !strings.Contains(content, "cli instructions") {
		t.Fatalf("initialize output missing instructions: %s", content)
	}
}

func TestCLIGetContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("referenced note"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	input := []byte(`{"id":"ctx","runtime":{"cwd":` + quote(root) + `},"workspace_roots":[{"path":` + quote(root) + `}],"triggering_record":{"id":"rec","seq":1,"kind":"message","role":"user","text":"see ref","created_at":"2026-07-22T00:00:00Z","char_count":7,"tokens":{"value":2,"source":"char_estimate"},"refs":[{"kind":"file","target":"note.txt"}]}}`)
	stdout, stderr, code := runCLI(t, input, "get-context")
	if code != 0 {
		t.Fatalf("get-context exit = %d stderr = %s stdout = %s", code, stderr, stdout)
	}
	var bundle contextprovider.ContextBundle
	if err := json.Unmarshal(stdout, &bundle); err != nil {
		t.Fatalf("unmarshal get-context: %v\n%s", err, stdout)
	}
	if len(bundle.PerCall.Referenced) != 1 || !strings.Contains(bundle.PerCall.Referenced[0].Content, "referenced note") {
		t.Fatalf("referenced candidates = %+v", bundle.PerCall.Referenced)
	}
}

func TestCLIUsageErrors(t *testing.T) {
	_, stderr, code := runCLI(t, nil, "nope")
	if code == 0 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("invalid command exit = %d stderr = %s", code, stderr)
	}

	_, stderr, code = runCLI(t, []byte(`{`), "initialize")
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
