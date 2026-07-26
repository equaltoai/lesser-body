package instanceapp_test

import (
	"encoding/json"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
	"github.com/theory-cloud/apptheory/v2/testkit"
)

func TestInstanceMCPOAuthSessionDeathReturnsInvalidTokenChallenge(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("MCP_SESSION_TTL_MINUTES", "1440")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "https://api.example.com/instance/{surface}/mcp")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, surface := range []string{instanceapp.SurfacePtah, instanceapp.SurfaceBa} {
		t.Run(surface, func(t *testing.T) {
			path := "/instance/" + surface + "/mcp"
			token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath(path))
			validHeaders := initializedMCPHeaders(t, env, app, path, token)

			validResp := invokeMCP(t, env, app, path, validHeaders, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      "valid-session",
				Method:  "tools/list",
			})
			if validResp.Status != 200 {
				t.Fatalf("valid session status = %d, want 200; body = %s", validResp.Status, string(validResp.Body))
			}

			deadHeaders := bearerHeaders(token)
			deadHeaders["mcp-protocol-version"] = []string{"2025-11-25"}
			deadHeaders["mcp-session-id"] = []string{"dead-session"}
			deadResp := invokeMCP(t, env, app, path, deadHeaders, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      "dead-session",
				Method:  "tools/list",
			})
			assertInstanceSessionInvalidTokenResponse(t, deadResp, surface)

			modernHeaders := bearerHeaders(token)
			modernHeaders["mcp-protocol-version"] = []string{"2026-07-28"}
			modernHeaders["mcp-method"] = []string{"tools/list"}
			modernHeaders["mcp-session-id"] = []string{"dead-session"}
			modernResp := invokeMCP(t, env, app, path, modernHeaders, map[string]any{
				"jsonrpc": "2.0",
				"id":      "modern-stateless",
				"method":  "tools/list",
				"params": map[string]any{
					"_meta": map[string]any{
						"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
						"io.modelcontextprotocol/clientCapabilities": map[string]any{},
					},
				},
			})
			if modernResp.Status != 200 {
				t.Fatalf("modern stateless status = %d, want 200; body = %s", modernResp.Status, string(modernResp.Body))
			}
			if got := firstHeader(modernResp.Headers, "www-authenticate"); got != "" {
				t.Fatalf("modern stateless response gained WWW-Authenticate: %q", got)
			}
		})
	}
}

func assertInstanceSessionInvalidTokenResponse(t testing.TB, resp apptheory.Response, surface string) {
	t.Helper()

	if resp.Status != 401 {
		t.Fatalf("dead session status = %d, want 401; body = %s", resp.Status, string(resp.Body))
	}
	wantChallenge := `Bearer error="invalid_token", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/instance/` + surface + `/mcp", scope="read write"`
	if got := firstHeader(resp.Headers, "www-authenticate"); got != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
	}

	var out struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode unauthorized response: %v; body = %s", err, string(resp.Body))
	}
	if out.Error.Code != "app.unauthorized" || out.Error.Message != "unauthorized" {
		t.Fatalf("unauthorized error shape = %+v", out.Error)
	}
	if out.Error.RequestID == "" {
		t.Fatalf("instance unauthorized response omitted request_id: %s", string(resp.Body))
	}
}
