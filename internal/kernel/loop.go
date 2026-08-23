package kernel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"frankenstein/internal/contextprovider"
	"frankenstein/internal/contextrenderer"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
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

	// --- Load or build the session config ---
	rendererConfig, err := k.getOrBuildConfig(ctx, sessionID, model)
	if err != nil {
		return sessionID, err
	}

	// --- Dynamic context (per turn) ---
	dynamic, err := k.getDynamicWithRetry(ctx, sessionID)
	if err != nil {
		return sessionID, err
	}

	// --- Transcript ---
	transcript, err := k.getWithRetry(ctx, sessionID)
	if err != nil {
		return sessionID, err
	}

	// --- Inner loop ---
	result := runInnerLoop(ctx, k.cfg, k.tools, k.model, k.renderer, k.observer,
		sessionID, k.turnID, model, rendererConfig, transcript.Records, dynamic,
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

// getOrBuildConfig returns the session's renderer config, reusing the stored
// config when it exists and its model matches the resolved model. A missing
// config, or a resolved model that differs from the stored config's model,
// triggers a rebuild. The config is held in kernel memory, never in session
// metadata.
func (k *Kernel) getOrBuildConfig(ctx context.Context, sessionID, model string) (contextrenderer.Config, error) {
	k.rendererConfigsMu.Lock()
	cfg, ok := k.rendererConfigs[sessionID]
	k.rendererConfigsMu.Unlock()
	if ok && cfg.Model == model {
		return cfg, nil
	}

	cfg, err := k.buildRendererConfig(ctx, sessionID, model)
	if err != nil {
		return contextrenderer.Config{}, err
	}

	k.rendererConfigsMu.Lock()
	k.rendererConfigs[sessionID] = cfg
	k.rendererConfigsMu.Unlock()
	return cfg, nil
}

// buildRendererConfig builds a fresh config for the session: one
// get_stable_context call (candidates mapped to material sections by their
// metadata slot convention), one list_tools call (the frozen catalog), and
// the resolved model.
func (k *Kernel) buildRendererConfig(ctx context.Context, sessionID, model string) (contextrenderer.Config, error) {
	stable, err := k.getStableWithRetry(ctx, sessionID)
	if err != nil {
		return contextrenderer.Config{}, err
	}
	catalog, err := k.listToolsWithRetry(ctx, sessionID)
	if err != nil {
		return contextrenderer.Config{}, err
	}
	return contextrenderer.Config{
		Material: materialSectionsFromCandidates(stable.Candidates),
		Tools:    &catalog,
		Model:    model,
	}, nil
}

// materialSectionsFromCandidates maps stable candidates to material sections,
// taking the section name from the candidate's metadata slot convention and
// skipping candidates with no usable name.
func materialSectionsFromCandidates(candidates []contextprovider.ContextCandidate) []contextrenderer.MaterialSection {
	sections := make([]contextrenderer.MaterialSection, 0, len(candidates))
	for _, c := range candidates {
		name, _ := c.Metadata[contextprovider.MetadataKeySlot].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		sections = append(sections, contextrenderer.MaterialSection{
			Name:    name,
			Content: c.Content,
		})
	}
	return sections
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
