package mcpapp_test

import (
	"encoding/json"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
	"github.com/theory-cloud/apptheory/v2/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestMcpAuth_TaskMethodsRequireReadScope(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"follow"}, []string{"https://api.example.com/mcp/agent1"})
	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeaders := map[string][]string{"authorization": {"Bearer " + token}}

	initResp := invokeJSON(t, env, app, authHeaders, &mcpruntime.Request{JSONRPC: "2.0", ID: "init", Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := firstHeader(initResp.Headers, "mcp-session-id")
	if sessionID == "" {
		t.Fatal("expected mcp-session-id")
	}

	for _, method := range []string{"tasks/list", "tasks/get", "tasks/result", "tasks/cancel"} {
		t.Run(method, func(t *testing.T) {
			req := &mcpruntime.Request{JSONRPC: "2.0", ID: method, Method: method}
			if method != "tasks/list" {
				req.Params = mustRawTaskAuthTest(t, map[string]any{"taskId": "task-1"})
			}
			resp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {"Bearer " + token},
				"mcp-session-id": {sessionID},
			}, req)
			if resp.Status != 403 {
				t.Fatalf("expected 403 for %s, got %d (%s)", method, resp.Status, string(resp.Body))
			}
			if got := firstHeader(resp.Headers, "www-authenticate"); got != `Bearer error="insufficient_scope", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="follow read", error_description="Additional read permission required"` {
				t.Fatalf("unexpected WWW-Authenticate header: %q", got)
			}
			var out struct {
				Error struct {
					Code    string `json:"code"`
					Details struct {
						Reason         string   `json:"reason"`
						RequiredScopes []string `json:"requiredScopes"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body, &out); err != nil {
				t.Fatalf("unmarshal %s error body: %v", method, err)
			}
			if out.Error.Code != "app.forbidden" || out.Error.Details.Reason != "insufficient_scope" || len(out.Error.Details.RequiredScopes) != 1 || out.Error.Details.RequiredScopes[0] != "read" {
				t.Fatalf("unexpected insufficient-scope body for %s: %+v", method, out)
			}
		})
	}
}

func mustRawTaskAuthTest(t testing.TB, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
