package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

func TestAccountResolveReturnsCanonicalFollowableReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/accounts/alice@example.com" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"canonical-42","username":"alice","acct":"alice@example.com","display_name":"Alice","url":"https://example.com/@alice"}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "caller"}, "oauth-token")

	result, err := handleAccountResolve(ctx, json.RawMessage(`{"account":"alice@example.com"}`))
	if err != nil {
		t.Fatalf("handleAccountResolve: %v", err)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	accountRef, _ := data["accountRef"].(map[string]any)
	if accountRef["id"] != "canonical-42" || accountRef["acct"] != "alice@example.com" {
		t.Fatalf("accountRef = %+v", accountRef)
	}
	for _, actionName := range []string{"follow", "unfollow"} {
		action, _ := data[actionName].(map[string]any)
		arguments, _ := action["arguments"].(map[string]any)
		if action["tool"] != actionName || arguments["account_id"] != "canonical-42" {
			t.Fatalf("%s action = %+v", actionName, action)
		}
	}
}

func TestCompactConversationParticipantPrefersResolvableAccountSelector(t *testing.T) {
	ref := compactConversationParticipantRef(map[string]any{"id": "participant-id", "acct": "alice@example.com", "url": "https://example.com/@alice"})
	if ref["accountSelector"] != "alice@example.com" {
		t.Fatalf("participant ref = %+v", ref)
	}
}
