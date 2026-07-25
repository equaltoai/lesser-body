package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
	"github.com/theory-cloud/apptheory/v2/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

var allArticleToolNames = []string{
	"article_draft_create",
	"article_draft_update",
	"article_draft_get",
	"article_draft_list",
	"article_draft_preview",
	"article_draft_publish",
	"article_update",
	"article_get",
	"article_list",
}

var articleWriteToolNames = []string{
	"article_draft_create",
	"article_draft_update",
	"article_draft_publish",
	"article_update",
}

var articleReadToolNames = []string{
	"article_draft_get",
	"article_draft_list",
	"article_draft_preview",
	"article_get",
	"article_list",
}

func TestArticleToolsRuntimeProfilesAreExplicit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(testing.TB)
	}{
		{
			name: "drone",
			setup: func(tb testing.TB) {
				tb.Helper()
				installMissingSoulBindingLookup(tb)
			},
		},
		{
			name: "souled",
			setup: func(tb testing.TB) {
				tb.Helper()
				installSoulBindingLookup(tb, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_SESSION_TABLE", "")
			t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
			t.Setenv("JWT_SECRET", "test")
			tc.setup(t)
			auth.ResetForTests()

			app, err := mcpapp.New("test", "dev")
			if err != nil {
				t.Fatalf("new app: %v", err)
			}
			env := testkit.New()
			token := newTestTokenWithAudience(t, "test", "agent1", []string{"read", "write"}, []string{"https://api.example.com/mcp/agent1"})
			initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
				"authorization": {"Bearer " + token},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
			if initResp.Status != 200 {
				t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
			}
			sessionID := initResp.Headers["mcp-session-id"][0]

			listResp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {"Bearer " + token},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
			if listResp.Status != 200 {
				t.Fatalf("tools/list: status=%d body=%s", listResp.Status, string(listResp.Body))
			}
			var rpc mcpruntime.Response
			if err := json.Unmarshal(listResp.Body, &rpc); err != nil {
				t.Fatalf("unmarshal tools/list: %v", err)
			}
			if rpc.Error != nil {
				t.Fatalf("tools/list error: %+v", rpc.Error)
			}
			var out struct {
				Tools []mcpruntime.ToolDef `json:"tools"`
			}
			b, _ := json.Marshal(rpc.Result)
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("unmarshal tools/list result: %v", err)
			}
			have := map[string]bool{}
			for _, tool := range out.Tools {
				have[tool.Name] = true
			}
			for _, name := range allArticleToolNames {
				if !have[name] {
					t.Fatalf("%s runtime should expose Article tool %q, tools=%+v", tc.name, name, out.Tools)
				}
			}
		})
	}
}

func TestArticleToolScopesAtMCPBoundary(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		requests++
		var op map[string]any
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch op["operationName"] {
		case "BodyCreateArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Draft","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-20T00:00:00Z","createdAt":"2026-05-20T00:00:00Z","updatedAt":"2026-05-20T00:00:00Z"}}}`))
		case "BodyUpdateArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"updateDraft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Draft updated","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":2,"lastSavedAt":"2026-05-20T00:01:00Z","createdAt":"2026-05-20T00:00:00Z","updatedAt":"2026-05-20T00:01:00Z"}}}`))
		case "BodyArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Draft","content":"draft body","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-20T00:00:00Z","createdAt":"2026-05-20T00:00:00Z","updatedAt":"2026-05-20T00:00:00Z"}}}`))
		case "BodyArticleDrafts":
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"node":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Draft","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-20T00:00:00Z","createdAt":"2026-05-20T00:00:00Z","updatedAt":"2026-05-20T00:00:00Z"},"cursor":"draft-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		case "BodyArticleDraftPreview":
			_, _ = w.Write([]byte(`{"data":{"draftPreview":{"draftId":"draft-1","success":true,"renderedHtml":"<p>draft body</p>","sourceFormat":"MARKDOWN","sourceBytes":10,"renderedBytes":17,"errors":[]}}}`))
		case "BodyPublishArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"publishDraft":{"id":"https://example.com/articles/draft","slug":"draft","title":"Draft","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":2,"publishedAt":"2026-05-20T00:02:00Z","createdAt":"2026-05-20T00:02:00Z","updatedAt":"2026-05-20T00:02:00Z"}}}`))
		case "BodyUpdateArticle":
			_, _ = w.Write([]byte(`{"data":{"updateArticle":{"id":"https://example.com/articles/draft","slug":"draft","title":"Article updated","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":2,"publishedAt":"2026-05-20T00:02:00Z","createdAt":"2026-05-20T00:02:00Z","updatedAt":"2026-05-20T00:03:00Z"}}}`))
		case "BodyArticle":
			_, _ = w.Write([]byte(`{"data":{"article":{"id":"https://example.com/articles/draft","slug":"draft","title":"Article","content":"article body","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":2,"publishedAt":"2026-05-20T00:02:00Z","createdAt":"2026-05-20T00:02:00Z","updatedAt":"2026-05-20T00:02:00Z"}}}`))
		case "BodyArticles":
			vars := op["variables"].(map[string]any)
			if strings.TrimSpace(vars["authorId"].(string)) != "agent1" {
				t.Fatalf("article_list should bind authorId to authenticated actor, got %+v", vars)
			}
			_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[{"node":{"id":"https://example.com/articles/draft","slug":"draft","title":"Article","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":2,"publishedAt":"2026-05-20T00:02:00Z","createdAt":"2026-05-20T00:02:00Z","updatedAt":"2026-05-20T00:02:00Z"},"cursor":"article-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		default:
			t.Fatalf("unexpected operation %q", op["operationName"])
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
	readAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	writeAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"write"})
	adminAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"admin"})

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {readAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	for i, name := range articleReadToolNames {
		callArticleTool(t, env, app, readAuth, sessionID, 10+i, name, articleToolArgs(name))
	}

	beforeDenied := requests
	for i, name := range articleWriteToolNames {
		assertArticleToolForbidden(t, env, app, readAuth, sessionID, 20+i, name, articleToolArgs(name))
	}
	if requests != beforeDenied {
		t.Fatalf("read-scoped denied Article writes should not reach Lesser: before=%d after=%d", beforeDenied, requests)
	}

	for i, name := range allArticleToolNames {
		callArticleTool(t, env, app, writeAuth, sessionID, 30+i, name, articleToolArgs(name))
	}
	for i, name := range allArticleToolNames {
		callArticleTool(t, env, app, adminAuth, sessionID, 50+i, name, articleToolArgs(name))
	}
}

func articleToolArgs(name string) map[string]any {
	switch name {
	case "article_draft_create":
		return map[string]any{"title": "Draft", "content": "draft body"}
	case "article_draft_update":
		return map[string]any{"id": "draft-1", "title": "Draft updated"}
	case "article_draft_get":
		return map[string]any{"id": "draft-1"}
	case "article_draft_list":
		return map[string]any{"limit": 1}
	case "article_draft_preview":
		return map[string]any{"id": "draft-1"}
	case "article_draft_publish":
		return map[string]any{"id": "draft-1"}
	case "article_update":
		return map[string]any{"id": "https://example.com/articles/draft", "title": "Article updated"}
	case "article_get":
		return map[string]any{"id": "https://example.com/articles/draft"}
	case "article_list":
		return map[string]any{"limit": 1}
	default:
		return map[string]any{}
	}
}

func assertArticleToolForbidden(t testing.TB, env *testkit.Env, app *apptheory.App, authHeader string, sessionID string, id int, name string, args map[string]any) {
	t.Helper()
	callParams, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
	if resp.Status != 403 {
		t.Fatalf("%s expected 403, got %d (%s)", name, resp.Status, string(resp.Body))
	}
}
