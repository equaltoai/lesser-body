package mcpapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentshare"
	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func WithActorBinding(next apptheory.Handler) apptheory.Handler {
	return withActorBinding(next, agentshare.IsActive)
}

type actorShareGrantChecker func(context.Context, string, string) (bool, error)

func withActorBinding(next apptheory.Handler, shareGrantActive actorShareGrantChecker) apptheory.Handler {
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
		if actor == "" {
			return actorBindingForbidden()
		}
		if strings.EqualFold(strings.TrimSpace(ctx.AuthIdentity), actor) {
			return next(ctx)
		}

		principal := auth.PrincipalFromContext(ctx)
		if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || shareGrantActive == nil {
			return actorBindingForbidden()
		}
		shared, err := shareGrantActive(ctx.Context(), actor, ctx.AuthIdentity)
		if err != nil {
			return nil, fmt.Errorf("check actor share grant: %w", err)
		}
		if !shared {
			return actorBindingForbidden()
		}
		return next(ctx)
	}
}

func actorBindingForbidden() (*apptheory.Response, error) {
	return nil, &apptheory.AppError{
		Code:    "app.forbidden",
		Message: "forbidden",
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
