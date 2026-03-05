package mcpapp

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func WithAudit(next apptheory.Handler, logger *slog.Logger) apptheory.Handler {
	if next == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if err := authorizeTools(ctx); err != nil {
			auditMcp(ctx, logger)
			return nil, err
		}
		auditMcp(ctx, logger)
		return next(ctx)
	}
}

func authorizeTools(ctx *apptheory.Context) error {
	if ctx == nil {
		return nil
	}

	body := ctx.Request.Body
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}

	if strings.HasPrefix(trimmed, "[") {
		reqs, err := mcpruntime.ParseBatchRequest(body)
		if err != nil {
			return nil
		}
		for _, req := range reqs {
			if err := authorizeToolsRequest(ctx, req); err != nil {
				return err
			}
		}
		return nil
	}

	req, err := mcpruntime.ParseRequest(body)
	if err != nil {
		return nil
	}
	return authorizeToolsRequest(ctx, req)
}

func authorizeToolsRequest(ctx *apptheory.Context, req *mcpruntime.Request) error {
	if ctx == nil || req == nil || req.Method != "tools/call" {
		return nil
	}

	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	toolName := strings.TrimSpace(params.Name)
	if toolName == "" {
		// Let the MCP runtime return Invalid params.
		return nil
	}

	p := auth.PrincipalFromContext(ctx)
	if p == nil {
		return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}
	if p.Type == auth.PrincipalTypeInstanceKey {
		return nil
	}
	if p.Claims == nil {
		return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	// M5 tool policy: read tools require read (or write/admin), write tools require write (or admin).
	if hasAnyScope(p.Claims.Scopes, "admin") {
		return nil
	}

	switch toolName {
	case "post_create", "post_boost", "post_favorite", "follow", "unfollow", "profile_update", "memory_append",
		"email_send", "email_reply", "email_delete", "sms_send", "phone_call":
		if hasAnyScope(p.Claims.Scopes, "write") {
			return nil
		}
	default:
		if hasAnyScope(p.Claims.Scopes, "write", "read") {
			return nil
		}
	}

	return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
}

func hasAnyScope(scopes []string, want ...string) bool {
	for _, s := range scopes {
		for _, w := range want {
			if s == w {
				return true
			}
		}
	}
	return false
}

func auditMcp(ctx *apptheory.Context, logger *slog.Logger) {
	if ctx == nil || logger == nil {
		return
	}

	body := ctx.Request.Body
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return
	}

	identity := strings.TrimSpace(ctx.AuthIdentity)
	requestID := strings.TrimSpace(ctx.RequestID)

	if strings.HasPrefix(trimmed, "[") {
		reqs, err := mcpruntime.ParseBatchRequest(body)
		if err != nil {
			return
		}
		for _, req := range reqs {
			auditMcpRequest(logger, requestID, identity, req)
		}
		return
	}

	req, err := mcpruntime.ParseRequest(body)
	if err != nil {
		return
	}
	auditMcpRequest(logger, requestID, identity, req)
}

func auditMcpRequest(logger *slog.Logger, requestID string, identity string, req *mcpruntime.Request) {
	if logger == nil || req == nil {
		return
	}

	if req.Method != "tools/call" {
		return
	}

	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)

	logger.Info("mcp tool call",
		"request_id", requestID,
		"identity", identity,
		"tool", strings.TrimSpace(params.Name),
	)
}
