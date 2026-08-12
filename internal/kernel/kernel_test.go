package kernel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// --- Fake capability implementations ---

type fakeSession struct {
	created   *session.Session
	createErr error
	getErr    error
	writeErr  error

	writeMessages    []session.WriteMessageInput
	writeToolCalls   []session.WriteToolCallInput
	writeToolResults []session.WriteToolResultInput
	writeSystemNotes []session.WriteSystemNoteInput
	writeRecords     []session.WriteRecordInput
	setMetadatas     []session.SetMetadataInput
	setUsages        []session.SetUsageInput
	deletes          []session.DeleteInput
}

func (f *fakeSession) Create(ctx context.Context, input session.CreateInput) (*session.CreateResult, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created == nil {
		f.created = newTestSession("sess1")
	}
	return &session.CreateResult{
		ID:      f.created.ID,
		Version: f.created.Version,
		State:   f.created.State,
	}, nil
}

func (f *fakeSession) Get(ctx context.Context, input session.GetInput) (*session.Session, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.created == nil {
		return nil, session.ErrNotFound
	}
	return f.created, nil
}

func (f *fakeSession) Delete(ctx context.Context, input session.DeleteInput) (*session.DeleteResult, error) {
	f.deletes = append(f.deletes, input)
	if f.created == nil {
		return nil, session.ErrNotFound
	}
	return &session.DeleteResult{
		ID:      f.created.ID,
		Version: f.created.Version,
		State:   session.SessionDeleted,
	}, nil
}

func (f *fakeSession) WriteMessage(ctx context.Context, input session.WriteMessageInput) (*session.WriteResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.writeMessages = append(f.writeMessages, input)
	return &session.WriteResult{ID: input.SessionID, RecordID: "rec_new", Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) WriteToolCall(ctx context.Context, input session.WriteToolCallInput) (*session.WriteResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.writeToolCalls = append(f.writeToolCalls, input)
	return &session.WriteResult{ID: input.SessionID, RecordID: "rec_new", Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) WriteToolResult(ctx context.Context, input session.WriteToolResultInput) (*session.WriteResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.writeToolResults = append(f.writeToolResults, input)
	return &session.WriteResult{ID: input.SessionID, RecordID: "rec_new", Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) WriteSystemNote(ctx context.Context, input session.WriteSystemNoteInput) (*session.WriteResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.writeSystemNotes = append(f.writeSystemNotes, input)
	return &session.WriteResult{ID: input.SessionID, RecordID: "rec_new", Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) WriteRecord(ctx context.Context, input session.WriteRecordInput) (*session.WriteResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.writeRecords = append(f.writeRecords, input)
	return &session.WriteResult{ID: input.SessionID, RecordID: input.Record.ID, Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) SetMetadata(ctx context.Context, input session.SetMetadataInput) (*session.SetResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.setMetadatas = append(f.setMetadatas, input)
	if f.created != nil {
		f.created.Metadata = input.Metadata
	}
	return &session.SetResult{ID: input.SessionID, Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) SetUsage(ctx context.Context, input session.SetUsageInput) (*session.SetResult, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.setUsages = append(f.setUsages, input)
	return &session.SetResult{ID: input.SessionID, Version: f.version(), State: session.SessionActive}, nil
}

func (f *fakeSession) version() int64 {
	if f.created == nil {
		return 1
	}
	return f.created.Version + 1
}

type fakeTools struct {
	catalog     toolinvocation.ToolCatalog
	listErr     *toolinvocation.ToolCatalogFailure
	executeRes  *toolinvocation.ToolExecutionResult
	executeErr  *toolinvocation.ToolExecutionFailure
}

func (f *fakeTools) ListTools(ctx context.Context, req toolinvocation.ToolCatalogRequest) (*toolinvocation.ToolCatalogListed, *toolinvocation.ToolCatalogFailure) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &toolinvocation.ToolCatalogListed{Catalog: f.catalog}, nil
}

func (f *fakeTools) Execute(ctx context.Context, req toolinvocation.ToolExecutionRequest) (*toolinvocation.ToolExecutionResult, *toolinvocation.ToolExecutionFailure) {
	if f.executeErr != nil {
		return nil, f.executeErr
	}
	return f.executeRes, nil
}

type fakeModel struct {
	result  *modelinvocation.ModelInvocationResult
	failure *modelinvocation.ModelInvocationFailure
}

func (f *fakeModel) Invoke(ctx context.Context, req modelinvocation.ModelInvocationRequest) (*modelinvocation.ModelInvocationResult, *modelinvocation.ModelInvocationFailure) {
	return f.result, f.failure
}

type fakeBuilder struct {
	estimateAlloc contextbuilder.Allocation
	estimateErr   error
	assemblePrefix contextbuilder.BuiltPrefix
	assembleErr    error
	prepareCtx    contextbuilder.BuiltContext
	prepareErr    error
}

func (f *fakeBuilder) Estimate(req contextbuilder.EstimateRequest) (contextbuilder.Allocation, error) {
	return f.estimateAlloc, f.estimateErr
}

func (f *fakeBuilder) Assemble(req contextbuilder.AssembleRequest) (contextbuilder.BuiltPrefix, error) {
	return f.assemblePrefix, f.assembleErr
}

func (f *fakeBuilder) Prepare(req contextbuilder.PrepareRequest) (contextbuilder.BuiltContext, error) {
	return f.prepareCtx, f.prepareErr
}

type fakeContextProvider struct {
	initBundle  *contextprovider.ContextBundle
	initFailure *contextprovider.ContextFailure
	getBundle   *contextprovider.ContextBundle
	getFailure  *contextprovider.ContextFailure
}

func (f *fakeContextProvider) Initialize(ctx context.Context, req contextprovider.ContextInitializeRequest) (*contextprovider.ContextBundle, *contextprovider.ContextFailure) {
	return f.initBundle, f.initFailure
}

func (f *fakeContextProvider) GetContext(ctx context.Context, req contextprovider.ContextRequest) (*contextprovider.ContextBundle, *contextprovider.ContextFailure) {
	return f.getBundle, f.getFailure
}

// noopObserver is a TurnObserver that records what it sees.
type noopObserver struct {
	contents   []string
	reasonings []string
	toolStarts []string
	toolResults []toolinvocation.ToolResult
	turnEndReason ExitReason
	turnEndContent string
}

func (o *noopObserver) OnModelContent(delta string)      { o.contents = append(o.contents, delta) }
func (o *noopObserver) OnReasoning(delta string)          { o.reasonings = append(o.reasonings, delta) }
func (o *noopObserver) OnToolCallStart(name string, args map[string]any) { o.toolStarts = append(o.toolStarts, name) }
func (o *noopObserver) OnToolResult(result toolinvocation.ToolResult)    { o.toolResults = append(o.toolResults, result) }
func (o *noopObserver) OnTurnEnd(exitReason ExitReason, finalContent string) {
	o.turnEndReason = exitReason
	o.turnEndContent = finalContent
}

// --- Test helpers ---

func newTestSession(id string) *session.Session {
	return &session.Session{
		ID:      id,
		State:   session.SessionActive,
		Version: 1,
		Records: []session.SessionRecord{{
			ID:   "rec1",
			Seq:  1,
			Kind: session.RecordMessage,
			Role: "user",
			Text: strPtr("hello"),
		}},
		Metadata: session.SessionMetadata{
			Custom: make(map[string]json.RawMessage),
		},
	}
}

func strPtr(s string) *string { return &s }

func newTestKernel() (*Kernel, *fakeSession, *fakeTools, *fakeModel, *fakeBuilder, *fakeContextProvider) {
	fs := &fakeSession{}
	ft := &fakeTools{}
	fm := &fakeModel{}
	fb := &fakeBuilder{}
	fc := &fakeContextProvider{}
	k := New(DefaultConfig(), ft, fm, fs, fb, fc)
	return k, fs, ft, fm, fb, fc
}

func completeTurnResult(content string) *modelinvocation.ModelInvocationResult {
	return &modelinvocation.ModelInvocationResult{
		RequestID:  "inv1",
		Content:    content,
		StopReason: modelinvocation.StopEndTurn,
	}
}

// --- Tests ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.TurnBudget != 90 {
		t.Errorf("TurnBudget = %d, want 90", cfg.TurnBudget)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.MaxOutputRetries != 2 {
		t.Errorf("MaxOutputRetries = %d, want 2", cfg.MaxOutputRetries)
	}
	if cfg.OutputBudgetRaise != 0.2 {
		t.Errorf("OutputBudgetRaise = %f, want 0.2", cfg.OutputBudgetRaise)
	}
	if cfg.CancelDrainTimeout != 1e9 { // 1 second in nanoseconds
		t.Errorf("CancelDrainTimeout = %v, want 1s", cfg.CancelDrainTimeout)
	}
}

func TestSessionBudgetExceeded(t *testing.T) {
	cfg := Config{SessionTokenLimit: 100}
	if sessionBudgetExceeded(cfg, session.SessionUsage{TotalInputTokens: 50, TotalOutputTokens: 49}) {
		t.Error("budget should not be exceeded at 99 tokens")
	}
	if !sessionBudgetExceeded(cfg, session.SessionUsage{TotalInputTokens: 50, TotalOutputTokens: 51}) {
		t.Error("budget should be exceeded at 101 tokens")
	}
	cfgZero := Config{SessionTokenLimit: 0}
	if sessionBudgetExceeded(cfgZero, session.SessionUsage{TotalInputTokens: 999999}) {
		t.Error("zero limit should never be exceeded")
	}
}

func TestModelCallRetryable(t *testing.T) {
	if modelCallRetryable(nil) {
		t.Error("nil failure should not be retryable")
	}
	if !modelCallRetryable(&modelinvocation.ModelInvocationFailure{Retryable: true}) {
		t.Error("retryable failure should be retryable")
	}
	if modelCallRetryable(&modelinvocation.ModelInvocationFailure{Retryable: false}) {
		t.Error("non-retryable failure should not be retryable")
	}
}

func TestEmptyResponsePolicy(t *testing.T) {
	if !emptyResponsePolicy(nil) {
		t.Error("nil result should be accepted as empty")
	}
	if !emptyResponsePolicy(&modelinvocation.ModelInvocationResult{}) {
		t.Error("empty result should be accepted")
	}
	if emptyResponsePolicy(&modelinvocation.ModelInvocationResult{Content: "hi"}) {
		t.Error("non-empty content should not be accepted as empty")
	}
	if emptyResponsePolicy(&modelinvocation.ModelInvocationResult{
		ToolCalls: []toolinvocation.ToolCall{{ID: "tc1"}},
	}) {
		t.Error("tool calls should not be accepted as empty")
	}
}

func TestMaxOutputPolicy(t *testing.T) {
	cfg := Config{MaxOutputRetries: 2, OutputBudgetRaise: 0.2}
	newBudget, shouldRetry := maxOutputPolicy(cfg, 100, 0)
	if !shouldRetry {
		t.Error("should retry on attempt 0")
	}
	if newBudget != 120 {
		t.Errorf("budget = %d, want 120", newBudget)
	}
	_, shouldRetry2 := maxOutputPolicy(cfg, 100, 2)
	if shouldRetry2 {
		t.Error("should not retry on attempt 2 (exhausted)")
	}
}

func TestToolResultRequestsStop(t *testing.T) {
	if toolResultRequestsStop(nil) {
		t.Error("nil results should not stop")
	}
	if !toolResultRequestsStop([]toolinvocation.ToolResult{{StopRequested: true}}) {
		t.Error("stop_requested should cause stop")
	}
	if toolResultRequestsStop([]toolinvocation.ToolResult{{StopRequested: false}}) {
		t.Error("stop_requested false should not cause stop")
	}
}

func TestBuildToolResultRecords(t *testing.T) {
	results := []toolinvocation.ToolResult{{
		CallID: "tc1",
		Text:   "result text",
	}}
	records := buildToolResultRecords(results)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Kind != session.RecordToolResult {
		t.Errorf("kind = %s, want %s", records[0].Kind, session.RecordToolResult)
	}
	if records[0].CallID != "tc1" {
		t.Errorf("callID = %s, want tc1", records[0].CallID)
	}
	if records[0].Text == nil || *records[0].Text != "result text" {
		t.Errorf("text = %v, want result text", records[0].Text)
	}
	// Turn grouping and identity are owned by the session service.
	if records[0].TurnID != "" {
		t.Errorf("turnID = %s, want empty", records[0].TurnID)
	}
	if records[0].Role != "" {
		t.Errorf("role = %s, want empty", records[0].Role)
	}
}

func TestNewSession(t *testing.T) {
	k, fs, ft, fm, fb, fc := newTestKernel()
	fs.created = newTestSession("sess1")
	ft.catalog = toolinvocation.ToolCatalog{ID: "cat1"}
	fc.initBundle = &contextprovider.ContextBundle{}
	fm.result = completeTurnResult("hello from model")
	fb.estimateAlloc = contextbuilder.Allocation{OutputReservation: 100}
	fb.assemblePrefix = contextbuilder.BuiltPrefix{
		SystemPrompt:   "you are helpful",
		SystemPromptID: "abc123",
	}
	fb.prepareCtx = contextbuilder.BuiltContext{
		Input: modelinvocation.ModelInput{
			System:   "you are helpful",
			Messages: []modelinvocation.ModelMessage{},
		},
	}

	obs := &noopObserver{}
	k.observer = obs
	sessID, err := k.New(context.Background(), NewInput{Messages: []string{"hello"}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if sessID != "sess1" {
		t.Errorf("session ID = %s, want sess1", sessID)
	}
	if obs.turnEndReason != ExitCompleted {
		t.Errorf("exit reason = %s, want %s", obs.turnEndReason, ExitCompleted)
	}
	if obs.turnEndContent != "hello from model" {
		t.Errorf("final content = %s, want hello from model", obs.turnEndContent)
	}
	if obs.contents[0] != "hello from model" {
		t.Errorf("model content = %s, want hello from model", obs.contents[0])
	}

	// Prefix should be cached in session metadata.
	cached, ok := loadBuiltPrefix(fs.created)
	if !ok {
		t.Fatal("built prefix should be cached")
	}
	if cached.SystemPromptID != "abc123" {
		t.Errorf("cached SystemPromptID = %s, want abc123", cached.SystemPromptID)
	}

	// Final assistant message should be written through WriteMessage.
	found := false
	for _, wm := range fs.writeMessages {
		if wm.Role == "assistant" && wm.Text == "hello from model" {
			found = true
			break
		}
	}
	if !found {
		t.Error("final assistant message not written")
	}
}

func TestNewWhileRunning(t *testing.T) {
	k, _, _, _, _, _ := newTestKernel()
	k.mu.Lock()
	k.state = KernelRunning
	k.mu.Unlock()

	_, err := k.New(context.Background(), NewInput{Messages: []string{"hello"}})
	if err != ErrBusy {
		t.Errorf("expected ErrBusy, got %v", err)
	}
}

func TestContinueWithEmptySessionID(t *testing.T) {
	k, _, _, _, _, _ := newTestKernel()
	err := k.Continue(context.Background(), ContinueInput{SessionID: "  ", Messages: []string{"hi"}})
	if err != ErrInvalidSession {
		t.Errorf("expected ErrInvalidSession, got %v", err)
	}
}

func TestCancelIdle(t *testing.T) {
	k, _, _, _, _, _ := newTestKernel()
	k.Cancel() // should not panic
	if k.state != KernelIdle {
		t.Errorf("state = %s, want idle", k.state)
	}
}

func TestTurnIDFormat(t *testing.T) {
	k, _, _, _, _, _ := newTestKernel()
	k.turnID = "turn_1234567890"
	if !strings.HasPrefix(k.turnID, "turn_") {
		t.Errorf("turnID = %s, want turn_ prefix", k.turnID)
	}
}

func TestContinueReusesPrefix(t *testing.T) {
	k, fs, ft, fm, fb, fc := newTestKernel()

	// Set up a session with a cached prefix.
	sess := newTestSession("sess1")
	prefix := contextbuilder.BuiltPrefix{
		SystemPrompt:   "cached prompt",
		SystemPromptID: "cached123",
	}
	raw, _ := json.Marshal(prefix)
	sess.Metadata.Custom[builtPrefixKey] = raw
	fs.created = sess
	ft.catalog = toolinvocation.ToolCatalog{ID: "cat1"}
	fc.getBundle = &contextprovider.ContextBundle{}
	fm.result = completeTurnResult("continued response")
	fb.prepareCtx = contextbuilder.BuiltContext{
		Input: modelinvocation.ModelInput{
			System:   "cached prompt",
			Messages: []modelinvocation.ModelMessage{},
		},
	}

	obs := &noopObserver{}
	k.observer = obs
	err := k.Continue(context.Background(), ContinueInput{SessionID: "sess1", Messages: []string{"continue"}})
	if err != nil {
		t.Fatalf("Continue failed: %v", err)
	}
	if obs.turnEndReason != ExitCompleted {
		t.Errorf("exit reason = %s, want %s", obs.turnEndReason, ExitCompleted)
	}
	if obs.turnEndContent != "continued response" {
		t.Errorf("final content = %s, want continued response", obs.turnEndContent)
	}
}

func TestSessionBudgetBlocksContinue(t *testing.T) {
	k, fs, _, _, _, _ := newTestKernel()
	k.cfg.SessionTokenLimit = 100
	sess := newTestSession("sess1")
	sess.Usage.TotalInputTokens = 80
	sess.Usage.TotalOutputTokens = 30 // 80+30=110 > 100
	fs.created = sess

	err := k.Continue(context.Background(), ContinueInput{SessionID: "sess1", Messages: []string{"hi"}})
	if err == nil {
		t.Fatal("expected error for budget exceeded")
	}
	if !strings.Contains(err.Error(), string(ExitBudgetExhausted)) {
		t.Errorf("error should mention budget exhausted, got: %v", err)
	}
}

func TestToolStopRequestedExits(t *testing.T) {
	k, fs, ft, fm, fb, fc := newTestKernel()
	fs.created = newTestSession("sess1")
	ft.catalog = toolinvocation.ToolCatalog{ID: "cat1"}
	fc.initBundle = &contextprovider.ContextBundle{}
	fb.estimateAlloc = contextbuilder.Allocation{OutputReservation: 100}
	fb.assemblePrefix = contextbuilder.BuiltPrefix{SystemPrompt: "prompt", SystemPromptID: "p1"}
	fb.prepareCtx = contextbuilder.BuiltContext{
		Input: modelinvocation.ModelInput{System: "prompt", Messages: []modelinvocation.ModelMessage{}},
	}

	// Model returns tool_calls, then tool execution returns stop_requested.
	fm.result = &modelinvocation.ModelInvocationResult{
		RequestID:  "inv1",
		StopReason: modelinvocation.StopToolCalls,
		ToolCalls:  []toolinvocation.ToolCall{{ID: "tc1", Name: "test_tool"}},
	}
	ft.executeRes = &toolinvocation.ToolExecutionResult{
		Results: []toolinvocation.ToolResult{{
			CallID:        "tc1",
			Text:          "done",
			StopRequested: true,
		}},
	}

	obs := &noopObserver{}
	k.observer = obs
	_, err := k.New(context.Background(), NewInput{Messages: []string{"hello"}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if obs.turnEndReason != ExitToolStopRequested {
		t.Errorf("exit reason = %s, want %s", obs.turnEndReason, ExitToolStopRequested)
	}
	if len(obs.toolStarts) != 1 || obs.toolStarts[0] != "test_tool" {
		t.Errorf("tool starts = %v, want [test_tool]", obs.toolStarts)
	}
	if len(obs.toolResults) != 1 {
		t.Errorf("tool results = %d, want 1", len(obs.toolResults))
	}
}

func TestModelErrorExit(t *testing.T) {
	k, fs, ft, fm, fb, fc := newTestKernel()
	fs.created = newTestSession("sess1")
	// No catalog or context needed since model fails immediately.
	fc.initBundle = &contextprovider.ContextBundle{}
	fb.estimateAlloc = contextbuilder.Allocation{OutputReservation: 100}
	fb.assemblePrefix = contextbuilder.BuiltPrefix{SystemPrompt: "prompt", SystemPromptID: "p1"}
	fb.prepareCtx = contextbuilder.BuiltContext{
		Input: modelinvocation.ModelInput{System: "prompt", Messages: []modelinvocation.ModelMessage{}},
	}
	ft = &fakeTools{catalog: toolinvocation.ToolCatalog{ID: "cat1"}}
	k.tools = ft

	fm.failure = &modelinvocation.ModelInvocationFailure{
		Code:      "provider_error",
		Retryable: false,
	}

	obs := &noopObserver{}
	k.observer = obs
	_, err := k.New(context.Background(), NewInput{Messages: []string{"hello"}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if obs.turnEndReason != ExitModelError {
		t.Errorf("exit reason = %s, want %s", obs.turnEndReason, ExitModelError)
	}
}

func TestTurnBudgetExhausted(t *testing.T) {
	k, fs, ft, fm, fb, fc := newTestKernel()
	k.cfg.TurnBudget = 2 // small budget
	fs.created = newTestSession("sess1")
	ft.catalog = toolinvocation.ToolCatalog{ID: "cat1"}
	fc.initBundle = &contextprovider.ContextBundle{}
	fb.estimateAlloc = contextbuilder.Allocation{OutputReservation: 100}
	fb.assemblePrefix = contextbuilder.BuiltPrefix{SystemPrompt: "prompt", SystemPromptID: "p1"}
	fb.prepareCtx = contextbuilder.BuiltContext{
		Input: modelinvocation.ModelInput{System: "prompt", Messages: []modelinvocation.ModelMessage{}},
	}

	// Model always returns tool_calls, tool execution succeeds with no stop.
	// This loops until the turn budget is exhausted.
	fm.result = &modelinvocation.ModelInvocationResult{
		RequestID:  "inv1",
		StopReason: modelinvocation.StopToolCalls,
		ToolCalls:  []toolinvocation.ToolCall{{ID: "tc1", Name: "noop"}},
	}
	ft.executeRes = &toolinvocation.ToolExecutionResult{
		Results: []toolinvocation.ToolResult{{CallID: "tc1", Text: "ok"}},
	}

	obs := &noopObserver{}
	k.observer = obs
	_, err := k.New(context.Background(), NewInput{Messages: []string{"hello"}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if obs.turnEndReason != ExitBudgetExhausted {
		t.Errorf("exit reason = %s, want %s", obs.turnEndReason, ExitBudgetExhausted)
	}
}
