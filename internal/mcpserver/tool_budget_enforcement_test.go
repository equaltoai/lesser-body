package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

func TestRegisteredToolsEnforceCallerMaxOutputBytesOnEverySuccessPath(t *testing.T) {
	largeResult := func() *mcpruntime.ToolResult {
		result, err := toolJSONResult(map[string]any{"payload": strings.Repeat("x", 2048)}, nil)
		if err != nil {
			t.Fatalf("toolJSONResult: %v", err)
		}
		return result
	}

	for _, tc := range []struct {
		name     string
		register func(*mcpruntime.ToolRegistry) error
		call     func(*mcpruntime.ToolRegistry) (*mcpruntime.ToolResult, error)
	}{
		{
			name: "buffered standard view",
			register: func(registry *mcpruntime.ToolRegistry) error {
				return registerTool(registry, mcpruntime.ToolDef{Name: "budget_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error) {
					return largeResult(), nil
				})
			},
			call: func(registry *mcpruntime.ToolRegistry) (*mcpruntime.ToolResult, error) {
				return registry.Call(context.Background(), "budget_fixture", json.RawMessage(`{"view":"standard","max_output_bytes":200}`))
			},
		},
		{
			name: "streaming completion",
			register: func(registry *mcpruntime.ToolRegistry) error {
				return registerStreamingTool(registry, mcpruntime.ToolDef{Name: "budget_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, json.RawMessage, func(mcpruntime.SSEEvent)) (*mcpruntime.ToolResult, error) {
					return largeResult(), nil
				})
			},
			call: func(registry *mcpruntime.ToolRegistry) (*mcpruntime.ToolResult, error) {
				return registry.CallStreaming(context.Background(), "budget_fixture", json.RawMessage(`{"view":"standard","max_output_bytes":200}`), func(mcpruntime.SSEEvent) {})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := mcpruntime.NewToolRegistry()
			if err := tc.register(registry); err != nil {
				t.Fatalf("register: %v", err)
			}
			result, err := tc.call(registry)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			payload := toolErrorPayloadForTest(t, result)
			if payload["code"] != "response_too_large" || intFromAny(payload["status"]) != 413 {
				t.Fatalf("error payload = %#v, want response_too_large/413", payload)
			}
			details, _ := payload["details"].(map[string]any)
			if details["tool"] != "budget_fixture" || details["view"] != "standard" || intFromAny(details["maxOutputBytes"]) != 200 || intFromAny(details["measuredBytes"]) <= 200 {
				t.Fatalf("budget details = %#v", details)
			}
		})
	}
}
