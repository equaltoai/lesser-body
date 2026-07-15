package ptahserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func TestRegisterToolsRegistersAgentBindSoulDefinition(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	tools := registry.List()
	if len(tools) != 1 {
		t.Fatalf("registered tools = %d, want 1", len(tools))
	}
	def := tools[0]
	if def.Name != toolAgentBindSoul {
		t.Fatalf("tool name = %q, want %q", def.Name, toolAgentBindSoul)
	}
	if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint {
		t.Fatalf("agent_bind_soul must not be read-only: %+v", def.Annotations)
	}
	if def.Annotations.DestructiveHint == nil || *def.Annotations.DestructiveHint {
		t.Fatalf("agent_bind_soul must be an additive, non-destructive mutation: %+v", def.Annotations)
	}
	if def.Annotations.IdempotentHint == nil || *def.Annotations.IdempotentHint {
		t.Fatalf("agent_bind_soul fresh calls are not globally idempotent without Lesser's idempotency key: %+v", def.Annotations)
	}

	var schema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("input schema is invalid json: %v", err)
	}
	if !contains(schema.Required, "soul_agent_id") || !contains(schema.Required, "idempotency_key") {
		t.Fatalf("input schema required = %v", schema.Required)
	}
	if _, ok := schema.Props["actor_username"]; !ok {
		t.Fatalf("input schema should advertise optional actor_username mismatch guard")
	}
}

func TestAgentBindSoulCallsLesserWithDedicatedBearerAndCanonicalHints(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithIntegrationBearer(" integration-secret ")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read", "write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{
		"soul_agent_id":"agent-0xabc",
		"idempotency_key":"bind-key-1",
		"actor_username":"drone-ada",
		"host_registration_id":"hreg_123",
		"host_conversation_id":"hconv_456",
		"principal_address":"0x2222222222222222222222222222222222222222",
		"evidence":{
			"host_request_id":"hreq_789",
			"declaration_hash":"sha256:abc",
			"issued_at":"2026-07-14T16:20:00Z"
		}
	}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	if client.integrationBearer != "integration-secret" {
		t.Fatalf("integration bearer = %q, want dedicated bearer", client.integrationBearer)
	}
	if client.integrationBearer == "user-oauth-token" {
		t.Fatalf("user OAuth bearer was forwarded to Lesser server-to-server surface")
	}
	if client.idempotencyKey != "bind-key-1" {
		t.Fatalf("idempotency key = %q", client.idempotencyKey)
	}
	if client.req.ActorUsername != "drone-ada" || client.req.SoulAgentID != "agent-0xabc" {
		t.Fatalf("required request fields = %+v", client.req)
	}
	if client.req.BodyActorID != "body://ptah/drone-ada" {
		t.Fatalf("default body_actor_id = %q", client.req.BodyActorID)
	}
	if client.req.HostRegistrationID != "hreg_123" || client.req.HostConversationID != "hconv_456" {
		t.Fatalf("host fields = %+v", client.req)
	}
	if client.req.AuthorityModel != lesserapi.SoulAuthorityModelInstanceTrust || client.req.AnchorState != lesserapi.SoulAnchorStateHostedOffchain || client.req.OperationalBinding != lesserapi.SoulOperationalBindingHostedBound {
		t.Fatalf("canonical hints = %+v", client.req)
	}
	if client.req.Evidence.Source != "ptah" || client.req.Evidence.HostRequestID != "hreq_789" || client.req.Evidence.DeclarationHash != "sha256:abc" || client.req.Evidence.IssuedAt != "2026-07-14T16:20:00Z" {
		t.Fatalf("evidence = %+v", client.req.Evidence)
	}

	data := structuredData(t, result)
	if data["status_link"] != "/api/v1/souls/bindings/agent-0xabc" {
		t.Fatalf("status_link = %+v", data["status_link"])
	}
	idem, _ := data["idempotency"].(map[string]any)
	if idem["key"] != "bind-key-1" || idem["replayed"] != false {
		t.Fatalf("idempotency = %+v", idem)
	}
	agent, _ := data["agent_summary"].(map[string]any)
	if agent["agent_id"] != "agent-0xabc" || agent["actor_username"] != "drone-ada" {
		t.Fatalf("agent summary = %+v", agent)
	}
}

func TestAgentBindSoulRequiresWriteScopeBeforeCallingLesser(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"read"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	assertToolError(t, result, "insufficient_scope", 403)
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want 0", client.calls)
	}
}

func TestAgentBindSoulRejectsMismatchedExplicitActorUsername(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1","actor_username":"other"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	assertToolError(t, result, "forbidden", 403)
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want 0", client.calls)
	}
}

func TestAgentBindSoulPreservesLesserAPIErrorStatusAndBody(t *testing.T) {
	client := &fakeSoulBindingClient{err: &lesserapi.APIError{Status: 409, Body: []byte(`{"error":"conflict","code":"SOUL_BINDING_CONFLICT"}`)}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	errorPayload := assertToolError(t, result, "conflict", 409)
	details, _ := errorPayload["details"].(map[string]any)
	if details["upstreamStatus"] != 409 || !strings.Contains(details["upstreamBody"].(string), "SOUL_BINDING_CONFLICT") {
		t.Fatalf("details did not preserve Lesser status/body: %+v", details)
	}
}

type fakeSoulBindingClient struct {
	calls             int
	integrationBearer string
	idempotencyKey    string
	req               lesserapi.SoulBindingRequest
	resp              *lesserapi.SoulBindingResponse
	err               error
}

func (f *fakeSoulBindingClient) InitiateSoulBinding(_ context.Context, integrationBearer string, idempotencyKey string, req lesserapi.SoulBindingRequest) (*lesserapi.SoulBindingResponse, error) {
	f.calls++
	f.integrationBearer = integrationBearer
	f.idempotencyKey = idempotencyKey
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func toolContext(username string, scopes []string, bearer string) context.Context {
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: username,
		Claims: &auth.Claims{
			Username: username,
			Scopes:   scopes,
		},
	}, bearer)
}

func successfulBindingResponse(replayed bool) *lesserapi.SoulBindingResponse {
	now := time.Date(2026, 7, 14, 16, 20, 2, 0, time.UTC)
	return &lesserapi.SoulBindingResponse{
		Version:      "1",
		Status:       "bound",
		BindingState: "bound",
		Agent: lesserapi.SoulBindingAgent{
			AgentID:            "agent-0xabc",
			Domain:             "example.com",
			LocalID:            "drone-ada",
			AuthorityModel:     lesserapi.SoulAuthorityModelInstanceTrust,
			AnchorState:        lesserapi.SoulAnchorStateHostedOffchain,
			OperationalBinding: lesserapi.SoulOperationalBindingHostedBound,
			LifecycleStatus:    "active",
			PublishedVersion:   3,
		},
		Binding: lesserapi.SoulAgentBinding{
			AgentUsername:    "drone-ada",
			PrincipalAddress: "0x1111111111111111111111111111111111111111",
			BoundAt:          now,
			UpdatedAt:        now,
		},
		Idempotency: &lesserapi.SoulBindingIdempotency{
			Key:         "bind-key-1",
			Replayed:    replayed,
			PayloadHash: "sha256:handler-payload",
		},
		Links: &lesserapi.SoulBindingLinks{Status: "/api/v1/souls/bindings/agent-0xabc"},
	}
}

func structuredData(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structuredContent: %+v", result)
	}
	data, ok := result.StructuredContent["data"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent.data missing: %+v", result.StructuredContent)
	}
	return data
}

func assertToolError(t *testing.T, result *mcpruntime.ToolResult, wantCode string, wantStatus int) map[string]any {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result IsError = false: %+v", result)
	}
	payload, ok := result.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent.error missing: %+v", result.StructuredContent)
	}
	if payload["code"] != wantCode || payload["status"] != wantStatus {
		t.Fatalf("error payload = %+v, want code=%s status=%d", payload, wantCode, wantStatus)
	}
	return payload
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
