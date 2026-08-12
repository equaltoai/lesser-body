package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

// installActAsUpstream installs a mock lesser that records the caller bearer
// and act-as header of the first request and serves the given response.
func installActAsUpstream(t *testing.T, status int, body string, onRequest func(method string, path string)) (authorization *string, actAs *string) {
	t.Helper()
	auth := new(string)
	act := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		*act = r.Header.Get("X-Lesser-Act-As")
		if onRequest != nil {
			onRequest(r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 200 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	return auth, act
}

func TestRequireActAsScopedOAuthBearer(t *testing.T) {
	t.Run("grantee gets caller bearer and act-as context", func(t *testing.T) {
		ctx, token, err := requireActAsScopedOAuthBearer(shareGrantToolContext("arch", "alice"))
		if err != nil || token != "oauth-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
		if got := lesserapi.ActAsFromContext(ctx); got != "arch" {
			t.Fatalf("act-as = %q, want actor route value", got)
		}
	})
	t.Run("owner gets token with unchanged context", func(t *testing.T) {
		owner := shareGrantToolContext("arch", "arch")
		ctx, token, err := requireActAsScopedOAuthBearer(owner)
		if err != nil || token != "oauth-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
		if ctx != owner {
			t.Fatal("owner path must return the context unchanged")
		}
		if got := lesserapi.ActAsFromContext(ctx); got != "" {
			t.Fatalf("owner path must never carry act-as, got %q", got)
		}
	})
	t.Run("no actor gets token with unchanged context", func(t *testing.T) {
		plain := articleDraftTestContext()
		ctx, token, err := requireActAsScopedOAuthBearer(plain)
		if err != nil || token != "test-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
		if ctx != plain || lesserapi.ActAsFromContext(ctx) != "" {
			t.Fatal("actor-less path must stay byte-identical")
		}
	})
	t.Run("x402 keeps missing oauth bearer failure and no act-as", func(t *testing.T) {
		ctx, _, err := requireActAsScopedOAuthBearer(x402GrantToolContext("arch", "paid-caller"))
		failure := mcpAuthFailureFromError(err)
		if failure == nil || failure.Code != "unauthorized" || !strings.Contains(failure.Message, "OAuth bearer token") {
			t.Fatalf("x402 path must keep pre-existing oauth gate failure, err=%v", err)
		}
		if got := lesserapi.ActAsFromContext(ctx); got != "" {
			t.Fatalf("x402 path must never carry act-as, got %q", got)
		}
	})
}

func TestSharePathSendsCallerBearerAndActAsHeaderREST(t *testing.T) {
	var method, path string
	auth, act := installActAsUpstream(t, 200, `{"id":"s1","content":"hello","agent_attribution":{"acted_by":"https://example.com/users/alice"}}`, func(m string, p string) {
		method, path = m, p
	})

	res, err := handlePostCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"hello"}`))
	if err != nil {
		t.Fatalf("post_create: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("post_create result = %+v", res)
	}
	if *auth != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q, want the caller's own bearer", *auth)
	}
	if *act != "arch" {
		t.Fatalf("X-Lesser-Act-As = %q, want the actor route value", *act)
	}
	if method != "POST" || path != "/api/v1/statuses" {
		t.Fatalf("upstream call = %s %s", method, path)
	}
	// Attribution honesty: lesser's agent_attribution.acted_by passes through
	// to the caller verbatim.
	if !strings.Contains(res.Content[0].Text, `"acted_by":"https://example.com/users/alice"`) {
		t.Fatalf("response must surface lesser's acted_by attribution, got %s", res.Content[0].Text)
	}
}

func TestSharePathVerifyCredentialsSendsActAsAndMarksAgentIdentity(t *testing.T) {
	auth, act := installActAsUpstream(t, 200, `{"id":"1","username":"arch","acct":"arch"}`, nil)

	res, err := handleProfileRead(shareGrantToolContext("arch", "alice"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("profile_read: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("profile_read result = %+v", res)
	}
	if *auth != "Bearer oauth-token" || *act != "arch" {
		t.Fatalf("headers auth=%q act-as=%q", *auth, *act)
	}
	// Identity honesty: under act-as the account is the AGENT's, and the
	// structured result must say so.
	marker, ok := res.StructuredContent["act_as"].(map[string]any)
	if !ok {
		t.Fatalf("missing act_as identity marker, structured = %+v", res.StructuredContent)
	}
	if marker["actor"] != "arch" || marker["caller"] != "alice" {
		t.Fatalf("act_as marker = %+v", marker)
	}
	data, _ := res.StructuredContent["data"].(map[string]any)
	if data["username"] != "arch" {
		t.Fatalf("verify_credentials under act-as must return the agent account, got %+v", data)
	}
}

func TestSharePathSendsActAsHeaderGraphQL(t *testing.T) {
	t.Run("message requests list", func(t *testing.T) {
		var path string
		auth, act := installActAsUpstream(t, 200, `{"data":{"conversations":[]}}`, func(_ string, p string) { path = p })
		res, err := handleMessageRequestsList(shareGrantToolContext("arch", "alice"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("message_requests_list: %v", err)
		}
		if res == nil || res.IsError {
			t.Fatalf("message_requests_list result = %+v", res)
		}
		if *auth != "Bearer oauth-token" || *act != "arch" {
			t.Fatalf("headers auth=%q act-as=%q", *auth, *act)
		}
		if path != "/api/graphql" {
			t.Fatalf("path = %q", path)
		}
	})
	t.Run("article draft create surfaces actedBy attribution", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `{"data":{"createDraft":{"id":"draft-1","author":{"id":"https://example.com/users/arch","username":"arch"},"actedBy":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","status":"DRAFT","contentFormat":"MARKDOWN","revision":1}}}`, nil)
		res, err := handleArticleDraftCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"draft body"}`))
		if err != nil {
			t.Fatalf("article_draft_create: %v", err)
		}
		if res == nil || res.IsError {
			t.Fatalf("article_draft_create result = %+v", res)
		}
		if *auth != "Bearer oauth-token" || *act != "arch" {
			t.Fatalf("headers auth=%q act-as=%q", *auth, *act)
		}
		data, _ := res.StructuredContent["data"].(map[string]any)
		draft, _ := data["draft"].(map[string]any)
		actedBy, _ := draft["actedBy"].(map[string]any)
		if actedBy["username"] != "alice" {
			t.Fatalf("draft.actedBy must surface lesser's caller attribution, draft = %+v", draft)
		}
	})
}

func TestSharePathTimelineHomeSendsActAs(t *testing.T) {
	var path string
	auth, act := installActAsUpstream(t, 200, `[]`, func(_ string, p string) { path = p })
	res, err := handleTimelineRead(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"timeline":"home"}`))
	if err != nil {
		t.Fatalf("timeline_read: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("timeline_read result = %+v", res)
	}
	if *auth != "Bearer oauth-token" || *act != "arch" || path != "/api/v1/timelines/home" {
		t.Fatalf("auth=%q act-as=%q path=%q", *auth, *act, path)
	}
}

func TestSharePathResourcesSendCallerBearerAndActAsHeader(t *testing.T) {
	t.Run("profile resource", func(t *testing.T) {
		var path string
		auth, act := installActAsUpstream(t, 200, `{"id":"1","username":"arch","acct":"arch"}`, func(_ string, p string) { path = p })
		contents, err := resourceProfile(shareGrantToolContext("arch", "alice"))
		if err != nil {
			t.Fatalf("resourceProfile: %v", err)
		}
		if len(contents) != 1 {
			t.Fatalf("contents = %+v", contents)
		}
		if *auth != "Bearer oauth-token" || *act != "arch" || path != "/api/v1/accounts/verify_credentials" {
			t.Fatalf("auth=%q act-as=%q path=%q", *auth, *act, path)
		}
	})
	t.Run("home timeline resource", func(t *testing.T) {
		var path string
		auth, act := installActAsUpstream(t, 200, `[]`, func(_ string, p string) { path = p })
		contents, err := resourceTimeline("home")(shareGrantToolContext("arch", "alice"))
		if err != nil {
			t.Fatalf("resourceTimeline(home): %v", err)
		}
		if len(contents) != 1 {
			t.Fatalf("contents = %+v", contents)
		}
		if *auth != "Bearer oauth-token" || *act != "arch" || path != "/api/v1/timelines/home" {
			t.Fatalf("auth=%q act-as=%q path=%q", *auth, *act, path)
		}
	})
	t.Run("notifications resource", func(t *testing.T) {
		var path string
		auth, act := installActAsUpstream(t, 200, `[]`, func(_ string, p string) { path = p })
		contents, err := resourceNotifications(shareGrantToolContext("arch", "alice"))
		if err != nil {
			t.Fatalf("resourceNotifications: %v", err)
		}
		if len(contents) != 1 {
			t.Fatalf("contents = %+v", contents)
		}
		if *auth != "Bearer oauth-token" || *act != "arch" || path != "/api/v1/notifications" {
			t.Fatalf("auth=%q act-as=%q path=%q", *auth, *act, path)
		}
	})
}

func TestOwnerPathResourcesNeverSendActAsHeader(t *testing.T) {
	owner := func() context.Context { return shareGrantToolContext("arch", "arch") }
	t.Run("profile resource", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `{"id":"1","username":"arch"}`, nil)
		if _, err := resourceProfile(owner()); err != nil {
			t.Fatalf("owner resourceProfile: %v", err)
		}
		if *auth != "Bearer oauth-token" || *act != "" {
			t.Fatalf("owner path must be byte-identical with no act-as header, auth=%q act-as=%q", *auth, *act)
		}
	})
	t.Run("home timeline resource", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `[]`, nil)
		if _, err := resourceTimeline("home")(owner()); err != nil {
			t.Fatalf("owner resourceTimeline(home): %v", err)
		}
		if *auth != "Bearer oauth-token" || *act != "" {
			t.Fatalf("owner path must be byte-identical with no act-as header, auth=%q act-as=%q", *auth, *act)
		}
	})
	t.Run("notifications resource", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `[]`, nil)
		if _, err := resourceNotifications(owner()); err != nil {
			t.Fatalf("owner resourceNotifications: %v", err)
		}
		if *auth != "Bearer oauth-token" || *act != "" {
			t.Fatalf("owner path must be byte-identical with no act-as header, auth=%q act-as=%q", *auth, *act)
		}
	})
}

func TestOwnerAndActorlessPathsNeverSendActAsHeader(t *testing.T) {
	t.Run("owner post_create", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `{"id":"s1","content":"hello"}`, nil)
		res, err := handlePostCreate(shareGrantToolContext("arch", "arch"), json.RawMessage(`{"content":"hello"}`))
		if err != nil || res == nil || res.IsError {
			t.Fatalf("owner post_create res=%+v err=%v", res, err)
		}
		if *auth != "Bearer oauth-token" || *act != "" {
			t.Fatalf("owner path must be byte-identical with no act-as header, auth=%q act-as=%q", *auth, *act)
		}
	})
	t.Run("actor-less draft create", func(t *testing.T) {
		auth, act := installActAsUpstream(t, 200, `{"data":{"createDraft":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","status":"DRAFT","contentFormat":"MARKDOWN","revision":1}}}`, nil)
		res, err := handleArticleDraftCreate(articleDraftTestContext(), json.RawMessage(`{"content":"draft body"}`))
		if err != nil || res == nil || res.IsError {
			t.Fatalf("actor-less draft create res=%+v err=%v", res, err)
		}
		if *auth != "Bearer test-token" || *act != "" {
			t.Fatalf("actor-less path must be byte-identical with no act-as header, auth=%q act-as=%q", *auth, *act)
		}
	})
}

func TestX402ShareContextNeverReachesUpstreamWithActAs(t *testing.T) {
	installFailingLesserUpstream(t)

	res, err := handleProfileRead(x402GrantToolContext("arch", "paid-caller"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("profile_read: %v", err)
	}
	errPayload, _ := res.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "unauthorized" {
		t.Fatalf("x402 path must keep the oauth gate failure, got %+v", res.StructuredContent)
	}
}

func TestUpstreamActAsRejectionPropagatesReason(t *testing.T) {
	t.Run("REST 400 reason preserved", func(t *testing.T) {
		installActAsUpstream(t, 400, `{"error":"scheduled_at cannot be combined with the act-as indicator"}`, nil)
		_, err := handlePostCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"hello"}`))
		if err == nil {
			t.Fatal("expected upstream 400 to propagate as a tool error")
		}
		if !strings.Contains(err.Error(), "status=400") || !strings.Contains(err.Error(), "scheduled_at cannot be combined with the act-as indicator") {
			t.Fatalf("upstream reason must be preserved, err = %v", err)
		}
	})
	t.Run("REST 403 grant rejection preserved", func(t *testing.T) {
		installActAsUpstream(t, 403, `{"error":"no active share grant for agent and caller"}`, nil)
		res, err := handlePostCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"hello"}`))
		if err != nil {
			t.Fatalf("post_create: %v", err)
		}
		if res == nil || !res.IsError {
			t.Fatalf("expected upstream 403 to propagate as a tool error, got %+v", res)
		}
		errPayload, _ := res.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != "forbidden" {
			t.Fatalf("error code = %+v", errPayload)
		}
		if status, _ := errPayload["status"].(int); status != 403 {
			t.Fatalf("error status = %+v", errPayload)
		}
		details, _ := errPayload["details"].(map[string]any)
		if upstream, _ := details["upstreamMessage"].(string); !strings.Contains(upstream, "no active share grant") {
			t.Fatalf("upstream reason must be preserved, details = %+v", details)
		}
	})
	t.Run("GraphQL BAD_REQUEST extension preserved", func(t *testing.T) {
		installActAsUpstream(t, 200, `{"data":{"createDraft":null},"errors":[{"message":"act-as indicator malformed","extensions":{"code":"BAD_REQUEST","http_status":400}}]}`, nil)
		res, err := handleArticleDraftCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"draft body"}`))
		if err != nil {
			t.Fatalf("article_draft_create: %v", err)
		}
		if res == nil || !res.IsError {
			t.Fatalf("expected error result, got %+v", res)
		}
		errPayload, _ := res.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != "BAD_REQUEST" {
			t.Fatalf("error code = %+v, want upstream BAD_REQUEST", errPayload)
		}
		if status, _ := errPayload["status"].(int); status != 400 {
			t.Fatalf("error status = %+v, want 400", errPayload)
		}
		if !strings.Contains(res.Content[0].Text, "act-as indicator malformed") {
			// The upstream reason must be preserved in the error details even
			// when the text block carries the generic summary.
			details, _ := errPayload["details"].(map[string]any)
			gqlErrors, _ := details["graphqlErrors"].([]any)
			first, _ := gqlErrors[0].(map[string]any)
			if first["message"] != "act-as indicator malformed" {
				t.Fatalf("upstream reason not preserved, payload = %+v", errPayload)
			}
		}
	})
}

func TestOwnerPathDraftWithoutActedByStaysByteIdentical(t *testing.T) {
	// When lesser returns no actedBy attribution (owner path), the shaped
	// draft must not grow an actedBy key.
	installActAsUpstream(t, 200, `{"data":{"createDraft":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","status":"DRAFT","contentFormat":"MARKDOWN","revision":1}}}`, nil)
	res, err := handleArticleDraftCreate(articleDraftTestContext(), json.RawMessage(`{"content":"draft body"}`))
	if err != nil || res == nil || res.IsError {
		t.Fatalf("draft create res=%+v err=%v", res, err)
	}
	data, _ := res.StructuredContent["data"].(map[string]any)
	draft, _ := data["draft"].(map[string]any)
	if _, ok := draft["actedBy"]; ok {
		t.Fatalf("owner path must not invent attribution, draft = %+v", draft)
	}
	draftRef, _ := data["draftRef"].(map[string]any)
	if _, ok := draftRef["actedBy"]; ok {
		t.Fatalf("owner path must not invent attribution, draftRef = %+v", draftRef)
	}
}
