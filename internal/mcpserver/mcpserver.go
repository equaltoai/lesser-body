package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/session"
)

const envMcpSessionTable = "MCP_SESSION_TABLE"

func New(name, version string) (*mcp.Server, error) {
	opts, err := buildServerOptionsFromEnv()
	if err != nil {
		return nil, err
	}

	srv := mcp.NewServer(name, version, opts...)
	if err := registerTools(srv.Registry()); err != nil {
		return nil, err
	}

	return srv, nil
}

func buildServerOptionsFromEnv() ([]mcp.ServerOption, error) {
	if os.Getenv(envMcpSessionTable) == "" {
		return nil, nil
	}

	db, err := tabletheory.NewBasic(session.Config{
		Region: os.Getenv("AWS_REGION"),
	})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}

	return []mcp.ServerOption{
		mcp.WithSessionStore(mcp.NewDynamoSessionStore(db)),
	}, nil
}

func registerTools(r *mcp.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	return r.RegisterTool(mcp.ToolDef{
		Name:        "echo",
		Description: "Echo back the provided message.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": { "type": "string" }
			},
			"required": ["message"]
		}`),
	}, func(ctx context.Context, args json.RawMessage) (*mcp.ToolResult, error) {
		var in struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}

		return &mcp.ToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: in.Message}},
		}, nil
	})
}

