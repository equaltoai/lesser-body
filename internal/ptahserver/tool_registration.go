package ptahserver

import (
	"fmt"

	"github.com/equaltoai/lesser-body/internal/mcpoutputschema"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func registerTool(r *mcpruntime.ToolRegistry, def mcpruntime.ToolDef, handler mcpruntime.ToolHandler) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if len(def.OutputSchema) > 0 {
		outputSchema, err := mcpoutputschema.WithToolError(def.OutputSchema)
		if err != nil {
			return fmt.Errorf("tool %q output schema: %w", def.Name, err)
		}
		def.OutputSchema = outputSchema
	}
	return r.RegisterTool(def, handler)
}
