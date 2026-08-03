package mcpapp_test

import (
	"encoding/json"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestLBM4_CommunicationPromptsExistAndReferencePreferences(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	listResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "prompts/list"})
	if listResp.Status != 200 {
		t.Fatalf("prompts/list: status=%d body=%s", listResp.Status, string(listResp.Body))
	}
	var rpcList mcpruntime.Response
	_ = json.Unmarshal(listResp.Body, &rpcList)
	if rpcList.Error != nil {
		t.Fatalf("prompts/list error: %+v", rpcList.Error)
	}
	var out struct {
		Prompts []mcpruntime.PromptDef `json:"prompts"`
	}
	{
		b, _ := json.Marshal(rpcList.Result)
		_ = json.Unmarshal(b, &out)
	}
	have := map[string]bool{}
	for _, p := range out.Prompts {
		have[p.Name] = true
	}
	for _, name := range []string{"compose_email", "handle_inbound", "respect_preferences"} {
		if !have[name] {
			t.Fatalf("expected prompt %q in prompts/list", name)
		}
	}

	getParams, _ := json.Marshal(map[string]any{
		"name":      "compose_email",
		"arguments": map[string]any{"to": "alice@example.com"},
	})
	getResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "prompts/get", Params: getParams})
	if getResp.Status != 200 {
		t.Fatalf("prompts/get: status=%d body=%s", getResp.Status, string(getResp.Body))
	}
	var rpcGet mcpruntime.Response
	_ = json.Unmarshal(getResp.Body, &rpcGet)
	if rpcGet.Error != nil {
		t.Fatalf("prompts/get error: %+v", rpcGet.Error)
	}
	var prompt mcpruntime.PromptResult
	{
		b, _ := json.Marshal(rpcGet.Result)
		_ = json.Unmarshal(b, &prompt)
	}
	combined := ""
	for _, m := range prompt.Messages {
		combined += "\n" + m.Content.Text
	}
	if !strings.Contains(combined, "identity_whoami") || !strings.Contains(combined, "agent://channels/preferences") {
		t.Fatalf("expected compose_email prompt to reference identity_whoami and agent://channels/preferences, got: %s", combined)
	}
	if !strings.Contains(combined, "current-instance local ID") || !strings.Contains(combined, "remote ActivityPub handle") {
		t.Fatalf("expected compose_email prompt to describe supported identity_lookup query forms, got: %s", combined)
	}

	getParams, _ = json.Marshal(map[string]any{
		"name":      "handle_inbound",
		"arguments": map[string]any{"channel": "sms", "messageId": "comm-msg-123"},
	})
	getResp = invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "prompts/get", Params: getParams})
	if getResp.Status != 200 {
		t.Fatalf("prompts/get handle_inbound: status=%d body=%s", getResp.Status, string(getResp.Body))
	}
	_ = json.Unmarshal(getResp.Body, &rpcGet)
	if rpcGet.Error != nil {
		t.Fatalf("prompts/get handle_inbound error: %+v", rpcGet.Error)
	}
	{
		b, _ := json.Marshal(rpcGet.Result)
		_ = json.Unmarshal(b, &prompt)
	}
	combined = ""
	for _, m := range prompt.Messages {
		combined += "\n" + m.Content.Text
	}
	if !strings.Contains(combined, "Pass messageId=comm-msg-123") {
		t.Fatalf("expected handle_inbound prompt to instruct reply threading, got: %s", combined)
	}

	getParams, _ = json.Marshal(map[string]any{
		"name":      "handle_inbound",
		"arguments": map[string]any{"channel": "voice", "messageId": "comm-msg-voice-123"},
	})
	getResp = invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "prompts/get", Params: getParams})
	if getResp.Status != 200 {
		t.Fatalf("prompts/get voice handle_inbound: status=%d body=%s", getResp.Status, string(getResp.Body))
	}
	_ = json.Unmarshal(getResp.Body, &rpcGet)
	if rpcGet.Error != nil {
		t.Fatalf("prompts/get voice handle_inbound error: %+v", rpcGet.Error)
	}
	{
		b, _ := json.Marshal(rpcGet.Result)
		_ = json.Unmarshal(b, &prompt)
	}
	combined = ""
	for _, m := range prompt.Messages {
		combined += "\n" + m.Content.Text
	}
	if strings.Contains(combined, "phone_call") {
		t.Fatalf("voice inbound prompt should not reference phone_call, got: %s", combined)
	}
	if !strings.Contains(combined, "outbound voice call") {
		t.Fatalf("voice inbound prompt should explain outbound voice is disabled, got: %s", combined)
	}

	getParams, _ = json.Marshal(map[string]any{
		"name":      "respect_preferences",
		"arguments": map[string]any{"query": "medic"},
	})
	getResp = invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 6, Method: "prompts/get", Params: getParams})
	if getResp.Status != 200 {
		t.Fatalf("prompts/get respect_preferences: status=%d body=%s", getResp.Status, string(getResp.Body))
	}
	_ = json.Unmarshal(getResp.Body, &rpcGet)
	if rpcGet.Error != nil {
		t.Fatalf("prompts/get respect_preferences error: %+v", rpcGet.Error)
	}
	{
		b, _ := json.Marshal(rpcGet.Result)
		_ = json.Unmarshal(b, &prompt)
	}
	combined = ""
	for _, m := range prompt.Messages {
		combined += "\n" + m.Content.Text
	}
	if !strings.Contains(combined, "current-instance local ID") || !strings.Contains(combined, "remote ActivityPub handle") || !strings.Contains(combined, "canonical actor URL") {
		t.Fatalf("expected respect_preferences prompt to describe supported identity_lookup query forms, got: %s", combined)
	}
}
