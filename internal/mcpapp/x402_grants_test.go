package mcpapp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
)

func TestX402Grant_AllowsScopedToolCallWithoutOAuthSession(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotReq mcpapp.X402GrantConsumeRequestForTests
	restore := mcpapp.SetX402GrantConsumerForTests(func(_ context.Context, req mcpapp.X402GrantConsumeRequestForTests) (mcpapp.X402GrantConsumeResponseForTests, error) {
		gotReq = req
		return validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour)), nil
	})
	defer restore()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	resp := invokeX402Tool(t, app, "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "paid hi"})
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("tools/call error: %+v", rpc.Error)
	}

	if gotReq.GrantID != "grant-123" || gotReq.GrantToken != "grant-token" {
		t.Fatalf("host consume did not receive grant id/token binding: %+v", gotReq)
	}
	if gotReq.Actor != "agent1" || gotReq.AgentID != "agent1" || gotReq.Capability != "tools.invoke" || gotReq.Tool != "echo" {
		t.Fatalf("unexpected consume request binding: %+v", gotReq)
	}
	if gotReq.Resource != "https://api.example.com/mcp/agent1" {
		t.Fatalf("unexpected resource binding: %q", gotReq.Resource)
	}
	if gotReq.PaymentEvidenceHash == "" || strings.Contains(gotReq.PaymentEvidenceHash, "payment-signature-value") {
		t.Fatalf("payment evidence must be hashed before host consume validation, got %q", gotReq.PaymentEvidenceHash)
	}
	wireReq, err := json.Marshal(gotReq)
	if err != nil {
		t.Fatalf("marshal consume request: %v", err)
	}
	wire := string(wireReq)
	for _, forbidden := range []string{"grantId", "actor", "paymentEvidenceHash"} {
		if strings.Contains(wire, forbidden) {
			t.Fatalf("consume request body should match accepted Host shape and omit %s: %s", forbidden, wire)
		}
	}
	for _, required := range []string{"grantToken", "agentId", "capability", "tool", "resource", "requestHash", "idempotencyKey"} {
		if !strings.Contains(wire, required) {
			t.Fatalf("consume request body missing accepted Host field %s: %s", required, wire)
		}
	}
}

func TestX402Grant_DefaultConsumerCallsAcceptedHostConsumeContract(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	auth.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)

	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST consume request, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode consume request: %v", err)
		}
		forbidden := []string{"grantId", "actor", "paymentEvidenceHash"}
		for _, key := range forbidden {
			if _, ok := gotBody[key]; ok {
				t.Fatalf("consume request body must omit %s: %+v", key, gotBody)
			}
		}
		evidenceHash := sha256.Sum256([]byte("payment-signature-value"))
		response := map[string]any{
			"accepted": true,
			"replayed": false,
			"grant": map[string]any{
				"grantId":           "grant-123",
				"agentId":           gotBody["agentId"],
				"capability":        gotBody["capability"],
				"tool":              gotBody["tool"],
				"resource":          gotBody["resource"],
				"requestHash":       gotBody["requestHash"],
				"callerSubjectHash": "caller-subject-hash",
				"payment": map[string]any{
					"evidenceHash": hex.EncodeToString(evidenceHash[:]),
				},
				"policyVersion": "caller-access-payment/v1",
				"authority":     "scoped_invocation",
				"status":        "issued",
				"maxUsage":      1,
				"usedCount":     1,
				"expiresAt":     time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			},
			"usage": map[string]any{
				"usedCount": 1,
				"maxUsage":  1,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	t.Setenv("LESSER_HOST_URL", server.URL)

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "paid hi"})
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (%s)", resp.Status, string(resp.Body))
	}
	if gotPath != "/api/v1/soul/x402/grants/grant-123/consume" {
		t.Fatalf("expected accepted Host consume path, got %q", gotPath)
	}
	if gotAuth != "Bearer instance-key-123" {
		t.Fatalf("expected instance-key bearer auth, got %q", gotAuth)
	}
	for _, required := range []string{"grantToken", "agentId", "capability", "tool", "resource", "requestHash", "idempotencyKey"} {
		value, _ := gotBody[required].(string)
		if strings.TrimSpace(value) == "" {
			t.Fatalf("consume request body missing %s: %+v", required, gotBody)
		}
	}
	if gotBody["grantToken"] != "grant-token" || gotBody["capability"] != "tools.invoke" || gotBody["tool"] != "echo" {
		t.Fatalf("unexpected consume request body: %+v", gotBody)
	}
}

func TestX402Grant_RejectsWrongAgentID(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
		resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
		resp.Grant.AgentID = "other-agent"
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_agent_mismatch")
}

func TestX402Grant_RejectsWrongTool(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
		resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
		resp.Grant.Tool = "profile_read"
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_tool_mismatch")
}

func TestX402Grant_RejectsExpiredGrant(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
		return validX402GrantConsumeResponse(req, time.Now().UTC().Add(-time.Minute))
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_expired")
}

func TestX402Grant_RejectsReplay(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
		resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
		resp.Replayed = true
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_replay")
}

func TestX402Grant_RejectsMissingCallerEvidenceAndUsageLimit(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*mcpapp.X402GrantConsumeResponseForTests)
		reason string
	}{
		{
			name: "caller evidence",
			mutate: func(resp *mcpapp.X402GrantConsumeResponseForTests) {
				resp.Grant.CallerSubjectHash = ""
			},
			reason: "x402_caller_evidence_missing",
		},
		{
			name: "usage limit",
			mutate: func(resp *mcpapp.X402GrantConsumeResponseForTests) {
				resp.Grant.MaxUsage = 0
				resp.Usage.MaxUsage = 0
			},
			reason: "x402_usage_limit_missing",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
				resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
				scenario.mutate(&resp)
				return resp
			})
			defer restore()

			resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
			assertX402FailureReason(t, resp, 403, scenario.reason)
		})
	}
}

func TestX402Grant_RejectsMissingPaymentEvidence(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	hostCalled := false
	restore := mcpapp.SetX402GrantConsumerForTests(func(_ context.Context, req mcpapp.X402GrantConsumeRequestForTests) (mcpapp.X402GrantConsumeResponseForTests, error) {
		hostCalled = true
		return validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour)), nil
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 402, "missing_payment_evidence")
	if hostCalled {
		t.Fatalf("missing payment evidence should fail before host validation")
	}
}

func TestX402Grant_RejectsMixedOAuthAndGrantAuth(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	hostCalled := false
	restore := mcpapp.SetX402GrantConsumerForTests(func(_ context.Context, req mcpapp.X402GrantConsumeRequestForTests) (mcpapp.X402GrantConsumeResponseForTests, error) {
		hostCalled = true
		return validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour)), nil
	})
	defer restore()

	app := mustNewTestApp(t)
	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	params, _ := json.Marshal(map[string]any{"name": "echo", "arguments": map[string]any{"message": "hi"}})
	resp := invokeJSONAtPath(t, testkit.New(), app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"lesser-x402-grant":    {"grant-token"},
		"payment-signature":    {"payment-signature-value"},
		"mcp-protocol-version": {"2025-11-25"},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params})
	assertX402FailureReason(t, resp, 403, "x402_mixed_auth_not_allowed")
	if hostCalled {
		t.Fatalf("mixed auth should fail before host validation")
	}
}

func TestX402Grant_RejectsWrongOrMissingPolicyVersion(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		policyVersion string
	}{
		{name: "missing", policyVersion: ""},
		{name: "wrong", policyVersion: "caller-access-payment/v0"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
				resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
				resp.Grant.PolicyVersion = scenario.policyVersion
				return resp
			})
			defer restore()

			resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
			assertX402FailureReason(t, resp, 403, "x402_policy_version_mismatch")
		})
	}
}

func TestX402Grant_RejectsRequestResourceBindingMismatch(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*mcpapp.X402GrantConsumeResponseForTests)
		reason string
	}{
		{
			name: "resource",
			mutate: func(resp *mcpapp.X402GrantConsumeResponseForTests) {
				resp.Grant.Resource = "https://api.example.com/mcp/other-agent"
			},
			reason: "x402_resource_mismatch",
		},
		{
			name: "request hash",
			mutate: func(resp *mcpapp.X402GrantConsumeResponseForTests) {
				resp.Grant.RequestHash = strings.Repeat("0", 64)
			},
			reason: "x402_request_hash_mismatch",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests {
				resp := validX402GrantConsumeResponse(req, time.Now().UTC().Add(time.Hour))
				scenario.mutate(&resp)
				return resp
			})
			defer restore()

			resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
			assertX402FailureReason(t, resp, 403, scenario.reason)
		})
	}
}

func setupX402GrantTest(t *testing.T, response func(mcpapp.X402GrantConsumeRequestForTests) mcpapp.X402GrantConsumeResponseForTests) func() {
	t.Helper()
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	return mcpapp.SetX402GrantConsumerForTests(func(_ context.Context, req mcpapp.X402GrantConsumeRequestForTests) (mcpapp.X402GrantConsumeResponseForTests, error) {
		return response(req), nil
	})
}

func mustNewTestApp(t *testing.T) *apptheory.App {
	t.Helper()
	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func invokeX402Tool(t *testing.T, app *apptheory.App, actor string, grant string, paymentEvidence string, tool string, args map[string]any) apptheory.Response {
	t.Helper()
	headers := map[string][]string{
		"lesser-x402-grant":      {grant},
		"lesser-x402-grant-id":   {"grant-123"},
		"lesser-x402-capability": {"tools.invoke"},
	}
	if paymentEvidence != "" {
		headers["payment-signature"] = []string{paymentEvidence}
	}
	env := testkit.New()
	initResp := invokeJSONAtPath(t, env, app, "/mcp/"+actor, map[string][]string{
		"lesser-x402-grant": {grant},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("x402 initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := ""
	if ids := initResp.Headers["mcp-session-id"]; len(ids) > 0 {
		sessionID = ids[0]
	}
	if sessionID == "" {
		t.Fatalf("expected x402 initialize to return mcp-session-id")
	}
	headers["mcp-session-id"] = []string{sessionID}
	callParams, _ := json.Marshal(map[string]any{
		"name":      tool,
		"arguments": args,
	})
	return invokeJSONAtPath(t, env, app, "/mcp/"+actor, headers, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  callParams,
	})
}

func validX402GrantConsumeResponse(req mcpapp.X402GrantConsumeRequestForTests, expiresAt time.Time) mcpapp.X402GrantConsumeResponseForTests {
	return mcpapp.X402GrantConsumeResponseForTests{
		Accepted: true,
		Replayed: false,
		Grant: mcpapp.X402InvocationGrantViewForTests{
			GrantID:           req.GrantID,
			AgentID:           req.AgentID,
			Actor:             req.Actor,
			Tool:              req.Tool,
			Capability:        req.Capability,
			PolicyVersion:     "caller-access-payment/v1",
			Authority:         "scoped_invocation",
			Status:            "issued",
			Resource:          req.Resource,
			RequestHash:       req.RequestHash,
			CallerSubjectHash: req.PaymentEvidenceHash,
			Payment: mcpapp.X402GrantPaymentBindingForTests{
				EvidenceHash: req.PaymentEvidenceHash,
			},
			ExpiresAt: expiresAt.Format(time.RFC3339),
			MaxUsage:  1,
			UsedCount: 1,
		},
		Usage: mcpapp.X402InvocationGrantUsageForTests{
			UsedCount: 1,
			MaxUsage:  1,
		},
	}
}

func assertX402FailureReason(t *testing.T, resp apptheory.Response, status int, reason string) {
	t.Helper()
	if resp.Status != status {
		t.Fatalf("expected status %d, got %d (%s)", status, resp.Status, string(resp.Body))
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
	if out.Error.Details["reason"] != reason {
		t.Fatalf("expected reason %q, got %+v", reason, out.Error.Details)
	}
	if details := out.Error.Details; details["paymentEvidence"] != nil || details["grant"] != nil {
		t.Fatalf("x402 failure leaked raw evidence/grant details: %+v", details)
	}
}
