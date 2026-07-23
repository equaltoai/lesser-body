package ptahserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

type fakeGenesisClient struct {
	bearer string
	calls  []string

	beginRequest      hostapi.RegistrationBeginRequest
	listAgentID       string
	listLimit         int
	advanceRequest    hostapi.MintConversationRequest
	registrationID    string
	conversationID    string
	beginResponse     map[string]any
	listResponse      map[string]any
	listErr           error
	advanceResponse   map[string]any
	readResponse      map[string]any
	recoverResponse   map[string]any
	preflightResponse map[string]any
	finalizeResponse  map[string]any
}

func (f *fakeGenesisClient) BeginRegistration(_ context.Context, bearer string, req hostapi.RegistrationBeginRequest) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "begin")
	f.beginRequest = req
	return f.beginResponse, nil
}

func (f *fakeGenesisClient) ListConversations(_ context.Context, bearer string, agentID string, limit int) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "list:"+agentID)
	f.listAgentID = agentID
	f.listLimit = limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResponse, nil
}

func (f *fakeGenesisClient) AdvanceConversation(_ context.Context, bearer string, registrationID string, req hostapi.MintConversationRequest) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "advance:"+registrationID)
	f.registrationID = registrationID
	f.conversationID = req.ConversationID
	f.advanceRequest = req
	return f.advanceResponse, nil
}

func (f *fakeGenesisClient) ReadConversation(_ context.Context, bearer string, registrationID string, conversationID string) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "read:"+registrationID+":"+conversationID)
	f.registrationID = registrationID
	f.conversationID = conversationID
	return f.readResponse, nil
}

func (f *fakeGenesisClient) RecoverConversation(_ context.Context, bearer string, registrationID string, conversationID string) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "recover:"+registrationID+":"+conversationID)
	f.registrationID = registrationID
	f.conversationID = conversationID
	return f.recoverResponse, nil
}

func (f *fakeGenesisClient) FinalizePreflight(_ context.Context, bearer string, registrationID string, conversationID string) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "preflight:"+registrationID+":"+conversationID)
	f.registrationID = registrationID
	f.conversationID = conversationID
	return f.preflightResponse, nil
}

func (f *fakeGenesisClient) FinalizeConversation(_ context.Context, bearer string, registrationID string, conversationID string) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "finalize:"+registrationID+":"+conversationID)
	f.registrationID = registrationID
	f.conversationID = conversationID
	return f.finalizeResponse, nil
}

func TestGenesisRejectsOrdinaryOAuthAndPaymentEvidenceWithoutCallingHost(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	for _, tc := range []struct {
		name      string
		scopes    []string
		wantError string
	}{
		{name: "read", scopes: []string{"read"}, wantError: "insufficient_scope"},
		{name: "write", scopes: []string{"write"}, wantError: "owner_operator_required"},
		{name: "follow", scopes: []string{"follow"}, wantError: "insufficient_scope"},
		{name: "push", scopes: []string{"push"}, wantError: "insufficient_scope"},
		{name: "read_write", scopes: []string{"read", "write"}, wantError: "owner_operator_required"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeGenesisClient{}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			ctx := auth.InjectToolRequestSnapshot(toolContext("owner", tc.scopes, "ordinary-oauth-bearer"), auth.ToolRequestSnapshot{
				Headers: map[string][]string{
					"lesser-x402-grant":      {"grant-token-test-only"},
					"lesser-x402-grant-id":   {"grant-id-test-only"},
					"lesser-x402-capability": {"tools.invoke"},
					"payment-signature":      {"payment-proof-test-only"},
				},
				Body: []byte(`{"domain":"example.com","local_id":"new-agent"}`),
			})
			result, err := registry.Call(ctx, toolAgentGenesisBegin, json.RawMessage(`{
				"domain":"example.com",
				"local_id":"new-agent"
			}`))
			if err != nil {
				t.Fatalf("genesis call: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("ordinary OAuth token should be rejected: %+v", result)
			}
			if got := structuredErrorCode(t, result); got != tc.wantError {
				t.Fatalf("error code = %q, want %s", got, tc.wantError)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("ordinary OAuth token reached Host: %v", fake.calls)
			}
		})
	}
}

func TestGenesisListRecommendationsUseOnlyCurrentHostStatuses(t *testing.T) {
	const agentID = "0xagent"
	conversations := sanitizeGenesisConversationSummaries([]any{
		map[string]any{"registration_id": "reg-ready", "conversation_id": "conv-ready", "status": "declaration_ready", "message_count": 2},
		map[string]any{"registration_id": "reg-owner", "conversation_id": "conv-owner", "status": "assistant_turn_ready", "message_count": 3},
		map[string]any{"registration_id": "reg-processing", "conversation_id": "conv-processing", "status": "in_progress", "message_count": 4},
		map[string]any{"registration_id": "reg-published", "conversation_id": "conv-published", "status": "published", "message_count": 5},
	}, agentID)
	if len(conversations) != 4 {
		t.Fatalf("conversations = %+v", conversations)
	}
	if conversations[0]["recommended_next_tool"] != toolAgentGenesisFinalizePreflight {
		t.Fatalf("declaration_ready recommendation = %+v", conversations[0])
	}
	if conversations[1]["recommended_next_tool"] != toolAgentGenesisRead || conversations[1]["alternate_next_tool"] != toolAgentGenesisAdvance {
		t.Fatalf("assistant_turn_ready recommendation = %+v", conversations[1])
	}
	if conversations[2]["recommended_next_tool"] != toolAgentGenesisRead || conversations[2]["wait"] != true {
		t.Fatalf("in_progress recommendation = %+v", conversations[2])
	}
	if conversations[3]["terminal"] != true || conversations[3]["recommended_next_tool"] != toolAgentGet || conversations[3]["alternate_next_tool"] != toolAgentList {
		t.Fatalf("published terminal recommendation = %+v", conversations[3])
	}
	start := genesisListStartHere(conversations, agentID)
	if start["recommended_next_tool"] != toolAgentGenesisFinalizePreflight || start["registration_id"] != "reg-ready" {
		t.Fatalf("start = %+v, want newest actionable non-terminal declaration_ready lane", start)
	}
}

func TestAgentGenesisListReturnsSanitizedHostError(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	fake := &fakeGenesisClient{listErr: &hostapi.APIError{Status: http.StatusForbidden, Code: "soul_instance.forbidden"}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	result, err := registry.Call(operatorToolContext("owner", []string{"read"}, "owner-oauth-bearer-test-only"), toolAgentGenesisList, json.RawMessage(`{"agent_id":"0xabc","limit":3}`))
	if err != nil {
		t.Fatalf("agent_genesis_list: %v", err)
	}
	payload := assertToolError(t, result, "forbidden", http.StatusForbidden)
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "owner-oauth-bearer") || strings.Contains(string(encoded), "host-instance-key") {
		t.Fatalf("host error leaked credential material: %s", string(encoded))
	}
}

func TestGenesisOwnerUsesHostStateMachineWithoutPreexistingAgent(t *testing.T) {
	const (
		hostKey        = "host-instance-key-test-only"
		callerBearer   = "owner-oauth-bearer-test-only"
		oldTranscript  = "private-full-genesis-transcript-must-not-be-returned"
		privateDeclare = "private-produced-declaration-must-not-be-returned"
		walletSecret   = "wallet-signature-secret-must-not-be-returned"
	)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", hostKey)
	fake := &fakeGenesisClient{
		beginResponse: map[string]any{
			"registration": map[string]any{
				"id":              "reg-123",
				"agent_id":        "agent-123",
				"domain":          "example.com",
				"local_id":        "new-agent",
				"authority_model": "instance_trust",
				"wallet_address":  walletSecret,
			},
			"promotion": map[string]any{"stage": "pending"},
		},
		advanceResponse: genesisConversationResponse("assistant_turn_ready", oldTranscript),
		readResponse:    genesisConversationResponse("assistant_turn_ready", oldTranscript),
		recoverResponse: genesisConversationResponse("assistant_turn_ready", oldTranscript),
		preflightResponse: map[string]any{
			"conversation": map[string]any{
				"registration_id": "reg-123", "conversation_id": "conv-456", "status": "declaration_ready",
				"declaration_candidate": genesisFinalizedCandidateProjection("preflight owner review"),
			},
			"authority_model":     "instance_trust",
			"declaration_preview": privateDeclare,
		},
		finalizeResponse: map[string]any{
			"agent_id": "agent-123",
			"publication": map[string]any{
				"agent_id":          "agent-123",
				"published_version": 1,
				"stage":             "hosted_offchain",
				"wallet_signature":  walletSecret,
			},
		},
	}
	registryStore := newMemoryAgentRegistry()

	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake), WithAgentRegistryStore(registryStore)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("owner", []string{"read", "write"}, callerBearer)

	begin := callGenesisTool(t, registry, ctx, toolAgentGenesisBegin, `{"domain":"example.com","local_id":"new-agent","capabilities":["post"]}`)
	beginData := structuredGenesisData(t, begin)
	if beginData["source"] != "lesser_host" || beginData["state_authority"] != "Host HostedGenesisSession" || beginData["flow"] != "genesis_conversation" {
		t.Fatalf("begin source/authority = %#v", beginData)
	}
	if got := beginData["registration_id"]; got != "reg-123" {
		t.Fatalf("registration id = %#v, want reg-123", got)
	}
	if fake.beginRequest.Domain != "example.com" || fake.beginRequest.LocalID != "new-agent" || len(fake.beginRequest.Capabilities) != 1 || fake.beginRequest.Capabilities[0] != "post" {
		t.Fatalf("Host begin request = %+v", fake.beginRequest)
	}
	if fake.bearer != hostKey {
		t.Fatalf("Host bearer = %q, want server-side instance key", fake.bearer)
	}

	advance := callGenesisTool(t, registry, ctx, toolAgentGenesisAdvance, `{"registration_id":"reg-123","model":"genesis-model","message":"Start the genesis conversation.","idempotency_key":"turn-1"}`)
	if fake.advanceRequest.ConversationID != "" || fake.advanceRequest.Model != "genesis-model" || fake.advanceRequest.Message != "Start the genesis conversation." || fake.advanceRequest.IdempotencyKey != "turn-1" {
		t.Fatalf("Host advance request = %+v", fake.advanceRequest)
	}
	if data := structuredGenesisData(t, advance); data["conversation"] == nil {
		t.Fatalf("advance omitted Host conversation projection: %#v", data)
	}

	read := callGenesisTool(t, registry, ctx, toolAgentGenesisRead, `{"registration_id":"reg-123","conversation_id":"conv-456"}`)
	recover := callGenesisTool(t, registry, ctx, toolAgentGenesisRecover, `{"registration_id":"reg-123","conversation_id":"conv-456"}`)
	preflight := callGenesisTool(t, registry, ctx, toolAgentGenesisFinalizePreflight, `{"registration_id":"reg-123","conversation_id":"conv-456"}`)
	finalize := callGenesisTool(t, registry, ctx, toolAgentGenesisFinalize, `{"registration_id":"reg-123","conversation_id":"conv-456"}`)
	finalizeData := structuredGenesisData(t, finalize)
	if finalizeData["agent_id"] != "agent-123" {
		t.Fatalf("finalize agent id = %#v", finalizeData["agent_id"])
	}
	if registryStore.upsertFinalizedCalls != 1 || len(registryStore.records) != 1 {
		t.Fatalf("finalize registry writes = calls %d rows %d, want one Host-derived row", registryStore.upsertFinalizedCalls, len(registryStore.records))
	}

	wantCalls := []string{
		"begin",
		"advance:reg-123",
		"read:reg-123:conv-456",
		"recover:reg-123:conv-456",
		"preflight:reg-123:conv-456",
		"finalize:reg-123:conv-456",
	}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("Host state-machine calls = %v, want %v", fake.calls, wantCalls)
	}

	encoded := mustMarshalGenesisResult(t, begin, advance, read, recover, preflight, finalize)
	for _, secret := range []string{hostKey, callerBearer, oldTranscript, privateDeclare, walletSecret} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("genesis result leaked %q: %s", secret, encoded)
		}
	}
	conversation, _ := structuredGenesisData(t, advance)["conversation"].(map[string]any)
	messages, _ := conversation["messages"].([]map[string]any)
	if len(messages) != 1 || messages[0]["content"] != "The latest Host genesis turn." {
		t.Fatalf("genesis should expose only the latest bounded Host turn: %#v", conversation["messages"])
	}
	if truncated, _ := conversation["messages_truncated"].(bool); !truncated {
		t.Fatalf("genesis response must mark omitted transcript turns: %#v", conversation)
	}
}

func TestGenesisFinalizeWritesRegistryRowAndMintedAgentIsVisible(t *testing.T) {
	const hostKey = "host-instance-key-test-only"
	t.Setenv("LESSER_HOST_INSTANCE_KEY", hostKey)
	fake := &fakeGenesisClient{
		finalizeResponse: map[string]any{
			"agent_id":          "agent-0xabc",
			"published_version": 7,
			"agent": map[string]any{
				"agent_id":                 "agent-0xabc",
				"domain":                   "example.com",
				"local_id":                 "ada",
				"authority_model":          "instance_trust",
				"anchor_state":             "hosted_offchain",
				"operational_binding":      "hosted_bound_soul",
				"lifecycle_status":         "active",
				"status":                   "active",
				"self_description_version": 7,
			},
			"conversation": map[string]any{
				"registration_id":       "reg-123",
				"conversation_id":       "conv-456",
				"agent_id":              "agent-0xabc",
				"status":                "published",
				"declaration_candidate": genesisFinalizedCandidateProjection("published owner review"),
			},
			"publication": map[string]any{
				"agent_id":          "agent-0xabc",
				"published_version": 7,
				"registration_uri":  "s3://host/registration.json",
				"authority_model":   "instance_trust",
				"anchor_state":      "hosted_offchain",
			},
		},
	}
	registryStore := newMemoryAgentRegistry()
	contentStore := newVersionedFakeAgentContentStore()
	if _, err := contentStore.Upsert(context.Background(), agentcontent.UpsertInput{
		Account:            "owner",
		AgentID:            "agent-0xabc",
		Type:               agentcontent.ContentTypeAgentSoul,
		Content:            "safe soul draft summary source",
		UpdatedBySubjectID: "subject-writer",
	}); err != nil {
		t.Fatalf("seed agent_soul content: %v", err)
	}
	if _, err := contentStore.Upsert(context.Background(), agentcontent.UpsertInput{
		Account:            "owner",
		AgentID:            "agent-0xabc",
		Type:               agentcontent.ContentTypeAgentInstructions,
		Content:            "safe instructions summary source",
		UpdatedBySubjectID: "subject-writer",
	}); err != nil {
		t.Fatalf("seed agent_instructions content: %v", err)
	}

	tools := mcpruntime.NewToolRegistry()
	if err := RegisterTools(tools, WithGenesisClient(fake), WithAgentRegistryStore(registryStore), WithAgentContentStore(contentStore), WithAgentLiveClient(&fakeAgentLiveClient{})); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("Owner", []string{"read", "write"}, "owner-oauth-bearer-test-only")
	args := `{"registration_id":"reg-123","conversation_id":"conv-456"}`

	first := callGenesisTool(t, tools, ctx, toolAgentGenesisFinalize, args)
	second := callGenesisTool(t, tools, ctx, toolAgentGenesisFinalize, args)
	if registryStore.upsertFinalizedCalls != 2 || len(registryStore.records) != 1 {
		t.Fatalf("double finalize registry writes = calls %d rows %d, want two idempotent calls and one row", registryStore.upsertFinalizedCalls, len(registryStore.records))
	}
	firstData := structuredGenesisData(t, first)
	firstRegistry, _ := firstData["registry"].(map[string]any)
	hostIdentity, _ := firstRegistry["host_identity"].(map[string]any)
	if hostIdentity["domain"] != "example.com" || hostIdentity["local_id"] != "ada" || hostIdentity["operational_binding"] != "hosted_bound_soul" || hostIdentity["lifecycle_status"] != "active" || hostIdentity["published_version"] != int64(7) || hostIdentity["self_description_version"] != int64(7) {
		t.Fatalf("finalize registry host_identity = %+v, want Host agent/publication fields captured", hostIdentity)
	}
	provenance, _ := firstRegistry["provenance"].(map[string]any)
	if provenance["source"] != agentregistry.SourceHostGenesisFinalize || provenance["authority"] != agentregistry.SourceAuthorityLesserHost || provenance["caller_claimed"] != false {
		t.Fatalf("finalize registry provenance = %+v", provenance)
	}
	guidance, _ := firstData["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGet || guidance["alternate_next_tool"] != toolAgentList {
		t.Fatalf("finalize guidance = %+v, want agent_get/agent_list", guidance)
	}
	secondData := structuredGenesisData(t, second)
	secondWrite, _ := secondData["registry_write"].(map[string]any)
	if secondWrite["created"] != false || secondWrite["idempotent"] != true {
		t.Fatalf("second registry_write = %+v, want idempotent replay", secondWrite)
	}

	getData := callToolData(t, tools, toolContextWithSubject("owner", []string{"read"}, "owner-oauth-bearer-test-only", "subject-reader"), toolAgentGet, `{"agent_id":"agent-0xabc"}`)
	getRegistry, _ := getData["registry"].(map[string]any)
	if getRegistry["agent_id"] != "agent-0xabc" {
		t.Fatalf("agent_get registry = %+v", getRegistry)
	}
	contentVersion, _ := getData["content_version"].(map[string]any)
	if contentVersion["status"] != "available" || contentVersion["source"] != "agentcontent" || contentVersion["agent_soul"] == nil || contentVersion["agent_instructions"] == nil {
		t.Fatalf("agent_get content_version = %+v, want source-backed content metadata", contentVersion)
	}

	crossAccountResult, err := tools.Call(toolContextWithSubject("other", []string{"read"}, "other-oauth-bearer-test-only", "subject-reader"), toolAgentGet, json.RawMessage(`{"agent_id":"agent-0xabc"}`))
	if err != nil {
		t.Fatalf("cross-account agent_get: %v", err)
	}
	assertToolError(t, crossAccountResult, "not_found", 404)

	listData := callToolData(t, tools, toolContextWithSubject("owner", []string{"read"}, "owner-oauth-bearer-test-only", "subject-reader"), toolAgentList, `{}`)
	agents, _ := listData["agents"].([]map[string]any)
	if len(agents) != 1 {
		t.Fatalf("agent_list agents = %+v, want one minted registry row", listData["agents"])
	}
	listRegistry, _ := agents[0]["registry"].(map[string]any)
	if agents[0]["source"] != agentListRegistrySourceCode || listRegistry["agent_id"] != "agent-0xabc" {
		t.Fatalf("agent_list minted item = %+v", agents[0])
	}
}

func TestGenesisRecoveryReasonAndRestartGuidanceAreSanitized(t *testing.T) {
	raw := map[string]any{
		"conversation": map[string]any{
			"registration_id":       "reg-123",
			"conversation_id":       "conv-456",
			"agent_id":              "agent-0xabc",
			"status":                "failed",
			"declaration_candidate": genesisSectionCandidateProjection(),
			"failure": map[string]any{
				"code":      "soul_bootstrap_restart_required",
				"retryable": true,
				"recovery": map[string]any{
					"action":              "restart_soul_bootstrap",
					"reason":              "Host checkpoint expired before publication",
					"max_attempts":        1,
					"retry_after_seconds": 5,
				},
			},
		},
	}
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	data := structuredGenesisData(t, result)
	recovery := nestedMap(data, "conversation", "failure", "recovery")
	if recovery["reason"] != "Host checkpoint expired before publication" {
		t.Fatalf("recovery = %+v, want safe reason", recovery)
	}
	guidance, _ := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisBegin || guidance["fresh_lane"] != true {
		t.Fatalf("guidance = %+v, want fresh agent_genesis_begin lane", guidance)
	}
	if !strings.Contains(result.Content[0].Text, "Do not call agent_genesis_recover") {
		t.Fatalf("restart guidance should explicitly say recover is not the restart path: %s", result.Content[0].Text)
	}

	unsafe := sanitizeGenesisFailure(map[string]any{"recovery": map[string]any{"action": "retry_same_step", "reason": "private wallet signature leaked"}})
	unsafeRecovery, _ := unsafe["recovery"].(map[string]any)
	if _, ok := unsafeRecovery["reason"]; ok {
		t.Fatalf("unsafe recovery reason was not dropped: %+v", unsafeRecovery)
	}
}

func TestGenesisRetrySameStepRuntimePayloadGuidance(t *testing.T) {
	raw := map[string]any{
		"conversation": map[string]any{
			"registration_id":       "Qh6JQOmy0KXO0bm5XNXsyg",
			"conversation_id":       "K-JYArykVuog3gq-2lHBJw",
			"status":                "failed",
			"declaration_candidate": genesisSectionCandidateProjection(),
			"failure": map[string]any{
				"code":      "microvm_unavailable",
				"retryable": true,
				"recovery": map[string]any{
					"action":              "retry_same_step",
					"max_attempts":        3,
					"retry_after_seconds": 5,
					"reason":              "microvm_unavailable",
				},
			},
		},
	}
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	data := structuredGenesisData(t, result)
	if data["status"] != "failed" {
		t.Fatalf("runtime payload status = %v, want failed", data["status"])
	}
	recovery := nestedMap(data, "conversation", "failure", "recovery")
	if recovery["action"] != "retry_same_step" || recovery["max_attempts"] != 3 || recovery["retry_after_seconds"] != 5 {
		t.Fatalf("runtime recovery projection = %+v", recovery)
	}
	guidance := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisRecover || guidance["fresh_lane"] != false {
		t.Fatalf("retry_same_step guidance = %+v, want recover on the same lane", guidance)
	}
	if guidance["poll_after_seconds"] != 5 || guidance["expected_wait_seconds"] != 5 || guidance["wait"] != true {
		t.Fatalf("retry_same_step delay guidance = %+v, want bounded 5-second wait", guidance)
	}
	instruction, _ := guidance["instruction"].(string)
	for _, want := range []string{"retry_same_step", "wait retry_after_seconds=5 seconds", "call " + toolAgentGenesisRecover + " exactly once"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("retry_same_step instruction missing %q: %s", want, instruction)
		}
	}
	if strings.Contains(instruction, "Poll "+toolAgentGenesisRead) {
		t.Fatalf("retry_same_step instruction still loops on read: %s", instruction)
	}
	if !strings.Contains(result.Content[0].Text, toolAgentGenesisRecover) || !strings.Contains(result.Content[0].Text, "retry_after_seconds=5") {
		t.Fatalf("MCP-visible runtime guidance missing recover/delay: %s", result.Content[0].Text)
	}
}

func TestGenesisHostRecoveryActionsMapDeterministically(t *testing.T) {
	cases := []struct {
		action               string
		wantNextTool         string
		wantFreshLane        bool
		wantInstructionParts []string
		wantNoNextTool       bool
	}{
		{
			action:               "retry_same_step",
			wantNextTool:         toolAgentGenesisRecover,
			wantInstructionParts: []string{"wait retry_after_seconds=5 seconds", "call " + toolAgentGenesisRecover + " exactly once"},
		},
		{
			action:               "restart_soul_bootstrap",
			wantNextTool:         toolAgentGenesisBegin,
			wantFreshLane:        true,
			wantInstructionParts: []string{"fresh genesis lane", "Do not call " + toolAgentGenesisRecover},
		},
		{
			action:               "refresh_state",
			wantNextTool:         toolAgentGenesisRead,
			wantInstructionParts: []string{"refresh_state", "call " + toolAgentGenesisRead + " exactly once", "Do not call a Genesis write tool"},
		},
		{
			action:               "operator_action",
			wantNoNextTool:       true,
			wantInstructionParts: []string{"operator_action", "contact the instance operator", "Stop automatic Genesis tool calls"},
		},
	}
	shapes := []struct {
		name string
		raw  func(map[string]any) map[string]any
	}{
		{
			name: "nested_conversation_failure",
			raw: func(failure map[string]any) map[string]any {
				return map[string]any{"conversation": map[string]any{"status": "failed", "failure": failure, "declaration_candidate": genesisSectionCandidateProjection()}}
			},
		},
		{
			name: "top_level_failure",
			raw: func(failure map[string]any) map[string]any {
				return map[string]any{"status": "failed", "failure": failure}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		for _, shape := range shapes {
			shape := shape
			t.Run(tc.action+"/"+shape.name, func(t *testing.T) {
				failure := map[string]any{
					"code":      "microvm_unavailable",
					"retryable": tc.action == "retry_same_step",
					"recovery": map[string]any{
						"action":              tc.action,
						"max_attempts":        3,
						"retry_after_seconds": 5,
						"reason":              "microvm_unavailable",
					},
				}
				result, err := genesisSuccessResult(toolAgentGenesisRead, "read", shape.raw(failure))
				if err != nil {
					t.Fatalf("genesisSuccessResult: %v", err)
				}
				data := structuredGenesisData(t, result)
				recovery := nestedMap(data, "conversation", "failure", "recovery")
				if recovery["action"] != tc.action {
					t.Fatalf("Host recovery action was normalized: %+v", recovery)
				}
				guidance := data["guidance"].(map[string]any)
				nextTool, hasNextTool := guidance["next_tool"]
				if tc.wantNoNextTool {
					if hasNextTool {
						t.Fatalf("%s guidance must not choose an automatic tool: %+v", tc.action, guidance)
					}
				} else if nextTool != tc.wantNextTool {
					t.Fatalf("%s next_tool = %v, want %s: %+v", tc.action, nextTool, tc.wantNextTool, guidance)
				}
				if guidance["fresh_lane"] != tc.wantFreshLane {
					t.Fatalf("%s fresh_lane = %v, want %t", tc.action, guidance["fresh_lane"], tc.wantFreshLane)
				}
				instruction, _ := guidance["instruction"].(string)
				for _, want := range tc.wantInstructionParts {
					if !strings.Contains(instruction, want) {
						t.Fatalf("%s instruction missing %q: %s", tc.action, want, instruction)
					}
				}
				if tc.action == "operator_action" && strings.Contains(instruction, "Poll "+toolAgentGenesisRead) {
					t.Fatalf("operator_action instruction must not prescribe endless reads: %s", instruction)
				}
			})
		}
	}
}

func TestGenesisUnknownRecoveryActionFailsClosedWithoutNormalization(t *testing.T) {
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", map[string]any{
		"conversation": map[string]any{
			"status":                "failed",
			"declaration_candidate": genesisSectionCandidateProjection(),
			"failure": map[string]any{
				"code": "microvm_unavailable",
				"recovery": map[string]any{
					"action": "future_host_action",
					"reason": "microvm_unavailable",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	data := structuredGenesisData(t, result)
	if got := nestedString(data, "conversation", "failure", "recovery", "action"); got != "future_host_action" {
		t.Fatalf("unknown Host action = %q, want preserved source value", got)
	}
	guidance := data["guidance"].(map[string]any)
	if _, ok := guidance["next_tool"]; ok {
		t.Fatalf("unknown Host action must not select a fallback tool: %+v", guidance)
	}
	instruction, _ := guidance["instruction"].(string)
	for _, want := range []string{"unrecognized failure.recovery.action", "Stop automatic Genesis tool calls", "will not normalize", "contact the instance operator"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("unknown Host action guidance missing %q: %s", want, instruction)
		}
	}
}

func TestGenesisProcessingStatesAreWaitOnlyGuidance(t *testing.T) {
	for _, status := range []string{"in_progress"} {
		status := status
		t.Run(status, func(t *testing.T) {
			raw := map[string]any{
				"conversation": map[string]any{
					"registration_id":       "reg-123",
					"conversation_id":       "conv-456",
					"agent_id":              "agent-123",
					"status":                status,
					"declaration_candidate": genesisSectionCandidateProjection(),
					"poll_after_seconds":    7,
					"progress":              "host_processing",
					"private_transcript":    "must-not-return",
					"wallet_signature":      "must-not-return",
					"producedDeclaration":   "must-not-return",
				},
			}
			result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
			if err != nil {
				t.Fatalf("genesisSuccessResult: %v", err)
			}
			data := structuredGenesisData(t, result)
			conversation := data["conversation"].(map[string]any)
			if conversation["poll_after_seconds"] != 7 {
				t.Fatalf("conversation poll_after_seconds = %#v, want 7", conversation["poll_after_seconds"])
			}
			guidance := data["guidance"].(map[string]any)
			if guidance["next_tool"] != toolAgentGenesisRead {
				t.Fatalf("processing guidance next_tool = %+v, want %s", guidance, toolAgentGenesisRead)
			}
			if guidance["wait"] != true {
				t.Fatalf("processing guidance wait = %+v, want true", guidance)
			}
			if guidance["forbidden_next_tool"] != toolAgentGenesisAdvance {
				t.Fatalf("processing guidance forbidden_next_tool = %+v, want %s", guidance, toolAgentGenesisAdvance)
			}
			if guidance["poll_after_seconds"] != 7 || guidance["expected_wait_seconds"] != 7 {
				t.Fatalf("processing guidance wait fields = %+v, want poll/expected wait 7", guidance)
			}
			if guidance["progress"] != "host_processing" {
				t.Fatalf("processing guidance progress = %+v", guidance)
			}
			instruction, _ := guidance["instruction"].(string)
			for _, want := range []string{
				"Host is processing",
				"Do not call " + toolAgentGenesisAdvance + " again",
				"do not nudge",
				"wait poll_after_seconds=7 seconds",
				"then call " + toolAgentGenesisRead,
				"Only call " + toolAgentGenesisAdvance + " after Host reports assistant_turn_ready",
			} {
				if !strings.Contains(instruction, want) {
					t.Fatalf("processing instruction missing %q: %s", want, instruction)
				}
			}
			if strings.Contains(instruction, "Continue the Host-owned mint conversation with "+toolAgentGenesisAdvance) {
				t.Fatalf("processing instruction still suggests advance: %s", instruction)
			}
			visible := result.Content[0].Text
			if !strings.Contains(visible, "Do not call "+toolAgentGenesisAdvance+" again") || !strings.Contains(visible, "do not nudge") || !strings.Contains(visible, "wait poll_after_seconds=7 seconds") {
				t.Fatalf("visible processing guidance missing no-nudge wait text: %s", visible)
			}
			encoded := mustMarshalGenesisResult(t, result)
			for _, forbidden := range []string{"must-not-return", "private_transcript", "wallet_signature", "producedDeclaration"} {
				if strings.Contains(encoded, forbidden) {
					t.Fatalf("processing result leaked forbidden marker %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestGenesisOwnerInputStatesStillAdvanceGuidance(t *testing.T) {
	for _, status := range []string{"assistant_turn_ready"} {
		status := status
		t.Run(status, func(t *testing.T) {
			result, err := genesisSuccessResult(toolAgentGenesisRead, "read", genesisConversationResponse(status, "private-transcript-must-not-return"))
			if err != nil {
				t.Fatalf("genesisSuccessResult: %v", err)
			}
			guidance := structuredGenesisData(t, result)["guidance"].(map[string]any)
			if guidance["next_tool"] != toolAgentGenesisAdvance {
				t.Fatalf("owner-input guidance = %+v, want %s", guidance, toolAgentGenesisAdvance)
			}
			if guidance["wait"] == true || guidance["forbidden_next_tool"] == toolAgentGenesisAdvance {
				t.Fatalf("owner-input guidance should not be wait-only: %+v", guidance)
			}
			instruction, _ := guidance["instruction"].(string)
			if !strings.Contains(instruction, "candidate phase is section") || !strings.Contains(instruction, toolAgentGenesisAdvance) {
				t.Fatalf("owner-input instruction = %q", instruction)
			}
		})
	}
}

func TestGenesisDeclarationReadyGuidanceUsesSuccessfulOperation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		toolName  string
		operation string
		wantTool  string
	}{
		{name: "read", toolName: toolAgentGenesisRead, operation: "read", wantTool: toolAgentGenesisFinalizePreflight},
		{name: "finalize_preflight", toolName: toolAgentGenesisFinalizePreflight, operation: "finalize_preflight", wantTool: toolAgentGenesisFinalize},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := genesisSuccessResult(tc.toolName, tc.operation, genesisConversationResponse("declaration_ready", "private-transcript-must-not-return"))
			if err != nil {
				t.Fatalf("genesisSuccessResult: %v", err)
			}
			guidance := structuredGenesisData(t, result)["guidance"].(map[string]any)
			if guidance["next_tool"] != tc.wantTool {
				t.Fatalf("%s declaration_ready guidance = %+v, want %s", tc.operation, guidance, tc.wantTool)
			}
			if !strings.Contains(result.Content[0].Text, tc.wantTool) {
				t.Fatalf("%s MCP-visible guidance missing %s: %s", tc.operation, tc.wantTool, result.Content[0].Text)
			}
		})
	}
}

func TestAgentGenesisAdvanceRelaysStructuralCandidateActionUnchanged(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	fake := &fakeGenesisClient{advanceResponse: genesisCandidateConversationResponse("review", "exact owner review")}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	candidateHash := "sha256:" + strings.Repeat("a", 64)
	reviewText := "exact owner review"
	reviewHash := "sha256:" + sha256Hex([]byte(reviewText))
	args := `{"registration_id":"reg-123","conversation_id":"conv-456","message":"Revise the boundaries refusal floor.","candidate_action":{"action":"edit","section":"boundaries","candidate_revision":9,"candidate_hash":"` + candidateHash + `","review_hash":"` + reviewHash + `"}}`
	callGenesisTool(t, registry, operatorToolContext("owner", []string{"write"}, "owner-token"), toolAgentGenesisAdvance, args)
	got := fake.advanceRequest.CandidateAction
	if got == nil || got.Action != "edit" || got.Section != "boundaries" || got.CandidateRevision != 9 || got.CandidateHash != candidateHash || got.ReviewHash != reviewHash {
		t.Fatalf("Host candidate_action changed: %#v", got)
	}
	affirmArgs := `{"registration_id":"reg-123","conversation_id":"conv-456","message":"Owner decision recorded structurally.","candidate_action":{"action":"affirm","candidate_revision":9,"candidate_hash":"` + candidateHash + `","review_hash":"` + reviewHash + `"}}`
	callGenesisTool(t, registry, operatorToolContext("owner", []string{"write"}, "owner-token"), toolAgentGenesisAdvance, affirmArgs)
	got = fake.advanceRequest.CandidateAction
	if got == nil || got.Action != "affirm" || got.Section != "" || got.CandidateRevision != 9 || got.CandidateHash != candidateHash || got.ReviewHash != reviewHash {
		t.Fatalf("Host affirm candidate_action changed: %#v", got)
	}
}

func TestAgentGenesisAdvanceRejectsInvalidCandidateActionBeforeHost(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	candidateHash := "sha256:" + strings.Repeat("a", 64)
	reviewHash := "sha256:" + strings.Repeat("b", 64)
	tests := []struct {
		name   string
		action string
	}{
		{name: "null", action: `null`},
		{name: "affirm section forbidden", action: `{"action":"affirm","section":"identity","candidate_revision":1,"candidate_hash":"` + candidateHash + `","review_hash":"` + reviewHash + `"}`},
		{name: "edit section missing", action: `{"action":"edit","candidate_revision":1,"candidate_hash":"` + candidateHash + `","review_hash":"` + reviewHash + `"}`},
		{name: "unknown field", action: `{"action":"affirm","candidate_revision":1,"candidate_hash":"` + candidateHash + `","review_hash":"` + reviewHash + `","legacy":true}`},
		{name: "malformed hash", action: `{"action":"affirm","candidate_revision":1,"candidate_hash":"sha256:bad","review_hash":"` + reviewHash + `"}`},
		{name: "missing binding", action: `{"action":"affirm","candidate_revision":1,"candidate_hash":"` + candidateHash + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeGenesisClient{}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			result, err := registry.Call(operatorToolContext("owner", []string{"write"}, "owner-token"), toolAgentGenesisAdvance, json.RawMessage(`{"registration_id":"reg-123","conversation_id":"conv-456","message":"owner decision","candidate_action":`+test.action+`}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			assertToolError(t, result, "invalid_request", http.StatusBadRequest)
			if len(fake.calls) != 0 {
				t.Fatalf("invalid candidate action reached Host: %v", fake.calls)
			}
		})
	}
}

func TestGenesisCandidateProjectionIsLosslessAndReviewGuidanceIsStructural(t *testing.T) {
	reviewText := strings.Repeat("R", genesisMaxReviewRunes)
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", genesisCandidateConversationResponse("review", reviewText))
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	data := structuredGenesisData(t, result)
	candidate := nestedMap(data, "conversation", "declaration_candidate")
	review := nestedMap(candidate, "review")
	if review["review_text"] != reviewText || utf8.RuneCountInString(review["review_text"].(string)) != genesisMaxReviewRunes {
		t.Fatalf("lossless review length = %d", utf8.RuneCountInString(review["review_text"].(string)))
	}
	if candidate["version"] != "hosted-genesis-declaration-candidate.v1" || candidate["phase"] != "review" || candidate["revision"] != int64(9) || candidate["candidate_hash"] != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("candidate projection = %#v", candidate)
	}
	completed, ok := candidate["completed_sections"].([]any)
	if !ok || len(completed) != 5 {
		t.Fatalf("completed_sections = %#v", candidate["completed_sections"])
	}
	guidance := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisAdvance || guidance["candidate_revision"] != int64(9) || guidance["candidate_hash"] != candidate["candidate_hash"] || guidance["review_hash"] != review["review_hash"] {
		t.Fatalf("review guidance bindings = %#v", guidance)
	}
	actions := guidance["candidate_action"].(map[string]any)
	affirm := actions["affirm"].(map[string]any)
	edit := actions["edit"].(map[string]any)
	allowedSections, _ := edit["allowed_sections"].([]any)
	if _, present := affirm["section"]; present || edit["section_required"] != true || len(allowedSections) != 5 {
		t.Fatalf("review action conditionality = %#v", actions)
	}
	instruction := guidance["instruction"].(string)
	for _, want := range []string{"exact lossless", "affirm forbids section", "edit requires one exact section", "phrases have zero authority"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("review guidance missing %q: %s", want, instruction)
		}
	}
	if strings.Contains(result.Content[0].Text, reviewText[:256]) {
		t.Fatal("full review leaked into text content instead of StructuredContent")
	}
}

func TestGenesisCandidateProjectionFailsClosed(t *testing.T) {
	t.Run("missing candidate", func(t *testing.T) {
		raw := genesisCandidateConversationResponse("review", "exact owner review")
		delete(nestedMap(raw, "conversation"), "declaration_candidate")
		result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
		if err != nil {
			t.Fatalf("genesisSuccessResult: %v", err)
		}
		assertToolError(t, result, "host_genesis_projection_invalid", http.StatusBadGateway)
	})
	t.Run("missing status", func(t *testing.T) {
		raw := genesisCandidateConversationResponse("review", "exact owner review")
		delete(nestedMap(raw, "conversation"), "status")
		result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
		if err != nil {
			t.Fatalf("genesisSuccessResult: %v", err)
		}
		assertToolError(t, result, "host_genesis_projection_invalid", http.StatusBadGateway)
	})
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "oversize review", mutate: func(c map[string]any) {
			review := c["review"].(map[string]any)
			review["review_text"] = strings.Repeat("x", genesisMaxReviewRunes+1)
			review["review_hash"] = "sha256:" + sha256Hex([]byte(review["review_text"].(string)))
		}},
		{name: "unknown candidate field", mutate: func(c map[string]any) { c["canonical_json"] = `{"secret":true}` }},
		{name: "unknown review field", mutate: func(c map[string]any) { c["review"].(map[string]any)["provider_payload"] = "secret" }},
		{name: "malformed candidate hash", mutate: func(c map[string]any) { c["candidate_hash"] = "sha256:BAD" }},
		{name: "revision binding mismatch", mutate: func(c map[string]any) { c["review"].(map[string]any)["candidate_revision"] = float64(8) }},
		{name: "candidate hash binding mismatch", mutate: func(c map[string]any) {
			c["review"].(map[string]any)["candidate_hash"] = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "review hash mismatch", mutate: func(c map[string]any) {
			c["review"].(map[string]any)["review_hash"] = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "missing review", mutate: func(c map[string]any) { delete(c, "review") }},
		{name: "unknown phase", mutate: func(c map[string]any) { c["phase"] = "legacy" }},
		{name: "null completed sections", mutate: func(c map[string]any) { c["completed_sections"] = nil }},
		{name: "out of order sections", mutate: func(c map[string]any) { c["completed_sections"] = []any{"philosophy", "identity"} }},
		{name: "review current section forbidden", mutate: func(c map[string]any) { c["current_section"] = "soul" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := genesisCandidateConversationResponse("review", "exact owner review")
			candidate := nestedMap(raw, "conversation", "declaration_candidate")
			test.mutate(candidate)
			result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
			if err != nil {
				t.Fatalf("genesisSuccessResult: %v", err)
			}
			payload := assertToolError(t, result, "host_genesis_projection_invalid", http.StatusBadGateway)
			encoded, _ := json.Marshal(payload)
			if strings.Contains(string(encoded), "canonical_json") || strings.Contains(string(encoded), "provider_payload") || strings.Contains(string(encoded), "exact owner review") {
				t.Fatalf("contract error leaked candidate internals: %s", encoded)
			}
		})
	}
}

func TestGenesisCandidateSectionGuidanceUsesNormalOwnerMessage(t *testing.T) {
	raw := genesisConversationResponse("assistant_turn_ready", "private transcript")
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	data := structuredGenesisData(t, result)
	candidate := nestedMap(data, "conversation", "declaration_candidate")
	if candidate["phase"] != "section" || candidate["current_section"] != "identity" {
		t.Fatalf("section candidate = %#v", candidate)
	}
	guidance := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisAdvance {
		t.Fatalf("section guidance = %#v", guidance)
	}
	instruction := guidance["instruction"].(string)
	for _, want := range []string{"candidate phase is section", "normal owner message", "current_section", "AppTheory MicroVM", "never Body-local tools"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("section guidance missing %q: %s", want, instruction)
		}
	}
}

func TestGenesisCandidateSectionRejectsCompletedReviewShape(t *testing.T) {
	raw := genesisConversationResponse("assistant_turn_ready", "private transcript")
	candidate := nestedMap(raw, "conversation", "declaration_candidate")
	candidate["completed_sections"] = []any{"identity", "philosophy", "discipline", "boundaries", "soul"}
	candidate["current_section"] = "soul"
	result, err := genesisSuccessResult(toolAgentGenesisRead, "read", raw)
	if err != nil {
		t.Fatalf("genesisSuccessResult: %v", err)
	}
	assertToolError(t, result, "host_genesis_projection_invalid", http.StatusBadGateway)
}

func genesisCandidateConversationResponse(phase string, reviewText string) map[string]any {
	candidateHash := "sha256:" + strings.Repeat("a", 64)
	reviewHash := "sha256:" + sha256Hex([]byte(reviewText))
	return map[string]any{
		"conversation": map[string]any{
			"registration_id": "reg-123", "conversation_id": "conv-456", "agent_id": "agent-123",
			"status": "assistant_turn_ready", "message_count": float64(2), "request_id": "req-123",
			"messages": []any{map[string]any{"id": "msg_000002", "role": "assistant", "content": "Review the exact candidate.", "order": float64(2)}},
			"declaration_candidate": map[string]any{
				"version": "hosted-genesis-declaration-candidate.v1", "phase": phase,
				"completed_sections": []any{"identity", "philosophy", "discipline", "boundaries", "soul"},
				"revision":           float64(9), "candidate_hash": candidateHash,
				"review": map[string]any{
					"renderer_version": "hosted-genesis-owner-review.v1", "candidate_revision": float64(9),
					"candidate_hash": candidateHash, "review_hash": reviewHash, "review_text": reviewText,
				},
			},
		},
	}
}

func genesisConversationResponse(status string, oldTranscript string) map[string]any {
	candidate := genesisSectionCandidateProjection()
	if status == "declaration_ready" || status == "published" {
		candidate = genesisFinalizedCandidateProjection("finalized owner review")
	}
	return map[string]any{
		"conversation": map[string]any{
			"registration_id":       "reg-123",
			"conversation_id":       "conv-456",
			"agent_id":              "agent-123",
			"status":                status,
			"declaration_candidate": candidate,
			"messages": []any{
				map[string]any{"id": "m-old", "role": "user", "content": oldTranscript},
				map[string]any{"id": "m-new", "role": "assistant", "content": "The latest Host genesis turn."},
			},
		},
	}
}

func genesisSectionCandidateProjection() map[string]any {
	return map[string]any{
		"version": "hosted-genesis-declaration-candidate.v1",
		"phase":   "section", "current_section": "identity", "completed_sections": []any{},
		"revision": float64(0), "candidate_hash": "sha256:" + strings.Repeat("0", 64),
	}
}

func genesisFinalizedCandidateProjection(reviewText string) map[string]any {
	candidateHash := "sha256:" + strings.Repeat("a", 64)
	return map[string]any{
		"version": "hosted-genesis-declaration-candidate.v1", "phase": "finalized",
		"completed_sections": []any{"identity", "philosophy", "discipline", "boundaries", "soul"},
		"revision":           float64(9), "candidate_hash": candidateHash,
		"review": map[string]any{
			"renderer_version": "hosted-genesis-owner-review.v1", "candidate_revision": float64(9),
			"candidate_hash": candidateHash, "review_hash": "sha256:" + sha256Hex([]byte(reviewText)), "review_text": reviewText,
		},
	}
}

func callGenesisTool(t *testing.T, registry *mcpruntime.ToolRegistry, ctx context.Context, name string, args string) *mcpruntime.ToolResult {
	t.Helper()
	result, err := registry.Call(ctx, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result == nil || result.IsError {
		t.Fatalf("%s returned error result: %+v", name, result)
	}
	return result
}

func structuredGenesisData(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatal("genesis result has no structured content")
	}
	data, ok := result.StructuredContent["data"].(map[string]any)
	if !ok {
		t.Fatalf("genesis structured data = %#v", result.StructuredContent)
	}
	return data
}

func structuredErrorCode(t *testing.T, result *mcpruntime.ToolResult) string {
	t.Helper()
	errorData, ok := result.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("structured error = %#v", result.StructuredContent)
	}
	code, _ := errorData["code"].(string)
	return code
}

func mustMarshalGenesisResult(t *testing.T, results ...*mcpruntime.ToolResult) string {
	t.Helper()
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal genesis results: %v", err)
	}
	return string(encoded)
}

var _ hostapi.GenesisClient = (*fakeGenesisClient)(nil)
