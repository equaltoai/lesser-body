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

// promoToolNames is the six-tool promo package surface. The registration test
// in mcpserver (TestPromoToolsRegisterAndClassify) bypasses the runtime policy
// filter, so these tests re-drive tools/list and the .well-known/mcp.json hint
// list through mcpapp.New — the same filtered path clients actually use. Promo
// tools ship in BOTH profiles from day one (the M3 F1 lesson), so both the
// souled and drone legs assert presence.
var promoToolNames = []string{
	"promo_compose",
	"promo_review_share",
	"promo_review_submit",
	"promo_state",
	"promo_release",
	"promo_read",
}

func TestPromoToolsVisibleInSouledRuntimeSurface(t *testing.T) {
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

	t.Run("tools/list keeps promo tools for the souled profile", func(t *testing.T) {
		have := listToolNames(t, env, app, authHeader, sessionID)
		for _, name := range promoToolNames {
			if !have[name] {
				t.Fatalf("promo tool %q missing from souled tools/list (filtered path)", name)
			}
		}
	})

	t.Run("well-known hint list includes promo tools", func(t *testing.T) {
		// Assertion strength: souled membership ONLY, mirroring the media
		// visibility test. The .well-known/mcp.json hint list is filtered
		// through the hardcoded ProfileSouled allowlist
		// (mcpapp/well_known.go: ToolAllowed(ProfileSouled, ...)), so this
		// subtest proves each promo tool passes the souled profile — it is not
		// drone coverage. Per-actor filtering happens at tools/list, whose
		// drone leg is asserted by TestPromoToolsVisibleInDroneRuntimeSurface.
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
		for _, name := range promoToolNames {
			if !hints[name] {
				t.Fatalf("promo tool %q missing from .well-known/mcp.json hints", name)
			}
		}
	})
}

func TestPromoToolsVisibleInDroneRuntimeSurface(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	// No soul binding: the actor resolves to the drone profile. Promo tools are
	// editorial content tooling, so they ship in the drone surface from day one
	// (the M3 F1 lesson — media tools only gained drone presence after the M3
	// gap was caught; promo is never allowed to regress that way).
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

	t.Run("tools/list keeps promo tools for the drone profile", func(t *testing.T) {
		have := listToolNames(t, env, app, authHeader, sessionID)
		for _, name := range promoToolNames {
			if !have[name] {
				t.Fatalf("promo tool %q missing from drone tools/list (filtered path)", name)
			}
		}
	})
}
