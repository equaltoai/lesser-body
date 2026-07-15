package instanceapp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
)

func TestInstancePlaneMCP_InitializeAndToolsList(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name       string
		path       string
		serverName string
	}{
		{name: "ptah", path: "/instance/ptah/mcp", serverName: "lesser-body-instance-ptah"},
		{name: "ba", path: "/instance/ba/mcp", serverName: "lesser-body-instance-ba"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath(tc.path))

			initResp := invokeMCP(t, env, app, tc.path, bearerHeaders(token), &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if initResp.Status != 200 {
				t.Fatalf("initialize status = %d, body = %s", initResp.Status, string(initResp.Body))
			}

			var initBody struct {
				Result struct {
					ServerInfo struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"serverInfo"`
				} `json:"result"`
			}
			if err := json.Unmarshal(initResp.Body, &initBody); err != nil {
				t.Fatalf("decode initialize response: %v", err)
			}
			if initBody.Result.ServerInfo.Name != tc.serverName {
				t.Fatalf("server name = %q, want %q", initBody.Result.ServerInfo.Name, tc.serverName)
			}
			if initBody.Result.ServerInfo.Version != "dev" {
				t.Fatalf("server version = %q, want dev", initBody.Result.ServerInfo.Version)
			}

			sessionID := firstHeader(initResp.Headers, "mcp-session-id")
			if sessionID == "" {
				t.Fatalf("initialize response did not include mcp-session-id")
			}

			headers := bearerHeaders(token)
			headers["mcp-session-id"] = []string{sessionID}
			headers["mcp-protocol-version"] = []string{"2025-11-25"}
			listResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      2,
				Method:  "tools/list",
			})
			if listResp.Status != 200 {
				t.Fatalf("tools/list status = %d, body = %s", listResp.Status, string(listResp.Body))
			}

			var listBody struct {
				Result struct {
					Tools []mcpruntime.ToolDef `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal(listResp.Body, &listBody); err != nil {
				t.Fatalf("decode tools/list response: %v", err)
			}
			if len(listBody.Result.Tools) != 0 {
				t.Fatalf("tools/list returned %d tools, want 0", len(listBody.Result.Tools))
			}
		})
	}
}

func TestInstancePlaneMCP_RejectsDisallowedPrincipals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		setup   func(t testing.TB) map[string][]string
		want    int
		wantErr string
	}{
		{
			name: "agent delegated OAuth token",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "test-secret")
				t.Setenv("JWT_SECRET_ARN", "")
				auth.ResetForTests()
				token := newTestTokenWithAudienceAndAgent(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"), true)
				return bearerHeaders(token)
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "legacy managed instance key despite compatibility flag",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "")
				t.Setenv("JWT_SECRET_ARN", "")
				t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
				t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
				t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
				auth.ResetForTests()
				return bearerHeaders("legacy-instance-key")
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "instance audience mismatch",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "test-secret")
				t.Setenv("JWT_SECRET_ARN", "")
				auth.ResetForTests()
				token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ba/mcp"))
				return bearerHeaders(token)
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := instanceapp.New("lesser-body-instance", "dev")
			if err != nil {
				t.Fatalf("new app: %v", err)
			}
			env := testkit.New()

			resp := invokeMCP(t, env, app, tc.path, tc.setup(t), &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if resp.Status != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", resp.Status, tc.want, string(resp.Body))
			}
			if !strings.Contains(string(resp.Body), tc.wantErr) {
				t.Fatalf("response did not include %q: %s", tc.wantErr, string(resp.Body))
			}
		})
	}
}

func TestInstancePlaneMCP_RejectsMissingInstanceAudience(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test-secret", "agent1", []string{"read"})

	resp := invokeMCP(t, env, app, "/instance/ptah/mcp", bearerHeaders(token), &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 403 {
		t.Fatalf("status = %d, want 403; body = %s", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "instance_principal_not_allowed") {
		t.Fatalf("response did not include instance_principal_not_allowed: %s", string(resp.Body))
	}
}

func TestInstancePlaneMCP_RejectsMissingHostForAudienceCheck(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))

	resp := invokeMCP(t, env, app, "/instance/ptah/mcp", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 403 {
		t.Fatalf("status = %d, want 403; body = %s", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "instance_principal_not_allowed") {
		t.Fatalf("response did not include instance_principal_not_allowed: %s", string(resp.Body))
	}
}

func TestInstancePlaneMCP_RejectsX402EvenWithOAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))

	headers := bearerHeaders(token)
	headers["lesser-x402-grant-id"] = []string{"grant-123"}
	headers["lesser-x402-grant"] = []string{"grant-token"}
	headers["lesser-x402-capability"] = []string{"ptah.instance"}
	headers["payment-signature"] = []string{"payment"}
	resp := invokeMCP(t, env, app, "/instance/ptah/mcp", headers, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 400 {
		t.Fatalf("status = %d, want 400; body = %s", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "x402_not_supported") {
		t.Fatalf("response did not include x402_not_supported: %s", string(resp.Body))
	}
}

func TestInstancePlaneMCP_RejectsUnauthenticatedRequests(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name    string
		headers map[string][]string
	}{
		{name: "missing bearer", headers: nil},
		{name: "x402 headers are not auth", headers: map[string][]string{
			"lesser-x402-grant-id":   {"grant-123"},
			"lesser-x402-grant":      {"grant-token"},
			"lesser-x402-capability": {"ptah.instance"},
			"payment-signature":      {"payment"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := invokeMCP(t, env, app, "/instance/ptah/mcp", tc.headers, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if resp.Status != 401 {
				t.Fatalf("status = %d, want 401; body = %s", resp.Status, string(resp.Body))
			}
		})
	}
}

func TestInstancePlaneMCP_UnknownPlaneIsNotMounted(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test-secret", "agent1", []string{"read"})

	resp := invokeMCP(t, env, app, "/instance/ka/mcp", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 404 {
		t.Fatalf("status = %d, want 404; body = %s", resp.Status, string(resp.Body))
	}
}

func TestInstancePlaneWellKnownStubs_FailClosed(t *testing.T) {
	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "ptah", path: "/.well-known/oauth-protected-resource/instance/ptah/mcp"},
		{name: "ba", path: "/.well-known/oauth-protected-resource/instance/ba/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.Invoke(context.Background(), app, apptheory.Request{
				Method: "GET",
				Path:   tc.path,
			})
			if resp.Status != 501 {
				t.Fatalf("status = %d, want 501; body = %s", resp.Status, string(resp.Body))
			}
			if !strings.Contains(string(resp.Body), "instance_metadata_not_implemented") {
				t.Fatalf("response did not include fail-closed stub code: %s", string(resp.Body))
			}
		})
	}
}

func invokeMCP(t testing.TB, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, payload any) apptheory.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	reqHeaders := map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"application/json, text/event-stream"},
	}
	for k, values := range headers {
		reqHeaders[k] = append([]string(nil), values...)
	}

	return env.Invoke(context.Background(), app, apptheory.Request{
		Method:  "POST",
		Path:    path,
		Headers: reqHeaders,
		Body:    body,
	})
}

func firstHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func bearerHeaders(token string) map[string][]string {
	return map[string][]string{
		"authorization":     {"Bearer " + token},
		"host":              {testHost},
		"x-forwarded-proto": {"https"},
	}
}

func audienceForPath(path string) []string {
	return []string{"https://" + testHost + path}
}

const testHost = "api.example.com"

func newTestToken(t testing.TB, secret string, username string, scopes []string) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithClaims(t, secret, &auth.Claims{
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
	})
}

func newTestTokenWithAudience(t testing.TB, secret string, username string, scopes []string, audience []string) string {
	t.Helper()

	return newTestTokenWithAudienceAndAgent(t, secret, username, scopes, audience, false)
}

func newTestTokenWithAudienceAndAgent(t testing.TB, secret string, username string, scopes []string, audience []string, isAgent bool) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithClaims(t, secret, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti_test",
			Audience:  jwt.ClaimStrings(audience),
		},
		Username: username,
		Scopes:   scopes,
		ClientID: "test-client",
		IsAgent:  isAgent,
	})
}

func newTestTokenWithClaims(t testing.TB, secret string, claims *auth.Claims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
