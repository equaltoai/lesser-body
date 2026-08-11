package auth

import (
	"context"
	"strings"
)

type toolContextKey int

const (
	toolContextKeyPrincipal toolContextKey = iota
	toolContextKeyBearerToken
	toolContextKeyRequestID
	toolContextKeyRequestSnapshot
	toolContextKeyActor
)

// ToolRequestSnapshot carries the sanitized-by-construction request envelope
// needed by tool-internal gates that must bind authorization to the exact MCP
// request. It may contain sensitive header values such as raw x402 grant tokens
// and payment evidence; callers must not log or persist it.
type ToolRequestSnapshot struct {
	Headers map[string][]string
	Body    []byte
	Path    string
}

func InjectToolContext(ctx context.Context, principal *Principal, bearerToken string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if principal != nil {
		ctx = context.WithValue(ctx, toolContextKeyPrincipal, principal)
	}
	if bearerToken != "" {
		ctx = context.WithValue(ctx, toolContextKeyBearerToken, bearerToken)
	}
	return ctx
}

func PrincipalFromToolContext(ctx context.Context) *Principal {
	if ctx == nil {
		return nil
	}
	val := ctx.Value(toolContextKeyPrincipal)
	p, _ := val.(*Principal)
	return p
}

func X402GrantFromToolContext(ctx context.Context) *X402InvocationGrant {
	principal := PrincipalFromToolContext(ctx)
	if principal == nil || principal.Type != PrincipalTypeX402Grant {
		return nil
	}
	return principal.X402Grant
}

func BearerTokenFromToolContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val := ctx.Value(toolContextKeyBearerToken)
	out, _ := val.(string)
	return out
}

func WithToolRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	if requestID != "" {
		ctx = context.WithValue(ctx, toolContextKeyRequestID, requestID)
	}
	return ctx
}

func RequestIDFromToolContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val := ctx.Value(toolContextKeyRequestID)
	out, _ := val.(string)
	return out
}

// WithToolActor records the resolved /mcp/{actor} route value (normalized
// lowercase) in the tool context so agent-scoped tools can resolve the agent
// context from the actor when the authenticated caller differs from it — the
// share-grant caller case admitted by the actor-binding middleware.
func WithToolActor(ctx context.Context, actor string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	actor = strings.ToLower(strings.TrimSpace(actor))
	if actor != "" {
		ctx = context.WithValue(ctx, toolContextKeyActor, actor)
	}
	return ctx
}

func ActorFromToolContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val := ctx.Value(toolContextKeyActor)
	out, _ := val.(string)
	return out
}

func InjectToolRequestSnapshot(ctx context.Context, snapshot ToolRequestSnapshot) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	copied := ToolRequestSnapshot{
		Headers: cloneHeaders(snapshot.Headers),
		Body:    append([]byte(nil), snapshot.Body...),
		Path:    strings.TrimSpace(snapshot.Path),
	}
	return context.WithValue(ctx, toolContextKeyRequestSnapshot, copied)
}

func RequestSnapshotFromToolContext(ctx context.Context) ToolRequestSnapshot {
	if ctx == nil {
		return ToolRequestSnapshot{}
	}
	snapshot, _ := ctx.Value(toolContextKeyRequestSnapshot).(ToolRequestSnapshot)
	return ToolRequestSnapshot{
		Headers: cloneHeaders(snapshot.Headers),
		Body:    append([]byte(nil), snapshot.Body...),
		Path:    strings.TrimSpace(snapshot.Path),
	}
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}
