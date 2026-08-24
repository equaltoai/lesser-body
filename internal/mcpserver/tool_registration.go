package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/equaltoai/lesser-body/internal/mcpoutputschema"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// registerTool keeps every schema-bearing Ka tool's declared output contract
// aligned with the shared Body error result. Success results retain their
// existing schema while strict MCP clients can also validate
// structuredContent.error when isError is true.
func registerTool(r *mcpruntime.ToolRegistry, def mcpruntime.ToolDef, handler mcpruntime.ToolHandler) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	registeredDef, err := toolDefWithErrorOutputSchema(def)
	if err != nil {
		return err
	}
	return r.RegisterTool(registeredDef, func(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
		result, callErr := handler(ctx, args)
		if callErr != nil {
			return normalizedToolResultFromError(def.Name, callErr)
		}
		return enforceCallerMaxOutputBytes(def.Name, args, result)
	})
}

func registerStreamingTool(r *mcpruntime.ToolRegistry, def mcpruntime.ToolDef, handler mcpruntime.StreamingToolHandler) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	registeredDef, err := toolDefWithErrorOutputSchema(def)
	if err != nil {
		return err
	}
	return r.RegisterStreamingTool(registeredDef, func(ctx context.Context, args json.RawMessage, emit func(mcpruntime.SSEEvent)) (*mcpruntime.ToolResult, error) {
		result, callErr := handler(ctx, args, emit)
		if callErr != nil {
			return normalizedToolResultFromError(def.Name, callErr)
		}
		return enforceCallerMaxOutputBytes(def.Name, args, result)
	})
}

func enforceCallerMaxOutputBytes(toolName string, args json.RawMessage, result *mcpruntime.ToolResult) (*mcpruntime.ToolResult, error) {
	if result == nil || result.IsError {
		return result, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(args, &raw); err != nil || raw == nil {
		return result, nil
	}
	maxOutputBytes, err := nonNegativeIntFromAny(raw["max_output_bytes"], "max_output_bytes")
	if err != nil || maxOutputBytes <= 0 {
		return result, nil
	}

	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return normalizedToolResultFromError(toolName, err)
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}

	view := "default"
	if candidate, ok := raw["view"].(string); ok && candidate != "" {
		view = candidate
	}
	return toolErrorResult("response_too_large", toolName+" response exceeds max_output_bytes", 413, map[string]any{
		"tool":                   toolName,
		"view":                   view,
		"measuredBytes":          measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":         maxOutputBytes,
		"contentTextBytes":       measurement.ContentTextBytes,
		"structuredContentBytes": measurement.StructuredContentBytes,
		"guidance":               "use a more compact view, reduce limit or preview_chars, or increase max_output_bytes",
	})
}

func toolDefWithErrorOutputSchema(def mcpruntime.ToolDef) (mcpruntime.ToolDef, error) {
	if len(def.OutputSchema) == 0 {
		return def, nil
	}

	outputSchema, err := mcpoutputschema.WithToolError(def.OutputSchema)
	if err != nil {
		return mcpruntime.ToolDef{}, fmt.Errorf("tool %q output schema: %w", def.Name, err)
	}
	def.OutputSchema = outputSchema
	return def, nil
}
