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

	for _, name := range []string{"article_draft_create", "article_draft_update", "article_draft_get", "article_draft_list"} {
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
			_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"}}}`))
		case "BodyArticleDraft":
			if !queryContainsDraftContentSelection(query) {
				t.Fatalf("draft get should request content so compact can produce a bounded preview: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","slug":"hello","content":"` + strings.Repeat("body ", 80) + `","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"}}}`))
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
			_, _ = w.Write([]byte(`{"data":{"updateDraft":{"id":"draft-1","contentType":"ARTICLE","title":"Updated","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":2,"lastSavedAt":"2026-05-19T21:01:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:01:00Z"}}}`))
		case "BodyArticleDrafts":
			if queryContainsDraftContentSelection(query) {
				t.Fatalf("compact list should not request GraphQL content field: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"node":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","autosaveVersion":1,"lastSavedAt":"2026-05-19T21:00:00Z","createdAt":"2026-05-19T21:00:00Z","updatedAt":"2026-05-19T21:00:00Z"},"cursor":"draft-1"}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"startCursor":"draft-1","endCursor":"draft-1"},"totalCount":1}}}`))
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
	if policy["autoPublishes"] != false || policy["publishToolAvailable"] != false {
		t.Fatalf("draft policy should deny publish claims, got %+v", policy)
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
		t.Fatalf("forbidden Article draft writes should not reach Lesser, got %d requests", requests)
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
