// Package mcpoutputschema keeps schema-bearing MCP tool definitions compatible
// with Body's shared structured error-result envelope.
package mcpoutputschema

import (
	"encoding/json"
	"fmt"
)

var toolErrorOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"error":{
			"type":"object",
			"properties":{
				"code":{"type":"string"},
				"message":{"type":"string"},
				"status":{"type":"integer"},
				"details":{"type":"object","additionalProperties":true}
			},
			"required":["code","message"],
			"additionalProperties":true
		}
	},
	"required":["error"],
	"additionalProperties":false
}`)

// WithToolError returns a JSON Schema that accepts either the supplied success
// schema or Body's structuredContent.error schema.
func WithToolError(successSchema json.RawMessage) (json.RawMessage, error) {
	var success map[string]any
	if err := json.Unmarshal(successSchema, &success); err != nil {
		return nil, fmt.Errorf("decode success schema: %w", err)
	}
	if success == nil {
		return nil, fmt.Errorf("success schema must be a JSON object")
	}

	var failure map[string]any
	if err := json.Unmarshal(toolErrorOutputSchema, &failure); err != nil {
		return nil, fmt.Errorf("decode shared error schema: %w", err)
	}

	combined, err := json.Marshal(map[string]any{
		"anyOf": []any{success, failure},
	})
	if err != nil {
		return nil, fmt.Errorf("encode combined schema: %w", err)
	}
	return combined, nil
}
