package soulapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestDoJSONRawPreservesSuccessfulResponseBytes(t *testing.T) {
	want := []byte(`{"declarations": { "exact": true }}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{baseURL: base, http: server.Client()}
	got, err := client.DoJSONRaw(context.Background(), http.MethodGet, "/recovery", nil, "managed-key", nil)
	if err != nil {
		t.Fatalf("DoJSONRaw: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("raw response = %q, want %q", got, want)
	}
}

func TestResolveBaseURL_PrefersExplicitSoulBaseURL(t *testing.T) {
	t.Setenv(envBaseURL, "https://soul.example.com")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	t.Setenv(envMcpURL, "https://api.example.com/mcp")

	u, err := resolveBaseURL(context.Background())
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://soul.example.com" {
		t.Fatalf("expected explicit soul base url, got %q", got)
	}
}

func TestResolveBaseURL_FallsBackToLesserAPIBaseURL(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv("LESSER_TABLE_NAME", "")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	t.Setenv(envMcpURL, "")

	u, err := resolveBaseURL(context.Background())
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected lesser api base url fallback, got %q", got)
	}
}

func TestResolveBaseURL_FallsBackToMcpEndpoint(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv("LESSER_TABLE_NAME", "")
	t.Setenv(envLesserAPIURL, "")
	t.Setenv(envMcpURL, "https://api.example.com/mcp")

	u, err := resolveBaseURL(context.Background())
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected mcp endpoint fallback, got %q", got)
	}
}

func TestResolveBaseURL_FallsBackToActorTemplateMcpEndpoint(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv("LESSER_TABLE_NAME", "")
	t.Setenv(envLesserAPIURL, "")
	t.Setenv(envMcpURL, "https://api.example.com/mcp/{actor}")

	u, err := resolveBaseURL(context.Background())
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected actor-template mcp endpoint fallback, got %q", got)
	}
}

func TestResolveBaseURL_UsesManagedTrustConfigWhenTableConfigured(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv("LESSER_TABLE_NAME", "lesser-main")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{TrustBaseURL: "https://lab.lesser.host", Present: true}, nil
	}
	defer func() {
		loadEffectiveTrustConfig = trustconfig.Default
	}()

	u, err := resolveBaseURL(context.Background())
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://lab.lesser.host" {
		t.Fatalf("expected managed trust base url, got %q", got)
	}
}

func TestResolveBaseURL_FailsClosedForManagedDeployWithoutTrustBaseURL(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv("LESSER_TABLE_NAME", "lesser-main")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{Present: true}, nil
	}
	defer func() {
		loadEffectiveTrustConfig = trustconfig.Default
	}()

	if _, err := resolveBaseURL(context.Background()); err == nil {
		t.Fatalf("expected managed trust config error")
	}
}

func TestParseBaseURL_AllowsHTTPOnlyForLoopback(t *testing.T) {
	for _, raw := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := parseBaseURL(raw); err != nil {
			t.Fatalf("expected loopback HTTP URL %q to be accepted: %v", raw, err)
		}
	}

	if _, err := parseBaseURL("http://api.example.com"); err == nil {
		t.Fatalf("expected non-loopback HTTP URL to be rejected")
	}
}
