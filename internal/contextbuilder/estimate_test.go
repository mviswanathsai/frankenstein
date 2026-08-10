package contextbuilder

import (
	"errors"
	"testing"
)

func TestEstimate(t *testing.T) {
	tests := []struct {
		name          string
		req           EstimateRequest
		unopinionated bool
		wantPositive  bool
		wantErrCode   string
	}{
		{
			name: "standard 128K window",
			req: EstimateRequest{
				ID:                  "req-standard",
				Model:               "claude-sonnet-4",
				ContextWindowTokens: 131072,
			},
			wantPositive: true,
		},
		{
			name: "undersized window",
			req: EstimateRequest{
				ID:                  "req-small",
				Model:               "claude-sonnet-4",
				ContextWindowTokens: 2048,
			},
		},
		{
			name: "unopinionated mode",
			req: EstimateRequest{
				ID:                  "req-unopinionated",
				Model:               "claude-sonnet-4",
				ContextWindowTokens: 131072,
			},
			unopinionated: true,
		},
		{
			name: "missing model",
			req: EstimateRequest{
				ID:                  "req-nomodel",
				ContextWindowTokens: 131072,
			},
			wantErrCode: FailureInvalidRequest,
		},
		{
			name: "zero context window",
			req: EstimateRequest{
				ID:    "req-zerowindow",
				Model: "claude-sonnet-4",
			},
			wantErrCode: FailureInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{Unopinionated: tt.unopinionated}
			got, err := s.Estimate(tt.req)

			if tt.wantErrCode != "" {
				if err == nil {
					t.Fatalf("Estimate() error = nil, want failure %q", tt.wantErrCode)
				}
				var fail ContextBuilderFailure
				if !errors.As(err, &fail) {
					t.Fatalf("Estimate() error %v does not wrap ContextBuilderFailure", err)
				}
				if fail.Code != tt.wantErrCode {
					t.Errorf("failure code = %q, want %q", fail.Code, tt.wantErrCode)
				}
				if fail.Retryable {
					t.Errorf("failure %q should not be retryable", fail.Code)
				}
				return
			}

			if err != nil {
				t.Fatalf("Estimate() error = %v, want nil", err)
			}
			if got.RequestID != tt.req.ID {
				t.Errorf("RequestID = %q, want %q", got.RequestID, tt.req.ID)
			}

			// System prompt and output reservation are always concrete.
			if got.SystemPromptTokens < 0 {
				t.Errorf("SystemPromptTokens = %d, want >= 0", got.SystemPromptTokens)
			}
			if got.OutputReservation < 0 {
				t.Errorf("OutputReservation = %d, want >= 0", got.OutputReservation)
			}

			if tt.unopinionated {
				if got.MaxToolsTokens != -1 {
					t.Errorf("MaxToolsTokens = %d, want -1", got.MaxToolsTokens)
				}
				if got.MaxContextTokens != -1 {
					t.Errorf("MaxContextTokens = %d, want -1", got.MaxContextTokens)
				}
				if got.MaxTranscriptTokens != -1 {
					t.Errorf("MaxTranscriptTokens = %d, want -1", got.MaxTranscriptTokens)
				}
			} else {
				if got.MaxToolsTokens < 0 {
					t.Errorf("MaxToolsTokens = %d, want >= 0", got.MaxToolsTokens)
				}
				if got.MaxContextTokens < 0 {
					t.Errorf("MaxContextTokens = %d, want >= 0", got.MaxContextTokens)
				}
				if got.MaxTranscriptTokens < 0 {
					t.Errorf("MaxTranscriptTokens = %d, want >= 0", got.MaxTranscriptTokens)
				}
			}

			if tt.wantPositive {
				for field, value := range map[string]int{
					"SystemPromptTokens":  got.SystemPromptTokens,
					"MaxToolsTokens":      got.MaxToolsTokens,
					"MaxContextTokens":    got.MaxContextTokens,
					"MaxTranscriptTokens": got.MaxTranscriptTokens,
					"OutputReservation":   got.OutputReservation,
				} {
					if value <= 0 {
						t.Errorf("%s = %d, want > 0", field, value)
					}
				}
			}
		})
	}
}
