package mcpserver

import (
	"net/http"
	"testing"

	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestCommToolResultFromError_MapsCommAPIErrors(t *testing.T) {
	res, err := commToolResultFromError(&soulapi.APIError{
		Status: 403,
		Body:   []byte(`{"error":{"message":"blocked by policy"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected isError tool result, got %+v", res)
	}
	errPayload, ok := res.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent.error, got %+v", res.StructuredContent)
	}
	if errPayload["code"] != "boundary_violation" {
		t.Fatalf("expected boundary_violation code, got %v", errPayload["code"])
	}
	if errPayload["status"] != 403 {
		t.Fatalf("expected status 403, got %v", errPayload["status"])
	}

	res, err = commToolResultFromError(&soulapi.APIError{
		Status:  429,
		Body:    []byte(`rate limited`),
		Headers: http.Header{"Retry-After": []string{"12"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errPayload, ok = res.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent.error, got %+v", res.StructuredContent)
	}
	if errPayload["code"] != "rate_limited" {
		t.Fatalf("expected rate_limited code, got %v", errPayload["code"])
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["retryAfterSeconds"] != 12 {
		t.Fatalf("expected retryAfterSeconds=12, got %v", details["retryAfterSeconds"])
	}
}

func TestCommToolResultFromError_PreservesLesserAuthContract(t *testing.T) {
	tests := []struct {
		name                  string
		status                int
		body                  string
		headers               http.Header
		wantError             string
		wantDescription       string
		wantScope             string
		wantAction            string
		wantRefreshRequired   bool
		wantReauthorize       bool
		wantRetryAfterSeconds int
	}{
		{
			name:                "invalid token refresh",
			status:              401,
			body:                `{"error":"invalid_token","error_description":"invalid token"}`,
			wantError:           "invalid_token",
			wantDescription:     "invalid token",
			wantAction:          "refresh",
			wantRefreshRequired: true,
		},
		{
			name:            "insufficient scope fails",
			status:          403,
			body:            `{"error":"insufficient_scope","error_description":"insufficient scope: requires read","scope":"read"}`,
			wantError:       "insufficient_scope",
			wantDescription: "insufficient scope: requires read",
			wantScope:       "read",
			wantAction:      "fail",
			wantReauthorize: false,
		},
		{
			name:            "invalid grant requires reauth",
			status:          400,
			body:            `{"error":"invalid_grant","error_description":"Invalid or expired refresh token"}`,
			wantError:       "invalid_grant",
			wantDescription: "Invalid or expired refresh token",
			wantAction:      "reauth",
			wantReauthorize: true,
		},
		{
			name:            "invalid client requires reconfigure",
			status:          401,
			body:            `{"error":"invalid_client","error_description":"Invalid client credentials"}`,
			wantError:       "invalid_client",
			wantDescription: "Invalid client credentials",
			wantAction:      "reconfigure",
			wantReauthorize: false,
		},
		{
			name:                  "slow down backs off",
			status:                429,
			body:                  `{"error":"slow_down","error_description":"Too many client_credentials token requests"}`,
			headers:               http.Header{"Retry-After": []string{"23"}},
			wantError:             "slow_down",
			wantDescription:       "Too many client_credentials token requests",
			wantAction:            "backoff",
			wantRetryAfterSeconds: 23,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := commToolResultFromError(&soulapi.APIError{
				Status:  tt.status,
				Body:    []byte(tt.body),
				Headers: tt.headers,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res == nil || !res.IsError {
				t.Fatalf("expected isError tool result, got %+v", res)
			}

			errPayload, ok := res.StructuredContent["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected structuredContent.error, got %+v", res.StructuredContent)
			}
			if errPayload["status"] != tt.status {
				t.Fatalf("expected status=%d, got %+v", tt.status, errPayload)
			}
			if errPayload["code"] != tt.wantError {
				t.Fatalf("expected code=%q, got %+v", tt.wantError, errPayload)
			}
			if errPayload["error"] != tt.wantError {
				t.Fatalf("expected error=%q, got %+v", tt.wantError, errPayload)
			}
			if errPayload["error_description"] != tt.wantDescription {
				t.Fatalf("expected error_description=%q, got %+v", tt.wantDescription, errPayload)
			}
			if tt.wantScope != "" && errPayload["scope"] != tt.wantScope {
				t.Fatalf("expected scope=%q, got %+v", tt.wantScope, errPayload)
			}

			details, _ := errPayload["details"].(map[string]any)
			if details["source"] != "soul_api" {
				t.Fatalf("expected source=soul_api, got %+v", details)
			}
			if details["authAction"] != tt.wantAction {
				t.Fatalf("expected authAction=%q, got %+v", tt.wantAction, details)
			}
			if details["refreshRequired"] != tt.wantRefreshRequired {
				t.Fatalf("expected refreshRequired=%t, got %+v", tt.wantRefreshRequired, details)
			}
			if details["reauthorize"] != tt.wantReauthorize {
				t.Fatalf("expected reauthorize=%t, got %+v", tt.wantReauthorize, details)
			}
			if tt.wantRetryAfterSeconds > 0 && details["retryAfterSeconds"] != tt.wantRetryAfterSeconds {
				t.Fatalf("expected retryAfterSeconds=%d, got %+v", tt.wantRetryAfterSeconds, details)
			}

			apiError, _ := details["apiError"].(map[string]any)
			if apiError["error"] != tt.wantError {
				t.Fatalf("expected preserved apiError.error=%q, got %+v", tt.wantError, apiError)
			}
		})
	}
}
