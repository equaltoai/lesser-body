package mcpapp

import (
	"encoding/json"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func mcpAuthChallengeTestContext() *apptheory.Context {
	return &apptheory.Context{
		Params: map[string]string{"actor": "agent1"},
		Request: apptheory.Request{
			Path: "/mcp/agent1",
			Headers: map[string][]string{
				"x-forwarded-proto": {"https"},
				"x-forwarded-host":  {"api.example.com"},
			},
		},
	}
}

func requireBridgeChallengeHeaders(t *testing.T, resp *apptheory.Response) {
	t.Helper()

	canonical := resp.Headers["www-authenticate"]
	if len(canonical) != 1 || strings.TrimSpace(canonical[0]) == "" {
		t.Fatalf("expected a single non-empty www-authenticate challenge, got %v", canonical)
	}
	bridge := resp.Headers["mcp-www-authenticate"]
	if len(bridge) != len(canonical) {
		t.Fatalf("mcp-www-authenticate = %v, want byte-identical copy of %v", bridge, canonical)
	}
	for i := range canonical {
		if bridge[i] != canonical[i] {
			t.Fatalf("mcp-www-authenticate[%d] = %q, want byte-identical to www-authenticate %q", i, bridge[i], canonical[i])
		}
	}
	if !strings.Contains(canonical[0], `resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1"`) {
		t.Fatalf("challenge missing resource_metadata URL: %q", canonical[0])
	}
	if got := resp.Headers["cache-control"]; len(got) != 1 || got[0] != "no-store" {
		t.Fatalf("cache-control = %v, want [no-store]", got)
	}
}

func TestUnauthorizedMCPResponse_EmitsBridgeChallengeHeaders(t *testing.T) {
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	resp := unauthorizedMCPResponse(mcpAuthChallengeTestContext())
	if resp == nil || resp.Status != 401 {
		t.Fatalf("expected 401 response, got %+v", resp)
	}
	requireBridgeChallengeHeaders(t, resp)
	if got := firstHeader(resp.Headers, "www-authenticate"); strings.Contains(got, `error=`) {
		t.Fatalf("absent bearer challenge gained an error parameter: %q", got)
	}
	assertMCPUnauthorizedDetails(t, resp, "authorize", false, "missing_or_invalid_bearer")
}

func TestOAuthUnauthorizedMCPResponse_EmitsBridgeChallengeHeaders(t *testing.T) {
	resp := OAuthUnauthorizedMCPResponse(
		mcpAuthChallengeTestContext(),
		"https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1",
		MCPAuthorizationScopes,
	)
	if resp == nil || resp.Status != 401 {
		t.Fatalf("expected 401 response, got %+v", resp)
	}
	requireBridgeChallengeHeaders(t, resp)
	if got := firstHeader(resp.Headers, "www-authenticate"); !strings.Contains(got, `scope="read write"`) {
		t.Fatalf("challenge missing scope: %q", got)
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); strings.Contains(got, `error=`) {
		t.Fatalf("absent bearer challenge gained an error parameter: %q", got)
	}
	assertMCPUnauthorizedDetails(t, resp, "authorize", false, "missing_or_invalid_bearer")
}

func TestOAuthUnauthorizedMCPResponse_RejectedBearerSignalsRefresh(t *testing.T) {
	ctx := mcpAuthChallengeTestContext()
	ctx.Request.Headers["authorization"] = []string{"Bearer rejected-token"}

	resp := OAuthUnauthorizedMCPResponse(
		ctx,
		"https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1",
		MCPAuthorizationScopes,
	)
	if resp == nil || resp.Status != 401 {
		t.Fatalf("expected 401 response, got %+v", resp)
	}
	requireBridgeChallengeHeaders(t, resp)
	const wantChallenge = `Bearer error="invalid_token", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"`
	if got := firstHeader(resp.Headers, "www-authenticate"); got != wantChallenge {
		t.Fatalf("www-authenticate = %q, want %q", got, wantChallenge)
	}
	assertMCPUnauthorizedDetails(t, resp, "refresh_or_reauthorize", true, "invalid_oauth_bearer")
}

func TestInsufficientScopeMCPResponse_EmitsBridgeChallengeHeaders(t *testing.T) {
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	resp := insufficientScopeMCPResponse(mcpAuthChallengeTestContext(), []string{"follow"}, []string{"read"})
	if resp == nil || resp.Status != 403 {
		t.Fatalf("expected 403 response, got %+v", resp)
	}
	requireBridgeChallengeHeaders(t, resp)
	if got := firstHeader(resp.Headers, "www-authenticate"); !strings.Contains(got, `error="insufficient_scope"`) {
		t.Fatalf("challenge missing insufficient_scope error: %q", got)
	}
}

func TestClientCompatibilityHeaders_ExposesBridgeChallengeHeader(t *testing.T) {
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	handler := WithClientCompatibilityHeaders(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		return unauthorizedMCPResponse(ctx), nil
	})
	resp, err := handler(mcpAuthChallengeTestContext())
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	expose := strings.ToLower(firstHeader(resp.Headers, "access-control-expose-headers"))
	if !strings.Contains(expose, "mcp-www-authenticate") {
		t.Fatalf("access-control-expose-headers = %q, want mcp-www-authenticate", expose)
	}
	if !strings.Contains(expose, "www-authenticate") {
		t.Fatalf("access-control-expose-headers = %q, want www-authenticate", expose)
	}
}

func assertMCPUnauthorizedDetails(t testing.TB, resp *apptheory.Response, wantAction string, wantRefresh bool, wantReason string) {
	t.Helper()

	var payload struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode unauthorized response: %v; body = %s", err, string(resp.Body))
	}
	if got := payload.Error.Details["authAction"]; got != wantAction {
		t.Fatalf("authAction = %v, want %q; details = %+v", got, wantAction, payload.Error.Details)
	}
	if got := payload.Error.Details["refreshRequired"]; got != wantRefresh {
		t.Fatalf("refreshRequired = %v, want %t; details = %+v", got, wantRefresh, payload.Error.Details)
	}
	if got := payload.Error.Details["reason"]; got != wantReason {
		t.Fatalf("reason = %v, want %q; details = %+v", got, wantReason, payload.Error.Details)
	}
}
