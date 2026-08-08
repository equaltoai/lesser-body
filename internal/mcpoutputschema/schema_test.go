package mcpoutputschema

import (
	"encoding/json"
	"testing"
)

func TestWithToolErrorKeepsMCPObjectRoot(t *testing.T) {
	combined, err := WithToolError(json.RawMessage(`{
		"type":"object",
		"properties":{"data":{"type":"object"}},
		"required":["data"]
	}`))
	if err != nil {
		t.Fatalf("WithToolError: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(combined, &schema); err != nil {
		t.Fatalf("unmarshal combined output schema: %v", err)
	}
	if got := schema["type"]; got != "object" {
		t.Fatalf("outputSchema.type = %#v, want object for MCP 2025-11-25 compatibility", got)
	}
	if alternatives, ok := schema["anyOf"].([]any); !ok || len(alternatives) != 2 {
		t.Fatalf("outputSchema.anyOf = %#v, want success/error alternatives", schema["anyOf"])
	}
}
