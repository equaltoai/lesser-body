package mcpapp

import (
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
)

func WithActorBinding(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		actor := actorFromRequestContext(ctx)
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeX402Grant {
			if x402GrantMatchesActor(ctx, principal.X402Grant, actor) {
				return next(ctx)
			}
			return nil, &apptheory.AppError{
				Code:    "app.forbidden",
				Message: "forbidden",
			}
		}
		if actor == "" || !strings.EqualFold(strings.TrimSpace(ctx.AuthIdentity), actor) {
			return nil, &apptheory.AppError{
				Code:    "app.forbidden",
				Message: "forbidden",
			}
		}
		return next(ctx)
	}
}

func x402GrantMatchesActor(ctx *apptheory.Context, grant *auth.X402InvocationGrant, actor string) bool {
	if grant == nil {
		return false
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(grant.Actor), actor) {
		return true
	}
	expectedAgentID := expectedAgentIDForX402Request(ctx, actor)
	if strings.TrimSpace(grant.Actor) == "" && expectedAgentID != "" && strings.EqualFold(strings.TrimSpace(grant.AgentID), expectedAgentID) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(grant.AgentID), actor) {
		return true
	}
	return false
}
