package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestLBM1_IdentityToolsAndChannelResources(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const tokenUser = "agent1"

	var gotMineAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/soul/agents/mine":
			gotMineAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{
				"agents":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}],
				"count":1
			}`))
		case r.URL.Path == "/api/v1/soul/search":
			q := strings.TrimSpace(r.URL.Query().Get("q"))
			if q == "agent-alice" {
				_, _ = w.Write([]byte(`{"version":"1","results":[{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"}],"count":1,"has_more":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"version":"1","results":[],"count":0,"has_more":false}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{
				"version":"3",
				"channels":{
					"ens":{"name":"agent-alice.lessersoul.eth"},
					"email":{"address":"agent-alice@lessersoul.ai","capabilities":["receive","send"],"verified":true},
					"phone":{"number":"+15550142","capabilities":["sms-receive"],"verified":false}
				},
				"contactPreferences":{
					"preferred":"email",
					"availability":{"schedule":"always"},
					"responseExpectation":{"target":"1h","guarantee":"best-effort"},
					"languages":["en"]
				}
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", tokenUser, []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	// identity_whoami should call /agents/mine with our bearer token and return channels + preferences.
	{
		gotMineAuth = ""
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_whoami",
			"arguments": map[string]any{},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_whoami: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotMineAuth != authHeader {
			t.Fatalf("expected soul api Authorization=%q, got %q", authHeader, gotMineAuth)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_whoami rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["agentId"] != agentID {
			t.Fatalf("agentId: want %s got %v", agentID, data["agentId"])
		}
		ch, _ := data["channels"].(map[string]any)
		if ch == nil {
			t.Fatalf("expected channels object")
		}
		email, _ := ch["email"].(map[string]any)
		if email["address"] != "agent-alice@lessersoul.ai" {
			t.Fatalf("unexpected email channel: %+v", email)
		}
		prefs, _ := data["contactPreferences"].(map[string]any)
		if prefs["preferred"] != "email" {
			t.Fatalf("unexpected contactPreferences: %+v", prefs)
		}
	}

	// identity_lookup should resolve managed email/ENS by deriving localId and searching.
	for _, q := range []string{"agent-alice@lessersoul.ai", "agent-alice.lessersoul.eth"} {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": q},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(%q): status=%d body=%s", q, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_lookup(%q) rpc error: %+v", q, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		matches, _ := data["matches"].([]any)
		if len(matches) != 1 {
			t.Fatalf("identity_lookup(%q): expected 1 match, got %+v", q, matches)
		}
		match, _ := matches[0].(map[string]any)
		if match["agentId"] != agentID {
			t.Fatalf("identity_lookup(%q): agentId want %s got %v", q, agentID, match["agentId"])
		}
	}

	// agent://channels + agent://channels/preferences should read successfully.
	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://channels"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read channels: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}

	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://channels/preferences"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read channel preferences: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}
}
