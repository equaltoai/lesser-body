package mcpapp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/apptheory/v4/testkit"

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
	previousProbe := probeAuthorizationServerMetadata
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		return "https://dev.example.com", nil
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource/mcp/Arch",
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}
	if got := resp.Headers["cache-control"]; len(got) == 0 || got[0] != "public, max-age=60" {
		t.Fatalf("unexpected cache-control header: %#v", resp.Headers["cache-control"])
	}

	var out struct {
		Resource               string   `json:"resource"`
		ResourceName           string   `json:"resource_name"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Resource != "https://api.example.com/mcp/Arch" {
		t.Fatalf("unexpected resource: %q", out.Resource)
	}
	if out.ResourceName != "Arch" {
		t.Fatalf("unexpected resource_name: %q", out.ResourceName)
	}
	if len(out.AuthorizationServers) != 1 || out.AuthorizationServers[0] != "https://dev.example.com" {
		t.Fatalf("unexpected authorization_servers: %#v", out.AuthorizationServers)
	}
	if want := []string{"read", "write", "follow", "push"}; !reflect.DeepEqual(out.ScopesSupported, want) {
		t.Fatalf("unexpected scopes_supported: got %#v want %#v", out.ScopesSupported, want)
	}
	for _, scope := range out.ScopesSupported {
		if scope == "admin" || strings.Contains(scope, "instance") {
			t.Fatalf("per-actor metadata advertised non-public scope %q", scope)
		}
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
	previousProbe := probeAuthorizationServerMetadata
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		return "https://dev.example.com", nil
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://tenant.example.com/mcp/{actor}")

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource/mcp/Arch",
		Headers: map[string][]string{
			"x-forwarded-proto": {"https"},
			"x-forwarded-host":  {"tenant.example.com"},
		},
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}

	var out struct {
		Resource             string   `json:"resource"`
		ResourceName         string   `json:"resource_name"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Resource != "https://tenant.example.com/mcp/Arch" {
		t.Fatalf("unexpected inferred resource: %q", out.Resource)
	}
	if out.ResourceName != "Arch" {
		t.Fatalf("unexpected inferred resource_name: %q", out.ResourceName)
	}
	if want := []string{"https://dev.example.com"}; !reflect.DeepEqual(out.AuthorizationServers, want) {
		t.Fatalf("unexpected inferred authorization_servers: got %#v want %#v", out.AuthorizationServers, want)
	}
}
