package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestArticleDraftToolsRegisteredWithCompactSchemas(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read", "write"})
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {"Bearer " + token}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
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
	var result struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	tools := map[string]mcpruntime.ToolDef{}
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}

	for _, name := range []string{"article_draft_create", "article_draft_update", "article_draft_get", "article_draft_list", "article_draft_preview", "article_draft_publish", "article_update", "article_get", "article_list"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("expected %s in tools/list", name)
		}
	}
	falsehood := false
	truth := true
	assertToolHint(t, tools["article_draft_create"], &falsehood, &falsehood, &falsehood)
	assertToolHint(t, tools["article_draft_update"], &falsehood, &falsehood, &falsehood)
	assertToolHint(t, tools["article_draft_get"], &truth, nil, nil)
	assertToolHint(t, tools["article_draft_list"], &truth, nil, nil)
	assertToolHint(t, tools["article_draft_preview"], &truth, nil, nil)
	assertToolHint(t, tools["article_draft_publish"], &falsehood, &falsehood, &falsehood)
	assertToolHint(t, tools["article_update"], &falsehood, &falsehood, &falsehood)
	assertToolHint(t, tools["article_get"], &truth, nil, nil)
	assertToolHint(t, tools["article_list"], &truth, nil, nil)

	var createSchema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(tools["article_draft_create"].InputSchema, &createSchema); err != nil {
		t.Fatalf("unmarshal article_draft_create schema: %v", err)
	}
	if !containsString(createSchema.Required, "content") {
		t.Fatalf("article_draft_create should require content, got %+v", createSchema.Required)
	}
	if got, _ := createSchema.Properties["max_output_bytes"]["type"].(string); got != "integer" {
		t.Fatalf("article_draft_create max_output_bytes schema = %+v", createSchema.Properties["max_output_bytes"])
	}
	viewEnum, _ := createSchema.Properties["view"]["enum"].([]any)
	if !reflect.DeepEqual(viewEnum, []any{"compact", "standard"}) {
		t.Fatalf("article_draft_create view enum = %+v", createSchema.Properties["view"])
	}
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_draft_create"], "data"), "draft", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_draft_get"], "data"), "draftRef", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_draft_list"], "data"), "drafts", "array")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_draft_preview"], "data"), "preview", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_draft_publish"], "data"), "article", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_get"], "data"), "articleRef", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, tools["article_list"], "data"), "articles", "array")
}

func TestArticleDraftToolsUseLesserGraphQLAndCompactDefaults(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var operations []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer auth: %q", got)
		}
		var op map[string]any
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		query, _ := op["query"].(string)
		if strings.Contains(query, "publishDraft") || strings.Contains(query, "preview") || strings.Contains(query, "api/v1/statuses") {
			t.Fatalf("draft tools must not expose preview/publish/status authoring query: %s", query)
		}

		w.Header().Set("Content-Type", "application/json")
		switch op["operationName"] {
		case "BodyCreateArticleDraft":
			vars := op["variables"].(map[string]any)
			input := vars["input"].(map[string]any)
			if input["contentType"] != "ARTICLE" || input["contentFormat"] != "MARKDOWN" {
				t.Fatalf("create input = %+v", input)
			}
			if queryContainsDraftContentSelection(query) {
				t.Fatalf("compact create should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Hello","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"}}}`))
		case "BodyArticleDraft":
			if !queryContainsDraftContentSelection(query) {
				t.Fatalf("draft get should request content so compact can produce a bounded preview: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Hello","slug":"hello","content":"` + strings.Repeat("body ", 80) + `","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"}}}`))
		case "BodyUpdateArticleDraft":
			vars := op["variables"].(map[string]any)
			if vars["id"] != "draft-1" {
				t.Fatalf("update variables = %+v", vars)
			}
			input := vars["input"].(map[string]any)
			if input["title"] != "Updated" {
				t.Fatalf("update input = %+v", input)
			}
			if queryContainsDraftContentSelection(query) {
				t.Fatalf("compact update should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"updateDraft":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Updated","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":2,"lastSavedAt":"2026-05-19T21:01:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:01:00Z"}}}`))
		case "BodyArticleDrafts":
			if queryContainsDraftContentSelection(query) {
				t.Fatalf("compact list should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"node":{"id":"draft-1","authorId":"agent1","contentType":"ARTICLE","title":"Hello","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"},"cursor":"draft-1"}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"startCursor":"draft-1","endCursor":"draft-1"},"totalCount":1}}}`))
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
	writeAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"write"})
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {writeAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	create := callArticleTool(t, env, app, writeAuth, sessionID, 2, "article_draft_create", map[string]any{
		"title":         "Hello",
		"content":       strings.Repeat("draft body ", 50),
		"preview_chars": 24,
	})
	createData := toolData(t, create)
	createDraft := createData["draft"].(map[string]any)
	if _, hasContent := createDraft["content"]; hasContent {
		t.Fatalf("compact create should omit content: %+v", createDraft)
	}
	if preview, _ := createDraft["contentPreview"].(string); preview == "" || len([]rune(preview)) > 24 {
		t.Fatalf("expected bounded compact create preview, got %q", preview)
	}
	policy := createData["policy"].(map[string]any)
	if policy["autoPublishes"] != false || policy["publishToolAvailable"] != true || policy["publishTool"] != "article_draft_publish" {
		t.Fatalf("draft policy should deny auto-publish while advertising explicit publish tool, got %+v", policy)
	}

	update := callArticleTool(t, env, app, writeAuth, sessionID, 3, "article_draft_update", map[string]any{"id": "draft-1", "title": "Updated"})
	updateDraft := toolData(t, update)["draft"].(map[string]any)
	if updateDraft["title"] != "Updated" {
		t.Fatalf("unexpected update draft: %+v", updateDraft)
	}

	readAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	get := callArticleTool(t, env, app, readAuth, sessionID, 4, "article_draft_get", map[string]any{"id": "draft-1", "preview_chars": 32})
	getDraft := toolData(t, get)["draft"].(map[string]any)
	if _, hasContent := getDraft["content"]; hasContent {
		t.Fatalf("compact get should omit content: %+v", getDraft)
	}
	if preview, _ := getDraft["contentPreview"].(string); preview == "" || len([]rune(preview)) > 32 {
		t.Fatalf("expected bounded compact get preview, got %q", preview)
	}
	expand := getDraft["expand"].(map[string]any)
	if expand["tool"] != "article_draft_get" {
		t.Fatalf("expected article_draft_get expansion, got %+v", expand)
	}

	list := callArticleTool(t, env, app, readAuth, sessionID, 5, "article_draft_list", map[string]any{"limit": 1})
	listData := toolData(t, list)
	if listData["nextCursor"] != "draft-1" || listData["count"] != float64(1) {
		t.Fatalf("unexpected list data: %+v", listData)
	}
	drafts := listData["drafts"].([]any)
	first := drafts[0].(map[string]any)
	if _, hasContent := first["content"]; hasContent {
		t.Fatalf("compact list should omit content: %+v", first)
	}
	if len(operations) != 4 {
		t.Fatalf("expected 4 GraphQL operations, got %d", len(operations))
	}
}

func TestArticleDraftPreviewToolUsesLesserRendererContractAndControls(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	renderedHTML := "<article><h1>Preview</h1><p>" + strings.Repeat("rendered ", 80) + "</p></article>"
	var operations []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer auth: %q", got)
		}
		var op map[string]any
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		if op["operationName"] == "BodyArticleDraft" {
			// CSR-010: preview now verifies draft ownership by fetching the
			// draft first. Return a compact draft (no content).
			vars := op["variables"].(map[string]any)
			id := vars["id"].(string)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draft": map[string]any{
				"id":              id,
				"authorId":        "agent1",
				"contentType":     "ARTICLE",
				"title":           "Test",
				"contentFormat":   "MARKDOWN",
				"status":          "DRAFT",
				"autosaveVersion": 1,
			}}})
			return
		}
		if op["operationName"] != "BodyArticleDraftPreview" {
			t.Fatalf("unexpected operation %q", op["operationName"])
		}
		query, _ := op["query"].(string)
		if !strings.Contains(query, "draftPreview(id: $id)") || strings.Contains(query, "draft(id:") {
			t.Fatalf("preview tool must use Lesser draftPreview contract, got %s", query)
		}

		vars := op["variables"].(map[string]any)
		id := vars["id"].(string)
		w.Header().Set("Content-Type", "application/json")
		preview := map[string]any{
			"draftId":       id,
			"success":       true,
			"renderedHtml":  renderedHTML,
			"sourceFormat":  "MARKDOWN",
			"sourceBytes":   42,
			"renderedBytes": len(renderedHTML),
			"errors":        []string{},
		}
		if id == "bad-draft" {
			preview = map[string]any{
				"draftId":       id,
				"success":       false,
				"renderedHtml":  nil,
				"sourceFormat":  "MARKDOWN",
				"sourceBytes":   42,
				"renderedBytes": 0,
				"errors":        []string{"unsupported article content format: plaintext"},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draftPreview": preview}})
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
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {readAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	compact := callArticleTool(t, env, app, readAuth, sessionID, 2, "article_draft_preview", map[string]any{"id": "draft-1", "preview_chars": 32})
	compactData := toolData(t, compact)
	compactPreview := compactData["preview"].(map[string]any)
	if compactPreview["draftId"] != "draft-1" || compactPreview["success"] != true {
		t.Fatalf("unexpected compact preview: %+v", compactPreview)
	}
	if _, hasFull := compactPreview["renderedHtml"]; hasFull {
		t.Fatalf("compact preview must omit full renderedHtml: %+v", compactPreview)
	}
	if got, _ := compactPreview["renderedHtmlPreview"].(string); got == "" || len([]rune(got)) > 32 {
		t.Fatalf("expected bounded renderedHtmlPreview, got %q", got)
	}
	if compactPreview["renderedHtmlTruncated"] != true || compactPreview["sourceBytes"] != float64(42) {
		t.Fatalf("unexpected compact preview budget metadata: %+v", compactPreview)
	}
	policy := compactData["policy"].(map[string]any)
	if policy["rendersLocally"] != false || policy["rawDraftContentReturned"] != false || policy["renderAuthority"] != "lesser_article_renderer_sanitizer" {
		t.Fatalf("preview policy should keep Lesser as render authority, got %+v", policy)
	}
	omitted := compactData["omitted"].([]any)
	if len(omitted) == 0 {
		t.Fatalf("compact preview should include renderedHtml omission metadata")
	}

	standard := callArticleTool(t, env, app, readAuth, sessionID, 3, "article_draft_preview", map[string]any{"id": "draft-1", "view": "standard"})
	standardPreview := toolData(t, standard)["preview"].(map[string]any)
	if standardPreview["renderedHtml"] != renderedHTML {
		t.Fatalf("standard preview should include Lesser-rendered HTML, got %+v", standardPreview)
	}

	renderFailure := callArticleTool(t, env, app, readAuth, sessionID, 4, "article_draft_preview", map[string]any{"id": "bad-draft"})
	failedPreview := toolData(t, renderFailure)["preview"].(map[string]any)
	if failedPreview["success"] != false {
		t.Fatalf("render failure should be surfaced as success=false payload, got %+v", failedPreview)
	}
	if _, hasFull := failedPreview["renderedHtml"]; hasFull {
		t.Fatalf("render failure must not include renderedHtml: %+v", failedPreview)
	}
	errorsList := failedPreview["errors"].([]any)
	if len(errorsList) != 1 || !strings.Contains(errorsList[0].(string), "unsupported article content format") {
		t.Fatalf("render failure errors = %+v", errorsList)
	}

	tooLarge := callToolAllowError(t, env, app, readAuth, sessionID, 5, "article_draft_preview", map[string]any{"id": "draft-1", "max_output_bytes": 256})
	if !tooLarge.IsError {
		t.Fatalf("expected response_too_large tool error, got %+v", tooLarge)
	}
	toolErr := tooLarge.StructuredContent["error"].(map[string]any)
	if toolErr["code"] != "response_too_large" {
		t.Fatalf("expected response_too_large tool error, got %+v", toolErr)
	}

	// CSR-010: each preview call now also fetches the draft for ownership
	// verification, doubling the operation count (4 previews + 4 draft gets).
	if len(operations) != 8 {
		t.Fatalf("expected 8 draftPreview+draft operations (4 previews + 4 ownership checks), got %d", len(operations))
	}
}

func TestPublishedArticleToolsUseLesserGraphQLAndCompactDefaults(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var operations []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer auth: %q", got)
		}
		var op map[string]any
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		query, _ := op["query"].(string)
		if strings.Contains(query, "preview") || strings.Contains(query, "api/v1/statuses") {
			t.Fatalf("published Article tools must not expose preview/status authoring query: %s", query)
		}

		w.Header().Set("Content-Type", "application/json")
		switch op["operationName"] {
		case "BodyArticleDraft":
			// CSR-010: publish now verifies draft ownership by fetching the
			// draft first. Return a compact draft (no content).
			vars := op["variables"].(map[string]any)
			id := vars["id"].(string)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draft": map[string]any{
				"id":              id,
				"authorId":        "agent1",
				"contentType":     "ARTICLE",
				"title":           "Test",
				"contentFormat":   "MARKDOWN",
				"status":          "DRAFT",
				"autosaveVersion": 1,
			}}})
			return
		case "BodyPublishArticleDraft":
			vars := op["variables"].(map[string]any)
			if vars["id"] != "draft-1" {
				t.Fatalf("publish variables = %+v", vars)
			}
			if queryContainsArticleContentSelection(query) {
				t.Fatalf("compact publish should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"publishDraft":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","excerpt":"Short excerpt","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
		case "BodyUpdateArticle":
			vars := op["variables"].(map[string]any)
			if vars["id"] != "https://example.com/articles/hello" {
				t.Fatalf("update variables = %+v", vars)
			}
			input := vars["input"].(map[string]any)
			if input["title"] != "Updated" || input["contentFormat"] != "HTML" {
				t.Fatalf("update input = %+v", input)
			}
			if _, hasSlug := input["slug"]; hasSlug {
				t.Fatalf("article_update must not expose slug mutation, got %+v", input)
			}
			if queryContainsArticleContentSelection(query) {
				t.Fatalf("compact update should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"updateArticle":{"id":"https://example.com/articles/hello","slug":"hello","title":"Updated","excerpt":"Updated excerpt","contentFormat":"HTML","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:01:00Z"}}}`))
		case "BodyArticle":
			if !queryContainsArticleContentSelection(query) {
				t.Fatalf("article_get should request content so compact can produce a bounded preview: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"article":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","excerpt":"Short excerpt","content":"` + strings.Repeat("published body ", 80) + `","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":160,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
		case "BodyArticles":
			vars := op["variables"].(map[string]any)
			if vars["authorId"] != "agent1" {
				t.Fatalf("article_list should default to authenticated actor authorId, got %+v", vars)
			}
			if queryContainsArticleContentSelection(query) {
				t.Fatalf("compact list should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[{"node":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","excerpt":"Short excerpt","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"},"cursor":"article-1"}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"startCursor":"article-1","endCursor":"article-1"},"totalCount":1}}}`))
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
	writeAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"write"})
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {writeAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	publish := callArticleTool(t, env, app, writeAuth, sessionID, 2, "article_draft_publish", map[string]any{"id": "draft-1"})
	publishData := toolData(t, publish)
	if publishData["canonicalArticleId"] != "https://example.com/articles/hello" || publishData["canonicalArticleUrl"] != "https://example.com/articles/hello" {
		t.Fatalf("publish should expose canonical Article id/url, got %+v", publishData)
	}
	publishedArticle := publishData["article"].(map[string]any)
	if _, hasContent := publishedArticle["content"]; hasContent {
		t.Fatalf("compact publish should omit content: %+v", publishedArticle)
	}

	// CSR-010: Verify ownership check (BodyArticleDraft) happens BEFORE the
	// publish mutation (BodyPublishArticleDraft). The test must fail if the
	// side-effecting publish happens before the ownership gating lookup.
	var draftIdx, publishIdx int = -1, -1
	for i, op := range operations {
		if op["operationName"] == "BodyArticleDraft" {
			draftIdx = i
		}
		if op["operationName"] == "BodyPublishArticleDraft" {
			publishIdx = i
		}
	}
	if draftIdx < 0 || publishIdx < 0 {
		t.Fatalf("expected both BodyArticleDraft and BodyPublishArticleDraft, got draftIdx=%d publishIdx=%d ops=%d", draftIdx, publishIdx, len(operations))
	}
	if draftIdx >= publishIdx {
		t.Fatalf("BodyArticleDraft (ownership check) must occur before BodyPublishArticleDraft (side effect), got draftIdx=%d publishIdx=%d", draftIdx, publishIdx)
	}

	update := callArticleTool(t, env, app, writeAuth, sessionID, 3, "article_update", map[string]any{
		"id":             "https://example.com/articles/hello",
		"title":          "Updated",
		"content":        strings.Repeat("updated body ", 20),
		"content_format": "HTML",
		"preview_chars":  24,
	})
	updateArticle := toolData(t, update)["article"].(map[string]any)
	if updateArticle["title"] != "Updated" {
		t.Fatalf("unexpected update article: %+v", updateArticle)
	}
	if preview, _ := updateArticle["contentPreview"].(string); preview == "" || len([]rune(preview)) > 24 {
		t.Fatalf("expected bounded compact update preview, got %q", preview)
	}

	readAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	get := callArticleTool(t, env, app, readAuth, sessionID, 4, "article_get", map[string]any{"id": "https://example.com/articles/hello", "preview_chars": 32})
	getArticle := toolData(t, get)["article"].(map[string]any)
	if _, hasContent := getArticle["content"]; hasContent {
		t.Fatalf("compact get should omit content: %+v", getArticle)
	}
	if preview, _ := getArticle["contentPreview"].(string); preview == "" || len([]rune(preview)) > 32 {
		t.Fatalf("expected bounded compact get preview, got %q", preview)
	}
	expand := getArticle["expand"].(map[string]any)
	if expand["tool"] != "article_get" {
		t.Fatalf("expected article_get expansion, got %+v", expand)
	}

	list := callArticleTool(t, env, app, readAuth, sessionID, 5, "article_list", map[string]any{"limit": 1})
	listData := toolData(t, list)
	if listData["nextCursor"] != "article-1" || listData["count"] != float64(1) || listData["authorId"] != "agent1" {
		t.Fatalf("unexpected list data: %+v", listData)
	}
	articles := listData["articles"].([]any)
	first := articles[0].(map[string]any)
	if _, hasContent := first["content"]; hasContent {
		t.Fatalf("compact list should omit content: %+v", first)
	}
	// CSR-010: publish now also fetches the draft for ownership verification.
	if len(operations) != 5 {
		t.Fatalf("expected 5 GraphQL operations (1 draft + 4 article), got %d", len(operations))
	}
}

func TestArticleDraftPublishRejectsWrongAuthor(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	publishDraftCalled := false
	var ops []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var op map[string]any
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		ops = append(ops, op)

		w.Header().Set("Content-Type", "application/json")
		switch op["operationName"] {
		case "BodyArticleDraft":
			// Return a draft owned by a DIFFERENT actor — not the caller.
			vars := op["variables"].(map[string]any)
			id := vars["id"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draft": map[string]any{
				"id":              id,
				"authorId":        "other-agent",
				"contentType":     "ARTICLE",
				"title":           "Not Yours",
				"contentFormat":   "MARKDOWN",
				"status":          "DRAFT",
				"autosaveVersion": 1,
			}}})
		case "BodyPublishArticleDraft":
			// CSR-010: publishDraft must never be invoked when ownership
			// verification fails. Track this so the test can assert it.
			publishDraftCalled = true
			_, _ = w.Write([]byte(`{"data":{"publishDraft":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
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
	writeAuth := "Bearer " + newTestToken(t, "test", "agent1", []string{"write"})
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {writeAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	result := callToolAllowError(t, env, app, writeAuth, sessionID, 2, "article_draft_publish", map[string]any{"id": "draft-1"})
	if !result.IsError {
		t.Fatalf("expected tool error for wrong-author publish, got success: %+v", result.StructuredContent)
	}
	errData, _ := result.StructuredContent["error"].(map[string]any)
	if errData == nil {
		t.Fatalf("expected structured error in tool result: %+v", result.StructuredContent)
	}
	if errData["code"] != "not_found" {
		t.Fatalf("expected not_found error code, got %q: %+v", errData["code"], errData)
	}

	// CSR-010 regression: the publish mutation must never fire when ownership
	// verification fails. If publishDraftCalled is true, the ownership gate
	// did not stop the side effect.
	if publishDraftCalled {
		t.Fatalf("BodyPublishArticleDraft must not be invoked when draft ownership check fails")
	}

	// Only the ownership-checking draft lookup should have been made.
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 operation (BodyArticleDraft ownership check), got %d: %+v", len(ops), ops)
	}
	if ops[0]["operationName"] != "BodyArticleDraft" {
		t.Fatalf("expected BodyArticleDraft as only operation, got %q", ops[0]["operationName"])
	}
}

func TestArticleDraftWriteToolsRequireWriteScope(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
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
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {readAuth}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	for id, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "article_draft_create", args: map[string]any{"content": "hello"}},
		{name: "article_draft_update", args: map[string]any{"id": "draft-1", "title": "Hello"}},
		{name: "article_draft_publish", args: map[string]any{"id": "draft-1"}},
		{name: "article_update", args: map[string]any{"id": "https://example.com/articles/hello", "title": "Hello"}},
	} {
		callParams, _ := json.Marshal(map[string]any{"name": tc.name, "arguments": tc.args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {readAuth},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id + 2, Method: "tools/call", Params: callParams})
		if resp.Status != 403 {
			t.Fatalf("%s expected 403 with read token, got %d (%s)", tc.name, resp.Status, string(resp.Body))
		}
	}
	if requests != 0 {
		t.Fatalf("forbidden Article writes should not reach Lesser, got %d requests", requests)
	}
}

func assertToolHint(t testing.TB, tool mcpruntime.ToolDef, readOnly *bool, destructive *bool, idempotent *bool) {
	t.Helper()
	if tool.Annotations == nil {
		t.Fatalf("expected annotations for %s", tool.Name)
	}
	if readOnly != nil && (tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != *readOnly) {
		t.Fatalf("%s readOnlyHint = %+v want %v", tool.Name, tool.Annotations.ReadOnlyHint, *readOnly)
	}
	if destructive != nil && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != *destructive) {
		t.Fatalf("%s destructiveHint = %+v want %v", tool.Name, tool.Annotations.DestructiveHint, *destructive)
	}
	if idempotent != nil && (tool.Annotations.IdempotentHint == nil || *tool.Annotations.IdempotentHint != *idempotent) {
		t.Fatalf("%s idempotentHint = %+v want %v", tool.Name, tool.Annotations.IdempotentHint, *idempotent)
	}
}

type articleToolCallResult struct {
	ResponseBody []byte
	RPC          mcpruntime.Response
	Result       mcpruntime.ToolResult
}

func callArticleTool(t testing.TB, env *testkit.Env, app *apptheory.App, authHeader string, sessionID string, id int, name string, args map[string]any) articleToolCallResult {
	t.Helper()
	callParams, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s rpc error: %+v", name, rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s tool result: %v", name, err)
	}
	if out.IsError {
		t.Fatalf("%s returned tool error: %+v", name, out.StructuredContent)
	}
	return articleToolCallResult{ResponseBody: resp.Body, RPC: rpc, Result: out}
}

func toolData(t testing.TB, result articleToolCallResult) map[string]any {
	t.Helper()
	data, _ := result.Result.StructuredContent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("missing structuredContent.data: %+v", result.Result.StructuredContent)
	}
	return data
}

func queryContainsDraftContentSelection(query string) bool {
	return strings.Contains(query, " content ") || strings.Contains(query, " content}") || strings.Contains(query, " content\n")
}

func queryContainsArticleContentSelection(query string) bool {
	return queryContainsDraftContentSelection(query)
}
