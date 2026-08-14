package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func TestArticleListDefaultCompactPageFitsListBudgetAndShapesNodes(t *testing.T) {
	conn := realisticArticleConnection(articleDraftDefaultLimit)
	var operation cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want exact caller bearer", got)
			http.Error(w, "unexpected bearer", http.StatusInternalServerError)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&operation); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeArticleDraftGraphQLData(t, w, map[string]any{"articles": conn})
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleList(articleDraftTestContext(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("article_list: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_list result = %+v", result)
	}
	if operation.OperationName != "BodyArticles" {
		t.Fatalf("operation = %+v", operation)
	}
	if operation.Variables["authorId"] != "alice" || operation.Variables["first"] != float64(articleDraftDefaultLimit) {
		t.Fatalf("delegated variables = %+v", operation.Variables)
	}

	measurement, err := measureToolResultPayload(result)
	if err != nil {
		t.Fatalf("measureToolResultPayload: %v", err)
	}
	t.Logf("article_list compact default envelope=%d structured=%d text=%d budget=%d",
		measurement.JSONRPCEnvelopeBytes,
		measurement.StructuredContentBytes,
		measurement.ContentTextBytes,
		articleListDefaultBudgetBytes,
	)
	if measurement.JSONRPCEnvelopeBytes > articleListDefaultBudgetBytes {
		t.Fatalf("default compact article_list envelope = %d, want <= %d", measurement.JSONRPCEnvelopeBytes, articleListDefaultBudgetBytes)
	}

	data := articleListStructuredData(t, result)
	if data["view"] != readViewCompact {
		t.Fatalf("view = %#v, data = %#v", data["view"], data)
	}
	if intFromAny(data["count"]) != articleDraftDefaultLimit || intFromAny(data["limit"]) != articleDraftDefaultLimit || intFromAny(data["totalCount"]) != articleDraftDefaultLimit+7 {
		t.Fatalf("count/limit/totalCount = %#v/%#v/%#v", data["count"], data["limit"], data["totalCount"])
	}
	budget, _ := data["budget"].(map[string]any)
	if intFromAny(budget["maxOutputBytes"]) != articleListDefaultBudgetBytes {
		t.Fatalf("budget = %#v", budget)
	}
	omitted := omissionRecords(t, data["omitted"])
	if len(omitted) != 1 || omitted[0]["path"] != "articles[].content" || omitted[0]["reason"] != "compact_default" {
		t.Fatalf("compact omissions = %#v", omitted)
	}
	if _, hasHandoff := omitted[0]["handoff"]; hasHandoff {
		t.Fatalf("compact omissions must not advertise stale lesser#1221 handoff: %#v", omitted[0])
	}
	policy, _ := data["policy"].(map[string]any)
	if policy["listSelection"] != "edges.cursor plus article node fields" || policy["fullContentExpansion"] != "call article_get with view=standard" {
		t.Fatalf("policy = %#v", policy)
	}
	if _, hasConditionalHandoff := policy["conditionalHandoff"]; hasConditionalHandoff {
		t.Fatalf("policy must not advertise stale lesser#1221 handoff: %#v", policy)
	}

	articles := articleRecords(t, data["articles"])
	if len(articles) != articleDraftDefaultLimit {
		t.Fatalf("articles = %d, want %d", len(articles), articleDraftDefaultLimit)
	}
	first := articles[0]
	if first["id"] != "https://example.com/articles/article-01" || first["slug"] != "article-01" || first["title"] != "Article 01 title" || first["cursor"] != "article-cursor-01" {
		t.Fatalf("first article ref = %#v", first)
	}
	if preview, _ := first["contentPreview"].(string); strings.TrimSpace(preview) == "" || !strings.Contains(preview, "article-01 body") {
		t.Fatalf("first article contentPreview = %#v", first["contentPreview"])
	}
	if _, hasContent := first["content"]; hasContent {
		t.Fatalf("compact article ref must not include full content: %#v", first)
	}
	if expand, _ := first["expand"].(map[string]any); expand["tool"] != "article_get" {
		t.Fatalf("expand = %#v", expand)
	}
}

func TestArticleListMixedPageUsesUnavailableCursorRef(t *testing.T) {
	conn := realisticArticleConnection(1)
	conn.Edges = append(conn.Edges,
		cmsapi.ArticleEdge{Cursor: "article-cursor-missing"},
		cmsapi.ArticleEdge{},
	)

	result, err := articleListResult(conn, 3, "alice", articleDraftViewParams{
		View:           readViewCompact,
		PreviewRunes:   articleDraftPreviewRunes,
		MaxOutputBytes: articleListDefaultBudgetBytes,
	})
	if err != nil {
		t.Fatalf("articleListResult: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("articleListResult = %+v", result)
	}

	data := articleListStructuredData(t, result)
	articles := articleRecords(t, data["articles"])
	if len(articles) != 2 {
		t.Fatalf("articles = %#v", articles)
	}
	unavailable := articles[1]
	if unavailable["cursor"] != "article-cursor-missing" || unavailable["unavailable"] != true || unavailable["reason"] != "deleted_or_unauthorized_mid_page" {
		t.Fatalf("unavailable edge ref = %#v", unavailable)
	}
	explanation, _ := unavailable["explanation"].(string)
	if !strings.Contains(explanation, "deleted") || !strings.Contains(explanation, "unauthorized") {
		t.Fatalf("unavailable explanation = %#v", unavailable["explanation"])
	}
	if _, hasDepthSafeRef := unavailable["depthSafeRef"]; hasDepthSafeRef {
		t.Fatalf("mixed-page unavailable ref must not claim stale depthSafeRef: %#v", unavailable)
	}
	if _, hasContractNote := unavailable["contractNote"]; hasContractNote {
		t.Fatalf("mixed-page unavailable ref must not ship stale contract note: %#v", unavailable)
	}
}

func TestArticleListStandardViewDoesNotDeclareCompactOmissions(t *testing.T) {
	result, err := articleListResult(realisticArticleConnection(1), 1, "alice", articleDraftViewParams{
		View: readViewStandard,
	})
	if err != nil {
		t.Fatalf("articleListResult: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("articleListResult = %+v", result)
	}

	data := articleListStructuredData(t, result)
	if omitted := omissionRecords(t, data["omitted"]); len(omitted) != 0 {
		t.Fatalf("standard omitted = %#v", omitted)
	}
	policy, _ := data["policy"].(map[string]any)
	if _, hasFullContentExpansion := policy["fullContentExpansion"]; hasFullContentExpansion {
		t.Fatalf("standard policy must not claim compact-only expansion: %#v", policy)
	}
	articles := articleRecords(t, data["articles"])
	if len(articles) != 1 || articles[0]["content"] == nil {
		t.Fatalf("standard articles = %#v", articles)
	}
}

func realisticArticleConnection(count int) *cmsapi.ArticleConnection {
	if count < 1 {
		count = 1
	}
	edges := make([]cmsapi.ArticleEdge, 0, count)
	startCursor := "article-cursor-01"
	endCursor := fmt.Sprintf("article-cursor-%02d", count)
	actedBy := &cmsapi.Actor{
		ID:       "https://example.com/users/editor",
		Username: "editor",
	}
	for i := 1; i <= count; i++ {
		suffix := fmt.Sprintf("%02d", i)
		slug := "article-" + suffix
		subtitle := fmt.Sprintf("Subtitle %s %s", suffix, strings.Repeat("detail ", 6))
		excerpt := fmt.Sprintf("Excerpt %s %s", suffix, strings.Repeat("summary ", 16))
		content := fmt.Sprintf("%s %s", slug+" body", strings.Repeat(slug+" body paragraph ", 24))
		edges = append(edges, cmsapi.ArticleEdge{
			Cursor: fmt.Sprintf("article-cursor-%s", suffix),
			Node: &cmsapi.Article{
				ID:                 "https://example.com/articles/" + slug,
				Slug:               slug,
				Title:              fmt.Sprintf("Article %s title", suffix),
				Subtitle:           &subtitle,
				Excerpt:            &excerpt,
				Content:            content,
				ContentFormat:      cmsapi.ContentFormatMarkdown,
				ReadingTimeMinutes: 4 + i%3,
				WordCount:          900 + i*37,
				ActedBy:            actedBy,
				PublishedAt:        fmt.Sprintf("2026-08-%02dT12:00:00Z", i),
				CreatedAt:          fmt.Sprintf("2026-08-%02dT11:45:00Z", i),
				UpdatedAt:          fmt.Sprintf("2026-08-%02dT12:30:00Z", i),
			},
		})
	}
	return &cmsapi.ArticleConnection{
		Edges: edges,
		PageInfo: cmsapi.PageInfo{
			HasNextPage:     true,
			HasPreviousPage: false,
			StartCursor:     &startCursor,
			EndCursor:       &endCursor,
		},
		TotalCount: count + 7,
	}
}

func articleListStructuredData(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("tool result is nil")
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("structuredContent.data missing: %#v", result.StructuredContent)
	}
	return data
}

func articleRecords(t *testing.T, raw any) []map[string]any {
	t.Helper()
	switch items := raw.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			record, _ := item.(map[string]any)
			if record == nil {
				t.Fatalf("article record = %#v", item)
			}
			out = append(out, record)
		}
		return out
	default:
		t.Fatalf("articles payload = %#v", raw)
		return nil
	}
}

func omissionRecords(t *testing.T, raw any) []map[string]any {
	t.Helper()
	items, _ := raw.([]any)
	if items == nil {
		t.Fatalf("omitted payload = %#v", raw)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record, _ := item.(map[string]any)
		if record == nil {
			t.Fatalf("omission record = %#v", item)
		}
		out = append(out, record)
	}
	return out
}
