package mcpapp

import (
	"context"
	"testing"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestGranteeAdmissionThreadsShareCallerThroughToolContext(t *testing.T) {
	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		caller, shared := mcpserver.ShareCallerFromContext(ctx.Context())
		if !shared || caller != "alice" {
			t.Fatalf("grantee admission must thread the share caller into the tool context, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
	handler := withActorBinding(WithToolContext(next),
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) { return true, nil },
	)

	ctx := actorOAuthContextWithDelegatedBy("arch", "@alice")
	ctx.Request.Headers = map[string][]string{"authorization": {"Bearer caller-token"}}
	resp, err := handler(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("grantee pipeline failed: response=%+v err=%v", resp, err)
	}
}

func TestOwnerAdmissionNeverThreadsShareCaller(t *testing.T) {
	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); shared || caller != "" {
			t.Fatalf("owner admission must not thread a share caller, caller=%q shared=%t", caller, shared)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}
	handler := withActorBinding(WithToolContext(next),
		func(context.Context, string) (string, error) { return "owner", nil },
		func(context.Context, string, string) (bool, error) {
			t.Fatal("owner grant check must not run")
			return false, nil
		},
	)

	resp, err := handler(actorOAuthContextWithDelegatedBy("arch", "owner"))
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
