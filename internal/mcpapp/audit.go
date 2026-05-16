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
		if err := authorizeMCPRequests(ctx); err != nil {
			auditMcp(ctx, logger)
			if stepUp, ok := err.(*mcpStepUpRequiredError); ok {
				return insufficientScopeMCPResponse(ctx, stepUp.GrantedScopes, stepUp.RequiredScopes), nil
			}
			return nil, err
		}
		auditMcp(ctx, logger)
		return next(ctx)
	}
}

type mcpStepUpRequiredError struct {
	GrantedScopes  []string
	RequiredScopes []string
}

func (e *mcpStepUpRequiredError) Error() string {
	if e == nil {
		return "forbidden"
	}
	return "insufficient scope"
}

func authorizeMCPRequests(ctx *apptheory.Context) error {
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
			if err := authorizeMCPRequest(ctx, req); err != nil {
				return err
			}
		}
		return nil
	}

	req, err := mcpruntime.ParseRequest(body)
	if err != nil {
		return nil
	}
	return authorizeMCPRequest(ctx, req)
}

func authorizeMCPRequest(ctx *apptheory.Context, req *mcpruntime.Request) error {
	if ctx == nil || req == nil {
		return nil
	}

	requiredScopes := requiredScopesForMCPRequest(req)
	if len(requiredScopes) == 0 {
		return nil
	}

	p := auth.PrincipalFromContext(ctx)
	if p == nil {
		return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}
	if p.Type == auth.PrincipalTypeInstanceKey {
		if auth.LegacyInstanceKeyInboundAuthEnabled() {
			return nil
		}
		return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}
	if p.Claims == nil {
		return &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	// M5 policy: read surfaces require read (or write/admin), write surfaces require write (or admin).
	if hasAnyScope(p.Claims.Scopes, "admin") {
		return nil
	}

	allowedScopes := append([]string(nil), requiredScopes...)
	if len(requiredScopes) == 1 && requiredScopes[0] == "read" {
		allowedScopes = append(allowedScopes, "write")
	}
	if hasAnyScope(p.Claims.Scopes, allowedScopes...) {
		return nil
	}

	return &mcpStepUpRequiredError{
		GrantedScopes:  append([]string(nil), p.Claims.Scopes...),
		RequiredScopes: requiredScopes,
	}
}

func requiredScopesForMCPRequest(req *mcpruntime.Request) []string {
	if req == nil {
		return nil
	}

	switch req.Method {
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &params)
		toolName := strings.TrimSpace(params.Name)
		if toolName == "" {
			// Let the MCP runtime return Invalid params.
			return nil
		}
		return requiredScopesForTool(toolName)
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if strings.TrimSpace(params.URI) == "" {
			return nil
		}
		return []string{"read"}
	case "prompts/get":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if strings.TrimSpace(params.Name) == "" {
			return nil
		}
		return []string{"read"}
	case "completion/complete":
		return []string{"read"}
	case "tasks/list", "tasks/get", "tasks/result", "tasks/cancel":
		return []string{"read"}
	default:
		return nil
	}
}

func requiredScopesForTool(toolName string) []string {
	switch toolName {
	case "post_create", "post_boost", "post_favorite", "follow", "unfollow", "profile_update", "memory_append", "notification_dismiss",
		"email_send", "email_reply", "email_delete", "email_mark_read", "email_mark_unread", "sms_send":
		return []string{"write"}
	default:
		return []string{"read"}
	}
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

	switch req.Method {
	case "tools/call":
		var params struct {
			Name string           `json:"name"`
			Task *json.RawMessage `json:"task,omitempty"`
		}
		_ = json.Unmarshal(req.Params, &params)

		logger.Info("mcp tool call",
			"request_id", requestID,
			"identity", identity,
			"tool", strings.TrimSpace(params.Name),
			"task_requested", taskMetadataPresent(params.Task),
		)
	case "tasks/list", "tasks/get", "tasks/result", "tasks/cancel":
		logger.Info("mcp task method",
			"request_id", requestID,
			"identity", identity,
			"method", req.Method,
			"task_id", taskIDFromParams(req.Params),
		)
	}
}

func taskMetadataPresent(raw *json.RawMessage) bool {
	if raw == nil {
		return false
	}
	return strings.TrimSpace(string(*raw)) != "" && strings.TrimSpace(string(*raw)) != "null"
}

func taskIDFromParams(raw json.RawMessage) string {
	var params struct {
		TaskID string `json:"taskId"`
	}
	_ = json.Unmarshal(raw, &params)
	return strings.TrimSpace(params.TaskID)
}
