package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

// installShareScopeAppEnv installs the app-level environment a composed social
// share-scope test needs: JWT signing, session/stream/task isolation, and the
// trust-config seam so actor binding and soul reads can run without cloud.
func installShareScopeAppEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ALLOWED_ORIGINS", "")
	t.Setenv("MCP_ENDPOINT", "")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	installTrustConfigIsolation(t)
}

// installShareScopeLesser installs a single lesser stub that admits the routed
// actor with the given relationship and serves the social surfaces, recording
// every request so tests can assert the caller bearer and absence of the act-as
// indicator on the surfaces that must not carry it.
func installShareScopeLesser(t *testing.T, relationship string, onRequest func(method, path, auth, actAs string)) {
	t.Helper()
	restoreAdmission := mcpapp.SetActorAdmissionForTests(nil)
	t.Cleanup(restoreAdmission)

	actedBy := "owner"
	if relationship == "grantee" {
		actedBy = "alice"
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Lesser-Act-As"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/agents/arch/access":
			_, _ = w.Write([]byte(`{"actor":"arch","relationship":"` + relationship + `","authorized":true,"acted_by":"` + actedBy + `"}`))
		case "/api/v1/timelines/public":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v2/search":
			_, _ = w.Write([]byte(`{"statuses":[],"accounts":[],"hashtags":[]}`))
		case "/api/v1/statuses/s1":
			_, _ = w.Write([]byte(`{"id":"s1","content":"hello","account":{"id":"acct1","acct":"arch@example.com"},"visibility":"public"}`))
		case "/api/v1/accounts/alice":
			_, _ = w.Write([]byte(`{"id":"1","username":"alice","acct":"alice"}`))
		case "/api/v1/accounts/verify_credentials":
			_, _ = w.Write([]byte(`{"id":"1","username":"arch","acct":"arch"}`))
		case "/api/v1/accounts/1/followers":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/accounts/1/following":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/accounts/1/follow":
			_, _ = w.Write([]byte(`{"id":"1","following":true}`))
		case "/api/v1/accounts/1/unfollow":
			_, _ = w.Write([]byte(`{"id":"1","following":false}`))
		case "/api/v1/accounts/update_credentials":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
}

type shareScopeRequest struct {
	method string
	path   string
	auth   string
	actAs  string
}

// shareScopeToken signs a grantee/owner-shaped access token: subject and
// username are the agent (arch), the authorizing human rides in DelegatedBy, and
// the token carries the full OAuth scope catalog so both read and write social
// surfaces are exercised.
func shareScopeToken(t *testing.T, delegatedBy string) string {
	t.Helper()
	now := time.Now().UTC()
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "arch",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti-share-scope",
		},
		Username:    "arch",
		Scopes:      []string{"read", "write", "follow", "push"},
		ClientID:    "test-client",
		IsAgent:     true,
		DelegatedBy: delegatedBy,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func shareScopeGranteeToken(t *testing.T) string {
	t.Helper()
	return shareScopeToken(t, "@alice")
}

func shareScopeOwnerToken(t *testing.T) string {
	t.Helper()
	return shareScopeToken(t, "@owner")
}

func invokeShareScopeRPC(t *testing.T, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, payload any) (int, []byte) {
	t.Helper()
	resp := invokeJSONAtPath(t, env, app, path, headers, payload)
	return resp.Status, resp.Body
}

func assertShareScopeNoRPCError(t *testing.T, status int, body []byte) {
	t.Helper()
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, string(body))
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal rpc response: %v (body=%s)", err, string(body))
	}
	if raw, ok := envelope["error"]; ok && raw != nil {
		t.Fatalf("unexpected rpc error: %s", string(body))
	}
}

// TestShareScopeGranteeProxiesAgentSubjectBearer drives the composed production
// chain (authorization -> actor binding -> tool dispatch) with a signed
// grantee-shaped JWT (subject = agent, DelegatedBy = grantee) and a stubbed
// lesser. Every previously-gated read/write site must reach lesser with the
// grantee's own bearer (which authenticates as the agent) and succeed, with no
// X-Lesser-Act-As header on surfaces that do not carry act-as attribution.
func TestShareScopeGranteeProxiesAgentSubjectBearer(t *testing.T) {
	installShareScopeAppEnv(t)
	installSoulBindingLookup(t, "arch", "0x1234567890abcdef")

	var got []shareScopeRequest
	installShareScopeLesser(t, "grantee", func(method, path, auth, actAs string) {
		got = append(got, shareScopeRequest{method: method, path: path, auth: auth, actAs: actAs})
	})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := shareScopeGranteeToken(t)
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/arch", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("grantee initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}

	sessionHeader := map[string][]string{"authorization": {authHeader}}
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionHeader["mcp-session-id"] = ids
	}

	tools := []struct {
		name string
		args map[string]any
	}{
		{"timeline_read", map[string]any{"timeline": "local"}},
		{"timeline_read", map[string]any{"timeline": "federated"}},
		{"post_search", map[string]any{"query": "hello"}},
		{"post_get", map[string]any{"id": "s1"}},
		{"account_resolve", map[string]any{"account": "alice"}},
		{"followers_list", map[string]any{}},
		{"following_list", map[string]any{}},
		{"follow", map[string]any{"account_id": "1"}},
		{"unfollow", map[string]any{"account_id": "1"}},
		{"profile_update", map[string]any{"display_name": "x"}},
	}
	for i, tc := range tools {
		params, _ := json.Marshal(map[string]any{"name": tc.name, "arguments": tc.args})
		status, body := invokeShareScopeRPC(t, env, app, "/mcp/arch", sessionHeader, &mcpruntime.Request{
			JSONRPC: "2.0", ID: i + 2, Method: "tools/call", Params: params,
		})
		assertShareScopeNoRPCError(t, status, body)
	}

	resources := []string{
		"agent://timeline/local",
		"agent://followers",
		"agent://following",
	}
	for i, uri := range resources {
		params, _ := json.Marshal(map[string]any{"uri": uri})
		status, body := invokeShareScopeRPC(t, env, app, "/mcp/arch", sessionHeader, &mcpruntime.Request{
			JSONRPC: "2.0", ID: i + 100, Method: "resources/read", Params: params,
		})
		assertShareScopeNoRPCError(t, status, body)
	}

	socialRequests := 0
	for _, r := range got {
		if r.path == "/api/v1/agents/arch/access" {
			continue
		}
		socialRequests++
		if r.auth != authHeader {
			t.Fatalf("grantee must proxy the agent-subject bearer, got auth=%q for %s %s", r.auth, r.method, r.path)
		}
		if r.actAs != "" {
			t.Fatalf("non-act-as surface must not send X-Lesser-Act-As, got %q for %s %s", r.actAs, r.method, r.path)
		}
	}
	if socialRequests == 0 {
		t.Fatal("expected grantee social requests to reach lesser")
	}
}

// TestShareScopeOwnerPathByteIdentical drives the same composed chain with an
// owner-shaped JWT (subject = agent, DelegatedBy = owner) and asserts the tools
// and resources succeed with no X-Lesser-Act-As header, pinning the owner path
// byte-identical to its pre-change behavior.
func TestShareScopeOwnerPathByteIdentical(t *testing.T) {
	installShareScopeAppEnv(t)
	installSoulBindingLookup(t, "arch", "0x1234567890abcdef")

	var got []shareScopeRequest
	installShareScopeLesser(t, "owner", func(method, path, auth, actAs string) {
		got = append(got, shareScopeRequest{method: method, path: path, auth: auth, actAs: actAs})
	})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := shareScopeOwnerToken(t)
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/arch", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("owner initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionHeader := map[string][]string{"authorization": {authHeader}}
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionHeader["mcp-session-id"] = ids
	}

	tools := []struct {
		name string
		args map[string]any
	}{
		{"timeline_read", map[string]any{"timeline": "local"}},
		{"post_search", map[string]any{"query": "hello"}},
		{"post_get", map[string]any{"id": "s1"}},
		{"account_resolve", map[string]any{"account": "alice"}},
		{"followers_list", map[string]any{}},
		{"following_list", map[string]any{}},
		{"follow", map[string]any{"account_id": "1"}},
		{"unfollow", map[string]any{"account_id": "1"}},
		{"profile_update", map[string]any{"display_name": "x"}},
	}
	for i, tc := range tools {
		params, _ := json.Marshal(map[string]any{"name": tc.name, "arguments": tc.args})
		status, body := invokeShareScopeRPC(t, env, app, "/mcp/arch", sessionHeader, &mcpruntime.Request{
			JSONRPC: "2.0", ID: i + 2, Method: "tools/call", Params: params,
		})
		assertShareScopeNoRPCError(t, status, body)
	}

	for _, uri := range []string{"agent://timeline/local", "agent://followers", "agent://following"} {
		params, _ := json.Marshal(map[string]any{"uri": uri})
		status, body := invokeShareScopeRPC(t, env, app, "/mcp/arch", sessionHeader, &mcpruntime.Request{
			JSONRPC: "2.0", ID: 200, Method: "resources/read", Params: params,
		})
		assertShareScopeNoRPCError(t, status, body)
	}

	for _, r := range got {
		if r.path == "/api/v1/agents/arch/access" {
			continue
		}
		if r.auth != authHeader {
			t.Fatalf("owner must proxy its own bearer, got auth=%q for %s %s", r.auth, r.method, r.path)
		}
		if r.actAs != "" {
			t.Fatalf("owner path must never send X-Lesser-Act-As, got %q for %s %s", r.actAs, r.method, r.path)
		}
	}
}
