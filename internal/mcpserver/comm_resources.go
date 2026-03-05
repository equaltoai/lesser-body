package mcpserver

import (
	"context"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func resourceNotImplementedJSON(uri string) mcpruntime.ResourceHandler {
	return func(_ context.Context) ([]mcpruntime.ResourceContent, error) {
		return resourceJSON(uri, map[string]any{
			"error": map[string]any{
				"code":    "not_implemented",
				"message": "not implemented",
				"status":  501,
			},
		})
	}
}
