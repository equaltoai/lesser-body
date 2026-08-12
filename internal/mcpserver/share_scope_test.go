package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/memory"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func x402GrantToolContext(actor string, identity string) context.Context {
	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeX402Grant,
		Identity: identity,
	}, "")
	return auth.WithToolActor(ctx, actor)
}

func installInMemoryStore(t *testing.T) {
	t.Helper()
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	memory.ResetForTests()
	t.Cleanup(memory.ResetForTests)
}

func TestShareGrantActorCallerClassification(t *testing.T) {
	t.Run("grantee is shared", func(t *testing.T) {
		actor, caller, shared := shareGrantActorCaller(shareGrantToolContext("arch", "alice"))
		if !shared || actor != "arch" || caller != "alice" {
			t.Fatalf("actor=%q caller=%q shared=%t", actor, caller, shared)
		}
	})
	t.Run("owner exact case is not shared", func(t *testing.T) {
		_, _, shared := shareGrantActorCaller(shareGrantToolContext("arch", "arch"))
		if shared {
			t.Fatal("owner request must not classify as shared")
		}
	})
	t.Run("owner mixed case is not shared", func(t *testing.T) {
		_, caller, shared := shareGrantActorCaller(shareGrantToolContext("arch", "Arch"))
		if shared || caller != "arch" {
			t.Fatalf("mixed-case owner must normalize to not-shared, caller=%q shared=%t", caller, shared)
		}
	})
	t.Run("no actor context is not shared", func(t *testing.T) {
		ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
			Type:     auth.PrincipalTypeOAuthToken,
			Identity: "alice",
		}, "token")
		actor, _, shared := shareGrantActorCaller(ctx)
		if shared || actor != "" {
			t.Fatalf("actor=%q shared=%t", actor, shared)
		}
	})
	t.Run("x402 principal is not shared", func(t *testing.T) {
		actor, _, shared := shareGrantActorCaller(x402GrantToolContext("arch", "paid-caller"))
		if shared || actor != "arch" {
			t.Fatalf("actor=%q shared=%t", actor, shared)
		}
	})
}

func TestActingMemoryScopeIdentityResolution(t *testing.T) {
	t.Run("grantee resolves actor partition", func(t *testing.T) {
		identity, err := actingMemoryScopeIdentity(shareGrantToolContext("arch", "alice"))
		if err != nil || identity != "arch" {
			t.Fatalf("identity=%q err=%v", identity, err)
		}
	})
	t.Run("owner keeps exact caller identity bytes", func(t *testing.T) {
		identity, err := actingMemoryScopeIdentity(shareGrantToolContext("arch", "Arch"))
		if err != nil || identity != "Arch" {
			t.Fatalf("owner identity must stay byte-identical, got %q err=%v", identity, err)
		}
	})
	t.Run("no actor keeps caller identity", func(t *testing.T) {
		identity, err := actingMemoryScopeIdentity(articleDraftTestContext())
		if err != nil || identity != "alice" {
			t.Fatalf("identity=%q err=%v", identity, err)
		}
	})
	t.Run("x402 keeps caller identity", func(t *testing.T) {
		identity, err := actingMemoryScopeIdentity(x402GrantToolContext("arch", "paid-caller"))
		if err != nil || identity != "paid-caller" {
			t.Fatalf("identity=%q err=%v", identity, err)
		}
	})
	t.Run("missing principal keeps missing identity error", func(t *testing.T) {
		_, err := actingMemoryScopeIdentity(context.Background())
		if err == nil || err.Error() != "missing identity" {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestMemoryAppendShareGrantCallerWritesActorPartition(t *testing.T) {
	installInMemoryStore(t)

	res, err := handleMemoryAppend(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"grantee note for arch"}`))
	if err != nil {
		t.Fatalf("memory_append: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("memory_append result = %+v", res)
	}

	store, err := memory.Default()
	if err != nil {
		t.Fatalf("memory.Default: %v", err)
	}
	actorEvents, err := store.Query(context.Background(), "arch", memory.QueryInput{Query: "grantee note"})
	if err != nil || len(actorEvents.Events) != 1 {
		t.Fatalf("actor partition events=%+v err=%v", actorEvents, err)
	}
	callerEvents, err := store.Query(context.Background(), "alice", memory.QueryInput{})
	if err != nil || len(callerEvents.Events) != 0 {
		t.Fatalf("grantee must not write own partition, events=%+v err=%v", callerEvents, err)
	}
}

func TestMemoryQueryShareGrantCallerReadsActorPartition(t *testing.T) {
	installInMemoryStore(t)

	store, err := memory.Default()
	if err != nil {
		t.Fatalf("memory.Default: %v", err)
	}
	if _, err := store.Append(context.Background(), "arch", memory.AppendInput{Content: "arch only memory"}); err != nil {
		t.Fatalf("seed append: %v", err)
	}

	res, err := handleMemoryQuery(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"query":"arch only"}`))
	if err != nil {
		t.Fatalf("memory_query: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("memory_query result = %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "arch only memory") {
		t.Fatalf("grantee must read actor partition, got %s", res.Content[0].Text)
	}
}

func TestMemoryAppendOwnerPathUnchanged(t *testing.T) {
	installInMemoryStore(t)

	if _, err := handleMemoryAppend(shareGrantToolContext("arch", "arch"), json.RawMessage(`{"content":"owner note"}`)); err != nil {
		t.Fatalf("owner memory_append: %v", err)
	}
	store, err := memory.Default()
	if err != nil {
		t.Fatalf("memory.Default: %v", err)
	}
	events, err := store.Query(context.Background(), "arch", memory.QueryInput{Query: "owner note"})
	if err != nil || len(events.Events) != 1 {
		t.Fatalf("owner partition events=%+v err=%v", events, err)
	}
}

func TestNotificationCursorShareGrantCallerKeyedByActor(t *testing.T) {
	installInMemoryStore(t)

	grantee := shareGrantToolContext("arch", "alice")
	if err := writeNotificationCursor(grantee, "cursor-9"); err != nil {
		t.Fatalf("writeNotificationCursor: %v", err)
	}
	cursor, err := readNotificationCursor(grantee)
	if err != nil || cursor != "cursor-9" {
		t.Fatalf("grantee cursor = %q err=%v", cursor, err)
	}
	// The cursor lives in the actor's partition: the grantee's own identity
	// scope must not see it.
	callerCursor, err := readNotificationCursor(shareGrantToolContext("alice", "alice"))
	if err != nil || callerCursor != "" {
		t.Fatalf("caller-scoped cursor = %q err=%v", callerCursor, err)
	}
}

func TestAuthenticatedArticleAuthorIDResolution(t *testing.T) {
	if got := authenticatedArticleAuthorID(shareGrantToolContext("arch", "alice")); got != "arch" {
		t.Fatalf("grantee author scope = %q, want actor", got)
	}
	if got := authenticatedArticleAuthorID(shareGrantToolContext("arch", "Arch")); got != "Arch" {
		t.Fatalf("owner author scope must stay byte-identical, got %q", got)
	}
	if got := authenticatedArticleAuthorID(articleDraftTestContext()); got != "alice" {
		t.Fatalf("no-actor author scope = %q", got)
	}
}

func TestRequireOwnerScopedOAuthBearer(t *testing.T) {
	t.Run("grantee fails closed", func(t *testing.T) {
		_, err := requireOwnerScopedOAuthBearer(shareGrantToolContext("arch", "alice"))
		failure := mcpAuthFailureFromError(err)
		if failure == nil || failure.Status != 403 || failure.Code != "forbidden" {
			t.Fatalf("err=%v", err)
		}
		if failure.Details["actor"] != "arch" || failure.Details["caller"] != "alice" || failure.Details["reason"] != "caller_token_scoped_upstream" {
			t.Fatalf("details=%+v", failure.Details)
		}
	})
	t.Run("owner gets token", func(t *testing.T) {
		token, err := requireOwnerScopedOAuthBearer(shareGrantToolContext("arch", "arch"))
		if err != nil || token != "oauth-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	})
	t.Run("no actor gets token", func(t *testing.T) {
		token, err := requireOwnerScopedOAuthBearer(articleDraftTestContext())
		if err != nil || token != "test-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	})
	t.Run("x402 keeps missing oauth bearer failure", func(t *testing.T) {
		_, err := requireOwnerScopedOAuthBearer(x402GrantToolContext("arch", "paid-caller"))
		failure := mcpAuthFailureFromError(err)
		if failure == nil || failure.Code != "unauthorized" || !strings.Contains(failure.Message, "OAuth bearer token") {
			t.Fatalf("x402 path must keep pre-existing oauth gate failure, err=%v", err)
		}
	})
}

func installFailingLesserUpstream(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("share-grant caller must not reach lesser upstream, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
}

func assertForbiddenShareResult(t *testing.T, res *mcpruntime.ToolResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
	errPayload, _ := res.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "forbidden" {
		t.Fatalf("error payload = %+v", res.StructuredContent)
	}
	if status, _ := errPayload["status"].(int); status != 403 {
		t.Fatalf("error status = %+v", errPayload)
	}
}

func TestSocialToolsFailClosedForShareGrantCaller(t *testing.T) {
	installFailingLesserUpstream(t)

	// Still-gated sites: upstream surfaces with no act-as support keep the
	// exact pre-existing 403 shape.
	res, err := handleFollow(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"account_id":"1"}`))
	assertForbiddenShareResult(t, res, err)

	res, err = handlePostSearch(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"query":"hello"}`))
	assertForbiddenShareResult(t, res, err)

	res, err = handleProfileUpdate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"display_name":"x"}`))
	assertForbiddenShareResult(t, res, err)

	// timeline_read multiplexes timelines: only home is act-as-enabled
	// upstream, so local/federated stay fail-closed on the share path.
	res, err = handleTimelineRead(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"timeline":"local"}`))
	assertForbiddenShareResult(t, res, err)

	res, err = handleTimelineRead(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"timeline":"federated"}`))
	assertForbiddenShareResult(t, res, err)
}

func TestSocialOwnerPathStillProxiesOwnBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Lesser-Act-As"); got != "" {
			t.Fatalf("owner path must never send X-Lesser-Act-As, got %q", got)
		}
		if r.URL.Path != "/api/v1/accounts/verify_credentials" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","username":"arch"}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	res, err := handleProfileRead(shareGrantToolContext("arch", "arch"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("owner profile_read: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("owner profile_read result = %+v", res)
	}
	if _, ok := res.StructuredContent["act_as"]; ok {
		t.Fatalf("owner path must not carry the act-as identity marker, got %+v", res.StructuredContent)
	}
}

func TestArticleDraftCreateReachesUpstreamForShareGrantCaller(t *testing.T) {
	// article_draft_create is act-as-enabled upstream: the share path must
	// reach lesser instead of failing closed. The act-as header + caller
	// bearer assertions live in share_act_as_test.go; here a failing upstream
	// proves the gate no longer short-circuits with a 403.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","author":{"id":"https://example.com/users/arch","username":"arch"},"contentType":"ARTICLE","status":"DRAFT","contentFormat":"MARKDOWN","revision":1}}}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	res, err := handleArticleDraftCreate(shareGrantToolContext("arch", "alice"), json.RawMessage(`{"content":"draft body"}`))
	if err != nil {
		t.Fatalf("article_draft_create: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("article_draft_create result = %+v", res)
	}
}

func TestArticleListShareGrantCallerUsesActorAuthorScope(t *testing.T) {
	var authorIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		authorID, _ := op.Variables["authorId"].(string)
		authorIDs = append(authorIDs, authorID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":0}}}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	res, err := handleArticleList(shareGrantToolContext("arch", "alice"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("article_list: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("article_list result = %+v", res)
	}
	if len(authorIDs) != 1 || authorIDs[0] != "arch" {
		t.Fatalf("authorId variables = %+v, want actor scope", authorIDs)
	}
}

func TestSoulReadPrivateMintConversationsFailsClosedForShareGrantCaller(t *testing.T) {
	installFailingLesserUpstream(t)

	_, err := soulReadPrivateMintConversations(shareGrantToolContext("arch", "alice"), soulReadPrivateRequest{Limit: 5})
	failure := mcpAuthFailureFromError(err)
	if failure == nil || failure.Status != 403 || failure.Code != "forbidden" {
		t.Fatalf("err=%v", err)
	}
}

func assertForbiddenShareResourceContents(t *testing.T, contents []mcpruntime.ResourceContent, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents = %+v", contents)
	}
	var payload struct {
		Error struct {
			Code    string         `json:"code"`
			Status  int            `json:"status"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if uerr := json.Unmarshal([]byte(contents[0].Text), &payload); uerr != nil {
		t.Fatalf("unmarshal resource error payload: %v", uerr)
	}
	if payload.Error.Code != "forbidden" || payload.Error.Status != 403 {
		t.Fatalf("error payload = %s", contents[0].Text)
	}
	if payload.Error.Details["reason"] != "caller_token_scoped_upstream" {
		t.Fatalf("details = %+v", payload.Error.Details)
	}
}

func TestResourceFollowersFollowingFailClosedForShareGrantCaller(t *testing.T) {
	installFailingLesserUpstream(t)

	// Followers/following resolve owner-only upstream endpoints: the share
	// path keeps the exact pre-existing 403 shape.
	contents, err := resourceFollowers(shareGrantToolContext("arch", "alice"))
	assertForbiddenShareResourceContents(t, contents, err)

	contents, err = resourceFollowing(shareGrantToolContext("arch", "alice"))
	assertForbiddenShareResourceContents(t, contents, err)
}

func TestResourceTimelineLocalFederatedStayGatedForShareGrantCaller(t *testing.T) {
	installFailingLesserUpstream(t)

	// Only the home timeline is act-as-enabled upstream; local/federated
	// public timelines keep the exact gated 403 shape (mirrors timeline_read).
	contents, err := resourceTimeline("local")(shareGrantToolContext("arch", "alice"))
	assertForbiddenShareResourceContents(t, contents, err)

	contents, err = resourceTimeline("federated")(shareGrantToolContext("arch", "alice"))
	assertForbiddenShareResourceContents(t, contents, err)
}

func TestSharedCallerActedByUnchangedAfterSeamRefactor(t *testing.T) {
	if got := sharedCallerActedBy(shareGrantToolContext("arch", "alice")); got != "alice" {
		t.Fatalf("grantee actedBy = %q", got)
	}
	if got := sharedCallerActedBy(shareGrantToolContext("arch", "arch")); got != "" {
		t.Fatalf("owner actedBy = %q", got)
	}
	if got := sharedCallerActedBy(articleDraftTestContext()); got != "" {
		t.Fatalf("no-actor actedBy = %q", got)
	}
	if got := sharedCallerActedBy(x402GrantToolContext("arch", "paid-caller")); got != "" {
		t.Fatalf("x402 actedBy = %q", got)
	}
}
