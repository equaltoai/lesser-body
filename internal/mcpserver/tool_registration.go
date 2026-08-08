package mcpserver

import (
	"fmt"

	"github.com/equaltoai/lesser-body/internal/mcpoutputschema"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
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
	return r.RegisterTool(registeredDef, handler)
}

func registerStreamingTool(r *mcpruntime.ToolRegistry, def mcpruntime.ToolDef, handler mcpruntime.StreamingToolHandler) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	registeredDef, err := toolDefWithErrorOutputSchema(def)
	if err != nil {
		return err
	}
	return r.RegisterStreamingTool(registeredDef, handler)
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
