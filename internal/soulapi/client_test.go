package soulapi

import "testing"

func TestResolveBaseURL_PrefersExplicitSoulBaseURL(t *testing.T) {
	t.Setenv(envBaseURL, "https://soul.example.com")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	t.Setenv(envMcpURL, "https://api.example.com/mcp")

	u, err := resolveBaseURL()
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://soul.example.com" {
		t.Fatalf("expected explicit soul base url, got %q", got)
	}
}

func TestResolveBaseURL_FallsBackToLesserAPIBaseURL(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv(envLesserAPIURL, "https://api.example.com")
	t.Setenv(envMcpURL, "")

	u, err := resolveBaseURL()
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected lesser api base url fallback, got %q", got)
	}
}

func TestResolveBaseURL_FallsBackToMcpEndpoint(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv(envLesserAPIURL, "")
	t.Setenv(envMcpURL, "https://api.example.com/mcp")

	u, err := resolveBaseURL()
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected mcp endpoint fallback, got %q", got)
	}
}
