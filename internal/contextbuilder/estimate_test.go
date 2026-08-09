package contextbuilder

import (
	"errors"
	"testing"
)

func TestEstimate(t *testing.T) {
	tests := []struct {
		name          string
		service       *Service
		req           EstimateRequest
		wantErrorCode string
		check         func(t *testing.T, allocation Allocation)
	}{
		{
			name:    "standard window",
			service: NewService(),
			req: EstimateRequest{
				ID:                  "estimate-128k",
				Model:               "model",
				ContextWindowTokens: 128 * 1024,
			},
			check: func(t *testing.T, allocation Allocation) {
				t.Helper()
				if allocation.RequestID != "estimate-128k" {
					t.Errorf("RequestID = %q, want estimate-128k", allocation.RequestID)
				}
				if allocation.SystemPromptTokens <= 0 || allocation.MaxToolsTokens <= 0 ||
					allocation.MaxContextTokens <= 0 || allocation.MaxTranscriptTokens <= 0 ||
					allocation.OutputReservation <= 0 {
					t.Errorf("allocation contains non-positive values: %+v", allocation)
				}
			},
		},
		{
			name:    "small window",
			service: NewService(),
			req:     EstimateRequest{Model: "model", ContextWindowTokens: 2048},
			check: func(t *testing.T, allocation Allocation) {
				t.Helper()
				if allocation.SystemPromptTokens < 0 || allocation.MaxToolsTokens < 0 ||
					allocation.MaxContextTokens < 0 || allocation.MaxTranscriptTokens < 0 ||
					allocation.OutputReservation < 0 {
					t.Errorf("allocation contains negative values: %+v", allocation)
				}
			},
		},
		{
			name:    "unopinionated",
			service: &Service{Unopinionated: true},
			req:     EstimateRequest{Model: "model", ContextWindowTokens: 128 * 1024},
			check: func(t *testing.T, allocation Allocation) {
				t.Helper()
				if allocation.MaxToolsTokens != -1 || allocation.MaxContextTokens != -1 || allocation.MaxTranscriptTokens != -1 {
					t.Errorf("unopinionated budgets = %+v, want -1 for tools/context/transcript", allocation)
				}
				if allocation.SystemPromptTokens < 0 || allocation.OutputReservation < 0 {
					t.Errorf("concrete budgets contain negative values: %+v", allocation)
				}
			},
		},
		{
			name:          "missing model",
			service:       NewService(),
			req:           EstimateRequest{ContextWindowTokens: 128 * 1024},
			wantErrorCode: FailureInvalidRequest,
		},
		{
			name:          "zero context window",
			service:       NewService(),
			req:           EstimateRequest{Model: "model"},
			wantErrorCode: FailureInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocation, err := tt.service.Estimate(tt.req)
			if tt.wantErrorCode != "" {
				if err == nil {
					t.Fatal("Estimate() error = nil, want error")
				}
				var failure *ContextBuilderFailure
				if !errors.As(err, &failure) {
					t.Fatalf("Estimate() error = %v, want ContextBuilderFailure", err)
				}
				if failure.Code != tt.wantErrorCode || failure.Retryable {
					t.Errorf("failure = %+v, want code %q and non-retryable", failure, tt.wantErrorCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Estimate() error = %v", err)
			}
			tt.check(t, allocation)
		})
	}
}
