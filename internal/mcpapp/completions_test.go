package mcpapp_test

import (
	"encoding/json"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestMCPCompletions_SouledPromptAndResourceSuggestions(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	sessionID := initializeCompletionSession(t, env, app, "/mcp/agent1", authHeader, true)

	timelineValues := completionValuesFor(t, env, app, "/mcp/agent1", authHeader, sessionID, 2, map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "summarize_timeline"},
		"argument": map[string]any{"name": "timeline", "value": "l"},
	})
	if !equalStrings(timelineValues, []string{"local"}) {
		t.Fatalf("expected local timeline completion, got %#v", timelineValues)
	}

	resourceValues := completionValuesFor(t, env, app, "/mcp/agent1", authHeader, sessionID, 3, map[string]any{
		"ref":      map[string]any{"type": "ref/resource", "uri": "agent://{resource}"},
		"argument": map[string]any{"name": "resource", "value": "channels"},
	})
	if !containsString(resourceValues, "channels") || !containsString(resourceValues, "channels/preferences") {
		t.Fatalf("expected souled channel resource completions, got %#v", resourceValues)
	}
}

func TestMCPCompletions_DroneProfileFiltersSensitiveSuggestions(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	sessionID := initializeCompletionSession(t, env, app, "/mcp/agent1", authHeader, true)

	promptParams, _ := json.Marshal(map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "compose_email"},
		"argument": map[string]any{"name": "tone", "value": "f"},
	})
	promptResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "completion/complete", Params: promptParams})
	if promptResp.Status != 403 {
		t.Fatalf("expected drone runtime boundary for compose_email completion, got status=%d body=%s", promptResp.Status, string(promptResp.Body))
	}
	if !strings.Contains(string(promptResp.Body), "runtime_boundary") {
		t.Fatalf("expected runtime_boundary response, got %s", string(promptResp.Body))
	}

	resourceValues := completionValuesFor(t, env, app, "/mcp/agent1", authHeader, sessionID, 3, map[string]any{
		"ref":      map[string]any{"type": "ref/resource", "uri": "agent://{resource}"},
		"argument": map[string]any{"name": "resource", "value": "channels"},
	})
	if len(resourceValues) != 0 {
		t.Fatalf("did not expect drone channel resource completions, got %#v", resourceValues)
	}

	timelineValues := completionValuesFor(t, env, app, "/mcp/agent1", authHeader, sessionID, 4, map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "summarize_timeline"},
		"argument": map[string]any{"name": "timeline", "value": "h"},
	})
	if !equalStrings(timelineValues, []string{"home"}) {
		t.Fatalf("expected drone-safe timeline completion, got %#v", timelineValues)
	}
}

func TestMCPCompletions_RequireReadScope(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestTokenWithAudience(t, "test", "agent1", nil, []string{"https://api.example.com/mcp/agent1"})
	sessionID := initializeCompletionSession(t, env, app, "/mcp/agent1", authHeader, true)

	params, _ := json.Marshal(map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "summarize_timeline"},
		"argument": map[string]any{"name": "timeline", "value": "h"},
	})
	resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "completion/complete", Params: params})
	if resp.Status != 403 {
		t.Fatalf("completion/complete: expected 403, got %d (%s)", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); !strings.Contains(got, "insufficient_scope") || !strings.Contains(got, `scope="read"`) {
		t.Fatalf("expected insufficient_scope read challenge, got %q", got)
	}
}

func TestMCPCompletions_UnsupportedRefsReturnNoSuggestions(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	sessionID := initializeCompletionSession(t, env, app, "/mcp/agent1", authHeader, true)

	values := completionValuesFor(t, env, app, "/mcp/agent1", authHeader, sessionID, 2, map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "unknown_prompt"},
		"argument": map[string]any{"name": "tone", "value": "f"},
	})
	if len(values) != 0 {
		t.Fatalf("expected unsupported prompt to return no suggestions, got %#v", values)
	}
}

func initializeCompletionSession(t testing.TB, env *testkit.Env, app *apptheory.App, path string, authHeader string, wantCompletions bool) string {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"protocolVersion": "2025-11-25"})
	initResp := invokeJSONAtPath(t, env, app, path, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: params})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	var rpc mcpruntime.Response
	if err := json.Unmarshal(initResp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal initialize: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("initialize error: %+v", rpc.Error)
	}
	var out struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	_, hasCompletions := out.Capabilities["completions"]
	if hasCompletions != wantCompletions {
		t.Fatalf("initialize completions capability: got %v want %v in %+v", hasCompletions, wantCompletions, out.Capabilities)
	}
	return sessionID
}

func completionValuesFor(t testing.TB, env *testkit.Env, app *apptheory.App, path string, authHeader string, sessionID string, id int, params map[string]any) []string {
	t.Helper()
	rawParams, _ := json.Marshal(params)
	resp := invokeJSONAtPath(t, env, app, path, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "completion/complete", Params: rawParams})
	if resp.Status != 200 {
		t.Fatalf("completion/complete: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal completion/complete: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("completion/complete error: %+v", rpc.Error)
	}
	var out struct {
		Completion struct {
			Values  []string `json:"values"`
			Total   *int     `json:"total,omitempty"`
			HasMore *bool    `json:"hasMore,omitempty"`
		} `json:"completion"`
	}
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal completion result: %v", err)
	}
	if out.Completion.Total == nil || *out.Completion.Total < len(out.Completion.Values) {
		t.Fatalf("unexpected completion total metadata: %+v", out.Completion)
	}
	if out.Completion.HasMore == nil {
		t.Fatalf("expected hasMore metadata in completion result: %+v", out.Completion)
	}
	return out.Completion.Values
}

func equalStrings(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
