package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateAndResumeSession(t *testing.T) {
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
	if len(created.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(created.Records))
	}
	if created.Records[0].Seq != 1 {
		t.Fatalf("initial record seq = %d, want 1", created.Records[0].Seq)
	}
	if created.Records[0].Role != "user" {
		t.Fatalf("initial record role = %q, want user", created.Records[0].Role)
	}
	if created.Records[0].Text != "start here" {
		t.Fatalf("initial record text = %q, want start here", created.Records[0].Text)
	}

	resumed, err := store.Resume(ctx, ResumeInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.ID != created.ID || resumed.Version != created.Version {
		t.Fatalf("resumed = (%s,%d), want (%s,%d)", resumed.ID, resumed.Version, created.ID, created.Version)
	}
	if !resumed.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("resume mutated UpdatedAt: got %s want %s", resumed.UpdatedAt, created.UpdatedAt)
	}
}

func TestCreateAndMutatePreserveTurnIDRefsAndCWD(t *testing.T) {
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
		TurnID: "turn-create",
		Refs:   []ContextRef{initialRef},
		Metadata: SessionMetadata{
			CWD: "/workspace/frankenstein",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Metadata.CWD != "/workspace/frankenstein" {
		t.Fatalf("created Metadata.CWD = %q, want /workspace/frankenstein", created.Metadata.CWD)
	}
	if created.Records[0].TurnID != "turn-create" {
		t.Fatalf("initial TurnID = %q, want turn-create", created.Records[0].TurnID)
	}
	if len(created.Records[0].Refs) != 1 || created.Records[0].Refs[0].Target != initialRef.Target {
		t.Fatalf("initial Refs = %+v, want target %q", created.Records[0].Refs, initialRef.Target)
	}

	mutated, err := store.Mutate(ctx, MutateInput{
		ID: created.ID,
		Ops: []MutationOp{
			{
				Type: MutationAppendRecord,
				Record: &SessionRecord{
					TurnID: "turn-create",
					Role:   "assistant",
					Text:   "next",
					Refs: []ContextRef{{
						Kind:   "artifact",
						Target: "artifact://reply-1",
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}
	if mutated.Records[1].TurnID != "turn-create" {
		t.Fatalf("mutated TurnID = %q, want turn-create", mutated.Records[1].TurnID)
	}
	if len(mutated.Records[1].Refs) != 1 || mutated.Records[1].Refs[0].Kind != "artifact" {
		t.Fatalf("mutated Refs = %+v, want one artifact ref", mutated.Records[1].Refs)
	}

	read, err := store.Read(ctx, ReadInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Metadata.CWD != "/workspace/frankenstein" {
		t.Fatalf("read Metadata.CWD = %q, want /workspace/frankenstein", read.Metadata.CWD)
	}
	if read.Records[0].TurnID != "turn-create" || read.Records[1].TurnID != "turn-create" {
		t.Fatalf("read TurnIDs = [%q,%q], want both turn-create", read.Records[0].TurnID, read.Records[1].TurnID)
	}
	if len(read.Records[0].Refs) != 1 || read.Records[0].Refs[0].Range == nil || read.Records[0].Refs[0].Range.End != 12 {
		t.Fatalf("read initial Refs = %+v, want line range ending 12", read.Records[0].Refs)
	}
	if len(read.Records[1].Refs) != 1 || read.Records[1].Refs[0].Target != "artifact://reply-1" {
		t.Fatalf("read appended Refs = %+v, want artifact://reply-1", read.Records[1].Refs)
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

func TestResumeMissingSessionDoesNotCreateContinuity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.Resume(ctx, ResumeInput{ID: "sess_missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resume() error = %v, want ErrNotFound", err)
	}

	_, err = store.Read(ctx, ReadInput{ID: "sess_missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read() error = %v, want ErrNotFound", err)
	}

	_, err = store.Materialize(ctx, MaterializeInput{ID: "sess_missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Materialize() error = %v, want ErrNotFound", err)
	}
}

func TestReadAndMaterializeOrderedRecord(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "start here"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mutated, err := store.Mutate(ctx, MutateInput{
		ID: created.ID,
		Ops: []MutationOp{
			{
				Type:   MutationAppendRecord,
				Record: &SessionRecord{Role: "assistant", Text: "next"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}

	read, err := store.Read(ctx, ReadInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.ID != created.ID || read.Version != mutated.Version {
		t.Fatalf("Read() returned (%s,%d), want (%s,%d)", read.ID, read.Version, created.ID, mutated.Version)
	}
	if len(read.Records) != 2 {
		t.Fatalf("Read() records = %d, want 2", len(read.Records))
	}
	if read.Records[0].Seq != 1 || read.Records[1].Seq != 2 {
		t.Fatalf("Read() record seqs = [%d,%d], want [1,2]", read.Records[0].Seq, read.Records[1].Seq)
	}
	if read.Records[1].Role != "assistant" || read.Records[1].Text != "next" {
		t.Fatalf("Read() second record = (%q,%q), want assistant next", read.Records[1].Role, read.Records[1].Text)
	}

	materialized, err := store.Materialize(ctx, MaterializeInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if materialized.SessionID != created.ID {
		t.Fatalf("Materialize() SessionID = %q, want %q", materialized.SessionID, created.ID)
	}
	if materialized.Version != mutated.Version {
		t.Fatalf("Materialize() Version = %d, want %d", materialized.Version, mutated.Version)
	}
	if materialized.Kind != ContinuationOrderedRecords {
		t.Fatalf("Materialize() Kind = %q, want ordered_records", materialized.Kind)
	}
	if len(materialized.Records) != 2 {
		t.Fatalf("Materialize() records = %d, want 2", len(materialized.Records))
	}
	if materialized.Records[0].ID != read.Records[0].ID || materialized.Records[1].ID != read.Records[1].ID {
		t.Fatalf("Materialize() record IDs do not match Read()")
	}
}

func TestReadOrdersRecordsBySessionSeq(t *testing.T) {
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
		Text: "third",
	}, 3, now.Add(3*time.Second))
	second := normalizeRecord(SessionRecord{
		ID:   "rec_second",
		Role: "user",
		Text: "second",
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

	read, err := store.Read(ctx, ReadInput{ID: created.ID})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(read.Records) != 3 {
		t.Fatalf("Read() records = %d, want 3", len(read.Records))
	}
	for i, record := range read.Records {
		wantSeq := int64(i + 1)
		if record.Seq != wantSeq {
			t.Fatalf("record %d seq = %d, want %d", i, record.Seq, wantSeq)
		}
	}
	if read.Records[1].Text != "second" || read.Records[2].Text != "third" {
		t.Fatalf("records returned in wrong order: got %q then %q", read.Records[1].Text, read.Records[2].Text)
	}
}

func TestAppendRecordCreatesEstimatedPromptState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mutated, err := store.Mutate(ctx, MutateInput{
		ID: created.ID,
		Ops: []MutationOp{
			{
				Type: MutationAppendRecord,
				Record: &SessionRecord{
					Kind: "message",
					Role: "user",
					Text: "hello world",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}

	if mutated.Version != 2 {
		t.Fatalf("Version = %d, want 2", mutated.Version)
	}
	if len(mutated.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(mutated.Records))
	}
	record := mutated.Records[1]
	if record.Seq != 2 {
		t.Fatalf("record.Seq = %d, want 2", record.Seq)
	}
	if record.CharCount != 11 {
		t.Fatalf("record.CharCount = %d, want 11", record.CharCount)
	}
	if record.Tokens != (TokenCount{Value: 3, Source: TokenSourceCharEstimate}) {
		t.Fatalf("record.Tokens = %+v, want char estimate 3", record.Tokens)
	}
	if mutated.Usage.CharCount != 12 {
		t.Fatalf("Usage.CharCount = %d, want 12", mutated.Usage.CharCount)
	}
	if mutated.Usage.LastPromptTokens != (TokenCount{Value: 3, Source: TokenSourceCharEstimate}) {
		t.Fatalf("LastPromptTokens = %+v, want estimated 3", mutated.Usage.LastPromptTokens)
	}
}

func TestIdempotentMutationDoesNotApplyTwice(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	input := MutateInput{
		ID:             created.ID,
		IdempotencyKey: "turn-1",
		Ops: []MutationOp{{
			Type:   MutationAppendRecord,
			Record: &SessionRecord{Role: "assistant", Text: "once"},
		}},
	}

	first, err := store.Mutate(ctx, input)
	if err != nil {
		t.Fatalf("first Mutate() error = %v", err)
	}
	second, err := store.Mutate(ctx, input)
	if err != nil {
		t.Fatalf("second Mutate() error = %v", err)
	}

	if first.Version != 2 {
		t.Fatalf("first Version = %d, want 2", first.Version)
	}
	if second.Version != first.Version {
		t.Fatalf("second Version = %d, want %d", second.Version, first.Version)
	}
	if len(second.Records) != 2 {
		t.Fatalf("second records = %d, want 2", len(second.Records))
	}
	if second.Records[1].Text != "once" {
		t.Fatalf("second record text = %q, want once", second.Records[1].Text)
	}
}

func TestSetUsageReplacesEstimateWithProviderUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	created, err := store.Create(ctx, CreateInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Mutate(ctx, MutateInput{
		ID: created.ID,
		Ops: []MutationOp{{
			Type:   MutationAppendRecord,
			Record: &SessionRecord{Role: "user", Text: "hello world"},
		}},
	}); err != nil {
		t.Fatalf("append Mutate() error = %v", err)
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
	mutated, err := store.Mutate(ctx, MutateInput{
		ID: created.ID,
		Ops: []MutationOp{{
			Type:  MutationSetUsage,
			Usage: &usage,
		}},
	})
	if err != nil {
		t.Fatalf("set usage Mutate() error = %v", err)
	}

	if mutated.Version != 3 {
		t.Fatalf("Version = %d, want 3", mutated.Version)
	}
	if mutated.Usage.LastPromptTokens.Source != TokenSourceProvider {
		t.Fatalf("LastPromptTokens.Source = %q, want provider", mutated.Usage.LastPromptTokens.Source)
	}
	if mutated.Usage.LastPromptTokens.Value != 18 {
		t.Fatalf("LastPromptTokens.Value = %d, want 18", mutated.Usage.LastPromptTokens.Value)
	}
	if mutated.Usage.TotalOutputTokens != 7 {
		t.Fatalf("TotalOutputTokens = %d, want 7", mutated.Usage.TotalOutputTokens)
	}
}

func TestDeleteMakesSessionUnresumable(t *testing.T) {
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
	if deleted.DeletedAt == nil {
		t.Fatalf("DeletedAt = nil, want timestamp")
	}

	_, err = store.Resume(ctx, ResumeInput{ID: created.ID})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("Resume() error = %v, want ErrDeleted", err)
	}

	_, err = store.Read(ctx, ReadInput{ID: created.ID})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("Read() error = %v, want ErrDeleted", err)
	}

	_, err = store.Materialize(ctx, MaterializeInput{ID: created.ID})
	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("Materialize() error = %v, want ErrDeleted", err)
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
