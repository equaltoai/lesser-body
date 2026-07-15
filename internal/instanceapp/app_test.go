package instanceapp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/ptahserver"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
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
		wantTools  []string
	}{
		{name: "ptah", path: "/instance/ptah/mcp", serverName: "lesser-body-instance-ptah", wantTools: []string{"agent_bind_soul"}},
		{name: "ba", path: "/instance/ba/mcp", serverName: "lesser-body-instance-ba", wantTools: []string{}},
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
			if got := toolNames(listBody.Result.Tools); !reflect.DeepEqual(got, tc.wantTools) {
				t.Fatalf("tools/list names = %v, want %v", got, tc.wantTools)
			}
			if tc.name == "ptah" {
				def := listBody.Result.Tools[0]
				if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint {
					t.Fatalf("agent_bind_soul annotations not write/additive: %+v", def.Annotations)
				}
			}
		})
	}
}

func TestInstancePlaneMCP_AgentBindSoulUsesDedicatedBearerAndReplays(t *testing.T) {
	requests := 0
	var captured []lesserapi.SoulBindingRequest
	var capturedAuth []string
	var capturedIdempotency []string
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/souls/bindings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		capturedAuth = append(capturedAuth, r.Header.Get("Authorization"))
		capturedIdempotency = append(capturedIdempotency, r.Header.Get("Idempotency-Key"))
		var body lesserapi.SoulBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Lesser request: %v", err)
		}
		captured = append(captured, body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceSoulBindingResponse(requests > 1)))
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	userToken := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", userToken)

	args := map[string]any{
		"soul_agent_id":        "agent-0xabc",
		"idempotency_key":      "bind-key-1",
		"host_registration_id": "hreg_123",
		"host_conversation_id": "hconv_456",
		"principal_address":    "0x2222222222222222222222222222222222222222",
		"evidence": map[string]any{
			"host_request_id":  "hreq_789",
			"declaration_hash": "sha256:abc",
			"issued_at":        "2026-07-14T16:20:00Z",
		},
	}
	first := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", args)
	if first.Result == nil || first.Result.IsError {
		t.Fatalf("first call result = %+v error = %+v", first.Result, first.Error)
	}
	second := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", args)
	if second.Result == nil || second.Result.IsError {
		t.Fatalf("second call result = %+v error = %+v", second.Result, second.Error)
	}

	if requests != 2 {
		t.Fatalf("Lesser requests = %d, want 2", requests)
	}
	for i, authz := range capturedAuth {
		if authz != "Bearer integration-secret" {
			t.Fatalf("request %d Authorization = %q, want dedicated integration bearer", i+1, authz)
		}
		if strings.Contains(authz, userToken) {
			t.Fatalf("request %d forwarded user OAuth token", i+1)
		}
	}
	for i, key := range capturedIdempotency {
		if key != "bind-key-1" {
			t.Fatalf("request %d Idempotency-Key = %q", i+1, key)
		}
	}
	for i, body := range captured {
		if body.ActorUsername != "agent1" || body.SoulAgentID != "agent-0xabc" || body.BodyActorID != "body://ptah/agent1" {
			t.Fatalf("request %d required/body fields = %+v", i+1, body)
		}
		if body.AuthorityModel != lesserapi.SoulAuthorityModelInstanceTrust || body.AnchorState != lesserapi.SoulAnchorStateHostedOffchain || body.OperationalBinding != lesserapi.SoulOperationalBindingHostedBound {
			t.Fatalf("request %d hints = %+v", i+1, body)
		}
		if body.Evidence.Source != "ptah" || body.Evidence.HostRequestID != "hreq_789" {
			t.Fatalf("request %d evidence = %+v", i+1, body.Evidence)
		}
	}
	if replayed := toolResultData(t, second.Result)["replayed"]; replayed != true {
		t.Fatalf("second replayed = %#v, want true", replayed)
	}
}

func TestInstancePlaneMCP_AgentBindSoulRequiresWriteScope(t *testing.T) {
	requests := 0
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)

	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", map[string]any{
		"soul_agent_id":   "agent-0xabc",
		"idempotency_key": "bind-key-1",
	})
	if out.Result == nil || !out.Result.IsError {
		t.Fatalf("result IsError = false: result=%+v error=%+v", out.Result, out.Error)
	}
	errPayload := toolResultError(t, out.Result)
	if errPayload["code"] != "insufficient_scope" || errPayload["status"] != float64(403) {
		t.Fatalf("error payload = %+v", errPayload)
	}
	if requests != 0 {
		t.Fatalf("Lesser requests = %d, want 0", requests)
	}
}

func TestInstancePlaneMCP_AgentBindSoulDoesNotMutateLocalBindingAndKaResolvesSouledFromFixture(t *testing.T) {
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceSoulBindingResponse(false)))
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	t.Setenv("LESSER_TABLE_NAME", "lesser-test")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	localBindingDB := &instanceBindingDB{
		agentID:  "agent-0xabc",
		username: "agent1",
	}
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return localBindingDB, nil
	})
	t.Cleanup(soulbinding.ResetForTests)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)
	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", map[string]any{
		"soul_agent_id":   "agent-0xabc",
		"idempotency_key": "bind-key-1",
	})
	if out.Result == nil || out.Result.IsError {
		t.Fatalf("tool result = %+v error = %+v", out.Result, out.Error)
	}

	resolved, err := soulbinding.ResolveAgentID(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("ResolveAgentID: %v", err)
	}
	if resolved != "agent-0xabc" {
		t.Fatalf("resolved soul agent = %q, want agent-0xabc", resolved)
	}
	profile := runtimepolicy.ResolveForActor(context.Background(), "agent1")
	if profile.Profile != runtimepolicy.ProfileSouled || !profile.BoundSoul || profile.SoulAgentID != "agent-0xabc" {
		t.Fatalf("runtime profile = %+v, want souled bound profile from soulbinding fixture", profile)
	}
	if localBindingDB.mutations != 0 {
		t.Fatalf("local SOUL_BODY_BINDING mutations = %d, want 0", localBindingDB.mutations)
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

func initializedMCPHeaders(t testing.TB, env *testkit.Env, app *apptheory.App, path string, token string) map[string][]string {
	t.Helper()

	initResp := invokeMCP(t, env, app, path, bearerHeaders(token), &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize status = %d, body = %s", initResp.Status, string(initResp.Body))
	}
	sessionID := firstHeader(initResp.Headers, "mcp-session-id")
	if sessionID == "" {
		t.Fatalf("initialize response missing mcp-session-id")
	}

	headers := bearerHeaders(token)
	headers["mcp-session-id"] = []string{sessionID}
	headers["mcp-protocol-version"] = []string{"2025-11-25"}
	return headers
}

type toolCallOutput struct {
	Result *mcpruntime.ToolResult `json:"result,omitempty"`
	Error  map[string]any         `json:"error,omitempty"`
}

func callMCPTool(t testing.TB, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, name string, args map[string]any) toolCallOutput {
	t.Helper()

	resp := invokeMCP(t, env, app, path, headers, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if resp.Status != 200 {
		t.Fatalf("tools/call status = %d, body = %s", resp.Status, string(resp.Body))
	}
	var out toolCallOutput
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode tools/call response: %v; body=%s", err, string(resp.Body))
	}
	return out
}

func toolNames(defs []mcpruntime.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func toolResultData(t testing.TB, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structuredContent: %+v", result)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("structuredContent.data missing: %+v", result.StructuredContent)
	}
	return data
}

func toolResultError(t testing.TB, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structuredContent: %+v", result)
	}
	payload, _ := result.StructuredContent["error"].(map[string]any)
	if payload == nil {
		t.Fatalf("structuredContent.error missing: %+v", result.StructuredContent)
	}
	return payload
}

func instanceSoulBindingResponse(replayed bool) string {
	return fmt.Sprintf(`{
		"version":"1",
		"status":"bound",
		"binding_state":"bound",
		"agent":{
			"agent_id":"agent-0xabc",
			"domain":"example.com",
			"local_id":"agent1",
			"authority_model":"%s",
			"anchor_state":"%s",
			"operational_binding":"%s",
			"lifecycle_status":"active",
			"published_version":3
		},
		"binding":{
			"agent_username":"agent1",
			"principal_address":"0x1111111111111111111111111111111111111111",
			"bound_at":"2026-07-14T16:20:02Z",
			"updated_at":"2026-07-14T16:20:02Z"
		},
		"idempotency":{
			"key":"bind-key-1",
			"replayed":%t,
			"payload_hash":"sha256:handler-payload"
		},
		"links":{"status":"/api/v1/souls/bindings/agent-0xabc"}
	}`, lesserapi.SoulAuthorityModelInstanceTrust, lesserapi.SoulAnchorStateHostedOffchain, lesserapi.SoulOperationalBindingHostedBound, replayed)
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

type instanceBindingDB struct {
	agentID   string
	username  string
	mutations int
}

func (f *instanceBindingDB) Model(any) tablecore.Query {
	return &instanceBindingQuery{db: f, where: map[string]any{}}
}

func (f *instanceBindingDB) Migrate() error                           { return nil }
func (f *instanceBindingDB) AutoMigrate(...any) error                 { return nil }
func (f *instanceBindingDB) Close() error                             { return nil }
func (f *instanceBindingDB) WithContext(context.Context) tablecore.DB { return f }

type instanceBindingQuery struct {
	db    *instanceBindingDB
	where map[string]any
}

func (q *instanceBindingQuery) Where(field string, _ string, value any) tablecore.Query {
	q.where[field] = value
	return q
}

func (q *instanceBindingQuery) Index(string) tablecore.Query                        { return q }
func (q *instanceBindingQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *instanceBindingQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *instanceBindingQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *instanceBindingQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *instanceBindingQuery) IfNotExists() tablecore.Query                        { return q }
func (q *instanceBindingQuery) IfExists() tablecore.Query                           { return q }
func (q *instanceBindingQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *instanceBindingQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *instanceBindingQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *instanceBindingQuery) Limit(int) tablecore.Query                    { return q }
func (q *instanceBindingQuery) Offset(int) tablecore.Query                   { return q }
func (q *instanceBindingQuery) Select(...string) tablecore.Query             { return q }
func (q *instanceBindingQuery) ConsistentRead() tablecore.Query              { return q }
func (q *instanceBindingQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *instanceBindingQuery) All(any) error                                { return nil }
func (q *instanceBindingQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	return nil, nil
}
func (q *instanceBindingQuery) Count() (int64, error)                     { return 0, nil }
func (q *instanceBindingQuery) Create() error                             { q.db.mutations++; return nil }
func (q *instanceBindingQuery) CreateOrUpdate() error                     { q.db.mutations++; return nil }
func (q *instanceBindingQuery) Update(...string) error                    { q.db.mutations++; return nil }
func (q *instanceBindingQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *instanceBindingQuery) Delete() error                             { q.db.mutations++; return nil }
func (q *instanceBindingQuery) Scan(any) error                            { return nil }
func (q *instanceBindingQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *instanceBindingQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *instanceBindingQuery) BatchGet([]any, any) error                 { return nil }
func (q *instanceBindingQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *instanceBindingQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *instanceBindingQuery) BatchCreate(any) error                      { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchDelete([]any) error                    { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchWrite([]any, []any) error              { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	q.db.mutations++
	return nil
}
func (q *instanceBindingQuery) Cursor(string) tablecore.Query               { return q }
func (q *instanceBindingQuery) SetCursor(string) error                      { return nil }
func (q *instanceBindingQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *instanceBindingQuery) First(dest any) error {
	if q.db == nil {
		return nil
	}
	return setStringFields(dest, map[string]string{
		"AgentID":  q.db.agentID,
		"Username": q.db.username,
	})
}

func setStringFields(dest any, values map[string]string) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}
	for field, value := range values {
		fv := elem.FieldByName(field)
		if !fv.IsValid() || !fv.CanSet() || fv.Kind() != reflect.String {
			continue
		}
		fv.SetString(value)
	}
	return nil
}
