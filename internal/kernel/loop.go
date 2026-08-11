package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const builtPrefixKey = "built_prefix"

// runTurn executes one full turn: session create/resume, setup sequence,
// inner loop, and teardown. On new sessions, sessionID is empty and the
// kernel creates one. Returns the session ID and any error.
func (k *Kernel) runTurn(ctx context.Context, sessionID string, input NewInput) (string, error) {
	if len(input.Messages) == 0 {
		return "", errors.New("input.Messages is required")
	}

	isNew := sessionID == ""

	// --- Session creation or resumption ---
	var sess *session.Session
	if isNew {
		var err error
		sess, err = k.session.Create(ctx, session.CreateInput{
			Prompt: input.Messages[0],
			TurnID: k.turnID,
		})
		if err != nil {
			return "", fmt.Errorf("session create: %w", err)
		}
		sessionID = sess.ID

		// Append subsequent messages as additional session records.
		for i := 1; i < len(input.Messages); i++ {
			msg := input.Messages[i]
			_, err = k.session.Mutate(ctx, session.MutateInput{
				ID: sessionID,
				Ops: []session.MutationOp{{
					Type: session.MutationAppendRecord,
					Record: &session.SessionRecord{
						TurnID: k.turnID,
						Kind:   session.RecordMessage,
						Role:   "user",
						Text:   &msg,
					},
				}},
			})
			if err != nil {
				return sessionID, fmt.Errorf("session append message %d: %w", i, err)
			}
		}
	} else {
		var err error
		sess, err = k.session.Resume(ctx, session.ResumeInput{ID: sessionID})
		if err != nil {
			return sessionID, fmt.Errorf("session resume: %w", err)
		}

		// Append continue messages as session records.
		for _, msg := range input.Messages {
			_, err = k.session.Mutate(ctx, session.MutateInput{
				ID: sessionID,
				Ops: []session.MutationOp{{
					Type: session.MutationAppendRecord,
					Record: &session.SessionRecord{
						TurnID: k.turnID,
						Kind:   session.RecordMessage,
						Role:   "user",
						Text:   &msg,
					},
				}},
			})
			if err != nil {
				return sessionID, fmt.Errorf("session append message: %w", err)
			}
		}
	}

	// --- Model resolution ---
	model := k.resolveModel(sess, input)

	// --- Check session-level budget on continue ---
	if !isNew && sessionBudgetExceeded(k.cfg, sess.Usage) {
		return sessionID, fmt.Errorf("%s: session budget exhausted", ExitBudgetExhausted)
	}

	// --- Setup or reuse cached prefix ---
	cachedPrefix, hasCached := loadBuiltPrefix(sess)
	var builtPrefix contextbuilder.BuiltPrefix
	var bundles []contextprovider.ContextBundle
	var catalog toolinvocation.ToolCatalog
	var outputBudget int

	if hasCached {
		builtPrefix = cachedPrefix

		// Still need catalog and context for the inner loop.
		var err error
		catalog, err = k.listToolsWithRetry(ctx, sessionID)
		if err != nil {
			return sessionID, err
		}
		bundles, err = k.getContextWithRetry(ctx, sessionID, isNew)
		if err != nil {
			return sessionID, err
		}
	} else {
		allocation, err := k.estimateWithRetry(sess, model)
		if err != nil {
			return sessionID, err
		}
		outputBudget = allocation.OutputReservation

		catalog, err = k.listToolsWithRetry(ctx, sessionID)
		if err != nil {
			return sessionID, err
		}

		bundles, err = k.getContextWithRetry(ctx, sessionID, isNew)
		if err != nil {
			return sessionID, err
		}

		builtPrefix, err = k.assembleWithRetry(ctx, sessionID, model, bundles, catalog)
		if err != nil {
			return sessionID, err
		}

		if err := k.storeBuiltPrefix(ctx, sessionID, sess, builtPrefix); err != nil {
			return sessionID, err
		}
	}

	// --- Materialize ---
	materialized, err := k.materializeWithRetry(ctx, sessionID)
	if err != nil {
		return sessionID, err
	}

	// --- Inner loop ---
	result := runInnerLoop(ctx, k.cfg, k.tools, k.model, k.builder, k.observer,
		sessionID, k.turnID, model, builtPrefix,
		materialized.Records, &catalog, outputBudget, bundles,
	)

	// --- Append accumulated records ---
	if len(result.newRecords) > 0 {
		ops := make([]session.MutationOp, len(result.newRecords))
		for i, rec := range result.newRecords {
			rec := rec
			ops[i] = session.MutationOp{
				Type:   session.MutationAppendRecord,
				Record: &rec,
			}
		}
		if _, err := k.session.Mutate(ctx, session.MutateInput{
			ID:  sessionID,
			Ops: ops,
		}); err != nil {
			return sessionID, fmt.Errorf("append inner loop records: %w", err)
		}
	}

	// --- Append final assistant record ---
	if result.finalContent != "" {
		finalRec := buildAssistantRecord(k.turnID, result.finalContent)
		if _, err := k.session.Mutate(ctx, session.MutateInput{
			ID: sessionID,
			Ops: []session.MutationOp{{
				Type:   session.MutationAppendRecord,
				Record: &finalRec,
			}},
		}); err != nil {
			return sessionID, fmt.Errorf("append final record: %w", err)
		}
	}

	// --- Observer notification ---
	if k.observer != nil {
		k.observer.OnTurnEnd(result.exitReason, result.finalContent)
	}

	return sessionID, nil
}

// resolveModel picks the model for this turn: per-turn override, then
// session metadata, then the kernel's configured default.
func (k *Kernel) resolveModel(sess *session.Session, input NewInput) string {
	if input.Model != "" {
		return input.Model
	}
	if sess != nil && sess.Metadata.Model != "" {
		return sess.Metadata.Model
	}
	return k.cfg.DefaultModel
}

// loadBuiltPrefix returns the stored BuiltPrefix from session metadata, if any.
func loadBuiltPrefix(sess *session.Session) (contextbuilder.BuiltPrefix, bool) {
	raw, ok := sess.Metadata.Custom[builtPrefixKey]
	if !ok {
		return contextbuilder.BuiltPrefix{}, false
	}
	var prefix contextbuilder.BuiltPrefix
	if err := json.Unmarshal(raw, &prefix); err != nil {
		return contextbuilder.BuiltPrefix{}, false
	}
	return prefix, true
}

// storeBuiltPrefix writes the BuiltPrefix into session metadata.
func (k *Kernel) storeBuiltPrefix(ctx context.Context, sessionID string, sess *session.Session, prefix contextbuilder.BuiltPrefix) error {
	raw, err := json.Marshal(prefix)
	if err != nil {
		return fmt.Errorf("marshal built prefix: %w", err)
	}
	custom := sess.Metadata.Custom
	if custom == nil {
		custom = make(map[string]json.RawMessage)
	}
	custom[builtPrefixKey] = raw
	_, err = k.session.Mutate(ctx, session.MutateInput{
		ID: sessionID,
		Ops: []session.MutationOp{{
			Type: session.MutationSetMetadata,
			Metadata: &session.SessionMetadata{
				Custom: custom,
			},
		}},
	})
	return err
}

// --- Setup helpers with retry ---

func (k *Kernel) estimateWithRetry(sess *session.Session, model string) (contextbuilder.Allocation, error) {
	firstRecordText := ""
	if len(sess.Records) > 0 && sess.Records[0].Text != nil {
		firstRecordText = *sess.Records[0].Text
	}
	tokenEstimate := int64(len(firstRecordText)) / 4

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		alloc, err := k.builder.Estimate(contextbuilder.EstimateRequest{
			ID:                  "est_" + k.turnID,
			Model:               model,
			ContextWindowTokens: 128000,
			Stub: contextbuilder.TranscriptStub{
				MessageCount:    len(sess.Records),
				EstimatedTokens: int(tokenEstimate),
			},
		})
		if err == nil {
			return alloc, nil
		}
		lastErr = err
		var cbErr contextbuilder.ContextBuilderFailure
		if errors.As(err, &cbErr) && !cbErr.Retryable {
			break
		}
	}
	return contextbuilder.Allocation{}, fmt.Errorf("estimate failed: %w", lastErr)
}

func (k *Kernel) listToolsWithRetry(ctx context.Context, sessionID string) (toolinvocation.ToolCatalog, error) {
	var lastFailure *toolinvocation.ToolCatalogFailure
	for attempt := 0; attempt < 3; attempt++ {
		listed, failure := k.tools.ListTools(ctx, toolinvocation.ToolCatalogRequest{
			ID:        "cat_" + k.turnID,
			SessionID: sessionID,
			TurnID:    k.turnID,
		})
		if failure == nil {
			return listed.Catalog, nil
		}
		lastFailure = failure
		if !failure.Retryable {
			break
		}
	}
	return toolinvocation.ToolCatalog{}, fmt.Errorf("list_tools failed: %v", lastFailure)
}

func (k *Kernel) getContextWithRetry(ctx context.Context, sessionID string, isNew bool) ([]contextprovider.ContextBundle, error) {
	var lastFailure *contextprovider.ContextFailure
	for attempt := 0; attempt < 3; attempt++ {
		var bundle *contextprovider.ContextBundle
		var failure *contextprovider.ContextFailure

		if isNew {
			bundle, failure = k.ctxProv.Initialize(ctx, contextprovider.ContextInitializeRequest{
				ID:        "ctx_" + k.turnID,
				SessionID: sessionID,
				Runtime: contextprovider.RuntimeFacts{
					CurrentDate: time.Now().UTC().Format(time.RFC3339),
				},
			})
		} else {
			bundle, failure = k.ctxProv.GetContext(ctx, contextprovider.ContextRequest{
				ID:        "ctx_" + k.turnID,
				SessionID: sessionID,
			})
		}
		if failure == nil {
			return []contextprovider.ContextBundle{*bundle}, nil
		}
		lastFailure = failure
		if failure.Retryable != nil && !*failure.Retryable {
			break
		}
	}
	return nil, fmt.Errorf("get_context failed: %v", lastFailure)
}

func (k *Kernel) assembleWithRetry(ctx context.Context, sessionID, model string, bundles []contextprovider.ContextBundle, catalog toolinvocation.ToolCatalog) (contextbuilder.BuiltPrefix, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		prefix, err := k.builder.Assemble(contextbuilder.AssembleRequest{
			ID:             "asm_" + k.turnID,
			SessionID:      sessionID,
			Model:          model,
			ContextBundles: bundles,
			Catalog:        &catalog,
		})
		if err == nil {
			return prefix, nil
		}
		lastErr = err
		var cbErr contextbuilder.ContextBuilderFailure
		if errors.As(err, &cbErr) && !cbErr.Retryable {
			break
		}
	}
	return contextbuilder.BuiltPrefix{}, fmt.Errorf("assemble failed: %w", lastErr)
}

func (k *Kernel) materializeWithRetry(ctx context.Context, sessionID string) (*session.MaterializedSession, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		materialized, err := k.session.Materialize(ctx, session.MaterializeInput{ID: sessionID})
		if err == nil {
			return materialized, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("materialize failed: %w", lastErr)
}
