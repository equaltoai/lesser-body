package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/session"
)

const envMcpSessionTable = "MCP_SESSION_TABLE"

func New(name, version string) (*Server, error) {
	opts, err := buildServerOptionsFromEnv()
	if err != nil {
		return nil, err
	}

	srv := NewServer(name, version, opts...)
	if err := registerTools(srv.Registry()); err != nil {
		return nil, err
	}
	if err := registerResources(srv); err != nil {
		return nil, err
	}
	if err := registerPrompts(srv); err != nil {
		return nil, err
	}

	return srv, nil
}

func buildServerOptionsFromEnv() ([]ServerOption, error) {
	if os.Getenv(envMcpSessionTable) == "" {
		return nil, nil
	}

	db, err := tabletheory.NewBasic(session.Config{
		Region: os.Getenv("AWS_REGION"),
	})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}

	return []ServerOption{
		WithSessionStore(mcpruntime.NewDynamoSessionStore(db)),
	}, nil
}

func registerTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	if err := r.RegisterTool(mcpruntime.ToolDef{
		Name:        "echo",
		Description: "Echo back the provided message.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": { "type": "string" }
			},
			"required": ["message"]
		}`),
	}, func(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
		var in struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}

		return &mcpruntime.ToolResult{
			Content: []mcpruntime.ContentBlock{{Type: "text", Text: in.Message}},
		}, nil
	}); err != nil {
		return err
	}

	if err := registerSocialTools(r); err != nil {
		return err
	}

	return registerMemoryTools(r)
}
