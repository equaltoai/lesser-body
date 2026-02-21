package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"

	"github.com/equaltoai/lesser-body/internal/auth"
)

// protocolVersion is the MCP protocol version supported by this server.
//
// Keep this in sync with AppTheory's runtime/mcp server until upstream exposes it.
const protocolVersion = "2025-06-18"

const (
	defaultSessionTTLMinutes = 60
	envSessionTTLMinutes     = "MCP_SESSION_TTL_MINUTES"
)

type InvalidParamsError struct {
	Message string
}

func (e *InvalidParamsError) Error() string {
	if e == nil {
		return "invalid params"
	}
	return strings.TrimSpace(e.Message)
}

func invalidParams(msg string) error {
	return &InvalidParamsError{Message: msg}
}

func isInvalidParams(err error) bool {
	var out *InvalidParamsError
	return errors.As(err, &out)
}

type Server struct {
	name             string
	version          string
	registry         *mcpruntime.ToolRegistry
	resourceRegistry *mcpruntime.ResourceRegistry
	promptRegistry   *mcpruntime.PromptRegistry
	sessionStore     mcpruntime.SessionStore
	idGen            apptheory.IDGenerator
	logger           *slog.Logger
}

type ServerOption func(*Server)

func WithSessionStore(store mcpruntime.SessionStore) ServerOption {
	return func(s *Server) {
		if store != nil {
			s.sessionStore = store
		}
	}
}

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		if logger != nil {
			s.logger = logger
		}
	}
}

func WithIDGenerator(gen apptheory.IDGenerator) ServerOption {
	return func(s *Server) {
		if gen != nil {
			s.idGen = gen
		}
	}
}

func NewServer(name, version string, opts ...ServerOption) *Server {
	s := &Server{
		name:             strings.TrimSpace(name),
		version:          strings.TrimSpace(version),
		registry:         mcpruntime.NewToolRegistry(),
		resourceRegistry: mcpruntime.NewResourceRegistry(),
		promptRegistry:   mcpruntime.NewPromptRegistry(),
		sessionStore:     mcpruntime.NewMemorySessionStore(),
		idGen:            apptheory.RandomIDGenerator{},
		logger:           slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func (s *Server) Registry() *mcpruntime.ToolRegistry {
	return s.registry
}

func (s *Server) Resources() *mcpruntime.ResourceRegistry {
	return s.resourceRegistry
}

func (s *Server) Prompts() *mcpruntime.PromptRegistry {
	return s.promptRegistry
}

func (s *Server) Handler() apptheory.Handler {
	return func(c *apptheory.Context) (*apptheory.Response, error) {
		ctx := s.injectToolContext(c)
		body := c.Request.Body

		sessionID := firstHeader(c.Request.Headers, "mcp-session-id")
		sessionID, err := s.resolveSession(ctx, sessionID)
		if err != nil {
			s.logger.ErrorContext(ctx, "session error", "error", err)
			return jsonRPCErrorResponse(nil, mcpruntime.CodeInternalError, "session error", sessionID), nil
		}

		trimmed := trimLeftSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			return s.handleBatch(ctx, body, sessionID)
		}

		req, parseErr := mcpruntime.ParseRequest(body)
		if parseErr != nil {
			s.logger.ErrorContext(ctx, "parse error", "error", parseErr)
			return s.marshalSingleResponse(mcpruntime.NewErrorResponse(nil, mcpruntime.CodeParseError, "Parse error: "+parseErr.Error()), sessionID)
		}

		if req.Method == "tools/call" && wantsSSE(c.Request.Headers) {
			return s.handleToolsCallStreaming(ctx, req, sessionID)
		}

		resp := s.dispatch(ctx, req)
		return s.marshalSingleResponse(resp, sessionID)
	}
}

func (s *Server) injectToolContext(c *apptheory.Context) context.Context {
	if c == nil {
		return context.Background()
	}
	ctx := c.Context()

	principal := auth.PrincipalFromContext(c)
	bearerToken := extractBearerToken(c.Request.Headers)
	return auth.InjectToolContext(ctx, principal, bearerToken)
}

func (s *Server) dispatch(ctx context.Context, req *mcpruntime.Request) *mcpruntime.Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(ctx, req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(ctx, req)
	default:
		s.logger.ErrorContext(ctx, "method not found", "method", req.Method)
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req *mcpruntime.Request) *mcpruntime.Response {
	capabilities := map[string]any{
		"tools": map[string]any{},
	}
	if s.resourceRegistry.Len() > 0 {
		capabilities["resources"] = map[string]any{}
	}
	if s.promptRegistry.Len() > 0 {
		capabilities["prompts"] = map[string]any{}
	}

	return mcpruntime.NewResultResponse(req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    capabilities,
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	})
}

func (s *Server) handleToolsList(req *mcpruntime.Request) *mcpruntime.Response {
	return mcpruntime.NewResultResponse(req.ID, map[string]any{
		"tools": s.registry.List(),
	})
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) handleToolsCall(ctx context.Context, req *mcpruntime.Request) *mcpruntime.Response {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: missing tool name")
	}

	result, err := s.registry.Call(ctx, params.Name, params.Arguments)
	if err != nil {
		return s.toolCallError(ctx, req.ID, params.Name, err)
	}

	return mcpruntime.NewResultResponse(req.ID, result)
}

func (s *Server) handleToolsCallStreaming(ctx context.Context, req *mcpruntime.Request, sessionID string) (*apptheory.Response, error) {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return marshalSSEResponse(mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: "+err.Error()), sessionID)
	}
	if strings.TrimSpace(params.Name) == "" {
		return marshalSSEResponse(mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: missing tool name"), sessionID)
	}

	events := make(chan apptheory.SSEEvent)

	go func() {
		defer close(events)

		emit := func(ev mcpruntime.SSEEvent) {
			select {
			case <-ctx.Done():
				return
			case events <- apptheory.SSEEvent{
				Event: "progress",
				Data:  ev.Data,
			}:
			}
		}

		result, err := s.registry.CallStreaming(ctx, params.Name, params.Arguments, emit)
		var finalResp *mcpruntime.Response
		if err != nil {
			finalResp = s.toolCallError(ctx, req.ID, params.Name, err)
		} else {
			finalResp = mcpruntime.NewResultResponse(req.ID, result)
		}

		select {
		case <-ctx.Done():
			return
		case events <- apptheory.SSEEvent{
			Event: "message",
			Data:  finalResp,
		}:
		}
	}()

	resp, err := apptheory.SSEStreamResponse(ctx, 200, events)
	if err != nil {
		return nil, err
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	if sessionID != "" {
		resp.Headers["mcp-session-id"] = []string{sessionID}
	}
	return resp, nil
}

func (s *Server) handleResourcesList(req *mcpruntime.Request) *mcpruntime.Response {
	return mcpruntime.NewResultResponse(req.ID, map[string]any{
		"resources": s.resourceRegistry.List(),
	})
}

func (s *Server) handleResourcesRead(ctx context.Context, req *mcpruntime.Request) *mcpruntime.Response {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.URI) == "" {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: missing uri")
	}

	contents, err := s.resourceRegistry.Read(ctx, params.URI)
	if err != nil {
		if strings.HasPrefix(err.Error(), "resource not found:") {
			return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, err.Error())
		}
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeServerError, err.Error())
	}

	return mcpruntime.NewResultResponse(req.ID, map[string]any{
		"contents": contents,
	})
}

func (s *Server) handlePromptsList(req *mcpruntime.Request) *mcpruntime.Response {
	return mcpruntime.NewResultResponse(req.ID, map[string]any{
		"prompts": s.promptRegistry.List(),
	})
}

func (s *Server) handlePromptsGet(ctx context.Context, req *mcpruntime.Request) *mcpruntime.Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, "Invalid params: missing name")
	}

	out, err := s.promptRegistry.Get(ctx, params.Name, params.Arguments)
	if err != nil {
		if strings.HasPrefix(err.Error(), "prompt not found:") {
			return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeInvalidParams, err.Error())
		}
		return mcpruntime.NewErrorResponse(req.ID, mcpruntime.CodeServerError, err.Error())
	}

	return mcpruntime.NewResultResponse(req.ID, out)
}

func (s *Server) toolCallError(ctx context.Context, reqID any, toolName string, err error) *mcpruntime.Response {
	if strings.HasPrefix(err.Error(), "tool not found:") {
		return mcpruntime.NewErrorResponse(reqID, mcpruntime.CodeInvalidParams, err.Error())
	}
	if isInvalidParams(err) {
		return mcpruntime.NewErrorResponse(reqID, mcpruntime.CodeInvalidParams, "Invalid params: "+err.Error())
	}

	if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		s.logger.ErrorContext(ctx, "tool timeout", "tool", toolName, "error", err)
		return mcpruntime.NewErrorResponse(reqID, mcpruntime.CodeServerError, fmt.Sprintf("tool %q timed out", toolName))
	}

	s.logger.ErrorContext(ctx, "tool error", "tool", toolName, "error", err)
	return mcpruntime.NewErrorResponse(reqID, mcpruntime.CodeServerError, err.Error())
}

func (s *Server) handleBatch(ctx context.Context, body []byte, sessionID string) (*apptheory.Response, error) {
	requests, err := mcpruntime.ParseBatchRequest(body)
	if err != nil {
		s.logger.ErrorContext(ctx, "batch parse error", "error", err)
		resp := mcpruntime.NewErrorResponse(nil, mcpruntime.CodeParseError, "Parse error: "+err.Error())
		return s.marshalSingleResponse(resp, sessionID)
	}

	responses := make([]*mcpruntime.Response, len(requests))
	for i, req := range requests {
		responses[i] = s.dispatch(ctx, req)
	}

	data, err := json.Marshal(responses)
	if err != nil {
		return nil, fmt.Errorf("marshal batch response: %w", err)
	}

	headers := map[string][]string{
		"content-type": {"application/json"},
	}
	if sessionID != "" {
		headers["mcp-session-id"] = []string{sessionID}
	}
	return &apptheory.Response{
		Status:  200,
		Headers: headers,
		Body:    data,
	}, nil
}

func (s *Server) marshalSingleResponse(resp *mcpruntime.Response, sessionID string) (*apptheory.Response, error) {
	data, err := mcpruntime.MarshalResponse(resp)
	if err != nil {
		return nil, err
	}
	headers := map[string][]string{
		"content-type": {"application/json"},
	}
	if sessionID != "" {
		headers["mcp-session-id"] = []string{sessionID}
	}
	return &apptheory.Response{
		Status:  200,
		Headers: headers,
		Body:    data,
	}, nil
}

func jsonRPCErrorResponse(id any, code int, message string, sessionID string) *apptheory.Response {
	resp := mcpruntime.NewErrorResponse(id, code, message)
	data, _ := mcpruntime.MarshalResponse(resp)
	headers := map[string][]string{
		"content-type": {"application/json"},
	}
	if sessionID != "" {
		headers["mcp-session-id"] = []string{sessionID}
	}
	return &apptheory.Response{Status: 200, Headers: headers, Body: data}
}

func marshalSSEResponse(resp *mcpruntime.Response, sessionID string) (*apptheory.Response, error) {
	out, err := apptheory.SSEResponse(200, apptheory.SSEEvent{
		Event: "message",
		Data:  resp,
	})
	if err != nil {
		return nil, err
	}
	if out.Headers == nil {
		out.Headers = map[string][]string{}
	}
	if sessionID != "" {
		out.Headers["mcp-session-id"] = []string{sessionID}
	}
	return out, nil
}

func wantsSSE(headers map[string][]string) bool {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "accept") {
			continue
		}
		for _, value := range values {
			v := strings.ToLower(strings.TrimSpace(value))
			if strings.Contains(v, "text/event-stream") {
				return true
			}
		}
	}
	return false
}

func trimLeftSpace(data []byte) []byte {
	for i, b := range data {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return data[i:]
		}
	}
	return nil
}

func firstHeader(headers map[string][]string, key string) string {
	for k, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(k), key) {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func extractBearerToken(headers map[string][]string) string {
	authHeader := firstHeader(headers, "authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.Fields(authHeader)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func sessionTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envSessionTTLMinutes))
	if raw != "" {
		if minutes, err := strconv.Atoi(raw); err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return time.Duration(defaultSessionTTLMinutes) * time.Minute
}

func (s *Server) resolveSession(ctx context.Context, sessionID string) (string, error) {
	now := time.Now().UTC()
	ttl := sessionTTL()

	if sessionID != "" {
		sess, err := s.sessionStore.Get(ctx, sessionID)
		switch {
		case err == nil:
			if !sess.ExpiresAt.IsZero() && now.After(sess.ExpiresAt) {
				_ = s.sessionStore.Delete(ctx, sessionID)
				break
			}

			sess.ExpiresAt = now.Add(ttl)
			if sess.CreatedAt.IsZero() {
				sess.CreatedAt = now
			}
			if putErr := s.sessionStore.Put(ctx, sess); putErr != nil {
				return "", fmt.Errorf("refresh session: %w", putErr)
			}

			return sessionID, nil
		case errors.Is(err, mcpruntime.ErrSessionNotFound):
			// fall through to create
		default:
			return "", fmt.Errorf("get session: %w", err)
		}
	}

	newID := s.idGen.NewID()
	sess := &mcpruntime.Session{
		ID:        newID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.sessionStore.Put(ctx, sess); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return newID, nil
}
