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
)

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
