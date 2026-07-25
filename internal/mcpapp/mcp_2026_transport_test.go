package mcpapp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/theory-cloud/apptheory/v2/testkit"
)

func TestBodyMCP_ModernTransportContract(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("MCP_ENDPOINT", "")
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	auth.ResetForTests()

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test-secret", "agent1", []string{"read"})

	t.Run("discovers truthful stateless capabilities with private no-store defaults", func(t *testing.T) {
		headers := map[string][]string{
			"authorization":        {"Bearer " + token},
			"mcp-protocol-version": {"2026-07-28"},
			"mcp-method":           {"server/discover"},
		}

		resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", headers, bodyModernDiscoverRequest("discover"))
		if resp.Status != 200 {
			t.Fatalf("server/discover status = %d, want 200; body = %s", resp.Status, string(resp.Body))
		}
		if got := firstHeader(resp.Headers, "mcp-session-id"); got != "" {
			t.Fatalf("stateless server/discover returned session id %q", got)
		}

		var out struct {
			Result struct {
				SupportedVersions []string       `json:"supportedVersions"`
				Capabilities      map[string]any `json:"capabilities"`
				ResultType        string         `json:"resultType"`
				TTLMS             *int64         `json:"ttlMs"`
				CacheScope        *string        `json:"cacheScope"`
				Meta              struct {
					ServerInfo struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"io.modelcontextprotocol/serverInfo"`
				} `json:"_meta"`
			} `json:"result"`
		}
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("decode server/discover response: %v; body=%s", err, string(resp.Body))
		}

		wantVersions := []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26"}
		if !reflect.DeepEqual(out.Result.SupportedVersions, wantVersions) {
			t.Fatalf("supported versions = %#v, want %#v", out.Result.SupportedVersions, wantVersions)
		}
		wantCapabilities := map[string]any{
			"tools":       map[string]any{},
			"resources":   map[string]any{},
			"prompts":     map[string]any{},
			"completions": map[string]any{},
		}
		if !reflect.DeepEqual(out.Result.Capabilities, wantCapabilities) {
			t.Fatalf("capabilities = %#v, want fail-closed %#v", out.Result.Capabilities, wantCapabilities)
		}
		if out.Result.ResultType != "complete" {
			t.Fatalf("resultType = %q, want complete", out.Result.ResultType)
		}
		if out.Result.TTLMS == nil || *out.Result.TTLMS != 0 || out.Result.CacheScope == nil || *out.Result.CacheScope != "private" {
			t.Fatalf("cache policy = ttlMs:%v cacheScope:%v, want present fail-closed 0/private", out.Result.TTLMS, out.Result.CacheScope)
		}
		if out.Result.Meta.ServerInfo.Name != "lesser-body" || out.Result.Meta.ServerInfo.Version != "dev" {
			t.Fatalf("server info = %+v, want lesser-body/dev", out.Result.Meta.ServerInfo)
		}
	})

	t.Run("rejects protocol header and metadata mismatch", func(t *testing.T) {
		headers := map[string][]string{
			"authorization":        {"Bearer " + token},
			"mcp-protocol-version": {"2025-11-25"},
			"mcp-method":           {"server/discover"},
		}

		resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", headers, bodyModernDiscoverRequest("mismatch"))
		if resp.Status != 400 {
			t.Fatalf("mismatch status = %d, want 400; body = %s", resp.Status, string(resp.Body))
		}
		var out struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("decode mismatch response: %v; body=%s", err, string(resp.Body))
		}
		if out.Error.Code != -32020 {
			t.Fatalf("mismatch error code = %d, want -32020", out.Error.Code)
		}
	})

	t.Run("keeps legacy session validation", func(t *testing.T) {
		headers := map[string][]string{
			"authorization":        {"Bearer " + token},
			"mcp-protocol-version": {"2025-11-25"},
		}

		resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", headers, map[string]any{
			"jsonrpc": "2.0",
			"id":      "missing-session",
			"method":  "tools/list",
		})
		if resp.Status != 400 {
			t.Fatalf("missing session status = %d, want 400; body = %s", resp.Status, string(resp.Body))
		}
		if !strings.Contains(string(resp.Body), "missing Mcp-Session-Id") {
			t.Fatalf("missing session response changed shape: %s", string(resp.Body))
		}
	})
}

func bodyModernDiscoverRequest(id string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "server/discover",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
		},
	}
}
