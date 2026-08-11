package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/cmsapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func articleDraftPublishDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_publish",
		Description:  "Publish an owner-scoped Article draft belonging to the authenticated actor through Lesser CMS. Returns the canonical published Article ID and URL; cross-actor draft ids return not found.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Lesser CMS draft id to publish."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns published Article content."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget when content is available. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["id"]
		}`),
	}
}

func articleUpdateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_update",
		Description:  "Update a published Article through Lesser CMS by canonical Article id. Canonical slug/URL changes are not exposed.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Canonical Lesser Article id/URL."},
				"title":{"type":"string","description":"Optional replacement Article title."},
				"content":{"type":"string","description":"Optional replacement Article body content."},
				"content_format":{"type":"string","enum":["MARKDOWN","HTML"],"description":"Optional replacement Article body format."},
				"subtitle":{"type":"string","description":"Optional replacement subtitle."},
				"excerpt":{"type":"string","description":"Optional replacement excerpt/summary."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns published Article content after update."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["id"]
		}`),
	}
}

func articleGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_get",
		Description:  "Read one published Article by canonical Article id/URL or slug. Defaults to a compact ref with bounded preview and expansion metadata.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Canonical Lesser Article id/URL. Required unless slug is provided."},
				"slug":{"type":"string","description":"Canonical Article slug. Used only when id is omitted."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns Article content."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			}
		}`),
	}
}

func articleListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_list",
		Description:  "List the authenticated actor's published Article refs through Lesser CMS. Defaults compact.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleListOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Maximum Article refs to return. Defaults to 20."},
				"cursor":{"type":"string","description":"Optional pagination cursor from a previous article_list response."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact refs. standard returns Article content for each listed Article."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget when content is available. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			}
		}`),
	}
}

func handleArticleDraftPublish(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, invalidParams("id is required")
	}

	token, err := requireOwnerScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	// CSR-010: Verify the draft being published belongs to the
	// authenticated actor BEFORE executing the publish mutation.
	// The ownership check must gate the side effect, not trail it.
	draft, err := client.GetArticleDraft(ctx, token, id, false)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_publish", err)
	}
	if !draftOwnedByAuthenticatedActor(ctx, draft) {
		return articleDraftOwnershipDenied("article_draft_publish", draft.ID)
	}

	article, err := client.PublishArticleDraft(ctx, token, id, params.View == readViewStandard)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_publish", err)
	}

	return articleSingleResult("article_draft_publish", "published", article, params, nil)
}

func handleArticleUpdate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		ID            string  `json:"id"`
		Title         *string `json:"title,omitempty"`
		Content       *string `json:"content,omitempty"`
		ContentFormat *string `json:"content_format,omitempty"`
		Subtitle      *string `json:"subtitle,omitempty"`
		Excerpt       *string `json:"excerpt,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, invalidParams("id is required")
	}
	if in.Title == nil && in.Content == nil && in.ContentFormat == nil && in.Subtitle == nil && in.Excerpt == nil {
		return nil, invalidParams("at least one Article field is required")
	}
	var format *string
	if in.ContentFormat != nil {
		normalized, err := normalizeArticleDraftContentFormat(*in.ContentFormat)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		format = &normalized
	}

	token, err := requireOwnerScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	article, err := client.UpdateArticle(ctx, token, id, cmsapi.UpdateArticleInput{
		Title:         trimOptionalString(in.Title),
		Content:       in.Content,
		ContentFormat: format,
		Subtitle:      trimOptionalString(in.Subtitle),
		Excerpt:       trimOptionalString(in.Excerpt),
	}, params.View == readViewStandard)
	if err != nil {
		return articleDraftToolResultFromError("article_update", err)
	}

	return articleSingleResult("article_update", "updated", article, params, in.Content)
}

func handleArticleGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		ID   string `json:"id,omitempty"`
		Slug string `json:"slug,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	id := strings.TrimSpace(in.ID)
	slug := strings.TrimSpace(in.Slug)
	if id == "" && slug == "" {
		return nil, invalidParams("id or slug is required")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	var article *cmsapi.Article
	if id != "" {
		article, err = client.GetArticle(ctx, token, id, true)
	} else {
		article, err = client.GetArticleBySlug(ctx, token, slug, true)
	}
	if err != nil {
		return articleDraftToolResultFromError("article_get", err)
	}

	return articleSingleResult("article_get", "read", article, params, nil)
}

func handleArticleList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	limit := in.Limit
	if limit == 0 {
		limit = articleDraftDefaultLimit
	}
	if limit < 1 || limit > articleDraftMaxLimit {
		return nil, invalidParams(fmt.Sprintf("limit must be between 1 and %d", articleDraftMaxLimit))
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	authorID := authenticatedArticleAuthorID(ctx)
	if authorID == "" {
		return nil, fmt.Errorf("authenticated actor identity is required")
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	includeContent := params.View == readViewStandard
	conn, err := client.ListArticles(ctx, token, limit, in.Cursor, authorID, includeContent)
	if err != nil {
		return articleDraftToolResultFromError("article_list", err)
	}
	return articleListResult(conn, limit, authorID, params)
}

// authenticatedArticleAuthorID resolves the author scope for article reads.
// Share-grant callers (admitted per request when caller != actor) resolve the
// actor's author scope: published-article reads then list the agent's
// articles, and body-side draft ownership checks compare against the agent.
// Owner requests and contexts without an actor route value keep the exact
// pre-existing caller-identity behavior.
func authenticatedArticleAuthorID(ctx context.Context) string {
	if actor, _, shared := shareGrantActorCaller(ctx); shared {
		return actor
	}
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil {
		return ""
	}
	return strings.TrimSpace(principal.Identity)
}

func articleSingleResult(toolName string, operation string, article *cmsapi.Article, params articleDraftViewParams, fallbackContent *string) (*mcpruntime.ToolResult, error) {
	if article == nil {
		return nil, fmt.Errorf("%s returned no article", toolName)
	}
	shaped := shapeArticle(article, params, fallbackContent)
	payload := map[string]any{
		"tool":                toolName,
		"operation":           operation,
		"source":              "lesser_cms_graphql",
		"view":                params.View,
		"article":             shaped,
		"articleRef":          compactArticleRef(article, params, fallbackContent),
		"canonicalArticleId":  canonicalArticleID(article),
		"canonicalArticleUrl": canonicalArticleURL(article),
		"omitted":             articleOmissions(params.View, false),
		"budget":              articleDraftBudget(params),
		"policy":              articlePolicyMetadata(),
	}
	text := map[string]any{
		"tool":                toolName,
		"operation":           operation,
		"articleId":           article.ID,
		"canonicalArticleUrl": canonicalArticleURL(article),
		"view":                params.View,
	}
	return articleDraftStructuredResult(toolName, params.View, fmt.Sprintf("Article %s: %s", operation, canonicalArticleURL(article)), payload, text, params.MaxOutputBytes)
}

func articleListResult(conn *cmsapi.ArticleConnection, limit int, authorID string, params articleDraftViewParams) (*mcpruntime.ToolResult, error) {
	if conn == nil {
		conn = &cmsapi.ArticleConnection{}
	}
	articles := make([]map[string]any, 0, len(conn.Edges))
	for _, edge := range conn.Edges {
		cursor := strings.TrimSpace(edge.Cursor)
		if edge.Node == nil {
			if cursor == "" {
				continue
			}
			articles = append(articles, compactArticleCursorRef(cursor))
			continue
		}
		item := shapeArticle(edge.Node, params, nil)
		if cursor != "" {
			item["cursor"] = cursor
		}
		articles = append(articles, item)
	}
	nextCursor := ""
	if conn.PageInfo.EndCursor != nil && strings.TrimSpace(*conn.PageInfo.EndCursor) != "" && conn.PageInfo.HasNextPage {
		nextCursor = strings.TrimSpace(*conn.PageInfo.EndCursor)
	}
	payload := map[string]any{
		"tool":       "article_list",
		"operation":  "list",
		"source":     "lesser_cms_graphql",
		"view":       params.View,
		"authorId":   authorID,
		"articles":   articles,
		"count":      len(articles),
		"limit":      limit,
		"nextCursor": nextCursor,
		"pageInfo":   conn.PageInfo,
		"totalCount": conn.TotalCount,
		"omitted":    articleListOmissions(),
		"budget":     articleDraftBudget(params),
		"policy":     articleListPolicyMetadata(),
	}
	text := map[string]any{
		"tool":     "article_list",
		"authorId": authorID,
		"count":    len(articles),
		"view":     params.View,
	}
	if nextCursor != "" {
		text["nextCursor"] = nextCursor
	}
	return articleDraftStructuredResult("article_list", params.View, fmt.Sprintf("%d Article refs", len(articles)), payload, text, params.MaxOutputBytes)
}

func compactArticleCursorRef(cursor string) map[string]any {
	cursor = strings.TrimSpace(cursor)
	return map[string]any{
		"cursor":       cursor,
		"depthSafeRef": true,
		"contractNote": "Lesser #1221 must expose a depth-safe Article list item before article IDs/slugs can be returned from article_list under the agent depth-3 profile.",
	}
}

func shapeArticle(article *cmsapi.Article, params articleDraftViewParams, fallbackContent *string) map[string]any {
	if params.View == readViewStandard {
		return standardArticle(article)
	}
	return compactArticleRef(article, params, fallbackContent)
}

func compactArticleRef(article *cmsapi.Article, params articleDraftViewParams, fallbackContent *string) map[string]any {
	out := map[string]any{
		"id":            strings.TrimSpace(article.ID),
		"url":           canonicalArticleURL(article),
		"canonicalUrl":  canonicalArticleURL(article),
		"contentFormat": strings.TrimSpace(article.ContentFormat),
		"expand": map[string]any{
			"tool":           "article_get",
			"arguments":      map[string]any{"id": strings.TrimSpace(article.ID), "view": readViewStandard},
			"resultPath":     "structuredContent.data.article",
			"textResultPath": "payload.article",
			"resultAccess":   toolResultAccessPath("payload.article", "data.article"),
		},
	}
	putIfNotEmpty(out, "title", article.Title)
	putIfNotEmpty(out, "slug", article.Slug)
	putIfNotEmpty(out, "subtitle", stringPtrValue(article.Subtitle))
	putIfNotEmpty(out, "excerpt", stringPtrValue(article.Excerpt))
	putIfNotEmpty(out, "publishedAt", article.PublishedAt)
	putIfNotEmpty(out, "createdAt", article.CreatedAt)
	putIfNotEmpty(out, "updatedAt", article.UpdatedAt)
	if article.ReadingTimeMinutes > 0 {
		out["readingTimeMinutes"] = article.ReadingTimeMinutes
	}
	if article.WordCount > 0 {
		out["wordCount"] = article.WordCount
	}
	content := article.Content
	if strings.TrimSpace(content) == "" && fallbackContent != nil {
		content = *fallbackContent
	}
	if strings.TrimSpace(content) != "" {
		preview, truncated := compactStringWithTruncation(content, params.PreviewRunes)
		putIfNotEmpty(out, "contentPreview", preview)
		out["contentTruncated"] = truncated
	}
	return out
}

func standardArticle(article *cmsapi.Article) map[string]any {
	out := map[string]any{
		"id":                 strings.TrimSpace(article.ID),
		"url":                canonicalArticleURL(article),
		"canonicalUrl":       canonicalArticleURL(article),
		"slug":               strings.TrimSpace(article.Slug),
		"title":              strings.TrimSpace(article.Title),
		"content":            article.Content,
		"contentFormat":      strings.TrimSpace(article.ContentFormat),
		"readingTimeMinutes": article.ReadingTimeMinutes,
		"wordCount":          article.WordCount,
	}
	putIfNotEmpty(out, "subtitle", stringPtrValue(article.Subtitle))
	putIfNotEmpty(out, "excerpt", stringPtrValue(article.Excerpt))
	putIfNotEmpty(out, "seoCanonicalUrl", stringPtrValue(article.CanonicalURL))
	putIfNotEmpty(out, "seoTitle", stringPtrValue(article.SEOTitle))
	putIfNotEmpty(out, "seoDescription", stringPtrValue(article.SEODescription))
	putIfNotEmpty(out, "ogImage", stringPtrValue(article.OGImage))
	// CSR-009: editorNotes and reviewStatus are internal CMS editorial
	// metadata fields. They are not exposed through MCP article tools.
	// The Article struct retains these fields for the CMS API layer
	// but the MCP output shaping strips them.
	putIfNotEmpty(out, "publishedAt", article.PublishedAt)
	putIfNotEmpty(out, "createdAt", article.CreatedAt)
	putIfNotEmpty(out, "updatedAt", article.UpdatedAt)
	return out
}

func articleOmissions(view string, list bool) []any {
	if view == readViewStandard {
		return []any{}
	}
	path := "article.content"
	if list {
		path = "articles[].content"
	}
	return []any{
		map[string]any{
			"path":      path,
			"reason":    "compact_default",
			"expansion": "call article_get with view=standard",
		},
	}
}

func articleListOmissions() []any {
	return []any{
		map[string]any{
			"path":      "articles[].id",
			"reason":    "graphql_depth_budget",
			"handoff":   "equaltoai/lesser#1221",
			"expansion": "pending Lesser depth-safe Article list item contract",
		},
		map[string]any{
			"path":      "articles[].content",
			"reason":    "graphql_depth_budget",
			"handoff":   "equaltoai/lesser#1221",
			"expansion": "pending Lesser depth-safe Article list item contract",
		},
	}
}

func articlePolicyMetadata() map[string]any {
	return map[string]any{
		"draftOnly":                false,
		"autoPublishes":            false,
		"canonicalArticleIdStable": true,
		"canonicalSlugMutable":     false,
		"previewToolAvailable":     false,
	}
}

func articleListPolicyMetadata() map[string]any {
	out := articlePolicyMetadata()
	out["graphqlDepthSafe"] = true
	out["listSelection"] = "edges.cursor"
	out["conditionalHandoff"] = "equaltoai/lesser#1221 must provide a depth-safe Article list-item contract for IDs/slugs/content refs."
	return out
}

func canonicalArticleID(article *cmsapi.Article) string {
	if article == nil {
		return ""
	}
	return strings.TrimSpace(article.ID)
}

func canonicalArticleURL(article *cmsapi.Article) string {
	id := canonicalArticleID(article)
	if id != "" {
		return id
	}
	if article == nil {
		return ""
	}
	return stringPtrValue(article.CanonicalURL)
}
