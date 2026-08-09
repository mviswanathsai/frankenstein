package contextbuilder

import (
	"errors"
	"testing"
)

func TestEstimate(t *testing.T) {
	tests := []struct {
		name        string
		svc         *Service
		req         EstimateRequest
		wantErrCode string // empty means no error expected
		check       func(t *testing.T, a Allocation)
	}{
		{
			name: "standard 128K window",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-1",
				Model:               "gpt-4",
				ContextWindowTokens: 128000,
			},
			check: func(t *testing.T, a Allocation) {
				assertPositive(t, "SystemPromptTokens", a.SystemPromptTokens)
				assertPositive(t, "MaxToolsTokens", a.MaxToolsTokens)
				assertPositive(t, "MaxContextTokens", a.MaxContextTokens)
				assertPositive(t, "MaxTranscriptTokens", a.MaxTranscriptTokens)
				assertPositive(t, "OutputReservation", a.OutputReservation)
				if a.RequestID != "req-1" {
					t.Errorf("RequestID = %q, want req-1", a.RequestID)
				}
			},
		},
		{
			name: "small window 2048",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-2",
				Model:               "test-model",
				ContextWindowTokens: 2048,
			},
			check: func(t *testing.T, a Allocation) {
				assertNonNegative(t, "SystemPromptTokens", a.SystemPromptTokens)
				assertNonNegative(t, "MaxToolsTokens", a.MaxToolsTokens)
				assertNonNegative(t, "MaxContextTokens", a.MaxContextTokens)
				assertNonNegative(t, "MaxTranscriptTokens", a.MaxTranscriptTokens)
				assertNonNegative(t, "OutputReservation", a.OutputReservation)
			},
		},
		{
			name: "unopinionated mode returns -1 for budget fields",
			svc:  &Service{Unopinionated: true},
			req: EstimateRequest{
				ID:                  "req-3",
				Model:               "gpt-4",
				ContextWindowTokens: 128000,
			},
			check: func(t *testing.T, a Allocation) {
				if a.MaxToolsTokens != -1 {
					t.Errorf("MaxToolsTokens = %d, want -1", a.MaxToolsTokens)
				}
				if a.MaxContextTokens != -1 {
					t.Errorf("MaxContextTokens = %d, want -1", a.MaxContextTokens)
				}
				if a.MaxTranscriptTokens != -1 {
					t.Errorf("MaxTranscriptTokens = %d, want -1", a.MaxTranscriptTokens)
				}
				// system_prompt_tokens and output_reservation stay concrete
				assertPositive(t, "SystemPromptTokens", a.SystemPromptTokens)
				assertPositive(t, "OutputReservation", a.OutputReservation)
			},
		},
		{
			name: "tiny window yields non-negative values",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-4",
				Model:               "test-model",
				ContextWindowTokens: 100,
			},
			check: func(t *testing.T, a Allocation) {
				assertNonNegative(t, "SystemPromptTokens", a.SystemPromptTokens)
				assertNonNegative(t, "MaxToolsTokens", a.MaxToolsTokens)
				assertNonNegative(t, "MaxContextTokens", a.MaxContextTokens)
				assertNonNegative(t, "MaxTranscriptTokens", a.MaxTranscriptTokens)
				assertNonNegative(t, "OutputReservation", a.OutputReservation)
			},
		},
		{
			name: "missing model returns invalid_request",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-5",
				Model:               "",
				ContextWindowTokens: 128000,
			},
			wantErrCode: FailureInvalidRequest,
		},
		{
			name: "zero context_window returns invalid_request",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-6",
				Model:               "gpt-4",
				ContextWindowTokens: 0,
			},
			wantErrCode: FailureInvalidRequest,
		},
		{
			name: "negative context_window returns invalid_request",
			svc:  &Service{},
			req: EstimateRequest{
				ID:                  "req-7",
				Model:               "gpt-4",
				ContextWindowTokens: -100,
			},
			wantErrCode: FailureInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alloc, err := tt.svc.Estimate(tt.req)

			if tt.wantErrCode != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var cbf *ContextBuilderFailure
				if !errors.As(err, &cbf) {
					t.Fatalf("error is not a *ContextBuilderFailure: %v", err)
				}
				if cbf.Code != tt.wantErrCode {
					t.Errorf("failure code = %q, want %q", cbf.Code, tt.wantErrCode)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, alloc)
			}
		})
	}
}

func assertPositive(t *testing.T, field string, v int) {
	t.Helper()
	if v <= 0 {
		t.Errorf("%s = %d, want > 0", field, v)
	}
}

func assertNonNegative(t *testing.T, field string, v int) {
	t.Helper()
	if v < 0 {
		t.Errorf("%s = %d, want >= 0", field, v)
	}
}
