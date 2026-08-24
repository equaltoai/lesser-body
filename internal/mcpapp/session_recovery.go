package mcpapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

const internalSessionRebindRequestID = "lesser-body-session-rebind"

var errSessionRebindInitialize = errors.New("MCP session rebind initialize failed")

// WithOAuthSessionRecovery transparently replaces a dead AppTheory MCP session
// for an already-authenticated OAuth principal. It creates the replacement by
// sending an internal initialize request through the same runtime handler, then
// replays the exact inbound request with only Mcp-Session-Id replaced.
//
// This middleware must wrap the raw AppTheory MCP runtime inside Body's auth,
// scope, profile, principal, audit, and tool-context gates. Those gates inspect
// the original request once; AppTheory rejects the dead session before dispatch,
// so the raw-runtime replay is the request's only dispatch opportunity.
//
// GET is never rebound because it is the SSE listener/resume transport and its
// event state belongs to the old session. Every GET caller retains AppTheory's
// spec-shaped 404 so MCP clients re-initialize instead of refreshing OAuth.
func WithOAuthSessionRecovery(next apptheory.Handler) apptheory.Handler {
	return withOAuthSessionRecovery(next)
}

func withActorOAuthSessionRecovery(next apptheory.Handler) apptheory.Handler {
	return withOAuthSessionRecovery(next)
}

func withOAuthSessionRecovery(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resp, err := next(ctx)
		if err != nil || !isOAuthPrincipal(ctx) || !isMCPSessionNotFoundResponse(resp) {
			return resp, err
		}

		if isMCPStreamRequest(ctx) {
			auditMCPSessionNotFound(ctx)
			setResponseHeader(resp, "cache-control", "no-store")
			return resp, nil
		}

		freshSessionID, err := initializeReplacementMCPSession(ctx, next)
		if err != nil {
			auditMCPSessionRebindFailure(ctx)
			return sessionRebindInternalError(ctx), nil
		}
		auditMCPSessionRebind(ctx)

		replayRequest := cloneAppTheoryRequest(ctx.Request)
		setRequestHeader(&replayRequest, "mcp-session-id", freshSessionID)
		replayResp, replayErr := invokeMCPHandlerWithRequest(ctx, next, replayRequest)
		if replayResp != nil {
			setResponseHeader(replayResp, "mcp-session-id", freshSessionID)
		}
		return replayResp, replayErr
	}
}

func initializeReplacementMCPSession(ctx *apptheory.Context, runtimeHandler apptheory.Handler) (string, error) {
	if ctx == nil || runtimeHandler == nil {
		return "", errSessionRebindInitialize
	}

	initializeRequest, err := replacementSessionInitializeRequest(ctx.Request)
	if err != nil {
		return "", errSessionRebindInitialize
	}
	resp, err := invokeMCPHandlerWithRequest(ctx, runtimeHandler, initializeRequest)
	if err != nil || resp == nil || resp.Status != http.StatusOK {
		return "", errSessionRebindInitialize
	}
	sessionID := strings.TrimSpace(firstHeaderValue(resp.Headers, "mcp-session-id"))
	if sessionID == "" {
		return "", errSessionRebindInitialize
	}
	return sessionID, nil
}

func replacementSessionInitializeRequest(original apptheory.Request) (apptheory.Request, error) {
	params := map[string]any{}
	if protocolVersion := strings.TrimSpace(firstHeaderValue(original.Headers, "mcp-protocol-version")); protocolVersion != "" {
		params["protocolVersion"] = protocolVersion
	}

	initialize := &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      internalSessionRebindRequestID,
		Method:  "initialize",
	}
	if len(params) > 0 {
		body, err := json.Marshal(params)
		if err != nil {
			return apptheory.Request{}, err
		}
		initialize.Params = body
	}
	body, err := json.Marshal(initialize)
	if err != nil {
		return apptheory.Request{}, err
	}

	req := cloneAppTheoryRequest(original)
	req.Method = http.MethodPost
	req.Body = body
	req.IsBase64 = false
	removeRequestHeaders(&req,
		"content-length",
		"last-event-id",
		"mcp-method",
		"mcp-name",
		"mcp-session-id",
		"transfer-encoding",
	)
	setRequestHeader(&req, "accept", "application/json, text/event-stream")
	setRequestHeader(&req, "content-type", "application/json")
	return req, nil
}

func invokeMCPHandlerWithRequest(
	ctx *apptheory.Context,
	handler apptheory.Handler,
	req apptheory.Request,
) (*apptheory.Response, error) {
	if ctx == nil || handler == nil {
		return nil, errSessionRebindInitialize
	}
	original := ctx.Request
	ctx.Request = req
	defer func() {
		ctx.Request = original
	}()
	return handler(ctx)
}

func cloneAppTheoryRequest(in apptheory.Request) apptheory.Request {
	out := in
	out.Body = append([]byte(nil), in.Body...)
	out.Headers = cloneStringSliceMap(in.Headers)
	out.Query = cloneStringSliceMap(in.Query)
	if in.Cookies != nil {
		out.Cookies = make(map[string]string, len(in.Cookies))
		for key, value := range in.Cookies {
			out.Cookies[key] = value
		}
	}
	return out
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if in == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func setRequestHeader(req *apptheory.Request, name string, value string) {
	if req == nil {
		return
	}
	if req.Headers == nil {
		req.Headers = map[string][]string{}
	}
	removeHeaders(req.Headers, name)
	req.Headers[strings.ToLower(strings.TrimSpace(name))] = []string{value}
}

func removeRequestHeaders(req *apptheory.Request, names ...string) {
	if req == nil || req.Headers == nil {
		return
	}
	for _, name := range names {
		removeHeaders(req.Headers, name)
	}
}

func setResponseHeader(resp *apptheory.Response, name string, value string) {
	if resp == nil {
		return
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	removeHeaders(resp.Headers, name)
	resp.Headers[strings.ToLower(strings.TrimSpace(name))] = []string{value}
}

func removeHeaders(headers map[string][]string, name string) {
	name = strings.TrimSpace(name)
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			delete(headers, key)
		}
	}
}

func isMCPStreamRequest(ctx *apptheory.Context) bool {
	return ctx != nil && strings.EqualFold(strings.TrimSpace(ctx.Request.Method), http.MethodGet)
}

func auditMCPSessionRebind(ctx *apptheory.Context) {
	slog.WarnContext(contextForSessionRecovery(ctx), "mcp session rebound",
		"request_id", requestIDForSessionRecovery(ctx),
		"principal_type", string(auth.PrincipalTypeOAuthToken),
		"reason", "mcp_session_rebound",
	)
}

func auditMCPSessionNotFound(ctx *apptheory.Context) {
	slog.WarnContext(contextForSessionRecovery(ctx), "mcp session not found",
		"request_id", requestIDForSessionRecovery(ctx),
		"principal_type", string(auth.PrincipalTypeOAuthToken),
		"reason", "mcp_session_not_found",
	)
}

func auditMCPSessionRebindFailure(ctx *apptheory.Context) {
	slog.ErrorContext(contextForSessionRecovery(ctx), "mcp session rebind failed",
		"request_id", requestIDForSessionRecovery(ctx),
		"principal_type", string(auth.PrincipalTypeOAuthToken),
		"reason", "mcp_session_initialize_failed",
	)
}

func contextForSessionRecovery(ctx *apptheory.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx.Context()
}

func requestIDForSessionRecovery(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ctx.RequestID)
}

func sessionRebindInternalError(ctx *apptheory.Context) *apptheory.Response {
	errBody := map[string]any{
		"code":    "app.internal",
		"message": "internal error",
	}
	if requestID := requestIDForSessionRecovery(ctx); requestID != "" {
		errBody["request_id"] = requestID
	}
	return apptheory.MustJSON(http.StatusInternalServerError, map[string]any{"error": errBody})
}

func isOAuthPrincipal(ctx *apptheory.Context) bool {
	principal := auth.PrincipalFromContext(ctx)
	return principal != nil && principal.Type == auth.PrincipalTypeOAuthToken
}

func isMCPSessionNotFoundResponse(resp *apptheory.Response) bool {
	if resp == nil || resp.Status != http.StatusNotFound || resp.BodyReader != nil {
		return false
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &body); err != nil || len(body) != 1 {
		return false
	}
	rawError, ok := body["error"]
	if !ok {
		return false
	}
	var message string
	if err := json.Unmarshal(rawError, &message); err != nil {
		return false
	}
	return message == "session not found"
}
