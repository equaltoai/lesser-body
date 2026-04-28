package mcpapp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
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
	return newTestTokenWithTimesAndAudience(t, secret, username, scopes, now, now.Add(time.Hour), nil)
}

func newTestTokenWithTimes(t testing.TB, secret string, username string, scopes []string, issuedAt, expiresAt time.Time) string {
	t.Helper()

	return newTestTokenWithTimesAndAudience(t, secret, username, scopes, issuedAt, expiresAt, nil)
}

func newTestTokenWithAudience(t testing.TB, secret string, username string, scopes []string, audience []string) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithTimesAndAudience(t, secret, username, scopes, now, now.Add(time.Hour), audience)
}

func newTestTokenWithTimesAndAudience(t testing.TB, secret string, username string, scopes []string, issuedAt, expiresAt time.Time, audience []string) string {
	t.Helper()

	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        "jti_test",
			Audience:  jwt.ClaimStrings(audience),
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
	return invokeJSONAtPath(t, env, app, defaultMcpPath(headers), headers, payload)
}

func invokeJSONAtPath(t testing.TB, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, payload any) apptheory.Response {
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
		Path:    path,
		Headers: reqHeaders,
		Body:    body,
	})
}

func defaultMcpPath(headers map[string][]string) string {
	token := bearerTokenFromHeaders(headers)
	if token == "" {
		return "/mcp/agent1"
	}

	if username := usernameFromJWTForTests(token); username != "" {
		return "/mcp/" + username
	}

	return "/mcp/instance"
}

func bearerTokenFromHeaders(headers map[string][]string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "authorization") {
			continue
		}
		for _, value := range values {
			parts := strings.Fields(strings.TrimSpace(value))
			if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func usernameFromJWTForTests(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Username)
}

func TestMcpAuth_RejectsJwtOlderThan24Hours(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	now := time.Now().UTC()
	token := newTestTokenWithTimes(t, "test", "agent1", []string{"read"}, now.Add(-7*24*time.Hour), now.Add(time.Hour))

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 401 {
		t.Fatalf("expected 401 for token older than 24h, got %d (%s)", resp.Status, string(resp.Body))
	}
}

func TestMcpActorRoute_InitializeAllowed(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"read"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 200 {
		t.Fatalf("expected 200 for /mcp/{actor}, got %d (%s)", resp.Status, string(resp.Body))
	}
}

func TestMcpActorRoute_RejectsIdentityMismatch(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"read"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSONAtPath(t, env, app, "/mcp/other-agent", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 403 {
		t.Fatalf("expected 403 for actor/token mismatch, got %d (%s)", resp.Status, string(resp.Body))
	}
}

func TestMcpAuth_ExpiredJwtRejected(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	now := time.Now().UTC()
	token := newTestTokenWithTimes(t, "test", "agent1", []string{"read"}, now.Add(-2*time.Hour), now.Add(-time.Minute))

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 401 {
		t.Fatalf("expected 401 for expired token, got %d (%s)", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
}

func TestMcpAuth_TokenAudienceMismatchRejected(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/other-agent"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 401 {
		t.Fatalf("expected 401 for audience mismatch, got %d (%s)", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
}

func TestMcpAuth_Unauthorized(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
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
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if out.Error.Code != "app.unauthorized" {
		t.Fatalf("expected app.unauthorized, got %q", out.Error.Code)
	}
	if out.Error.Details["authAction"] != "authorize" {
		t.Fatalf("expected authAction=authorize, got %+v", out.Error.Details)
	}
	if out.Error.Details["refreshRequired"] != false {
		t.Fatalf("expected refreshRequired=false, got %+v", out.Error.Details)
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
	if expose := firstHeader(resp.Headers, "access-control-expose-headers"); !strings.Contains(strings.ToLower(expose), "www-authenticate") {
		t.Fatalf("expected access-control-expose-headers to include www-authenticate, got %q", expose)
	}
}

func TestMcpActorRoute_UnauthorizedAdvertisesActorMetadata(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSONAtPath(t, env, app, "/mcp/Arch", nil, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 401 {
		t.Fatalf("expected 401, got %d (%s)", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/Arch", scope="read write"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
}

func TestMcpActorRoute_RejectsWrongPublicHost(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://example.com/mcp/agent1"})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":     {"Bearer " + token},
		"x-forwarded-proto": {"https"},
		"x-forwarded-host":  {"api.example.com"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 400 {
		t.Fatalf("expected 400 for wrong public host, got %d (%s)", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "app.invalid_public_url") {
		t.Fatalf("expected invalid public url body, got %s", string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "https://example.com/mcp/agent1") {
		t.Fatalf("expected canonical MCP url in body, got %s", string(resp.Body))
	}
}

func TestSharedMcpRoute_ReturnsRetiredResponse(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "POST",
		Path:   "/mcp",
		Headers: map[string][]string{
			"content-type": {"application/json"},
		},
		Body: []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
	})

	if resp.Status != 410 {
		t.Fatalf("expected 410, got %d (%s)", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "app.endpoint_retired") {
		t.Fatalf("unexpected body: %s", string(resp.Body))
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
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"follow"}, []string{"https://api.example.com/mcp/agent1"})

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
	if got := firstHeader(callResp.Headers, "www-authenticate"); got != `Bearer error="insufficient_scope", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="follow read", error_description="Additional read permission required"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
}

func TestMcpAuth_ToolCallWriteScopeChallengePreservesGrantedScopes(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})

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
		"name":      "post_create",
		"arguments": json.RawMessage(`{"content":"hi","visibility":"public"}`),
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer " + token},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  callParams,
	})

	if callResp.Status != 403 {
		t.Fatalf("expected 403, got %d (%s)", callResp.Status, string(callResp.Body))
	}
	if got := firstHeader(callResp.Headers, "www-authenticate"); got != `Bearer error="insufficient_scope", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write", error_description="Additional write permission required"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
}

func TestMcpAuth_InstanceKeyRejectedByDefault(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "lhk_test")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "")
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
	if initResp.Status != 401 {
		t.Fatalf("expected 401, got %d (%s)", initResp.Status, string(initResp.Body))
	}
}

func TestMcpAuth_InstanceKeyAllowedWithCompatibilityFlag(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "lhk_test")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
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

func TestMcpAuth_InstanceKeyCompatibilityModeReturnsStructuredOAuthOnlyErrors(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "lhk_test")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
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
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name":      "profile_read",
		"arguments": map[string]any{},
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer lhk_test"},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  callParams,
	})
	if callResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", callResp.Status, string(callResp.Body))
	}

	var toolRPC mcpruntime.Response
	if err := json.Unmarshal(callResp.Body, &toolRPC); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	var toolResult mcpruntime.ToolResult
	{
		b, _ := json.Marshal(toolRPC.Result)
		_ = json.Unmarshal(b, &toolResult)
	}
	if !toolResult.IsError {
		t.Fatalf("expected tool result error, got %+v", toolResult)
	}
	toolErr, _ := toolResult.StructuredContent["error"].(map[string]any)
	if toolErr["code"] != "unauthorized" {
		t.Fatalf("unexpected tool error payload: %+v", toolErr)
	}
	toolDetails, _ := toolErr["details"].(map[string]any)
	if toolDetails["authAction"] != "authorize" || toolDetails["reason"] != "missing_oauth_bearer" {
		t.Fatalf("unexpected tool auth details: %+v", toolDetails)
	}

	resourceParams, _ := json.Marshal(map[string]any{"uri": "agent://profile"})
	resourceResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer lhk_test"},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "resources/read",
		Params:  resourceParams,
	})
	if resourceResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", resourceResp.Status, string(resourceResp.Body))
	}

	var resourceRPC mcpruntime.Response
	if err := json.Unmarshal(resourceResp.Body, &resourceRPC); err != nil {
		t.Fatalf("unmarshal resources/read: %v", err)
	}
	var resourceOut struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	{
		b, _ := json.Marshal(resourceRPC.Result)
		_ = json.Unmarshal(b, &resourceOut)
	}
	if len(resourceOut.Contents) != 1 {
		t.Fatalf("expected one resource content, got %+v", resourceOut.Contents)
	}
	var resourcePayload struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(resourceOut.Contents[0].Text), &resourcePayload); err != nil {
		t.Fatalf("unmarshal resource payload: %v", err)
	}
	if resourcePayload.Error.Code != "unauthorized" {
		t.Fatalf("unexpected resource payload: %+v", resourcePayload)
	}
	if resourcePayload.Error.Details["authAction"] != "authorize" || resourcePayload.Error.Details["reason"] != "missing_oauth_bearer" {
		t.Fatalf("unexpected resource auth details: %+v", resourcePayload.Error.Details)
	}
}

func TestMcpAuth_AuthorizedJwt_AllowsConfiguredBrowserOrigin(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://dev.example.com,https://app.dev.example.com")
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
		"origin":        {"https://dev.example.com"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", initResp.Status, string(initResp.Body))
	}
	if ids := initResp.Headers["mcp-session-id"]; len(ids) == 0 || ids[0] == "" {
		t.Fatalf("expected non-empty mcp-session-id header")
	}
	if got := firstHeader(initResp.Headers, "access-control-allow-origin"); got != "https://dev.example.com" {
		t.Fatalf("expected access-control-allow-origin to echo request origin, got %q", got)
	}
	exposeHeaders := ""
	if values := initResp.Headers["access-control-expose-headers"]; len(values) > 0 {
		exposeHeaders = values[0]
	}
	if !strings.Contains(strings.ToLower(exposeHeaders), "mcp-session-id") {
		t.Fatalf("expected access-control-expose-headers to include mcp-session-id, got %q", exposeHeaders)
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
