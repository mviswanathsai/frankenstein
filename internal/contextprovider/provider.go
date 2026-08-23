package contextprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"frankenstein/internal/session"
	"frankenstein/internal/touchedpath"
)

// Provider implements the context_provider.v0.2 contract.
//
// The provider keeps implementation-private lifecycle state: a file cache,
// an index of dynamically discovered sources re-offered on every dynamic
// request, and the set of canonical paths emitted by get_stable_context.
// The stable set enforces the stable/dynamic partition structurally:
// material frozen at session start is never re-offered as a dynamic
// candidate unless a caller explicitly references it.
type Provider struct {
	opts  Options
	cache *fileCache

	mu          sync.RWMutex
	index       map[string]sourceSpec
	stablePaths map[string]struct{}
}

func NewProvider(opts Options) *Provider {
	opts = normalizeOptions(opts)
	return &Provider{
		opts:        opts,
		cache:       newFileCache(),
		index:       map[string]sourceSpec{},
		stablePaths: map[string]struct{}{},
	}
}

func (p *Provider) Info() ContractInfo {
	return ContractInfo{
		Capability:      CapabilityName,
		ContractVersion: ContractVersion,
		ProviderID:      p.opts.ProviderID,
	}
}

// GetStableContext returns the session's stable material: everything
// discoverable at startup from the granted boundary — instruction files
// along the ancestor chains of the workspace roots and cwd, identity,
// profile, baseline memory files, and the skill index. Called once per
// session; the caller freezes the result into renderer config.
//
// The canonical paths of emitted candidates are remembered as the stable
// set. Stable finds are deliberately not published to the dynamic index.
func (p *Provider) GetStableContext(ctx context.Context, req StableContextRequest) (*ContextResponse, *ContextFailure) {
	state, failure := p.prepare(ctx, req.ID, req.Runtime, req.WorkspaceRoots)
	if failure != nil {
		return nil, failure
	}

	var specs []sourceSpec
	var synthetic []syntheticSpec
	if cwd, ok := state.auth.authorizedCWD(state.scope); ok {
		discovered, skillIndexes, err := discoverForCWD(state, cwd)
		if failure := p.failureFromError(req.ID, err); failure != nil {
			return nil, failure
		}
		specs = append(specs, discovered...)
		synthetic = append(synthetic, skillIndexes...)
	}
	if failure := p.failureFromError(req.ID, state.terminal); failure != nil {
		return nil, failure
	}

	occurrences, failure := p.processAll(ctx, state, specs, synthetic)
	if failure != nil {
		return nil, failure
	}
	response, _, emittedPaths := buildResponse(req.ID, occurrences, state.failures, p.opts)

	p.mu.Lock()
	if p.stablePaths == nil {
		p.stablePaths = map[string]struct{}{}
	}
	for _, path := range emittedPaths {
		if path != "" {
			p.stablePaths[path] = struct{}{}
		}
	}
	p.mu.Unlock()

	return response, nil
}

// GetDynamicContext returns dynamic context candidates for the current
// request. Discovery is strictly evidence-driven: explicit input refs are
// dereferenced (with sibling-directory inspection), touched paths trigger
// file, containing-directory, and parent discovery, and everything the
// provider has dynamically found earlier in the session is re-offered from
// its index so a diff-gated renderer sees a complete current offering.
//
// Discovered candidates whose source is already in the stable set are
// omitted. Explicit input refs are never omitted: the accounting invariant
// requires every ref to appear among some candidate's refs or in failures.
func (p *Provider) GetDynamicContext(ctx context.Context, req DynamicContextRequest) (*ContextResponse, *ContextFailure) {
	state, failure := p.prepare(ctx, req.ID, req.Runtime, req.WorkspaceRoots)
	if failure != nil {
		return nil, failure
	}

	var specs []sourceSpec
	var synthetic []syntheticSpec
	specs = append(specs, p.indexSpecs(state)...)

	refSpecs, refSynthetic := p.handleExplicitRefs(state, req.Refs)
	specs = append(specs, refSpecs...)
	synthetic = append(synthetic, refSynthetic...)

	touchedSpecs, touchedSynthetic := p.handleTouchedPaths(state, req.TouchedPaths)
	specs = append(specs, touchedSpecs...)
	synthetic = append(synthetic, touchedSynthetic...)

	if failure := p.failureFromError(req.ID, state.terminal); failure != nil {
		return nil, failure
	}

	occurrences, failure := p.processAll(ctx, state, specs, synthetic)
	if failure != nil {
		return nil, failure
	}
	occurrences = p.omitStableCovered(occurrences)

	response, indexed, _ := buildResponse(req.ID, occurrences, state.failures, p.opts)
	p.publishIndex(indexed)
	return response, nil
}

// omitStableCovered drops discovered occurrences whose canonical source path
// was already emitted by get_stable_context. Explicit occurrences survive:
// every input ref must be accounted for through candidate refs or failures.
func (p *Provider) omitStableCovered(occurrences []candidateOccurrence) []candidateOccurrence {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.stablePaths) == 0 {
		return occurrences
	}
	out := make([]candidateOccurrence, 0, len(occurrences))
	for _, occ := range occurrences {
		if occ.Path != "" && !occ.Explicit {
			if _, stable := p.stablePaths[occ.Path]; stable {
				continue
			}
		}
		out = append(out, occ)
	}
	return out
}

func (p *Provider) prepare(ctx context.Context, requestID string, runtime *RuntimeFacts, roots []WorkspaceRoot) (*requestState, *ContextFailure) {
	if strings.TrimSpace(requestID) == "" {
		return nil, terminalFailure(requestID, FailureInvalidRequest, "id is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, terminalFailure(requestID, FailureContextCanceled, err.Error())
	}
	auth, err := newAuthorizer(roots)
	if err != nil {
		var providerErr *providerError
		if errors.As(err, &providerErr) {
			return nil, terminalFailure(requestID, providerErr.code, providerErr.message)
		}
		return nil, terminalFailure(requestID, FailureInvalidWorkspaceRoot, err.Error())
	}
	scope, err := validateRuntime(runtime)
	if err != nil {
		var providerErr *providerError
		if errors.As(err, &providerErr) {
			return nil, terminalFailure(requestID, providerErr.code, providerErr.message)
		}
		return nil, terminalFailure(requestID, FailureInvalidRequest, err.Error())
	}
	return &requestState{
		ctx:       ctx,
		requestID: requestID,
		opts:      p.opts,
		auth:      auth,
		scope:     scope,
	}, nil
}

func (p *Provider) failureFromError(requestID string, err error) *ContextFailure {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return terminalFailure(requestID, FailureContextCanceled, err.Error())
	}
	var providerErr *providerError
	if errors.As(err, &providerErr) {
		return terminalFailure(requestID, providerErr.code, providerErr.message)
	}
	return terminalFailure(requestID, FailureInternalFailure, err.Error())
}

func (p *Provider) handleExplicitRefs(state *requestState, refs []session.ContextRef) ([]sourceSpec, []syntheticSpec) {
	var specs []sourceSpec
	var synthetic []syntheticSpec
	for _, ref := range refs {
		label := refLabel(ref)
		switch ref.Kind {
		case "file":
			resolved := state.auth.resolveInput(ref.Target, state.scope)
			if resolved.err != nil {
				state.addFailure(refFailure(label, resolved.code, resolved.err.Error()))
				continue
			}
			if !resolved.exists {
				state.addFailure(refFailuref(label, FailureSourceMissing, "source does not exist: %s", resolved.lexical))
				continue
			}
			if !resolved.isFile {
				if resolved.isDir {
					state.addFailure(refFailuref(label, FailureNonRegularSource, "expected file but got directory: %s", resolved.canonical))
				} else {
					state.addFailure(refFailuref(label, FailureNonRegularSource, "source is not a regular file: %s", resolved.canonical))
				}
				continue
			}
			classified := classifySource(resolved.canonical, sourceKindFile)
			dest := newDestination(classified.Slot, priorityExplicitRef, true, label)
			spec := makeFileSpec(state, resolved.canonical, sourceKindFile, "explicit_ref", classified, []destination{dest}, classified.Recognized)
			if len(spec) > 0 {
				spec[0].Optional = false
				spec[0].Refs = []session.ContextRef{ref}
				specs = append(specs, spec...)
			}
			siblingSpecs, siblingSynthetic, err := inspectContainingDirectory(state, resolved.canonical)
			if err == nil {
				specs = append(specs, siblingSpecs...)
				synthetic = append(synthetic, siblingSynthetic...)
			}
		case "directory":
			resolved := state.auth.resolveInput(ref.Target, state.scope)
			if resolved.err != nil {
				state.addFailure(refFailure(label, resolved.code, resolved.err.Error()))
				continue
			}
			if !resolved.exists {
				state.addFailure(refFailuref(label, FailureSourceMissing, "source does not exist: %s", resolved.lexical))
				continue
			}
			if !resolved.isDir {
				state.addFailure(refFailuref(label, FailureNonRegularSource, "expected directory: %s", resolved.canonical))
				continue
			}
			synthetic = append(synthetic, directoryReferencedCandidate(state, resolved.canonical, ref))
			dirSpecs, dirSynthetic, err := discoverDirectory(state, resolved.canonical, priorityOptionalDiscovered)
			if err == nil {
				specs = append(specs, dirSpecs...)
				synthetic = append(synthetic, dirSynthetic...)
			}
		default:
			state.addFailure(refFailuref(label, FailureUnsupportedRefKind, "unsupported ref kind %q", ref.Kind))
		}
	}
	return specs, synthetic
}

func (p *Provider) handleTouchedPaths(state *requestState, touched []touchedpath.TouchedPath) ([]sourceSpec, []syntheticSpec) {
	var specs []sourceSpec
	var synthetic []syntheticSpec
	for _, touchedPath := range touched {
		if strings.TrimSpace(touchedPath.Path) == "" {
			continue
		}
		resolved := state.auth.resolveInput(touchedPath.Path, state.scope)
		if resolved.err != nil {
			continue
		}
		if resolved.exists {
			if resolved.isFile {
				classified := classifySource(resolved.canonical, sourceKindFile)
				var dest destination
				indexable := false
				if classified.Recognized {
					dest = newDestination(classified.Slot, priorityForSlot(classified.Slot, priorityDirectTouched), false, "")
					indexable = true
				} else {
					dest = newDestination(SlotUnknown, priorityDirectTouched, false, "")
				}
				specs = append(specs, makeFileSpec(state, resolved.canonical, sourceKindFile, "touched_path", classified, []destination{dest}, indexable)...)
				siblingSpecs, siblingSynthetic, err := inspectContainingDirectory(state, resolved.canonical)
				if err == nil {
					specs = append(specs, siblingSpecs...)
					synthetic = append(synthetic, siblingSynthetic...)
				}
				continue
			}
			if resolved.isDir {
				dirSpecs, dirSynthetic, err := discoverDirectory(state, resolved.canonical, priorityOptionalDiscovered)
				if err == nil {
					specs = append(specs, dirSpecs...)
					synthetic = append(synthetic, dirSynthetic...)
				}
			}
			continue
		}
		parent, ok, _, err := state.auth.existingParent(resolved.lexical)
		if err != nil || !ok {
			continue
		}
		dirSpecs, dirSynthetic, err := discoverDirectory(state, parent, priorityOptionalDiscovered)
		if err == nil {
			specs = append(specs, dirSpecs...)
			synthetic = append(synthetic, dirSynthetic...)
		}
	}
	return specs, synthetic
}

func (p *Provider) processAll(ctx context.Context, state *requestState, specs []sourceSpec, synthetic []syntheticSpec) ([]candidateOccurrence, *ContextFailure) {
	occurrences := make([]candidateOccurrence, 0, len(synthetic)+len(specs))
	for _, spec := range synthetic {
		if strings.TrimSpace(spec.Candidate.Content) == "" {
			continue
		}
		occurrences = append(occurrences, candidateOccurrence{
			Candidate: spec.Candidate,
			Priority:  spec.Destination.Priority,
			Order:     spec.Order,
			Explicit:  spec.Destination.Explicit,
			RefLabel:  spec.Destination.RefLabel,
		})
	}

	fileOccurrences, failure := p.processSources(ctx, state, specs)
	if failure != nil {
		return nil, failure
	}
	occurrences = append(occurrences, fileOccurrences...)
	sortOccurrences(occurrences)
	return occurrences, nil
}

func (p *Provider) processSources(ctx context.Context, state *requestState, specs []sourceSpec) ([]candidateOccurrence, *ContextFailure) {
	specs = dedupeSpecs(specs)
	workers := p.opts.MaxConcurrentReads
	if workers > len(specs) {
		workers = len(specs)
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan sourceSpec)
	results := make(chan sourceProcessResult, len(specs))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spec := range jobs {
				if ctx.Err() != nil {
					results <- sourceProcessResult{spec: spec, code: FailureContextCanceled, err: ctx.Err()}
					continue
				}
				results <- p.processSource(ctx, spec)
			}
		}()
	}
	for _, spec := range specs {
		if ctx.Err() != nil {
			break
		}
		jobs <- spec
	}
	close(jobs)
	wg.Wait()
	close(results)

	var occurrences []candidateOccurrence
	for result := range results {
		if result.err != nil {
			if result.code == FailureContextCanceled {
				return nil, terminalFailure(state.requestID, FailureContextCanceled, result.err.Error())
			}
			for _, dest := range result.spec.Destinations {
				if dest.Explicit {
					state.addFailure(refFailure(dest.RefLabel, result.code, result.err.Error()))
				}
			}
			continue
		}
		occurrences = append(occurrences, result.occurrences...)
	}
	return occurrences, nil
}

type sourceProcessResult struct {
	spec        sourceSpec
	occurrences []candidateOccurrence
	code        string
	err         error
}

func (p *Provider) processSource(ctx context.Context, spec sourceSpec) sourceProcessResult {
	readLimit, code, err := readLimitForSpec(spec, p.opts)
	if err != nil {
		return sourceProcessResult{spec: spec, code: code, err: err}
	}
	read, code, err := readCachedOrFresh(ctx, p.cache, spec.Path, readLimit, p.opts.MaxSourceReadBytes)
	if err != nil {
		return sourceProcessResult{spec: spec, code: code, err: err}
	}
	content, code, err := buildFileContent(spec, read, p.opts)
	if err != nil {
		return sourceProcessResult{spec: spec, code: code, err: err}
	}
	var occurrences []candidateOccurrence
	for _, dest := range spec.Destinations {
		refs := cloneRefs(spec.Refs)
		if len(refs) == 0 {
			refs = []session.ContextRef{sourceRef(spec.Path, spec.Label)}
		}
		candidate := ContextCandidate{
			ID:       fileCandidateID(p.opts.ProviderID, dest.Slot, spec.Path),
			Metadata: slotMetadata(dest.Slot),
			Content:  content,
			Refs:     refs,
		}
		specCopy := spec
		occurrences = append(occurrences, candidateOccurrence{
			Candidate: candidate,
			Path:      spec.Path,
			Priority:  dest.Priority,
			Order:     spec.Order,
			Explicit:  dest.Explicit,
			RefLabel:  dest.RefLabel,
			IndexSpec: &specCopy,
		})
	}
	return sourceProcessResult{spec: spec, occurrences: occurrences}
}

func dedupeSpecs(specs []sourceSpec) []sourceSpec {
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return specs[i].Path < specs[j].Path
	})
	byKey := map[string]sourceSpec{}
	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		key := fmt.Sprintf("%s|%s|%s", spec.Path, spec.Kind, spec.Adapter)
		existing, ok := byKey[key]
		if !ok {
			byKey[key] = spec
			keys = append(keys, key)
			continue
		}
		existing.Destinations = append(existing.Destinations, spec.Destinations...)
		existing.Optional = existing.Optional && spec.Optional
		existing.Indexable = existing.Indexable || spec.Indexable
		if len(existing.Refs) == 0 {
			existing.Refs = spec.Refs
		}
		byKey[key] = existing
	}
	out := make([]sourceSpec, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}

func (p *Provider) publishIndex(specs []sourceSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, spec := range specs {
		if spec.Path == "" {
			continue
		}
		key := spec.Path + "|" + string(spec.Kind) + "|" + spec.Adapter
		p.index[key] = spec
	}
}

func (p *Provider) indexSpecs(state *requestState) []sourceSpec {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]sourceSpec, 0, len(p.index))
	for _, spec := range p.index {
		if !state.auth.isAuthorized(spec.Path) {
			continue
		}
		info, err := os.Stat(spec.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		spec.Order = state.nextOrder()
		out = append(out, spec)
	}
	return out
}

func directoryReferencedCandidate(state *requestState, path string, ref session.ContextRef) syntheticSpec {
	entries, _ := os.ReadDir(path)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var lines []string
	for _, entry := range entries {
		if len(lines) >= 200 {
			lines = append(lines, "- ...")
			break
		}
		name := entry.Name()
		if classified := classifyBaseFilename(name); classified.Recognized {
			lines = append(lines, "- "+name)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "(directory exists; no recognized context sources found at this level)")
	}
	header := fmt.Sprintf("## Directory reference\nSource: %s\n\n", path)
	content, _ := composeLimitedContent(header, strings.Join(lines, "\n"), truncationMarker(path), false, state.opts.MaxCandidateContentBytes)
	dest := newDestination(SlotUnknown, priorityExplicitRef, true, refLabel(ref))
	return syntheticSpec{
		Candidate: ContextCandidate{
			ID:       syntheticCandidateID(state.opts.ProviderID, SlotUnknown, path),
			Metadata: slotMetadata(SlotUnknown),
			Content:  content,
			Refs:     []session.ContextRef{ref},
		},
		Destination: dest,
		Order:       state.nextOrder(),
	}
}

func cloneRefs(refs []session.ContextRef) []session.ContextRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]session.ContextRef, len(refs))
	copy(out, refs)
	return out
}

func priorityForSlot(slot string, fallback int) int {
	switch slot {
	case SlotIdentity, SlotProjectInstructions:
		return priorityInstructionsIdentity
	case SlotUserProfile, SlotMemory:
		return priorityProfileMemory
	case SlotSkills:
		return prioritySkillIndex
	case SlotUnknown:
		return priorityUnknownOptional
	default:
		return fallback
	}
}
