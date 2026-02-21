package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/memory"
)

func TestM9_ResourcesAndPrompts(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			_, _ = w.Write([]byte(`{"id":"acct1","username":"agent1"}`))
		case "/api/v1/timelines/home":
			_, _ = w.Write([]byte(`[{"id":"t1"}]`))
		case "/api/v1/timelines/public":
			_, _ = w.Write([]byte(`[{"id":"t2"}]`))
		case "/api/v1/notifications":
			_, _ = w.Write([]byte(`[{"id":"n1"}]`))
		case "/api/v1/accounts/acct1/followers":
			_, _ = w.Write([]byte(`[{"id":"f1"}]`))
		case "/api/v1/accounts/acct1/following":
			_, _ = w.Write([]byte(`[{"id":"g1"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"write"})
	authHeader := "Bearer " + token

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	// initialize should advertise resources + prompts
	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	var initRPC mcpruntime.Response
	_ = json.Unmarshal(initResp.Body, &initRPC)
	var initOut struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	{
		b, _ := json.Marshal(initRPC.Result)
		_ = json.Unmarshal(b, &initOut)
	}
	if _, ok := initOut.Capabilities["resources"]; !ok {
		t.Fatalf("expected initialize capabilities.resources")
	}
	if _, ok := initOut.Capabilities["prompts"]; !ok {
		t.Fatalf("expected initialize capabilities.prompts")
	}

	// Seed memory store so agent://memory/recent has content.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "memory_append",
			"arguments": map[string]any{
				"content":  "hello",
				"event_id": "01JMY4Y6A00000000000000000",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("memory_append: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}

	// resources/list should include at least profile + memory/recent
	{
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "resources/list"})
		if resp.Status != 200 {
			t.Fatalf("resources/list: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("resources/list error: %+v", rpc.Error)
		}

		var out struct {
			Resources []mcpruntime.ResourceDef `json:"resources"`
		}
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		have := map[string]bool{}
		for _, r := range out.Resources {
			have[r.URI] = true
		}
		if !have["agent://profile"] || !have["agent://memory/recent"] {
			t.Fatalf("expected agent://profile and agent://memory/recent, got %+v", out.Resources)
		}
	}

	// resources/read (profile) should proxy to Lesser API with Authorization header.
	{
		gotAuth = ""
		params, _ := json.Marshal(map[string]any{"uri": "agent://profile"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotAuth != "Bearer "+token {
			t.Fatalf("expected upstream auth header, got %q", gotAuth)
		}
	}

	// resources/read (memory) should return seeded event.
	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://memory/recent"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read memory: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("resources/read error: %+v", rpc.Error)
		}
		var out struct {
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
		}
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if len(out.Contents) != 1 || out.Contents[0].Text == "" {
			t.Fatalf("unexpected resource contents: %+v", out.Contents)
		}
		if !json.Valid([]byte(out.Contents[0].Text)) {
			t.Fatalf("expected JSON text in contents, got %q", out.Contents[0].Text)
		}
	}

	// prompts/list + prompts/get should work for compose_post
	{
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 6, Method: "prompts/list"})
		if resp.Status != 200 {
			t.Fatalf("prompts/list: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("prompts/list error: %+v", rpc.Error)
		}
	}

	{
		params, _ := json.Marshal(map[string]any{
			"name": "compose_post",
			"arguments": map[string]any{
				"topic": "test",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 7, Method: "prompts/get", Params: params})
		if resp.Status != 200 {
			t.Fatalf("prompts/get: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("prompts/get error: %+v", rpc.Error)
		}
	}
}
