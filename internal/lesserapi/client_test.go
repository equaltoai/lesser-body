package lesserapi

import "testing"

func TestResolveBaseURL_FallsBackToActorTemplateEndpoint(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv(envMcpURL, "https://api.example.com/mcp/{actor}")

	u, err := resolveBaseURL()
	if err != nil {
		t.Fatalf("resolveBaseURL: %v", err)
	}
	if got := u.String(); got != "https://api.example.com" {
		t.Fatalf("expected mcp endpoint fallback, got %q", got)
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
