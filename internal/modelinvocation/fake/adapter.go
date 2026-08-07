package fake

import (
	"context"
	"errors"

	"frankenstein/internal/modelinvocation"
)

// Adapter is a scriptable fake ProviderAdapter that emits a pre-configured
// sequence of fragments. Used by service tests to simulate provider behavior
// without real API calls.
type Adapter struct {
	fragments []modelinvocation.Fragment
}

// NewAdapter creates a fake adapter that emits the given fragments in order,
// then closes the channel. A nil fragments slice simulates "connection failed":
// Invoke returns an error.
func NewAdapter(fragments []modelinvocation.Fragment) *Adapter {
	return &Adapter{fragments: fragments}
}

// Invoke implements modelinvocation.ProviderAdapter.
//
// It starts a goroutine that emits fragments one by one on a buffered channel,
// then closes the channel. Between each fragment, it checks ctx.Done() and
// stops if the context is cancelled.
//
// Returns (nil, error) if the adapter was configured with nil fragments
// (simulating a connection failure).
func (a *Adapter) Invoke(ctx context.Context, req modelinvocation.ProviderRequest) (<-chan modelinvocation.Fragment, error) {
	if a.fragments == nil {
		return nil, errors.New("fake: connection failed")
	}

	ch := make(chan modelinvocation.Fragment, len(a.fragments))

	go func() {
		defer close(ch)
		for _, f := range a.fragments {
			select {
			case <-ctx.Done():
				return
			case ch <- f:
			}
		}
	}()

	return ch, nil
}
