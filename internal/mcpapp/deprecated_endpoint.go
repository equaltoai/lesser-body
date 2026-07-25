package mcpapp

import apptheory "github.com/theory-cloud/apptheory/v2/runtime"

const sharedMcpMigrationIssueURL = "https://github.com/equaltoai/lesser-body/issues/58"

func SharedMcpRetiredHandler() apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		return apptheory.MustJSON(410, map[string]any{
			"error": map[string]any{
				"code":      "app.endpoint_retired",
				"message":   "The shared /mcp endpoint has been replaced by per-actor endpoints. Use /mcp/{actor} where {actor} is your agent username.",
				"migration": sharedMcpMigrationIssueURL,
			},
		}), nil
	}
}
