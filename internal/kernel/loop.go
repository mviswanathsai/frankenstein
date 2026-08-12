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

// runTurn executes one full turn: session create/get, setup sequence,
// inner loop, and teardown. On new sessions, sessionID is empty and the
// kernel creates one. Returns the session ID and any error.
func (k *Kernel) runTurn(ctx context.Context, sessionID string, input NewInput) (string, error) {
	if len(input.Messages) == 0 {
		return "", errors.New("input.Messages is required")
	}

	isNew := sessionID == ""

	// --- Session creation or resumption ---
	if isNew {
		created, err := k.session.Create(ctx, session.CreateInput{
			Prompt: input.Messages[0],
		})
		if err != nil {
			return "", fmt.Errorf("session create: %w", err)
		}
		sessionID = created.ID

		// Batch-append subsequent messages.
		if err := k.appendUserMessages(ctx, sessionID, input.Messages[1:]); err != nil {
			return sessionID, err
		}
	} else {
		// Batch-append continue messages.
		if err := k.appendUserMessages(ctx, sessionID, input.Messages); err != nil {
			return sessionID, err
		}
	}

	// Load the full session after the writes.
	sess, err := k.session.Get(ctx, session.GetInput{ID: sessionID})
	if err != nil {
		return sessionID, fmt.Errorf("session get: %w", err)
	}

	// --- Model resolution ---
	model := k.resolveModel(sess, input)

	// --- Check session-level budget ---
	if sessionBudgetExceeded(k.cfg, sess.Usage) {
		return sessionID, fmt.Errorf("%w: session budget exceeded", errors.New(string(ExitBudgetExhausted)))
	}

	// --- Setup or reuse cached prefix ---
	cachedPrefix, hasCached := loadBuiltPrefix(sess)
	var builtPrefix contextbuilder.BuiltPrefix
	var bundles []contextprovider.ContextBundle
	var catalog toolinvocation.ToolCatalog

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
		catalog, err := k.listToolsWithRetry(ctx, sessionID)
		if err != nil {
			return sessionID, err
		}

		bundles, err := k.getContextWithRetry(ctx, sessionID, isNew)
		if err != nil {
			return sessionID, err
		}

		builtPrefix, err := k.assembleWithRetry(ctx, sessionID, model, bundles, catalog)
		if err != nil {
			return sessionID, err
		}

		if err := k.storeBuiltPrefix(ctx, sessionID, sess, builtPrefix); err != nil {
			return sessionID, err
		}
	}

	// --- Transcript ---
	transcript, err := k.getWithRetry(ctx, sessionID)
	if err != nil {
		return sessionID, err
	}

	// --- Inner loop ---
	result := runInnerLoop(ctx, k.cfg, k.tools, k.model, k.builder, k.observer,
		sessionID, k.turnID, model, builtPrefix,
		transcript.Records, &catalog, bundles,
	)

	// --- Append accumulated records ---
	if len(result.newRecords) > 0 {
		for _, rec := range result.newRecords {
			switch rec.Kind {
			case session.RecordToolResult:
				text := ""
				if rec.Text != nil {
					text = *rec.Text
				}
				if _, err := k.session.WriteToolResult(ctx, session.WriteToolResultInput{
					SessionID: sessionID,
					Text:      text,
					CallID:    rec.CallID,
					Refs:      rec.Refs,
				}); err != nil {
					return sessionID, fmt.Errorf("write tool result: %w", err)
				}
			case session.RecordToolCall:
				// Handle tool_call if any appear in newRecords (unlikely today, but be safe)
				if _, err := k.session.WriteToolCall(ctx, session.WriteToolCallInput{
					SessionID: sessionID,
					Name:      rec.ToolCalls[0].Name,
					Arguments: rec.ToolCalls[0].Arguments,
					CallID:    rec.CallID,
					Refs:      rec.Refs,
				}); err != nil {
					return sessionID, fmt.Errorf("write tool call: %w", err)
				}
			}
		}
	}

	// --- Append final assistant message ---
	if result.finalContent != "" {
		if _, err := k.session.WriteMessage(ctx, session.WriteMessageInput{
			SessionID: sessionID,
			Text:      result.finalContent,
			Role:      "assistant",
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

// storeBuiltPrefix writes the BuiltPrefix into session metadata. set_metadata
// is a full replacement, so the whole metadata object is rebuilt from the
// current session with the prefix added.
func (k *Kernel) storeBuiltPrefix(ctx context.Context, sessionID string, sess *session.Session, prefix contextbuilder.BuiltPrefix) error {
	raw, err := json.Marshal(prefix)
	if err != nil {
		return fmt.Errorf("marshal built prefix: %w", err)
	}
	metadata := sess.Metadata
	if metadata.Custom == nil {
		metadata.Custom = make(map[string]json.RawMessage)
	}
	metadata.Custom[builtPrefixKey] = raw
	_, err = k.session.SetMetadata(ctx, session.SetMetadataInput{
		SessionID: sessionID,
		Metadata:  metadata,
	})
	return err
}

// --- Setup helpers with retry ---

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

func (k *Kernel) getWithRetry(ctx context.Context, sessionID string) (*session.Session, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		sess, err := k.session.Get(ctx, session.GetInput{ID: sessionID})
		if err == nil {
			return sess, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("get failed: %w", lastErr)
}

// appendUserMessages writes a list of user messages to the session. Each
// message becomes a message record with role user; the session service
// assigns record identity and turn grouping.
func (k *Kernel) appendUserMessages(ctx context.Context, sessionID string, messages []string) error {
	for _, msg := range messages {
		if _, err := k.session.WriteMessage(ctx, session.WriteMessageInput{
			SessionID: sessionID,
			Text:      msg,
			Role:      "user",
		}); err != nil {
			return err
		}
	}
	return nil
}
