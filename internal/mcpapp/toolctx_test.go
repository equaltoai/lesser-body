package mcpapp

import (
	"context"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

// The tool-context hook replaces the former WithToolContext middleware
// (reflect+unsafe request-context override) with the supported
// mcpruntime.WithToolContextHook path. These tests pin the migration
// equivalence: principal, bearer, request-id, and normalized actor must all
// still reach the derived tool context.

func TestToolContextHookInjectsNormalizedActorRouteValue(t *testing.T) {
	ctx := &apptheory.Context{
		AuthIdentity: "Alice",
		RequestID:    "req-abc",
		Params:       map[string]string{"actor": " Arch "},
		Request:      apptheory.Request{Path: "/mcp/ Arch "},
	}
	auth.WithPrincipal(ctx, &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "Alice"})

	derived := ToolContextHook(ctx, context.Background())

	if got := auth.ActorFromToolContext(derived); got != "arch" {
		t.Fatalf("tool context actor = %q, want normalized %q", got, "arch")
	}
	if principal := auth.PrincipalFromToolContext(derived); principal == nil || principal.Identity != "Alice" {
		t.Fatalf("tool context must preserve real caller principal, got %+v", principal)
	}
	if got := auth.BearerTokenFromToolContext(derived); got != "" {
		t.Fatalf("tool context bearer must be absent when the request carries none, got %q", got)
	}
	if got := auth.RequestIDFromToolContext(derived); got != "req-abc" {
		t.Fatalf("tool context request id = %q, want %q", got, "req-abc")
	}
}

func TestToolContextHookPropagatesBearerAndRequestID(t *testing.T) {
	ctx := &apptheory.Context{
		AuthIdentity: "arch",
		RequestID:    "req-xyz",
		Params:       map[string]string{"actor": "arch"},
		Request: apptheory.Request{
			Path:    "/mcp/arch",
			Headers: map[string][]string{"authorization": {"Bearer tok-abc"}},
		},
	}
	auth.WithPrincipal(ctx, &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "arch"})

	derived := ToolContextHook(ctx, context.Background())

	if got := auth.BearerTokenFromToolContext(derived); got != "tok-abc" {
		t.Fatalf("tool context bearer = %q, want %q", got, "tok-abc")
	}
	if got := auth.RequestIDFromToolContext(derived); got != "req-xyz" {
		t.Fatalf("tool context request id = %q, want %q", got, "req-xyz")
	}
	if principal := auth.PrincipalFromToolContext(derived); principal == nil || principal.Identity != "arch" {
		t.Fatalf("tool context must preserve the principal, got %+v", principal)
	}
}

func TestToolContextHookWithoutActorLeavesActorUnset(t *testing.T) {
	ctx := &apptheory.Context{
		AuthIdentity: "arch",
		Request:      apptheory.Request{Path: "/mcp"},
	}
	auth.WithPrincipal(ctx, &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "arch"})

	derived := ToolContextHook(ctx, context.Background())

	if got := auth.ActorFromToolContext(derived); got != "" {
		t.Fatalf("actor must be absent on non-actor requests, got %q", got)
	}
}

func TestToolContextHookNilSafe(t *testing.T) {
	base := context.Background()
	if got := ToolContextHook(nil, base); got != base {
		t.Fatal("nil apptheory context must return the base context unchanged")
	}
	if got := ToolContextHook(&apptheory.Context{}, nil); got != nil {
		t.Fatal("nil base context must be returned unchanged")
	}
	// A request with no principal, token, request-id, actor, policy, or share
	// caller must leave the base context untouched.
	plain := &apptheory.Context{Request: apptheory.Request{Path: "/mcp"}}
	if got := ToolContextHook(plain, base); got != base {
		t.Fatal("empty request must not replace the base context")
	}
}
