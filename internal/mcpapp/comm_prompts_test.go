package mcpapp_test

import (
	"encoding/json"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestLBM4_CommunicationPromptsExistAndReferencePreferences(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
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
}
