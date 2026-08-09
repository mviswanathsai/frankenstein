package contextbuilder

type Service struct {
	Unopinionated bool
}

func NewService() *Service {
	return &Service{}
}
