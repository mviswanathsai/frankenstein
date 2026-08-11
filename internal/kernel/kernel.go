package kernel

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kernel is the harness turn loop. It sequences capability calls, owns the
// continue/stop decision, and enforces budgets, retry, and cancellation.
type Kernel struct {
	mu    sync.Mutex
	state KernelState
	cfg   Config

	tools   ToolInvoker
	model   ModelInvoker
	session SessionStore
	builder ContextBuilder
	ctxProv ContextProvider

	turnID    string
	observer  TurnObserver
	cancelFn  context.CancelFunc
	cancelCtx context.Context
}

var ErrBusy = errors.New("kernel is busy")
var ErrInvalidSession = errors.New("session_id is required")

// New creates a kernel with the given config and capability services.
// All service parameters are required (no nil checks in v0).
func New(cfg Config, tools ToolInvoker, model ModelInvoker, session SessionStore, builder ContextBuilder, ctxProv ContextProvider) *Kernel {
	return &Kernel{
		state:   KernelIdle,
		cfg:     cfg,
		tools:   tools,
		model:   model,
		session: session,
		builder: builder,
		ctxProv: ctxProv,
	}
}

// New starts a new session and runs a turn. It blocks until the turn ends or
// is cancelled. Returns the created session_id and any error from the turn.
func (k *Kernel) New(ctx context.Context, input NewInput) (sessionID string, err error) {
	k.mu.Lock()
	if k.state != KernelIdle {
		k.mu.Unlock()
		return "", ErrBusy
	}
	k.state = KernelRunning
	k.mu.Unlock()

	k.cancelCtx, k.cancelFn = context.WithCancel(ctx)
	k.turnID = "turn_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	k.observer = nil

	defer func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if r := recover(); r != nil {
			err = fmt.Errorf("kernel panic: %v", r)
		}
		k.state = KernelIdle
		k.cancelFn = nil
		k.cancelCtx = nil
		k.turnID = ""
		k.observer = nil
	}()

	return k.runTurn(k.cancelCtx, "", input)
}

// Continue runs another turn on an existing session. It blocks until the turn
// ends or is cancelled. Returns only an error; the session already exists.
func (k *Kernel) Continue(ctx context.Context, input ContinueInput) (err error) {
	if strings.TrimSpace(input.SessionID) == "" {
		return ErrInvalidSession
	}

	k.mu.Lock()
	if k.state != KernelIdle {
		k.mu.Unlock()
		return ErrBusy
	}
	k.state = KernelRunning
	k.mu.Unlock()

	k.cancelCtx, k.cancelFn = context.WithCancel(ctx)
	k.turnID = "turn_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	k.observer = nil

	defer func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if r := recover(); r != nil {
			err = fmt.Errorf("kernel panic: %v", r)
		}
		k.state = KernelIdle
		k.cancelFn = nil
		k.cancelCtx = nil
		k.turnID = ""
		k.observer = nil
	}()

	_, err = k.runTurn(k.cancelCtx, input.SessionID, NewInput{
		Messages:      input.Messages,
		Model:         input.Model,
		ThinkingLevel: input.ThinkingLevel,
	})
	return err
}

// Cancel requests termination of the current turn. It transitions the kernel
// from running to cancelling and propagates cancellation to in-flight
// capability calls. It does not block. When idle or already cancelling, it is
// a no-op.
func (k *Kernel) Cancel() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == KernelRunning {
		k.state = KernelCancelling
		if k.cancelFn != nil {
			k.cancelFn()
		}
	}
}

// runTurn executes one turn loop. Stub: to be implemented by ticket #6
// (kernel-loop).
func (k *Kernel) runTurn(ctx context.Context, sessionID string, input NewInput) (string, error) {
	// Stub: to be implemented by ticket #6 (kernel-loop).
	return "", fmt.Errorf("runTurn not implemented")
}
