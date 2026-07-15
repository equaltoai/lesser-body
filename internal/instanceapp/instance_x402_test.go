package instanceapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/ptahserver"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
)

const testSoulAgentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestInstancePlaneX402_AgentCreateRequiresGrantBeforeSideEffects(t *testing.T) {
	requests := 0
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer lesser.Close()

	registryStore := newInstanceAgentRegistryStore(t)
	resetRegistry := ptahserver.SetAgentRegistryFactoryForTests(func() (ptahserver.AgentRegistry, error) {
		return registryStore, nil
	})
	t.Cleanup(resetRegistry)
	resetConsumer := instancex402.SetConsumerForTests(func(context.Context, instancex402.ConsumeRequestForTests) (instancex402.ConsumeResponseForTests, error) {
		t.Fatalf("missing x402 grant must fail before Host consume")
		return instancex402.ConsumeResponseForTests{}, nil
	})
	t.Cleanup(resetConsumer)

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)

	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_create", map[string]any{
		"agent_username": "ptah_agent",
		"actor_username": "agent1",
		"scopes":         []string{"read"},
	})
	if out.Result == nil || !out.Result.IsError {
		t.Fatalf("agent_create without grant result = %+v error=%+v", out.Result, out.Error)
	}
	errPayload := toolResultError(t, out.Result)
	if errPayload["code"] != "payment_required" || instanceStatusValue(errPayload["status"]) != 402 {
		t.Fatalf("x402 missing-grant payload = %+v", errPayload)
	}
	if reason := toolResultErrorReason(t, out.Result); reason != "x402_grant_required" {
		t.Fatalf("x402 reason = %q, want x402_grant_required", reason)
	}
	if requests != 0 {
		t.Fatalf("Lesser side effects = %d, want 0", requests)
	}
	if _, err := registryStore.Get(context.Background(), "agent1", "https://lesser.example/users/ptah_agent"); err == nil {
		t.Fatalf("registry entry was created before x402 grant")
	}
}

func TestInstancePlaneX402_BaInstallPlanRequiresGrantBeforeDownloadGrant(t *testing.T) {
	const endpoint = "https://api.dev.example.com/instance/{surface}/mcp"
	resetConsumer := instancex402.SetConsumerForTests(func(context.Context, instancex402.ConsumeRequestForTests) (instancex402.ConsumeResponseForTests, error) {
		t.Fatalf("missing x402 grant must fail before Host consume")
		return instancex402.ConsumeResponseForTests{}, nil
	})
	t.Cleanup(resetConsumer)

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, endpoint)
	auth.ResetForTests()

	grantStore := newDownloadGrantStore(t)
	content := newBaPlanContentStore("agent1", "agent-one")
	app, err := instanceapp.New("lesser-body-instance", "dev",
		instanceapp.WithDownloadGrantStore(grantStore),
		instanceapp.WithBaContentStore(content),
		instanceapp.WithBaInstanceEndpoint(endpoint),
		instanceapp.WithBaNamespace("equaltoai"),
		instanceapp.WithBaToolOptions(baserver.WithRateLimiter(baserver.NewInMemoryGrantMintLimiter(10, time.Minute))),
	)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ba/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ba/mcp", token)

	out := callMCPTool(t, env, app, "/instance/ba/mcp", headers, baserver.ToolAgentLocalInstallPlan, map[string]any{
		"agent_id":       "agent-one",
		"client":         "codex",
		"actor_username": "agent1",
	})
	if out.Result == nil || !out.Result.IsError {
		t.Fatalf("install plan without grant result = %+v error=%+v", out.Result, out.Error)
	}
	errPayload := toolResultError(t, out.Result)
	if errPayload["code"] != "payment_required" || instanceStatusValue(errPayload["status"]) != 402 {
		t.Fatalf("x402 missing-grant payload = %+v", errPayload)
	}
	if reason := toolResultErrorReason(t, out.Result); reason != "x402_grant_required" {
		t.Fatalf("x402 reason = %q, want x402_grant_required", reason)
	}
}

func TestInstancePlaneX402_AgentCreateConsumesThenProceedsAndReplayStopsSideEffect(t *testing.T) {
	consumeCalls := 0
	var captured []instancex402.ConsumeRequestForTests
	resetResolver := instancex402.SetAgentIDResolverForTests(func(_ context.Context, account string) (string, error) {
		if account != "agent1" {
			t.Fatalf("resolved account = %q, want agent1", account)
		}
		return testSoulAgentID, nil
	})
	t.Cleanup(resetResolver)
	resetConsumer := instancex402.SetConsumerForTests(func(_ context.Context, req instancex402.ConsumeRequestForTests) (instancex402.ConsumeResponseForTests, error) {
		consumeCalls++
		captured = append(captured, req)
		resp := acceptedInstanceGrantResponse(req)
		if consumeCalls > 1 {
			resp.Replayed = true
		}
		return resp, nil
	})
	t.Cleanup(resetConsumer)

	lesserRequests := 0
	var capturedAuth string
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if consumeCalls == 0 {
			t.Fatalf("Lesser side effect ran before Host x402 consume")
		}
		lesserRequests++
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceAgentDelegationResponse()))
	}))
	defer lesser.Close()

	registryStore := newInstanceAgentRegistryStore(t)
	resetRegistry := ptahserver.SetAgentRegistryFactoryForTests(func() (ptahserver.AgentRegistry, error) {
		return registryStore, nil
	})
	t.Cleanup(resetRegistry)

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-secret")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)
	addInstanceX402Headers(headers, "grant-agent-create", "raw-grant-token-agent-create", instancex402.CapabilityAgentCreate, "raw-payment-evidence-agent-create")
	args := map[string]any{
		"agent_username": "ptah_agent",
		"actor_username": "agent1",
		"scopes":         []string{"read"},
	}

	first := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_create", args)
	if first.Result == nil || first.Result.IsError {
		t.Fatalf("first agent_create result = %+v error=%+v", first.Result, first.Error)
	}
	if consumeCalls != 1 || lesserRequests != 1 {
		t.Fatalf("first calls consume=%d lesser=%d, want 1/1", consumeCalls, lesserRequests)
	}
	if capturedAuth != "Bearer "+token {
		t.Fatalf("Lesser Authorization = %q, want caller bearer", capturedAuth)
	}
	assertInstanceConsumeRequest(t, captured[0], instancex402.CapabilityAgentCreate, "agent_create", instancex402.ResourceAgentCreate, "raw-payment-evidence-agent-create")
	assertNoRawX402Leak(t, first.Result, "raw-grant-token-agent-create", "raw-payment-evidence-agent-create", "host-instance-key-secret")

	second := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_create", args)
	if reason := toolResultErrorReason(t, second.Result); reason != "x402_grant_replay" {
		t.Fatalf("second x402 reason = %q, want x402_grant_replay; result=%+v", reason, second.Result)
	}
	if consumeCalls != 2 || lesserRequests != 1 {
		t.Fatalf("replay calls consume=%d lesser=%d, want 2/1", consumeCalls, lesserRequests)
	}
}

func TestInstancePlaneX402_BaInstallPlanConsumesInstanceGrantThenMintsDownloadGrant(t *testing.T) {
	const endpoint = "https://api.dev.example.com/instance/{surface}/mcp"
	consumeCalls := 0
	var captured instancex402.ConsumeRequestForTests
	resetResolver := instancex402.SetAgentIDResolverForTests(func(_ context.Context, account string) (string, error) {
		if account != "agent1" {
			t.Fatalf("resolved account = %q, want agent1", account)
		}
		return testSoulAgentID, nil
	})
	t.Cleanup(resetResolver)
	resetConsumer := instancex402.SetConsumerForTests(func(_ context.Context, req instancex402.ConsumeRequestForTests) (instancex402.ConsumeResponseForTests, error) {
		consumeCalls++
		captured = req
		return acceptedInstanceGrantResponse(req), nil
	})
	t.Cleanup(resetConsumer)

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, endpoint)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-secret")
	auth.ResetForTests()

	grantStore := newDownloadGrantStore(t)
	content := newBaPlanContentStore("agent1", "agent-one")
	app, err := instanceapp.New("lesser-body-instance", "dev",
		instanceapp.WithDownloadGrantStore(grantStore),
		instanceapp.WithBaContentStore(content),
		instanceapp.WithBaInstanceEndpoint(endpoint),
		instanceapp.WithBaNamespace("equaltoai"),
		instanceapp.WithBaToolOptions(baserver.WithRateLimiter(baserver.NewInMemoryGrantMintLimiter(10, time.Minute))),
	)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ba/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ba/mcp", token)
	addInstanceX402Headers(headers, "grant-install-plan", "raw-grant-token-install-plan", instancex402.CapabilityInstallPlan, "raw-payment-evidence-install-plan")

	out := callMCPTool(t, env, app, "/instance/ba/mcp", headers, baserver.ToolAgentLocalInstallPlan, map[string]any{
		"agent_id":       "agent-one",
		"client":         "codex",
		"actor_username": "agent1",
	})
	if out.Result == nil || out.Result.IsError {
		t.Fatalf("install plan result = %+v error=%+v", out.Result, out.Error)
	}
	if consumeCalls != 1 {
		t.Fatalf("Host consume calls = %d, want 1", consumeCalls)
	}
	assertInstanceConsumeRequest(t, captured, instancex402.CapabilityInstallPlan, baserver.ToolAgentLocalInstallPlan, instancex402.ResourceInstallPlan, "raw-payment-evidence-install-plan")
	data := toolResultData(t, out.Result)
	if data["grant_id"] == "" || data["download_url"] == "" {
		t.Fatalf("install plan did not mint download grant: %+v", data)
	}
	assertNoRawX402Leak(t, out.Result, "raw-grant-token-install-plan", "raw-payment-evidence-install-plan", "host-instance-key-secret")
}

func acceptedInstanceGrantResponse(req instancex402.ConsumeRequestForTests) instancex402.ConsumeResponseForTests {
	return instancex402.ConsumeResponseForTests{
		Accepted: true,
		Grant: instancex402.GrantViewForTests{
			GrantID:           req.GrantID,
			AgentID:           req.AgentID,
			CapabilityVersion: req.CapabilityVersion,
			Capability:        req.Capability,
			Tool:              req.Tool,
			Scope:             "write",
			Resource:          req.Resource,
			RequestHash:       req.RequestHash,
			Payment:           instancex402.PaymentBindingForTests{EvidenceHash: req.PaymentEvidenceHash},
			Authority:         "scoped_invocation",
			Status:            "issued",
			MaxUsage:          1,
			UsedCount:         1,
			ExpiresAt:         time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		Usage: instancex402.GrantUsageForTests{UsedCount: 1, MaxUsage: 1},
	}
}

func assertInstanceConsumeRequest(t testing.TB, req instancex402.ConsumeRequestForTests, capability, tool, resource, rawEvidence string) {
	t.Helper()
	if req.AgentID != testSoulAgentID || req.CapabilityVersion != instancex402.CapabilityVersionInstanceV1 || req.Capability != capability || req.Tool != tool || req.Resource != resource {
		t.Fatalf("consume request binding = %+v", req)
	}
	if req.GrantToken == "" || req.RequestHash == "" || req.IdempotencyKey == "" || req.PaymentEvidenceHash == "" {
		t.Fatalf("consume request missing required fields: %+v", req)
	}
	if !strings.HasPrefix(req.PaymentEvidenceHash, "sha256:") || req.PaymentEvidenceHash == rawEvidence || strings.Contains(req.PaymentEvidenceHash, rawEvidence) {
		t.Fatalf("paymentEvidenceHash is not a hash of the evidence: %q", req.PaymentEvidenceHash)
	}
}

func addInstanceX402Headers(headers map[string][]string, grantID, grantToken, capability, paymentEvidence string) {
	headers["lesser-x402-grant-id"] = []string{grantID}
	headers["lesser-x402-grant"] = []string{grantToken}
	headers["lesser-x402-capability"] = []string{capability}
	headers["payment-signature"] = []string{paymentEvidence}
}

func toolResultErrorReason(t testing.TB, result *mcpruntime.ToolResult) string {
	t.Helper()
	payload := toolResultError(t, result)
	details, _ := payload["details"].(map[string]any)
	reason, _ := details["reason"].(string)
	return reason
}

func assertNoRawX402Leak(t testing.TB, result *mcpruntime.ToolResult, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	text := string(encoded)
	for _, value := range forbidden {
		value = strings.TrimSpace(value)
		if value != "" && strings.Contains(text, value) {
			t.Fatalf("tool result leaked forbidden x402 material %q: %s", value, text)
		}
	}
}

func instanceStatusValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
