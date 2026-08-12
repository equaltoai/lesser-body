package mcpapp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestActorShareAdmissionLeavesOwnerPathUnchanged(t *testing.T) {
	checkerCalls := 0
	handler := withActorBinding(okActorHandler(t), func(context.Context, string, string) (bool, error) {
		checkerCalls++
		return false, nil
	})

	ctx := actorOAuthContext(" Arch ", "arch")
	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("owner admission changed: response=%+v err=%v", resp, err)
	}
	if checkerCalls != 0 {
		t.Fatalf("owner admission must not read a share grant, calls=%d", checkerCalls)
	}
}

func TestActorShareAdmissionAllowsActiveGrantWithoutImpersonation(t *testing.T) {
	checkerCalls := 0
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if ctx.AuthIdentity != "Alice" {
			t.Fatalf("shared admission must retain real caller identity, got %q", ctx.AuthIdentity)
		}
		if actorFromRequestContext(ctx) != "Arch" {
			t.Fatalf("shared admission must retain agent route context, got %q", actorFromRequestContext(ctx))
		}
		principal := auth.PrincipalFromContext(ctx)
		if principal == nil || principal.Identity != "Alice" {
			t.Fatalf("shared admission must retain authenticated principal, got %+v", principal)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}, func(_ context.Context, agent, grantee string) (bool, error) {
		checkerCalls++
		if agent != "Arch" || grantee != "Alice" {
			t.Fatalf("grant checker must receive route actor and real caller, got %q/%q", agent, grantee)
		}
		return true, nil
	})

	ctx := actorOAuthContext("Alice", "Arch")
	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("active share admission failed: response=%+v err=%v", resp, err)
	}
	if checkerCalls != 1 {
		t.Fatalf("expected exactly one per-request grant check, got %d", checkerCalls)
	}
}

func TestActorShareAdmissionMissingGrantMatchesExistingForbidden(t *testing.T) {
	missingHandler := withActorBinding(okActorHandler(t), func(context.Context, string, string) (bool, error) {
		return false, nil
	})
	existingHandler := withActorBinding(okActorHandler(t), nil)

	_, missingErr := missingHandler(actorOAuthContext("alice", "arch"))
	_, existingErr := existingHandler(actorOAuthContext("alice", "arch"))
	missingAppErr, missingOK := missingErr.(*apptheory.AppError)
	existingAppErr, existingOK := existingErr.(*apptheory.AppError)
	if !missingOK || !existingOK {
		t.Fatalf("expected AppError denials, missing=%T existing=%T", missingErr, existingErr)
	}
	if missingAppErr.Code != "app.forbidden" || missingAppErr.Code != existingAppErr.Code || missingAppErr.Message != existingAppErr.Message {
		t.Fatalf("missing grant must be indistinguishable from existing denial, missing=%+v existing=%+v", missingAppErr, existingAppErr)
	}
}

func TestActorShareAdmissionReadFailureFailsClosedOperationally(t *testing.T) {
	handler := withActorBinding(okActorHandler(t), func(context.Context, string, string) (bool, error) {
		return false, errors.New("dynamodb timeout")
	})

	resp, err := handler(actorOAuthContext("alice", "arch"))
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

func TestActorShareAdmissionRechecksRevocationOnNextRequest(t *testing.T) {
	checks := 0
	nextCalls := 0
	handler := withActorBinding(func(*apptheory.Context) (*apptheory.Response, error) {
		nextCalls++
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}, func(context.Context, string, string) (bool, error) {
		checks++
		return checks == 1, nil
	})

	if resp, err := handler(actorOAuthContext("alice", "arch")); err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("first active request failed: response=%+v err=%v", resp, err)
	}
	resp, err := handler(actorOAuthContext("alice", "arch"))
	if resp != nil {
		t.Fatalf("revoked next request must be denied, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("revoked next request must use existing 403 error, got %T %+v", err, err)
	}
	if checks != 2 || nextCalls != 1 {
		t.Fatalf("expected two uncached checks and one dispatch, checks=%d dispatches=%d", checks, nextCalls)
	}
}

func TestActorShareAdmissionLeavesX402PathUntouched(t *testing.T) {
	checkerCalls := 0
	handler := withActorBinding(okActorHandler(t), func(context.Context, string, string) (bool, error) {
		checkerCalls++
		return false, nil
	})
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
	if checkerCalls != 0 {
		t.Fatalf("x402 path must not consult actor share grants, calls=%d", checkerCalls)
	}
}

func TestSharedEmailSendAuditAndToolContextNameRealCallerAndAgent(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		principal := auth.PrincipalFromToolContext(ctx.Context())
		if principal == nil || principal.Identity != "alice" {
			t.Fatalf("email send tool context must name real caller, got %+v", principal)
		}
		resolved, ok := runtimepolicy.FromContext(ctx.Context())
		if !ok || resolved.Profile != runtimepolicy.ProfileSouled || resolved.SoulAgentID != "agent-arch" {
			t.Fatalf("email send tool context must preserve resolved agent, got %+v/%v", resolved, ok)
		}
		if actorFromRequestContext(ctx) != "arch" {
			t.Fatalf("email send route agent changed: %q", actorFromRequestContext(ctx))
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
	handler := withActorBinding(WithAudit(WithToolContext(next), logger), func(context.Context, string, string) (bool, error) {
		return true, nil
	})

	ctx := actorOAuthContext("alice", "arch")
	ctx.Request.Headers = map[string][]string{"authorization": {"Bearer caller-token"}}
	ctx.Request.Body = []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"email_send","arguments":{"to":"recipient@example.com","subject":"hello","body":"body"}}}`)
	setRequestContext(ctx, runtimepolicy.WithContext(context.Background(), runtimepolicy.Resolved{
		Profile:               runtimepolicy.ProfileSouled,
		Determined:            true,
		DeterminationSource:   "soulbinding_present",
		BoundSoul:             true,
		SoulAgentID:           "agent-arch",
		CommunicationsEnabled: true,
	}))

	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("shared email send pipeline failed: response=%+v err=%v", resp, err)
	}
	logged := logs.String()
	for _, want := range []string{`"msg":"mcp tool call"`, `"identity":"alice"`, `"actor":"arch"`, `"tool":"email_send"`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("shared email audit missing %s: %s", want, logged)
		}
	}
	if strings.Contains(logged, "recipient@example.com") || strings.Contains(logged, "caller-token") {
		t.Fatalf("audit must not leak comms PII or bearer token: %s", logged)
	}
}

func actorOAuthContext(identity, actor string) *apptheory.Context {
	ctx := &apptheory.Context{
		AuthIdentity: identity,
		Params:       map[string]string{"actor": actor},
		Request:      apptheory.Request{Path: "/mcp/" + actor},
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: identity,
		Claims: &auth.Claims{
			Username: identity,
			Scopes:   []string{"read", "write"},
		},
	})
	return ctx
}

func okActorHandler(t testing.TB) apptheory.Handler {
	t.Helper()
	return func(*apptheory.Context) (*apptheory.Response, error) {
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
}
