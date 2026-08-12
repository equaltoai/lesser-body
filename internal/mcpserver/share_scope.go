package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

// shareGrantActorCaller classifies the tool context for actor-scoped identity
// resolution. The actor route value is injected into the tool context only for
// admitted requests (owner, active share grantee, or x402 grant), and the
// OAuth principal-type gate keeps the x402 payment path on its pre-existing
// behavior. shared is true exactly when an OAuth caller's normalized identity
// differs from the normalized actor route value — the share-grant caller case
// admitted per request by the actor-binding middleware.
func shareGrantActorCaller(ctx context.Context) (actor string, caller string, shared bool) {
	actor = auth.ActorFromToolContext(ctx)
	if actor == "" {
		return "", "", false
	}
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken {
		return actor, "", false
	}
	caller = strings.TrimSpace(principal.Identity)
	if caller == "" && principal.Claims != nil {
		caller = strings.TrimSpace(principal.Claims.GetUsername())
	}
	caller = normalizeLocalAgentUsername(caller)
	if caller == "" || caller == actor {
		return actor, caller, false
	}
	return actor, caller, true
}

// actingMemoryScopeIdentity resolves which body-owned memory-store partition a
// memory or cursor operation applies to. Share-grant callers operate on the
// actor's partition: the route's memory scope belongs to the agent, and the
// grantee is admitted to drive the agent's surface. Owner requests, non-OAuth
// principals (x402), and contexts without an actor route value keep the exact
// pre-existing caller-identity behavior, including the "missing identity"
// error shape.
func actingMemoryScopeIdentity(ctx context.Context) (string, error) {
	if actor, _, shared := shareGrantActorCaller(ctx); shared {
		return actor, nil
	}
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return "", fmt.Errorf("missing identity")
	}
	return strings.TrimSpace(p.Identity), nil
}

// requireOwnerScopedOAuthBearer gates tools that proxy the caller's own lesser
// user bearer token to identity-scoped lesser REST/GraphQL endpoints that have
// NO upstream act-as surface under lesser's agent-share-act-as contract
// (docs/contracts/agent-share-act-as.md: deliberate exclusions plus every
// unlisted endpoint). On the share-grant path these tools cannot honor the
// /mcp/{actor} scope: proxying would silently act on lesser as the caller, not
// the agent. Those calls fail closed with an explicit 403; the same operations
// stay available to the caller on their own actor route. Tools whose upstream
// surface IS act-as-enabled use requireActAsScopedOAuthBearer instead. Owner
// requests (caller == actor), non-OAuth principals, and contexts without an
// actor route value fall through to the exact pre-existing requireOAuthBearer
// behavior.
func requireOwnerScopedOAuthBearer(ctx context.Context) (string, error) {
	if actor, caller, shared := shareGrantActorCaller(ctx); shared {
		return "", &mcpAuthFailure{
			Code:    "forbidden",
			Message: "shared callers cannot drive " + actor + "'s lesser account: the upstream lesser contract scopes this call to the caller's own bearer token and defines no actor delegation",
			Status:  403,
			Details: map[string]any{
				"reason": "caller_token_scoped_upstream",
				"actor":  actor,
				"caller": caller,
			},
		}
	}
	return requireOAuthBearer(ctx)
}

// requireActAsScopedOAuthBearer resolves the caller's bearer for tools whose
// upstream lesser surface is act-as-enabled per lesser's agent-share-act-as
// contract. On the share-grant path it returns the caller's own bearer together
// with a context carrying the X-Lesser-Act-As indicator naming the actor route
// value; lesser then performs its per-request ConsistentRead grant check,
// scopes the call to the agent, and records the caller as actedBy attribution.
// Upstream 400/403/500 rejections propagate as tool errors with the upstream
// reason preserved — there is no retry or fallback to owner semantics. Owner
// requests (caller == actor), non-OAuth principals (x402), and contexts
// without an actor route value fall through to the exact pre-existing
// requireOAuthBearer behavior with the context returned unchanged, so the
// owner path stays byte-identical and never carries the header.
func requireActAsScopedOAuthBearer(ctx context.Context) (context.Context, string, error) {
	if actor, _, shared := shareGrantActorCaller(ctx); shared {
		token, err := requireOAuthBearer(ctx)
		if err != nil {
			return ctx, "", err
		}
		return lesserapi.WithActAs(ctx, actor), token, nil
	}
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return ctx, "", err
	}
	return ctx, token, nil
}
