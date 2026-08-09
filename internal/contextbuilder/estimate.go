package contextbuilder

// Estimate computes a token allocation for a transcript stub against the
// model's context window. It returns a concrete Allocation (or an error for
// invalid requests). When Service.Unopinionated is true, the three budget
// fields (max_tools_tokens, max_context_tokens, max_transcript_tokens) are
// set to -1, leaving those decisions to the caller.
func (s *Service) Estimate(req EstimateRequest) (Allocation, error) {
	if req.Model == "" {
		return Allocation{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureInvalidRequest,
			Message:   "model is required",
			Retryable: false,
		}
	}
	if req.ContextWindowTokens <= 0 {
		return Allocation{}, &ContextBuilderFailure{
			RequestID: req.ID,
			Code:      FailureInvalidRequest,
			Message:   "context_window_tokens must be positive",
			Retryable: false,
		}
	}

	window := req.ContextWindowTokens

	output := max(1024, window/4)
	system := min(2048, window/5)
	tools := min(2048, window/5)
	ctxBudget := min(2048, window/10)

	transcript := window - output - system - tools - ctxBudget

	// If the transcript budget is negative, reduce output_reservation
	// to compensate. If output_reservation goes below zero, the window
	// is non-viable — zero everything.
	if transcript < 0 {
		deficit := -transcript
		output -= deficit
		if output < 0 {
			output, system, tools, ctxBudget, transcript = 0, 0, 0, 0, 0
		} else {
			transcript = 0
		}
	}

	alloc := Allocation{
		RequestID:          req.ID,
		SystemPromptTokens: system,
		OutputReservation:  output,
	}

	if s.Unopinionated {
		alloc.MaxToolsTokens = -1
		alloc.MaxContextTokens = -1
		alloc.MaxTranscriptTokens = -1
	} else {
		alloc.MaxToolsTokens = tools
		alloc.MaxContextTokens = ctxBudget
		alloc.MaxTranscriptTokens = transcript
	}

	return alloc, nil
}
