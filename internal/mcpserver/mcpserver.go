package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/tabletheory"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	"github.com/theory-cloud/tabletheory/pkg/session"
)

const (
	envMcpAllowedOrigins = "MCP_ALLOWED_ORIGINS"
	envMcpSessionTable   = "MCP_SESSION_TABLE"
	envMcpStreamTable    = "MCP_STREAM_TABLE"

	initialSessionListenerSafetyBuffer = 5 * time.Second
	initialSessionListenerMaxDuration  = 25 * time.Second
)

var newMCPDB = func() (tablecore.DB, error) {
	return tabletheory.NewBasic(session.Config{
		Region: os.Getenv("AWS_REGION"),
	})
}

func New(name, version string) (*Server, error) {
	opts, err := buildServerOptionsFromEnv()
	if err != nil {
		return nil, err
	}

	srv := mcpruntime.NewServer(name, version, opts...)
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
	opts := []ServerOption{
		mcpruntime.WithCapabilityConfig(bodyCapabilityConfig()),
		mcpruntime.WithInitialSessionListenerBudget(mcpruntime.InitialSessionListenerBudgetOptions{
			SafetyBuffer: initialSessionListenerSafetyBuffer,
			MaxDuration:  initialSessionListenerMaxDuration,
		}),
		mcpruntime.WithCompletionHooks(promptCompletion, resourceCompletion),
	}

	if validator := originValidatorFromEnv(); validator != nil {
		opts = append(opts, mcpruntime.WithOriginValidator(validator))
	}

	if os.Getenv(envMcpSessionTable) != "" || os.Getenv(envMcpStreamTable) != "" {
		db, err := newMCPDB()
		if err != nil {
			return nil, fmt.Errorf("create tabletheory client: %w", err)
		}

		if os.Getenv(envMcpSessionTable) != "" {
			opts = append(opts, mcpruntime.WithSessionStore(mcpruntime.NewDynamoSessionStore(db)))
		}
		if os.Getenv(envMcpStreamTable) != "" {
			opts = append(opts, mcpruntime.WithStreamStore(mcpruntime.NewDynamoStreamStore(db)))
		}
	}

	return opts, nil
}

func bodyCapabilityConfig() mcpruntime.CapabilityConfig {
	return mcpruntime.CapabilityConfig{
		Tools:       true,
		Resources:   true,
		Prompts:     true,
		Completions: true,
		Tasks:       false,
	}
}

func originValidatorFromEnv() mcpruntime.OriginValidator {
	origins := SplitCommaSeparatedEnv(os.Getenv(envMcpAllowedOrigins))
	if len(origins) == 0 {
		return nil
	}
	for _, origin := range origins {
		if origin == "*" {
			return func(origin string) bool {
				return strings.TrimSpace(origin) != ""
			}
		}
	}
	return mcpruntime.AllowOrigins(origins...)
}

func SplitCommaSeparatedEnv(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
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

	if err := registerMemoryTools(r); err != nil {
		return err
	}

	if err := registerSkillsTools(r); err != nil {
		return err
	}

	return registerCommunicationTools(r)
}
