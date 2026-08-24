package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

func TestArticleDraftToolDescriptionsDeclareAuthorizationBoundary(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := registerArticleTools(registry); err != nil {
		t.Fatalf("registerArticleTools: %v", err)
	}

	want := map[string]string{
		"article_draft_create":  "owner-scoped",
		"article_draft_update":  "owner-scoped",
		"article_draft_get":     "active reviewer grant",
		"article_draft_list":    "owner-scoped",
		"article_draft_preview": "active reviewer grant",
		"article_draft_publish": "owner-scoped",
	}
	for _, def := range registry.List() {
		boundary, ok := want[def.Name]
		if !ok {
			continue
		}
		delete(want, def.Name)
		description := strings.ToLower(def.Description)
		if !strings.Contains(description, boundary) {
			t.Errorf("%s description does not declare %q boundary: %q", def.Name, boundary, def.Description)
		}
	}
	for name := range want {
		t.Errorf("draft tool %s was not registered", name)
	}
}

func TestArticleDraftGetUsesCallerAuthorizedReviewAfterOwnerLookupNotFound(t *testing.T) {
	const source = "# Exact reviewer source\n\nDo not truncate this evidence."
	var operations []cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want exact caller bearer", got)
			http.Error(w, "unexpected bearer", http.StatusInternalServerError)
			return
		}
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"draft":null}}`))
		case "BodyArticleDraftReview":
			_, _ = fmt.Fprintf(w, `{"data":{"draftReview":{"draftId":"draft-1","ownerId":"author","title":"Review me","slug":"review-me","content":%q,"renderedHtml":"<h1>Review me</h1>","renderErrors":[],"contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-08-09T12:00:00Z","createdAt":"2026-08-09T11:00:00Z","contentHash":"sha256:review","revision":4,"activeReviewerIds":["reviewer"],"publishEligible":false,"publishBlockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"reviewer","grantedAt":"2026-08-09T11:30:00Z","status":"ACTIVE"}],"grant":{"reviewerId":"reviewer","grantedAt":"2026-08-09T11:30:00Z","status":"ACTIVE"},"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`, source)
		default:
			t.Errorf("unexpected operation %q", op.OperationName)
			http.Error(w, "unexpected operation", http.StatusBadRequest)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard","max_output_bytes":50000}`))
	if err != nil {
		t.Fatalf("article_draft_get: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_draft_get result = %+v", result)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	draft, _ := data["draft"].(map[string]any)
	if draft["id"] != "draft-1" || draft["content"] != source || draft["contentType"] != cmsapi.ObjectTypeArticle || draft["contentHash"] != "sha256:review" || draft["revision"] != 4 {
		t.Fatalf("reviewer draft projection = %+v", draft)
	}
	if len(operations) != 2 || operations[0].OperationName != "BodyArticleDraft" || operations[1].OperationName != "BodyArticleDraftReview" {
		t.Fatalf("operations = %+v", operations)
	}
	for _, field := range []string{"ownerId", "slug", "content", "contentHash", "revision"} {
		if !strings.Contains(operations[1].Query, field) {
			t.Fatalf("review fallback query missing %q: %s", field, operations[1].Query)
		}
	}
	if strings.Contains(operations[1].Query, "renderedHtml") || strings.Contains(operations[1].Query, "renderErrors") {
		t.Fatalf("draft get must not transport unused rendering: %s", operations[1].Query)
	}
	policy, _ := data["policy"].(map[string]any)
	if policy["readAuthorization"] != "lesser_owner_or_active_reviewer" || policy["reviewerProjection"] != "caller_authorized_draftReview_snapshot" {
		t.Fatalf("reviewer draft policy = %+v", policy)
	}
}

func TestArticleDraftGetFailsClosedWhenCallerHasNoActiveReviewGrant(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND","http_status":404}}]}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-revoked","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_get: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("revoked reviewer result = %+v", result)
	}
	if len(operations) != 2 || operations[0] != "BodyArticleDraft" || operations[1] != "BodyArticleDraftReview" {
		t.Fatalf("operations = %+v", operations)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if bytes.Contains(encoded, []byte("content")) {
		t.Fatalf("authorization failure leaked draft content: %s", encoded)
	}
}

func TestArticleDraftGetDoesNotFallbackOnNonNotFoundOwnerFailure(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"CMS unavailable","extensions":{"code":"INTERNAL","http_status":500}}]}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_get: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("internal owner lookup failure result = %+v", result)
	}
	if len(operations) != 1 || operations[0] != "BodyArticleDraft" {
		t.Fatalf("non-not-found failure must not trigger reviewer fallback, operations = %+v", operations)
	}
}

func TestArticleDraftPreviewDelegatesOwnerOrActiveReviewerAuthorizationToLesser(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want exact caller bearer", got)
			http.Error(w, "unexpected bearer", http.StatusInternalServerError)
			return
		}
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyArticleDraftReview":
			if op.Variables["id"] == "draft-revoked" {
				_, _ = w.Write([]byte(`{"data":{"draftReview":null}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-08-09T12:00:00Z","createdAt":"2026-08-09T11:00:00Z","contentHash":"sha256:review","revision":4,"activeReviewerIds":["reviewer"],"publishEligible":false,"publishBlockingReasons":[],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[],"grant":{"reviewerId":"reviewer","grantedAt":"2026-08-09T11:30:00Z","status":"ACTIVE"},"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":[],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		case "BodyArticleDraftPreview":
			_, _ = w.Write([]byte(`{"data":{"draftPreview":{"draftId":"draft-1","success":true,"renderedHtml":"<h1>Authorized review</h1>","sourceFormat":"MARKDOWN","sourceBytes":24,"renderedBytes":26,"errors":[]}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"owner-only draft preflight is forbidden in reviewer mode","extensions":{"code":"NOT_FOUND","http_status":404}}]}`))
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftPreview(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_preview: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("authorized reviewer preview = %+v", result)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	preview, _ := data["preview"].(map[string]any)
	if preview["renderedHtml"] != "<h1>Authorized review</h1>" {
		t.Fatalf("reviewer preview = %+v", preview)
	}
	policy, _ := data["policy"].(map[string]any)
	if policy["readAuthorization"] != "lesser_owner_or_active_reviewer" || policy["statePreflight"] != "caller_authorized_draftReview" {
		t.Fatalf("reviewer preview policy = %+v", policy)
	}

	revoked, err := handleArticleDraftPreview(articleDraftTestContext(), json.RawMessage(`{"id":"draft-revoked","view":"standard"}`))
	if err != nil {
		t.Fatalf("revoked article_draft_preview: %v", err)
	}
	if revoked == nil || !revoked.IsError {
		t.Fatalf("revoked reviewer preview = %+v", revoked)
	}
	revokedPayload := toolErrorPayloadForTest(t, revoked)
	revokedDetails, _ := revokedPayload["details"].(map[string]any)
	if revokedPayload["code"] != "not_found" || intFromAny(revokedPayload["status"]) != http.StatusNotFound ||
		revokedDetails["lookup"] != "id" || revokedDetails["tool"] != "article_draft_preview" {
		t.Fatalf("revoked preview payload = %#v, details = %#v", revokedPayload, revokedDetails)
	}
	if len(operations) != 3 || operations[0] != "BodyArticleDraftReview" || operations[1] != "BodyArticleDraftPreview" || operations[2] != "BodyArticleDraftReview" {
		t.Fatalf("preview must use Lesser's caller-authorized review preflight and grant-aware draftPreview, operations = %+v", operations)
	}
}

func TestArticleDraftUpdateReturnsNotFoundForTypedMissingOwnerLookup(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		if op.OperationName != "BodyArticleDraft" {
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
		_, _ = w.Write([]byte(`{"data":{"draft":null}}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftUpdate(articleDraftTestContext(), json.RawMessage(`{"id":"draft-missing","title":"Updated"}`))
	if err != nil {
		t.Fatalf("article_draft_update: %v", err)
	}
	payload := toolErrorPayloadForTest(t, result)
	if payload["code"] != "not_found" || intFromAny(payload["status"]) != http.StatusNotFound {
		t.Fatalf("error payload = %#v, want not_found/404", payload)
	}
	details, _ := payload["details"].(map[string]any)
	if details["lookup"] != "id" || details["tool"] != "article_draft_update" {
		t.Fatalf("error details = %#v, want lookup=id tool=article_draft_update", details)
	}
	if len(operations) != 1 || operations[0] != "BodyArticleDraft" {
		t.Fatalf("update preflight operations = %+v, want only BodyArticleDraft", operations)
	}
}

func TestArticleDraftPublishReturnsNotFoundForTypedMissingOwnerLookup(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		if op.OperationName != "BodyArticleDraft" {
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
		_, _ = w.Write([]byte(`{"data":{"draft":null}}`))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleDraftPublish(articleDraftTestContext(), json.RawMessage(`{"id":"draft-missing"}`))
	if err != nil {
		t.Fatalf("article_draft_publish: %v", err)
	}
	payload := toolErrorPayloadForTest(t, result)
	if payload["code"] != "not_found" || intFromAny(payload["status"]) != http.StatusNotFound {
		t.Fatalf("error payload = %#v, want not_found/404", payload)
	}
	details, _ := payload["details"].(map[string]any)
	if details["lookup"] != "id" || details["tool"] != "article_draft_publish" {
		t.Fatalf("error details = %#v, want lookup=id tool=article_draft_publish", details)
	}
	if len(operations) != 1 || operations[0] != "BodyArticleDraft" {
		t.Fatalf("publish preflight operations = %+v, want only BodyArticleDraft", operations)
	}
}

func TestArticleDraftByIDToolsRejectOwnedNonArticleDrafts(t *testing.T) {
	testArticleDraftByIDToolsRejectDraft(t, cmsapi.Draft{
		ID:            "draft-non-article",
		AuthorID:      "alice",
		ContentType:   "NOTE",
		Status:        cmsapi.DraftStatusDraft,
		ContentFormat: cmsapi.ContentFormatMarkdown,
		Content:       "not an article",
	})
}

func TestArticleDraftByIDToolsRejectOwnedNonDraftStatus(t *testing.T) {
	testArticleDraftByIDToolsRejectDraft(t, cmsapi.Draft{
		ID:            "draft-published",
		AuthorID:      "alice",
		ContentType:   cmsapi.ObjectTypeArticle,
		Status:        "PUBLISHED",
		ContentFormat: cmsapi.ContentFormatMarkdown,
		Content:       "not a draft",
	})
}

func testArticleDraftByIDToolsRejectDraft(t *testing.T, draft cmsapi.Draft) {
	t.Helper()

	testCases := []struct {
		name       string
		args       string
		handler    func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error)
		assertions func(*testing.T, *articleDraftGraphQLTestServer)
	}{
		{
			name:    "get",
			args:    fmt.Sprintf(`{"id":%q}`, draft.ID),
			handler: handleArticleDraftGet,
		},
		{
			name:    "update",
			args:    fmt.Sprintf(`{"id":%q,"title":"mutated"}`, draft.ID),
			handler: handleArticleDraftUpdate,
			assertions: func(t *testing.T, server *articleDraftGraphQLTestServer) {
				t.Helper()
				if got := server.updateCalls(); got != 0 {
					t.Fatalf("updateDraft mutation calls = %d, want 0", got)
				}
				if got := server.title(); got == "mutated" {
					t.Fatalf("draft title mutated before article-draft validation")
				}
			},
		},
	}
	// Lesser's canonical renderer owns the ARTICLE content-type check for
	// preview. Body still performs a caller-authorized DRAFT-state preflight.
	if draft.ContentType == cmsapi.ObjectTypeArticle {
		testCases = append(testCases, struct {
			name       string
			args       string
			handler    func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error)
			assertions func(*testing.T, *articleDraftGraphQLTestServer)
		}{
			name:    "preview",
			args:    fmt.Sprintf(`{"id":%q}`, draft.ID),
			handler: handleArticleDraftPreview,
		})
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := newArticleDraftGraphQLTestServer(t, draft)

			res, err := tc.handler(articleDraftTestContext(), json.RawMessage(tc.args))
			assertArticleDraftNotFound(t, res, err)
			if tc.assertions != nil {
				tc.assertions(t, server)
			}
		})
	}
}

type articleDraftGraphQLTestServer struct {
	t      *testing.T
	server *httptest.Server

	mu           sync.Mutex
	draft        cmsapi.Draft
	updateCount  int
	previewCount int
}

func newArticleDraftGraphQLTestServer(t *testing.T, draft cmsapi.Draft) *articleDraftGraphQLTestServer {
	t.Helper()

	if draft.Title == nil {
		title := "original"
		draft.Title = &title
	}
	if strings.TrimSpace(draft.ID) == "" {
		draft.ID = "draft-test"
	}

	h := &articleDraftGraphQLTestServer{
		t:     t,
		draft: draft,
	}
	h.server = httptest.NewServer(http.HandlerFunc(h.serveGraphQL))
	t.Cleanup(func() {
		h.server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", h.server.URL)
	lesserapi.ResetForTests()
	return h
}

func (h *articleDraftGraphQLTestServer) serveGraphQL(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/graphql" {
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		h.t.Fatalf("Authorization header = %q, want bearer passthrough", got)
	}

	var req struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.t.Fatalf("decode graphql request: %v", err)
	}

	id, _ := req.Variables["id"].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != "" && id != h.draft.ID {
		h.t.Fatalf("graphql id = %q, want %q", id, h.draft.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	switch req.OperationName {
	case "BodyArticleDraft":
		writeArticleDraftGraphQLData(h.t, w, map[string]any{"draft": h.draft})
	case "BodyArticleDraftReview":
		writeArticleDraftGraphQLData(h.t, w, map[string]any{"draftReview": map[string]any{
			"draftId": h.draft.ID,
			"status":  h.draft.Status,
		}})
	case "BodyArticleDraftPreview":
		h.previewCount++
		writeArticleDraftGraphQLData(h.t, w, map[string]any{"draftPreview": cmsapi.DraftPreview{
			DraftID:       h.draft.ID,
			Success:       true,
			SourceFormat:  cmsapi.ContentFormatMarkdown,
			SourceBytes:   len(h.draft.Content),
			RenderedBytes: len(h.draft.Content),
		}})
	case "BodyUpdateArticleDraft":
		h.updateCount++
		if input, _ := req.Variables["input"].(map[string]any); input != nil {
			if title, _ := input["title"].(string); title != "" {
				h.draft.Title = &title
			}
			if content, _ := input["content"].(string); content != "" {
				h.draft.Content = content
			}
		}
		writeArticleDraftGraphQLData(h.t, w, map[string]any{"updateDraft": h.draft})
	default:
		h.t.Fatalf("unexpected graphql operation %q", req.OperationName)
	}
}

func (h *articleDraftGraphQLTestServer) updateCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updateCount
}

func (h *articleDraftGraphQLTestServer) title() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.draft.Title == nil {
		return ""
	}
	return *h.draft.Title
}

func writeArticleDraftGraphQLData(t *testing.T, w http.ResponseWriter, data map[string]any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Fatalf("encode graphql response: %v", err)
	}
}

func articleDraftTestContext() context.Context {
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "alice",
	}, "test-token")
}

func assertArticleDraftNotFound(t *testing.T, res *mcpruntime.ToolResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res == nil {
		t.Fatal("handler returned nil result")
	}
	if !res.IsError {
		t.Fatalf("handler IsError = false, want true; result = %#v", res.StructuredContent)
	}
	errPayload, _ := res.StructuredContent["error"].(map[string]any)
	if errPayload == nil {
		t.Fatalf("structuredContent.error missing: %#v", res.StructuredContent)
	}
	if got := errPayload["code"]; got != "not_found" {
		t.Fatalf("error code = %#v, want not_found; payload = %#v", got, errPayload)
	}
	if got := fmt.Sprint(errPayload["status"]); got != "404" {
		t.Fatalf("error status = %#v, want 404; payload = %#v", errPayload["status"], errPayload)
	}
}

func TestDraftOwnershipAcceptsLesserAuthorActor(t *testing.T) {
	draft := &cmsapi.Draft{
		ID: "draft-author-actor",
		Author: &cmsapi.Actor{
			ID:       "https://example.com/users/alice",
			Username: "alice",
		},
		ContentType: cmsapi.ObjectTypeArticle,
		Status:      cmsapi.DraftStatusDraft,
	}
	if !draftOwnedByAuthenticatedActor(articleDraftTestContext(), draft) {
		t.Fatalf("expected Lesser Draft.author username/id to satisfy ownership check")
	}
	draft.Author.Username = ""
	if !draftOwnedByAuthenticatedActor(articleDraftTestContext(), draft) {
		t.Fatalf("expected local actor id segment to satisfy ownership check")
	}
}
