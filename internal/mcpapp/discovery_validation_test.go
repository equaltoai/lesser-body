package mcpapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestNew_ValidatesConfiguredMCPEndpoint(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/not-mcp")

	_, err := New("test", "dev")
	if err == nil || !strings.Contains(err.Error(), "MCP_ENDPOINT") {
		t.Fatalf("expected MCP_ENDPOINT validation error, got %v", err)
	}
}

func TestNew_ValidatesTrustBaseURLReachability(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")

	previousLoader := loadEffectiveTrustConfig
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	}
	t.Cleanup(func() {
		loadEffectiveTrustConfig = previousLoader
	})

	previousProbe := probeAuthorizationServerMetadata
	probeAuthorizationServerMetadata = func(context.Context, string) error {
		return errors.New("dial tcp: i/o timeout")
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	_, err := New("test", "dev")
	if err == nil || !strings.Contains(err.Error(), "TrustBaseURL") {
		t.Fatalf("expected TrustBaseURL validation error, got %v", err)
	}
}

func TestWellKnownOAuthProtectedResource_RejectsConfiguredEndpointMismatch(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")

	previousLoader := loadEffectiveTrustConfig
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	}
	t.Cleanup(func() {
		loadEffectiveTrustConfig = previousLoader
	})

	previousProbe := probeAuthorizationServerMetadata
	probeAuthorizationServerMetadata = func(context.Context, string) error { return nil }
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource",
		Headers: map[string][]string{
			"x-forwarded-proto": {"https"},
			"x-forwarded-host":  {"other.example.com"},
		},
	})
	if resp.Status != 500 {
		t.Fatalf("expected 500, got %d (%s)", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "MCP_ENDPOINT") {
		t.Fatalf("expected MCP_ENDPOINT mismatch details, got %s", string(resp.Body))
	}
}

func TestWellKnownOAuthProtectedResource_AddsCORSForAllowedOrigin(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://claude.ai,https://app.example.com")

	previousLoader := loadEffectiveTrustConfig
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	}
	t.Cleanup(func() {
		loadEffectiveTrustConfig = previousLoader
	})

	previousProbe := probeAuthorizationServerMetadata
	probeAuthorizationServerMetadata = func(context.Context, string) error { return nil }
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource",
		Headers: map[string][]string{
			"origin": {"https://claude.ai"},
		},
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "access-control-allow-origin"); got != "https://claude.ai" {
		t.Fatalf("unexpected access-control-allow-origin: %q", got)
	}
}

func firstHeader(headers map[string][]string, key string) string {
	for candidate, values := range headers {
		if !strings.EqualFold(candidate, key) {
			continue
		}
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	return ""
}
