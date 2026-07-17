package instancex402

import (
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
)

func TestIsInstanceOperatorRequiresExplicitAuthority(t *testing.T) {
	tests := []struct {
		name   string
		claims auth.Claims
		want   bool
	}{
		{
			name: "ordinary write token",
			claims: auth.Claims{
				Scopes: []string{"read", "write"},
			},
		},
		{
			name: "delegated marker is not authority",
			claims: auth.Claims{
				Scopes:      []string{"write"},
				DelegatedBy: "operator-client",
			},
		},
		{
			name: "explicit client class",
			claims: auth.Claims{
				Scopes:      []string{"write"},
				ClientClass: "account_operator",
			},
			want: true,
		},
		{
			name: "explicit owner class",
			claims: auth.Claims{
				Scopes:      []string{"write"},
				ClientClass: "owner",
			},
			want: true,
		},
		{
			name: "admin scope",
			claims: auth.Claims{
				Scopes: []string{"admin"},
			},
			want: true,
		},
		{
			name: "operator agent type",
			claims: auth.Claims{
				Scopes:    []string{"write"},
				AgentType: "operator",
			},
			want: true,
		},
		{
			name: "agent with operator-looking claims",
			claims: auth.Claims{
				Scopes:      []string{"admin"},
				ClientClass: "operator",
				IsAgent:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal := &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Claims: &tt.claims}
			if got := IsInstanceOperator(principal); got != tt.want {
				t.Fatalf("IsInstanceOperator = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInstanceOperatorRejectsNonOAuthPrincipal(t *testing.T) {
	if IsInstanceOperator(&auth.Principal{Type: auth.PrincipalTypeInstanceKey}) {
		t.Fatal("instance key must not become an OAuth owner/operator principal")
	}
}
