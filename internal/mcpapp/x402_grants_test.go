package mcpapp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
)

func TestX402Grant_AllowsScopedToolCallWithoutOAuthSession(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotReq mcpapp.X402GrantValidationRequestForTests
	restore := mcpapp.SetX402GrantValidatorForTests(func(_ context.Context, req mcpapp.X402GrantValidationRequestForTests) (mcpapp.X402GrantValidationResponseForTests, error) {
		gotReq = req
		return validX402GrantResponse(req, time.Now().UTC().Add(time.Hour)), nil
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

	if gotReq.Grant != "grant-token" {
		t.Fatalf("host validation did not receive opaque grant")
	}
	if gotReq.Actor != "agent1" || gotReq.AgentID != "agent1" || gotReq.Tool != "echo" || gotReq.Method != "tools/call" {
		t.Fatalf("unexpected validation request binding: %+v", gotReq)
	}
	if gotReq.Resource != "https://api.example.com/mcp/agent1" {
		t.Fatalf("unexpected resource binding: %q", gotReq.Resource)
	}
	if gotReq.PaymentEvidenceHash == "" || strings.Contains(gotReq.PaymentEvidenceHash, "payment-signature-value") {
		t.Fatalf("payment evidence must be hashed before host validation, got %q", gotReq.PaymentEvidenceHash)
	}
}

func TestX402Grant_RejectsWrongAgentID(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
		resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
		resp.AgentID = "other-agent"
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_agent_mismatch")
}

func TestX402Grant_RejectsWrongTool(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
		resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
		resp.Tool = "profile_read"
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_tool_mismatch")
}

func TestX402Grant_RejectsExpiredGrant(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
		return validX402GrantResponse(req, time.Now().UTC().Add(-time.Minute))
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_expired")
}

func TestX402Grant_RejectsReplay(t *testing.T) {
	restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
		resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
		resp.UsageAccepted = false
		resp.Replay = true
		return resp
	})
	defer restore()

	resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
	assertX402FailureReason(t, resp, 403, "x402_grant_replay")
}

func TestX402Grant_RejectsMissingCallerEvidenceAndUsageLimit(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*mcpapp.X402GrantValidationResponseForTests)
		reason string
	}{
		{
			name: "caller evidence",
			mutate: func(resp *mcpapp.X402GrantValidationResponseForTests) {
				resp.CallerEvidenceHash = ""
			},
			reason: "x402_caller_evidence_missing",
		},
		{
			name: "usage limit",
			mutate: func(resp *mcpapp.X402GrantValidationResponseForTests) {
				resp.MaxUses = 0
			},
			reason: "x402_usage_limit_missing",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
				resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
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
	restore := mcpapp.SetX402GrantValidatorForTests(func(_ context.Context, req mcpapp.X402GrantValidationRequestForTests) (mcpapp.X402GrantValidationResponseForTests, error) {
		hostCalled = true
		return validX402GrantResponse(req, time.Now().UTC().Add(time.Hour)), nil
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
	restore := mcpapp.SetX402GrantValidatorForTests(func(_ context.Context, req mcpapp.X402GrantValidationRequestForTests) (mcpapp.X402GrantValidationResponseForTests, error) {
		hostCalled = true
		return validX402GrantResponse(req, time.Now().UTC().Add(time.Hour)), nil
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
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
				resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
				resp.PolicyVersion = scenario.policyVersion
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
		mutate func(*mcpapp.X402GrantValidationResponseForTests)
		reason string
	}{
		{
			name: "resource",
			mutate: func(resp *mcpapp.X402GrantValidationResponseForTests) {
				resp.Resource = "https://api.example.com/mcp/other-agent"
			},
			reason: "x402_resource_mismatch",
		},
		{
			name: "request hash",
			mutate: func(resp *mcpapp.X402GrantValidationResponseForTests) {
				resp.RequestHash = "sha256:wrong"
			},
			reason: "x402_request_hash_mismatch",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restore := setupX402GrantTest(t, func(req mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests {
				resp := validX402GrantResponse(req, time.Now().UTC().Add(time.Hour))
				scenario.mutate(&resp)
				return resp
			})
			defer restore()

			resp := invokeX402Tool(t, mustNewTestApp(t), "agent1", "grant-token", "payment-signature-value", "echo", map[string]any{"message": "hi"})
			assertX402FailureReason(t, resp, 403, scenario.reason)
		})
	}
}

func setupX402GrantTest(t *testing.T, response func(mcpapp.X402GrantValidationRequestForTests) mcpapp.X402GrantValidationResponseForTests) func() {
	t.Helper()
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	return mcpapp.SetX402GrantValidatorForTests(func(_ context.Context, req mcpapp.X402GrantValidationRequestForTests) (mcpapp.X402GrantValidationResponseForTests, error) {
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
		"lesser-x402-grant": {grant},
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

func validX402GrantResponse(req mcpapp.X402GrantValidationRequestForTests, expiresAt time.Time) mcpapp.X402GrantValidationResponseForTests {
	return mcpapp.X402GrantValidationResponseForTests{
		Valid:               true,
		GrantID:             "grant-123",
		AgentID:             req.AgentID,
		Actor:               req.Actor,
		Tool:                req.Tool,
		Capability:          req.Tool,
		Scope:               req.Method,
		GrantVersion:        req.Version,
		PolicyVersion:       "caller-access-payment/v1",
		Resource:            req.Resource,
		RequestHash:         req.RequestHash,
		PaymentEvidenceHash: req.PaymentEvidenceHash,
		CallerEvidenceHash:  req.PaymentEvidenceHash,
		ExpiresAt:           expiresAt.Format(time.RFC3339),
		MaxUses:             1,
		RemainingUses:       0,
		UsageAccepted:       true,
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
