package contextbuilder

// Service implements the context_builder capability.
type Service struct {
	// Unopinionated, when true, returns -1 for max_tools_tokens,
	// max_context_tokens, and max_transcript_tokens, leaving those
	// budget decisions to the caller.
	Unopinionated bool
}

// NewService creates a Service.
func NewService() *Service {
	return &Service{}
}

// Prepare normalizes a transcript into a model input.
// Defined in prepare.go.
