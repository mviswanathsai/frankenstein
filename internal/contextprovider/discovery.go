package contextprovider

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"frankenstein/internal/session"
)

type requestState struct {
	ctx       context.Context
	requestID string
	opts      Options
	auth      *pathAuthorizer
	scope     runtimeScope
	failures  []string
	order     int
	inspected int
	terminal  error
}

func (s *requestState) nextOrder() int {
	s.order++
	return s.order
}

func (s *requestState) addFailure(value string) {
	if strings.TrimSpace(value) != "" {
		s.failures = append(s.failures, value)
	}
}

func (s *requestState) inspectEntry() error {
	if s.terminal != nil {
		return s.terminal
	}
	s.inspected++
	if s.inspected > s.opts.MaxInspectedDirEntries {
		s.terminal = &providerError{code: FailureTraversalLimitExceeded, message: "inspected directory entry limit exceeded"}
		return s.terminal
	}
	return nil
}

func discoverForCWD(state *requestState, cwd string) ([]sourceSpec, []syntheticSpec, error) {
	root, ok := state.auth.nearestRoot(cwd)
	if !ok {
		return nil, nil, nil
	}
	dirs := dirsBetween(root, cwd)
	var specs []sourceSpec
	var synthetic []syntheticSpec

	fallbacks := codexFallbackFilenames(state, dirs)
	for _, dir := range dirs {
		if err := state.ctx.Err(); err != nil {
			return nil, nil, err
		}
		specs = append(specs, discoverCodexDirectory(state, dir, fallbacks)...)
		specs = append(specs, discoverClaudeDirectory(state, dir)...)
		specs = append(specs, discoverCursorDirectory(state, dir)...)
		specs = append(specs, discoverHermesDirectory(state, dir)...)
	}
	synthetic = append(synthetic, discoverSkillIndexes(state, dirs)...)
	return specs, synthetic, nil
}

func discoverDirectory(state *requestState, dir string, priority int) ([]sourceSpec, []syntheticSpec, error) {
	if err := state.ctx.Err(); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var specs []sourceSpec
	for _, entry := range entries {
		if err := state.inspectEntry(); err != nil {
			return nil, nil, err
		}
		name := entry.Name()
		if name == "AGENTS.md" && hasAgentsOverride(state, dir) {
			continue
		}
		path := filepath.Join(dir, name)
		classified := classifyBaseFilename(path)
		if classified.Recognized {
			canonical, code, err := state.auth.canonicalizeExisting(path)
			if err != nil {
				if code == FailurePermissionDenied {
					continue
				}
				continue
			}
			specs = append(specs, makeFileSpec(state, canonical, sourceKindFile, "ordinary", classified, []destination{
				newDestination(classified.Slot, priority, false, ""),
			}, true)...)
		}
	}
	specs = append(specs, discoverClaudeDirectory(state, dir)...)
	specs = append(specs, discoverCursorDirectory(state, dir)...)
	specs = append(specs, discoverHermesDirectory(state, dir)...)
	synthetic := discoverSkillIndexes(state, []string{dir})
	return specs, synthetic, nil
}

func inspectContainingDirectory(state *requestState, filePath string) ([]sourceSpec, []syntheticSpec, error) {
	return discoverDirectory(state, filepath.Dir(filePath), priorityOptionalDiscovered)
}

func discoverCodexDirectory(state *requestState, dir string, fallbacks []string) []sourceSpec {
	names := []string{"AGENTS.override.md", "AGENTS.md"}
	names = append(names, fallbacks...)
	for _, name := range names {
		path := filepath.Join(dir, name)
		canonical, code, err := state.auth.canonicalizeExisting(path)
		if err != nil {
			if code != FailureSourceMissing {
				continue
			}
			continue
		}
		classified := classifySource(canonical, sourceKindFile)
		return makeFileSpec(state, canonical, sourceKindFile, "codex", classified, []destination{
			newDestination(classified.Slot, priorityInstructionsIdentity, false, ""),
		}, true)
	}
	return nil
}

func discoverClaudeDirectory(state *requestState, dir string) []sourceSpec {
	var specs []sourceSpec
	for _, name := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "CLAUDE.local.md"} {
		path := filepath.Join(dir, name)
		canonical, _, err := state.auth.canonicalizeExisting(path)
		if err != nil {
			continue
		}
		classified := classification{Slot: SlotProjectInstructions, Recognized: true}
		specs = append(specs, makeFileSpec(state, canonical, sourceKindFile, "claude", classified, []destination{
			newDestination(SlotProjectInstructions, priorityNativeRules, false, ""),
		}, true)...)
		specs = append(specs, discoverClaudeImports(state, canonical, 0)...)
	}
	rulesDir := filepath.Join(dir, ".claude", "rules")
	specs = append(specs, discoverMarkdownTree(state, rulesDir, sourceKindClaudeRule, "claude", 8)...)
	return specs
}

func discoverCursorDirectory(state *requestState, dir string) []sourceSpec {
	var specs []sourceSpec
	for _, name := range []string{".cursorrules", "CURSOR.md", "AGENTS.md", "CLAUDE.md"} {
		if name == "AGENTS.md" && hasAgentsOverride(state, dir) {
			continue
		}
		path := filepath.Join(dir, name)
		canonical, _, err := state.auth.canonicalizeExisting(path)
		if err != nil {
			continue
		}
		classified := classifySource(canonical, sourceKindFile)
		specs = append(specs, makeFileSpec(state, canonical, sourceKindFile, "cursor", classified, []destination{
			newDestination(classified.Slot, priorityNativeRules, false, ""),
		}, true)...)
	}
	rulesDir := filepath.Join(dir, ".cursor", "rules")
	specs = append(specs, discoverCursorRules(state, rulesDir)...)
	return specs
}

func discoverHermesDirectory(state *requestState, dir string) []sourceSpec {
	var specs []sourceSpec
	for _, name := range []string{".hermes.md", "HERMES.md", "AGENTS.md", "CLAUDE.md", ".cursorrules", "SOUL.md"} {
		if name == "AGENTS.md" && hasAgentsOverride(state, dir) {
			continue
		}
		path := filepath.Join(dir, name)
		canonical, _, err := state.auth.canonicalizeExisting(path)
		if err != nil {
			continue
		}
		classified := classifySource(canonical, sourceKindFile)
		priority := priorityInstructionsIdentity
		if classified.Slot == SlotMemory || classified.Slot == SlotUserProfile {
			priority = priorityProfileMemory
		}
		specs = append(specs, makeFileSpec(state, canonical, sourceKindFile, "hermes", classified, []destination{
			newDestination(classified.Slot, priority, false, ""),
		}, true)...)
	}
	specs = append(specs, discoverCursorRules(state, filepath.Join(dir, ".cursor", "rules"))...)
	return specs
}

func hasAgentsOverride(state *requestState, dir string) bool {
	_, _, err := state.auth.canonicalizeExisting(filepath.Join(dir, "AGENTS.override.md"))
	return err == nil
}

func discoverCursorRules(state *requestState, rulesDir string) []sourceSpec {
	var specs []sourceSpec
	canonical, _, err := state.auth.canonicalizeExisting(rulesDir)
	if err != nil {
		return nil
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(canonical)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := state.inspectEntry(); err != nil {
			return specs
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mdc") {
			continue
		}
		path := filepath.Join(canonical, entry.Name())
		fileCanonical, _, err := state.auth.canonicalizeExisting(path)
		if err != nil {
			continue
		}
		if !cursorRuleApplicable(fileCanonical) {
			continue
		}
		classified := classification{Slot: SlotProjectInstructions, Recognized: true}
		specs = append(specs, makeFileSpec(state, fileCanonical, sourceKindCursorRule, "cursor", classified, []destination{
			newDestination(SlotProjectInstructions, priorityNativeRules, false, ""),
		}, true)...)
	}
	return specs
}

func discoverMarkdownTree(state *requestState, root string, kind sourceKind, adapter string, maxDepth int) []sourceSpec {
	canonicalRoot, _, err := state.auth.canonicalizeExisting(root)
	if err != nil {
		return nil
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	var specs []sourceSpec
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if depth > maxDepth || state.ctx.Err() != nil {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if err := state.inspectEntry(); err != nil {
				return
			}
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walk(path, depth+1)
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			canonical, _, err := state.auth.canonicalizeExisting(path)
			if err != nil {
				continue
			}
			classified := classification{Slot: SlotProjectInstructions, Recognized: true}
			specs = append(specs, makeFileSpec(state, canonical, kind, adapter, classified, []destination{
				newDestination(SlotProjectInstructions, priorityNativeRules, false, ""),
			}, true)...)
		}
	}
	walk(canonicalRoot, 0)
	return specs
}

func discoverClaudeImports(state *requestState, sourcePath string, depth int) []sourceSpec {
	if depth >= 4 {
		return nil
	}
	content, err := readSmallFile(sourcePath, 128<<10)
	if err != nil {
		return nil
	}
	var specs []sourceSpec
	for _, imported := range parseClaudeImports(content) {
		resolved := imported
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(sourcePath), filepath.Clean(resolved))
		}
		canonical, _, err := state.auth.canonicalizeExisting(resolved)
		if err != nil {
			continue
		}
		classified := classifySource(canonical, sourceKindFile)
		if !classified.Recognized {
			classified = classification{Slot: SlotProjectInstructions, Recognized: true}
		}
		specs = append(specs, makeFileSpec(state, canonical, sourceKindFile, "claude_import", classified, []destination{
			newDestination(classified.Slot, priorityNativeRules, false, ""),
		}, true)...)
		specs = append(specs, discoverClaudeImports(state, canonical, depth+1)...)
	}
	return specs
}

var claudeImportPattern = regexp.MustCompile(`(?m)(^|\s)@([^\s` + "`" + `'"<>()]+)`)

func parseClaudeImports(content string) []string {
	matches := claudeImportPattern.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		value := strings.TrimSpace(match[2])
		if value == "" || strings.Contains(value, "://") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func codexFallbackFilenames(state *requestState, dirs []string) []string {
	var fallbacks []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		configPath := filepath.Join(dir, ".codex", "config.toml")
		canonical, _, err := state.auth.canonicalizeExisting(configPath)
		if err != nil {
			continue
		}
		content, err := readSmallFile(canonical, 128<<10)
		if err != nil {
			continue
		}
		for _, name := range parseCodexFallbacks(content) {
			if name == "" || strings.ContainsAny(name, `/\`) {
				continue
			}
			if !seen[name] {
				seen[name] = true
				fallbacks = append(fallbacks, name)
			}
		}
	}
	return fallbacks
}

var fallbackPattern = regexp.MustCompile(`(?m)project_doc_fallback_filenames\s*=\s*\[([^\]]*)\]`)
var quotedStringPattern = regexp.MustCompile(`"([^"]+)"|'([^']+)'`)

func parseCodexFallbacks(content string) []string {
	match := fallbackPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return nil
	}
	var out []string
	for _, stringMatch := range quotedStringPattern.FindAllStringSubmatch(match[1], -1) {
		if len(stringMatch) < 3 {
			continue
		}
		value := stringMatch[1]
		if value == "" {
			value = stringMatch[2]
		}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func discoverSkillIndexes(state *requestState, dirs []string) []syntheticSpec {
	var skillFiles []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, container := range []string{
			filepath.Join(dir, ".agents", "skills"),
			filepath.Join(dir, ".claude", "skills"),
			filepath.Join(dir, ".cursor", "skills"),
			filepath.Join(dir, "skills"),
			filepath.Join(dir, ".skills"),
		} {
			for _, path := range scanSkillContainer(state, container) {
				if !seen[path] {
					seen[path] = true
					skillFiles = append(skillFiles, path)
				}
			}
		}
	}
	sort.Strings(skillFiles)
	if len(skillFiles) == 0 {
		return nil
	}

	var lines []string
	for _, path := range skillFiles {
		meta := readSkillMetadata(path)
		name := meta.Name
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		description := meta.Description
		if description == "" {
			description = "No description provided."
		}
		lines = append(lines, fmt.Sprintf("- %s: %s (source: %s)", name, description, path))
	}
	body := strings.Join(lines, "\n")
	header := "## Skills index\n\n"
	content, _ := composeLimitedContent(header, body, "\n\n[TRUNCATED: skills index exceeds candidate content limit; content is incomplete.]\n", false, state.opts.MaxCandidateContentBytes)
	dest := newDestination(SlotSkills, prioritySkillIndex, false, "")
	candidate := ContextCandidate{
		ID:       syntheticCandidateID(state.opts.ProviderID, SlotSkills, "skills-index"),
		Metadata: slotMetadata(SlotSkills),
		Content:  content,
	}
	return []syntheticSpec{{
		Candidate:   candidate,
		Destination: dest,
		Order:       state.nextOrder(),
	}}
}

func scanSkillContainer(state *requestState, container string) []string {
	canonical, _, err := state.auth.canonicalizeExisting(container)
	if err != nil {
		return nil
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(canonical)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []string
	for _, entry := range entries {
		if err := state.inspectEntry(); err != nil {
			return out
		}
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(canonical, entry.Name(), "SKILL.md")
		resolved, _, err := state.auth.canonicalizeExisting(skillPath)
		if err != nil {
			continue
		}
		out = append(out, resolved)
	}
	return out
}

type skillMetadata struct {
	Name        string
	Description string
}

func readSkillMetadata(path string) skillMetadata {
	content, err := readSmallFile(path, 64<<10)
	if err != nil {
		return skillMetadata{}
	}
	meta := skillMetadata{}
	frontmatter, body := splitFrontmatter(content)
	if name := frontmatter["name"]; name != "" {
		meta.Name = name
	}
	if description := frontmatter["description"]; description != "" {
		meta.Description = description
	}
	if meta.Description == "" {
		meta.Description = firstParagraph(body)
	}
	return meta
}

func cursorRuleApplicable(path string) bool {
	content, err := readSmallFile(path, 64<<10)
	if err != nil {
		return false
	}
	frontmatter, _ := splitFrontmatter(content)
	alwaysApply := strings.EqualFold(frontmatter["alwaysApply"], "true")
	if alwaysApply {
		return true
	}
	if strings.TrimSpace(frontmatter["globs"]) != "" {
		return true
	}
	if value, ok := frontmatter["alwaysApply"]; ok && strings.EqualFold(value, "false") {
		return false
	}
	return true
}

func splitFrontmatter(content string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(content, "---\n") {
		return out, content
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return out, content
	}
	raw := content[4 : 4+end]
	body := strings.TrimLeft(content[4+end+4:], "\n")
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		out[key] = value
	}
	return out, body
}

func firstParagraph(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

func readSmallFile(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > limit {
		raw = raw[:limit]
	}
	return bytesToValidUTF8(raw), nil
}

func makeFileSpec(state *requestState, path string, kind sourceKind, adapter string, classified classification, destinations []destination, indexable bool) []sourceSpec {
	if !classified.Recognized {
		classified.Slot = SlotUnknown
	}
	label := filepath.Base(path)
	spec := sourceSpec{
		Path:         path,
		Kind:         kind,
		Label:        label,
		Adapter:      adapter,
		Refs:         []session.ContextRef{sourceRef(path, label)},
		Destinations: destinations,
		Optional:     true,
		Indexable:    indexable,
		Order:        state.nextOrder(),
	}
	return []sourceSpec{spec}
}

func dirsBetween(root, cwd string) []string {
	root = filepath.Clean(root)
	cwd = filepath.Clean(cwd)
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == "." {
		return []string{root}
	}
	parts := strings.Split(rel, string(filepath.Separator))
	dirs := []string{root}
	current := root
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

func refLabel(ref session.ContextRef) string {
	if ref.Label != "" {
		return ref.Label
	}
	if ref.Kind != "" && ref.Target != "" {
		return ref.Kind + ":" + ref.Target
	}
	return ref.Target
}
