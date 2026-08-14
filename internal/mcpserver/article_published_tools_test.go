package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const articleListCompactEnvelopeCeilingBytes = 48 * 1024
const articleListCompactLongMetadataEnvelopeCeilingBytes = 60 * 1024
const articleListStandardEnvelopeCeilingBytes = 448 * 1024

type articleFixtureOptions struct {
	includeContent bool
	contentRunes   int
	titleRunes     int
	subtitleRunes  int
	excerptRunes   int
}

func TestArticleGetReturnsCleanNotFoundForMissingSlugIDAndURL(t *testing.T) {
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
		case "BodyArticle":
			_, _ = w.Write([]byte(`{"data":{"article":null}}`))
		case "BodyArticleBySlug":
			_, _ = w.Write([]byte(`{"data":{"articleBySlug":null}}`))
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	for _, tc := range []struct {
		name       string
		args       string
		wantOp     string
		wantLookup string
	}{
		{name: "missing by id", args: `{"id":"article-123"}`, wantOp: "BodyArticle", wantLookup: "id"},
		{name: "missing by url", args: `{"id":"https://example.com/articles/missing"}`, wantOp: "BodyArticle", wantLookup: "url"},
		{name: "missing by slug", args: `{"slug":"missing-slug"}`, wantOp: "BodyArticleBySlug", wantLookup: "slug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(operations)
			result, err := handleArticleGet(articleDraftTestContext(), json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("handleArticleGet: %v", err)
			}
			payload := toolErrorPayloadForTest(t, result)
			if payload["code"] != "not_found" || intFromAny(payload["status"]) != http.StatusNotFound {
				t.Fatalf("error payload = %#v, want not_found/404", payload)
			}
			details, _ := payload["details"].(map[string]any)
			if details["lookup"] != tc.wantLookup || details["tool"] != "article_get" {
				t.Fatalf("error details = %#v, want lookup=%q tool=article_get", details, tc.wantLookup)
			}
			if len(operations) != before+1 || operations[before].OperationName != tc.wantOp {
				t.Fatalf("operations = %+v, want trailing op %q", operations, tc.wantOp)
			}
		})
	}
}

func TestArticleGetReturnsSuccessForExistingSlugIDAndURL(t *testing.T) {
	const articleURL = "https://example.com/articles/hello"
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
		case "BodyArticle":
			_, _ = fmt.Fprintf(w, `{"data":{"article":{"id":%q,"slug":"hello","title":"Hello","content":"body","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`, articleURL)
		case "BodyArticleBySlug":
			_, _ = fmt.Fprintf(w, `{"data":{"articleBySlug":{"id":%q,"slug":"hello","title":"Hello","content":"body","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`, articleURL)
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	for _, tc := range []struct {
		name   string
		args   string
		wantOp string
	}{
		{name: "existing by id", args: `{"id":"article-123","view":"standard"}`, wantOp: "BodyArticle"},
		{name: "existing by url", args: `{"id":"https://example.com/articles/hello","view":"standard"}`, wantOp: "BodyArticle"},
		{name: "existing by slug", args: `{"slug":"hello","view":"standard"}`, wantOp: "BodyArticleBySlug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(operations)
			result, err := handleArticleGet(articleDraftTestContext(), json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("handleArticleGet: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("result = %+v, want success", result)
			}
			if len(operations) != before+1 || operations[before].OperationName != tc.wantOp {
				t.Fatalf("operations = %+v, want trailing op %q", operations, tc.wantOp)
			}

			data := articleListStructuredData(t, result)
			if data["tool"] != "article_get" || data["operation"] != "read" || data["view"] != readViewStandard {
				t.Fatalf("structured data = %#v", data)
			}
			article, _ := data["article"].(map[string]any)
			if article["id"] != articleURL || article["slug"] != "hello" || article["content"] != "body" {
				t.Fatalf("article = %#v", article)
			}
		})
	}
}

func TestArticleToolErrorFallbackDoesNotLeakRawErrors(t *testing.T) {
	result, err := articleDraftToolResultFromError("article_get", errors.New("raw transport secret must not escape"))
	if err != nil {
		t.Fatalf("articleDraftToolResultFromError: %v", err)
	}
	payload := toolErrorPayloadForTest(t, result)
	if payload["code"] != "internal_error" || intFromAny(payload["status"]) != http.StatusInternalServerError {
		t.Fatalf("error payload = %#v, want internal_error/500", payload)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "raw transport secret") {
		t.Fatalf("raw handler text escaped in result: %s", encoded)
	}
}

func TestArticleListDefaultCompactPageFitsListBudgetAndShapesNodes(t *testing.T) {
	conn := realisticArticleConnection(articleDraftDefaultLimit, articleFixtureOptions{})
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
	if measurement.JSONRPCEnvelopeBytes > articleListCompactEnvelopeCeilingBytes {
		t.Fatalf("default compact article_list envelope = %d, want <= fixed ceiling %d", measurement.JSONRPCEnvelopeBytes, articleListCompactEnvelopeCeilingBytes)
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
	if _, hasPreview := first["contentPreview"]; hasPreview {
		t.Fatalf("production compact article_list must not include contentPreview without GraphQL content: %#v", first)
	}
	if _, hasContent := first["content"]; hasContent {
		t.Fatalf("compact article ref must not include full content: %#v", first)
	}
	if expand, _ := first["expand"].(map[string]any); expand["tool"] != "article_get" {
		t.Fatalf("expand = %#v", expand)
	}
}

func TestArticleListCompactDefaultPageTruncatesMetadataWithinBudget(t *testing.T) {
	result, err := articleListResult(realisticArticleConnection(articleDraftDefaultLimit, articleFixtureOptions{
		titleRunes:    articleCompactTitleRunes + 1,
		subtitleRunes: 200,
		excerptRunes:  500,
	}), articleDraftDefaultLimit, "alice", articleDraftViewParams{
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

	measurement, err := measureToolResultPayload(result)
	if err != nil {
		t.Fatalf("measureToolResultPayload: %v", err)
	}
	t.Logf("article_list compact long-metadata envelope=%d structured=%d text=%d budget=%d",
		measurement.JSONRPCEnvelopeBytes,
		measurement.StructuredContentBytes,
		measurement.ContentTextBytes,
		articleListDefaultBudgetBytes,
	)
	if measurement.JSONRPCEnvelopeBytes > articleListDefaultBudgetBytes {
		t.Fatalf("bounded compact article_list envelope = %d, want <= %d", measurement.JSONRPCEnvelopeBytes, articleListDefaultBudgetBytes)
	}
	if measurement.JSONRPCEnvelopeBytes > articleListCompactLongMetadataEnvelopeCeilingBytes {
		t.Fatalf("bounded compact article_list envelope = %d, want <= fixed ceiling %d", measurement.JSONRPCEnvelopeBytes, articleListCompactLongMetadataEnvelopeCeilingBytes)
	}

	data := articleListStructuredData(t, result)
	articles := articleRecords(t, data["articles"])
	first := articles[0]
	if got, _ := first["title"].(string); len([]rune(got)) > articleCompactTitleRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("title = %q, want truncated to <= %d runes", got, articleCompactTitleRunes)
	}
	if got, _ := first["subtitle"].(string); len([]rune(got)) > articleCompactSubtitleRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("subtitle = %q, want truncated to <= %d runes", got, articleCompactSubtitleRunes)
	}
	if got, _ := first["excerpt"].(string); len([]rune(got)) > articleCompactExcerptRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("excerpt = %q, want truncated to <= %d runes", got, articleCompactExcerptRunes)
	}
}

func TestArticleListMixedPageUsesUnavailableCursorRef(t *testing.T) {
	conn := realisticArticleConnection(1, articleFixtureOptions{})
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

func TestArticleListStandardDefaultPageFitsBudget(t *testing.T) {
	conn := realisticArticleConnection(articleListStandardDefaultLimit, articleFixtureOptions{
		includeContent: true,
		contentRunes:   20 * 1024,
	})
	var operation cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&operation); err != nil {
			t.Fatalf("decode operation: %v", err)
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

	result, err := handleArticleList(articleDraftTestContext(), json.RawMessage(`{"view":"standard"}`))
	if err != nil {
		t.Fatalf("article_list standard: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_list standard result = %+v", result)
	}
	if operation.OperationName != "BodyArticles" {
		t.Fatalf("operation = %+v", operation)
	}
	if operation.Variables["first"] != float64(articleListStandardDefaultLimit) {
		t.Fatalf("standard default limit variables = %+v", operation.Variables)
	}

	measurement, err := measureToolResultPayload(result)
	if err != nil {
		t.Fatalf("measureToolResultPayload: %v", err)
	}
	t.Logf("article_list standard default envelope=%d structured=%d text=%d budget=%d",
		measurement.JSONRPCEnvelopeBytes,
		measurement.StructuredContentBytes,
		measurement.ContentTextBytes,
		articleListStandardDefaultBudgetBytes,
	)
	if measurement.JSONRPCEnvelopeBytes > articleListStandardDefaultBudgetBytes {
		t.Fatalf("default standard article_list envelope = %d, want <= %d", measurement.JSONRPCEnvelopeBytes, articleListStandardDefaultBudgetBytes)
	}
	if measurement.JSONRPCEnvelopeBytes > articleListStandardEnvelopeCeilingBytes {
		t.Fatalf("default standard article_list envelope = %d, want <= fixed ceiling %d", measurement.JSONRPCEnvelopeBytes, articleListStandardEnvelopeCeilingBytes)
	}

	data := articleListStructuredData(t, result)
	if data["view"] != readViewStandard {
		t.Fatalf("view = %#v, data = %#v", data["view"], data)
	}
	if intFromAny(data["limit"]) != articleListStandardDefaultLimit || intFromAny(data["count"]) != articleListStandardDefaultLimit {
		t.Fatalf("limit/count = %#v/%#v", data["limit"], data["count"])
	}
	budget, _ := data["budget"].(map[string]any)
	if intFromAny(budget["maxOutputBytes"]) != articleListStandardDefaultBudgetBytes {
		t.Fatalf("budget = %#v", budget)
	}
	articles := articleRecords(t, data["articles"])
	if len(articles) != articleListStandardDefaultLimit || strings.TrimSpace(fmt.Sprint(articles[0]["content"])) == "" {
		t.Fatalf("standard articles = %#v", articles)
	}
}

func TestArticleListStandardRejectsUnsafeLimitBeforeDispatch(t *testing.T) {
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handleArticleList(articleDraftTestContext(), json.RawMessage(`{"view":"standard","limit":80}`))
	if result != nil {
		t.Fatalf("result = %+v, want nil on invalid params", result)
	}
	var invalid *InvalidParamsError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "view=standard") || !strings.Contains(err.Error(), "10") || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("err = %v, want standard limit invalid params", err)
	}
	if serverCalled {
		t.Fatal("standard unsafe limit should fail before Lesser dispatch")
	}
}

func TestArticleListStandardViewDoesNotDeclareCompactOmissions(t *testing.T) {
	result, err := articleListResult(realisticArticleConnection(1, articleFixtureOptions{includeContent: true}), 1, "alice", articleDraftViewParams{
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

func TestArticleListInputSchemaRequiresExplicitStandardViewForTightLimitCap(t *testing.T) {
	assertInputSchemaAccepts(t, articleListDef().InputSchema, `{"limit":20}`)
	assertInputSchemaAccepts(t, articleListDef().InputSchema, `{"view":"standard","limit":10}`)
	assertInputSchemaRejects(t, articleListDef().InputSchema, `{"view":"standard","limit":20}`)
}

func realisticArticleConnection(count int, opts articleFixtureOptions) *cmsapi.ArticleConnection {
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
		title := fmt.Sprintf("Article %s title", suffix)
		if opts.titleRunes > 0 {
			title = sizedString(title, opts.titleRunes)
		}
		subtitle := fmt.Sprintf("Subtitle %s %s", suffix, strings.Repeat("detail ", 6))
		if opts.subtitleRunes > 0 {
			subtitle = sizedString(subtitle, opts.subtitleRunes)
		}
		excerpt := fmt.Sprintf("Excerpt %s %s", suffix, strings.Repeat("summary ", 16))
		if opts.excerptRunes > 0 {
			excerpt = sizedString(excerpt, opts.excerptRunes)
		}
		content := ""
		if opts.includeContent {
			content = fmt.Sprintf("%s %s", slug+" body", strings.Repeat(slug+" body paragraph ", 24))
			if opts.contentRunes > 0 {
				content = sizedString(slug+" body ", opts.contentRunes)
			}
		}
		edges = append(edges, cmsapi.ArticleEdge{
			Cursor: fmt.Sprintf("article-cursor-%s", suffix),
			Node: &cmsapi.Article{
				ID:                 "https://example.com/articles/" + slug,
				Slug:               slug,
				Title:              title,
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

func sizedString(prefix string, runes int) string {
	if runes <= 0 {
		return strings.TrimSpace(prefix)
	}
	trimmed := strings.TrimSpace(prefix)
	current := len([]rune(trimmed))
	if current >= runes {
		return string([]rune(trimmed)[:runes])
	}
	return trimmed + strings.Repeat("x", runes-current)
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

func assertInputSchemaAccepts(t *testing.T, schema json.RawMessage, payload string) {
	t.Helper()
	if violations := validateInputSchemaDocument(t, schema, payload); len(violations) > 0 {
		t.Fatalf("payload %s rejected by input schema: %s", payload, strings.Join(violations, "; "))
	}
}

func assertInputSchemaRejects(t *testing.T, schema json.RawMessage, payload string) {
	t.Helper()
	if violations := validateInputSchemaDocument(t, schema, payload); len(violations) == 0 {
		t.Fatalf("payload %s unexpectedly accepted by input schema", payload)
	}
}

func validateInputSchemaDocument(t *testing.T, schema json.RawMessage, payload string) []string {
	t.Helper()
	var declared any
	if err := json.Unmarshal(schema, &declared); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		t.Fatalf("unmarshal payload %s: %v", payload, err)
	}
	var violations []string
	validateInputSchema("input", declared, value, &violations)
	return violations
}

func validateInputSchema(path string, schema any, value any, violations *[]string) {
	node, ok := schema.(map[string]any)
	if !ok {
		return
	}

	if branches, ok := node["allOf"].([]any); ok {
		for _, branch := range branches {
			validateInputSchema(path, branch, value, violations)
		}
	}

	if ifSchema, ok := node["if"]; ok {
		var conditionViolations []string
		validateInputSchema(path, ifSchema, value, &conditionViolations)
		if len(conditionViolations) == 0 {
			if thenSchema, ok := node["then"]; ok {
				validateInputSchema(path, thenSchema, value, violations)
			}
		} else if elseSchema, ok := node["else"]; ok {
			validateInputSchema(path, elseSchema, value, violations)
		}
	}

	if want, ok := node["const"]; ok && fmt.Sprint(value) != fmt.Sprint(want) {
		*violations = append(*violations, fmt.Sprintf("%s: value %#v does not match const %#v", path, value, want))
	}

	if want, ok := node["type"].(string); ok && !kaJSONTypeMatches(want, value) {
		*violations = append(*violations, fmt.Sprintf("%s: value has JSON type %s, want %s", path, kaJSONType(value), want))
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		properties, _ := node["properties"].(map[string]any)
		if required, ok := node["required"].([]any); ok {
			for _, raw := range required {
				key, _ := raw.(string)
				if key == "" {
					continue
				}
				if _, present := typed[key]; !present {
					*violations = append(*violations, fmt.Sprintf("%s.%s: missing required property", path, key))
				}
			}
		}
		for key, child := range typed {
			if childSchema, present := properties[key]; present {
				validateInputSchema(path+"."+key, childSchema, child, violations)
			}
		}
	case float64:
		if minimum, ok := schemaNumber(node["minimum"]); ok && typed < minimum {
			*violations = append(*violations, fmt.Sprintf("%s: value %v is below minimum %v", path, typed, minimum))
		}
		if maximum, ok := schemaNumber(node["maximum"]); ok && typed > maximum {
			*violations = append(*violations, fmt.Sprintf("%s: value %v exceeds maximum %v", path, typed, maximum))
		}
	}
}

func schemaNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}
