package contextbuilder

import "fmt"

// Error makes ContextBuilderFailure usable as an error while retaining its
// structured failure details through errors.As.
func (f *ContextBuilderFailure) Error() string {
	if f.Message == "" {
		return f.Code
	}
	return fmt.Sprintf("%s: %s", f.Code, f.Message)
}

// Estimate returns the default context-window allocation policy. The policy
// reserves space for the response first, then assigns bounded budgets to the
// prompt, tools, and context.
func (s *Service) Estimate(req EstimateRequest) (Allocation, error) {
	if req.Model == "" {
		return Allocation{}, invalidEstimateRequest(req.ID, "model is required")
	}
	if req.ContextWindowTokens <= 0 {
		return Allocation{}, invalidEstimateRequest(req.ID, "context_window_tokens must be positive")
	}

	window := req.ContextWindowTokens
	output := max(1024, window/4)
	system := min(2048, window/5)
	tools := min(2048, window/5)
	context := min(2048, window/10)

	// For very small windows, the initial output reservation can consume more
	// than the remaining window. Keep the allocation viable rather than
	// returning a negative transcript budget.
	transcript := window - output - system - tools - context
	if transcript < 0 {
		output = max(window-system-tools-context, 0)
		transcript = window - output - system - tools - context
		if transcript < 0 {
			output, system, tools, context, transcript = 0, 0, 0, 0, 0
		}
	}

	allocation := Allocation{
		RequestID:           req.ID,
		SystemPromptTokens:  system,
		MaxToolsTokens:      tools,
		MaxContextTokens:    context,
		MaxTranscriptTokens: transcript,
		OutputReservation:   output,
	}
	if s.Unopinionated {
		allocation.MaxToolsTokens = -1
		allocation.MaxContextTokens = -1
		allocation.MaxTranscriptTokens = -1
	}
	return allocation, nil
}

func invalidEstimateRequest(requestID, message string) error {
	failure := &ContextBuilderFailure{
		RequestID: requestID,
		Code:      FailureInvalidRequest,
		Message:   message,
		Retryable: false,
	}
	return fmt.Errorf("contextbuilder estimate: %w", failure)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
