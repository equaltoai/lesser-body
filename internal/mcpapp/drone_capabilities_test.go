package mcpapp_test

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestDroneRuntimeCapabilityContractIsExplicit(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

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

	t.Run("tools list exposes drone-only runtime surface", func(t *testing.T) {
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
		if resp.Status != 200 {
			t.Fatalf("tools/list: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("tools/list error: %+v", rpc.Error)
		}

		var out struct {
			Tools []mcpruntime.ToolDef `json:"tools"`
		}
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}

		have := map[string]bool{}
		for _, tool := range out.Tools {
			have[tool.Name] = true
		}
		if !have["post_create"] || !have["memory_query"] || !have["soul_read"] {
			t.Fatalf("expected social, memory, and public soul tools for drone runtime, got %+v", out.Tools)
		}
		for _, blocked := range []string{
			"email_send", "email_read", "email_get", "email_get_content", "email_search", "email_reply",
			"email_delete", "email_mark_read", "email_mark_unread", "sms_send", "sms_read", "voicemail_read",
			"identity_whoami",
		} {
			if have[blocked] {
				t.Fatalf("did not expect %q in drone tools/list: %+v", blocked, out.Tools)
			}
		}
	})

	t.Run("resources and prompts lists omit communication surfaces", func(t *testing.T) {
		resourcesResp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "resources/list"})
		if resourcesResp.Status != 200 {
			t.Fatalf("resources/list: status=%d body=%s", resourcesResp.Status, string(resourcesResp.Body))
		}

		var resourcesRPC mcpruntime.Response
		_ = json.Unmarshal(resourcesResp.Body, &resourcesRPC)
		if resourcesRPC.Error != nil {
			t.Fatalf("resources/list error: %+v", resourcesRPC.Error)
		}

		var resourcesOut struct {
			Resources []mcpruntime.ResourceDef `json:"resources"`
		}
		{
			b, _ := json.Marshal(resourcesRPC.Result)
			_ = json.Unmarshal(b, &resourcesOut)
		}
		resourceHave := map[string]bool{}
		for _, resource := range resourcesOut.Resources {
			resourceHave[resource.URI] = true
		}
		if !resourceHave["agent://capabilities"] || !resourceHave["agent://memory/recent"] {
			t.Fatalf("expected drone-safe resources, got %+v", resourcesOut.Resources)
		}
		for _, blocked := range []string{"agent://channels", "agent://email/inbox", "agent://sms/messages"} {
			if resourceHave[blocked] {
				t.Fatalf("did not expect %q in drone resources/list: %+v", blocked, resourcesOut.Resources)
			}
		}

		promptsResp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "prompts/list"})
		if promptsResp.Status != 200 {
			t.Fatalf("prompts/list: status=%d body=%s", promptsResp.Status, string(promptsResp.Body))
		}

		var promptsRPC mcpruntime.Response
		_ = json.Unmarshal(promptsResp.Body, &promptsRPC)
		if promptsRPC.Error != nil {
			t.Fatalf("prompts/list error: %+v", promptsRPC.Error)
		}

		var promptsOut struct {
			Prompts []mcpruntime.PromptDef `json:"prompts"`
		}
		{
			b, _ := json.Marshal(promptsRPC.Result)
			_ = json.Unmarshal(b, &promptsOut)
		}
		promptHave := map[string]bool{}
		for _, prompt := range promptsOut.Prompts {
			promptHave[prompt.Name] = true
		}
		if !promptHave["compose_post"] || !promptHave["memory_reflect"] {
			t.Fatalf("expected drone-safe prompts, got %+v", promptsOut.Prompts)
		}
		for _, blocked := range []string{"compose_email", "handle_inbound", "respect_preferences"} {
			if promptHave[blocked] {
				t.Fatalf("did not expect %q in drone prompts/list: %+v", blocked, promptsOut.Prompts)
			}
		}
	})

	t.Run("agent capabilities resource reports runtime profile and contracts", func(t *testing.T) {
		params, _ := json.Marshal(map[string]any{"uri": "agent://capabilities"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read agent://capabilities: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("agent://capabilities error: %+v", rpc.Error)
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
		if len(out.Contents) != 1 {
			t.Fatalf("expected one capabilities content payload, got %+v", out.Contents)
		}

		var payload struct {
			Runtime struct {
				Profile               string `json:"profile"`
				Determined            bool   `json:"determined"`
				BoundSoul             bool   `json:"boundSoul"`
				CommunicationsEnabled bool   `json:"communicationsEnabled"`
				WalletAccessEnabled   bool   `json:"walletAccessEnabled"`
			} `json:"runtime"`
			Tools     []string       `json:"tools"`
			Resources []string       `json:"resources"`
			Prompts   []string       `json:"prompts"`
			Contracts map[string]any `json:"contracts"`
		}
		if err := json.Unmarshal([]byte(out.Contents[0].Text), &payload); err != nil {
			t.Fatalf("unmarshal capabilities payload: %v", err)
		}
		if payload.Runtime.Profile != "drone" || !payload.Runtime.Determined || payload.Runtime.BoundSoul {
			t.Fatalf("unexpected runtime payload: %+v", payload.Runtime)
		}
		if payload.Runtime.CommunicationsEnabled || payload.Runtime.WalletAccessEnabled {
			t.Fatalf("expected drone runtime to disable comms and wallet access, got %+v", payload.Runtime)
		}
		for _, blocked := range []string{"email_send", "agent://channels", "compose_email"} {
			if containsString(payload.Tools, blocked) || containsString(payload.Resources, blocked) || containsString(payload.Prompts, blocked) {
				t.Fatalf("did not expect blocked surface %q in capabilities payload: %+v", blocked, payload)
			}
		}
		if _, ok := payload.Contracts["drone"]; !ok {
			t.Fatalf("expected drone contract in capabilities payload, got %+v", payload.Contracts)
		}
		if _, ok := payload.Contracts["souled"]; !ok {
			t.Fatalf("expected souled contract in capabilities payload, got %+v", payload.Contracts)
		}
	})

	t.Run("well-known discovery publishes runtime profiles", func(t *testing.T) {
		resp := env.Invoke(context.Background(), app, apptheory.Request{
			Method: "GET",
			Path:   "/.well-known/mcp.json",
		})
		if resp.Status != 200 {
			t.Fatalf("GET /.well-known/mcp.json: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var payload struct {
			RuntimeProfiles map[string]struct {
				CommunicationsEnabled bool     `json:"communicationsEnabled"`
				WalletAccessEnabled   bool     `json:"walletAccessEnabled"`
				Tools                 []string `json:"tools"`
			} `json:"runtime_profiles"`
		}
		if err := json.Unmarshal(resp.Body, &payload); err != nil {
			t.Fatalf("unmarshal mcp.json: %v", err)
		}
		droneProfile, ok := payload.RuntimeProfiles["drone"]
		if !ok {
			t.Fatalf("expected drone runtime profile, got %+v", payload.RuntimeProfiles)
		}
		if droneProfile.CommunicationsEnabled || droneProfile.WalletAccessEnabled {
			t.Fatalf("expected drone discovery profile to disable comms and wallet access, got %+v", droneProfile)
		}
		if !containsString(droneProfile.Tools, "post_create") || !containsString(droneProfile.Tools, "soul_read") {
			t.Fatalf("unexpected drone discovery tools: %+v", droneProfile.Tools)
		}
		for _, blocked := range []string{
			"email_send", "email_read", "email_get", "email_get_content", "email_search", "email_reply",
			"email_delete", "email_mark_read", "email_mark_unread", "sms_send", "sms_read", "voicemail_read",
		} {
			if containsString(droneProfile.Tools, blocked) {
				t.Fatalf("unexpected drone discovery tool %q: %+v", blocked, droneProfile.Tools)
			}
		}
	})
}

func TestDroneRuntimeCapabilityContractFailsClosedWhenSoulbindingIsUnavailable(t *testing.T) {
	scenarios := []struct {
		name       string
		setup      func(testing.TB)
		wantSource string
	}{
		{
			name: "soulbinding unconfigured",
			setup: func(tb testing.TB) {
				tb.Helper()
				resetSoulBindingLookup(tb)
				tb.Setenv("LESSER_TABLE_NAME", "")
			},
			wantSource: "soulbinding_unconfigured",
		},
		{
			name: "soulbinding lookup error",
			setup: func(tb testing.TB) {
				tb.Helper()
				installErroringSoulBindingLookup(tb)
			},
			wantSource: "soulbinding_lookup_error",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("MCP_SESSION_TABLE", "")
			t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
			t.Setenv("JWT_SECRET", "test")
			scenario.setup(t)
			auth.ResetForTests()
			restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
				return &trustconfig.Effective{}, nil
			})
			t.Cleanup(restoreTrustConfig)

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

			listResp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {authHeader},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
			if listResp.Status != 200 {
				t.Fatalf("tools/list: status=%d body=%s", listResp.Status, string(listResp.Body))
			}

			var rpc mcpruntime.Response
			_ = json.Unmarshal(listResp.Body, &rpc)
			if rpc.Error != nil {
				t.Fatalf("tools/list error: %+v", rpc.Error)
			}

			var listOut struct {
				Tools []mcpruntime.ToolDef `json:"tools"`
			}
			{
				b, _ := json.Marshal(rpc.Result)
				_ = json.Unmarshal(b, &listOut)
			}
			have := map[string]bool{}
			for _, tool := range listOut.Tools {
				have[tool.Name] = true
			}
			for _, blocked := range []string{
				"email_send", "email_read", "email_get", "email_get_content", "email_search", "email_reply",
				"email_delete", "email_mark_read", "email_mark_unread", "sms_send", "sms_read", "voicemail_read",
				"identity_whoami",
			} {
				if have[blocked] {
					t.Fatalf("expected fail-closed tool list for %s; found %q in %+v", scenario.wantSource, blocked, listOut.Tools)
				}
			}

			params, _ := json.Marshal(map[string]any{"uri": "agent://capabilities"})
			capResp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {authHeader},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "resources/read", Params: params})
			if capResp.Status != 200 {
				t.Fatalf("resources/read agent://capabilities: status=%d body=%s", capResp.Status, string(capResp.Body))
			}

			_ = json.Unmarshal(capResp.Body, &rpc)
			if rpc.Error != nil {
				t.Fatalf("agent://capabilities error: %+v", rpc.Error)
			}

			var capOut struct {
				Contents []struct {
					Text string `json:"text"`
				} `json:"contents"`
			}
			{
				b, _ := json.Marshal(rpc.Result)
				_ = json.Unmarshal(b, &capOut)
			}

			var payload struct {
				Runtime struct {
					Profile             string `json:"profile"`
					Determined          bool   `json:"determined"`
					DeterminationSource string `json:"determinationSource"`
					BoundSoul           bool   `json:"boundSoul"`
				} `json:"runtime"`
			}
			if err := json.Unmarshal([]byte(capOut.Contents[0].Text), &payload); err != nil {
				t.Fatalf("unmarshal capabilities payload: %v", err)
			}
			if payload.Runtime.Profile != "drone" || payload.Runtime.Determined || payload.Runtime.BoundSoul {
				t.Fatalf("expected unresolved drone runtime for %s, got %+v", scenario.wantSource, payload.Runtime)
			}
			if payload.Runtime.DeterminationSource != scenario.wantSource {
				t.Fatalf("expected determinationSource=%q, got %+v", scenario.wantSource, payload.Runtime)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
