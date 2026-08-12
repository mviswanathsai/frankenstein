package kernel

import (
	"time"
)

// KernelState is the kernel's lifecycle state.
type KernelState string

const (
	KernelIdle       KernelState = "idle"
	KernelRunning    KernelState = "running"
	KernelCancelling KernelState = "cancelling"
)

// ExitReason records why a turn ended.
type ExitReason string

const (
	ExitCompleted         ExitReason = "completed"
	ExitToolStopRequested ExitReason = "tool_stop_requested"
	ExitBudgetExhausted   ExitReason = "budget_exhausted"
	ExitModelError        ExitReason = "model_error"
	ExitToolError         ExitReason = "tool_error"
	ExitCancelled         ExitReason = "cancelled"
	ExitInternalError     ExitReason = "internal_error"
)

// NewInput is the frontend's request to start a new session.
type NewInput struct {
	Messages      []string
	Model         string
	ThinkingLevel string
}

// ContinueInput is the frontend's request to continue an existing session.
type ContinueInput struct {
	SessionID     string
	Messages      []string
	Model         string
	ThinkingLevel string
}

// Config holds kernel-level defaults and budgets.
type Config struct {
	DefaultModel       string
	DefaultThinking    string
	TurnBudget         int
	MaxRetries         int
	MaxOutputRetries   int
	OutputBudgetRaise  float64
	// OutputBudget is the initial per-call output token budget, applied as
	// max_output_tokens on each model invocation. Zero means no explicit cap.
	OutputBudget       int
	SessionTokenLimit  int64
	CancelDrainTimeout time.Duration
}

// DefaultConfig returns Config with the contract-mandated defaults.
func DefaultConfig() Config {
	return Config{
		TurnBudget:         90,
		MaxRetries:         3,
		MaxOutputRetries:   2,
		OutputBudgetRaise:  0.2,
		CancelDrainTimeout: 1 * time.Second,
	}
}
