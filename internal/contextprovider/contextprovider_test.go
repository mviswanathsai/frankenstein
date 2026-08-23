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
	"frankenstein/internal/touchedpath"
)

func TestBaseFilenameClassification(t *testing.T) {
	tests := []struct {
		name string
		slot string
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

	_, failure := provider.GetStableContext(context.Background(), StableContextRequest{
		ID:             "req",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: "relative"}},
	})
	if failure == nil || failure.Code != FailureInvalidRelativeWorkspaceRoot {
		t.Fatalf("relative root failure = %+v, want %s", failure, FailureInvalidRelativeWorkspaceRoot)
	}
	if failure.Retryable {
		t.Fatalf("relative root failure should be non-retryable")
	}

	_, failure = provider.GetStableContext(context.Background(), StableContextRequest{
		ID:             "req",
		Runtime:        &RuntimeFacts{CWD: "relative"},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	if failure == nil || failure.Code != FailureInvalidRelativeCWD {
		t.Fatalf("relative cwd failure = %+v, want %s", failure, FailureInvalidRelativeCWD)
	}
}

func TestMissingRequestIDIsInvalidRequest(t *testing.T) {
	provider := NewProvider(Options{})
	_, failure := provider.GetStableContext(context.Background(), StableContextRequest{
		WorkspaceRoots: []WorkspaceRoot{},
	})
	if failure == nil || failure.Code != FailureInvalidRequest || failure.Retryable {
		t.Fatalf("failure = %+v, want invalid_request non-retryable", failure)
	}
	_, failure = provider.GetDynamicContext(context.Background(), DynamicContextRequest{})
	if failure == nil || failure.Code != FailureInvalidRequest {
		t.Fatalf("dynamic failure = %+v, want invalid_request", failure)
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

func TestStableContextDiscoversInstructionsAndCompactSkillsIndex(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "service")
	mustMkdir(t, cwd)
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root instructions")
	mustMkdir(t, filepath.Join(cwd, ".cursor", "rules"))
	mustWrite(t, filepath.Join(cwd, ".cursor", "rules", "backend.mdc"), "---\ndescription: backend\n---\nbackend cursor rule")
	mustMkdir(t, filepath.Join(root, ".agents", "skills", "tester"))
	mustWrite(t, filepath.Join(root, ".agents", "skills", "tester", "SKILL.md"), "---\nname: tester\ndescription: Run tests.\n---\nFULL BODY SHOULD NOT BE IN INDEX")

	response := mustStable(t, NewProvider(Options{}), StableContextRequest{
		ID:             "stable",
		Runtime:        &RuntimeFacts{CWD: cwd},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})

	project := slotContents(response, SlotProjectInstructions)
	if !strings.Contains(project, "root instructions") {
		t.Fatalf("project instructions missing root AGENTS.md: %s", project)
	}
	if !strings.Contains(project, "backend cursor rule") {
		t.Fatalf("project instructions missing cursor rule: %s", project)
	}
	skills := slotContents(response, SlotSkills)
	if !strings.Contains(skills, "tester: Run tests.") {
		t.Fatalf("skills index missing compact metadata: %s", skills)
	}
	if strings.Contains(skills, "FULL BODY SHOULD NOT BE IN INDEX") {
		t.Fatalf("skills index loaded full skill body: %s", skills)
	}
	for _, candidate := range response.Candidates {
		if candidate.Metadata[MetadataKeySlot] == nil {
			t.Fatalf("candidate %s missing slot metadata", candidate.ID)
		}
		if candidate.Content == "" {
			t.Fatalf("candidate %s has empty content", candidate.ID)
		}
	}
}

func TestCodexOverridePrecedence(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "base")
	mustWrite(t, filepath.Join(root, "AGENTS.override.md"), "override")

	response := mustStable(t, NewProvider(Options{}), StableContextRequest{
		ID:             "stable",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	content := slotContents(response, SlotProjectInstructions)
	if !strings.Contains(content, "override") {
		t.Fatalf("override not included: %s", content)
	}
	if strings.Contains(content, "base") {
		t.Fatalf("AGENTS.md was included despite override: %s", content)
	}
}

func TestExplicitRefsProduceCandidatesOrFailures(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "explicit instructions")
	mustWrite(t, filepath.Join(root, "note.txt"), "plain note")
	provider := NewProvider(Options{})

	response := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "AGENTS.md"},
			{Kind: "file", Target: "missing.md"},
			{Kind: "url", Target: "https://example.com"},
		},
	})

	var matched *ContextCandidate
	for i := range response.Candidates {
		if strings.Contains(response.Candidates[i].Content, "explicit instructions") {
			matched = &response.Candidates[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("explicit file ref was not returned: %+v", response.Candidates)
	}
	if matched.Metadata[MetadataKeySlot] != SlotProjectInstructions {
		t.Fatalf("recognized explicit AGENTS.md slot = %v, want %s", matched.Metadata[MetadataKeySlot], SlotProjectInstructions)
	}
	if len(matched.Refs) != 1 || matched.Refs[0].Target != "AGENTS.md" {
		t.Fatalf("candidate refs = %+v, want the original input ref", matched.Refs)
	}

	failures := strings.Join(response.Failures, "\n")
	if !strings.Contains(failures, FailureSourceMissing) || !strings.Contains(failures, FailureUnsupportedRefKind) {
		t.Fatalf("failures = %v, want missing and unsupported ref entries", response.Failures)
	}
	if strings.Contains(failures, "AGENTS.md") {
		t.Fatalf("dereferenced ref was also reported failed: %v", response.Failures)
	}
}

func TestFailureEntriesKeepInputOrder(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "first-missing.txt"), "")
	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "zzz-missing.md"},
			{Kind: "file", Target: "aaa-missing.md"},
		},
	})
	if len(response.Failures) != 2 {
		t.Fatalf("failures = %v, want two entries", response.Failures)
	}
	if !strings.Contains(response.Failures[0], "zzz-missing.md") || !strings.Contains(response.Failures[1], "aaa-missing.md") {
		t.Fatalf("failures out of input order: %v", response.Failures)
	}
}

func TestRelativeRefWithoutCWDProducesFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "file.txt"), "content")
	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "file.txt"},
		},
	})
	if got := strings.Join(response.Failures, "\n"); !strings.Contains(got, FailureMissingCWDForRelativePath) {
		t.Fatalf("failures = %v, want missing cwd", response.Failures)
	}
}

func TestTouchedNonexistentPathDiscoversParentInstructions(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mustMkdir(t, backend)
	mustWrite(t, filepath.Join(backend, "AGENTS.md"), "backend instructions")

	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TouchedPaths:   []touchedpath.TouchedPath{{Path: "backend/new_file.go", Operation: "write"}},
	})
	content := slotContents(response, SlotProjectInstructions)
	if !strings.Contains(content, "backend instructions") {
		t.Fatalf("parent instructions not discovered: %s", content)
	}
}

func TestUnknownReasonIsAccepted(t *testing.T) {
	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		Reason:         "totally_novel_reason",
		WorkspaceRoots: []WorkspaceRoot{},
	})
	if response.RequestID != "ctx" || len(response.Candidates) != 0 || len(response.Failures) != 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestEmptyRootsWithoutCWDDoNotGrantAccess(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "instructions")
	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		WorkspaceRoots: []WorkspaceRoot{},
		TouchedPaths:   []touchedpath.TouchedPath{{Path: root, Operation: "read"}},
	})
	if len(response.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want none without granted roots", response.Candidates)
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
	response := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ok",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "inside-link.txt"},
		},
	})
	if !strings.Contains(contents(response.Candidates), "inside") {
		t.Fatalf("in-root symlink was not read: %+v", response.Candidates)
	}

	response = mustDynamic(t, provider, DynamicContextRequest{
		ID:             "bad",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "outside-link.txt"},
			{Kind: "file", Target: "broken-link.txt"},
		},
	})
	failures := strings.Join(response.Failures, "\n")
	if !strings.Contains(failures, FailureSymlinkEscape) {
		t.Fatalf("failures = %v, want symlink escape", response.Failures)
	}
	if !strings.Contains(failures, FailureSourceMissing) {
		t.Fatalf("failures = %v, want broken symlink missing", response.Failures)
	}
}

func TestNoExpansionOrBasenameSearch(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "$CTX_TARGET"), "literal env name")
	mustWrite(t, filepath.Join(root, "actual.txt"), "actual")

	response := mustDynamic(t, NewProvider(Options{}), DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "$CTX_TARGET"},
			{Kind: "file", Target: "*.txt"},
			{Kind: "file", Target: "actual"},
		},
	})
	if !strings.Contains(contents(response.Candidates), "literal env name") {
		t.Fatalf("literal $ path was not read")
	}
	failures := strings.Join(response.Failures, "\n")
	if !strings.Contains(failures, "*.txt") || !strings.Contains(failures, "actual") {
		t.Fatalf("failures = %v, want glob and basename unresolved", response.Failures)
	}
}

func TestCandidateContentLimitAndUTF8Truncation(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("å", 400)
	mustWrite(t, filepath.Join(root, "AGENTS.md"), body)
	provider := NewProvider(Options{MaxCandidateContentBytes: 512})
	response := mustStable(t, provider, StableContextRequest{
		ID:             "stable",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	content := slotContents(response, SlotProjectInstructions)
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
	response := mustDynamic(t, NewProvider(Options{MaxSourceReadBytes: 64, MaxCandidateContentBytes: 256}), DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs: []session.ContextRef{
			{Kind: "file", Target: "large.txt"},
		},
	})
	if got := strings.Join(response.Failures, "\n"); !strings.Contains(got, FailureSourceTooLarge) {
		t.Fatalf("failures = %v, want source too large", response.Failures)
	}
}

// TestStableFreshnessOnReread proves the file cache never pins stale content:
// get_stable_context called again after an edit must reflect the new bytes
// (the contract forbids returning known stale content as usable context).
func TestStableFreshnessOnReread(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWrite(t, path, "first")
	provider := NewProvider(Options{})
	req := StableContextRequest{
		ID:             "stable",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	}

	first := mustStable(t, provider, req)
	if !strings.Contains(slotContents(first, SlotProjectInstructions), "first") {
		t.Fatalf("initial stable response missing content: %+v", first.Candidates)
	}

	mustWrite(t, path, "second")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	second := mustStable(t, provider, req)
	got := slotContents(second, SlotProjectInstructions)
	if strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("stale content returned: %s", got)
	}
}

// TestDynamicReofferFreshness is the load-bearing cache-freshness test: the
// dynamic index re-offers every previously found source each call, so a stale
// read would silently persist wrong content across turns.
func TestDynamicReofferFreshness(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mustMkdir(t, backend)
	path := filepath.Join(backend, "AGENTS.md")
	mustWrite(t, path, "first")
	provider := NewProvider(Options{})

	firstReq := DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TouchedPaths:   []touchedpath.TouchedPath{{Path: filepath.Join(backend, "main.go"), Operation: "write"}},
	}
	first := mustDynamic(t, provider, firstReq)
	if !strings.Contains(slotContents(first, SlotProjectInstructions), "first") {
		t.Fatalf("discovered instructions missing: %+v", first.Candidates)
	}

	// Re-offering continues without new evidence.
	reoffered := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ctx2",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	if !strings.Contains(slotContents(reoffered, SlotProjectInstructions), "first") {
		t.Fatalf("dynamic offering did not persist: %+v", reoffered.Candidates)
	}

	mustWrite(t, path, "second")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	updated := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ctx3",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	got := slotContents(updated, SlotProjectInstructions)
	if strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("re-offer served stale content: %s", got)
	}
}

// TestStableSetOmittedFromDynamic enforces the stable/dynamic partition:
// material frozen by get_stable_context is not re-offered by
// get_dynamic_context, but an explicit input ref always wins because every
// ref must be accounted for.
func TestStableSetOmittedFromDynamic(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mustMkdir(t, backend)
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root instructions")
	mustWrite(t, filepath.Join(backend, "note.go"), "package backend")
	provider := NewProvider(Options{})

	stable := mustStable(t, provider, StableContextRequest{
		ID:             "stable",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	var stableID string
	for _, c := range stable.Candidates {
		if c.Metadata[MetadataKeySlot] == SlotProjectInstructions && strings.Contains(c.Content, "root instructions") {
			stableID = c.ID
			break
		}
	}
	if stableID == "" {
		t.Fatalf("stable response missing AGENTS.md candidate: %+v", stable.Candidates)
	}

	dynamic := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "dyn",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TouchedPaths:   []touchedpath.TouchedPath{{Path: filepath.Join(backend, "note.go"), Operation: "read"}},
	})
	for _, c := range dynamic.Candidates {
		if c.ID == stableID {
			t.Fatalf("stable material was re-offered dynamically: %+v", c)
		}
	}
	if len(dynamic.Candidates) == 0 {
		t.Fatalf("touched-path evidence produced nothing at all: %+v", dynamic)
	}

	explicit := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "explicit",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs:           []session.ContextRef{{Kind: "file", Target: "AGENTS.md"}},
	})
	found := false
	for _, c := range explicit.Candidates {
		if c.ID == stableID && len(c.Refs) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("explicit ref was omitted despite stable coverage: %+v", explicit)
	}
}

// TestCandidateIDStabilityAcrossActions pins the deterministic identity rule:
// the same logical candidate keeps its ID across responses within a provider
// lifecycle, including across the two actions.
func TestCandidateIDStabilityAcrossActions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "instructions")
	provider := NewProvider(Options{})

	stable := mustStable(t, provider, StableContextRequest{
		ID:             "s1",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	again := mustStable(t, provider, StableContextRequest{
		ID:             "s2",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
	})
	if len(stable.Candidates) == 0 || len(stable.Candidates) != len(again.Candidates) {
		t.Fatalf("candidate counts differ: %d vs %d", len(stable.Candidates), len(again.Candidates))
	}
	for i := range stable.Candidates {
		if stable.Candidates[i].ID != again.Candidates[i].ID {
			t.Fatalf("IDs differ across responses: %s vs %s", stable.Candidates[i].ID, again.Candidates[i].ID)
		}
	}

	explicit := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "d1",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		Refs:           []session.ContextRef{{Kind: "file", Target: "AGENTS.md"}},
	})
	var dynamicID string
	for _, c := range explicit.Candidates {
		if strings.Contains(c.Content, "instructions") {
			dynamicID = c.ID
		}
	}
	if dynamicID != stable.Candidates[0].ID {
		t.Fatalf("same source carried different IDs across actions: %s vs %s", dynamicID, stable.Candidates[0].ID)
	}
}

// TestRootsChangedRevokesCachedAccess: authorization comes only from the
// current request's roots; removing a root revokes access to its sources
// even when the dynamic index holds them.
func TestRootsChangedRevokesCachedAccess(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	mustMkdir(t, backend)
	mustWrite(t, filepath.Join(backend, "AGENTS.md"), "private")
	provider := NewProvider(Options{})

	first := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ctx",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{{Path: root}},
		TouchedPaths:   []touchedpath.TouchedPath{{Path: filepath.Join(backend, "new.go"), Operation: "write"}},
	})
	if len(first.Candidates) == 0 {
		t.Fatalf("expected discovered candidates: %+v", first)
	}

	revoked := mustDynamic(t, provider, DynamicContextRequest{
		ID:             "ctx2",
		Runtime:        &RuntimeFacts{CWD: root},
		WorkspaceRoots: []WorkspaceRoot{},
	})
	if len(revoked.Candidates) != 0 {
		t.Fatalf("candidate survived root removal: %+v", revoked.Candidates)
	}
}

func TestConcurrentStableAndDynamicRequests(t *testing.T) {
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
				_, failure := provider.GetStableContext(context.Background(), StableContextRequest{
					ID:             "stable",
					Runtime:        &RuntimeFacts{CWD: root},
					WorkspaceRoots: []WorkspaceRoot{{Path: root}},
				})
				if failure != nil {
					errs <- failure.Message
				}
				return
			}
			_, failure := provider.GetDynamicContext(context.Background(), DynamicContextRequest{
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

func mustStable(t *testing.T, provider *Provider, req StableContextRequest) *ContextResponse {
	t.Helper()
	response, failure := provider.GetStableContext(context.Background(), req)
	if failure != nil {
		t.Fatalf("GetStableContext() failure = %+v", failure)
	}
	return response
}

func mustDynamic(t *testing.T, provider *Provider, req DynamicContextRequest) *ContextResponse {
	t.Helper()
	response, failure := provider.GetDynamicContext(context.Background(), req)
	if failure != nil {
		t.Fatalf("GetDynamicContext() failure = %+v", failure)
	}
	return response
}

// slotContents joins the content of candidates carrying the given slot
// convention value in their advisory metadata.
func slotContents(response *ContextResponse, slot string) string {
	var values []string
	for _, candidate := range response.Candidates {
		if value, _ := candidate.Metadata[MetadataKeySlot].(string); value == slot {
			values = append(values, candidate.Content)
		}
	}
	return strings.Join(values, "\n")
}

func contents(candidates []ContextCandidate) string {
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
