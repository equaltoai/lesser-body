package ptahserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/golang-jwt/jwt/v5"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func TestRegisterToolsRegistersPtahDefinitions(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	tools := registry.List()
	if got, want := toolDefNames(tools), []string{toolAgentBindSoul, toolAgentCreate, toolAgentGet, toolAgentList, toolAgentSoulGet, toolAgentSoulUpsert, toolAgentSoulArchive, toolAgentInstructionsGet, toolAgentInstructionsUpsert, toolAgentInstructionsArchive}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered tool order = %v, want %v", got, want)
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

	createDef := tools[1]
	if createDef.Name != toolAgentCreate {
		t.Fatalf("second tool name = %q, want %q", createDef.Name, toolAgentCreate)
	}
	if createDef.Annotations == nil || createDef.Annotations.ReadOnlyHint == nil || *createDef.Annotations.ReadOnlyHint {
		t.Fatalf("agent_create must not be read-only: %+v", createDef.Annotations)
	}
	if createDef.Annotations.DestructiveHint == nil || *createDef.Annotations.DestructiveHint {
		t.Fatalf("agent_create must be additive/non-destructive in Body registry: %+v", createDef.Annotations)
	}
	if createDef.Annotations.IdempotentHint == nil || *createDef.Annotations.IdempotentHint {
		t.Fatalf("agent_create is not globally idempotent because Lesser delegation mints credentials: %+v", createDef.Annotations)
	}
	var createSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(createDef.InputSchema, &createSchema); err != nil {
		t.Fatalf("agent_create input schema invalid json: %v", err)
	}
	for _, required := range []string{"agent_username", "scopes"} {
		if !contains(createSchema.Required, required) {
			t.Fatalf("agent_create required = %v, missing %s", createSchema.Required, required)
		}
	}
	for _, prop := range []string{"actor_username", "display_name", "bio", "expires_in", "device_label", "agent_info"} {
		if _, ok := createSchema.Props[prop]; !ok {
			t.Fatalf("agent_create schema missing %s", prop)
		}
	}

	getDef := tools[2]
	if getDef.Name != toolAgentGet {
		t.Fatalf("third tool name = %q, want %q", getDef.Name, toolAgentGet)
	}
	assertReadOnlyToolDef(t, getDef)
	var getSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(getDef.InputSchema, &getSchema); err != nil {
		t.Fatalf("agent_get input schema invalid json: %v", err)
	}
	if !contains(getSchema.Required, "agent_id") {
		t.Fatalf("agent_get required = %v, missing agent_id", getSchema.Required)
	}
	if _, ok := getSchema.Props["actor_username"]; !ok {
		t.Fatalf("agent_get schema should advertise optional actor_username mismatch guard")
	}

	listDef := tools[3]
	if listDef.Name != toolAgentList {
		t.Fatalf("fourth tool name = %q, want %q", listDef.Name, toolAgentList)
	}
	assertReadOnlyToolDef(t, listDef)
	var listSchema struct {
		Props map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(listDef.InputSchema, &listSchema); err != nil {
		t.Fatalf("agent_list input schema invalid json: %v", err)
	}
	for _, prop := range []string{"limit", "cursor"} {
		if _, ok := listSchema.Props[prop]; !ok {
			t.Fatalf("agent_list schema missing %s", prop)
		}
	}

	soulGetDef := tools[4]
	if soulGetDef.Name != toolAgentSoulGet {
		t.Fatalf("fifth tool name = %q, want %q", soulGetDef.Name, toolAgentSoulGet)
	}
	assertReadOnlyToolDef(t, soulGetDef)
	assertContains(t, soulGetDef.Description, agentSoulProvisionalMarker)
	var soulGetSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(soulGetDef.InputSchema, &soulGetSchema); err != nil {
		t.Fatalf("agent_soul_get input schema invalid json: %v", err)
	}
	if !contains(soulGetSchema.Required, "agent_id") {
		t.Fatalf("agent_soul_get required = %v, missing agent_id", soulGetSchema.Required)
	}
	if _, ok := soulGetSchema.Props["actor_username"]; !ok {
		t.Fatalf("agent_soul_get schema should advertise optional actor_username mismatch guard")
	}

	soulUpsertDef := tools[5]
	if soulUpsertDef.Name != toolAgentSoulUpsert {
		t.Fatalf("sixth tool name = %q, want %q", soulUpsertDef.Name, toolAgentSoulUpsert)
	}
	assertMutationToolDef(t, soulUpsertDef, false)
	assertContains(t, soulUpsertDef.Description, agentSoulProvisionalMarker)
	var soulUpsertSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(soulUpsertDef.InputSchema, &soulUpsertSchema); err != nil {
		t.Fatalf("agent_soul_upsert input schema invalid json: %v", err)
	}
	for _, required := range []string{"agent_id", "content"} {
		if !contains(soulUpsertSchema.Required, required) {
			t.Fatalf("agent_soul_upsert required = %v, missing %s", soulUpsertSchema.Required, required)
		}
	}
	for _, prop := range []string{"actor_username", "content"} {
		if _, ok := soulUpsertSchema.Props[prop]; !ok {
			t.Fatalf("agent_soul_upsert schema missing %s", prop)
		}
	}

	soulArchiveDef := tools[6]
	if soulArchiveDef.Name != toolAgentSoulArchive {
		t.Fatalf("seventh tool name = %q, want %q", soulArchiveDef.Name, toolAgentSoulArchive)
	}
	assertMutationToolDef(t, soulArchiveDef, true)
	assertContains(t, soulArchiveDef.Description, agentSoulProvisionalMarker)
	var soulArchiveSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(soulArchiveDef.InputSchema, &soulArchiveSchema); err != nil {
		t.Fatalf("agent_soul_archive input schema invalid json: %v", err)
	}
	if !contains(soulArchiveSchema.Required, "agent_id") {
		t.Fatalf("agent_soul_archive required = %v, missing agent_id", soulArchiveSchema.Required)
	}
	if _, ok := soulArchiveSchema.Props["actor_username"]; !ok {
		t.Fatalf("agent_soul_archive schema should advertise optional actor_username mismatch guard")
	}

	instructionsGetDef := tools[7]
	if instructionsGetDef.Name != toolAgentInstructionsGet {
		t.Fatalf("eighth tool name = %q, want %q", instructionsGetDef.Name, toolAgentInstructionsGet)
	}
	assertReadOnlyToolDef(t, instructionsGetDef)
	assertNotContains(t, instructionsGetDef.Description, agentSoulProvisionalMarker)
	var instructionsGetSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(instructionsGetDef.InputSchema, &instructionsGetSchema); err != nil {
		t.Fatalf("agent_instructions_get input schema invalid json: %v", err)
	}
	if !contains(instructionsGetSchema.Required, "agent_id") {
		t.Fatalf("agent_instructions_get required = %v, missing agent_id", instructionsGetSchema.Required)
	}
	if _, ok := instructionsGetSchema.Props["actor_username"]; !ok {
		t.Fatalf("agent_instructions_get schema should advertise optional actor_username mismatch guard")
	}

	instructionsUpsertDef := tools[8]
	if instructionsUpsertDef.Name != toolAgentInstructionsUpsert {
		t.Fatalf("ninth tool name = %q, want %q", instructionsUpsertDef.Name, toolAgentInstructionsUpsert)
	}
	assertMutationToolDef(t, instructionsUpsertDef, false)
	assertNotContains(t, instructionsUpsertDef.Description, agentSoulProvisionalMarker)
	var instructionsUpsertSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(instructionsUpsertDef.InputSchema, &instructionsUpsertSchema); err != nil {
		t.Fatalf("agent_instructions_upsert input schema invalid json: %v", err)
	}
	for _, required := range []string{"agent_id", "content"} {
		if !contains(instructionsUpsertSchema.Required, required) {
			t.Fatalf("agent_instructions_upsert required = %v, missing %s", instructionsUpsertSchema.Required, required)
		}
	}
	for _, prop := range []string{"actor_username", "content"} {
		if _, ok := instructionsUpsertSchema.Props[prop]; !ok {
			t.Fatalf("agent_instructions_upsert schema missing %s", prop)
		}
	}

	instructionsArchiveDef := tools[9]
	if instructionsArchiveDef.Name != toolAgentInstructionsArchive {
		t.Fatalf("tenth tool name = %q, want %q", instructionsArchiveDef.Name, toolAgentInstructionsArchive)
	}
	assertMutationToolDef(t, instructionsArchiveDef, true)
	assertNotContains(t, instructionsArchiveDef.Description, agentSoulProvisionalMarker)
	var instructionsArchiveSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(instructionsArchiveDef.InputSchema, &instructionsArchiveSchema); err != nil {
		t.Fatalf("agent_instructions_archive input schema invalid json: %v", err)
	}
	if !contains(instructionsArchiveSchema.Required, "agent_id") {
		t.Fatalf("agent_instructions_archive required = %v, missing agent_id", instructionsArchiveSchema.Required)
	}
	if _, ok := instructionsArchiveSchema.Props["actor_username"]; !ok {
		t.Fatalf("agent_instructions_archive schema should advertise optional actor_username mismatch guard")
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

func TestAgentCreateDelegatesWithCallerBearerAndCreatesRegistry(t *testing.T) {
	client := &fakeAgentDelegateClient{resp: successfulAgentDelegationResponse()}
	store := &fakeAgentRegistry{agent: &agentregistry.Agent{
		Account:   "drone-ada",
		AgentID:   "https://lesser.example/users/ptah_agent",
		CreatedAt: time.Date(2026, 7, 15, 12, 10, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 15, 12, 10, 0, 0, time.UTC),
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentDelegateClient(client), WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read", "write"}, "owner-oauth-token"), toolAgentCreate, json.RawMessage(`{
		"agent_username":"ptah_agent",
		"actor_username":"drone-ada",
		"display_name":"Ptah Agent",
		"bio":"delegated runtime",
		"scopes":[" read ","write:statuses"],
		"expires_in":3600,
		"device_label":"ptah-instance-plane",
		"agent_info":{"version":"1"}
	}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if client.calls != 1 {
		t.Fatalf("DelegateAgent calls = %d, want 1", client.calls)
	}
	if client.bearer != "owner-oauth-token" {
		t.Fatalf("DelegateAgent bearer = %q, want caller bearer", client.bearer)
	}
	if client.bearer == "integration-secret" {
		t.Fatalf("agent_create used the dedicated soul-binding bearer")
	}
	if client.req.AgentUsername != "ptah_agent" || client.req.DisplayName != "Ptah Agent" || client.req.Bio != "delegated runtime" {
		t.Fatalf("delegate request identity fields = %+v", client.req)
	}
	if got := strings.Join(client.req.Scopes, ","); got != "read,write:statuses" {
		t.Fatalf("delegate scopes = %q", got)
	}
	if client.req.ExpiresIn != 3600 || client.req.DeviceLabel != "ptah-instance-plane" {
		t.Fatalf("delegate token options = %+v", client.req)
	}
	if info, ok := client.req.AgentInfo.(map[string]any); !ok || info["version"] != "1" {
		t.Fatalf("agent_info = %#v", client.req.AgentInfo)
	}
	if store.calls != 1 {
		t.Fatalf("registry Create calls = %d, want 1", store.calls)
	}
	if store.in.Account != "drone-ada" || store.in.AgentID != "https://lesser.example/users/ptah_agent" {
		t.Fatalf("registry input = %+v", store.in)
	}

	data := structuredData(t, result)
	token, _ := data["token"].(map[string]any)
	if token["access_token"] != "mock-access-token" || token["refresh_token"] != "mock-refresh-token" {
		t.Fatalf("structured token = %+v", token)
	}
	if result.Content == nil || strings.Contains(result.Content[0].Text, "mock-access-token") || strings.Contains(result.Content[0].Text, "mock-refresh-token") {
		t.Fatalf("text content leaked token credentials: %+v", result.Content)
	}
	registrySummary, _ := data["registry"].(map[string]any)
	if registrySummary["account"] != "drone-ada" || registrySummary["agent_id"] != "https://lesser.example/users/ptah_agent" {
		t.Fatalf("registry summary = %+v", registrySummary)
	}
}

func TestAgentCreateMapsDuplicateRegistryConflictWithoutLeakingCrossAccountState(t *testing.T) {
	client := &fakeAgentDelegateClient{resp: successfulAgentDelegationResponse()}
	store := &fakeAgentRegistry{err: fmt.Errorf("%w: hidden account/agent details", agentregistry.ErrAgentAlreadyExists)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentDelegateClient(client), WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"write"}, "owner-oauth-token"), toolAgentCreate, json.RawMessage(`{"agent_username":"ptah_agent","scopes":["read"]}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "agent_already_exists", 409)
	if client.calls != 1 || store.calls != 1 {
		t.Fatalf("calls delegate=%d registry=%d, want 1/1", client.calls, store.calls)
	}
	textBytes, _ := json.Marshal(payload)
	if strings.Contains(string(textBytes), "hidden account/agent details") || strings.Contains(string(textBytes), "mock-access-token") || strings.Contains(string(textBytes), "mock-refresh-token") {
		t.Fatalf("duplicate error leaked registry details or credentials: %s", string(textBytes))
	}
	details, _ := payload["details"].(map[string]any)
	if details["reconciliationRequired"] != true || details["mintedCredentialsMayExist"] != true {
		t.Fatalf("duplicate details should document partial mint reconciliation: %+v", details)
	}
}

func TestAgentCreateRejectsBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		args      string
		wantCode  string
		wantState int
	}{
		{
			name:      "insufficient scope",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{"agent_username":"ptah_agent","scopes":["read"]}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "agent principal",
			ctx:       toolContextWithAgent("drone-ada", []string{"write"}, "agent-runtime-token", true),
			args:      `{"agent_username":"ptah_agent","scopes":["read"]}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "missing bearer",
			ctx:       toolContext("drone-ada", []string{"write"}, ""),
			args:      `{"agent_username":"ptah_agent","scopes":["read"]}`,
			wantCode:  "unauthorized",
			wantState: 401,
		},
		{
			name:      "missing username",
			ctx:       toolContext("drone-ada", []string{"write"}, "owner-oauth-token"),
			args:      `{"scopes":["read"]}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "missing scopes",
			ctx:       toolContext("drone-ada", []string{"write"}, "owner-oauth-token"),
			args:      `{"agent_username":"ptah_agent","scopes":[]}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "mismatched actor username",
			ctx:       toolContext("drone-ada", []string{"write"}, "owner-oauth-token"),
			args:      `{"agent_username":"ptah_agent","scopes":["read"],"actor_username":"other"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeAgentDelegateClient{resp: successfulAgentDelegationResponse()}
			store := &fakeAgentRegistry{agent: &agentregistry.Agent{Account: "drone-ada", AgentID: "https://lesser.example/users/ptah_agent"}}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentDelegateClient(client), WithAgentRegistryStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, toolAgentCreate, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantState)
			if client.calls != 0 || store.calls != 0 {
				t.Fatalf("side effects occurred before rejection: delegate=%d registry=%d", client.calls, store.calls)
			}
		})
	}
}

func TestAgentCreatePreservesLesserAPIErrorStatusAndDetails(t *testing.T) {
	client := &fakeAgentDelegateClient{err: &lesserapi.APIError{Status: 503, Body: []byte(`{"error":"agent delegation failed","error_code":"AGENT_DELEGATION_TEST","access_token":"should-redact","refresh_token":"also-redact"}`)}}
	store := &fakeAgentRegistry{agent: &agentregistry.Agent{Account: "drone-ada", AgentID: "https://lesser.example/users/ptah_agent"}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentDelegateClient(client), WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"write"}, "owner-oauth-token"), toolAgentCreate, json.RawMessage(`{"agent_username":"ptah_agent","scopes":["read"]}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "upstream_unavailable", 503)
	if client.calls != 1 || store.calls != 0 {
		t.Fatalf("calls delegate=%d registry=%d, want 1/0", client.calls, store.calls)
	}
	details, _ := payload["details"].(map[string]any)
	if details["source"] != "lesser_agent_delegate" || details["upstreamStatus"] != 503 {
		t.Fatalf("details did not preserve status/source: %+v", details)
	}
	body := details["upstreamBody"].(string)
	if !strings.Contains(body, "AGENT_DELEGATION_TEST") || strings.Contains(body, "should-redact") || strings.Contains(body, "also-redact") {
		t.Fatalf("upstream body did not preserve code with redaction: %s", body)
	}
}

func TestAgentCreateDocumentsPartialFailureWhenRegistryCreateFails(t *testing.T) {
	client := &fakeAgentDelegateClient{resp: successfulAgentDelegationResponse()}
	store := &fakeAgentRegistry{err: errors.New("registry table unavailable")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentDelegateClient(client), WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"write"}, "owner-oauth-token"), toolAgentCreate, json.RawMessage(`{"agent_username":"ptah_agent","scopes":["read"]}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "agent_registry_error", 500)
	if client.calls != 1 || store.calls != 1 {
		t.Fatalf("calls delegate=%d registry=%d, want 1/1", client.calls, store.calls)
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "mock-access-token") || strings.Contains(string(encoded), "mock-refresh-token") {
		t.Fatalf("partial failure leaked minted credentials: %s", string(encoded))
	}
	details, _ := payload["details"].(map[string]any)
	if details["partialFailure"] != true || details["lesserDelegationSucceeded"] != true || details["reconciliationRequired"] != true {
		t.Fatalf("partial failure details = %+v", details)
	}
}

func TestAgentGetReadsAccountScopedRegistryWithReadCapableScopes(t *testing.T) {
	for _, scope := range []string{"read", "write", "admin"} {
		t.Run(scope, func(t *testing.T) {
			store := &fakeAgentRegistry{getAgent: &agentregistry.Agent{
				Account:   "drone-ada",
				AgentID:   "agent-123",
				CreatedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC),
			}}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(toolContext("Drone-Ada", []string{scope}, "owner-oauth-token"), toolAgentGet, json.RawMessage(`{"agent_id":" agent-123 ","actor_username":"drone-ada"}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("result = %+v", result)
			}
			if store.getCalls != 1 || store.getAccount != "drone-ada" || store.getAgentID != "agent-123" {
				t.Fatalf("registry Get calls/account/id = %d/%q/%q, want 1/drone-ada/agent-123", store.getCalls, store.getAccount, store.getAgentID)
			}
			data := structuredData(t, result)
			registrySummary, _ := data["registry"].(map[string]any)
			if registrySummary["account"] != "drone-ada" || registrySummary["agent_id"] != "agent-123" {
				t.Fatalf("registry summary = %+v", registrySummary)
			}
			contentVersion, _ := data["content_version"].(map[string]any)
			contentSummary, _ := data["content_summary"].(map[string]any)
			if contentVersion["status"] != "not_available" || contentSummary["status"] != "not_available" {
				t.Fatalf("content placeholders = version %+v summary %+v", contentVersion, contentSummary)
			}
		})
	}
}

func TestAgentGetNotFoundDoesNotLeakRegistryDetails(t *testing.T) {
	store := &fakeAgentRegistry{getErr: fmt.Errorf("%w: hidden account/agent details", agentregistry.ErrAgentNotFound)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"read"}, "owner-oauth-token"), toolAgentGet, json.RawMessage(`{"agent_id":"agent-elsewhere"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "not_found", 404)
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "hidden account/agent details") || strings.Contains(string(encoded), "agent-elsewhere") || strings.Contains(string(encoded), "drone-ada") {
		t.Fatalf("not_found leaked account/agent details: %s", string(encoded))
	}
	if store.getCalls != 1 {
		t.Fatalf("registry Get calls = %d, want 1", store.getCalls)
	}
}

func TestAgentGetRejectsInvalidInputAndPrincipalsBeforeRegistryRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		args      string
		wantCode  string
		wantState int
	}{
		{
			name:      "missing agent_id",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "unknown field",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{"agent_id":"agent-123","account":"other"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "mismatched actor username",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{"agent_id":"agent-123","actor_username":"other"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "insufficient scope",
			ctx:       toolContext("drone-ada", []string{"follow"}, "owner-oauth-token"),
			args:      `{"agent_id":"agent-123"}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "agent principal",
			ctx:       toolContextWithAgent("drone-ada", []string{"read"}, "agent-runtime-token", true),
			args:      `{"agent_id":"agent-123"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAgentRegistry{getAgent: &agentregistry.Agent{Account: "drone-ada", AgentID: "agent-123"}}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, toolAgentGet, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantState)
			if store.getCalls != 0 {
				t.Fatalf("registry Get calls = %d, want 0", store.getCalls)
			}
		})
	}
}

func TestAgentListReadsAccountScopedRegistryWithPagination(t *testing.T) {
	store := &fakeAgentRegistry{listResult: &agentregistry.ListResult{
		Agents: []*agentregistry.Agent{
			{
				Account:   "drone-ada",
				AgentID:   "agent-001",
				CreatedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC),
			},
			{
				Account:   "drone-ada",
				AgentID:   "agent-002",
				CreatedAt: time.Date(2026, 7, 15, 12, 2, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 7, 15, 12, 3, 0, 0, time.UTC),
			},
		},
		NextCursor: "cursor-2",
		HasMore:    true,
		Count:      2,
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{"limit":2,"cursor":"cursor-1"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.listCalls != 1 || store.listIn.Account != "drone-ada" || store.listIn.Limit != 2 || store.listIn.Cursor != "cursor-1" {
		t.Fatalf("registry List input = calls %d %+v, want account-scoped limit/cursor", store.listCalls, store.listIn)
	}
	data := structuredData(t, result)
	agents, _ := data["agents"].([]map[string]any)
	if len(agents) != 2 {
		t.Fatalf("agents = %+v, want two", data["agents"])
	}
	gotIDs := []string{}
	for _, item := range agents {
		registrySummary, _ := item["registry"].(map[string]any)
		gotIDs = append(gotIDs, registrySummary["agent_id"].(string))
		contentVersion, _ := item["content_version"].(map[string]any)
		if contentVersion["status"] != "not_available" {
			t.Fatalf("content_version = %+v, want not_available", contentVersion)
		}
	}
	if want := []string{"agent-001", "agent-002"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("agent ids = %v, want %v", gotIDs, want)
	}
	pagination, _ := data["pagination"].(map[string]any)
	if pagination["next_cursor"] != "cursor-2" || pagination["has_more"] != true || pagination["count"] != 2 || pagination["limit"] != 2 {
		t.Fatalf("pagination = %+v", pagination)
	}
}

func TestAgentListRejectsInvalidInputAndPrincipalsBeforeRegistryRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		args      string
		wantCode  string
		wantState int
	}{
		{
			name:      "zero limit",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{"limit":0}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "too large limit",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      fmt.Sprintf(`{"limit":%d}`, agentregistry.MaxListLimit+1),
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "caller-supplied account rejected",
			ctx:       toolContext("drone-ada", []string{"read"}, "owner-oauth-token"),
			args:      `{"account":"other"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "insufficient scope",
			ctx:       toolContext("drone-ada", []string{"follow"}, "owner-oauth-token"),
			args:      `{}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "agent principal",
			ctx:       toolContextWithAgent("drone-ada", []string{"read"}, "agent-runtime-token", true),
			args:      `{}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAgentRegistry{listResult: &agentregistry.ListResult{}}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, toolAgentList, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantState)
			if store.listCalls != 0 {
				t.Fatalf("registry List calls = %d, want 0", store.listCalls)
			}
		})
	}
}

func TestAgentListMapsInvalidCursor(t *testing.T) {
	store := &fakeAgentRegistry{listErr: fmt.Errorf("%w: hidden cursor decode detail", agentregistry.ErrInvalidCursor)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{"cursor":"bad-cursor"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "invalid_request", 400)
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "hidden cursor decode detail") {
		t.Fatalf("invalid cursor leaked store detail: %s", string(encoded))
	}
	if store.listCalls != 1 || store.listIn.Account != "drone-ada" || store.listIn.Limit != agentregistry.DefaultListLimit || store.listIn.Cursor != "bad-cursor" {
		t.Fatalf("registry List input = calls %d %+v, want default-limit account scoped cursor", store.listCalls, store.listIn)
	}
}

func TestAgentSoulGetReadsAccountScopedContentWithReadCapableScopes(t *testing.T) {
	for _, scope := range []string{"read", "write", "admin"} {
		t.Run(scope, func(t *testing.T) {
			store := &fakeAgentContentStore{getRecord: agentSoulRecord("drone-ada", "agent-123", "draft soul", 3, agentcontent.LifecycleStateDraft, "subject-prev")}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{scope}, "owner-oauth-token", "subject-reader"), toolAgentSoulGet, json.RawMessage(`{"agent_id":" agent-123 ","actor_username":"drone-ada"}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("result = %+v", result)
			}
			if store.getCalls != 1 || store.getAccount != "drone-ada" || store.getAgentID != "agent-123" || store.getType != agentcontent.ContentTypeAgentSoul {
				t.Fatalf("content Get calls/account/id/type = %d/%q/%q/%q, want account-scoped agent_soul", store.getCalls, store.getAccount, store.getAgentID, store.getType)
			}
			data := structuredData(t, result)
			record, _ := data["agent_soul"].(map[string]any)
			if record["account"] != "drone-ada" || record["agent_id"] != "agent-123" || record["content"] != "draft soul" || record["version"] != int64(3) {
				t.Fatalf("agent_soul record = %+v", record)
			}
			schema, _ := data["schema"].(map[string]any)
			if schema["marker"] != agentSoulProvisionalMarker || schema["status"] != "provisional" {
				t.Fatalf("schema marker = %+v", schema)
			}
		})
	}
}

func TestAgentSoulUpsertUsesAgentContentStoreWithSubject(t *testing.T) {
	store := &fakeAgentContentStore{upsertRecord: agentSoulRecord("drone-ada", "agent-123", "draft soul v1", 1, agentcontent.LifecycleStateDraft, "subject-writer")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{"write"}, "owner-oauth-token", "subject-writer"), toolAgentSoulUpsert, json.RawMessage(`{
		"agent_id":" agent-123 ",
		"actor_username":"drone-ada",
		"content":"draft soul v1"
	}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("Upsert calls = %d, want 1", store.upsertCalls)
	}
	if store.upsertIn.Account != "drone-ada" || store.upsertIn.AgentID != "agent-123" || store.upsertIn.Type != agentcontent.ContentTypeAgentSoul || store.upsertIn.Content != "draft soul v1" || store.upsertIn.UpdatedBySubjectID != "subject-writer" {
		t.Fatalf("Upsert input = %+v, want account-scoped agent_soul with subject", store.upsertIn)
	}
	data := structuredData(t, result)
	record, _ := data["agent_soul"].(map[string]any)
	if record["updated_by_subject_id"] != "subject-writer" || record["content"] != "draft soul v1" {
		t.Fatalf("agent_soul record = %+v", record)
	}
	if strings.Contains(result.Content[0].Text, "draft soul v1") {
		t.Fatalf("text content duplicated provisional soul content: %s", result.Content[0].Text)
	}
}

func TestAgentSoulArchiveUsesStoreAndReportsIdempotentReplay(t *testing.T) {
	store := &fakeAgentContentStore{
		getRecord:     agentSoulRecord("drone-ada", "agent-123", "draft soul", 4, agentcontent.LifecycleStateArchived, "subject-prev"),
		archiveRecord: agentSoulRecord("drone-ada", "agent-123", "draft soul", 4, agentcontent.LifecycleStateArchived, "subject-archive"),
	}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{"write"}, "owner-oauth-token", "subject-archive"), toolAgentSoulArchive, json.RawMessage(`{"agent_id":"agent-123","actor_username":"drone-ada"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.getCalls != 1 || store.archiveCalls != 1 {
		t.Fatalf("Get/Archive calls = %d/%d, want 1/1", store.getCalls, store.archiveCalls)
	}
	if store.archiveIn.Account != "drone-ada" || store.archiveIn.AgentID != "agent-123" || store.archiveIn.Type != agentcontent.ContentTypeAgentSoul || store.archiveIn.UpdatedBySubjectID != "subject-archive" {
		t.Fatalf("Archive input = %+v, want account-scoped agent_soul with subject", store.archiveIn)
	}
	data := structuredData(t, result)
	if data["already_archived"] != true || data["idempotent"] != true {
		t.Fatalf("archive idempotency metadata = %+v", data)
	}
	record, _ := data["agent_soul"].(map[string]any)
	if record["lifecycle_state"] != string(agentcontent.LifecycleStateArchived) || record["version"] != int64(4) {
		t.Fatalf("archived record = %+v", record)
	}
}

func TestAgentSoulRejectsInvalidInputAndPrincipalsBeforeContentStoreWrite(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tool      string
		ctx       context.Context
		args      string
		wantCode  string
		wantState int
	}{
		{
			name:      "upsert insufficient scope",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","content":"draft"}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "upsert agent principal",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithAgent("drone-ada", []string{"write"}, "agent-runtime-token", true),
			args:      `{"agent_id":"agent-123","content":"draft"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "upsert missing subject",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", ""),
			args:      `{"agent_id":"agent-123","content":"draft"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "upsert missing content",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "get caller-supplied account rejected",
			tool:      toolAgentSoulGet,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"),
			args:      `{"agent_id":"agent-123","account":"other"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "archive mismatched actor username",
			tool:      toolAgentSoulArchive,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","actor_username":"other"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAgentContentStore{getRecord: agentSoulRecord("drone-ada", "agent-123", "draft", 1, agentcontent.LifecycleStateDraft, "subject")}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantState)
			if store.getCalls != 0 || store.upsertCalls != 0 || store.archiveCalls != 0 {
				t.Fatalf("content store side effects occurred before rejection: get=%d upsert=%d archive=%d", store.getCalls, store.upsertCalls, store.archiveCalls)
			}
		})
	}
}

func TestAgentSoulMapsContentStoreErrorsWithoutLeakingScopeDetails(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tool        string
		store       *fakeAgentContentStore
		ctx         context.Context
		args        string
		wantCode    string
		wantStatus  int
		forbidTerms []string
	}{
		{
			name:       "get not found",
			tool:       toolAgentSoulGet,
			store:      &fakeAgentContentStore{getErr: fmt.Errorf("%w: hidden account agent-elsewhere", agentcontent.ErrContentNotFound)},
			ctx:        toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"),
			args:       `{"agent_id":"agent-elsewhere"}`,
			wantCode:   "not_found",
			wantStatus: 404,
			forbidTerms: []string{
				"hidden account",
				"agent-elsewhere",
				"drone-ada",
			},
		},
		{
			name:       "upsert too large",
			tool:       toolAgentSoulUpsert,
			store:      &fakeAgentContentStore{upsertErr: &agentcontent.SizeError{Type: agentcontent.ContentTypeAgentSoul, Limit: agentcontent.MaxAgentSoulBytes, Actual: agentcontent.MaxAgentSoulBytes + 1}},
			ctx:        toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:       `{"agent_id":"agent-123","content":"too large"}`,
			wantCode:   "invalid_request",
			wantStatus: 400,
		},
		{
			name:       "upsert conflict",
			tool:       toolAgentSoulUpsert,
			store:      &fakeAgentContentStore{upsertErr: fmt.Errorf("%w: conditional retry exhausted", agentcontent.ErrContentConflict)},
			ctx:        toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:       `{"agent_id":"agent-123","content":"draft"}`,
			wantCode:   "conflict",
			wantStatus: 409,
		},
		{
			name:       "archive invalid lifecycle",
			tool:       toolAgentSoulArchive,
			store:      &fakeAgentContentStore{getRecord: agentSoulRecord("drone-ada", "agent-123", "draft", 1, agentcontent.LifecycleStateDraft, "subject-prev"), archiveErr: fmt.Errorf("%w: corrupted", agentcontent.ErrInvalidLifecycleState)},
			ctx:        toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:       `{"agent_id":"agent-123"}`,
			wantCode:   "internal",
			wantStatus: 500,
			forbidTerms: []string{
				"corrupted",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentContentStore(tc.store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			result, err := registry.Call(tc.ctx, tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			payload := assertToolError(t, result, tc.wantCode, tc.wantStatus)
			encoded, _ := json.Marshal(payload)
			for _, term := range tc.forbidTerms {
				if strings.Contains(string(encoded), term) {
					t.Fatalf("error payload leaked %q: %s", term, string(encoded))
				}
			}
		})
	}
}

func TestAgentInstructionsGetReadsAccountScopedContentWithReadCapableScopes(t *testing.T) {
	for _, scope := range []string{"read", "write", "admin"} {
		t.Run(scope, func(t *testing.T) {
			store := &fakeAgentContentStore{getRecord: agentInstructionsRecord("drone-ada", "agent-123", "follow the operator", 2, agentcontent.LifecycleStateDraft, "subject-prev")}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{scope}, "owner-oauth-token", "subject-reader"), toolAgentInstructionsGet, json.RawMessage(`{"agent_id":" agent-123 ","actor_username":"drone-ada"}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("result = %+v", result)
			}
			if store.getCalls != 1 || store.getAccount != "drone-ada" || store.getAgentID != "agent-123" || store.getType != agentcontent.ContentTypeAgentInstructions {
				t.Fatalf("content Get calls/account/id/type = %d/%q/%q/%q, want account-scoped agent_instructions", store.getCalls, store.getAccount, store.getAgentID, store.getType)
			}
			data := structuredData(t, result)
			record, _ := data["agent_instructions"].(map[string]any)
			if record["account"] != "drone-ada" || record["agent_id"] != "agent-123" || record["type"] != string(agentcontent.ContentTypeAgentInstructions) || record["content"] != "follow the operator" || record["version"] != int64(2) {
				t.Fatalf("agent_instructions record = %+v", record)
			}
			if _, ok := data["schema"]; ok {
				t.Fatalf("agent_instructions should not carry agent_soul provisional schema marker: %+v", data["schema"])
			}
		})
	}
}

func TestAgentInstructionsUpsertUsesAgentContentStoreWithSubject(t *testing.T) {
	store := &fakeAgentContentStore{upsertRecord: agentInstructionsRecord("drone-ada", "agent-123", "instructions v1", 1, agentcontent.LifecycleStateDraft, "subject-writer")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{"write"}, "owner-oauth-token", "subject-writer"), toolAgentInstructionsUpsert, json.RawMessage(`{
		"agent_id":" agent-123 ",
		"actor_username":"drone-ada",
		"content":"instructions v1"
	}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("Upsert calls = %d, want 1", store.upsertCalls)
	}
	if store.upsertIn.Account != "drone-ada" || store.upsertIn.AgentID != "agent-123" || store.upsertIn.Type != agentcontent.ContentTypeAgentInstructions || store.upsertIn.Content != "instructions v1" || store.upsertIn.UpdatedBySubjectID != "subject-writer" {
		t.Fatalf("Upsert input = %+v, want account-scoped agent_instructions with subject", store.upsertIn)
	}
	data := structuredData(t, result)
	record, _ := data["agent_instructions"].(map[string]any)
	if record["updated_by_subject_id"] != "subject-writer" || record["content"] != "instructions v1" {
		t.Fatalf("agent_instructions record = %+v", record)
	}
	if strings.Contains(result.Content[0].Text, "instructions v1") {
		t.Fatalf("text content duplicated instructions content: %s", result.Content[0].Text)
	}
}

func TestAgentInstructionsArchiveUsesStoreAndReportsIdempotentReplay(t *testing.T) {
	store := &fakeAgentContentStore{
		getRecord:     agentInstructionsRecord("drone-ada", "agent-123", "instructions", 4, agentcontent.LifecycleStateArchived, "subject-prev"),
		archiveRecord: agentInstructionsRecord("drone-ada", "agent-123", "instructions", 4, agentcontent.LifecycleStateArchived, "subject-archive"),
	}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{"write"}, "owner-oauth-token", "subject-archive"), toolAgentInstructionsArchive, json.RawMessage(`{"agent_id":"agent-123","actor_username":"drone-ada"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.getCalls != 1 || store.archiveCalls != 1 {
		t.Fatalf("Get/Archive calls = %d/%d, want 1/1", store.getCalls, store.archiveCalls)
	}
	if store.archiveIn.Account != "drone-ada" || store.archiveIn.AgentID != "agent-123" || store.archiveIn.Type != agentcontent.ContentTypeAgentInstructions || store.archiveIn.UpdatedBySubjectID != "subject-archive" {
		t.Fatalf("Archive input = %+v, want account-scoped agent_instructions with subject", store.archiveIn)
	}
	data := structuredData(t, result)
	if data["already_archived"] != true || data["idempotent"] != true {
		t.Fatalf("archive idempotency metadata = %+v", data)
	}
	record, _ := data["agent_instructions"].(map[string]any)
	if record["lifecycle_state"] != string(agentcontent.LifecycleStateArchived) || record["version"] != int64(4) {
		t.Fatalf("archived record = %+v", record)
	}
}

func TestAgentInstructionsRejectsInvalidInputAndPrincipalsBeforeContentStoreWrite(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tool      string
		ctx       context.Context
		args      string
		wantCode  string
		wantState int
	}{
		{
			name:      "get insufficient scope",
			tool:      toolAgentInstructionsGet,
			ctx:       toolContextWithSubject("drone-ada", []string{"follow"}, "owner-oauth-token", "subject-reader"),
			args:      `{"agent_id":"agent-123"}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "upsert insufficient scope",
			tool:      toolAgentInstructionsUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","content":"instructions"}`,
			wantCode:  "insufficient_scope",
			wantState: 403,
		},
		{
			name:      "upsert agent principal",
			tool:      toolAgentInstructionsUpsert,
			ctx:       toolContextWithAgent("drone-ada", []string{"write"}, "agent-runtime-token", true),
			args:      `{"agent_id":"agent-123","content":"instructions"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "upsert missing subject",
			tool:      toolAgentInstructionsUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", ""),
			args:      `{"agent_id":"agent-123","content":"instructions"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
		{
			name:      "get caller-supplied account rejected",
			tool:      toolAgentInstructionsGet,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"),
			args:      `{"agent_id":"agent-123","account":"other"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "archive mismatched actor username",
			tool:      toolAgentInstructionsArchive,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","actor_username":"other"}`,
			wantCode:  "forbidden",
			wantState: 403,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAgentContentStore{getRecord: agentInstructionsRecord("drone-ada", "agent-123", "instructions", 1, agentcontent.LifecycleStateDraft, "subject")}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantState)
			if store.getCalls != 0 || store.upsertCalls != 0 || store.archiveCalls != 0 {
				t.Fatalf("content store side effects occurred before rejection: get=%d upsert=%d archive=%d", store.getCalls, store.upsertCalls, store.archiveCalls)
			}
		})
	}
}

func TestAgentInstructionsMapsContentStoreErrorsWithoutLeakingScopeDetails(t *testing.T) {
	store := &fakeAgentContentStore{getErr: fmt.Errorf("%w: hidden account agent-elsewhere", agentcontent.ErrContentNotFound)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"), toolAgentInstructionsGet, json.RawMessage(`{"agent_id":"agent-elsewhere"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "not_found", 404)
	details, _ := payload["details"].(map[string]any)
	if details["content_type"] != string(agentcontent.ContentTypeAgentInstructions) {
		t.Fatalf("content error details = %+v, want agent_instructions content_type", details)
	}
	encoded, _ := json.Marshal(payload)
	for _, term := range []string{"hidden account", "agent-elsewhere", "drone-ada"} {
		if strings.Contains(string(encoded), term) {
			t.Fatalf("error payload leaked %q: %s", term, string(encoded))
		}
	}
}

func TestAgentInstructionsAndSoulUseIndependentContentTypeCounters(t *testing.T) {
	store := newVersionedFakeAgentContentStore()
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := toolContextWithSubject("Drone-Ada", []string{"read", "write"}, "owner-oauth-token", "subject-writer")

	soulV1 := callToolData(t, registry, ctx, toolAgentSoulUpsert, `{"agent_id":"agent-123","content":"soul v1"}`)
	instructionsV1 := callToolData(t, registry, ctx, toolAgentInstructionsUpsert, `{"agent_id":"agent-123","content":"instructions v1"}`)
	soulV2 := callToolData(t, registry, ctx, toolAgentSoulUpsert, `{"agent_id":"agent-123","content":"soul v2"}`)
	instructionsGot := callToolData(t, registry, ctx, toolAgentInstructionsGet, `{"agent_id":"agent-123"}`)

	if got := soulV1["agent_soul"].(map[string]any)["version"]; got != int64(1) {
		t.Fatalf("soul v1 version = %v, want 1", got)
	}
	if got := instructionsV1["agent_instructions"].(map[string]any)["version"]; got != int64(1) {
		t.Fatalf("instructions v1 version = %v, want 1", got)
	}
	if got := soulV2["agent_soul"].(map[string]any)["version"]; got != int64(2) {
		t.Fatalf("soul v2 version = %v, want 2", got)
	}
	instructionsRecord := instructionsGot["agent_instructions"].(map[string]any)
	if instructionsRecord["version"] != int64(1) || instructionsRecord["content"] != "instructions v1" {
		t.Fatalf("instructions after soul update = %+v, want unchanged independent version 1", instructionsRecord)
	}
	if len(store.records) != 2 {
		t.Fatalf("fake store records = %d, want separate agent_soul and agent_instructions records", len(store.records))
	}
	for _, typ := range []agentcontent.ContentType{agentcontent.ContentTypeAgentSoul, agentcontent.ContentTypeAgentInstructions} {
		if _, ok := store.records[agentContentTestKey{account: "drone-ada", agentID: "agent-123", typ: typ}]; !ok {
			t.Fatalf("missing fake store record for content type %s; records = %+v", typ, store.records)
		}
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

type fakeAgentDelegateClient struct {
	calls  int
	bearer string
	req    lesserapi.AgentDelegationRequest
	resp   *lesserapi.AgentDelegationResponse
	err    error
}

func (f *fakeAgentDelegateClient) DelegateAgent(_ context.Context, bearerToken string, req lesserapi.AgentDelegationRequest) (*lesserapi.AgentDelegationResponse, error) {
	f.calls++
	f.bearer = bearerToken
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeAgentRegistry struct {
	calls int
	in    agentregistry.CreateInput
	agent *agentregistry.Agent
	err   error

	getCalls   int
	getAccount string
	getAgentID string
	getAgent   *agentregistry.Agent
	getErr     error

	listCalls  int
	listIn     agentregistry.ListInput
	listResult *agentregistry.ListResult
	listErr    error
}

type fakeAgentContentStore struct {
	getCalls   int
	getAccount string
	getAgentID string
	getType    agentcontent.ContentType
	getRecord  *agentcontent.Record
	getErr     error

	upsertCalls  int
	upsertIn     agentcontent.UpsertInput
	upsertRecord *agentcontent.Record
	upsertErr    error

	archiveCalls  int
	archiveIn     agentcontent.ArchiveInput
	archiveRecord *agentcontent.Record
	archiveErr    error
}

func (f *fakeAgentRegistry) Create(_ context.Context, in agentregistry.CreateInput) (*agentregistry.Agent, error) {
	f.calls++
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return f.agent, nil
}

func (f *fakeAgentRegistry) Get(_ context.Context, account string, agentID string) (*agentregistry.Agent, error) {
	f.getCalls++
	f.getAccount = account
	f.getAgentID = agentID
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getAgent != nil {
		return f.getAgent, nil
	}
	return f.agent, nil
}

func (f *fakeAgentRegistry) List(_ context.Context, in agentregistry.ListInput) (*agentregistry.ListResult, error) {
	f.listCalls++
	f.listIn = in
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listResult != nil {
		return f.listResult, nil
	}
	return &agentregistry.ListResult{}, nil
}

func (f *fakeAgentContentStore) Get(_ context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error) {
	f.getCalls++
	f.getAccount = account
	f.getAgentID = agentID
	f.getType = contentType
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getRecord, nil
}

func (f *fakeAgentContentStore) Upsert(_ context.Context, in agentcontent.UpsertInput) (*agentcontent.Record, error) {
	f.upsertCalls++
	f.upsertIn = in
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if f.upsertRecord != nil {
		return f.upsertRecord, nil
	}
	return agentContentRecord(in.Type, in.Account, in.AgentID, in.Content, 1, agentcontent.LifecycleStateDraft, in.UpdatedBySubjectID), nil
}

func (f *fakeAgentContentStore) Archive(_ context.Context, in agentcontent.ArchiveInput) (*agentcontent.Record, error) {
	f.archiveCalls++
	f.archiveIn = in
	if f.archiveErr != nil {
		return nil, f.archiveErr
	}
	if f.archiveRecord != nil {
		return f.archiveRecord, nil
	}
	return agentContentRecord(in.Type, in.Account, in.AgentID, "", 1, agentcontent.LifecycleStateArchived, in.UpdatedBySubjectID), nil
}

type agentContentTestKey struct {
	account string
	agentID string
	typ     agentcontent.ContentType
}

type versionedFakeAgentContentStore struct {
	records map[agentContentTestKey]*agentcontent.Record
}

func newVersionedFakeAgentContentStore() *versionedFakeAgentContentStore {
	return &versionedFakeAgentContentStore{records: map[agentContentTestKey]*agentcontent.Record{}}
}

func (f *versionedFakeAgentContentStore) Get(_ context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error) {
	key := agentContentTestKey{account: account, agentID: agentID, typ: contentType}
	record := f.records[key]
	if record == nil {
		return nil, agentcontent.ErrContentNotFound
	}
	return cloneAgentContentRecord(record), nil
}

func (f *versionedFakeAgentContentStore) Upsert(_ context.Context, in agentcontent.UpsertInput) (*agentcontent.Record, error) {
	key := agentContentTestKey{account: in.Account, agentID: in.AgentID, typ: in.Type}
	version := int64(1)
	createdAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if existing := f.records[key]; existing != nil {
		version = existing.Version + 1
		createdAt = existing.CreatedAt
	}
	record := agentContentRecord(in.Type, in.Account, in.AgentID, in.Content, version, agentcontent.LifecycleStateDraft, in.UpdatedBySubjectID)
	record.CreatedAt = createdAt
	record.UpdatedAt = createdAt.Add(time.Duration(version) * time.Minute)
	f.records[key] = cloneAgentContentRecord(record)
	return record, nil
}

func (f *versionedFakeAgentContentStore) Archive(_ context.Context, in agentcontent.ArchiveInput) (*agentcontent.Record, error) {
	key := agentContentTestKey{account: in.Account, agentID: in.AgentID, typ: in.Type}
	record := f.records[key]
	if record == nil {
		return nil, agentcontent.ErrContentNotFound
	}
	archived := cloneAgentContentRecord(record)
	archived.LifecycleState = agentcontent.LifecycleStateArchived
	archived.UpdatedBySubjectID = in.UpdatedBySubjectID
	archived.UpdatedAt = archived.UpdatedAt.Add(time.Minute)
	f.records[key] = cloneAgentContentRecord(archived)
	return archived, nil
}

func toolContext(username string, scopes []string, bearer string) context.Context {
	return toolContextWithSubject(username, scopes, bearer, username)
}

func toolContextWithAgent(username string, scopes []string, bearer string, isAgent bool) context.Context {
	subject := username
	if isAgent {
		subject = "agent-subject-" + strings.ToLower(strings.TrimSpace(username))
	}
	return toolContextWithSubjectAndAgent(username, scopes, bearer, subject, isAgent)
}

func toolContextWithSubject(username string, scopes []string, bearer string, subject string) context.Context {
	return toolContextWithSubjectAndAgent(username, scopes, bearer, subject, false)
}

func toolContextWithSubjectAndAgent(username string, scopes []string, bearer string, subject string, isAgent bool) context.Context {
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: username,
		Claims: &auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
			Username:         username,
			Scopes:           scopes,
			IsAgent:          isAgent,
		},
	}, bearer)
}

func agentSoulRecord(account, agentID, content string, version int64, state agentcontent.LifecycleState, updatedBySubjectID string) *agentcontent.Record {
	return agentContentRecord(agentcontent.ContentTypeAgentSoul, account, agentID, content, version, state, updatedBySubjectID)
}

func agentInstructionsRecord(account, agentID, content string, version int64, state agentcontent.LifecycleState, updatedBySubjectID string) *agentcontent.Record {
	return agentContentRecord(agentcontent.ContentTypeAgentInstructions, account, agentID, content, version, state, updatedBySubjectID)
}

func agentContentRecord(contentType agentcontent.ContentType, account, agentID, content string, version int64, state agentcontent.LifecycleState, updatedBySubjectID string) *agentcontent.Record {
	return &agentcontent.Record{
		Account:            account,
		AgentID:            agentID,
		Type:               contentType,
		Content:            content,
		Version:            version,
		LifecycleState:     state,
		CreatedAt:          time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC),
		UpdatedBySubjectID: updatedBySubjectID,
	}
}

func cloneAgentContentRecord(record *agentcontent.Record) *agentcontent.Record {
	if record == nil {
		return nil
	}
	clone := *record
	return &clone
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

func successfulAgentDelegationResponse() *lesserapi.AgentDelegationResponse {
	return &lesserapi.AgentDelegationResponse{
		Account: lesserapi.AgentDelegationAccount{
			ID:          "https://lesser.example/users/ptah_agent",
			Username:    "ptah_agent",
			Acct:        "ptah_agent",
			DisplayName: "Ptah Agent",
			Bot:         true,
			URL:         "https://lesser.example/@ptah_agent",
		},
		Token: lesserapi.AgentDelegationToken{
			AccessToken:  "mock-access-token",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			RefreshToken: "mock-refresh-token",
			Scope:        "read write:statuses",
			CreatedAt:    1794744000,
		},
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

func callToolData(t *testing.T, registry *mcpruntime.ToolRegistry, ctx context.Context, toolName string, args string) map[string]any {
	t.Helper()
	result, err := registry.Call(ctx, toolName, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s Call: %v", toolName, err)
	}
	if result == nil || result.IsError {
		t.Fatalf("%s result = %+v", toolName, result)
	}
	return structuredData(t, result)
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

func toolDefNames(defs []mcpruntime.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertReadOnlyToolDef(t testing.TB, def mcpruntime.ToolDef) {
	t.Helper()
	if def.Annotations == nil {
		t.Fatalf("%s annotations missing", def.Name)
	}
	if def.Annotations.ReadOnlyHint == nil || !*def.Annotations.ReadOnlyHint {
		t.Fatalf("%s should advertise readOnlyHint=true: %+v", def.Name, def.Annotations)
	}
	if def.Annotations.DestructiveHint == nil || *def.Annotations.DestructiveHint {
		t.Fatalf("%s should advertise destructiveHint=false: %+v", def.Name, def.Annotations)
	}
	if def.Annotations.IdempotentHint == nil || !*def.Annotations.IdempotentHint {
		t.Fatalf("%s should advertise idempotentHint=true: %+v", def.Name, def.Annotations)
	}
}

func assertMutationToolDef(t testing.TB, def mcpruntime.ToolDef, idempotent bool) {
	t.Helper()
	if def.Annotations == nil {
		t.Fatalf("%s annotations missing", def.Name)
	}
	if def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint {
		t.Fatalf("%s should advertise readOnlyHint=false: %+v", def.Name, def.Annotations)
	}
	if def.Annotations.DestructiveHint == nil || *def.Annotations.DestructiveHint {
		t.Fatalf("%s should advertise destructiveHint=false: %+v", def.Name, def.Annotations)
	}
	if def.Annotations.IdempotentHint == nil || *def.Annotations.IdempotentHint != idempotent {
		t.Fatalf("%s idempotentHint = %+v want %v", def.Name, def.Annotations.IdempotentHint, idempotent)
	}
}

func assertContains(t testing.TB, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func assertNotContains(t testing.TB, got string, forbidden string) {
	t.Helper()
	if strings.Contains(got, forbidden) {
		t.Fatalf("expected %q not to contain %q", got, forbidden)
	}
}
