package mcpapp

import (
	"context"
	"errors"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentshare"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

// actorAdmission resolves the caller's relationship to the routed actor via
// Lesser's actor-admission endpoint. It returns the relationship exactly as
// Lesser reported it ("owner" or "grantee") or an error. The middleware admits
// only those two values and fails closed on every other outcome.
type actorAdmission func(ctx context.Context, actor, bearerToken string) (string, error)

var actorAdmissionForTests actorAdmission

// SetActorAdmissionForTests replaces the production Lesser admission call for
// tests and returns a restore function. Passing nil restores the production
// Lesser call.
func SetActorAdmissionForTests(fn actorAdmission) func() {
	prev := actorAdmissionForTests
	actorAdmissionForTests = fn
	return func() { actorAdmissionForTests = prev }
}

func WithActorBinding(next apptheory.Handler) apptheory.Handler {
	admit := actorAdmissionViaLesser
	if actorAdmissionForTests != nil {
		admit = actorAdmissionForTests
	}
	return withActorBinding(next, admit)
}

func actorAdmissionViaLesser(ctx context.Context, actor, bearerToken string) (string, error) {
	client, err := lesserapi.Default()
	if err != nil {
		return "", err
	}
	resp, err := client.GetActorAccess(ctx, actor, bearerToken)
	if err != nil {
		return "", err
	}
	// A 200 with authorized=false must deny: body asks lesser for the explicit
	// decision and must not re-infer it from the relationship alone.
	if !resp.Authorized {
		return "", errors.New("lesser denied actor access")
	}
	return strings.TrimSpace(resp.Relationship), nil
}

func withActorBinding(next apptheory.Handler, admit actorAdmission) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		actor := agentshare.NormalizePrincipalUsername(actorFromRequestContext(ctx))
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeX402Grant {
			if x402GrantMatchesActor(ctx, principal.X402Grant, actor) {
				return next(ctx)
			}
			return actorBindingForbidden()
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeInstanceKey {
			// The deprecated managed-instance-key path authenticates as the
			// "instance" identity and is admitted only on the instance actor
			// route; it has no DelegatedBy and never consults Lesser admission.
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
		// authorizing human rides in DelegatedBy; key on it and forward the
		// caller's own bearer to Lesser for the authoritative decision.
		delegatedBy := agentshare.NormalizePrincipalUsername(claimsDelegatedBy(principal))
		if delegatedBy == "" {
			// Absent or unreadable DelegatedBy must fail closed. It must never
			// fall back to admitting the request as the owner.
			return actorBindingForbidden()
		}

		bearerToken, ok := auth.BearerTokenFromRequest(ctx)
		if !ok || strings.TrimSpace(bearerToken) == "" {
			return actorBindingForbidden()
		}

		if admit == nil {
			return actorBindingForbidden()
		}
		relationship, err := admit(ctx.Context(), actor, bearerToken)
		if err != nil {
			// 401, transport error, timeout, malformed body, and any other
			// non-200 outcome all fail closed here. Never admit on an error
			// path and never fall back to admitting as owner.
			return actorBindingForbidden()
		}

		switch relationship {
		case "owner":
			return next(ctx)
		case "grantee":
			// Grantee path: record the resolved driver so downstream act-as,
			// memory-partition, and attribution seams thread it without
			// re-resolving the grant. The value lands on the apptheory request
			// and the per-POST tool-context hook folds it into the tool
			// context; the former reflect+unsafe request-context override is
			// gone.
			mcpserver.SetShareCallerOnAppTheoryContext(ctx, delegatedBy)
			return next(ctx)
		default:
			return actorBindingForbidden()
		}
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
