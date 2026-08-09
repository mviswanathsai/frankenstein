package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &Store{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT,
	metadata_json TEXT NOT NULL,
	usage_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS session_records (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	id TEXT NOT NULL,
	turn_id TEXT,
	kind TEXT NOT NULL,
	role TEXT,
	text TEXT,
	refs_json TEXT,
	raw_json TEXT,
	created_at TEXT NOT NULL,
	char_count INTEGER NOT NULL,
	token_count INTEGER NOT NULL,
	token_count_source TEXT NOT NULL,
	PRIMARY KEY (session_id, seq),
	UNIQUE (session_id, id)
);

CREATE TABLE IF NOT EXISTS session_mutation_results (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	idempotency_key TEXT NOT NULL,
	version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (session_id, idempotency_key)
);
	`)
	if err != nil {
		return err
	}
	return s.ensureSessionRecordColumns(ctx)
}

func (s *Store) ensureSessionRecordColumns(ctx context.Context) error {
	columns, err := s.sessionRecordColumns(ctx)
	if err != nil {
		return err
	}
	migrations := []struct {
		name string
		sql  string
	}{
		{name: "turn_id", sql: `ALTER TABLE session_records ADD COLUMN turn_id TEXT`},
		{name: "refs_json", sql: `ALTER TABLE session_records ADD COLUMN refs_json TEXT`},
	}
	for _, migration := range migrations {
		if columns[migration.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, migration.sql); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) sessionRecordColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(session_records)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (s *Store) Create(ctx context.Context, input CreateInput) (*Session, error) {
	if strings.TrimSpace(input.Prompt) == "" {
		return nil, fmt.Errorf("%w: prompt is required", ErrInvalidInput)
	}

	now := s.now()
	record := normalizeRecord(SessionRecord{
		TurnID: input.TurnID,
		Refs:   input.Refs,
		Kind:   RecordMessage,
		Role:   "user",
		Text:   &input.Prompt,
	}, 1, now)

	session := &Session{
		ID:        newID("sess"),
		Version:   1,
		State:     SessionActive,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  input.Metadata,
		Usage:     updateUsageForAppendedRecord(SessionUsage{}, record),
		Records:   []SessionRecord{record},
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	if err := insertSession(ctx, tx, session); err != nil {
		return nil, err
	}
	if err := insertRecord(ctx, tx, session.ID, record); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Store) Resume(ctx context.Context, input ResumeInput) (*Session, error) {
	return s.loadActive(ctx, input.ID)
}

func (s *Store) Read(ctx context.Context, input ReadInput) (*Session, error) {
	return s.loadActive(ctx, input.ID)
}

func (s *Store) Materialize(ctx context.Context, input MaterializeInput) (*MaterializedSession, error) {
	session, err := s.loadActive(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return &MaterializedSession{
		SessionID: session.ID,
		Version:   session.Version,
		State:     session.State,
		Kind:      ContinuationOrderedRecords,
		Metadata:  session.Metadata,
		Usage:     session.Usage,
		Records:   session.Records,
	}, nil
}

func (s *Store) loadActive(ctx context.Context, rawID string) (*Session, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	session, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if session.State == SessionDeleted {
		return nil, ErrDeleted
	}
	return session, nil
}

func (s *Store) Mutate(ctx context.Context, input MutateInput) (*Session, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if len(input.Ops) == 0 {
		return nil, fmt.Errorf("%w: ops are required", ErrInvalidMutation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	current, err := loadSessionTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if current.State == SessionDeleted {
		return nil, ErrDeleted
	}
	if idempotencyKey != "" {
		alreadyApplied, err := mutationAlreadyApplied(ctx, tx, current.ID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if alreadyApplied {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return current, nil
		}
	}

	now := s.now()
	nextSeq := int64(len(current.Records) + 1)
	for _, op := range input.Ops {
		switch op.Type {
		case MutationAppendRecord:
			if op.Record == nil {
				return nil, fmt.Errorf("%w: append_record requires record", ErrInvalidMutation)
			}
			record := normalizeRecord(*op.Record, nextSeq, now)
			current.Records = append(current.Records, record)
			if err := insertRecord(ctx, tx, current.ID, record); err != nil {
				return nil, err
			}
			nextSeq++
			current.Usage = updateUsageForAppendedRecord(current.Usage, record)
		case MutationSetMetadata:
			if op.Metadata == nil {
				return nil, fmt.Errorf("%w: set_metadata requires metadata", ErrInvalidMutation)
			}
			current.Metadata = *op.Metadata
		case MutationSetUsage:
			if op.Usage == nil {
				return nil, fmt.Errorf("%w: set_usage requires usage", ErrInvalidMutation)
			}
			current.Usage = *op.Usage
		default:
			return nil, fmt.Errorf("%w: unknown mutation type %q", ErrInvalidMutation, op.Type)
		}
	}

	current.Version++
	current.UpdatedAt = now
	if err := updateSession(ctx, tx, current); err != nil {
		return nil, err
	}
	if idempotencyKey != "" {
		if err := insertMutationResult(ctx, tx, current.ID, idempotencyKey, current.Version, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) Delete(ctx context.Context, input DeleteInput) (*Session, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	current, err := loadSessionTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if current.State == SessionDeleted {
		return current, nil
	}

	now := s.now()
	current.State = SessionDeleted
	current.DeletedAt = &now
	current.UpdatedAt = now
	current.Version++

	if err := updateSession(ctx, tx, current); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) load(ctx context.Context, id string) (*Session, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	session, err := loadSessionTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return session, nil
}

func insertSession(ctx context.Context, tx *sql.Tx, session *Session) error {
	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(session.Usage)
	if err != nil {
		return err
	}
	var deletedAt any
	if session.DeletedAt != nil {
		deletedAt = session.DeletedAt.Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO sessions (
	id, version, state, created_at, updated_at, deleted_at, metadata_json, usage_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Version,
		session.State,
		session.CreatedAt.Format(time.RFC3339Nano),
		session.UpdatedAt.Format(time.RFC3339Nano),
		deletedAt,
		string(metadataJSON),
		string(usageJSON),
	)
	return err
}

func updateSession(ctx context.Context, tx *sql.Tx, session *Session) error {
	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(session.Usage)
	if err != nil {
		return err
	}
	var deletedAt any
	if session.DeletedAt != nil {
		deletedAt = session.DeletedAt.Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE sessions
SET version = ?, state = ?, updated_at = ?, deleted_at = ?, metadata_json = ?, usage_json = ?
WHERE id = ?`,
		session.Version,
		session.State,
		session.UpdatedAt.Format(time.RFC3339Nano),
		deletedAt,
		string(metadataJSON),
		string(usageJSON),
		session.ID,
	)
	return err
}

func mutationAlreadyApplied(ctx context.Context, tx *sql.Tx, sessionID, idempotencyKey string) (bool, error) {
	row := tx.QueryRowContext(ctx, `
	SELECT 1
	FROM session_mutation_results
	WHERE session_id = ? AND idempotency_key = ?`, sessionID, idempotencyKey)

	var matched int
	if err := row.Scan(&matched); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func insertMutationResult(ctx context.Context, tx *sql.Tx, sessionID, idempotencyKey string, version int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
	INSERT INTO session_mutation_results (
		session_id, idempotency_key, version, created_at
	) VALUES (?, ?, ?, ?)`,
		sessionID,
		idempotencyKey,
		version,
		now.Format(time.RFC3339Nano),
	)
	return err
}

func insertRecord(ctx context.Context, tx *sql.Tx, sessionID string, record SessionRecord) error {
	var raw any
	if len(record.Raw) > 0 {
		raw = string(record.Raw)
	}
	var refs any
	if len(record.Refs) > 0 {
		refsJSON, err := json.Marshal(record.Refs)
		if err != nil {
			return err
		}
		refs = string(refsJSON)
	}
	var text any
	if record.Text != nil {
		text = *record.Text
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO session_records (
	session_id, seq, id, turn_id, kind, role, text, refs_json, raw_json, created_at, char_count, token_count, token_count_source
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		record.Seq,
		record.ID,
		record.TurnID,
		record.Kind,
		record.Role,
		text,
		refs,
		raw,
		record.CreatedAt.Format(time.RFC3339Nano),
		record.CharCount,
		record.Tokens.Value,
		record.Tokens.Source,
	)
	return err
}

func loadSessionTx(ctx context.Context, tx *sql.Tx, id string) (*Session, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, version, state, created_at, updated_at, deleted_at, metadata_json, usage_json
FROM sessions
WHERE id = ?`, id)

	var session Session
	var state string
	var createdAt, updatedAt, deletedAt sql.NullString
	var metadataJSON, usageJSON string
	if err := row.Scan(
		&session.ID,
		&session.Version,
		&state,
		&createdAt,
		&updatedAt,
		&deletedAt,
		&metadataJSON,
		&usageJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	session.State = SessionState(state)
	parsedCreatedAt, err := parseTime(createdAt.String)
	if err != nil {
		return nil, err
	}
	parsedUpdatedAt, err := parseTime(updatedAt.String)
	if err != nil {
		return nil, err
	}
	session.CreatedAt = parsedCreatedAt
	session.UpdatedAt = parsedUpdatedAt
	if deletedAt.Valid {
		parsedDeletedAt, err := parseTime(deletedAt.String)
		if err != nil {
			return nil, err
		}
		session.DeletedAt = &parsedDeletedAt
	}
	if err := json.Unmarshal([]byte(metadataJSON), &session.Metadata); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(usageJSON), &session.Usage); err != nil {
		return nil, err
	}

	records, err := loadRecordsTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	session.Records = records
	return &session, nil
}

func loadRecordsTx(ctx context.Context, tx *sql.Tx, sessionID string) ([]SessionRecord, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, seq, turn_id, kind, role, text, refs_json, raw_json, created_at, char_count, token_count, token_count_source
FROM session_records
WHERE session_id = ?
ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SessionRecord
	for rows.Next() {
		var record SessionRecord
		var turnID sql.NullString
		var text sql.NullString
		var refs sql.NullString
		var raw sql.NullString
		var createdAt string
		var tokenSource string
		if err := rows.Scan(
			&record.ID,
			&record.Seq,
			&turnID,
			&record.Kind,
			&record.Role,
			&text,
			&refs,
			&raw,
			&createdAt,
			&record.CharCount,
			&record.Tokens.Value,
			&tokenSource,
		); err != nil {
			return nil, err
		}
		if turnID.Valid {
			record.TurnID = turnID.String
		}
		if text.Valid {
			record.Text = &text.String
		}
		if refs.Valid && strings.TrimSpace(refs.String) != "" {
			if err := json.Unmarshal([]byte(refs.String), &record.Refs); err != nil {
				return nil, err
			}
		}
		record.Tokens.Source = TokenCountSource(tokenSource)
		if raw.Valid {
			record.Raw = json.RawMessage(raw.String)
		}
		parsedCreatedAt, err := parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		record.CreatedAt = parsedCreatedAt
		records = append(records, record)
	}
	return records, rows.Err()
}

func normalizeRecord(record SessionRecord, seq int64, now time.Time) SessionRecord {
	if strings.TrimSpace(record.ID) == "" {
		record.ID = newID("rec")
	}
	if record.Seq == 0 {
		record.Seq = seq
	}
	if record.Kind == "" {
		record.Kind = RecordMessage
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.Text != nil {
		record.CharCount = int64(len([]rune(*record.Text)))
	}
	if record.Tokens.Value == 0 {
		record.Tokens = TokenCount{
			Value:  estimateTokens(record.CharCount),
			Source: TokenSourceCharEstimate,
		}
	} else if record.Tokens.Source == "" {
		record.Tokens.Source = TokenSourceTokenizer
	}
	return record
}

func updateUsageForAppendedRecord(usage SessionUsage, record SessionRecord) SessionUsage {
	usage.CharCount += record.CharCount
	usage.LastPromptTokens = TokenCount{
		Value:  estimateTokens(usage.CharCount),
		Source: TokenSourceCharEstimate,
	}
	if usage.ContextWindowTokens > 0 {
		usage.LastContextUsedPct = float64(usage.LastPromptTokens.Value) / float64(usage.ContextWindowTokens) * 100
	}
	return usage
}

func estimateTokens(chars int64) int64 {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return parsed, nil
}

func newID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
