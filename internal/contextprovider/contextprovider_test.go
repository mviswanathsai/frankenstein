package contextprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"frankenstein/internal/session"
)

func TestBaseFilenameClassification(t *testing.T) {
	tests := []struct {
		name string
		slot ContextSlot
	}{
		{"USER.md", SlotUserProfile},
		{"MEMORY.md", SlotMemory},
		{"AGENTS.md", SlotProjectInstructions},
		{"AGENTS.override.md", SlotProjectInstructions},
		{"CLAUDE.md", SlotProjectInstructions},
		{"CLAUDE.local.md", SlotProjectInstructions},
		{"CURSOR.md", SlotProjectInstructions},
		{".cursorrules", SlotProjectInstructions},
		{"HERMES.md", SlotProjectInstructions},
		{".hermes.md", SlotProjectInstructions},
		{"SOUL.md", SlotIdentity},
		{"README.md", SlotUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyBaseFilename(filepath.Join("/tmp/project", tt.name))
			if got.Slot != tt.slot {
				t.Fatalf("slot = %q, want %q", got.Slot, tt.slot)
			}
			if tt.slot != SlotUnknown && !got.Recognized {
				t.Fatalf("%s should be recognized", tt.name)
			}
		})
	}
}

func TestWorkspaceRootAndCWDValidation(t *testing.T) {
	root := t.TempDir()
	provider := NewProvider(Options{})

	_, failure := provider.Initialize(context.Background(), ContextInitializeRequest{
		ID:             "req",
		Runtime:        RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: "relative"}},
	})
	if failure == nil || failure.Code != FailureInvalidRelativeWorkspaceRoot {
		t.Fatalf("relative root failure = %+v, want %s", failure, FailureInvalidRelativeWorkspaceRoot)
	}

	_, failure = provider.Initialize(context.Background(), ContextInitializeRequest{
		ID:             "req",
		Runtime:        RuntimeFacts{CWD: "relative"},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	if failure == nil || failure.Code != FailureInvalidRelativeCWD {
		t.Fatalf("relative cwd failure = %+v, want %s", failure, FailureInvalidRelativeCWD)
	}
}

func TestPathAuthorizationContainment(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	sibling := filepath.Join(parent, "project2")
	mustMkdir(t, root)
	mustMkdir(t, filepath.Join(root, "nested"))
	mustMkdir(t, sibling)

	auth, err := newAuthorizer([]WorkspaceRoot{{Path: root}, {Path: filepath.Join(root, ".")}, {Path: filepath.Join(root, "nested")}})
	if err != nil {
		t.Fatalf("newAuthorizer() error = %v", err)
	}
	if !auth.isAuthorized(root) {
		t.Fatalf("root itself should be authorized")
	}
	if !auth.isAuthorized(filepath.Join(root, "a", "b", "c.txt")) {
		t.Fatalf("deep child should be authorized")
	}
	if auth.isAuthorized(filepath.Join(sibling, "x.txt")) {
		t.Fatalf("similar textual prefix should not be authorized")
	}
	if len(auth.roots) != 1 {
		t.Fatalf("deduped roots = %d, want 1", len(auth.roots))
	}
}

func TestInitializeDiscoversInstructionsAndCompactSkillsIndex(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "service")
	mustMkdir(t, cwd)
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root instructions")
	mustMkdir(t, filepath.Join(cwd, ".cursor", "rules"))
	mustWrite(t, filepath.Join(cwd, ".cursor", "rules", "backend.mdc"), "---\ndescription: backend\n---\nbackend cursor rule")
	mustMkdir(t, filepath.Join(root, ".agents", "skills", "tester"))
	mustWrite(t, filepath.Join(root, ".agents", "skills", "tester", "SKILL.md"), "---\nname: tester\ndescription: Run tests.\n---\nFULL BODY SHOULD NOT BE IN INDEX")

	bundle := mustInitialize(t, NewProvider(Options{}), ContextInitializeRequest{
		ID:             "init",
		Runtime:        RuntimeFacts{CWD: cwd},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})

	project := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if !strings.Contains(project, "root instructions") {
		t.Fatalf("project instructions missing root AGENTS.md: %s", project)
	}
	if !strings.Contains(project, "backend cursor rule") {
		t.Fatalf("project instructions missing cursor rule: %s", project)
	}
	skills := bucketContents(bundle.Retained.Buckets[SlotSkills])
	if !strings.Contains(skills, "tester: Run tests.") {
		t.Fatalf("skills index missing compact metadata: %s", skills)
	}
	if strings.Contains(skills, "FULL BODY SHOULD NOT BE IN INDEX") {
		t.Fatalf("skills index loaded full skill body: %s", skills)
	}
	if len(bundle.PerCall.Buckets) != 0 || len(bundle.PerCall.Referenced) != 0 {
		t.Fatalf("initialize per_call = %+v, want empty", bundle.PerCall)
	}
}

func TestCodexOverridePrecedence(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "base")
	mustWrite(t, filepath.Join(root, "AGENTS.override.md"), "override")

	bundle := mustInitialize(t, NewProvider(Options{}), ContextInitializeRequest{
		ID:             "init",
		Runtime:        RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	content := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if !strings.Contains(content, "override") {
		t.Fatalf("override not included: %s", content)
	}
	if strings.Contains(content, "base") {
		t.Fatalf("AGENTS.md was included despite override: %s", content)
	}
}

func TestExplicitRefsAreReferencedOrFailures(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "explicit instructions")
	mustWrite(t, filepath.Join(root, "note.txt"), "plain note")
	provider := NewProvider(Options{})

	bundle := mustGetContext(t, provider, ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "AGENTS.md"},
			{Kind: "file", Target: "missing.md"},
			{Kind: "url", Target: "https://example.com"},
		}},
	})

	referenced := collectionContents(bundle.PerCall.Referenced)
	if !strings.Contains(referenced, "explicit instructions") {
		t.Fatalf("explicit file ref was not returned in per_call.referenced: %s", referenced)
	}
	project := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if !strings.Contains(project, "explicit instructions") {
		t.Fatalf("recognized explicit AGENTS.md was not semantically classified: %s", project)
	}
	failures := strings.Join(bundle.Failures, "\n")
	if !strings.Contains(failures, FailureSourceMissing) || !strings.Contains(failures, FailureUnsupportedRefKind) {
		t.Fatalf("failures = %v, want missing and unsupported ref entries", bundle.Failures)
	}
}

func TestRelativeRefWithoutCWDProducesFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "content")
	bundle := mustGetContext(t, NewProvider(Options{}), ContextRequest{
		ID:             "ctx",
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "file.txt"},
		}},
	})
	if got := strings.Join(bundle.Failures, "\n"); !strings.Contains(got, FailureMissingCWDForRelativePath) {
		t.Fatalf("failures = %v, want missing cwd", bundle.Failures)
	}
}

func TestTouchedNonexistentPathDiscoversParentInstructions(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mustMkdir(t, backend)
	mustWrite(t, filepath.Join(backend, "AGENTS.md"), "backend instructions")

	bundle := mustGetContext(t, NewProvider(Options{}), ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TouchedPaths:   []TouchedPath{{Path: "backend/new_file.go", Operation: "write"}},
	})
	content := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if !strings.Contains(content, "backend instructions") {
		t.Fatalf("parent instructions not discovered: %s", content)
	}
}

func TestSymlinkAuthorization(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "inside.txt"), "inside")
	mustWrite(t, filepath.Join(outside, "outside.txt"), "outside")
	if err := os.Symlink(filepath.Join(root, "inside.txt"), filepath.Join(root, "inside-link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(root, "outside-link.txt")); err != nil {
		t.Fatalf("symlink outside: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.txt"), filepath.Join(root, "broken-link.txt")); err != nil {
		t.Fatalf("broken symlink: %v", err)
	}

	provider := NewProvider(Options{})
	bundle := mustGetContext(t, provider, ContextRequest{
		ID:             "ok",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "inside-link.txt"},
		}},
	})
	if !strings.Contains(collectionContents(bundle.PerCall.Referenced), "inside") {
		t.Fatalf("in-root symlink was not read: %+v", bundle.PerCall.Referenced)
	}

	bundle = mustGetContext(t, provider, ContextRequest{
		ID:             "bad",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "outside-link.txt"},
			{Kind: "file", Target: "broken-link.txt"},
		}},
	})
	failures := strings.Join(bundle.Failures, "\n")
	if !strings.Contains(failures, FailureSymlinkEscape) {
		t.Fatalf("failures = %v, want symlink escape", bundle.Failures)
	}
	if !strings.Contains(failures, FailureSourceMissing) {
		t.Fatalf("failures = %v, want broken symlink missing", bundle.Failures)
	}
}

func TestNoExpansionOrBasenameSearch(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "$CTX_TARGET"), "literal env name")
	mustWrite(t, filepath.Join(root, "actual.txt"), "actual")

	bundle := mustGetContext(t, NewProvider(Options{}), ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "$CTX_TARGET"},
			{Kind: "file", Target: "*.txt"},
			{Kind: "file", Target: "actual"},
		}},
	})
	if !strings.Contains(collectionContents(bundle.PerCall.Referenced), "literal env name") {
		t.Fatalf("literal $ path was not read")
	}
	failures := strings.Join(bundle.Failures, "\n")
	if !strings.Contains(failures, "*.txt") || !strings.Contains(failures, "actual") {
		t.Fatalf("failures = %v, want glob and basename unresolved", bundle.Failures)
	}
}

func TestCandidateContentLimitAndUTF8Truncation(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("å", 400)
	mustWrite(t, filepath.Join(root, "AGENTS.md"), body)
	provider := NewProvider(Options{MaxCandidateContentBytes: 512})
	bundle := mustInitialize(t, provider, ContextInitializeRequest{
		ID:             "init",
		Runtime:        RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	content := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if len([]byte(content)) > 512 {
		t.Fatalf("content bytes = %d, want <= 512", len([]byte(content)))
	}
	if !strings.Contains(content, "TRUNCATED") {
		t.Fatalf("content missing truncation marker: %q", content)
	}
	if strings.ToValidUTF8(content, "") != content {
		t.Fatalf("content is not valid UTF-8")
	}
}

func TestSourceTooLargeExplicitRefFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "large.txt"), strings.Repeat("x", 100))
	bundle := mustGetContext(t, NewProvider(Options{MaxSourceReadBytes: 64, MaxCandidateContentBytes: 256}), ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TriggeringRecord: &session.SessionRecord{Refs: []session.ContextRef{
			{Kind: "file", Target: "large.txt"},
		}},
	})
	if got := strings.Join(bundle.Failures, "\n"); !strings.Contains(got, FailureSourceTooLarge) {
		t.Fatalf("failures = %v, want source too large", bundle.Failures)
	}
}

func TestFreshnessCacheAndCompleteCurrentOffering(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWrite(t, path, "first")
	provider := NewProvider(Options{})
	_ = mustInitialize(t, provider, ContextInitializeRequest{
		ID:             "init",
		Runtime:        RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})

	mustWrite(t, path, "second")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	bundle := mustGetContext(t, provider, ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	content := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions])
	if strings.Contains(content, "first") || !strings.Contains(content, "second") {
		t.Fatalf("stale content returned: %s", content)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	bundle = mustGetContext(t, provider, ContextRequest{
		ID:             "ctx2",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	if got := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions]); got != "" {
		t.Fatalf("deleted source still offered: %s", got)
	}
}

func TestRootsChangedRevokesCachedAccess(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "private")
	provider := NewProvider(Options{})
	_ = mustInitialize(t, provider, ContextInitializeRequest{
		ID:             "init",
		Runtime:        RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	bundle := mustGetContext(t, provider, ContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{},
	})
	if got := bucketContents(bundle.Retained.Buckets[SlotProjectInstructions]); got != "" {
		t.Fatalf("candidate survived root removal: %s", got)
	}
}

func TestConcurrentInitializeAndGetContext(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "instructions")
	provider := NewProvider(Options{})
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, failure := provider.Initialize(context.Background(), ContextInitializeRequest{
					ID:             "init",
					Runtime:        RuntimeFacts{CWD: root},
					WorkspaceRoots: []WorkspaceRoot{{Path: root}},
				})
				if failure != nil {
					errs <- failure.Message
				}
				return
			}
			_, failure := provider.GetContext(context.Background(), ContextRequest{
				ID:             "ctx",
				Runtime:        &RuntimeFacts{CWD: root},
				WorkspaceRoots: []WorkspaceRoot{{Path: root}},
			})
			if failure != nil {
				errs <- failure.Message
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent request failed: %s", err)
	}
}

func mustInitialize(t *testing.T, provider *Provider, req ContextInitializeRequest) *ContextBundle {
	t.Helper()
	bundle, failure := provider.Initialize(context.Background(), req)
	if failure != nil {
		t.Fatalf("Initialize() failure = %+v", failure)
	}
	return bundle
}

func mustGetContext(t *testing.T, provider *Provider, req ContextRequest) *ContextBundle {
	t.Helper()
	bundle, failure := provider.GetContext(context.Background(), req)
	if failure != nil {
		t.Fatalf("GetContext() failure = %+v", failure)
	}
	return bundle
}

func bucketContents(candidates []ContextCandidate) string {
	return collectionContents(candidates)
}

func collectionContents(candidates []ContextCandidate) string {
	var values []string
	for _, candidate := range candidates {
		values = append(values, candidate.Content)
	}
	return strings.Join(values, "\n")
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
