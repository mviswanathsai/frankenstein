package contextrenderer

// Service implements the context_renderer capability contract.
type Service struct{}

// NewService returns a Service with no configuration.
func NewService() *Service {
	return &Service{}
}
