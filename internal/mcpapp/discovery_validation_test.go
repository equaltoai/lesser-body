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

func TestValidatedMcpEndpoint_AcceptsActorTemplate(t *testing.T) {
	got, err := validatedMcpEndpoint("https://api.example.com/mcp/{actor}")
	if err != nil {
		t.Fatalf("validatedMcpEndpoint: %v", err)
	}
	if got != "https://api.example.com/mcp/{actor}" {
		t.Fatalf("unexpected validated endpoint: %q", got)
	}
}

func TestNew_ValidatesAuthorizationServerMetadataReachabilityFromMCPEndpoint(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

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
	probedURL := ""
	probeAuthorizationServerMetadata = func(_ context.Context, metadataURL string) (string, error) {
		probedURL = metadataURL
		return "", errors.New("dial tcp: i/o timeout")
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	_, err := New("test", "dev")
	if err == nil || !strings.Contains(err.Error(), "MCP_ENDPOINT") {
		t.Fatalf("expected MCP_ENDPOINT validation error, got %v", err)
	}
	if want := "https://api.example.com/.well-known/oauth-authorization-server"; probedURL != want {
		t.Fatalf("unexpected metadata probe url: got %q want %q", probedURL, want)
	}
}

func TestValidatedMcpEndpointForRequest_ResolvesActorTemplate(t *testing.T) {
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	got, err := validatedMcpEndpointForRequest(&apptheory.Context{
		Params: map[string]string{"actor": "Arch"},
		Request: apptheory.Request{
			Path: "/mcp/Arch",
			Headers: map[string][]string{
				"x-forwarded-proto": {"https"},
				"x-forwarded-host":  {"api.example.com"},
			},
		},
	})
	if err != nil {
		t.Fatalf("validatedMcpEndpointForRequest: %v", err)
	}
	if got != "https://api.example.com/mcp/Arch" {
		t.Fatalf("unexpected resolved endpoint: %q", got)
	}
}

func TestNew_CachesAuthorizationServerIssuerFromMetadataProbe(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

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
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		return "https://issuer.example", nil
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	if _, err := New("test", "dev"); err != nil {
		t.Fatalf("new app: %v", err)
	}
	if got := cachedAuthorizationServerIssuer(); got != "https://issuer.example" {
		t.Fatalf("unexpected cached issuer: got %q want %q", got, "https://issuer.example")
	}
}

func TestNew_SkipsAuthorizationServerMetadataProbeWithoutMCPEndpoint(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "")

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
	probeCalled := false
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		probeCalled = true
		return "https://issuer.example", nil
	}
	t.Cleanup(func() {
		probeAuthorizationServerMetadata = previousProbe
	})

	if _, err := New("test", "dev"); err != nil {
		t.Fatalf("new app: %v", err)
	}
	if probeCalled {
		t.Fatalf("expected metadata probe to be skipped without MCP_ENDPOINT")
	}
}

func TestWellKnownOAuthProtectedResource_RejectsMissingConfiguredEndpoint(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "")

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

	app, err := New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource/mcp/agent1",
		Headers: map[string][]string{
			"x-forwarded-proto": {"https"},
			"x-forwarded-host":  {"evil.example"},
		},
	})
	if resp.Status != 500 {
		t.Fatalf("expected 500, got %d (%s)", resp.Status, string(resp.Body))
	}
	body := string(resp.Body)
	if !strings.Contains(body, "MCP_ENDPOINT is required") {
		t.Fatalf("expected missing MCP_ENDPOINT error, got %s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("missing MCP_ENDPOINT path must not infer metadata from untrusted host headers: %s", body)
	}

	discoveryResp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/mcp.json",
		Headers: map[string][]string{
			"x-forwarded-proto": {"https"},
			"x-forwarded-host":  {"evil.example"},
		},
	})
	if discoveryResp.Status != 500 {
		t.Fatalf("expected discovery 500, got %d (%s)", discoveryResp.Status, string(discoveryResp.Body))
	}
	discoveryBody := string(discoveryResp.Body)
	if !strings.Contains(discoveryBody, "MCP_ENDPOINT is required") {
		t.Fatalf("expected missing MCP_ENDPOINT discovery error, got %s", discoveryBody)
	}
	if strings.Contains(discoveryBody, "evil.example") {
		t.Fatalf("discovery must not infer endpoint from untrusted host headers: %s", discoveryBody)
	}
}

func TestWellKnownOAuthProtectedResource_RejectsConfiguredEndpointMismatch(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

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
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		return "https://issuer.example", nil
	}
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
		Path:   "/.well-known/oauth-protected-resource/mcp/agent1",
		Headers: map[string][]string{
			"x-forwarded-proto": {"https"},
			"x-forwarded-host":  {"other.example.com"},
		},
	})
	if resp.Status != 400 {
		t.Fatalf("expected 400, got %d (%s)", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "app.invalid_public_url") {
		t.Fatalf("expected invalid public url details, got %s", string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1") {
		t.Fatalf("expected canonical resource metadata url in body, got %s", string(resp.Body))
	}
}

func TestWellKnownOAuthProtectedResource_AddsCORSForAllowedOrigin(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
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
	probeAuthorizationServerMetadata = func(context.Context, string) (string, error) {
		return "https://issuer.example", nil
	}
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
		Path:   "/.well-known/oauth-protected-resource/mcp/agent1",
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
