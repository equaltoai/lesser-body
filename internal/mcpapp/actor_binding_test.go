package mcpapp

import (
	"context"
	"errors"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestWithActorBinding_OwnerAdmitsWithoutShareMarker(t *testing.T) {
	var admitCalls int
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); shared || caller != "" {
			t.Fatalf("owner admission must not thread a share caller, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}, func(ctx context.Context, actor, bearer string) (string, error) {
		admitCalls++
		if actor != "arch" || bearer != "caller-token" {
			t.Fatalf("admission must receive route actor and caller bearer, got %q/%q", actor, bearer)
		}
		return "owner", nil
	})

	resp, err := handler(actorOAuthContextWithToken("arch", "owner", "caller-token"))
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("owner admission: response=%+v err=%v", resp, err)
	}
	if admitCalls != 1 {
		t.Fatalf("expected exactly one admission call, got %d", admitCalls)
	}
}

func TestWithActorBinding_GranteeAdmitsAndThreadsShareCaller(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		principal := auth.PrincipalFromContext(ctx)
		if principal == nil || principal.Identity != "arch" {
			t.Fatalf("shared admission must keep the agent as the token subject, got %+v", principal)
		}
		if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); !shared || caller != "alice" {
			t.Fatalf("grantee admission must thread the share caller, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}, func(ctx context.Context, actor, bearer string) (string, error) {
		if actor != "arch" || bearer != "caller-token" {
			t.Fatalf("admission must receive route actor and caller bearer, got %q/%q", actor, bearer)
		}
		return "grantee", nil
	})

	resp, err := handler(actorOAuthContextWithToken("arch", "@alice", "caller-token"))
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("grantee admission: response=%+v err=%v", resp, err)
	}
}

func TestWithActorBinding_AdmissionErrorFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"lesser 403", errors.New("lesser api error (status=403)")},
		{"lesser 401", errors.New("lesser api error (status=401)")},
		{"transport error", errors.New("request failed: connection refused")},
		{"timeout", context.DeadlineExceeded},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
				t.Fatal("error path must not reach the handler")
				return nil, nil
			}, func(ctx context.Context, actor, bearer string) (string, error) {
				return "", tc.err
			})

			resp, err := handler(actorOAuthContextWithToken("arch", "alice", "caller-token"))
			if resp != nil {
				t.Fatalf("error path must not return a success response: %+v", resp)
			}
			appErr, ok := err.(*apptheory.AppError)
			if !ok || appErr.Code != "app.forbidden" {
				t.Fatalf("error path must fail closed with the existing 403, got %T %+v", err, err)
			}
		})
	}
}

func TestWithActorBinding_MalformedRelationshipFailsClosed(t *testing.T) {
	for _, relationship := range []string{"", "admin", "member"} {
		relationship := relationship
		t.Run("relationship_"+relationship, func(t *testing.T) {
			handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
				t.Fatal("malformed relationship must not reach the handler")
				return nil, nil
			}, func(ctx context.Context, actor, bearer string) (string, error) {
				return relationship, nil
			})

			resp, err := handler(actorOAuthContextWithToken("arch", "alice", "caller-token"))
			if resp != nil {
				t.Fatalf("malformed relationship must not return success: %+v", resp)
			}
			appErr, ok := err.(*apptheory.AppError)
			if !ok || appErr.Code != "app.forbidden" {
				t.Fatalf("malformed relationship must fail closed with 403, got %T %+v", err, err)
			}
		})
	}
}

func TestWithActorBinding_NilAdmissionFailsClosed(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("nil admission must not reach the handler")
		return nil, nil
	}, nil)

	resp, err := handler(actorOAuthContextWithToken("arch", "alice", "caller-token"))
	if resp != nil {
		t.Fatalf("nil admission must not return success: %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("nil admission must fail closed with 403, got %T %+v", err, err)
	}
}

func TestWithActorBinding_AbsentDelegatedByDeniedBeforeAdmission(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("absent DelegatedBy must not reach the handler")
		return nil, nil
	}, func(ctx context.Context, actor, bearer string) (string, error) {
		t.Fatal("absent DelegatedBy must not call admission")
		return "", nil
	})

	resp, err := handler(actorOAuthContextWithToken("arch", "", "caller-token"))
	if resp != nil {
		t.Fatalf("absent DelegatedBy must not admit: %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("absent DelegatedBy must deny with the existing 403, got %T %+v", err, err)
	}
}

func TestWithActorBinding_AbsentBearerDeniedBeforeAdmission(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("absent bearer must not reach the handler")
		return nil, nil
	}, func(ctx context.Context, actor, bearer string) (string, error) {
		t.Fatal("absent bearer must not call admission")
		return "", nil
	})

	ctx := actorOAuthContextWithToken("arch", "alice", "")
	ctx.Request.Headers = nil
	resp, err := handler(ctx)
	if resp != nil {
		t.Fatalf("absent bearer must not admit: %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("absent bearer must deny with the existing 403, got %T %+v", err, err)
	}
}

func TestWithActorBinding_LeavesX402PathUntouched(t *testing.T) {
	handler := withActorBinding(okActorHandler(t),
		func(ctx context.Context, actor, bearer string) (string, error) {
			t.Fatal("admission must not run on x402 path")
			return "", nil
		},
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
}

func TestWithActorBinding_RejectsMissingActor(t *testing.T) {
	handler := withActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("next handler should not be called")
		return nil, nil
	}, func(ctx context.Context, actor, bearer string) (string, error) {
		t.Fatal("admission must not run without actor")
		return "", nil
	})

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

func actorOAuthContextWithToken(actor, delegatedBy, bearer string) *apptheory.Context {
	ctx := &apptheory.Context{
		AuthIdentity: actor,
		Params:       map[string]string{"actor": actor},
		Request:      apptheory.Request{Path: "/mcp/" + actor},
	}
	if bearer != "" {
		ctx.Request.Headers = map[string][]string{"authorization": {"Bearer " + bearer}}
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
