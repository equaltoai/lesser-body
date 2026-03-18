package mcpapp

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestWellKnownOAuthProtectedResource(t *testing.T) {
	previous := loadEffectiveTrustConfig
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	}
	t.Cleanup(func() {
		loadEffectiveTrustConfig = previous
	})

	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource",
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}
	if got := resp.Headers["cache-control"]; len(got) == 0 || got[0] != "public, max-age=60" {
		t.Fatalf("unexpected cache-control header: %#v", resp.Headers["cache-control"])
	}

	var out struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Resource != "https://api.example.com/mcp" {
		t.Fatalf("unexpected resource: %q", out.Resource)
	}
	if len(out.AuthorizationServers) != 1 || out.AuthorizationServers[0] != "https://lesser.example" {
		t.Fatalf("unexpected authorization_servers: %#v", out.AuthorizationServers)
	}
	if got, want := len(out.ScopesSupported), 3; got != want {
		t.Fatalf("expected %d scopes, got %d (%#v)", want, got, out.ScopesSupported)
	}
	if len(out.BearerMethodsSupported) != 1 || out.BearerMethodsSupported[0] != "header" {
		t.Fatalf("unexpected bearer_methods_supported: %#v", out.BearerMethodsSupported)
	}
}

func TestWellKnownOAuthProtectedResource_InferEndpointFromRequest(t *testing.T) {
	previous := loadEffectiveTrustConfig
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	}
	t.Cleanup(func() {
		loadEffectiveTrustConfig = previous
	})

	t.Setenv("MCP_SESSION_TABLE", "")

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
			"x-forwarded-host":  {"tenant.example.com"},
		},
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}

	var out struct {
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Resource != "https://tenant.example.com/mcp" {
		t.Fatalf("unexpected inferred resource: %q", out.Resource)
	}
}
