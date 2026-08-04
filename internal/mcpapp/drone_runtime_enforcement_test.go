package mcpapp_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestDroneRuntimeEnforcesNoCommsBoundary(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
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
	token := newTestTokenWithAudience(t, "test", "agent1", []string{"write"}, []string{"https://api.example.com/mcp/agent1"})
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	assertForbidden := func(t *testing.T, body []byte, wantSurface string, wantName string) {
		t.Helper()

		type forbidden struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Reason                string `json:"reason"`
					Profile               string `json:"profile"`
					Surface               string `json:"surface"`
					Name                  string `json:"name"`
					CommunicationsEnabled bool   `json:"communicationsEnabled"`
					WalletAccessEnabled   bool   `json:"walletAccessEnabled"`
				} `json:"details"`
			} `json:"error"`
		}
		var out forbidden
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("unmarshal forbidden response: %v", err)
		}
		if out.Error.Code != "app.forbidden" {
			t.Fatalf("expected app.forbidden, got %+v", out.Error)
		}
		if out.Error.Details.Reason != "runtime_boundary" || out.Error.Details.Profile != "drone" {
			t.Fatalf("expected drone runtime boundary details, got %+v", out.Error.Details)
		}
		if out.Error.Details.Surface != wantSurface || out.Error.Details.Name != wantName {
			t.Fatalf("unexpected denied surface details: %+v", out.Error.Details)
		}
		if out.Error.Details.CommunicationsEnabled || out.Error.Details.WalletAccessEnabled {
			t.Fatalf("expected denied drone runtime flags, got %+v", out.Error.Details)
		}
	}

	t.Run("communication tool calls are rejected", func(t *testing.T) {
		callParams, _ := json.Marshal(map[string]any{
			"name": "sms_send",
			"arguments": map[string]any{
				"to":   "+15550143",
				"body": "hello",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 403 {
			t.Fatalf("sms_send: expected 403, got %d (%s)", resp.Status, string(resp.Body))
		}
		assertForbidden(t, resp.Body, "tool", "sms_send")
	})

	t.Run("communication resources are rejected", func(t *testing.T) {
		params, _ := json.Marshal(map[string]any{"uri": "agent://channels"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "resources/read", Params: params})
		if resp.Status != 403 {
			t.Fatalf("resources/read agent://channels: expected 403, got %d (%s)", resp.Status, string(resp.Body))
		}
		assertForbidden(t, resp.Body, "resource", "agent://channels")
	})

	t.Run("communication prompts are rejected", func(t *testing.T) {
		params, _ := json.Marshal(map[string]any{
			"name":      "compose_email",
			"arguments": map[string]any{"to": "alice@example.com"},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "prompts/get", Params: params})
		if resp.Status != 403 {
			t.Fatalf("prompts/get compose_email: expected 403, got %d (%s)", resp.Status, string(resp.Body))
		}
		assertForbidden(t, resp.Body, "prompt", "compose_email")
	})
}

func TestDroneRuntimeEnforcesNoCommsBoundaryWhenSoulbindingIsUnavailable(t *testing.T) {
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
			token := newTestTokenWithAudience(t, "test", "agent1", []string{"write"}, []string{"https://api.example.com/mcp/agent1"})
			authHeader := "Bearer " + token

			initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
				"authorization": {authHeader},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
			if initResp.Status != 200 {
				t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
			}
			sessionID := initResp.Headers["mcp-session-id"][0]

			assertForbidden := func(t *testing.T, body []byte, wantSurface string, wantName string) {
				t.Helper()

				type forbidden struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
						Details struct {
							Reason                string `json:"reason"`
							Profile               string `json:"profile"`
							Surface               string `json:"surface"`
							Name                  string `json:"name"`
							Determined            bool   `json:"determined"`
							DeterminationSource   string `json:"determinationSource"`
							CommunicationsEnabled bool   `json:"communicationsEnabled"`
							WalletAccessEnabled   bool   `json:"walletAccessEnabled"`
						} `json:"details"`
					} `json:"error"`
				}

				var out forbidden
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("unmarshal forbidden response: %v", err)
				}
				if out.Error.Code != "app.forbidden" {
					t.Fatalf("expected app.forbidden, got %+v", out.Error)
				}
				if out.Error.Details.Reason != "runtime_boundary" || out.Error.Details.Profile != "drone" {
					t.Fatalf("expected drone runtime boundary details, got %+v", out.Error.Details)
				}
				if out.Error.Details.Surface != wantSurface || out.Error.Details.Name != wantName {
					t.Fatalf("unexpected denied surface details: %+v", out.Error.Details)
				}
				if out.Error.Details.Determined {
					t.Fatalf("expected unresolved fail-closed runtime for %s, got %+v", scenario.wantSource, out.Error.Details)
				}
				if out.Error.Details.DeterminationSource != scenario.wantSource {
					t.Fatalf("expected determinationSource=%q, got %+v", scenario.wantSource, out.Error.Details)
				}
				if out.Error.Details.CommunicationsEnabled || out.Error.Details.WalletAccessEnabled {
					t.Fatalf("expected denied drone runtime flags, got %+v", out.Error.Details)
				}
			}

			callParams, _ := json.Marshal(map[string]any{
				"name": "sms_send",
				"arguments": map[string]any{
					"to":   "+15550143",
					"body": "hello",
				},
			})
			resp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {authHeader},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
			if resp.Status != 403 {
				t.Fatalf("sms_send: expected 403, got %d (%s)", resp.Status, string(resp.Body))
			}
			assertForbidden(t, resp.Body, "tool", "sms_send")
		})
	}
}
