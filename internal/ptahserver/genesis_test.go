package ptahserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	completeResponse  map[string]any
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

func (f *fakeGenesisClient) CompleteConversation(_ context.Context, bearer string, registrationID string, conversationID string) (map[string]any, error) {
	f.bearer = bearer
	f.calls = append(f.calls, "complete:"+registrationID+":"+conversationID)
	f.registrationID = registrationID
	f.conversationID = conversationID
	return f.completeResponse, nil
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

func TestGenesisListRecommendationsCoverCompletionAndFinalizationStates(t *testing.T) {
	const agentID = "0xagent"
	conversations := sanitizeGenesisConversationSummaries([]any{
		map[string]any{"registration_id": "reg-ready", "conversation_id": "conv-ready", "status": "declaration_ready", "message_count": 2},
		map[string]any{"registration_id": "reg-preflight", "conversation_id": "conv-preflight", "status": "finalization_ready", "message_count": 3},
		map[string]any{"registration_id": "reg-finalize", "conversation_id": "conv-finalize", "status": "ready_to_finalize", "message_count": 4},
		map[string]any{"registration_id": "reg-complete", "conversation_id": "conv-complete", "status": "complete", "message_count": 5},
	}, agentID)
	if len(conversations) != 4 {
		t.Fatalf("conversations = %+v", conversations)
	}
	if conversations[0]["recommended_next_tool"] != toolAgentGenesisComplete {
		t.Fatalf("declaration_ready recommendation = %+v", conversations[0])
	}
	if conversations[1]["recommended_next_tool"] != toolAgentGenesisFinalizePreflight {
		t.Fatalf("finalization_ready recommendation = %+v", conversations[1])
	}
	if conversations[2]["recommended_next_tool"] != toolAgentGenesisFinalize {
		t.Fatalf("ready_to_finalize recommendation = %+v", conversations[2])
	}
	if conversations[3]["terminal"] != true || conversations[3]["recommended_next_tool"] != toolAgentGet || conversations[3]["alternate_next_tool"] != toolAgentList {
		t.Fatalf("complete terminal recommendation = %+v", conversations[3])
	}
	start := genesisListStartHere(conversations, agentID)
	if start["recommended_next_tool"] != toolAgentGenesisComplete || start["registration_id"] != "reg-ready" {
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
		completeResponse: map[string]any{
			"conversation": map[string]any{
				"registration_id": "reg-123",
				"conversation_id": "conv-456",
				"agent_id":        "agent-123",
				"status":          "declaration_ready",
				"messages": []any{
					map[string]any{"role": "user", "content": oldTranscript},
					map[string]any{"role": "assistant", "content": "The declaration checkpoint is ready."},
				},
				"produced_declarations": map[string]any{
					"declaration_id":   "decl-123",
					"declaration":      privateDeclare,
					"declaration_hash": "sha256:declaration",
				},
			},
		},
		preflightResponse: map[string]any{
			"conversation":        map[string]any{"registration_id": "reg-123", "conversation_id": "conv-456", "status": "declaration_ready"},
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
	complete := callGenesisTool(t, registry, ctx, toolAgentGenesisComplete, `{"registration_id":"reg-123","conversation_id":"conv-456"}`)
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
		"complete:reg-123:conv-456",
		"preflight:reg-123:conv-456",
		"finalize:reg-123:conv-456",
	}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("Host state-machine calls = %v, want %v", fake.calls, wantCalls)
	}

	encoded := mustMarshalGenesisResult(t, begin, advance, read, recover, complete, preflight, finalize)
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
				"registration_id": "reg-123",
				"conversation_id": "conv-456",
				"agent_id":        "agent-0xabc",
				"status":          "published",
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
			"registration_id": "reg-123",
			"conversation_id": "conv-456",
			"agent_id":        "agent-0xabc",
			"status":          "failed",
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

	unsafe := sanitizeGenesisFailure(map[string]any{"recovery": map[string]any{"action": "retry", "reason": "private wallet signature leaked"}})
	unsafeRecovery, _ := unsafe["recovery"].(map[string]any)
	if _, ok := unsafeRecovery["reason"]; ok {
		t.Fatalf("unsafe recovery reason was not dropped: %+v", unsafeRecovery)
	}
}

func TestGenesisProcessingStatesAreWaitOnlyGuidance(t *testing.T) {
	for _, status := range []string{"in_progress", "declaration_extraction_pending"} {
		status := status
		t.Run(status, func(t *testing.T) {
			raw := map[string]any{
				"conversation": map[string]any{
					"registration_id":     "reg-123",
					"conversation_id":     "conv-456",
					"agent_id":            "agent-123",
					"status":              status,
					"poll_after_seconds":  7,
					"progress":            "host_processing",
					"private_transcript":  "must-not-return",
					"wallet_signature":    "must-not-return",
					"producedDeclaration": "must-not-return",
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
				"Only call " + toolAgentGenesisAdvance + " after Host reports assistant_turn_ready, awaiting_owner, or needs_owner_turn",
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
	for _, status := range []string{"assistant_turn_ready", "awaiting_owner", "needs_owner_turn"} {
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
			if !strings.Contains(instruction, "Host is waiting for owner/operator input") || !strings.Contains(instruction, toolAgentGenesisAdvance) {
				t.Fatalf("owner-input instruction = %q", instruction)
			}
		})
	}
}

func genesisConversationResponse(status string, oldTranscript string) map[string]any {
	return map[string]any{
		"conversation": map[string]any{
			"registration_id": "reg-123",
			"conversation_id": "conv-456",
			"agent_id":        "agent-123",
			"status":          status,
			"messages": []any{
				map[string]any{"id": "m-old", "role": "user", "content": oldTranscript},
				map[string]any{"id": "m-new", "role": "assistant", "content": "The latest Host genesis turn."},
			},
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
