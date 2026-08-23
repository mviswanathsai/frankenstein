package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

const (
	builtPrefixKey   = "built_prefix"
	stableContextKey = "stable_context"
)

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
	var dynamic *contextprovider.ContextResponse
	var catalog toolinvocation.ToolCatalog

	// Dynamic context is needed by the inner loop on every path.
	dynamic, err = k.getDynamicWithRetry(ctx, sessionID)
	if err != nil {
		return sessionID, err
	}

	if hasCached {
		builtPrefix = cachedPrefix

		// Still need the catalog for the inner loop.
		catalog, err = k.listToolsWithRetry(ctx, sessionID)
		if err != nil {
			return sessionID, err
		}
	} else {
		catalog, err = k.listToolsWithRetry(ctx, sessionID)
		if err != nil {
			return sessionID, err
		}

		stable, err := k.ensureStableContext(ctx, sessionID, sess)
		if err != nil {
			return sessionID, err
		}

		builtPrefix, err = k.assembleWithRetry(ctx, sessionID, model, stable.Candidates, catalog)
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
		transcript.Records, &catalog, []contextprovider.ContextResponse{*dynamic},
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

// loadStableContext returns the frozen stable response from session metadata,
// if any. The stable response survives process restarts the same way the
// built prefix does.
func loadStableContext(sess *session.Session) (*contextprovider.ContextResponse, bool) {
	raw, ok := sess.Metadata.Custom[stableContextKey]
	if !ok {
		return nil, false
	}
	var response contextprovider.ContextResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, false
	}
	return &response, true
}

// ensureStableContext returns the frozen stable response for the session,
// fetching and storing it once if it is not already present. Resumed sessions
// without a stored stable response (for example, sessions created before this
// key existed) fetch a fresh one here.
func (k *Kernel) ensureStableContext(ctx context.Context, sessionID string, sess *session.Session) (*contextprovider.ContextResponse, error) {
	if resp, ok := loadStableContext(sess); ok {
		return resp, nil
	}
	resp, err := k.getStableWithRetry(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := k.storeStableContext(ctx, sessionID, sess, resp); err != nil {
		return nil, fmt.Errorf("store stable context: %w", err)
	}
	return resp, nil
}

// storeStableContext writes the stable response into session metadata using
// the same full-replacement pattern as storeBuiltPrefix. Both helpers mutate
// the same in-memory session object, so keys written by one remain visible
// to the other.
func (k *Kernel) storeStableContext(ctx context.Context, sessionID string, sess *session.Session, resp *contextprovider.ContextResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal stable context: %w", err)
	}
	metadata := sess.Metadata
	if metadata.Custom == nil {
		metadata.Custom = make(map[string]json.RawMessage)
	}
	metadata.Custom[stableContextKey] = raw
	sess.Metadata = metadata
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

// kernelWorkspaceScope resolves the workspace scope the kernel grants to
// context-provider calls: the process working directory as both the single
// granted root and runtime.cwd. When the working directory cannot be
// resolved, the scope is empty — roots are required by the contract but may
// be empty, granting no filesystem access.
func kernelWorkspaceScope() ([]contextprovider.WorkspaceRoot, *contextprovider.RuntimeFacts) {
	wd, err := os.Getwd()
	if err != nil || strings.TrimSpace(wd) == "" {
		return []contextprovider.WorkspaceRoot{}, nil
	}
	return []contextprovider.WorkspaceRoot{{Path: wd}}, &contextprovider.RuntimeFacts{CWD: wd}
}

func (k *Kernel) getStableWithRetry(ctx context.Context, sessionID string) (*contextprovider.ContextResponse, error) {
	roots, runtime := kernelWorkspaceScope()
	var lastFailure *contextprovider.ContextFailure
	for attempt := 0; attempt < 3; attempt++ {
		response, failure := k.ctxProv.GetStableContext(ctx, contextprovider.StableContextRequest{
			ID:             "sctx_" + k.turnID,
			SessionID:      sessionID,
			Runtime:        runtime,
			WorkspaceRoots: roots,
		})
		if failure == nil {
			return response, nil
		}
		lastFailure = failure
		if !failure.Retryable {
			break
		}
	}
	return nil, fmt.Errorf("get_stable_context failed: %v", lastFailure)
}

func (k *Kernel) getDynamicWithRetry(ctx context.Context, sessionID string) (*contextprovider.ContextResponse, error) {
	roots, runtime := kernelWorkspaceScope()
	var lastFailure *contextprovider.ContextFailure
	for attempt := 0; attempt < 3; attempt++ {
		response, failure := k.ctxProv.GetDynamicContext(ctx, contextprovider.DynamicContextRequest{
			ID:             "ctx_" + k.turnID,
			SessionID:      sessionID,
			Reason:         "user_message",
			Runtime:        runtime,
			WorkspaceRoots: roots,
		})
		if failure == nil {
			return response, nil
		}
		lastFailure = failure
		if !failure.Retryable {
			break
		}
	}
	return nil, fmt.Errorf("get_dynamic_context failed: %v", lastFailure)
}

func (k *Kernel) assembleWithRetry(ctx context.Context, sessionID, model string, stableCandidates []contextprovider.ContextCandidate, catalog toolinvocation.ToolCatalog) (contextbuilder.BuiltPrefix, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		prefix, err := k.builder.Assemble(contextbuilder.AssembleRequest{
			ID:               "asm_" + k.turnID,
			SessionID:        sessionID,
			Model:            model,
			StableCandidates: stableCandidates,
			Catalog:          &catalog,
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
