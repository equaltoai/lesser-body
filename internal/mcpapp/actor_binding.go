package mcpapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentshare"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func WithActorBinding(next apptheory.Handler) apptheory.Handler {
	return withActorBinding(next, agentshare.AgentOwner, agentshare.IsActive)
}

type actorOwnerResolver func(context.Context, string) (string, error)
type actorShareGrantChecker func(context.Context, string, string) (bool, error)

func withActorBinding(next apptheory.Handler, ownerResolver actorOwnerResolver, shareGrantActive actorShareGrantChecker) apptheory.Handler {
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
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeInstanceKey {
			// The deprecated managed-instance-key path authenticates as the
			// "instance" identity and is admitted only on the instance actor
			// route; it has no DelegatedBy and never consults a share grant.
			if strings.EqualFold(strings.TrimSpace(actor), strings.TrimSpace(principal.Identity)) {
				return next(ctx)
			}
			return actorBindingForbidden()
		}
		if actor == "" {
			return actorBindingForbidden()
		}

		principal := auth.PrincipalFromContext(ctx)
		if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken {
			return actorBindingForbidden()
		}

		// The token subject is always the agent in the settled identity model,
		// so AuthIdentity no longer distinguishes owner from grantee. The
		// authorizing human rides in DelegatedBy; key on it.
		delegatedBy := agentshare.NormalizePrincipalUsername(claimsDelegatedBy(principal))
		if delegatedBy == "" {
			// Absent or unreadable DelegatedBy must fail closed. It must never
			// fall back to admitting the request as the owner.
			return actorBindingForbidden()
		}

		owner, err := ownerResolver(ctx.Context(), actor)
		if err != nil {
			return nil, fmt.Errorf("resolve actor owner: %w", err)
		}
		owner = agentshare.NormalizePrincipalUsername(owner)
		if owner == "" {
			return nil, fmt.Errorf("resolve actor owner: empty owner for %q", actor)
		}

		if delegatedBy == owner {
			// Owner path: admit without consulting the share grant.
			return next(ctx)
		}

		if shareGrantActive == nil {
			return actorBindingForbidden()
		}
		shared, err := shareGrantActive(ctx.Context(), actor, delegatedBy)
		if err != nil {
			return nil, fmt.Errorf("check actor share grant: %w", err)
		}
		if !shared {
			return actorBindingForbidden()
		}

		// Grantee path: record the resolved driver so downstream act-as,
		// memory-partition, and attribution seams thread it without re-reading
		// the grant or re-resolving the owner.
		setRequestContext(ctx, mcpserver.WithShareCaller(ctx.Context(), delegatedBy))
		return next(ctx)
	}
}

func actorBindingForbidden() (*apptheory.Response, error) {
	return nil, &apptheory.AppError{
		Code:    "app.forbidden",
		Message: "forbidden",
	}
}

func claimsDelegatedBy(principal *auth.Principal) string {
	if principal == nil || principal.Claims == nil {
		return ""
	}
	return strings.TrimSpace(principal.Claims.DelegatedBy)
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
