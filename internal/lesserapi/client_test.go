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
