package mcpapp_test

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

// mediaToolNames is the seven-tool editorial media surface. The registration
// test in mcpserver (TestMediaToolsRegisterAndClassify) bypasses the runtime
// policy filter, so these tests re-drive tools/list and the .well-known/mcp.json
// hint list through mcpapp.New — the same filtered path clients actually use.
var mediaToolNames = []string{
	"upload_grant_mint",
	"upload_finalize",
	"media_state",
	"media_read",
	"draft_media_attach",
	"draft_media_detach",
	"draft_media_reorder",
}

func TestMediaToolsVisibleInSouledRuntimeSurface(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "agent-1")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	t.Run("tools/list keeps media tools for the souled profile", func(t *testing.T) {
		have := listToolNames(t, env, app, authHeader, sessionID)
		for _, name := range mediaToolNames {
			if !have[name] {
				t.Fatalf("media tool %q missing from souled tools/list (filtered path)", name)
			}
		}
	})

	t.Run("well-known hint list includes media tools", func(t *testing.T) {
		// Assertion strength: souled membership ONLY. The .well-known/mcp.json
		// hint list is filtered through the hardcoded ProfileSouled allowlist
		// (mcpapp/well_known.go: ToolAllowed(ProfileSouled, ...)), so this
		// subtest proves each media tool passes the souled profile — it is not
		// drone coverage and not per-actor filtering. The .well-known surface is
		// intentionally a static hint surface; per-actor filtering happens at
		// tools/list, whose drone leg is asserted by
		// TestMediaToolsVisibleInDroneRuntimeSurface below. Do not mistake this
		// subtest for a drone assertion.
		resp := env.Invoke(context.Background(), app, apptheory.Request{
			Method: "GET",
			Path:   "/.well-known/mcp.json",
		})
		if resp.Status != 200 {
			t.Fatalf("GET /.well-known/mcp.json: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var payload struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(resp.Body, &payload); err != nil {
			t.Fatalf("unmarshal mcp.json: %v", err)
		}
		hints := map[string]bool{}
		for _, tool := range payload.Tools {
			hints[tool.Name] = true
		}
		for _, name := range mediaToolNames {
			if !hints[name] {
				t.Fatalf("media tool %q missing from .well-known/mcp.json hints", name)
			}
		}
	})
}

func TestMediaToolsVisibleInDroneRuntimeSurface(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	// No soul binding: the actor resolves to the drone profile.
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	t.Run("tools/list keeps media tools for the drone profile", func(t *testing.T) {
		have := listToolNames(t, env, app, authHeader, sessionID)
		for _, name := range mediaToolNames {
			if !have[name] {
				t.Fatalf("media tool %q missing from drone tools/list (filtered path)", name)
			}
		}
	})
}

// listToolNames drives tools/list through the app and returns the set of tool
// names as actually filtered by the runtime policy middleware.
func listToolNames(t *testing.T, env *testkit.Env, app *apptheory.App, authHeader, sessionID string) map[string]bool {
	t.Helper()
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if resp.Status != 200 {
		t.Fatalf("tools/list: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("tools/list error: %+v", rpc.Error)
	}
	var out struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	{
		b, _ := json.Marshal(rpc.Result)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal tools: %v", err)
		}
	}
	have := map[string]bool{}
	for _, tool := range out.Tools {
		have[tool.Name] = true
	}
	return have
}
