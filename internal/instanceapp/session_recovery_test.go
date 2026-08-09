package instanceapp_test

import (
	"encoding/json"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"
)

func TestInstanceMCPOAuthDeadSessionTransparentlyRebinds(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("MCP_SESSION_TTL_MINUTES", "1440")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "https://api.example.com/instance/{surface}/mcp")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	stubInstanceAuthorizationServerMetadata(t)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, surface := range []string{instanceapp.SurfacePtah, instanceapp.SurfaceBa} {
		t.Run(surface, func(t *testing.T) {
			path := "/instance/" + surface + "/mcp"
			token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath(path))

			deadHeaders := bearerHeaders(token)
			deadHeaders["mcp-protocol-version"] = []string{"2025-11-25"}
			deadHeaders["mcp-session-id"] = []string{"dead-" + surface + "-session"}
			reboundResp := invokeMCP(t, env, app, path, deadHeaders, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      "dead-session",
				Method:  "tools/list",
			})
			if reboundResp.Status != 200 {
				t.Fatalf("rebound status = %d, want 200; body = %s", reboundResp.Status, string(reboundResp.Body))
			}
			freshSessionID := firstHeader(reboundResp.Headers, "mcp-session-id")
			if freshSessionID == "" || freshSessionID == "dead-"+surface+"-session" {
				t.Fatalf("rebound response session id = %q, want a fresh id", freshSessionID)
			}
			if got := firstHeader(reboundResp.Headers, "www-authenticate"); got != "" {
				t.Fatalf("successful rebind gained WWW-Authenticate: %q", got)
			}
			assertInstanceToolsListSuccess(t, reboundResp.Body)

			nextHeaders := bearerHeaders(token)
			nextHeaders["mcp-protocol-version"] = []string{"2025-11-25"}
			nextHeaders["mcp-session-id"] = []string{freshSessionID}
			nextResp := invokeMCP(t, env, app, path, nextHeaders, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      "fresh-session-next-call",
				Method:  "tools/list",
			})
			if nextResp.Status != 200 {
				t.Fatalf("fresh session next call status = %d, want 200; body = %s", nextResp.Status, string(nextResp.Body))
			}
			assertInstanceToolsListSuccess(t, nextResp.Body)
		})
	}
}

func TestInstanceMCP20260728StatelessRequestsAreNotRebound(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("MCP_SESSION_TTL_MINUTES", "1440")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "https://api.example.com/instance/{surface}/mcp")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	stubInstanceAuthorizationServerMetadata(t)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, surface := range []string{instanceapp.SurfacePtah, instanceapp.SurfaceBa} {
		t.Run(surface, func(t *testing.T) {
			path := "/instance/" + surface + "/mcp"
			token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath(path))
			modernHeaders := bearerHeaders(token)
			modernHeaders["mcp-protocol-version"] = []string{"2026-07-28"}
			modernHeaders["mcp-method"] = []string{"tools/list"}
			modernHeaders["mcp-session-id"] = []string{"dead-session-is-ignored"}
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
			if got := firstHeader(modernResp.Headers, "mcp-session-id"); got != "" {
				t.Fatalf("modern stateless response gained mcp-session-id: %q", got)
			}
		})
	}
}

func assertInstanceToolsListSuccess(t testing.TB, body []byte) {
	t.Helper()

	var rpc mcpruntime.Response
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("tools/list returned JSON-RPC error: %+v", rpc.Error)
	}
	var result struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	resultBody, err := json.Marshal(rpc.Result)
	if err != nil {
		t.Fatalf("marshal tools/list result: %v", err)
	}
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
}
