package contextbuilder

// Service implements the context_builder capability contract.
type Service struct {
	// Unopinionated makes Estimate return -1 for the budget fields the
	// builder does not want to own (tools, context, transcript), leaving
	// system prompt and output reservation concrete.
	Unopinionated bool
}

// NewService returns a Service with no configuration.
func NewService() *Service {
	return &Service{}
}
