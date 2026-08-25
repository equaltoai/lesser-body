package mcpapp_test

import (
	"encoding/json"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

func requireToolErrorResult(t testing.TB, rpc *mcpruntime.Response) (*mcpruntime.ToolResult, map[string]any) {
	t.Helper()
	if rpc == nil {
		t.Fatal("nil JSON-RPC response")
	}
	if rpc.Error != nil {
		t.Fatalf("tool failure escaped as JSON-RPC error: %+v", rpc.Error)
	}
	var result mcpruntime.ToolResult
	b, err := json.Marshal(rpc.Result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if !result.IsError {
		t.Fatalf("tool result = %+v, want isError=true", result)
	}
	payload, _ := result.StructuredContent["error"].(map[string]any)
	if payload == nil {
		t.Fatalf("structuredContent.error = %#v, want object", result.StructuredContent["error"])
	}
	return &result, payload
}
