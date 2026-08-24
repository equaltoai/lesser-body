package instanceapp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/theory-cloud/apptheory/v4/testkit"
)

func TestPtahMCP_ModernTransportContract(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))

	t.Run("discovers truthful stateless capabilities with private no-store defaults", func(t *testing.T) {
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2026-07-28"}
		headers["mcp-method"] = []string{"server/discover"}

		resp := invokeMCP(t, env, app, "/instance/ptah/mcp", headers, modernDiscoverRequest("discover"))
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
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
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
		if out.Result.Meta.ServerInfo.Name != "lesser-body-instance-ptah" || out.Result.Meta.ServerInfo.Version != "dev" {
			t.Fatalf("server info = %+v, want lesser-body-instance-ptah/dev", out.Result.Meta.ServerInfo)
		}
	})

	t.Run("rejects protocol header and metadata mismatch", func(t *testing.T) {
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2025-11-25"}
		headers["mcp-method"] = []string{"server/discover"}

		resp := invokeMCP(t, env, app, "/instance/ptah/mcp", headers, modernDiscoverRequest("mismatch"))
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
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2025-11-25"}

		resp := invokeMCP(t, env, app, "/instance/ptah/mcp", headers, map[string]any{
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

func TestBaMCP_ModernTransportContract(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ba/mcp"))

	t.Run("discovers truthful stateless capabilities with private no-store defaults", func(t *testing.T) {
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2026-07-28"}
		headers["mcp-method"] = []string{"server/discover"}

		resp := invokeMCP(t, env, app, "/instance/ba/mcp", headers, modernDiscoverRequest("discover"))
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
			"tools": map[string]any{},
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
		if out.Result.Meta.ServerInfo.Name != "lesser-body-instance-ba" || out.Result.Meta.ServerInfo.Version != "dev" {
			t.Fatalf("server info = %+v, want lesser-body-instance-ba/dev", out.Result.Meta.ServerInfo)
		}
	})

	t.Run("rejects protocol header and metadata mismatch", func(t *testing.T) {
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2025-11-25"}
		headers["mcp-method"] = []string{"server/discover"}

		resp := invokeMCP(t, env, app, "/instance/ba/mcp", headers, modernDiscoverRequest("mismatch"))
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
		headers := bearerHeaders(token)
		headers["mcp-protocol-version"] = []string{"2025-11-25"}

		resp := invokeMCP(t, env, app, "/instance/ba/mcp", headers, map[string]any{
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

func modernDiscoverRequest(id string) map[string]any {
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
