package auth

import "context"

type toolContextKey int

const (
	toolContextKeyPrincipal toolContextKey = iota
	toolContextKeyBearerToken
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

func BearerTokenFromToolContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val := ctx.Value(toolContextKeyBearerToken)
	out, _ := val.(string)
	return out
}
