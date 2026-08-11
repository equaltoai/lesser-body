package auth

import (
	"context"
	"testing"
)

func TestWithToolActorRoundTripNormalizes(t *testing.T) {
	ctx := WithToolActor(context.Background(), " Arch ")
	if got := ActorFromToolContext(ctx); got != "arch" {
		t.Fatalf("ActorFromToolContext = %q, want normalized %q", got, "arch")
	}
}

func TestWithToolActorEmptyLeavesContextUnset(t *testing.T) {
	for _, actor := range []string{"", "   "} {
		ctx := WithToolActor(context.Background(), actor)
		if got := ActorFromToolContext(ctx); got != "" {
			t.Fatalf("ActorFromToolContext(%q) = %q, want empty", actor, got)
		}
	}
}

func TestActorFromToolContextAbsentAndNil(t *testing.T) {
	if got := ActorFromToolContext(context.Background()); got != "" {
		t.Fatalf("absent actor = %q, want empty", got)
	}
	if got := ActorFromToolContext(nil); got != "" {
		t.Fatalf("nil context actor = %q, want empty", got)
	}
}

func TestWithToolActorCoexistsWithPrincipalAndToken(t *testing.T) {
	principal := &Principal{Type: PrincipalTypeOAuthToken, Identity: "alice"}
	ctx := InjectToolContext(context.Background(), principal, "token")
	ctx = WithToolActor(ctx, "Arch")

	if got := PrincipalFromToolContext(ctx); got != principal {
		t.Fatalf("principal changed after actor injection: %+v", got)
	}
	if got := BearerTokenFromToolContext(ctx); got != "token" {
		t.Fatalf("bearer token changed after actor injection: %q", got)
	}
	if got := ActorFromToolContext(ctx); got != "arch" {
		t.Fatalf("actor = %q, want %q", got, "arch")
	}
}
