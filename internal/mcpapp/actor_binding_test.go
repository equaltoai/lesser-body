package mcpapp

import (
	"context"
	"errors"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestWithActorBinding_OwnerPathAdmitsWithoutGrantCheck(t *testing.T) {
	grantChecks := 0
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) {
			grantChecks++
			return false, nil
		},
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "owner"))
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("owner admission: response=%+v err=%v", resp, err)
	}
	if grantChecks != 0 {
		t.Fatalf("owner path must not consult the share grant, calls=%d", grantChecks)
	}
}

func TestWithActorBinding_GranteeActiveAdmittedAndMarked(t *testing.T) {
	grantChecks := 0
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		principal := auth.PrincipalFromContext(ctx)
		if principal == nil || principal.Identity != "arch" {
			t.Fatalf("shared admission must keep the agent as the token subject, got %+v", principal)
		}
		if principal.Claims == nil || principal.Claims.DelegatedBy != "@alice" {
			t.Fatalf("shared admission must retain DelegatedBy, got %+v", principal)
		}
		if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); !shared || caller != "alice" {
			t.Fatalf("grantee admission must thread the share caller, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	},
		func(context.Context, string) (string, error) { return "owner", nil },
		func(_ context.Context, agent, grantee string) (bool, error) {
			grantChecks++
			if agent != "arch" || grantee != "alice" {
				t.Fatalf("grant checker must receive route actor and DelegatedBy, got %q/%q", agent, grantee)
			}
			return true, nil
		},
	)

	ctx := actorOAuthContextWithDelegatedBy("arch", "@alice")
	ctx.Request.Headers = map[string][]string{"authorization": {"Bearer caller-token"}}
	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("active share admission: response=%+v err=%v", resp, err)
	}
	if grantChecks != 1 {
		t.Fatalf("expected exactly one per-request grant check, got %d", grantChecks)
	}
}

func TestWithActorBinding_GranteeRevokedDeniedOnNextRequest(t *testing.T) {
	checks := 0
	nextCalls := 0
	handler := withActorBinding(func(*apptheory.Context) (*apptheory.Response, error) {
		nextCalls++
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	},
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) {
			checks++
			return checks == 1, nil
		},
	)

	if resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "alice")); err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("first active request failed: response=%+v err=%v", resp, err)
	}
	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "alice"))
	if resp != nil {
		t.Fatalf("revoked next request must be denied, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("revoked next request must use the existing 403 error, got %T %+v", err, err)
	}
	if checks != 2 || nextCalls != 1 {
		t.Fatalf("expected two uncached checks and one dispatch, checks=%d dispatches=%d", checks, nextCalls)
	}
}

func TestWithActorBinding_GranteeMissingGrantMatchesExistingForbidden(t *testing.T) {
	missingHandler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) { return false, nil },
	)
	existingHandler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "owner", nil },
		nil,
	)

	_, missingErr := missingHandler(actorOAuthContextWithDelegatedBy("arch", "alice"))
	_, existingErr := existingHandler(actorOAuthContextWithDelegatedBy("arch", "alice"))
	missingAppErr, missingOK := missingErr.(*apptheory.AppError)
	existingAppErr, existingOK := existingErr.(*apptheory.AppError)
	if !missingOK || !existingOK {
		t.Fatalf("expected AppError denials, missing=%T existing=%T", missingErr, existingErr)
	}
	if missingAppErr.Code != "app.forbidden" || missingAppErr.Code != existingAppErr.Code || missingAppErr.Message != existingAppErr.Message {
		t.Fatalf("missing grant must be indistinguishable from existing denial, missing=%+v existing=%+v", missingAppErr, existingAppErr)
	}
}

func TestWithActorBinding_GrantReadFailureFailsClosedOperationally(t *testing.T) {
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) { return false, errors.New("dynamodb timeout") },
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "alice"))
	if resp != nil {
		t.Fatalf("read failure must not return success response: %+v", resp)
	}
	if err == nil {
		t.Fatal("read failure must surface an operational error")
	}
	if appErr, ok := err.(*apptheory.AppError); ok && appErr.Code == "app.forbidden" {
		t.Fatalf("operational failure must not be collapsed into a missing-grant signal: %+v", appErr)
	}
}

func TestWithActorBinding_OwnerReadFailureFailsClosed(t *testing.T) {
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "", errors.New("dynamodb timeout") },
		func(context.Context, string, string) (bool, error) {
			t.Fatal("grant check must not run after owner read failure")
			return false, nil
		},
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "owner"))
	if resp != nil {
		t.Fatalf("owner read failure must not return success: %+v", resp)
	}
	if err == nil {
		t.Fatal("owner read failure must surface an operational error")
	}
}

func TestWithActorBinding_EmptyOwnerFailsClosed(t *testing.T) {
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) { return "  ", nil },
		func(context.Context, string, string) (bool, error) {
			t.Fatal("grant check must not run for an empty owner")
			return false, nil
		},
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "owner"))
	if resp != nil {
		t.Fatalf("empty owner must not admit: %+v", resp)
	}
	if err == nil {
		t.Fatal("empty owner must fail closed with an operational error")
	}
}

func TestWithActorBinding_AbsentDelegatedByDenied(t *testing.T) {
	grantChecks := 0
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) {
			t.Fatal("owner resolver must not run for absent DelegatedBy")
			return "", nil
		},
		func(context.Context, string, string) (bool, error) { grantChecks++; return true, nil },
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", ""))
	if resp != nil {
		t.Fatalf("absent DelegatedBy must not admit: %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("absent DelegatedBy must deny with the existing 403 error, got %T %+v", err, err)
	}
	if grantChecks != 0 {
		t.Fatalf("absent DelegatedBy must not consult the share grant, calls=%d", grantChecks)
	}
}

func TestWithActorBinding_LeavesX402PathUntouched(t *testing.T) {
	grantChecks := 0
	handler := withActorBinding(okActorHandler(t),
		func(context.Context, string) (string, error) {
			t.Fatal("owner resolver must not run on x402 path")
			return "", nil
		},
		func(context.Context, string, string) (bool, error) { grantChecks++; return false, nil },
	)
	ctx := &apptheory.Context{
		AuthIdentity: "public-paid",
		Params:       map[string]string{"actor": "arch"},
		Request:      apptheory.Request{Path: "/mcp/arch"},
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:     auth.PrincipalTypeX402Grant,
		Identity: "public-paid",
		X402Grant: &auth.X402InvocationGrant{
			Actor: "arch",
		},
	})

	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("matching x402 grant path changed: response=%+v err=%v", resp, err)
	}
	if grantChecks != 0 {
		t.Fatalf("x402 path must not consult actor share grants, calls=%d", grantChecks)
	}
}

func TestWithActorBinding_RejectsMissingActor(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("next handler should not be called")
		return nil, nil
	},
		func(context.Context, string) (string, error) {
			t.Fatal("owner resolver must not run without actor")
			return "", nil
		},
		func(context.Context, string, string) (bool, error) {
			t.Fatal("grant check must not run without actor")
			return false, nil
		},
	)

	resp, err := handler(&apptheory.Context{
		AuthIdentity: "arch",
		Request:      apptheory.Request{Path: "/mcp"},
	})
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("expected AppError forbidden, got %T %+v", err, err)
	}
}

func actorOAuthContextWithDelegatedBy(actor, delegatedBy string) *apptheory.Context {
	ctx := &apptheory.Context{
		AuthIdentity: actor,
		Params:       map[string]string{"actor": actor},
		Request:      apptheory.Request{Path: "/mcp/" + actor},
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: actor,
		Claims: &auth.Claims{
			Username:    actor,
			DelegatedBy: delegatedBy,
			Scopes:      []string{"read", "write"},
		},
	})
	return ctx
}
