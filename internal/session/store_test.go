package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateAndGetSession(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start here"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.ID == "" {
		t.Fatalf("ID is empty")
	}
	if created.Version != 1 {
		t.Fatalf("Version = %d, want 1", created.Version)
	}
	if created.State != SessionActive {
		t.Fatalf("State = %q, want active", created.State)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID || got.Version != created.Version {
		t.Fatalf("got = (%s,%d), want (%s,%d)", got.ID, got.Version, created.ID, created.Version)
	}
	if len(got.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(got.Records))
	}
	if got.Records[0].Seq != 1 {
		t.Fatalf("initial record seq = %d, want 1", got.Records[0].Seq)
	}
	if got.Records[0].Role != "user" {
		t.Fatalf("initial record role = %q, want user", got.Records[0].Role)
	}
	if textValue(got.Records[0].Text) != "start here" {
		t.Fatalf("initial record text = %q, want start here", textValue(got.Records[0].Text))
	}
	if got.Records[0].TurnID == "" {
		t.Fatalf("initial record TurnID is empty, want service-assigned")
	}

	// Get must not mutate session state.
	again, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if again.Version != got.Version || !again.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("get mutated state: version %d->%d updated_at %s->%s", got.Version, again.Version, got.UpdatedAt, again.UpdatedAt)
	}
}

func TestCreatePreservesRefsAndCWD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	initialRef := ContextRef{
		Kind:   "file",
		Target: "docs/session-capability-contract.md",
		Label:  "session contract",
		Range:  &ContextRefRange{Unit: "line", Start: 1, End: 12},
	}
	created, err := store.Create(ctx, CreateInput{
		Prompt: "start here",
		Refs:   []ContextRef{initialRef},
		Metadata: SessionMetadata{
			CWD: "/workspace/frankenstein",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Metadata.CWD != "/workspace/frankenstein" {
		t.Fatalf("Metadata.CWD = %q, want /workspace/frankenstein", got.Metadata.CWD)
	}
	if len(got.Records[0].Refs) != 1 || got.Records[0].Refs[0].Target != initialRef.Target {
		t.Fatalf("initial Refs = %+v, want target %q", got.Records[0].Refs, initialRef.Target)
	}
	if len(got.Records[0].Refs) != 1 || got.Records[0].Refs[0].Range == nil || got.Records[0].Refs[0].Range.End != 12 {
		t.Fatalf("initial Refs = %+v, want line range ending 12", got.Records[0].Refs)
	}
}

func TestCreateRequiresPrompt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.Create(ctx, CreateInput{Prompt: "   "})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
	}
}

func TestGetMissingSession(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.Get(ctx, GetInput{ID: "sess_missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestWriteMessageTurnGrouping(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "first"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A user message opens a new turn distinct from the create turn.
	userWrite, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "second",
		Role:      "user",
	})
	if err != nil {
		t.Fatalf("WriteMessage(user) error = %v", err)
	}
	// An assistant message extends the current turn.
	assistantWrite, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "reply",
		Role:      "assistant",
	})
	if err != nil {
		t.Fatalf("WriteMessage(assistant) error = %v", err)
	}
	// Another user message opens yet another turn.
	lastUserWrite, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "third",
		Role:      "user",
	})
	if err != nil {
		t.Fatalf("WriteMessage(user) error = %v", err)
	}

	if userWrite.RecordID == "" || assistantWrite.RecordID == "" || lastUserWrite.RecordID == "" {
		t.Fatalf("write results missing record_id: %+v %+v %+v", userWrite, assistantWrite, lastUserWrite)
	}
	if userWrite.ID != created.ID || userWrite.State != SessionActive {
		t.Fatalf("write result = %+v, want session %s active", userWrite, created.ID)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Records) != 4 {
		t.Fatalf("records = %d, want 4", len(got.Records))
	}
	t1, t2, t3, t4 := got.Records[0].TurnID, got.Records[1].TurnID, got.Records[2].TurnID, got.Records[3].TurnID
	if t1 == t2 || t2 == t4 {
		t.Fatalf("consecutive user messages share turn: [%q,%q,%q,%q]", t1, t2, t3, t4)
	}
	if t2 != t3 {
		t.Fatalf("assistant message turn = %q, want %q (last user turn)", t3, t2)
	}
}

func TestWriteMessageRefsPreservedOnGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "see ref",
		Role:      "user",
		Refs: []ContextRef{{
			Kind:   "file",
			Target: "docs/session-capability-contract.md",
		}},
	}); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Records[1].Refs) != 1 || got.Records[1].Refs[0].Target != "docs/session-capability-contract.md" {
		t.Fatalf("written Refs = %+v, want one file ref", got.Records[1].Refs)
	}
}

func TestWriteMessageRequiresTextAndValidRole(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.WriteMessage(ctx, WriteMessageInput{SessionID: created.ID, Text: "   ", Role: "user"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty text error = %v, want ErrInvalidInput", err)
	}
	_, err = store.WriteMessage(ctx, WriteMessageInput{SessionID: created.ID, Text: "hi", Role: "tool"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid role error = %v, want ErrInvalidInput", err)
	}
}

func TestWriteToolCallAndToolResultExtendTurn(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "run the tool"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	callWrite, err := store.WriteToolCall(ctx, WriteToolCallInput{
		SessionID: created.ID,
		Name:      "run_shell",
		Arguments: map[string]any{"cmd": "ls"},
		CallID:    "call-1",
		ToolID:    "tool_shell",
	})
	if err != nil {
		t.Fatalf("WriteToolCall() error = %v", err)
	}
	resultWrite, err := store.WriteToolResult(ctx, WriteToolResultInput{
		SessionID: created.ID,
		Text:      "total 8",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("WriteToolResult() error = %v", err)
	}
	if callWrite.RecordID == "" || resultWrite.RecordID == "" {
		t.Fatalf("write results missing record_id: %+v %+v", callWrite, resultWrite)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Records) != 3 {
		t.Fatalf("records = %d, want 3", len(got.Records))
	}
	// The tool call and its result extend the create turn.
	if got.Records[1].TurnID != got.Records[0].TurnID || got.Records[2].TurnID != got.Records[0].TurnID {
		t.Fatalf("tool records not in create turn: [%q,%q,%q]", got.Records[0].TurnID, got.Records[1].TurnID, got.Records[2].TurnID)
	}
	if got.Records[1].Kind != RecordToolCall || len(got.Records[1].ToolCalls) != 1 {
		t.Fatalf("tool call record = %+v, want one ToolCall", got.Records[1])
	}
	if got.Records[1].ToolCalls[0].ID != "call-1" || got.Records[1].ToolCalls[0].Name != "run_shell" {
		t.Fatalf("ToolCall = %+v, want call-1 run_shell", got.Records[1].ToolCalls[0])
	}
	if got.Records[1].ToolCalls[0].ToolID != "tool_shell" {
		t.Fatalf("ToolCall.ToolID = %q, want tool_shell", got.Records[1].ToolCalls[0].ToolID)
	}
	if got.Records[2].Kind != RecordToolResult || got.Records[2].CallID != "call-1" {
		t.Fatalf("tool result record = %+v, want call_id call-1", got.Records[2])
	}
	if textValue(got.Records[2].Text) != "total 8" {
		t.Fatalf("tool result text = %q, want total 8", textValue(got.Records[2].Text))
	}
}

func TestWriteSystemNoteExtendsTurn(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.WriteSystemNote(ctx, WriteSystemNoteInput{
		SessionID: created.ID,
		Text:      "context compacted",
	}); err != nil {
		t.Fatalf("WriteSystemNote() error = %v", err)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Records[1].Kind != RecordSystemNote || textValue(got.Records[1].Text) != "context compacted" {
		t.Fatalf("system note record = %+v", got.Records[1])
	}
	if got.Records[1].TurnID != got.Records[0].TurnID {
		t.Fatalf("system note turn = %q, want %q", got.Records[1].TurnID, got.Records[0].TurnID)
	}
}

func TestWriteRecordPreservesCallerFieldsAndDefaults(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	callerText := "imported"
	callerTime := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	result, err := store.WriteRecord(ctx, WriteRecordInput{
		SessionID: created.ID,
		Record: SessionRecord{
			ID:        "rec_imported",
			TurnID:    "turn_imported",
			Kind:      RecordMessage,
			Role:      "assistant",
			Text:      &callerText,
			CreatedAt: callerTime,
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if result.RecordID != "rec_imported" {
		t.Fatalf("RecordID = %q, want rec_imported", result.RecordID)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	rec := got.Records[1]
	if rec.ID != "rec_imported" || rec.TurnID != "turn_imported" {
		t.Fatalf("imported record identity not preserved: %+v", rec)
	}
	if !rec.CreatedAt.Equal(callerTime) {
		t.Fatalf("imported record CreatedAt = %s, want %s", rec.CreatedAt, callerTime)
	}
	if rec.Seq != 2 {
		t.Fatalf("imported record seq = %d, want 2", rec.Seq)
	}

	// A record with empty id/turn_id gets defaults filled by the service.
	blankText := "service-filled"
	blankResult, err := store.WriteRecord(ctx, WriteRecordInput{
		SessionID: created.ID,
		Record: SessionRecord{
			Kind: RecordSystemNote,
			Text: &blankText,
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord(blank) error = %v", err)
	}
	if blankResult.RecordID == "" {
		t.Fatalf("blank record id not assigned")
	}
	got2, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got2.Records[2].ID == "" || got2.Records[2].TurnID == "" {
		t.Fatalf("blank record defaults not filled: %+v", got2.Records[2])
	}
}

func TestWriteRecordRejectsUnknownKind(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.WriteRecord(ctx, WriteRecordInput{
		SessionID: created.ID,
		Record:    SessionRecord{Kind: "branch"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("WriteRecord() error = %v, want ErrInvalidInput", err)
	}
}

func TestSetMetadataReplacesObject(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	replacement := SessionMetadata{
		Title:         "renamed",
		ModelProvider: "anthropic",
		Model:         "claude",
	}
	result, err := store.SetMetadata(ctx, SetMetadataInput{
		SessionID: created.ID,
		Metadata:  replacement,
	})
	if err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}
	if result.ID != created.ID || result.State != SessionActive {
		t.Fatalf("SetMetadata() result = %+v", result)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Metadata.Title != "renamed" || got.Metadata.ModelProvider != "anthropic" {
		t.Fatalf("metadata = %+v, want renamed anthropic", got.Metadata)
	}
	if got.Metadata.CWD != "" {
		t.Fatalf("metadata = %+v, want full replacement (no cwd)", got.Metadata)
	}
}

func TestSetUsageReplacesObject(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	usage := SessionUsage{
		CharCount:           42,
		LastPromptTokens:    TokenCount{Value: 18, Source: TokenSourceProvider},
		LastOutputTokens:    7,
		TotalInputTokens:    18,
		TotalOutputTokens:   7,
		ContextWindowTokens: 100,
		LastContextUsedPct:  18,
		APICallCount:        1,
	}
	if _, err := store.SetUsage(ctx, SetUsageInput{
		SessionID: created.ID,
		Usage:     usage,
	}); err != nil {
		t.Fatalf("SetUsage() error = %v", err)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Usage.LastPromptTokens.Source != TokenSourceProvider {
		t.Fatalf("LastPromptTokens.Source = %q, want provider", got.Usage.LastPromptTokens.Source)
	}
	if got.Usage.LastPromptTokens.Value != 18 {
		t.Fatalf("LastPromptTokens.Value = %d, want 18", got.Usage.LastPromptTokens.Value)
	}
	if got.Usage.TotalOutputTokens != 7 {
		t.Fatalf("TotalOutputTokens = %d, want 7", got.Usage.TotalOutputTokens)
	}
}

func TestVersionAdvancesOnWriteNotOnGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "first"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	write, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "second",
		Role:      "user",
	})
	if err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if write.Version != 2 {
		t.Fatalf("write version = %d, want 2", write.Version)
	}

	setResult, err := store.SetUsage(ctx, SetUsageInput{
		SessionID: created.ID,
		Usage:     SessionUsage{APICallCount: 1},
	})
	if err != nil {
		t.Fatalf("SetUsage() error = %v", err)
	}
	if setResult.Version != 3 {
		t.Fatalf("set_usage version = %d, want 3", setResult.Version)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("get version = %d, want 3", got.Version)
	}
	gotAgain, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if gotAgain.Version != got.Version || !gotAgain.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("get mutated version/updated_at")
	}
}

func TestGetOrdersRecordsBySessionSeq(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "first"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer rollback(tx)

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	third := normalizeRecord(SessionRecord{
		ID:   "rec_third",
		Role: "assistant",
		Text: stringPointer("third"),
	}, 3, now.Add(3*time.Second))
	second := normalizeRecord(SessionRecord{
		ID:   "rec_second",
		Role: "user",
		Text: stringPointer("second"),
	}, 2, now.Add(2*time.Second))

	if err := insertRecord(ctx, tx, created.ID, third); err != nil {
		t.Fatalf("insert third record error = %v", err)
	}
	if err := insertRecord(ctx, tx, created.ID, second); err != nil {
		t.Fatalf("insert second record error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Records) != 3 {
		t.Fatalf("Get() records = %d, want 3", len(got.Records))
	}
	for i, record := range got.Records {
		wantSeq := int64(i + 1)
		if record.Seq != wantSeq {
			t.Fatalf("record %d seq = %d, want %d", i, record.Seq, wantSeq)
		}
	}
	if textValue(got.Records[1].Text) != "second" || textValue(got.Records[2].Text) != "third" {
		t.Fatalf("records returned in wrong order: got %q then %q", textValue(got.Records[1].Text), textValue(got.Records[2].Text))
	}
}

func TestRecordWritesPreserveExistingUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Capture usage after create (initialized from the first record).
	got, err := store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	initialUsage := got.Usage

	write, err := store.WriteMessage(ctx, WriteMessageInput{
		SessionID: created.ID,
		Text:      "hello world",
		Role:      "user",
	})
	if err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if write.Version != 2 {
		t.Fatalf("Version = %d, want 2", write.Version)
	}

	got, err = store.Get(ctx, GetInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(got.Records))
	}
	record := got.Records[1]
	if record.Seq != 2 {
		t.Fatalf("record.Seq = %d, want 2", record.Seq)
	}
	if record.CharCount != 11 {
		t.Fatalf("record.CharCount = %d, want 11", record.CharCount)
	}
	if record.Tokens != (TokenCount{Value: 3, Source: TokenSourceCharEstimate}) {
		t.Fatalf("record.Tokens = %+v, want char estimate 3", record.Tokens)
	}

	// Record writes must not auto-update session usage. Usage is owned
	// by the kernel, which updates it via SetUsage.
	if got.Usage != initialUsage {
		t.Fatalf("Usage changed on record write: got %+v, want %+v", got.Usage, initialUsage)
	}
}

func TestDeleteMakesSessionUnavailable(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	deleted, err := store.Delete(ctx, DeleteInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.State != SessionDeleted {
		t.Fatalf("State = %q, want deleted", deleted.State)
	}
	if deleted.Version != 2 {
		t.Fatalf("Version = %d, want 2", deleted.Version)
	}

	_, err = store.Get(ctx, GetInput{ID: created.ID})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("Get() error = %v, want ErrDeleted", err)
	}
	_, err = store.WriteMessage(ctx, WriteMessageInput{SessionID: created.ID, Text: "hi", Role: "user"})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("WriteMessage() error = %v, want ErrDeleted", err)
	}
	_, err = store.SetMetadata(ctx, SetMetadataInput{SessionID: created.ID, Metadata: SessionMetadata{}})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("SetMetadata() error = %v, want ErrDeleted", err)
	}

	// Deleting again returns the current state without another version bump.
	again, err := store.Delete(ctx, DeleteInput{ID: created.ID})
	if err != nil {
		t.Fatalf("second Delete() error = %v", err)
	}
	if again.Version != deleted.Version {
		t.Fatalf("second delete version = %d, want %d", again.Version, deleted.Version)
	}
}

func TestWriteOnMissingSession(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.WriteMessage(ctx, WriteMessageInput{SessionID: "sess_missing", Text: "hi", Role: "user"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("WriteMessage() error = %v, want ErrNotFound", err)
	}
	_, err = store.SetUsage(ctx, SetUsageInput{SessionID: "sess_missing", Usage: SessionUsage{}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetUsage() error = %v, want ErrNotFound", err)
	}
	_, err = store.Delete(ctx, DeleteInput{ID: "sess_missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	store.now = func() time.Time {
		return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	}
	return store
}
