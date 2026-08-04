package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestOwnerScopedDraftReviewFallbackSharesPostAndReturnsFeedback(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("LESSER_TABLE_NAME", "")
	t.Setenv("JWT_SECRET", "test")
	installTrustConfigIsolation(t)
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	authorToken := newTestToken(t, "test", "author", []string{"write"})
	reviewerToken := newTestToken(t, "test", "reviewer", []string{"write"})
	authorAuth := "Bearer " + authorToken
	reviewerAuth := "Bearer " + reviewerToken
	const draftBody = "DRAFT_REVIEW_BODY: tighten the opening and verify the conclusion."

	state := struct {
		sync.Mutex
		draftCreated      bool
		reviewPostContent string
		feedbackContent   string
		publishCalls      int
	}{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/graphql":
			var op struct {
				OperationName string         `json:"operationName"`
				Variables     map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
				t.Errorf("decode GraphQL operation: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			state.Lock()
			defer state.Unlock()
			switch op.OperationName {
			case "BodyCreateArticleDraft":
				if r.Header.Get("Authorization") != authorAuth {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":"wrong draft owner"}`))
					return
				}
				input, _ := op.Variables["input"].(map[string]any)
				if input["content"] != draftBody || input["contentType"] != "ARTICLE" {
					t.Errorf("create draft input = %+v", input)
				}
				state.draftCreated = true
				_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","authorId":"author","contentType":"ARTICLE","title":"Review me","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"createdAt":"2026-07-26T13:00:00Z","updatedAt":"2026-07-26T13:00:00Z"}}}`))
			case "BodyArticleDraft":
				if r.Header.Get("Authorization") != reviewerAuth {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":"wrong reviewer"}`))
					return
				}
				// Defense-in-depth fixture: even if an upstream response carries an
				// owner-mismatched draft, Body must return not_found without content.
				_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","authorId":"author","contentType":"ARTICLE","title":"Review me","content":"` + draftBody + `","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1}}}`))
			case "BodyPublishArticleDraft":
				state.publishCalls++
				_, _ = w.Write([]byte(`{"errors":[{"message":"publication is outside the review loop"}]}`))
			default:
				t.Errorf("unexpected GraphQL operation %q", op.OperationName)
				w.WriteHeader(http.StatusBadRequest)
			}

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/statuses":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode status body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			state.Lock()
			defer state.Unlock()
			switch r.Header.Get("Authorization") {
			case authorAuth:
				content, _ := body["status"].(string)
				if body["visibility"] != "direct" || !strings.Contains(content, "@reviewer") || !strings.Contains(content, draftBody) {
					t.Errorf("author review post = %+v", body)
				}
				state.reviewPostContent = content
				_, _ = w.Write([]byte(`{"id":"review-post-1","visibility":"direct"}`))
			case reviewerAuth:
				content, _ := body["status"].(string)
				if body["visibility"] != "direct" || body["in_reply_to_id"] != "review-post-1" || !strings.Contains(content, "@author") {
					t.Errorf("reviewer feedback post = %+v", body)
				}
				state.feedbackContent = content
				_, _ = w.Write([]byte(`{"id":"feedback-post-1","visibility":"direct","in_reply_to_id":"review-post-1"}`))
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unknown actor"}`))
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/conversations/lookup":
			state.Lock()
			defer state.Unlock()
			switch r.Header.Get("Authorization") {
			case reviewerAuth:
				if r.URL.Query().Get("counterpart") != "author" || state.reviewPostContent == "" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"review copy not found"}`))
					return
				}
				writeReviewConversation(t, w, "conv-review", "author", "review-post-1", state.reviewPostContent, "")
			case authorAuth:
				if r.URL.Query().Get("counterpart") != "reviewer" || state.feedbackContent == "" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"feedback not found"}`))
					return
				}
				writeReviewConversation(t, w, "conv-review", "reviewer", "feedback-post-1", state.feedbackContent, "review-post-1")
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unknown actor"}`))
			}

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authorSession := initializeActorSession(t, env, app, "/mcp/author", authorAuth)
	reviewerSession := initializeActorSession(t, env, app, "/mcp/reviewer", reviewerAuth)

	created := callActorTool(t, env, app, "/mcp/author", authorAuth, authorSession, 20, "article_draft_create", map[string]any{
		"title":   "Review me",
		"content": draftBody,
	})
	createdData := requireToolData(t, created)
	createdDraft, _ := createdData["draft"].(map[string]any)
	if createdDraft["id"] != "draft-1" || createdDraft["status"] != "DRAFT" {
		t.Fatalf("created draft = %+v", createdData)
	}

	crossActor := callActorTool(t, env, app, "/mcp/reviewer", reviewerAuth, reviewerSession, 21, "article_draft_get", map[string]any{
		"id":   "draft-1",
		"view": "standard",
	})
	if !crossActor.IsError {
		t.Fatalf("cross-actor draft read unexpectedly succeeded: %+v", crossActor.StructuredContent)
	}
	crossActorJSON, _ := json.Marshal(crossActor.StructuredContent)
	if strings.Contains(string(crossActorJSON), draftBody) {
		t.Fatalf("owner-scoped rejection leaked draft content: %s", crossActorJSON)
	}

	reviewPost := callActorTool(t, env, app, "/mcp/author", authorAuth, authorSession, 22, "post_create", map[string]any{
		"content":    "@reviewer Pre-publication review copy for draft-1:\n" + draftBody,
		"visibility": "direct",
	})
	if reviewPost.IsError {
		t.Fatalf("review post failed: %+v", reviewPost.StructuredContent)
	}

	reviewerRead := callActorTool(t, env, app, "/mcp/reviewer", reviewerAuth, reviewerSession, 23, "direct_messages_read", map[string]any{
		"counterpart": "author",
		"view":        "standard",
	})
	reviewerData := requireToolData(t, reviewerRead)
	reviewerJSON, _ := json.Marshal(reviewerData)
	if !strings.Contains(string(reviewerJSON), draftBody) || !strings.Contains(string(reviewerJSON), "review-post-1") {
		t.Fatalf("reviewer did not receive shared pre-publication review copy: %s", reviewerJSON)
	}

	feedback := callActorTool(t, env, app, "/mcp/reviewer", reviewerAuth, reviewerSession, 24, "post_create", map[string]any{
		"content":     "@author Feedback for draft-1: tighten the opening; conclusion verified.",
		"visibility":  "direct",
		"in_reply_to": "review-post-1",
	})
	if feedback.IsError {
		t.Fatalf("feedback reply failed: %+v", feedback.StructuredContent)
	}

	authorRead := callActorTool(t, env, app, "/mcp/author", authorAuth, authorSession, 25, "direct_messages_read", map[string]any{
		"counterpart": "reviewer",
		"view":        "standard",
	})
	authorData := requireToolData(t, authorRead)
	authorJSON, _ := json.Marshal(authorData)
	if !strings.Contains(string(authorJSON), "Feedback for draft-1") || !strings.Contains(string(authorJSON), "review-post-1") {
		t.Fatalf("author did not receive threaded feedback: %s", authorJSON)
	}

	state.Lock()
	defer state.Unlock()
	if !state.draftCreated || state.reviewPostContent == "" || state.feedbackContent == "" || state.publishCalls != 0 {
		t.Fatalf("review loop state: draftCreated=%t reviewShared=%t feedbackReturned=%t publishCalls=%d", state.draftCreated, state.reviewPostContent != "", state.feedbackContent != "", state.publishCalls)
	}
}

func writeReviewConversation(t testing.TB, w http.ResponseWriter, conversationID string, actor string, messageID string, content string, inReplyTo string) {
	t.Helper()
	message := map[string]any{
		"id":         messageID,
		"content":    content,
		"created_at": "2026-07-26T13:05:00Z",
		"visibility": "direct",
		"account": map[string]any{
			"id":       "acct-" + actor,
			"username": actor,
			"acct":     actor + "@example.test",
		},
	}
	if inReplyTo != "" {
		message["in_reply_to_id"] = inReplyTo
	}
	payload := map[string]any{
		"id":          conversationID,
		"unread":      true,
		"accounts":    []any{message["account"]},
		"messages":    []any{message},
		"last_status": message,
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode review conversation: %v", err)
	}
}
