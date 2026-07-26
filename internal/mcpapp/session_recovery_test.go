package mcpapp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
	"github.com/theory-cloud/apptheory/v2/testkit"
)

func TestActorMCPOAuthSessionDeathReturnsInvalidTokenChallenge(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(
		t,
		"test-secret",
		"agent1",
		[]string{"read"},
		[]string{"https://api.example.com/mcp/agent1"},
	)

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "initialize",
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25"}`),
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize status = %d, want 200; body = %s", initResp.Status, string(initResp.Body))
	}
	sessionID := firstHeader(initResp.Headers, "mcp-session-id")
	if sessionID == "" {
		t.Fatal("initialize response missing mcp-session-id")
	}

	validResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "valid-session",
		Method:  "tools/list",
	})
	if validResp.Status != 200 {
		t.Fatalf("valid session status = %d, want 200; body = %s", validResp.Status, string(validResp.Body))
	}

	unknownResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {"unknown-session"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "unknown-session",
		Method:  "tools/list",
	})
	assertActorSessionInvalidTokenResponse(t, unknownResp)

	deleteResp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "DELETE",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":        {"Bearer " + token},
			"mcp-protocol-version": {"2025-11-25"},
			"mcp-session-id":       {sessionID},
		},
	})
	if deleteResp.Status != 202 {
		t.Fatalf("delete live session status = %d, want 202; body = %s", deleteResp.Status, string(deleteResp.Body))
	}

	for _, method := range []string{"POST", "GET", "DELETE"} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			headers := map[string][]string{
				"authorization":        {"Bearer " + token},
				"accept":               {"application/json, text/event-stream"},
				"mcp-protocol-version": {"2025-11-25"},
				"mcp-session-id":       {sessionID},
			}
			var body []byte
			if method == "POST" {
				headers["content-type"] = []string{"application/json"}
				body = []byte(`{"jsonrpc":"2.0","id":"dead-session","method":"tools/list"}`)
			} else if method == "GET" {
				headers["accept"] = []string{"text/event-stream"}
			}

			resp := env.Invoke(context.Background(), app, apptheory.Request{
				Method:  method,
				Path:    "/mcp/agent1",
				Headers: headers,
				Body:    body,
			})
			assertActorSessionInvalidTokenResponse(t, resp)
		})
	}

	modernResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2026-07-28"},
		"mcp-method":           {"tools/list"},
		"mcp-session-id":       {sessionID},
	}, bodyModernRequest("modern-stateless", "tools/list", map[string]any{}))
	if modernResp.Status != 200 {
		t.Fatalf("modern stateless status = %d, want 200; body = %s", modernResp.Status, string(modernResp.Body))
	}
	if got := firstHeader(modernResp.Headers, "www-authenticate"); got != "" {
		t.Fatalf("modern stateless response gained WWW-Authenticate: %q", got)
	}
}

func TestActorMCPSessionNotFoundRemains404ForNonOAuthPrincipal(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	resp := invokeJSONAtPath(t, testkit.New(), app, "/mcp/instance", map[string][]string{
		"authorization":        {"Bearer legacy-instance-key"},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {"dead-session"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "legacy-dead-session",
		Method:  "tools/list",
	})

	if resp.Status != 404 {
		t.Fatalf("non-OAuth session-not-found status = %d, want 404; body = %s", resp.Status, string(resp.Body))
	}
	if strings.TrimSpace(string(resp.Body)) != `{"error":"session not found"}` {
		t.Fatalf("non-OAuth session-not-found body changed: %s", string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != "" {
		t.Fatalf("non-OAuth 404 gained WWW-Authenticate: %q", got)
	}
}

func assertActorSessionInvalidTokenResponse(t testing.TB, resp apptheory.Response) {
	t.Helper()

	if resp.Status != 401 {
		t.Fatalf("dead session status = %d, want 401; body = %s", resp.Status, string(resp.Body))
	}
	const wantChallenge = `Bearer error="invalid_token", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"`
	if got := firstHeader(resp.Headers, "www-authenticate"); got != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
	}
	if expose := firstHeader(resp.Headers, "access-control-expose-headers"); !strings.Contains(strings.ToLower(expose), "www-authenticate") {
		t.Fatalf("access-control-expose-headers = %q, want www-authenticate", expose)
	}

	var out struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode unauthorized response: %v; body = %s", err, string(resp.Body))
	}
	if out.Error.Code != "app.unauthorized" || out.Error.Message != "unauthorized" {
		t.Fatalf("unauthorized error shape = %+v", out.Error)
	}
	if out.Error.Details["reason"] != "mcp_session_not_found" ||
		out.Error.Details["authAction"] != "reauthorize" ||
		out.Error.Details["refreshRequired"] != true {
		t.Fatalf("unauthorized session recovery details = %+v", out.Error.Details)
	}
}
