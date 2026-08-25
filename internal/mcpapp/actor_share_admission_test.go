package mcpapp

import (
	"context"
	"testing"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

func TestGranteeAdmissionThreadsShareCallerThroughToolContext(t *testing.T) {
	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		// The per-POST tool-context hook derives the handler context from the
		// apptheory request; the actor-binding middleware recorded the grantee
		// caller on the request and the hook folds it into the tool context.
		toolCtx := ToolContextHook(ctx, ctx.Context())
		caller, shared := mcpserver.ShareCallerFromContext(toolCtx)
		if !shared || caller != "alice" {
			t.Fatalf("grantee admission must thread the share caller into the tool context, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
	handler := withActorBinding(next,
		func(ctx context.Context, actor, bearer string) (string, error) {
			return "grantee", nil
		},
	)

	resp, err := handler(actorOAuthContextWithToken("arch", "@alice", "caller-token"))
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("grantee pipeline failed: response=%+v err=%v", resp, err)
	}
}

func TestOwnerAdmissionNeverThreadsShareCaller(t *testing.T) {
	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		toolCtx := ToolContextHook(ctx, ctx.Context())
		if caller, shared := mcpserver.ShareCallerFromContext(toolCtx); shared || caller != "" {
			t.Fatalf("owner admission must not thread a share caller, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
	handler := withActorBinding(next,
		func(ctx context.Context, actor, bearer string) (string, error) {
			return "owner", nil
		},
	)

	resp, err := handler(actorOAuthContextWithToken("arch", "owner", "caller-token"))
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("owner pipeline failed: response=%+v err=%v", resp, err)
	}
}

func okActorHandler(t testing.TB) apptheory.Handler {
	t.Helper()
	return func(*apptheory.Context) (*apptheory.Response, error) {
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
}
