package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
)

const (
	shareTestActorAgentID   = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shareTestGranteeAgentID = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// installSoulBindingLookupMap serves soul-binding lookups only for the
// usernames present in bindings; every other username reads as not found,
// exactly like a grantee with no binding row of their own.
func installSoulBindingLookupMap(t *testing.T, bindings map[string]string) {
	t.Helper()
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				pk, _ := where["PK"].(string)
				username := strings.TrimPrefix(pk, "SOUL_BODY_BINDING_USERNAME#")
				agentID, ok := bindings[username]
				if !ok {
					return tableerrors.ErrItemNotFound
				}
				setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": username,
				})
				return nil
			},
		}, nil
	})
}

func shareGrantToolContext(actor string, caller string) context.Context {
	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: caller,
		Claims:   &auth.Claims{Username: caller, Scopes: []string{"read", "write"}},
	}, "oauth-token")
	return auth.WithToolActor(ctx, actor)
}

func resetCommTestClients(t *testing.T) {
	t.Helper()
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)
}

func TestAuthenticatedAgentIDResolvesActorForShareGrantCallerWithoutOwnBinding(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})
	resetCommTestClients(t)

	// The grantee path must not call lesser's bound-self verification: the
	// per-request share-grant check at admission replaces it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("share-grant resolution must not call lesser bound-self verification, got %s", r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)

	agentID, err := authenticatedAgentID(shareGrantToolContext("Arch", "alice"))
	if err != nil {
		t.Fatalf("authenticatedAgentID: %v", err)
	}
	if agentID != shareTestActorAgentID {
		t.Fatalf("agentID = %q, want actor's agent %q", agentID, shareTestActorAgentID)
	}
}

func TestAuthenticatedAgentIDResolvesActorForShareGrantCallerWithOwnBinding(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{
		"arch":  shareTestActorAgentID,
		"alice": shareTestGranteeAgentID,
	})
	resetCommTestClients(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("share-grant resolution must not call lesser bound-self verification, got %s", r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)

	agentID, err := authenticatedAgentID(shareGrantToolContext("arch", "Alice"))
	if err != nil {
		t.Fatalf("authenticatedAgentID: %v", err)
	}
	if agentID != shareTestActorAgentID {
		t.Fatalf("grantee with own binding resolved %q, want actor's agent %q (not own %q)", agentID, shareTestActorAgentID, shareTestGranteeAgentID)
	}
}

func TestAuthenticatedAgentIDOwnerPathUnchangedWithActorContext(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})
	resetCommTestClients(t)

	boundSelfCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/souls/bound/me" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("bound-self Authorization = %q", got)
		}
		boundSelfCalls++
		_, _ = w.Write([]byte(boundSelfResponse(shareTestActorAgentID, "arch", "test.example.com", "agent-arch")))
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)

	agentID, err := authenticatedAgentID(shareGrantToolContext("arch", "Arch"))
	if err != nil {
		t.Fatalf("authenticatedAgentID owner path: %v", err)
	}
	if agentID != shareTestActorAgentID {
		t.Fatalf("owner agentID = %q, want %q", agentID, shareTestActorAgentID)
	}
	if boundSelfCalls != 1 {
		t.Fatalf("owner path must keep bound-self verification, calls=%d", boundSelfCalls)
	}
}

func TestAuthenticatedAgentIDX402PrincipalIgnoresActorContext(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:      auth.PrincipalTypeX402Grant,
		Identity:  "public-paid",
		X402Grant: &auth.X402InvocationGrant{Actor: "arch"},
	}, "")
	ctx = auth.WithToolActor(ctx, "arch")

	_, err := authenticatedAgentID(ctx)
	if err == nil {
		t.Fatalf("x402 principal must not resolve via actor context")
	}
	failure := mcpAuthFailureFromError(err)
	if failure == nil || failure.Details["reason"] != "missing_oauth_bearer" {
		t.Fatalf("x402 path must stay on the OAuth-required failure, got %+v", failure)
	}
}

func TestIdentityWhoamiResolvesSharedAgentForGrantee(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})
	resetCommTestClients(t)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			t.Fatalf("grantee whoami must not verify the caller's own binding")
		case "/api/v1/soul/agents/" + shareTestActorAgentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + shareTestActorAgentID + `","domain":"test.example.com","local_id":"agent-arch","status":"active"}}`))
		case "/api/v1/soul/agents/" + shareTestActorAgentID + "/registration":
			_, _ = w.Write([]byte(`{"version":"3","contactPreferences":{},` + boundBodyPolicyJSONForTest("identity.self.read") + `}`))
		case "/api/v1/soul/comm/contactability/" + shareTestActorAgentID:
			if got := r.Header.Get("Authorization"); got != "Bearer host-instance-key" {
				t.Fatalf("contactability Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{
				"instanceSlug":"theory",
				"agentId":"` + shareTestActorAgentID + `",
				"contactable":true,
				"channels":[{
					"channelType":"email",
					"address":"agent-arch.theory@lessersoul.ai",
					"provider":"migadu",
					"capabilities":["receive","send"],
					"protocols":["smtp","imap"],
					"verified":true,
					"status":"active",
					"receiveAllowed":true,
					"sendAllowed":true
				}]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)

	result, err := handleIdentityWhoami(shareGrantToolContext("arch", "alice"), []byte(`{}`))
	if err != nil {
		t.Fatalf("handleIdentityWhoami: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("identity_whoami result = %+v", result)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if got, _ := data["agentId"].(string); got != shareTestActorAgentID {
		t.Fatalf("whoami agentId = %q, want shared agent %q", got, shareTestActorAgentID)
	}
	channels, _ := data["channels"].(map[string]any)
	email, _ := channels["email"].(map[string]any)
	if email["address"] != "agent-arch.theory@lessersoul.ai" {
		t.Fatalf("whoami must project the shared agent's channels, got %+v", email)
	}
}

type capturedCommSend struct {
	payload map[string]any
	bearer  string
}

// sharedAgentCommServer stubs the soul-api surface a comm send walks through:
// agent read, registration (twice: payload + boundary advisory), contactability,
// and the comm send itself. Any bound-self verification call fails the test.
func sharedAgentCommServer(t *testing.T, sent *capturedCommSend) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/souls/bound/me":
			t.Fatalf("grantee comm send must not verify the caller's own binding")
		case r.URL.Path == "/api/v1/soul/agents/"+shareTestActorAgentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + shareTestActorAgentID + `","domain":"test.example.com","local_id":"agent-arch","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+shareTestActorAgentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","contactPreferences":{},` + boundBodyPolicyJSONForTest("communication.sms.send") + `}`))
		case r.URL.Path == "/api/v1/soul/comm/contactability/"+shareTestActorAgentID:
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v1/soul/comm/send" && r.Method == http.MethodPost:
			sent.bearer = r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&sent.payload); err != nil {
				t.Fatalf("decode comm send payload: %v", err)
			}
			_, _ = w.Write([]byte(`{"messageId":"msg-1","status":"queued"}`))
		default:
			t.Fatalf("unexpected path: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestShareGrantCallerWithoutOwnBindingSendsCommAsSharedAgent(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})
	resetCommTestClients(t)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key")

	sent := &capturedCommSend{}
	server := sharedAgentCommServer(t, sent)
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)

	result, err := handleSmsSend(shareGrantToolContext("Arch", "Alice"), []byte(`{"to":"+15551234567","body":"hello","messageId":"idem-1"}`))
	if err != nil {
		t.Fatalf("handleSmsSend: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("sms_send result = %+v", result)
	}
	if sent.payload == nil {
		t.Fatalf("host comm send was not called")
	}
	if got := sent.payload["agentId"]; got != shareTestActorAgentID {
		t.Fatalf("comm send agentId = %v, want shared agent %q", got, shareTestActorAgentID)
	}
	if got := sent.payload["actedBy"]; got != "alice" {
		t.Fatalf("comm send actedBy = %v, want normalized real caller %q", got, "alice")
	}
	if got := sent.payload["channel"]; got != "sms" {
		t.Fatalf("comm send channel = %v, want sms", got)
	}
	if got := sent.payload["idempotencyKey"]; got != "idem-1" {
		t.Fatalf("comm send idempotencyKey = %v, want idem-1", got)
	}
}

func TestShareGrantCallerWithOwnBindingStillSendsCommAsSharedAgent(t *testing.T) {
	installSoulBindingLookupMap(t, map[string]string{
		"arch":  shareTestActorAgentID,
		"alice": shareTestGranteeAgentID,
	})
	resetCommTestClients(t)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key")

	sent := &capturedCommSend{}
	server := sharedAgentCommServer(t, sent)
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)

	result, err := handleSmsSend(shareGrantToolContext("arch", "alice"), []byte(`{"to":"+15551234567","body":"hello","messageId":"idem-1"}`))
	if err != nil {
		t.Fatalf("handleSmsSend: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("sms_send result = %+v", result)
	}
	if sent.payload == nil {
		t.Fatalf("host comm send was not called")
	}
	if got := sent.payload["agentId"]; got != shareTestActorAgentID {
		t.Fatalf("grantee with own binding sent as %v, want shared agent %q (not own %q)", got, shareTestActorAgentID, shareTestGranteeAgentID)
	}
	if got := sent.payload["actedBy"]; got != "alice" {
		t.Fatalf("comm send actedBy = %v, want %q", got, "alice")
	}
}

// TestOwnerSendOmitsActedByAndResolvesAsBefore pins the byte-for-byte owner
// contract: with actor == caller the host payload is exactly the pre-rework
// shape (channel, agentId, idempotencyKey, plus message fields) with no
// actedBy key, and bound-self verification still runs. The subtests cover the
// owner route (actor == caller) and the defensive no-actor-context fallback.
func TestOwnerSendOmitsActedByAndResolvesAsBefore(t *testing.T) {
	tests := []struct {
		name       string
		withActor  bool
		actorValue string
	}{
		{name: "owner route actor equals caller", withActor: true, actorValue: "arch"},
		{name: "defensive fallback without actor context", withActor: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			installSoulBindingLookupMap(t, map[string]string{"arch": shareTestActorAgentID})
			resetCommTestClients(t)
			t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key")

			boundSelfCalls := 0
			sent := &capturedCommSend{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/v1/souls/bound/me":
					if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
						t.Fatalf("bound-self Authorization = %q", got)
					}
					boundSelfCalls++
					_, _ = w.Write([]byte(boundSelfResponse(shareTestActorAgentID, "arch", "test.example.com", "agent-arch")))
				case r.URL.Path == "/api/v1/soul/agents/"+shareTestActorAgentID:
					_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + shareTestActorAgentID + `","domain":"test.example.com","local_id":"agent-arch","status":"active"}}`))
				case r.URL.Path == "/api/v1/soul/agents/"+shareTestActorAgentID+"/registration":
					_, _ = w.Write([]byte(`{"version":"3","contactPreferences":{},` + boundBodyPolicyJSONForTest("communication.sms.send") + `}`))
				case r.URL.Path == "/api/v1/soul/comm/contactability/"+shareTestActorAgentID:
					_, _ = w.Write([]byte(`{}`))
				case r.URL.Path == "/api/v1/soul/comm/send" && r.Method == http.MethodPost:
					sent.bearer = r.Header.Get("Authorization")
					if err := json.NewDecoder(r.Body).Decode(&sent.payload); err != nil {
						t.Fatalf("decode comm send payload: %v", err)
					}
					_, _ = w.Write([]byte(`{"messageId":"msg-1","status":"queued"}`))
				default:
					t.Fatalf("unexpected path: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("LESSER_API_BASE_URL", server.URL)
			t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)

			ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
				Type:     auth.PrincipalTypeOAuthToken,
				Identity: "Arch",
				Claims:   &auth.Claims{Username: "Arch", Scopes: []string{"read", "write"}},
			}, "oauth-token")
			if tc.withActor {
				ctx = auth.WithToolActor(ctx, tc.actorValue)
			}

			result, err := handleSmsSend(ctx, []byte(`{"to":"+15551234567","body":"hello","messageId":"idem-1"}`))
			if err != nil {
				t.Fatalf("handleSmsSend: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("sms_send result = %+v", result)
			}
			if boundSelfCalls != 1 {
				t.Fatalf("owner path must keep bound-self verification, calls=%d", boundSelfCalls)
			}
			if _, present := sent.payload["actedBy"]; present {
				t.Fatalf("owner payload must not carry actedBy: %+v", sent.payload)
			}
			want := map[string]any{
				"channel":        "sms",
				"agentId":        shareTestActorAgentID,
				"idempotencyKey": "idem-1",
				"to":             "+15551234567",
				"body":           "[bot] hello",
				"inReplyTo":      "idem-1",
			}
			if len(sent.payload) != len(want) {
				t.Fatalf("owner payload keys changed: %+v", sent.payload)
			}
			for key, wantValue := range want {
				if sent.payload[key] != wantValue {
					t.Fatalf("owner payload[%q] = %v, want %v (full payload %+v)", key, sent.payload[key], wantValue, sent.payload)
				}
			}
		})
	}
}
