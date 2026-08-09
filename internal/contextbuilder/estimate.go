package contextbuilder

import "fmt"

// Estimate divides the model's context window into per-input token budgets.
// The default policy reserves concrete numbers for every field; when
// Service.Unopinionated is set, the three budget fields the builder does not
// want to own are returned as -1 so the caller can fill them.
func (s *Service) Estimate(req EstimateRequest) (Allocation, error) {
	if req.Model == "" {
		return Allocation{}, invalidRequest(req.ID, "model is required")
	}
	if req.ContextWindowTokens <= 0 {
		return Allocation{}, invalidRequest(req.ID, "context_window_tokens must be greater than zero")
	}

	window := req.ContextWindowTokens

	output := max(1024, window/4)
	system := min(2048, window/5)
	tools := min(2048, window/5)
	context := min(2048, window/10)
	transcript := window - output - system - tools - context

	// An undersized window cannot host every budget at its floor. Shrink
	// output_reservation to compensate and zero the transcript so no budget
	// goes negative.
	if transcript < 0 {
		output += transcript
		transcript = 0
		if output < 0 {
			output = 0
		}
	}

	if s.Unopinionated {
		tools = -1
		context = -1
		transcript = -1
	}

	return Allocation{
		RequestID:           req.ID,
		SystemPromptTokens:  system,
		MaxToolsTokens:      tools,
		MaxContextTokens:    context,
		MaxTranscriptTokens: transcript,
		OutputReservation:   output,
	}, nil
}

// invalidRequest builds an error wrapping a non-retryable invalid_request
// failure, recoverable with errors.As.
func invalidRequest(requestID, message string) error {
	return fmt.Errorf("%w", ContextBuilderFailure{
		RequestID: requestID,
		Code:      FailureInvalidRequest,
		Message:   message,
		Retryable: false,
	})
}

// Error lets a ContextBuilderFailure be wrapped and recovered with errors.As.
func (f ContextBuilderFailure) Error() string {
	return fmt.Sprintf("context_builder failure %q (request %q)", f.Code, f.RequestID)
}
