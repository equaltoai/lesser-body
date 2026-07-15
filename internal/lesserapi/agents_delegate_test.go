package lesserapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDelegateAgentSendsContractAndDecodesSuccess(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/agents/delegate" {
			t.Fatalf("path = %s, want /api/v1/agents/delegate", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Fatalf("query = %q, want empty", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner-manage-token" {
			t.Fatalf("Authorization = %q, want caller bearer", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Fatalf("Idempotency-Key = %q, want none", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q", got)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if strings.Contains(string(bodyBytes), "owner-manage-token") {
			t.Fatalf("request body leaked bearer: %s", string(bodyBytes))
		}

		var body AgentDelegationRequest
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.AgentUsername != "ptah_agent" || body.DisplayName != "Ptah Agent" || body.Bio != "delegated runtime" {
			t.Fatalf("agent body fields = %+v", body)
		}
		if got, want := strings.Join(body.Scopes, ","), "read,write:statuses"; got != want {
			t.Fatalf("scopes = %q, want %q", got, want)
		}
		if body.ExpiresIn != 3600 || body.DeviceLabel != "ptah-instance-plane" {
			t.Fatalf("token options = %+v", body)
		}
		if info, ok := body.AgentInfo.(map[string]any); !ok || info["version"] != "1" {
			t.Fatalf("agent_info = %#v", body.AgentInfo)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successAgentDelegationResponse()))
	}))
	defer server.Close()

	client := newAgentDelegateTestClient(t, server)
	resp, err := client.DelegateAgent(context.Background(), " owner-manage-token ", AgentDelegationRequest{
		AgentUsername: "ptah_agent",
		DisplayName:   "Ptah Agent",
		Bio:           "delegated runtime",
		Scopes:        []string{"read", "write:statuses"},
		ExpiresIn:     3600,
		DeviceLabel:   "ptah-instance-plane",
		AgentInfo: map[string]any{
			"version": "1",
		},
	})
	if err != nil {
		t.Fatalf("DelegateAgent: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if resp.Account.ID != "https://lesser.example/users/ptah_agent" || resp.Account.Username != "ptah_agent" || !resp.Account.Bot {
		t.Fatalf("account = %+v", resp.Account)
	}
	if resp.Token.AccessToken != "mock-access-token" || resp.Token.RefreshToken != "mock-refresh-token" {
		t.Fatalf("token = %+v", resp.Token)
	}
	if resp.Token.TokenType != "Bearer" || resp.Token.ExpiresIn != 3600 || resp.Token.Scope != "read write:statuses" || resp.Token.CreatedAt != 1794744000 {
		t.Fatalf("token metadata = %+v", resp.Token)
	}
}

func TestDelegateAgentRequiresInputsBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newAgentDelegateTestClient(t, server)
	validReq := AgentDelegationRequest{AgentUsername: "ptah_agent", Scopes: []string{"read"}}

	cases := []struct {
		name        string
		bearerToken string
		req         AgentDelegationRequest
		want        string
	}{
		{
			name:        "empty bearer",
			bearerToken: " ",
			req:         validReq,
			want:        "bearer is required",
		},
		{
			name:        "empty username",
			bearerToken: "owner-manage-token",
			req:         AgentDelegationRequest{AgentUsername: " ", Scopes: []string{"read"}},
			want:        "agent username is required",
		},
		{
			name:        "empty scopes",
			bearerToken: "owner-manage-token",
			req:         AgentDelegationRequest{AgentUsername: "ptah_agent"},
			want:        "scopes are required",
		},
		{
			name:        "blank scope entry",
			bearerToken: "owner-manage-token",
			req:         AgentDelegationRequest{AgentUsername: "ptah_agent", Scopes: []string{"read", " "}},
			want:        "scopes cannot contain empty values",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DelegateAgent(context.Background(), tc.bearerToken, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "owner-manage-token") {
				t.Fatalf("error leaked bearer: %v", err)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestDelegateAgentReturnsSourceBackedAPIErrorStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusServiceUnavailable,
		http.StatusInternalServerError,
	} {
		status := status
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"error":"agent delegation failed","error_code":"AGENT_DELEGATION_TEST_%d"}`, status)
			}))
			defer server.Close()

			client := newAgentDelegateTestClient(t, server)
			_, err := client.DelegateAgent(context.Background(), "owner-manage-token", AgentDelegationRequest{AgentUsername: "ptah_agent", Scopes: []string{"read"}})
			if err == nil {
				t.Fatalf("expected API error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %T: %v", err, err)
			}
			if apiErr.Status != status {
				t.Fatalf("status = %d, want %d", apiErr.Status, status)
			}
			if !strings.Contains(string(apiErr.Body), fmt.Sprintf("AGENT_DELEGATION_TEST_%d", status)) {
				t.Fatalf("error body = %s", string(apiErr.Body))
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestDelegateAgentDoesNotRetryNonIdempotentFailures(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"persistence failed","error_code":"INTERNAL_ERROR"}`))
	}))
	defer server.Close()

	client := newAgentDelegateTestClient(t, server)
	_, err := client.DelegateAgent(context.Background(), "owner-manage-token", AgentDelegationRequest{AgentUsername: "ptah_agent", Scopes: []string{"read"}})
	if err == nil {
		t.Fatalf("expected API error")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly one non-idempotent call", requests)
	}
}

func TestAPIErrorErrorRedactsTokenFields(t *testing.T) {
	err := (&APIError{Status: http.StatusInternalServerError, Body: []byte(`{"error":"failed","access_token":"mock-access-token","refresh_token":"mock-refresh-token"}`)}).Error()
	if strings.Contains(err, "mock-access-token") || strings.Contains(err, "mock-refresh-token") {
		t.Fatalf("APIError leaked token fields: %s", err)
	}
	if !strings.Contains(err, `"access_token":"<redacted>"`) || !strings.Contains(err, `"refresh_token":"<redacted>"`) {
		t.Fatalf("APIError did not redact expected fields: %s", err)
	}
}

func newAgentDelegateTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	base, err := parseBaseURL(server.URL)
	if err != nil {
		t.Fatalf("parseBaseURL: %v", err)
	}
	return &Client{baseURL: base, http: server.Client()}
}

func successAgentDelegationResponse() string {
	return `{
		"account": {
			"id": "https://lesser.example/users/ptah_agent",
			"username": "ptah_agent",
			"acct": "ptah_agent",
			"display_name": "Ptah Agent",
			"locked": false,
			"bot": true,
			"discoverable": true,
			"group": false,
			"created_at": "2026-07-15T12:00:00Z",
			"note": "",
			"url": "https://lesser.example/@ptah_agent",
			"avatar": "https://lesser.example/avatars/original/missing.png",
			"avatar_static": "https://lesser.example/avatars/original/missing.png",
			"header": "https://lesser.example/headers/original/missing.png",
			"header_static": "https://lesser.example/headers/original/missing.png",
			"followers_count": 0,
			"following_count": 0,
			"statuses_count": 0,
			"last_status_at": "",
			"emojis": [],
			"fields": []
		},
		"token": {
			"access_token": "mock-access-token",
			"token_type": "Bearer",
			"expires_in": 3600,
			"refresh_token": "mock-refresh-token",
			"scope": "read write:statuses",
			"created_at": 1794744000
		}
	}`
}
