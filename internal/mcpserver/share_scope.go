package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

type shareCallerContextKey struct{}

// WithShareCaller marks a tool context as admitted through the share-grant path
// with the given normalized real-caller username. Only the actor-binding
// middleware sets this marker, and only after Lesser's actor-admission endpoint
// reports relationship "grantee", so its presence is the admission
// classification rather than a re-derived guess. Owner requests, non-OAuth
// principals, and actor-less contexts never carry the marker.
func WithShareCaller(ctx context.Context, caller string) context.Context {
	caller = strings.TrimSpace(caller)
	if ctx == nil || caller == "" {
		return ctx
	}
	return context.WithValue(ctx, shareCallerContextKey{}, caller)
}

// ShareCallerFromContext returns the share-grant caller recorded by
// WithShareCaller, or false when the context carries no share marker.
func ShareCallerFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	caller, ok := ctx.Value(shareCallerContextKey{}).(string)
	return strings.TrimSpace(caller), ok
}

// shareGrantActorCaller classifies the tool context for actor-scoped identity
// resolution. The actor route value is injected into the tool context only for
// admitted requests, and the OAuth principal-type gate keeps the x402 payment
// path on its pre-existing behavior. shared is true exactly when the
// actor-binding middleware admitted the request through the share-grant path
// and recorded the grantee caller via WithShareCaller — never by comparing the
// token subject (which is always the agent) against the actor.
func shareGrantActorCaller(ctx context.Context) (actor string, caller string, shared bool) {
	actor = auth.ActorFromToolContext(ctx)
	if actor == "" {
		return "", "", false
	}
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken {
		return actor, "", false
	}
	if caller, ok := ShareCallerFromContext(ctx); ok {
		return actor, caller, true
	}
	return actor, "", false
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

// requireOwnerScopedOAuthBearer resolves the caller's OAuth bearer for tools
// and resources whose upstream lesser surface has no X-Lesser-Act-As
// indicator (docs/contracts/agent-share-act-as.md: the deliberate exclusions
// plus every unlisted endpoint). Under the shipped identity model
// (lesser#1397) the token subject is always the agent — the account owner —
// with the authorizing human carried in DelegatedBy, so proxying the caller's
// bearer to lesser authenticates as the agent on the owner path and the
// share-grant path alike. The pre-M1 premise that a grantee's bearer carried
// the grantee as subject (and therefore proxying would act as the caller, not
// the agent) no longer holds, so the former share-caller refusal is removed:
// there is no caller/agent split left to refuse, and these surfaces carry no
// ActedBy attribution (that is requireActAsScopedOAuthBearer's job). The
// historical name is retained to keep the call sites stable; "owner-scoped"
// now describes the bearer's subject (the account owner, i.e. the agent), not
// a caller-is-owner gate. Owner requests, non-OAuth principals (x402), and
// actor-less contexts are unaffected: this is exactly requireOAuthBearer.
func requireOwnerScopedOAuthBearer(ctx context.Context) (string, error) {
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
