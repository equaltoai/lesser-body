package mcpserver

import (
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
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

func TestArticleDraftToolDescriptionsDeclareOwnerScoping(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := registerArticleTools(registry); err != nil {
		t.Fatalf("registerArticleTools: %v", err)
	}

	want := map[string]bool{
		"article_draft_create":  false,
		"article_draft_update":  false,
		"article_draft_get":     false,
		"article_draft_list":    false,
		"article_draft_preview": false,
		"article_draft_publish": false,
	}
	for _, def := range registry.List() {
		if _, ok := want[def.Name]; !ok {
			continue
		}
		want[def.Name] = true
		description := strings.ToLower(def.Description)
		if !strings.Contains(description, "owner-scoped") {
			t.Errorf("%s description does not declare owner scoping: %q", def.Name, def.Description)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("draft tool %s was not registered", name)
		}
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

	for _, tc := range []struct {
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
			name:    "preview",
			args:    fmt.Sprintf(`{"id":%q}`, draft.ID),
			handler: handleArticleDraftPreview,
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
	} {
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
