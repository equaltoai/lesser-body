package ptahserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/golang-jwt/jwt/v5"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func TestRegisterToolsRegistersPtahDefinitions(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	tools := registry.List()
	for _, tool := range tools {
		if tool.Name == "agent_create" {
			t.Fatalf("removed agent_create tool is still advertised: %+v", tool)
		}
	}
	if got, want := toolDefNames(tools), []string{toolAgentBindSoul, toolAgentGet, toolAgentList, toolAgentSoulGet, toolAgentSoulUpsert, toolAgentSoulPublish, toolAgentSoulArchive, toolAgentInstructionsGet, toolAgentInstructionsUpsert, toolAgentInstructionsArchive, toolAgentGenesisSkillGet, toolAgentGenesisBegin, toolAgentGenesisList, toolAgentGenesisRead, toolAgentGenesisAdvance, toolAgentGenesisRecover, toolAgentGenesisFinalizePreflight, toolAgentGenesisFinalize}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered tool order = %v, want %v", got, want)
	}
	for _, tool := range tools {
		assertNotContains(t, tool.Description, "provisional_agent_soul_schema_pending_lesser_soul_s1")
	}
	assertReadOnlyToolDef(t, toolDefByName(t, tools, toolAgentGenesisSkillGet))
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisSkillGet).Description, "no local installation")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisSkillGet).Description, toolAgentGenesisBegin)
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisBegin).Description, toolAgentGenesisSkillGet)
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisBegin).Description, toolAgentGenesisAdvance)
	assertReadOnlyToolDef(t, toolDefByName(t, tools, toolAgentGenesisList))
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisList).Description, "recovery/navigation index")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisList).Description, "recommended")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRead).Description, "exact lossless")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisAdvance).Description, "candidate_action")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisAdvance).Description, "AppTheory MicroVM")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRecover).Description, "restart_soul_bootstrap")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRecover).Description, toolAgentGenesisBegin)
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRecover).Description, "retry_same_step")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRecover).Description, "refresh_state")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisRecover).Description, "operator_action")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisFinalizePreflight).Description, "declaration-ready")
	assertContains(t, toolDefByName(t, tools, toolAgentGenesisFinalize).Description, "Host-derived Ptah registry row")

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

	getDef := tools[1]
	if getDef.Name != toolAgentGet {
		t.Fatalf("second tool name = %q, want %q", getDef.Name, toolAgentGet)
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

	listDef := tools[2]
	if listDef.Name != toolAgentList {
		t.Fatalf("third tool name = %q, want %q", listDef.Name, toolAgentList)
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

	soulGetDef := tools[3]
	if soulGetDef.Name != toolAgentSoulGet {
		t.Fatalf("fourth tool name = %q, want %q", soulGetDef.Name, toolAgentSoulGet)
	}
	assertReadOnlyToolDef(t, soulGetDef)
	assertContains(t, soulGetDef.Description, "Panonomous soul-document v2")
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

	soulUpsertDef := tools[4]
	if soulUpsertDef.Name != toolAgentSoulUpsert {
		t.Fatalf("fifth tool name = %q, want %q", soulUpsertDef.Name, toolAgentSoulUpsert)
	}
	assertMutationToolDef(t, soulUpsertDef, false)
	assertContains(t, soulUpsertDef.Description, "soul_version")
	var soulUpsertSchema struct {
		Required []string       `json:"required"`
		Props    map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(soulUpsertDef.InputSchema, &soulUpsertSchema); err != nil {
		t.Fatalf("agent_soul_upsert input schema invalid json: %v", err)
	}
	for _, required := range []string{"agent_id"} {
		if !contains(soulUpsertSchema.Required, required) {
			t.Fatalf("agent_soul_upsert required = %v, missing %s", soulUpsertSchema.Required, required)
		}
	}
	for _, prop := range []string{"actor_username", "schema_version", "body", "content", "summary", "structure", "provenance"} {
		if _, ok := soulUpsertSchema.Props[prop]; !ok {
			t.Fatalf("agent_soul_upsert schema missing %s", prop)
		}
	}

	soulPublishDef := tools[5]
	if soulPublishDef.Name != toolAgentSoulPublish {
		t.Fatalf("sixth tool name = %q, want %q", soulPublishDef.Name, toolAgentSoulPublish)
	}
	assertMutationToolDef(t, soulPublishDef, true)
	assertContains(t, soulPublishDef.Description, "published immutable snapshot")

	soulArchiveDef := tools[6]
	if soulArchiveDef.Name != toolAgentSoulArchive {
		t.Fatalf("seventh tool name = %q, want %q", soulArchiveDef.Name, toolAgentSoulArchive)
	}
	assertMutationToolDef(t, soulArchiveDef, true)
	assertContains(t, soulArchiveDef.Description, "retire")
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

func TestAgentBindSoulCallsLesserWithDedicatedBearerAndHostLocalActor(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	store := &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("theory", "agent-0xabc", "theo-marsh")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithIntegrationBearer(" integration-secret ")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Theory", []string{"read", "write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{
		"soul_agent_id":"agent-0xabc",
		"idempotency_key":"bind-key-1",
		"actor_username":"theory",
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
	if store.getAccount != "theory" || store.getAgentID != "agent-0xabc" {
		t.Fatalf("registry lookup = account %q agent %q", store.getAccount, store.getAgentID)
	}
	if client.req.ActorUsername != "theo-marsh" || client.req.SoulAgentID != "agent-0xabc" {
		t.Fatalf("required request fields = %+v", client.req)
	}
	if client.req.ActorUsername == "theory" {
		t.Fatalf("regression: account-holder username was sent to Lesser as target actor")
	}
	if client.req.BodyActorID != "body://ptah/theo-marsh" {
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
	if agent["agent_id"] != "agent-0xabc" || agent["actor_username"] != "theo-marsh" {
		t.Fatalf("agent summary = %+v", agent)
	}
}

func TestAgentBindSoulAcceptsVerifiedBodyActorIDForms(t *testing.T) {
	for _, bodyActorID := range []string{"body://ptah/theo-marsh", "theo-marsh"} {
		t.Run(bodyActorID, func(t *testing.T) {
			client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
			store := &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("theory", "agent-0xabc", "theo-marsh")}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithIntegrationBearer("integration-secret")); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(toolContext("theory", []string{"write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(fmt.Sprintf(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1","body_actor_id":%q}`, bodyActorID)))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("result = %+v", result)
			}
			if client.calls != 1 || client.req.ActorUsername != "theo-marsh" {
				t.Fatalf("Lesser call = calls %d request %+v", client.calls, client.req)
			}
			if client.req.BodyActorID != "body://ptah/theo-marsh" {
				t.Fatalf("canonical body_actor_id = %q", client.req.BodyActorID)
			}
		})
	}
}

func TestAgentBindSoulRejectsAuthoritativeActorDivergence(t *testing.T) {
	response := successfulBindingResponse(false)
	response.Binding.AgentUsername = "sentinelsentinel"
	client := &fakeSoulBindingClient{
		resp:                  response,
		preserveResponseActor: true,
	}
	store := &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("owner", "agent-0xabc", "sentinel")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(
		toolContext("owner", []string{"write"}, "user-oauth-token"),
		toolAgentBindSoul,
		json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`),
	)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	assertToolError(t, result, "actor_endpoint_divergence", http.StatusConflict)
	if client.calls != 1 {
		t.Fatalf("Lesser binding calls = %d, want 1", client.calls)
	}
}

func TestAgentBindSoulAcceptsMixedCaseAuthoritativeActorAgreement(t *testing.T) {
	response := successfulBindingResponse(false)
	response.Binding.AgentUsername = "SentinelSentinel"
	client := &fakeSoulBindingClient{
		resp:                  response,
		preserveResponseActor: true,
	}
	store := &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("owner", "agent-0xabc", "SentinelSentinel")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(
		toolContext("owner", []string{"write"}, "user-oauth-token"),
		toolAgentBindSoul,
		json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`),
	)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("mixed-case agreement result = %+v", result)
	}
	if client.calls != 1 || client.req.ActorUsername != "sentinelsentinel" {
		t.Fatalf("Lesser binding request = calls:%d actor:%q", client.calls, client.req.ActorUsername)
	}
}

func TestAgentBindSoulRefetchesHostIdentityWhenRegistryMappingEmpty(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	store := &fakeAgentRegistry{getAgent: &agentregistry.Agent{
		Account:            "theory",
		AgentID:            "agent-0xabc",
		Source:             agentregistry.SourceHostGenesisFinalize,
		SourceAuthority:    agentregistry.SourceAuthorityLesserHost,
		SourceOperation:    agentregistry.SourceOperationAgentGenesisFinalize,
		HostRegistrationID: "reg-123",
		HostConversationID: "conv-456",
	}}
	identity := &fakeHostIdentityClient{identity: &hostapi.AgentIdentity{
		AgentID:                "agent-0xabc",
		Domain:                 "theory.greater.website",
		LocalID:                "theo-marsh",
		AuthorityModel:         lesserapi.SoulAuthorityModelInstanceTrust,
		AnchorState:            lesserapi.SoulAnchorStateHostedOffchain,
		OperationalBinding:     lesserapi.SoulOperationalBindingHostedBound,
		LifecycleStatus:        "active",
		Status:                 "active",
		SelfDescriptionVersion: 1,
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithHostIdentityClient(identity), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("theory", []string{"write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1","body_actor_id":"body://ptah/theo-marsh"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if identity.calls != 1 || identity.agentID != "agent-0xabc" {
		t.Fatalf("identity refetch = calls %d agent %q", identity.calls, identity.agentID)
	}
	if store.upsertFinalizedCalls != 1 {
		t.Fatalf("registry repair calls = %d, want 1", store.upsertFinalizedCalls)
	}
	if store.upsertFinalizedIn.Account != "theory" || store.upsertFinalizedIn.LocalID != "theo-marsh" || store.upsertFinalizedIn.Domain != "theory.greater.website" || store.upsertFinalizedIn.OperationalBinding != lesserapi.SoulOperationalBindingHostedBound || store.upsertFinalizedIn.SelfDescriptionVersion != 1 {
		t.Fatalf("registry repair input = %+v", store.upsertFinalizedIn)
	}
	if client.req.ActorUsername != "theo-marsh" || client.req.BodyActorID != "body://ptah/theo-marsh" {
		t.Fatalf("Lesser request = %+v", client.req)
	}
}

func TestAgentBindSoulHostRefetchDoesNotRewriteCorrectedRegistryLocalID(t *testing.T) {
	client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
	store := &fakeAgentRegistry{getAgent: &agentregistry.Agent{
		Account:         "owner",
		AgentID:         "agent-0xabc",
		Source:          agentregistry.SourceHostGenesisFinalize,
		SourceAuthority: agentregistry.SourceAuthorityLesserHost,
		SourceOperation: agentregistry.SourceOperationAgentGenesisFinalize,
		LocalID:         "sentinelsentinel@example.com",
	}}
	identity := &fakeHostIdentityClient{identity: &hostapi.AgentIdentity{
		AgentID:            "agent-0xabc",
		LocalID:            "sentinel",
		AuthorityModel:     lesserapi.SoulAuthorityModelInstanceTrust,
		AnchorState:        lesserapi.SoulAnchorStateHostedOffchain,
		OperationalBinding: lesserapi.SoulOperationalBindingHostedBound,
		LifecycleStatus:    "active",
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithHostIdentityClient(identity), WithIntegrationBearer("integration-secret")); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(
		toolContext("owner", []string{"write"}, "user-oauth-token"),
		toolAgentBindSoul,
		json.RawMessage(`{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`),
	)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "actor_endpoint_divergence", http.StatusConflict)
	details, _ := payload["details"].(map[string]any)
	if details["source"] != "agent_registry_host_refetch" {
		t.Fatalf("divergence details = %+v", details)
	}
	if identity.calls != 1 || store.upsertFinalizedCalls != 0 || client.calls != 0 {
		t.Fatalf("refetch side effects = identity:%d registry_writes:%d Lesser_calls:%d", identity.calls, store.upsertFinalizedCalls, client.calls)
	}
	if store.getAgent.LocalID != "sentinelsentinel@example.com" {
		t.Fatalf("corrected registry local_id rewritten to %q", store.getAgent.LocalID)
	}
}

func TestAgentBindSoulFailsClosedForUnverifiedTargetActor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		store      *fakeAgentRegistry
		args       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "body_actor_id mismatch",
			store:      &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("theory", "agent-0xabc", "theo-marsh")},
			args:       `{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1","body_actor_id":"body://ptah/other"}`,
			wantCode:   "forbidden",
			wantStatus: 403,
		},
		{
			name:       "cross account or missing registry row",
			store:      &fakeAgentRegistry{getErr: agentregistry.ErrAgentNotFound},
			args:       `{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1","body_actor_id":"body://ptah/theo-marsh"}`,
			wantCode:   "host_actor_mapping_unavailable",
			wantStatus: 409,
		},
		{
			name:       "non Host finalized registry row",
			store:      &fakeAgentRegistry{getAgent: &agentregistry.Agent{Account: "theory", AgentID: "agent-0xabc", LocalID: "theo-marsh"}},
			args:       `{"soul_agent_id":"agent-0xabc","idempotency_key":"bind-key-1"}`,
			wantCode:   "host_actor_mapping_unavailable",
			wantStatus: 409,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeSoulBindingClient{resp: successfulBindingResponse(false)}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(tc.store), WithIntegrationBearer("integration-secret")); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(toolContext("theory", []string{"write"}, "user-oauth-token"), toolAgentBindSoul, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, tc.wantCode, tc.wantStatus)
			if client.calls != 0 {
				t.Fatalf("client calls = %d, want 0", client.calls)
			}
		})
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
	store := &fakeAgentRegistry{getAgent: hostFinalizedRegistryAgent("drone-ada", "agent-0xabc", "drone-ada")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithSoulBindingClient(client), WithAgentRegistryStore(store), WithIntegrationBearer("integration-secret")); err != nil {
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
		Count: 2,
	}}
	live := &fakeAgentLiveClient{}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.listCalls != 1 || store.listIn.Account != "drone-ada" || store.listIn.Limit != agentregistry.MaxListLimit || store.listIn.Cursor != "" {
		t.Fatalf("registry List input = calls %d %+v, want account-scoped full-page read", store.listCalls, store.listIn)
	}
	data := structuredData(t, result)
	agents, _ := data["agents"].([]map[string]any)
	if len(agents) != 1 {
		t.Fatalf("agents = %+v, want one", data["agents"])
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
	if want := []string{"agent-001"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("agent ids = %v, want %v", gotIDs, want)
	}
	pagination, _ := data["pagination"].(map[string]any)
	nextCursor, _ := pagination["next_cursor"].(string)
	if nextCursor == "" || pagination["has_more"] != true || pagination["count"] != 1 || pagination["limit"] != 1 {
		t.Fatalf("pagination = %+v", pagination)
	}

	result, err = registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(fmt.Sprintf(`{"limit":10,"cursor":%q}`, nextCursor)))
	if err != nil {
		t.Fatalf("second Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("second result = %+v", result)
	}
	if store.listCalls != 2 || store.listIn.Account != "drone-ada" || store.listIn.Limit != agentregistry.MaxListLimit || store.listIn.Cursor != "" {
		t.Fatalf("second registry List input = calls %d %+v, want account-scoped full-page read", store.listCalls, store.listIn)
	}
	secondData := structuredData(t, result)
	secondAgents, _ := secondData["agents"].([]map[string]any)
	if len(secondAgents) != 1 {
		t.Fatalf("second agents = %+v, want one", secondData["agents"])
	}
	secondRegistry, _ := secondAgents[0]["registry"].(map[string]any)
	if secondRegistry["agent_id"] != "agent-002" {
		t.Fatalf("second registry = %+v, want agent-002", secondRegistry)
	}
}

func TestAgentListFallsBackToLesserLiveAgentsWhenRegistryIsEmpty(t *testing.T) {
	store := &fakeAgentRegistry{listResult: &agentregistry.ListResult{}}
	live := &fakeAgentLiveClient{agents: []lesserapi.AgentDirectoryEntry{{
		Username:     "scout",
		DisplayName:  "Scout",
		AgentType:    "CUSTOM",
		AgentVersion: "1",
	}}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.listCalls != 1 || live.calls != 1 {
		t.Fatalf("dependency calls = registry:%d live:%d, want one each", store.listCalls, live.calls)
	}
	data := structuredData(t, result)
	agents, _ := data["agents"].([]map[string]any)
	if len(agents) != 1 {
		t.Fatalf("agents = %+v, want one live entry", data["agents"])
	}
	if _, ok := agents[0]["registry"]; ok {
		t.Fatalf("live-only entry unexpectedly claimed Body registry ownership: %+v", agents[0])
	}
	if agents[0]["source"] != "lesser_live" {
		t.Fatalf("source = %v, want lesser_live", agents[0]["source"])
	}
	liveSummary, _ := agents[0]["live_agent"].(map[string]any)
	if liveSummary["username"] != "scout" {
		t.Fatalf("live_agent = %+v, want scout", liveSummary)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if strings.Contains(string(encoded), "access_token") || strings.Contains(string(encoded), "refresh_token") || strings.Contains(string(encoded), "delegated_scopes") {
		t.Fatalf("agent_list leaked credential/private field names: %s", encoded)
	}
}

func TestAgentListPreservesRegistryEntriesAcrossRegistryPages(t *testing.T) {
	store := &fakeAgentRegistry{listResults: map[string]*agentregistry.ListResult{
		"": {
			Agents:     []*agentregistry.Agent{{Account: "drone-ada", AgentID: "agent-001"}},
			NextCursor: "registry-page-2",
			HasMore:    true,
		},
		"registry-page-2": {
			Agents: []*agentregistry.Agent{{Account: "drone-ada", AgentID: "agent-002"}},
		},
	}}
	live := &fakeAgentLiveClient{}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("drone-ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{"limit":100}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.listCalls != 2 || live.calls != 1 {
		t.Fatalf("dependency calls = registry:%d live:%d, want two registry pages and one live read", store.listCalls, live.calls)
	}
	agents, _ := structuredData(t, result)["agents"].([]map[string]any)
	if len(agents) != 2 {
		t.Fatalf("agents = %+v, want both registry entries", agents)
	}
}

func TestAgentListMergesAndDeduplicatesRegistryAndLiveAgentsStably(t *testing.T) {
	store := &fakeAgentRegistry{listResult: &agentregistry.ListResult{Agents: []*agentregistry.Agent{
		{Account: "drone-ada", AgentID: "https://lesser.example/users/scout"},
		{Account: "drone-ada", AgentID: "ptah-only"},
	}}}
	live := &fakeAgentLiveClient{agents: []lesserapi.AgentDirectoryEntry{
		{Username: "scout", DisplayName: "Scout"},
		{Username: "live-only", DisplayName: "Live Only"},
		{Username: "scout", DisplayName: "Scout duplicate"},
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	agents, _ := structuredData(t, result)["agents"].([]map[string]any)
	if len(agents) != 3 {
		t.Fatalf("agents = %+v, want three deduplicated entries", agents)
	}
	orderedNames := make([]string, 0, len(agents))
	var sawScout, sawPtahOnly, sawLiveOnly bool
	for _, item := range agents {
		liveSummary, _ := item["live_agent"].(map[string]any)
		registrySummary, _ := item["registry"].(map[string]any)
		switch liveSummary["username"] {
		case "scout":
			orderedNames = append(orderedNames, "scout")
			sawScout = true
			if item["source"] != "merged" || registrySummary["agent_id"] != "https://lesser.example/users/scout" {
				t.Fatalf("scout merge = %+v", item)
			}
		case "live-only":
			orderedNames = append(orderedNames, "live-only")
			sawLiveOnly = true
			if item["source"] != "lesser_live" || registrySummary != nil {
				t.Fatalf("live-only merge = %+v", item)
			}
		default:
			if liveSummary != nil {
				t.Fatalf("unexpected live username in item = %+v", item)
			}
			registryID, _ := registrySummary["agent_id"].(string)
			orderedNames = append(orderedNames, registryID)
			if item["source"] != "ptah_registry" || registrySummary["agent_id"] != "ptah-only" {
				t.Fatalf("registry-only merge = %+v", item)
			}
			sawPtahOnly = true
		}
	}
	if !sawScout || !sawPtahOnly || !sawLiveOnly {
		t.Fatalf("merge coverage = scout:%t ptah-only:%t live-only:%t", sawScout, sawPtahOnly, sawLiveOnly)
	}
	if want := []string{"live-only", "scout", "ptah-only"}; !reflect.DeepEqual(orderedNames, want) {
		t.Fatalf("merged ordering = %v, want %v", orderedNames, want)
	}
}

func TestAgentListReturnsSanitizedLiveSourceError(t *testing.T) {
	store := &fakeAgentRegistry{listResult: &agentregistry.ListResult{}}
	live := &fakeAgentLiveClient{err: errors.New("upstream body contains private detail")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Drone-Ada", []string{"read"}, "owner-oauth-token"), toolAgentList, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "agent_live_source_error", http.StatusBadGateway)
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "private detail") {
		t.Fatalf("live source error leaked upstream detail: %s", encoded)
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
			live := &fakeAgentLiveClient{}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithAgentRegistryStore(store), WithAgentLiveClient(live)); err != nil {
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
			if live.calls != 0 {
				t.Fatalf("live agent calls = %d, want 0", live.calls)
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
	if store.listCalls != 1 || store.listIn.Account != "drone-ada" || store.listIn.Limit != agentregistry.MaxListLimit || store.listIn.Cursor != "bad-cursor" {
		t.Fatalf("registry List input = calls %d %+v, want full-page account scoped legacy cursor", store.listCalls, store.listIn)
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
			if schema["schema_version"] != agentcontent.SoulDocumentSchemaVersion || schema["status"] != "stable" {
				t.Fatalf("schema metadata = %+v", schema)
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
	if store.upsertIn.Account != "drone-ada" || store.upsertIn.AgentID != "agent-123" || store.upsertIn.Type != agentcontent.ContentTypeAgentSoul ||
		store.upsertIn.SoulDocument == nil || store.upsertIn.SoulDocument.Body != "draft soul v1" ||
		store.upsertIn.UpdatedBySubjectID != "subject-writer" {
		t.Fatalf("Upsert input = %+v, want account-scoped agent_soul with subject", store.upsertIn)
	}
	data := structuredData(t, result)
	record, _ := data["agent_soul"].(map[string]any)
	if record["updated_by_subject_id"] != "subject-writer" || record["content"] != "draft soul v1" {
		t.Fatalf("agent_soul record = %+v", record)
	}
	if strings.Contains(result.Content[0].Text, "draft soul v1") {
		t.Fatalf("text content duplicated soul body: %s", result.Content[0].Text)
	}
}

func TestAgentSoulPublishUsesStoreAndReportsIdempotentReplay(t *testing.T) {
	store := &fakeAgentContentStore{
		getRecord:     agentSoulRecord("drone-ada", "agent-123", "published soul", 4, agentcontent.LifecycleStatePublished, "subject-prev"),
		publishRecord: agentSoulRecord("drone-ada", "agent-123", "published soul", 4, agentcontent.LifecycleStatePublished, "subject-prev"),
	}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	result, err := registry.Call(toolContextWithSubject("Drone-Ada", []string{"write"}, "owner-oauth-token", "subject-publish"), toolAgentSoulPublish, json.RawMessage(`{"agent_id":"agent-123","actor_username":"drone-ada"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if store.getCalls != 1 || store.publishCalls != 1 {
		t.Fatalf("Get/Publish calls = %d/%d, want 1/1", store.getCalls, store.publishCalls)
	}
	if store.publishIn.Account != "drone-ada" || store.publishIn.AgentID != "agent-123" || store.publishIn.UpdatedBySubjectID != "subject-publish" {
		t.Fatalf("Publish input = %+v", store.publishIn)
	}
	data := structuredData(t, result)
	if data["already_published"] != true || data["idempotent"] != true {
		t.Fatalf("publish idempotency metadata = %+v", data)
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
			name:      "upsert invalid registry selector",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"local/agent","body":"draft"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "upsert explicit null structure",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","body":"draft","structure":null}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name: "upsert missing required section summary",
			tool: toolAgentSoulUpsert,
			ctx:  toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args: `{"agent_id":"agent-123","body":"draft","structure":{"five_bodies":{
					"identity":{},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
					"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[{"bypass":"x","invariant":"i","closestSafePath":"c"}]}
				}}}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "upsert trailing JSON",
			tool:      toolAgentSoulUpsert,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","body":"draft"} true`,
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
			name:      "get invalid registry selector",
			tool:      toolAgentSoulGet,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"),
			args:      `{"agent_id":"local/agent"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "get trailing JSON",
			tool:      toolAgentSoulGet,
			ctx:       toolContextWithSubject("drone-ada", []string{"read"}, "owner-oauth-token", "subject-reader"),
			args:      `{"agent_id":"agent-123"} true`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "publish invalid registry selector",
			tool:      toolAgentSoulPublish,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"local/agent"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "publish explicit null actor username",
			tool:      toolAgentSoulPublish,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123","actor_username":null}`,
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
		{
			name:      "archive invalid registry selector",
			tool:      toolAgentSoulArchive,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"local/agent"}`,
			wantCode:  "invalid_request",
			wantState: 400,
		},
		{
			name:      "archive trailing JSON",
			tool:      toolAgentSoulArchive,
			ctx:       toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:      `{"agent_id":"agent-123"} true`,
			wantCode:  "invalid_request",
			wantState: 400,
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
			if store.getCalls != 0 || store.upsertCalls != 0 || store.publishCalls != 0 || store.archiveCalls != 0 {
				t.Fatalf("content store side effects occurred before rejection: get=%d upsert=%d publish=%d archive=%d", store.getCalls, store.upsertCalls, store.publishCalls, store.archiveCalls)
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
			name: "archive draft transition",
			tool: toolAgentSoulArchive,
			store: &fakeAgentContentStore{
				getRecord: agentSoulRecord("drone-ada", "agent-123", "draft", 1, agentcontent.LifecycleStateDraft, "subject-prev"),
				archiveErr: &agentcontent.TransitionError{
					Action: agentcontent.ContentActionArchive,
					From:   agentcontent.LifecycleStateDraft,
					To:     agentcontent.LifecycleStateArchived,
				},
			},
			ctx:        toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
			args:       `{"agent_id":"agent-123"}`,
			wantCode:   "invalid_lifecycle_transition",
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

func TestAgentSoulPublishMapsLegacyRowToActionableTypedRewriteError(t *testing.T) {
	store := &fakeAgentContentStore{
		publishErr: &agentcontent.SoulRewriteRequiredError{
			Action: agentcontent.ContentActionPublish,
		},
	}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithAgentContentStore(store)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(
		toolContextWithSubject("drone-ada", []string{"write"}, "owner-oauth-token", "subject-writer"),
		toolAgentSoulPublish,
		json.RawMessage(`{"agent_id":"agent-123"}`),
	)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := assertToolError(t, result, "agent_soul_rewrite_required", http.StatusConflict)
	details, _ := payload["details"].(map[string]any)
	if details["content_type"] != string(agentcontent.ContentTypeAgentSoul) ||
		details["action"] != string(agentcontent.ContentActionPublish) ||
		details["rewrite_tool"] != toolAgentSoulUpsert ||
		details["publish_tool"] != toolAgentSoulPublish {
		t.Fatalf("rewrite guidance details = %+v", details)
	}
	message, _ := payload["message"].(string)
	for _, required := range []string{toolAgentSoulUpsert, toolAgentSoulPublish} {
		if !strings.Contains(message, required) {
			t.Fatalf("rewrite guidance message does not name %s: %q", required, message)
		}
	}
	if strings.Contains(message, "Body failed to access") {
		t.Fatalf("legacy row fell through to bare internal error: %q", message)
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
	calls                 int
	integrationBearer     string
	idempotencyKey        string
	req                   lesserapi.SoulBindingRequest
	resp                  *lesserapi.SoulBindingResponse
	err                   error
	preserveResponseActor bool
}

func (f *fakeSoulBindingClient) InitiateSoulBinding(_ context.Context, integrationBearer string, idempotencyKey string, req lesserapi.SoulBindingRequest) (*lesserapi.SoulBindingResponse, error) {
	f.calls++
	f.integrationBearer = integrationBearer
	f.idempotencyKey = idempotencyKey
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	if f.resp == nil || f.preserveResponseActor {
		return f.resp, nil
	}
	clone := *f.resp
	clone.Binding = f.resp.Binding
	clone.Binding.AgentUsername = req.ActorUsername
	return &clone, nil
}

type fakeHostIdentityClient struct {
	calls    int
	agentID  string
	identity *hostapi.AgentIdentity
	err      error
}

func (f *fakeHostIdentityClient) GetAgentIdentity(_ context.Context, agentID string) (*hostapi.AgentIdentity, error) {
	f.calls++
	f.agentID = agentID
	if f.err != nil {
		return nil, f.err
	}
	return f.identity, nil
}

type fakeAgentLiveClient struct {
	calls  int
	agents []lesserapi.AgentDirectoryEntry
	err    error
}

func (f *fakeAgentLiveClient) ListAgents(_ context.Context) ([]lesserapi.AgentDirectoryEntry, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.agents, nil
}

func hostFinalizedRegistryAgent(account string, agentID string, localID string) *agentregistry.Agent {
	return &agentregistry.Agent{
		Account:                strings.ToLower(strings.TrimSpace(account)),
		AgentID:                strings.TrimSpace(agentID),
		CreatedAt:              time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 7, 18, 12, 30, 0, 0, time.UTC),
		Source:                 agentregistry.SourceHostGenesisFinalize,
		SourceAuthority:        agentregistry.SourceAuthorityLesserHost,
		SourceOperation:        agentregistry.SourceOperationAgentGenesisFinalize,
		HostRegistrationID:     "reg-123",
		HostConversationID:     "conv-456",
		Domain:                 "theory.greater.website",
		LocalID:                strings.TrimSpace(localID),
		AuthorityModel:         lesserapi.SoulAuthorityModelInstanceTrust,
		AnchorState:            lesserapi.SoulAnchorStateHostedOffchain,
		OperationalBinding:     lesserapi.SoulOperationalBindingHostedBound,
		LifecycleStatus:        "active",
		PublishedVersion:       1,
		SelfDescriptionVersion: 1,
	}
}

type fakeAgentRegistry struct {
	calls int
	in    agentregistry.CreateInput
	agent *agentregistry.Agent
	err   error

	upsertFinalizedCalls   int
	upsertFinalizedIn      agentregistry.FinalizedInput
	upsertFinalizedCreated bool
	upsertFinalizedAgent   *agentregistry.Agent
	upsertFinalizedErr     error

	getCalls   int
	getAccount string
	getAgentID string
	getAgent   *agentregistry.Agent
	getErr     error

	listCalls   int
	listIn      agentregistry.ListInput
	listResult  *agentregistry.ListResult
	listResults map[string]*agentregistry.ListResult
	listErr     error
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

	publishCalls  int
	publishIn     agentcontent.PublishInput
	publishRecord *agentcontent.Record
	publishErr    error

	seedPublishedCalls   int
	seedPublishedIn      agentcontent.SeedPublishedInput
	seedPublishedRecord  *agentcontent.Record
	seedPublishedCreated bool
	seedPublishedErr     error

	seedInstructionsCalls   int
	seedInstructionsIn      agentcontent.SeedInstructionsInput
	seedInstructionsRecord  *agentcontent.Record
	seedInstructionsCreated bool
	seedInstructionsErr     error

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

func (f *fakeAgentRegistry) UpsertFinalized(_ context.Context, in agentregistry.FinalizedInput) (*agentregistry.Agent, bool, error) {
	f.upsertFinalizedCalls++
	f.upsertFinalizedIn = in
	if f.upsertFinalizedErr != nil {
		return nil, false, f.upsertFinalizedErr
	}
	if f.upsertFinalizedAgent != nil {
		return f.upsertFinalizedAgent, f.upsertFinalizedCreated, nil
	}
	agent := &agentregistry.Agent{
		Account:                strings.ToLower(strings.TrimSpace(in.Account)),
		AgentID:                strings.TrimSpace(in.AgentID),
		CreatedAt:              time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		Source:                 agentregistry.SourceHostGenesisFinalize,
		SourceAuthority:        agentregistry.SourceAuthorityLesserHost,
		SourceOperation:        agentregistry.SourceOperationAgentGenesisFinalize,
		HostRegistrationID:     strings.TrimSpace(in.HostRegistrationID),
		HostConversationID:     strings.TrimSpace(in.HostConversationID),
		Domain:                 strings.TrimSpace(in.Domain),
		LocalID:                strings.TrimSpace(in.LocalID),
		AuthorityModel:         strings.TrimSpace(in.AuthorityModel),
		AnchorState:            strings.TrimSpace(in.AnchorState),
		OperationalBinding:     strings.TrimSpace(in.OperationalBinding),
		LifecycleStatus:        strings.TrimSpace(in.LifecycleStatus),
		PublishedVersion:       in.PublishedVersion,
		SelfDescriptionVersion: in.SelfDescriptionVersion,
	}
	f.agent = agent
	return agent, f.upsertFinalizedCreated, nil
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
	if f.listResults != nil {
		if result, ok := f.listResults[in.Cursor]; ok {
			return result, nil
		}
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
	content := in.Content
	if in.SoulDocument != nil {
		content = in.SoulDocument.Body
	}
	return agentContentRecord(in.Type, in.Account, in.AgentID, content, 1, agentcontent.LifecycleStateDraft, in.UpdatedBySubjectID), nil
}

func (f *fakeAgentContentStore) Publish(_ context.Context, in agentcontent.PublishInput) (*agentcontent.Record, error) {
	f.publishCalls++
	f.publishIn = in
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	if f.publishRecord != nil {
		return f.publishRecord, nil
	}
	if f.getRecord != nil {
		record := cloneAgentContentRecord(f.getRecord)
		record.LifecycleState = agentcontent.LifecycleStatePublished
		record.UpdatedBySubjectID = in.UpdatedBySubjectID
		return record, nil
	}
	return agentContentRecord(agentcontent.ContentTypeAgentSoul, in.Account, in.AgentID, "", 1, agentcontent.LifecycleStatePublished, in.UpdatedBySubjectID), nil
}

func (f *fakeAgentContentStore) SeedPublished(_ context.Context, in agentcontent.SeedPublishedInput) (*agentcontent.Record, bool, error) {
	f.seedPublishedCalls++
	f.seedPublishedIn = in
	if f.seedPublishedErr != nil {
		return nil, false, f.seedPublishedErr
	}
	if f.seedPublishedRecord != nil {
		return f.seedPublishedRecord, f.seedPublishedCreated, nil
	}
	record := agentContentRecord(agentcontent.ContentTypeAgentSoul, in.Account, in.AgentID, in.SoulDocument.Body, 2, agentcontent.LifecycleStatePublished, in.UpdatedBySubjectID)
	record.SoulVersion = 1
	record.Document = cloneSoulDocumentForTest(in.SoulDocument)
	record.Document.SoulVersion = 1
	record.Document.LifecycleState = agentcontent.LifecycleStatePublished
	record.Document.Version = 2
	record.Document.UpdatedBySubjectID = in.UpdatedBySubjectID
	return record, true, nil
}

func (f *fakeAgentContentStore) SeedInstructions(_ context.Context, in agentcontent.SeedInstructionsInput) (*agentcontent.Record, bool, error) {
	f.seedInstructionsCalls++
	f.seedInstructionsIn = in
	if f.seedInstructionsErr != nil {
		return nil, false, f.seedInstructionsErr
	}
	if f.seedInstructionsRecord != nil {
		return f.seedInstructionsRecord, f.seedInstructionsCreated, nil
	}
	return agentContentRecord(
		agentcontent.ContentTypeAgentInstructions,
		in.Account,
		in.AgentID,
		in.Content,
		1,
		agentcontent.LifecycleStateDraft,
		in.UpdatedBySubjectID,
	), true, nil
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

type memoryAgentRegistry struct {
	records map[string]*agentregistry.Agent

	upsertFinalizedCalls int
}

func newMemoryAgentRegistry() *memoryAgentRegistry {
	return &memoryAgentRegistry{records: map[string]*agentregistry.Agent{}}
}

func (m *memoryAgentRegistry) Create(_ context.Context, in agentregistry.CreateInput) (*agentregistry.Agent, error) {
	if m.records == nil {
		m.records = map[string]*agentregistry.Agent{}
	}
	key := memoryAgentRegistryKey(in.Account, in.AgentID)
	if _, ok := m.records[key]; ok {
		return nil, agentregistry.ErrAgentAlreadyExists
	}
	agent := &agentregistry.Agent{
		Account:   strings.ToLower(strings.TrimSpace(in.Account)),
		AgentID:   strings.TrimSpace(in.AgentID),
		CreatedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
	}
	m.records[key] = cloneRegistryAgent(agent)
	return agent, nil
}

func (m *memoryAgentRegistry) UpsertFinalized(_ context.Context, in agentregistry.FinalizedInput) (*agentregistry.Agent, bool, error) {
	if m.records == nil {
		m.records = map[string]*agentregistry.Agent{}
	}
	m.upsertFinalizedCalls++
	key := memoryAgentRegistryKey(in.Account, in.AgentID)
	created := false
	agent := m.records[key]
	if agent == nil {
		created = true
		agent = &agentregistry.Agent{
			CreatedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		}
	}
	agent.Account = strings.ToLower(strings.TrimSpace(in.Account))
	agent.AgentID = strings.TrimSpace(in.AgentID)
	agent.UpdatedAt = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Add(time.Duration(m.upsertFinalizedCalls) * time.Minute)
	agent.Source = agentregistry.SourceHostGenesisFinalize
	agent.SourceAuthority = agentregistry.SourceAuthorityLesserHost
	agent.SourceOperation = agentregistry.SourceOperationAgentGenesisFinalize
	agent.HostRegistrationID = strings.TrimSpace(in.HostRegistrationID)
	agent.HostConversationID = strings.TrimSpace(in.HostConversationID)
	agent.Domain = strings.TrimSpace(in.Domain)
	agent.LocalID = strings.TrimSpace(in.LocalID)
	agent.AuthorityModel = strings.TrimSpace(in.AuthorityModel)
	agent.AnchorState = strings.TrimSpace(in.AnchorState)
	agent.OperationalBinding = strings.TrimSpace(in.OperationalBinding)
	agent.LifecycleStatus = strings.TrimSpace(in.LifecycleStatus)
	agent.PublishedVersion = in.PublishedVersion
	agent.SelfDescriptionVersion = in.SelfDescriptionVersion
	m.records[key] = cloneRegistryAgent(agent)
	return cloneRegistryAgent(agent), created, nil
}

func (m *memoryAgentRegistry) Get(_ context.Context, account string, agentID string) (*agentregistry.Agent, error) {
	agent := m.records[memoryAgentRegistryKey(account, agentID)]
	if agent == nil {
		return nil, agentregistry.ErrAgentNotFound
	}
	return cloneRegistryAgent(agent), nil
}

func (m *memoryAgentRegistry) List(_ context.Context, in agentregistry.ListInput) (*agentregistry.ListResult, error) {
	account := strings.ToLower(strings.TrimSpace(in.Account))
	agents := make([]*agentregistry.Agent, 0)
	for _, agent := range m.records {
		if agent != nil && strings.ToLower(strings.TrimSpace(agent.Account)) == account {
			agents = append(agents, cloneRegistryAgent(agent))
		}
	}
	return &agentregistry.ListResult{Agents: agents, Count: len(agents)}, nil
}

func memoryAgentRegistryKey(account string, agentID string) string {
	return strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.TrimSpace(agentID)
}

func cloneRegistryAgent(agent *agentregistry.Agent) *agentregistry.Agent {
	if agent == nil {
		return nil
	}
	clone := *agent
	return &clone
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
	content := in.Content
	var document *agentcontent.SoulDocument
	if in.SoulDocument != nil {
		document = cloneSoulDocumentForTest(in.SoulDocument)
		content = document.Body
	}
	record := agentContentRecord(in.Type, in.Account, in.AgentID, content, version, agentcontent.LifecycleStateDraft, in.UpdatedBySubjectID)
	record.SoulVersion = version
	record.Document = document
	record.CreatedAt = createdAt
	record.UpdatedAt = createdAt.Add(time.Duration(version) * time.Minute)
	f.records[key] = cloneAgentContentRecord(record)
	return record, nil
}

func (f *versionedFakeAgentContentStore) Publish(_ context.Context, in agentcontent.PublishInput) (*agentcontent.Record, error) {
	key := agentContentTestKey{account: in.Account, agentID: in.AgentID, typ: agentcontent.ContentTypeAgentSoul}
	record := f.records[key]
	if record == nil {
		return nil, agentcontent.ErrContentNotFound
	}
	published := cloneAgentContentRecord(record)
	if published.LifecycleState == agentcontent.LifecycleStatePublished {
		return published, nil
	}
	if published.LifecycleState != agentcontent.LifecycleStateDraft {
		return nil, &agentcontent.TransitionError{
			Action: agentcontent.ContentActionPublish,
			From:   published.LifecycleState,
			To:     agentcontent.LifecycleStatePublished,
		}
	}
	published.Version++
	published.LifecycleState = agentcontent.LifecycleStatePublished
	published.UpdatedBySubjectID = in.UpdatedBySubjectID
	published.UpdatedAt = published.UpdatedAt.Add(time.Minute)
	if published.Document != nil {
		published.Document.LifecycleState = agentcontent.LifecycleStatePublished
		published.Document.UpdatedBySubjectID = in.UpdatedBySubjectID
		published.Document.Version = published.Version
	}
	f.records[key] = cloneAgentContentRecord(published)
	return published, nil
}

func (f *versionedFakeAgentContentStore) SeedPublished(ctx context.Context, in agentcontent.SeedPublishedInput) (*agentcontent.Record, bool, error) {
	key := agentContentTestKey{account: in.Account, agentID: in.AgentID, typ: agentcontent.ContentTypeAgentSoul}
	if current := f.records[key]; current != nil {
		if current.Document == nil || current.Document.Provenance == nil || in.SoulDocument == nil || in.SoulDocument.Provenance == nil ||
			current.Document.Provenance.DeclarationCandidateHash != in.SoulDocument.Provenance.DeclarationCandidateHash {
			return nil, false, agentcontent.ErrContentConflict
		}
		if current.LifecycleState == agentcontent.LifecycleStatePublished {
			return cloneAgentContentRecord(current), false, nil
		}
		published, err := f.Publish(ctx, agentcontent.PublishInput{
			Account:            in.Account,
			AgentID:            in.AgentID,
			UpdatedBySubjectID: in.UpdatedBySubjectID,
		})
		return published, false, err
	}
	draft, err := f.Upsert(ctx, agentcontent.UpsertInput{
		Account:            in.Account,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentSoul,
		SoulDocument:       in.SoulDocument,
		UpdatedBySubjectID: in.UpdatedBySubjectID,
	})
	if err != nil {
		return nil, false, err
	}
	_ = draft
	published, err := f.Publish(ctx, agentcontent.PublishInput{
		Account:            in.Account,
		AgentID:            in.AgentID,
		UpdatedBySubjectID: in.UpdatedBySubjectID,
	})
	return published, true, err
}

func (f *versionedFakeAgentContentStore) SeedInstructions(ctx context.Context, in agentcontent.SeedInstructionsInput) (*agentcontent.Record, bool, error) {
	key := agentContentTestKey{account: in.Account, agentID: in.AgentID, typ: agentcontent.ContentTypeAgentInstructions}
	if current := f.records[key]; current != nil {
		return cloneAgentContentRecord(current), false, nil
	}
	record, err := f.Upsert(ctx, agentcontent.UpsertInput{
		Account:            in.Account,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentInstructions,
		Content:            in.Content,
		UpdatedBySubjectID: in.UpdatedBySubjectID,
	})
	return record, err == nil, err
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

func cloneSoulDocumentForTest(document *agentcontent.SoulDocument) *agentcontent.SoulDocument {
	if document == nil {
		return nil
	}
	payload, _ := json.Marshal(document)
	var clone agentcontent.SoulDocument
	_ = json.Unmarshal(payload, &clone)
	return &clone
}

func operatorToolContext(username string, scopes []string, bearer string) context.Context {
	ctx := toolContext(username, scopes, bearer)
	principal := auth.PrincipalFromToolContext(ctx)
	if principal != nil && principal.Claims != nil {
		principal.Claims.ClientClass = "operator"
	}
	return ctx
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
	record := &agentcontent.Record{
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
	if contentType == agentcontent.ContentTypeAgentSoul {
		record.SoulVersion = version
		record.Document = &agentcontent.SoulDocument{
			SchemaVersion:      agentcontent.SoulDocumentSchemaVersion,
			AgentID:            agentID,
			Body:               content,
			SoulVersion:        version,
			LifecycleState:     state,
			UpdatedBySubjectID: updatedBySubjectID,
			CreatedAt:          record.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:          record.UpdatedAt.Format(time.RFC3339Nano),
			Version:            version,
		}
	}
	return record
}

func cloneAgentContentRecord(record *agentcontent.Record) *agentcontent.Record {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Document = cloneSoulDocumentForTest(record.Document)
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

func toolDefByName(t testing.TB, defs []mcpruntime.ToolDef, name string) mcpruntime.ToolDef {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("tool %s not found in %v", name, toolDefNames(defs))
	return mcpruntime.ToolDef{}
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
