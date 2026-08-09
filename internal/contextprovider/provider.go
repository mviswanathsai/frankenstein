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
)

type Provider struct {
	opts  Options
	cache *fileCache

	mu    sync.RWMutex
	index map[string]sourceSpec
}

func NewProvider(opts Options) *Provider {
	opts = normalizeOptions(opts)
	return &Provider{
		opts:  opts,
		cache: newFileCache(),
		index: map[string]sourceSpec{},
	}
}

func (p *Provider) Info() ContractInfo {
	return ContractInfo{
		Capability:      CapabilityName,
		ContractVersion: ContractVersion,
		ProviderID:      p.opts.ProviderID,
	}
}

func (p *Provider) Initialize(ctx context.Context, req ContextInitializeRequest) (*ContextBundle, *ContextFailure) {
	state, failure := p.prepare(ctx, req.ID, &req.Runtime, req.WorkspaceRoots)
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

	refSpecs, refSynthetic := p.handleExplicitRefs(state, req.Refs, lifetimeRetained)
	specs = append(specs, refSpecs...)
	synthetic = append(synthetic, refSynthetic...)
	if failure := p.failureFromError(req.ID, state.terminal); failure != nil {
		return nil, failure
	}

	occurrences, failure := p.processAll(ctx, state, specs, synthetic)
	if failure != nil {
		return nil, failure
	}
	bundle, indexed := buildBundle(req.ID, p.opts.ProviderID, occurrences, state.failures, p.opts)
	p.publishIndex(indexed)
	return bundle, nil
}

func (p *Provider) GetContext(ctx context.Context, req ContextRequest) (*ContextBundle, *ContextFailure) {
	state, failure := p.prepare(ctx, req.ID, req.Runtime, req.WorkspaceRoots)
	if failure != nil {
		return nil, failure
	}

	var specs []sourceSpec
	var synthetic []syntheticSpec
	specs = append(specs, p.indexSpecs(state)...)
	if cwd, ok := state.auth.authorizedCWD(state.scope); ok {
		discovered, skillIndexes, err := discoverForCWD(state, cwd)
		if failure := p.failureFromError(req.ID, err); failure != nil {
			return nil, failure
		}
		specs = append(specs, discovered...)
		synthetic = append(synthetic, skillIndexes...)
	}

	if req.TriggeringRecord != nil {
		refSpecs, refSynthetic := p.handleExplicitRefs(state, req.TriggeringRecord.Refs, lifetimePerCall)
		specs = append(specs, refSpecs...)
		synthetic = append(synthetic, refSynthetic...)
	}
	touchedSpecs, touchedSynthetic := p.handleTouchedPaths(state, req.TouchedPaths)
	specs = append(specs, touchedSpecs...)
	synthetic = append(synthetic, touchedSynthetic...)
	if queryShaped(req.TriggeringRecord) {
		specs = addPerCallMemoryDestinations(specs)
	}
	if failure := p.failureFromError(req.ID, state.terminal); failure != nil {
		return nil, failure
	}

	occurrences, failure := p.processAll(ctx, state, specs, synthetic)
	if failure != nil {
		return nil, failure
	}
	bundle, indexed := buildBundle(req.ID, p.opts.ProviderID, occurrences, state.failures, p.opts)
	p.publishIndex(indexed)
	return bundle, nil
}

func (p *Provider) prepare(ctx context.Context, requestID string, runtime *RuntimeFacts, roots []WorkspaceRoot) (*requestState, *ContextFailure) {
	if strings.TrimSpace(requestID) == "" {
		return nil, terminalFailure(requestID, p.opts.ProviderID, FailureInvalidRequest, "id is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, terminalFailure(requestID, p.opts.ProviderID, FailureContextCanceled, err.Error())
	}
	auth, err := newAuthorizer(roots)
	if err != nil {
		var providerErr *providerError
		if errors.As(err, &providerErr) {
			return nil, terminalFailure(requestID, p.opts.ProviderID, providerErr.code, providerErr.message)
		}
		return nil, terminalFailure(requestID, p.opts.ProviderID, FailureInvalidWorkspaceRoot, err.Error())
	}
	scope, err := validateRuntime(runtime)
	if err != nil {
		var providerErr *providerError
		if errors.As(err, &providerErr) {
			return nil, terminalFailure(requestID, p.opts.ProviderID, providerErr.code, providerErr.message)
		}
		return nil, terminalFailure(requestID, p.opts.ProviderID, FailureInvalidRequest, err.Error())
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
		return terminalFailure(requestID, p.opts.ProviderID, FailureContextCanceled, err.Error())
	}
	var providerErr *providerError
	if errors.As(err, &providerErr) {
		return terminalFailure(requestID, p.opts.ProviderID, providerErr.code, providerErr.message)
	}
	return terminalFailure(requestID, p.opts.ProviderID, FailureInvalidRequest, err.Error())
}

func (p *Provider) handleExplicitRefs(state *requestState, refs []session.ContextRef, life lifetime) ([]sourceSpec, []syntheticSpec) {
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
			destinations := []destination{referencedDestination(life, priorityExplicitRef, true, label)}
			if classified.Recognized {
				semanticPriority := priorityForSlot(classified.Slot, priorityInstructionsIdentity)
				destinations = append(destinations, retainedDestination(classified.Slot, semanticPriority, false, ""))
			}
			spec := makeFileSpec(state, resolved.canonical, sourceKindFile, "explicit_ref", classified, destinations, classified.Recognized)
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
			synthetic = append(synthetic, directoryReferencedCandidate(state, resolved.canonical, ref, life))
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

func (p *Provider) handleTouchedPaths(state *requestState, touched []TouchedPath) ([]sourceSpec, []syntheticSpec) {
	var specs []sourceSpec
	var synthetic []syntheticSpec
	for _, touchedPath := range touched {
		if strings.TrimSpace(touchedPath.Path) == "" {
			continue
		}
		resolved := state.auth.resolveInput(touchedPath.Path, state.scope)
		if resolved.err != nil {
			if resolved.code == FailureMissingCWDForRelativePath {
				continue
			}
			continue
		}
		if resolved.exists {
			if resolved.isFile {
				classified := classifySource(resolved.canonical, sourceKindFile)
				dest := perCallDestination(SlotUnknown, priorityDirectTouched, false, "")
				indexable := false
				if classified.Recognized {
					dest = retainedDestination(classified.Slot, priorityForSlot(classified.Slot, priorityDirectTouched), false, "")
					indexable = true
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
			Candidate:   spec.Candidate,
			Destination: spec.Destination,
			Priority:    spec.Destination.Priority,
			Order:       spec.Order,
			Explicit:    spec.Destination.Explicit,
			RefLabel:    spec.Destination.RefLabel,
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
				return nil, terminalFailure(state.requestID, p.opts.ProviderID, FailureContextCanceled, result.err.Error())
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
			ID:      fileCandidateID(p.opts.ProviderID, dest, spec),
			Content: content,
			Refs:    refs,
		}
		specCopy := spec
		occurrences = append(occurrences, candidateOccurrence{
			Candidate:   candidate,
			Destination: dest,
			Priority:    dest.Priority,
			Order:       spec.Order,
			Explicit:    dest.Explicit && dest.Referenced,
			RefLabel:    dest.RefLabel,
			IndexSpec:   &specCopy,
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

func directoryReferencedCandidate(state *requestState, path string, ref session.ContextRef, life lifetime) syntheticSpec {
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
	dest := referencedDestination(life, priorityExplicitRef, true, refLabel(ref))
	return syntheticSpec{
		Candidate: ContextCandidate{
			ID:      syntheticCandidateID(state.opts.ProviderID, dest, path),
			Content: content,
			Refs:    []session.ContextRef{ref},
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

func priorityForSlot(slot ContextSlot, fallback int) int {
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

func queryShaped(record *session.SessionRecord) bool {
	if record == nil {
		return false
	}
	if record.Text == nil {
		return false
	}
	text := strings.TrimSpace(*record.Text)
	if text == "" {
		return false
	}
	return strings.Contains(text, "?")
}

func addPerCallMemoryDestinations(specs []sourceSpec) []sourceSpec {
	out := make([]sourceSpec, len(specs))
	copy(out, specs)
	for i := range out {
		classified := classifySource(out[i].Path, out[i].Kind)
		if classified.Slot != SlotMemory {
			continue
		}
		out[i].Destinations = append(out[i].Destinations, perCallDestination(SlotMemory, priorityProfileMemory, false, ""))
	}
	return out
}
