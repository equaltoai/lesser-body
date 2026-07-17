package ptahserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/hostapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

type fakeGenesisClient struct {
	bearer string
	calls  []string

	beginRequest      hostapi.RegistrationBeginRequest
	advanceRequest    hostapi.MintConversationRequest
	registrationID    string
	conversationID    string
	beginResponse     map[string]any
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

func TestGenesisRejectsOrdinaryWriteOAuthWithoutCallingHost(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	fake := &fakeGenesisClient{}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("owner", []string{"read", "write"}, "ordinary-write-bearer"), toolAgentGenesisBegin, json.RawMessage(`{
		"domain":"example.com",
		"local_id":"new-agent"
	}`))
	if err != nil {
		t.Fatalf("genesis call: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("ordinary write token should be rejected: %+v", result)
	}
	if got := structuredErrorCode(t, result); got != "owner_operator_required" {
		t.Fatalf("error code = %q, want owner_operator_required", got)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("ordinary write token reached Host: %v", fake.calls)
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

	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("owner", []string{"read", "write"}, callerBearer)

	begin := callGenesisTool(t, registry, ctx, toolAgentGenesisBegin, `{"domain":"example.com","local_id":"new-agent","capabilities":["post"]}`)
	beginData := structuredGenesisData(t, begin)
	if beginData["source"] != "lesser_host" || beginData["state_authority"] != "Host HostedGenesisSession" || beginData["flow"] != "genesis_conversation" {
		t.Fatalf("begin source/authority = %#v", beginData)
	}
	if existing, ok := beginData["existing_agent_create"].(bool); !ok || existing {
		t.Fatalf("genesis flow incorrectly reports existing-agent delegation: %#v", beginData["existing_agent_create"])
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
