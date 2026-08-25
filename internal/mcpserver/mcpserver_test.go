package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func invokeRPC(t testing.TB, env *testkit.Env, app *apptheory.App, sessionID string, req *mcpruntime.Request) (*mcpruntime.Response, string) {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	headers := map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"application/json, text/event-stream"},
	}
	if sessionID != "" {
		headers["mcp-session-id"] = []string{sessionID}
	}

	httpResp := env.Invoke(context.Background(), app, apptheory.Request{
		Method:  "POST",
		Path:    "/mcp",
		Headers: headers,
		Body:    body,
	})

	nextSessionID := sessionID
	if ids := httpResp.Headers["mcp-session-id"]; len(ids) > 0 && ids[0] != "" {
		nextSessionID = ids[0]
	}

	var rpcResp mcpruntime.Response
	if err := json.Unmarshal(httpResp.Body, &rpcResp); err != nil {
		t.Fatalf("unmarshal response: %v (status=%d)", err, httpResp.Status)
	}
	return &rpcResp, nextSessionID
}

func TestEchoTool(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")

	srv, err := mcpserver.New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	for _, tool := range srv.Registry().List() {
		if tool.Execution == nil {
			continue
		}
		switch tool.Execution.TaskSupport {
		case "", mcpruntime.TaskSupportForbidden:
			continue
		default:
			t.Fatalf("tool %q must not advertise task support without MCP_TASK_TABLE: %+v", tool.Name, tool.Execution)
		}
	}

	env := testkit.New()
	app := env.App()
	app.Post("/mcp", srv.Handler())

	initResp, sessionID := invokeRPC(t, env, app, "", &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if sessionID == "" {
		t.Fatalf("expected non-empty session id")
	}
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	{
		b, marshalErr := json.Marshal(initResp.Result)
		if marshalErr != nil {
			t.Fatalf("marshal initialize result: %v", marshalErr)
		}
		var out struct {
			Capabilities map[string]any `json:"capabilities"`
		}
		if unmarshalErr := json.Unmarshal(b, &out); unmarshalErr != nil {
			t.Fatalf("unmarshal initialize result: %v", unmarshalErr)
		}
		if _, ok := out.Capabilities["tools"]; !ok {
			t.Fatalf("initialize result missing capabilities.tools: %+v", out.Capabilities)
		}
	}

	toolsResp, sessionID := invokeRPC(t, env, app, sessionID, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})
	if toolsResp.Error != nil {
		t.Fatalf("tools/list error: %+v", toolsResp.Error)
	}

	var toolsResult struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	{
		b, marshalErr := json.Marshal(toolsResp.Result)
		if marshalErr != nil {
			t.Fatalf("marshal tools/list result: %v", marshalErr)
		}
		if err := json.Unmarshal(b, &toolsResult); err != nil {
			t.Fatalf("unmarshal tools/list result: %v", err)
		}
	}
	foundEcho := false
	for _, tool := range toolsResult.Tools {
		if tool.Name == "echo" {
			foundEcho = true
			break
		}
	}
	if !foundEcho {
		t.Fatalf("expected echo tool in tools/list, have: %+v", toolsResult.Tools)
	}

	callParams, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": json.RawMessage(`{"message":"hi"}`),
	})
	callResp, _ := invokeRPC(t, env, app, sessionID, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  callParams,
	})
	if callResp.Error != nil {
		t.Fatalf("tools/call error: %+v", callResp.Error)
	}

	var toolResult mcpruntime.ToolResult
	{
		b, marshalErr := json.Marshal(callResp.Result)
		if marshalErr != nil {
			t.Fatalf("marshal tools/call result: %v", marshalErr)
		}
		if err := json.Unmarshal(b, &toolResult); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
	}
	if len(toolResult.Content) != 1 || toolResult.Content[0].Type != "text" || toolResult.Content[0].Text != "hi" {
		t.Fatalf("unexpected tool result: %+v", toolResult)
	}
}

func TestInitializeCapabilitiesArePinnedToConfiguredSurfaces(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")

	srv, err := mcpserver.New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	env := testkit.New()
	app := env.App()
	app.Post("/mcp", srv.Handler())

	params, _ := json.Marshal(map[string]any{"protocolVersion": "2025-11-25"})
	initResp, _ := invokeRPC(t, env, app, "", &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "init-capabilities",
		Method:  "initialize",
		Params:  params,
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	b, err := json.Marshal(initResp.Result)
	if err != nil {
		t.Fatalf("marshal initialize result: %v", err)
	}
	var out struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}

	for _, name := range []string{"tools", "resources", "prompts", "completions"} {
		if _, ok := out.Capabilities[name]; !ok {
			t.Fatalf("expected %q capability to remain advertised: %+v", name, out.Capabilities)
		}
	}
	for _, name := range []string{"tasks", "logging"} {
		if _, ok := out.Capabilities[name]; ok {
			t.Fatalf("did not expect fail-closed %q capability to be advertised: %+v", name, out.Capabilities)
		}
	}
	assertNoUnsupportedSubCapabilities(t, "tools", out.Capabilities["tools"])
	assertNoUnsupportedSubCapabilities(t, "resources", out.Capabilities["resources"])
	assertNoUnsupportedSubCapabilities(t, "prompts", out.Capabilities["prompts"])
}

func assertNoUnsupportedSubCapabilities(t testing.TB, name string, raw any) {
	t.Helper()
	capability, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected %s capability object, got %T", name, raw)
	}
	for _, sub := range []string{"listChanged", "subscribe"} {
		if _, ok := capability[sub]; ok {
			t.Fatalf("did not expect %s.%s overclaim: %+v", name, sub, capability)
		}
	}
}

// TestMCPServerMethodNotAllowedCarriesAllowHeader pins the AppTheory v4 wire
// delta that MCP method-not-allowed responses carry the RFC 9110 Allow header.
// body's app router only routes POST/GET/DELETE to the MCP handler, so this
// asserts the server-level shape directly: an unsupported method reaching the
// handler answers 405 with `Allow: DELETE, GET, POST`.
func TestMCPServerMethodNotAllowedCarriesAllowHeader(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")

	srv, err := mcpserver.New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	resp, handlerErr := srv.Handler()(&apptheory.Context{
		Request: apptheory.Request{
			Method:  "PUT",
			Path:    "/mcp",
			Headers: map[string][]string{"content-type": {"application/json"}},
			Body:    []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		},
	})
	if handlerErr != nil {
		t.Fatalf("handler error: %v", handlerErr)
	}
	if resp == nil || resp.Status != 405 {
		t.Fatalf("expected 405 method not allowed, got %+v", resp)
	}
	allow := ""
	if values := resp.Headers["allow"]; len(values) > 0 {
		allow = values[0]
	}
	if allow != "DELETE, GET, POST" {
		t.Fatalf("expected Allow: DELETE, GET, POST on the 405, got %q", allow)
	}
}
