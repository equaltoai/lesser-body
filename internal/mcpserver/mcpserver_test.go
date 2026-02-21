package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

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
