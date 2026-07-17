package lesserapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAgentsReadsPublicDirectoryWithoutCallerBearer(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/agents" {
			t.Fatalf("path = %s, want /api/v1/agents", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Fatalf("query = %q, want empty", r.URL.RawQuery)
		}
		authorization = r.Header.Get("Authorization")
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"username":"scout",
				"display_name":"Scout",
				"bio":"public directory entry",
				"created_at":"2026-07-15T12:00:00Z",
				"verified":true,
				"agent_type":"CUSTOM",
				"agent_version":"1",
				"agent_capabilities":{"can_post":true,"can_reply":true,"max_posts_per_hour":10},
				"mcp_access":{"mcp_url":"https://api.example.com/mcp/scout","scopes":["read"]},
				"identity_semantics":{"identity_state":"DRONE","soul_agent_id":"private-soul-id"},
				"agent_owner":"private-owner",
				"delegated_scopes":["write"],
				"access_token":"must-not-be-decoded",
				"refresh_token":"must-not-be-decoded"
			}
		]`))
	}))
	defer server.Close()

	client := newAgentDelegateTestClient(t, server)
	agents, err := client.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if authorization != "" {
		t.Fatalf("Authorization = %q, want no caller bearer", authorization)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %+v, want one entry", agents)
	}
	if agents[0].Username != "scout" || agents[0].DisplayName != "Scout" || !agents[0].Verified {
		t.Fatalf("agent identity = %+v", agents[0])
	}
	if agents[0].CreatedAt == nil || agents[0].CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00") != "2026-07-15T12:00:00Z" {
		t.Fatalf("created_at = %v", agents[0].CreatedAt)
	}
	if agents[0].IdentitySemantics.SoulBindingState != "" {
		t.Fatalf("private identity field was decoded: %+v", agents[0].IdentitySemantics)
	}
	encoded, err := json.Marshal(agents)
	if err != nil {
		t.Fatalf("marshal public agents: %v", err)
	}
	if strings.Contains(string(encoded), "must-not-be-decoded") || strings.Contains(string(encoded), "private-owner") || strings.Contains(string(encoded), "private-soul-id") {
		t.Fatalf("private or credential fields leaked through typed response: %s", encoded)
	}
}

func TestListAgentsUsesInstanceEndpointBaseFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents" {
			t.Fatalf("path = %s, want /api/v1/agents", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	t.Setenv(envBaseURL, "")
	t.Setenv(envMcpURL, "")
	t.Setenv(envInstanceMcpURL, server.URL+"/instance/{surface}/mcp")
	ResetForTests()
	t.Cleanup(ResetForTests)

	client, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if _, err := client.ListAgents(context.Background()); err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
}

func TestListAgentsReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"directory unavailable","access_token":"secret"}`))
	}))
	defer server.Close()

	client := newAgentDelegateTestClient(t, server)
	_, err := client.ListAgents(context.Background())
	if err == nil {
		t.Fatal("ListAgents returned nil error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want APIError", err, err)
	}
	if apiErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusServiceUnavailable)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("API error leaked credential: %v", err)
	}
}
