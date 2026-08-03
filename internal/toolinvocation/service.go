package toolinvocation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/session"
)

const (
	defaultCatalogCacheCapacity = 16
	discoveryProvider           = "frankenstein.discovery"
	discoveryProviderVersion    = "0"
	toolLoadID                  = discoveryProvider + ":" + discoveryProviderVersion + ":tool_load"
	toolCallID                  = discoveryProvider + ":" + discoveryProviderVersion + ":tool_call"
)

type CallStartedAcknowledger func(context.Context, ToolCallStarted) error

type ProxyDispatchAcknowledger func(context.Context, ToolProxyDispatchAttempted) error

type DiscoveryStrategy string

const (
	DiscoveryDirect DiscoveryStrategy = ""
	DiscoveryProxy  DiscoveryStrategy = "proxy"
)

type Options struct {
	CatalogCacheCapacity     int
	DiscoveryStrategy        DiscoveryStrategy
	AcknowledgeCallStarted   CallStartedAcknowledger
	AcknowledgeProxyDispatch ProxyDispatchAcknowledger
}

type Registration struct {
	Provider        string
	ProviderVersion string
	LocalName       string

	Name        string
	Description string
	InputSchema json.RawMessage

	InitiallyVisible bool
	Discoverable     bool
	RuntimeAvailable func(context.Context) bool
	Backend          BackendFunc
}

type BackendRequest struct {
	Call      ToolCall
	Tool      ToolDefinition
	Arguments map[string]any
}

type BackendResult struct {
	Status        ToolResultStatus
	Text          string
	Refs          []session.ContextRef
	TouchedPaths  []contextprovider.TouchedPath
	SideEffect    ToolSideEffect
	StopRequested bool
	Failure       *ToolFailure
}

type BackendFunc func(context.Context, BackendRequest) BackendResult

type Service struct {
	opts Options

	mu    sync.RWMutex
	tools map[string]*registeredTool
	order []string

	cache *catalogCache

	idemMu sync.Mutex
	idem   map[idempotencyKey]*idempotencyRecord

	lineageMu sync.Mutex
	lineage   map[lineageKey]*sync.Mutex
}

type registeredTool struct {
	def              ToolDefinition
	schema           compiledSchema
	initiallyVisible bool
	discoverable     bool
	runtimeAvailable func(context.Context) bool
	backend          BackendFunc
}

func NewService(opts Options, registrations ...Registration) (*Service, error) {
	if opts.AcknowledgeCallStarted == nil {
		return nil, errors.New("toolinvocation: AcknowledgeCallStarted is required")
	}
	if opts.DiscoveryStrategy != DiscoveryDirect && opts.DiscoveryStrategy != DiscoveryProxy {
		return nil, fmt.Errorf("toolinvocation: unsupported discovery strategy %q", opts.DiscoveryStrategy)
	}
	if opts.DiscoveryStrategy == DiscoveryProxy && opts.AcknowledgeProxyDispatch == nil {
		return nil, errors.New("toolinvocation: AcknowledgeProxyDispatch is required for proxy discovery")
	}
	if opts.CatalogCacheCapacity <= 0 {
		opts.CatalogCacheCapacity = defaultCatalogCacheCapacity
	}
	service := &Service{
		opts:    opts,
		tools:   map[string]*registeredTool{},
		cache:   newCatalogCache(opts.CatalogCacheCapacity),
		idem:    map[idempotencyKey]*idempotencyRecord{},
		lineage: map[lineageKey]*sync.Mutex{},
	}
	for _, reg := range service.discoveryRegistrations() {
		if err := service.Register(reg); err != nil {
			return nil, err
		}
	}
	for _, reg := range registrations {
		if err := service.Register(reg); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) Register(reg Registration) error {
	tool, err := compileRegistration(reg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[tool.def.ID]; exists {
		return fmt.Errorf("toolinvocation: duplicate tool id %q", tool.def.ID)
	}
	for _, existing := range s.tools {
		if existing.def.Name == tool.def.Name {
			return fmt.Errorf("toolinvocation: duplicate tool name %q", tool.def.Name)
		}
	}
	s.tools[tool.def.ID] = tool
	s.order = append(s.order, tool.def.ID)
	return nil
}

func (s *Service) Info() ContractInfo {
	return Info()
}

func (s *Service) ListTools(_ context.Context, req ToolCatalogRequest) (*ToolCatalogListed, *ToolCatalogFailure) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &ToolCatalogFailure{RequestID: req.ID, Code: FailureInvalidRequest, Message: "id is required"}
	}
	catalog := s.catalogFromIDs(s.initialToolIDs())
	s.cache.put(catalog)
	return &ToolCatalogListed{RequestID: req.ID, Catalog: catalog}, nil
}

func (s *Service) Execute(ctx context.Context, req ToolExecutionRequest) (*ToolExecutionResult, *ToolExecutionFailure) {
	if failure := validateExecutionRequest(req); failure != nil {
		return nil, failure
	}
	if hasCatalogChangingCall(req.Calls) && (strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.TurnID) == "") {
		return nil, requestFailure(req, FailureInvalidRequest, "catalog-changing calls require session_id and turn_id")
	}
	record, replay, failure := s.beginIdempotency(req)
	if failure != nil {
		return nil, failure
	}
	if replay {
		return cloneResult(record.result), cloneFailure(record.failure)
	}

	if hasCatalogChangingCall(req.Calls) {
		lock := s.lineageLock(req.SessionID, req.TurnID)
		lock.Lock()
		defer lock.Unlock()
	}

	result := s.executeFresh(ctx, req)
	s.finishIdempotency(record, result, nil)
	return cloneResult(result), nil
}

func (s *Service) executeFresh(ctx context.Context, req ToolExecutionRequest) *ToolExecutionResult {
	results := make([]ToolResult, 0, len(req.Calls))
	var transition *transitionBuilder
	for _, call := range req.Calls {
		if ctx.Err() != nil {
			results = append(results, cancelledBeforeStart(call))
			continue
		}
		tool, invalid := s.resolveCall(ctx, call)
		if invalid != nil {
			results = append(results, *invalid)
			continue
		}
		if tool.def.Name == "tool_load" {
			result, next := s.executeToolLoad(ctx, req, call, tool, transition)
			transition = next
			results = append(results, result)
			continue
		}
		if tool.def.Name == "tool_call" {
			results = append(results, s.executeToolCall(ctx, req, call, tool))
			continue
		}
		results = append(results, s.dispatchBackend(ctx, req.ID, call, tool))
	}

	out := &ToolExecutionResult{RequestID: req.ID, Results: results}
	if transition != nil && transition.changed {
		catalog := transition.catalog(s)
		s.cache.put(catalog)
		out.CatalogTransition = &ToolCatalogTransition{
			BaseCatalogID: req.CatalogID,
			Catalog:       catalog,
		}
	}
	return out
}

func (s *Service) executeToolLoad(ctx context.Context, req ToolExecutionRequest, call ToolCall, tool *registeredTool, transition *transitionBuilder) (ToolResult, *transitionBuilder) {
	if err := s.opts.AcknowledgeCallStarted(ctx, ToolCallStarted{RequestID: req.ID, CallID: call.ID, ToolID: tool.def.ID, Name: tool.def.Name}); err != nil {
		return failedResult(call, tool, FailureCallStartedUnacknowledged, "call_started was not acknowledged", true), transition
	}
	name, _ := call.Arguments["name"].(string)
	target := s.discoverableByName(name)
	if target == nil {
		return failedResult(call, tool, FailureUnknownTool, fmt.Sprintf("tool is not discoverable: %s", name), false), transition
	}
	if transition == nil {
		base, ok := s.cache.get(req.CatalogID)
		if !ok {
			return failedResult(call, tool, FailureCatalogUnavailable, "base catalog is unavailable", true), nil
		}
		transition = s.transitionFromBase(base)
	}
	transition.add(target)
	return successResult(call, tool, fmt.Sprintf("Loaded tool %q for the next model invocation.", name), SideEffectNone), transition
}

func (s *Service) executeToolCall(ctx context.Context, req ToolExecutionRequest, call ToolCall, proxy *registeredTool) ToolResult {
	targetName, ok := call.Arguments["name"].(string)
	targetName = strings.TrimSpace(targetName)
	if !ok || targetName == "" {
		return failedResult(call, proxy, FailureInvalidArguments, `argument "name" must be string`, false)
	}
	target := s.discoverableByName(targetName)
	attempt := ToolProxyDispatchAttempted{
		RequestID:           req.ID,
		CallID:              call.ID,
		ProxyToolID:         proxy.def.ID,
		RequestedTargetName: targetName,
	}
	if target != nil {
		attempt.EffectiveToolID = target.def.ID
		attempt.EffectiveDefinitionRevision = target.def.Revision
	}
	if err := s.opts.AcknowledgeProxyDispatch(ctx, attempt); err != nil {
		if target != nil {
			return failedResult(call, target, FailureProxyDispatchUnrecorded, "proxy dispatch attempt was not recorded", true)
		}
		result := *callFailure(call, FailureProxyDispatchUnrecorded, "proxy dispatch attempt was not recorded", true)
		result.Name = targetName
		return result
	}
	if err := proxy.schema.validate(call.Arguments); err != nil {
		return proxyFailure(call, target, targetName, FailureInvalidArguments, err.Error(), false)
	}
	if target == nil {
		result := *callFailure(call, FailureUnknownTool, fmt.Sprintf("tool is not discoverable: %s", targetName), false)
		result.Name = targetName
		return result
	}
	nested, ok := call.Arguments["arguments"].(map[string]any)
	if !ok {
		return failedResult(call, target, FailureInvalidArguments, `argument "arguments" must be object`, false)
	}
	if err := target.schema.validate(nested); err != nil {
		return failedResult(call, target, FailureInvalidArguments, err.Error(), false)
	}
	if !target.runtimeAvailableNow(ctx) {
		return failedResult(call, target, FailureToolUnavailable, "tool is not currently available", true)
	}
	effectiveCall := call
	effectiveCall.ToolID = target.def.ID
	effectiveCall.DefinitionRevision = target.def.Revision
	effectiveCall.Name = target.def.Name
	effectiveCall.Arguments = nested
	return s.dispatchBackend(ctx, req.ID, effectiveCall, target)
}

func proxyFailure(call ToolCall, target *registeredTool, targetName, code, text string, retryable bool) ToolResult {
	if target != nil {
		return failedResult(call, target, code, text, retryable)
	}
	result := *callFailure(call, code, text, retryable)
	result.Name = targetName
	return result
}

func (s *Service) dispatchBackend(ctx context.Context, requestID string, call ToolCall, tool *registeredTool) (out ToolResult) {
	if err := s.opts.AcknowledgeCallStarted(ctx, ToolCallStarted{RequestID: requestID, CallID: call.ID, ToolID: tool.def.ID, Name: tool.def.Name}); err != nil {
		return failedResult(call, tool, FailureCallStartedUnacknowledged, "call_started was not acknowledged", true)
	}
	defer func() {
		if recover() != nil {
			out = failedResult(call, tool, FailureBackendFailed, "backend panicked", true)
			out.SideEffect = SideEffectUnknown
		}
	}()
	result := tool.backend(ctx, BackendRequest{Call: call, Tool: tool.def, Arguments: call.Arguments})
	return normalizeBackendResult(call, tool, result)
}

func (s *Service) resolveCall(ctx context.Context, call ToolCall) (*registeredTool, *ToolResult) {
	if (call.ToolID == "") != (call.DefinitionRevision == "") {
		return nil, callFailure(call, FailureInvalidArguments, "tool_id and definition_revision must be supplied together", false)
	}
	if call.ToolID == "" {
		return nil, callFailure(call, FailureUnknownTool, "tool identity is missing", false)
	}
	s.mu.RLock()
	tool := s.tools[call.ToolID]
	s.mu.RUnlock()
	if tool == nil {
		return nil, callFailure(call, FailureUnknownTool, "tool is not currently registered", false)
	}
	if tool.def.Name != call.Name {
		return nil, callFailure(call, FailureUnknownTool, "tool name does not match current registration", false)
	}
	if tool.def.Revision != call.DefinitionRevision {
		result := failedResult(call, tool, FailureStaleToolDefinition, "tool definition revision is stale", true)
		return nil, &result
	}
	if !tool.runtimeAvailableNow(ctx) {
		result := failedResult(call, tool, FailureToolUnavailable, "tool is not currently available", true)
		return nil, &result
	}
	if tool.def.Name == "tool_call" {
		return tool, nil
	}
	if err := tool.schema.validate(call.Arguments); err != nil {
		result := failedResult(call, tool, FailureInvalidArguments, err.Error(), false)
		return nil, &result
	}
	return tool, nil
}

func (s *Service) initialToolIDs() []string {
	var ids []string
	for _, tool := range s.orderedTools() {
		if tool.initiallyVisible {
			ids = append(ids, tool.def.ID)
		}
	}
	return ids
}

func (s *Service) catalogFromIDs(ids []string) ToolCatalog {
	tools := make([]ToolDefinition, 0, len(ids))
	for _, id := range ids {
		s.mu.RLock()
		tool := s.tools[id]
		s.mu.RUnlock()
		if tool == nil {
			continue
		}
		tools = append(tools, cloneDefinition(tool.def))
	}
	catalog := ToolCatalog{Tools: tools}
	catalog.ID = catalogID(catalog.Tools)
	return catalog
}

func (s *Service) discoverableByName(name string) *registeredTool {
	for _, tool := range s.orderedTools() {
		if tool.discoverable && tool.def.Name == name {
			return tool
		}
	}
	return nil
}

func (s *Service) discoverableTools() []*registeredTool {
	var out []*registeredTool
	for _, tool := range s.orderedTools() {
		if tool.discoverable {
			out = append(out, tool)
		}
	}
	return out
}

func (s *Service) orderedTools() []*registeredTool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]*registeredTool, 0, len(s.order))
	for _, id := range s.order {
		tools = append(tools, s.tools[id])
	}
	return tools
}

func (s *Service) transitionFromBase(base ToolCatalog) *transitionBuilder {
	builder := &transitionBuilder{base: base, byID: map[string]bool{}}
	for _, def := range base.Tools {
		s.mu.RLock()
		tool := s.tools[def.ID]
		s.mu.RUnlock()
		if tool == nil {
			builder.changed = true
			continue
		}
		if tool.def.Revision != def.Revision || tool.def.Name != def.Name {
			builder.changed = true
		}
		builder.ids = append(builder.ids, tool.def.ID)
		builder.byID[tool.def.ID] = true
	}
	return builder
}

func (s *Service) lineageLock(sessionID, turnID string) *sync.Mutex {
	key := lineageKey{sessionID: sessionID, turnID: turnID}
	s.lineageMu.Lock()
	defer s.lineageMu.Unlock()
	lock := s.lineage[key]
	if lock == nil {
		// ponytail: locks are retained for process lifetime; add cleanup if session volume matters.
		lock = &sync.Mutex{}
		s.lineage[key] = lock
	}
	return lock
}

type lineageKey struct {
	sessionID string
	turnID    string
}

func (s *Service) discoveryRegistrations() []Registration {
	schema := func(value string) json.RawMessage { return json.RawMessage(value) }
	regs := []Registration{
		{
			Provider: discoveryProvider, ProviderVersion: discoveryProviderVersion, LocalName: "tool_search",
			Name: "tool_search", Description: "Search currently discoverable tools.",
			InputSchema:      schema(`{"additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"}`),
			InitiallyVisible: true, Backend: s.toolSearch,
		},
		{
			Provider: discoveryProvider, ProviderVersion: discoveryProviderVersion, LocalName: "tool_describe",
			Name: "tool_describe", Description: "Describe one currently discoverable tool.",
			InputSchema:      schema(`{"additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}`),
			InitiallyVisible: true, Backend: s.toolDescribe,
		},
	}
	if s.opts.DiscoveryStrategy == DiscoveryProxy {
		return append(regs, Registration{
			Provider: discoveryProvider, ProviderVersion: discoveryProviderVersion, LocalName: "tool_call",
			Name: "tool_call", Description: "Call one currently discoverable tool by name. Use name for the target tool and arguments for that tool's argument object.",
			InputSchema:      schema(`{"additionalProperties":false,"properties":{"arguments":{"type":"object"},"name":{"type":"string"}},"required":["name","arguments"],"type":"object"}`),
			InitiallyVisible: true, Backend: func(context.Context, BackendRequest) BackendResult {
				return BackendResult{Status: ResultSucceeded, Text: "proxy call accepted", SideEffect: SideEffectNone}
			},
		})
	}
	return append(regs,
		Registration{
			Provider: discoveryProvider, ProviderVersion: discoveryProviderVersion, LocalName: "tool_load",
			Name: "tool_load", Description: "Load one discoverable tool into the next model-facing catalog.",
			InputSchema:      schema(`{"additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}`),
			InitiallyVisible: true, Backend: func(context.Context, BackendRequest) BackendResult {
				return BackendResult{Status: ResultSucceeded, Text: "tool load accepted", SideEffect: SideEffectNone}
			},
		},
	)
}

func (s *Service) toolSearch(_ context.Context, req BackendRequest) BackendResult {
	query := strings.ToLower(strings.TrimSpace(req.Arguments["query"].(string)))
	var lines []string
	for _, tool := range s.discoverableTools() {
		haystack := strings.ToLower(tool.def.Name + " " + tool.def.Description)
		if query == "" || strings.Contains(haystack, query) {
			lines = append(lines, fmt.Sprintf("- %s: %s", tool.def.Name, tool.def.Description))
		}
	}
	if len(lines) == 0 {
		return BackendResult{Text: "No discoverable tools matched.", SideEffect: SideEffectNone}
	}
	return BackendResult{Text: strings.Join(lines, "\n"), SideEffect: SideEffectNone}
}

func (s *Service) toolDescribe(_ context.Context, req BackendRequest) BackendResult {
	name := req.Arguments["name"].(string)
	tool := s.discoverableByName(name)
	if tool == nil {
		return BackendResult{
			Status:     ResultFailed,
			Text:       fmt.Sprintf("Tool %q is not discoverable.", name),
			SideEffect: SideEffectNone,
			Failure:    &ToolFailure{Code: FailureUnknownTool},
		}
	}
	return BackendResult{
		Text:       fmt.Sprintf("name: %s\ndescription: %s\ninput_schema: %s", tool.def.Name, tool.def.Description, string(tool.def.InputSchema)),
		SideEffect: SideEffectNone,
	}
}

func compileRegistration(reg Registration) (*registeredTool, error) {
	components := map[string]string{
		"provider":         strings.TrimSpace(reg.Provider),
		"provider_version": strings.TrimSpace(reg.ProviderVersion),
		"local_name":       strings.TrimSpace(reg.LocalName),
	}
	for label, value := range components {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("toolinvocation: %s is required", label)
		}
		if strings.Contains(value, ":") {
			return nil, fmt.Errorf("toolinvocation: %s must not contain ':'", label)
		}
	}
	name := strings.TrimSpace(reg.Name)
	if name == "" {
		return nil, errors.New("toolinvocation: tool name is required")
	}
	description := strings.TrimSpace(reg.Description)
	if description == "" {
		return nil, fmt.Errorf("toolinvocation: description is required for %s", name)
	}
	if reg.Backend == nil {
		return nil, fmt.Errorf("toolinvocation: backend is required for %s", name)
	}
	schema, canonicalSchema, err := compileSchema(reg.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("toolinvocation: invalid schema for %s: %w", name, err)
	}
	id := components["provider"] + ":" + components["provider_version"] + ":" + components["local_name"]
	def := ToolDefinition{
		ID:          id,
		Name:        name,
		Description: description,
		InputSchema: canonicalSchema,
	}
	def.Revision = definitionRevision(def)
	return &registeredTool{
		def:              def,
		schema:           schema,
		initiallyVisible: reg.InitiallyVisible,
		discoverable:     reg.Discoverable,
		runtimeAvailable: reg.RuntimeAvailable,
		backend:          reg.Backend,
	}, nil
}

func (t *registeredTool) runtimeAvailableNow(ctx context.Context) bool {
	return t != nil && (t.runtimeAvailable == nil || t.runtimeAvailable(ctx))
}

type transitionBuilder struct {
	base    ToolCatalog
	ids     []string
	byID    map[string]bool
	changed bool
}

func (b *transitionBuilder) add(tool *registeredTool) {
	if b.byID[tool.def.ID] {
		return
	}
	b.ids = append(b.ids, tool.def.ID)
	b.byID[tool.def.ID] = true
	b.changed = true
}

func (b *transitionBuilder) catalog(s *Service) ToolCatalog {
	return s.catalogFromIDs(b.ids)
}

type idempotencyKey struct {
	sessionID string
	key       string
}

type idempotencyRecord struct {
	fingerprint string
	done        chan struct{}
	result      *ToolExecutionResult
	failure     *ToolExecutionFailure
}

func (s *Service) beginIdempotency(req ToolExecutionRequest) (*idempotencyRecord, bool, *ToolExecutionFailure) {
	fingerprint, err := fingerprint(req)
	if err != nil {
		return nil, false, requestFailure(req, FailureInvalidRequest, err.Error())
	}
	key := idempotencyKey{sessionID: req.SessionID, key: req.IdempotencyKey}

	s.idemMu.Lock()
	existing := s.idem[key]
	if existing == nil {
		record := &idempotencyRecord{fingerprint: fingerprint, done: make(chan struct{})}
		s.idem[key] = record
		s.idemMu.Unlock()
		return record, false, nil
	}
	if existing.fingerprint != fingerprint {
		s.idemMu.Unlock()
		return nil, false, requestFailure(req, FailureIdempotencyConflict, "idempotency key was reused with a different payload")
	}
	s.idemMu.Unlock()
	<-existing.done
	return existing, true, nil
}

func (s *Service) finishIdempotency(record *idempotencyRecord, result *ToolExecutionResult, failure *ToolExecutionFailure) {
	record.result = cloneResult(result)
	record.failure = cloneFailure(failure)
	close(record.done)
}

func validateExecutionRequest(req ToolExecutionRequest) *ToolExecutionFailure {
	if strings.TrimSpace(req.ID) == "" {
		return requestFailure(req, FailureInvalidRequest, "id is required")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return requestFailure(req, FailureMissingIdempotencyKey, "idempotency_key is required")
	}
	if strings.TrimSpace(req.CatalogID) == "" {
		return requestFailure(req, FailureInvalidRequest, "catalog_id is required")
	}
	if req.Mode != "" && req.Mode != ExecutionSequential && req.Mode != ExecutionAllowParallel {
		return requestFailure(req, FailureInvalidRequest, "unsupported execution mode")
	}
	if len(req.Calls) == 0 {
		return requestFailure(req, FailureInvalidRequest, "calls are required")
	}
	seen := map[string]bool{}
	for _, call := range req.Calls {
		if strings.TrimSpace(call.ID) == "" {
			return requestFailure(req, FailureInvalidRequest, "call id is required")
		}
		if seen[call.ID] {
			return requestFailure(req, FailureDuplicateCallID, "duplicate call id")
		}
		seen[call.ID] = true
	}
	return nil
}

func hasCatalogChangingCall(calls []ToolCall) bool {
	for _, call := range calls {
		if call.ToolID == toolLoadID {
			return true
		}
	}
	return false
}

func requestFailure(req ToolExecutionRequest, code, message string) *ToolExecutionFailure {
	ids := make([]string, 0, len(req.Calls))
	for _, call := range req.Calls {
		if call.ID != "" {
			ids = append(ids, call.ID)
		}
	}
	return &ToolExecutionFailure{
		RequestID:         req.ID,
		Code:              code,
		Message:           message,
		Retryable:         code != FailureInvalidRequest && code != FailureDuplicateCallID && code != FailureMissingIdempotencyKey && code != FailureIdempotencyConflict,
		Results:           []ToolResult{},
		UnresolvedCallIDs: ids,
	}
}

func cancelledBeforeStart(call ToolCall) ToolResult {
	return ToolResult{
		CallID:       call.ID,
		Name:         call.Name,
		Status:       ResultCancelled,
		Text:         "Call was cancelled before it started.",
		Refs:         []session.ContextRef{},
		TouchedPaths: []contextprovider.TouchedPath{},
		SideEffect:   SideEffectNone,
		Failure:      &ToolFailure{Code: FailureCancelled, Retryable: true},
	}
}

func callFailure(call ToolCall, code, text string, retryable bool) *ToolResult {
	return &ToolResult{
		CallID:       call.ID,
		Name:         call.Name,
		Status:       ResultFailed,
		Text:         text,
		Refs:         []session.ContextRef{},
		TouchedPaths: []contextprovider.TouchedPath{},
		SideEffect:   SideEffectNone,
		Failure:      &ToolFailure{Code: code, Retryable: retryable},
	}
}

func failedResult(call ToolCall, tool *registeredTool, code, text string, retryable bool) ToolResult {
	return ToolResult{
		CallID:       call.ID,
		ToolID:       tool.def.ID,
		Name:         tool.def.Name,
		Status:       ResultFailed,
		Text:         text,
		Refs:         []session.ContextRef{},
		TouchedPaths: []contextprovider.TouchedPath{},
		SideEffect:   SideEffectNone,
		Failure:      &ToolFailure{Code: code, Retryable: retryable},
	}
}

func successResult(call ToolCall, tool *registeredTool, text string, sideEffect ToolSideEffect) ToolResult {
	return ToolResult{
		CallID:       call.ID,
		ToolID:       tool.def.ID,
		Name:         tool.def.Name,
		Status:       ResultSucceeded,
		Text:         text,
		Refs:         []session.ContextRef{},
		TouchedPaths: []contextprovider.TouchedPath{},
		SideEffect:   sideEffect,
	}
}

func normalizeBackendResult(call ToolCall, tool *registeredTool, result BackendResult) ToolResult {
	status := result.Status
	if status == "" {
		status = ResultSucceeded
	}
	if !validResultStatus(status) {
		return malformedBackendResult(call, tool, result, SideEffectUnknown, "backend returned an unsupported status")
	}
	sideEffect := result.SideEffect
	if sideEffect == "" {
		sideEffect = SideEffectNone
	}
	if !validSideEffect(sideEffect) {
		return malformedBackendResult(call, tool, result, SideEffectUnknown, "backend returned an unsupported side-effect value")
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return malformedBackendResult(call, tool, result, sideEffect, "backend returned empty model-facing text")
	}
	if status == ResultSucceeded && result.Failure != nil {
		return malformedBackendResult(call, tool, result, sideEffect, "successful backend result included a failure")
	}
	if result.Failure != nil && strings.TrimSpace(result.Failure.Code) == "" {
		return malformedBackendResult(call, tool, result, sideEffect, "backend failure code is empty")
	}
	out := ToolResult{
		CallID:        call.ID,
		ToolID:        tool.def.ID,
		Name:          tool.def.Name,
		Status:        status,
		Text:          text,
		Refs:          result.Refs,
		TouchedPaths:  result.TouchedPaths,
		SideEffect:    sideEffect,
		StopRequested: result.StopRequested,
		Failure:       result.Failure,
	}
	if out.Refs == nil {
		out.Refs = []session.ContextRef{}
	}
	if out.TouchedPaths == nil {
		out.TouchedPaths = []contextprovider.TouchedPath{}
	}
	if out.Status != ResultSucceeded && out.Failure == nil {
		switch out.Status {
		case ResultFailed:
			out.Failure = &ToolFailure{Code: FailureBackendFailed, Retryable: true}
		case ResultCancelled:
			out.Failure = &ToolFailure{Code: FailureCancelled, Retryable: true}
		case ResultTimedOut:
			out.Failure = &ToolFailure{Code: FailureTimedOut, Retryable: true}
		case ResultUnknown:
			out.Failure = &ToolFailure{Code: FailureOutcomeUnknown, Retryable: false}
		default:
			return malformedBackendResult(call, tool, result, sideEffect, "non-successful backend result omitted its failure")
		}
	}
	if _, err := json.Marshal(out); err != nil {
		return malformedBackendResult(call, tool, BackendResult{}, sideEffect, "backend returned invalid structured evidence")
	}
	return out
}

func validResultStatus(status ToolResultStatus) bool {
	switch status {
	case ResultSucceeded, ResultFailed, ResultDenied, ResultCancelled, ResultTimedOut, ResultUnknown:
		return true
	default:
		return false
	}
}

func validSideEffect(sideEffect ToolSideEffect) bool {
	switch sideEffect {
	case SideEffectNone, SideEffectApplied, SideEffectPartial, SideEffectUnknown:
		return true
	default:
		return false
	}
}

func malformedBackendResult(call ToolCall, tool *registeredTool, result BackendResult, sideEffect ToolSideEffect, text string) ToolResult {
	out := failedResult(call, tool, FailureMalformedResult, text, true)
	out.Refs = result.Refs
	out.TouchedPaths = result.TouchedPaths
	out.SideEffect = sideEffect
	if out.Refs == nil {
		out.Refs = []session.ContextRef{}
	}
	if out.TouchedPaths == nil {
		out.TouchedPaths = []contextprovider.TouchedPath{}
	}
	return out
}

type catalogCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]ToolCatalog
	order    []string
}

func newCatalogCache(capacity int) *catalogCache {
	if capacity < 1 {
		capacity = 1
	}
	return &catalogCache{capacity: capacity, items: map[string]ToolCatalog{}}
}

func (c *catalogCache) put(catalog ToolCatalog) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[catalog.ID]; !exists {
		c.order = append(c.order, catalog.ID)
	}
	c.items[catalog.ID] = cloneCatalog(catalog)
	for len(c.order) > c.capacity {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.items, evict)
	}
}

func (c *catalogCache) get(id string) (ToolCatalog, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	catalog, ok := c.items[id]
	return cloneCatalog(catalog), ok
}

type compiledSchema struct {
	root schemaNode
}

type schemaNode struct {
	typ                  string
	properties           map[string]schemaNode
	required             map[string]bool
	allowAdditionalProps bool
	items                *schemaNode
}

func compileSchema(raw json.RawMessage) (compiledSchema, json.RawMessage, error) {
	var root map[string]any
	if err := decodeJSON(raw, &root); err != nil {
		return compiledSchema{}, nil, err
	}
	node, err := compileSchemaNode(root, "schema")
	if err != nil {
		return compiledSchema{}, nil, err
	}
	if node.typ != "object" {
		return compiledSchema{}, nil, errors.New("top-level schema type must be object")
	}
	canonical, err := canonicalJSON(root)
	if err != nil {
		return compiledSchema{}, nil, err
	}
	return compiledSchema{root: node}, canonical, nil
}

func compileSchemaNode(raw map[string]any, path string) (schemaNode, error) {
	for _, key := range sortedKeys(raw) {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items":
		default:
			return schemaNode{}, fmt.Errorf("unsupported schema keyword %q at %s", key, path)
		}
	}
	typ, ok := raw["type"].(string)
	if !ok || !supportedJSONType(typ) {
		return schemaNode{}, fmt.Errorf("unsupported schema type at %s", path)
	}
	node := schemaNode{
		typ:                  typ,
		properties:           map[string]schemaNode{},
		required:             map[string]bool{},
		allowAdditionalProps: true,
	}

	switch typ {
	case "object":
		if value, ok := raw["additionalProperties"]; ok {
			allowed, ok := value.(bool)
			if !ok {
				return schemaNode{}, fmt.Errorf("additionalProperties must be boolean at %s", path)
			}
			node.allowAdditionalProps = allowed
		}
		if _, ok := raw["items"]; ok {
			return schemaNode{}, fmt.Errorf("items is only supported for array schemas at %s", path)
		}
		if props, ok := raw["properties"]; ok {
			propsMap, ok := props.(map[string]any)
			if !ok {
				return schemaNode{}, fmt.Errorf("properties must be an object at %s", path)
			}
			for _, name := range sortedKeys(propsMap) {
				value := propsMap[name]
				prop, ok := value.(map[string]any)
				if !ok {
					return schemaNode{}, fmt.Errorf("property %q schema must be an object", name)
				}
				child, err := compileSchemaNode(prop, path+"."+name)
				if err != nil {
					return schemaNode{}, err
				}
				node.properties[name] = child
			}
		}
		if required, ok := raw["required"]; ok {
			items, ok := required.([]any)
			if !ok {
				return schemaNode{}, fmt.Errorf("required must be an array at %s", path)
			}
			for _, item := range items {
				name, ok := item.(string)
				if !ok {
					return schemaNode{}, fmt.Errorf("required entries must be strings at %s", path)
				}
				if _, ok := node.properties[name]; !ok {
					return schemaNode{}, fmt.Errorf("required property %q is not defined at %s", name, path)
				}
				node.required[name] = true
			}
		}
	case "array":
		_, hasProperties := raw["properties"]
		_, hasRequired := raw["required"]
		_, hasAdditionalProperties := raw["additionalProperties"]
		if hasProperties || hasRequired || hasAdditionalProperties {
			return schemaNode{}, fmt.Errorf("object keywords are not supported for array schemas at %s", path)
		}
		items, ok := raw["items"]
		if !ok {
			return schemaNode{}, fmt.Errorf("array schema at %s requires items", path)
		}
		itemMap, ok := items.(map[string]any)
		if !ok {
			return schemaNode{}, fmt.Errorf("array items at %s must be a schema object", path)
		}
		itemNode, err := compileSchemaNode(itemMap, path+"[]")
		if err != nil {
			return schemaNode{}, err
		}
		node.items = &itemNode
	default:
		_, hasProperties := raw["properties"]
		_, hasRequired := raw["required"]
		_, hasAdditionalProperties := raw["additionalProperties"]
		_, hasItems := raw["items"]
		if hasProperties || hasRequired || hasAdditionalProperties || hasItems {
			return schemaNode{}, fmt.Errorf("only type is supported for %s schema at %s", typ, path)
		}
	}
	return node, nil
}

func (s compiledSchema) validate(args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	return s.root.validate(args, "")
}

func (s schemaNode) validate(value any, path string) error {
	if !matchesJSONType(value, s.typ) {
		return fmt.Errorf("argument %q must be %s", path, s.typ)
	}
	switch s.typ {
	case "object":
		args := value.(map[string]any)
		for _, name := range sortedKeys(s.required) {
			childPath := joinArgumentPath(path, name)
			if _, ok := args[name]; !ok {
				return fmt.Errorf("missing required argument %q", childPath)
			}
		}
		for _, name := range sortedKeys(args) {
			childValue := args[name]
			child, ok := s.properties[name]
			childPath := joinArgumentPath(path, name)
			if !ok {
				if s.allowAdditionalProps {
					continue
				}
				return fmt.Errorf("unknown argument %q", childPath)
			}
			if err := child.validate(childValue, childPath); err != nil {
				return err
			}
		}
	case "array":
		items := value.([]any)
		for i, item := range items {
			if err := s.items.validate(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func joinArgumentPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func supportedJSONType(typ string) bool {
	switch typ {
	case "string", "number", "integer", "boolean", "object", "array":
		return true
	default:
		return false
	}
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		return isNumber(value)
	case "integer":
		n, ok := numberValue(value)
		return ok && math.Trunc(n) == n
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func isNumber(value any) bool {
	_, ok := numberValue(value)
	return ok
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func definitionRevision(def ToolDefinition) string {
	canonical, _ := canonicalJSON(map[string]any{
		"name":         def.Name,
		"description":  def.Description,
		"input_schema": json.RawMessage(def.InputSchema),
	})
	return stableID("tooldef", canonical)
}

func catalogID(defs []ToolDefinition) string {
	canonical, _ := canonicalJSON(defs)
	return stableID("toolcat", canonical)
}

func stableID(prefix string, raw []byte) string {
	sum := sha256.Sum256(raw)
	return prefix + "_" + hex.EncodeToString(sum[:])
}

func fingerprint(value any) (string, error) {
	canonical, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	return stableID("idem", canonical), nil
}

func canonicalJSON(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := decodeJSON(raw, &normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func decodeJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func cloneDefinition(def ToolDefinition) ToolDefinition {
	def.InputSchema = append(json.RawMessage(nil), def.InputSchema...)
	return def
}

func cloneCatalog(catalog ToolCatalog) ToolCatalog {
	out := ToolCatalog{ID: catalog.ID, Tools: make([]ToolDefinition, len(catalog.Tools))}
	for i, def := range catalog.Tools {
		out.Tools[i] = cloneDefinition(def)
	}
	return out
}

func cloneResult(in *ToolExecutionResult) *ToolExecutionResult {
	if in == nil {
		return nil
	}
	var out ToolExecutionResult
	raw, _ := json.Marshal(in)
	_ = json.Unmarshal(raw, &out)
	return &out
}

func cloneFailure(in *ToolExecutionFailure) *ToolExecutionFailure {
	if in == nil {
		return nil
	}
	var out ToolExecutionFailure
	raw, _ := json.Marshal(in)
	_ = json.Unmarshal(raw, &out)
	return &out
}
