package mcpapp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func newTestToken(t testing.TB, secret string, username string, scopes []string) string {
	t.Helper()

	now := time.Now().UTC()
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti_test",
		},
		Username: username,
		Scopes:   scopes,
		ClientID: "test-client",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func invokeJSON(t testing.TB, env *testkit.Env, app *apptheory.App, headers map[string][]string, payload any) apptheory.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	reqHeaders := map[string][]string{
		"content-type": {"application/json"},
	}
	for k, v := range headers {
		reqHeaders[k] = v
	}

	return env.Invoke(context.Background(), app, apptheory.Request{
		Method:  "POST",
		Path:    "/mcp",
		Headers: reqHeaders,
		Body:    body,
	})
}

func TestMcpAuth_Unauthorized(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSON(t, env, app, nil, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 401 {
		t.Fatalf("expected 401, got %d", resp.Status)
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if out.Error.Code != "app.unauthorized" {
		t.Fatalf("expected app.unauthorized, got %q", out.Error.Code)
	}
}

func TestMcpAuth_AuthorizedJwt(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"read"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", initResp.Status, string(initResp.Body))
	}
	sessionID := ""
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionID = ids[0]
	}
	if sessionID == "" {
		t.Fatalf("expected non-empty mcp-session-id header")
	}

	var rpcInit mcpruntime.Response
	if err := json.Unmarshal(initResp.Body, &rpcInit); err != nil {
		t.Fatalf("unmarshal initialize: %v", err)
	}
	if rpcInit.Error != nil {
		t.Fatalf("initialize error: %+v", rpcInit.Error)
	}

	listResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":   {"Bearer " + token},
		"mcp-session-id":  {sessionID},
		"accept":          {"application/json"},
		"content-type":    {"application/json"},
		"x-forwarded-for": {"127.0.0.1"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})
	if listResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", listResp.Status, string(listResp.Body))
	}
	var rpcList mcpruntime.Response
	if err := json.Unmarshal(listResp.Body, &rpcList); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if rpcList.Error != nil {
		t.Fatalf("tools/list error: %+v", rpcList.Error)
	}

	callParams, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": json.RawMessage(`{"message":"hi"}`),
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer " + token},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	})
	if callResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", callResp.Status, string(callResp.Body))
	}
	var rpcCall mcpruntime.Response
	if err := json.Unmarshal(callResp.Body, &rpcCall); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	if rpcCall.Error != nil {
		t.Fatalf("tools/call error: %+v", rpcCall.Error)
	}
}

func TestMcpAuth_ToolCallForbiddenWithoutScopes(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"follow"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", initResp.Status, string(initResp.Body))
	}
	sessionID := ""
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionID = ids[0]
	}
	if sessionID == "" {
		t.Fatalf("expected non-empty mcp-session-id header")
	}

	callParams, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": json.RawMessage(`{"message":"hi"}`),
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer " + token},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	})

	if callResp.Status != 403 {
		t.Fatalf("expected 403, got %d (%s)", callResp.Status, string(callResp.Body))
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(callResp.Body, &out); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if out.Error.Code != "app.forbidden" {
		t.Fatalf("expected app.forbidden, got %q", out.Error.Code)
	}
}

func TestMcpAuth_InstanceKey(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "lhk_test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer lhk_test"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", initResp.Status, string(initResp.Body))
	}
	sessionID := ""
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionID = ids[0]
	}
	if sessionID == "" {
		t.Fatalf("expected non-empty mcp-session-id header")
	}

	callParams, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": json.RawMessage(`{"message":"hi"}`),
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer lhk_test"},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	})
	if callResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", callResp.Status, string(callResp.Body))
	}
}
