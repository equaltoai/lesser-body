package mcpapp

import (
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestWithToolContextInjectsNormalizedActorRouteValue(t *testing.T) {
	next := func(c *apptheory.Context) (*apptheory.Response, error) {
		if got := auth.ActorFromToolContext(c.Context()); got != "arch" {
			t.Fatalf("tool context actor = %q, want normalized %q", got, "arch")
		}
		if principal := auth.PrincipalFromToolContext(c.Context()); principal == nil || principal.Identity != "Alice" {
			t.Fatalf("tool context must preserve real caller principal, got %+v", principal)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}

	ctx := &apptheory.Context{
		AuthIdentity: "Alice",
		Params:       map[string]string{"actor": " Arch "},
		Request:      apptheory.Request{Path: "/mcp/ Arch "},
	}
	auth.WithPrincipal(ctx, &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "Alice"})

	resp, err := WithToolContext(next)(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("WithToolContext dispatch failed: response=%+v err=%v", resp, err)
	}
}

func TestWithToolContextWithoutActorLeavesActorUnset(t *testing.T) {
	next := func(c *apptheory.Context) (*apptheory.Response, error) {
		if got := auth.ActorFromToolContext(c.Context()); got != "" {
			t.Fatalf("actor must be absent on non-actor requests, got %q", got)
		}
		return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
	}

	ctx := &apptheory.Context{
		AuthIdentity: "arch",
		Request:      apptheory.Request{Path: "/mcp"},
	}
	auth.WithPrincipal(ctx, &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "arch"})

	resp, err := WithToolContext(next)(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("WithToolContext dispatch failed: response=%+v err=%v", resp, err)
	}
}
