package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const (
	articleDraftDefaultLimit              = 20
	articleDraftMaxLimit                  = 80
	articleDraftPreviewRunes              = 240
	articleDraftDefaultBudgetBytes        = 12000
	articleListDefaultBudgetBytes         = 64 * 1024
	articleListStandardDefaultLimit       = 10
	articleListStandardMaxLimit           = 10
	articleListStandardDefaultBudgetBytes = 512 * 1024
	articleCompactTitleRunes              = 120
	articleCompactSubtitleRunes           = 160
	articleCompactExcerptRunes            = 256
)

func registerArticleTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: articleDraftCreateDef(), Handler: handleArticleDraftCreate},
		{Def: articleDraftUpdateDef(), Handler: handleArticleDraftUpdate},
		{Def: articleDraftGetDef(), Handler: handleArticleDraftGet},
		{Def: articleDraftListDef(), Handler: handleArticleDraftList},
		{Def: articleDraftPreviewDef(), Handler: handleArticleDraftPreview},
		{Def: articleDraftPublishDef(), Handler: handleArticleDraftPublish},
		{Def: articleUpdateDef(), Handler: handleArticleUpdate},
		{Def: articleGetDef(), Handler: handleArticleGet},
		{Def: articleListDef(), Handler: handleArticleList},
	} {
		if err := registerTool(r, tool.Def, tool.Handler); err != nil {
			return err
		}
	}
	return registerArticleReviewTools(r)
}

func articleDraftCreateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_create",
		Description:  "Create an owner-scoped, draft-only Article for the authenticated actor through Lesser CMS. Defaults to a compact draft ref; nothing auto-publishes and no cross-actor read grant is created.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleDraftSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"title":{"type":"string","description":"Optional draft title."},
				"slug":{"type":"string","description":"Optional draft slug hint. Lesser normalizes the final draft slug."},
				"content":{"type":"string","description":"Draft body content. This creates an unpublished ARTICLE draft only."},
				"content_format":{"type":"string","enum":["MARKDOWN","HTML"],"description":"Draft body format. Defaults to MARKDOWN."},
				"object_id":{"type":"string","description":"Optional existing object target for draft updates; not a promised canonical Article id before publish."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns the draft content after creation."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["content"]
		}`),
	}
}

func articleDraftUpdateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_update",
		Description:  "Update an owner-scoped Article draft belonging to the authenticated actor through Lesser CMS. Does not grant reviewer access, preview, or publish.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleDraftSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Lesser CMS draft id."},
				"title":{"type":"string","description":"Optional replacement draft title."},
				"slug":{"type":"string","description":"Optional replacement draft slug hint. Lesser normalizes the final draft slug."},
				"content":{"type":"string","description":"Optional replacement draft body content."},
				"content_format":{"type":"string","enum":["MARKDOWN","HTML"],"description":"Optional replacement draft body format."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns the draft content after update."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["id"]
		}`),
	}
}

func articleDraftGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_get",
		Description:  "Read one Article draft by draft id when Lesser authorizes the caller as its owner or through an active reviewer grant. Revoked or unauthorized cross-actor ids return not found; defaults to a compact ref with bounded preview and expansion metadata.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleDraftSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Lesser CMS draft id."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns draft content."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["id"]
		}`),
	}
}

func articleDraftPreviewDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_preview",
		Description:  "Render one Article draft through Lesser's canonical publication renderer/sanitizer when Lesser authorizes the caller as its owner or through an active reviewer grant. Revoked or unauthorized cross-actor ids return not found; defaults compact and never returns raw draft content.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleDraftPreviewOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Lesser CMS draft id."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact. standard returns Lesser-rendered sanitized HTML when rendering succeeds."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact rendered-HTML preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			},
			"required":["id"]
		}`),
	}
}

func articleDraftListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_list",
		Description:  "List only the authenticated actor's owner-scoped unpublished Article draft refs. Defaults compact, filters to DRAFT status, and does not list drafts shared by identifier from another actor.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleDraftListOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Maximum draft refs to return. Defaults to 20."},
				"cursor":{"type":"string","description":"Optional pagination cursor from a previous article_draft_list response."},
				"view":{"type":"string","enum":["compact","standard"],"description":"Defaults to compact refs. standard returns draft content for each listed draft."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget when content is available. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to a bounded budget and return response_too_large if exceeded."}
			}
		}`),
	}
}

func handleArticleDraftCreate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		Title         *string `json:"title,omitempty"`
		Slug          *string `json:"slug,omitempty"`
		Content       *string `json:"content,omitempty"`
		ContentFormat string  `json:"content_format,omitempty"`
		ObjectID      *string `json:"object_id,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if in.Content == nil {
		return nil, invalidParams("content is required")
	}
	format, err := normalizeArticleDraftContentFormat(in.ContentFormat)
	if err != nil {
		return nil, invalidParams(err.Error())
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	draft, err := client.CreateArticleDraft(ctx, token, cmsapi.CreateDraftInput{
		Title:         trimOptionalString(in.Title),
		Slug:          trimOptionalString(in.Slug),
		Content:       *in.Content,
		ContentFormat: format,
		ObjectID:      trimOptionalString(in.ObjectID),
	}, params.View == readViewStandard)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_create", err)
	}

	fallback := in.Content
	return articleDraftSingleResult("article_draft_create", "created", draft, params, fallback)
}

func handleArticleDraftUpdate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	params, err := parseArticleDraftViewParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	var in struct {
		ID            string  `json:"id"`
		Title         *string `json:"title,omitempty"`
		Slug          *string `json:"slug,omitempty"`
		Content       *string `json:"content,omitempty"`
		ContentFormat *string `json:"content_format,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, invalidParams("id is required")
	}
	if in.Title == nil && in.Slug == nil && in.Content == nil && in.ContentFormat == nil {
		return nil, invalidParams("at least one draft field is required")
	}
	var format *string
	if in.ContentFormat != nil {
		normalized, err := normalizeArticleDraftContentFormat(*in.ContentFormat)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		format = &normalized
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := client.GetArticleDraft(ctx, token, id, false)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_update", err)
	}
	if !draftOwnedByAuthenticatedActor(ctx, existing) || !draftIsArticleDraft(existing) {
		return articleDraftNotFoundDenied("article_draft_update", existing.ID)
	}
	draft, err := client.UpdateArticleDraft(ctx, token, id, cmsapi.UpdateDraftInput{
		Title:         trimOptionalString(in.Title),
		Slug:          trimOptionalString(in.Slug),
		Content:       in.Content,
		ContentFormat: format,
	}, params.View == readViewStandard)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_update", err)
	}
	if !draftOwnedByAuthenticatedActor(ctx, draft) || !draftIsArticleDraft(draft) {
		return articleDraftNotFoundDenied("article_draft_update", draft.ID)
	}

	return articleDraftSingleResult("article_draft_update", "updated", draft, params, in.Content)
}

func handleArticleDraftGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
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

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	draft, err := client.GetArticleDraft(ctx, token, id, true)
	reviewerProjection := false
	if err != nil {
		if !articleDraftOwnerLookupNotFound(err) {
			return articleDraftToolResultFromError("article_draft_get", err)
		}
		// Lesser's draft(id:) projection is deliberately owner-only. Its
		// draftReview(id:) projection is the authoritative owner-or-active-
		// reviewer read path and returns the exact same source/hash/revision
		// snapshot used by the review workflow. Reuse the caller's bearer and
		// never infer or cache grant state in Body.
		review, reviewErr := client.ReadArticleDraftReviewSource(ctx, token, id)
		if reviewErr != nil {
			return articleDraftToolResultFromError("article_draft_get", reviewErr)
		}
		draft = articleDraftFromCallerAuthorizedReview(review)
		reviewerProjection = true
	}
	if !draftIsArticleDraft(draft) {
		return articleDraftNotFoundDenied("article_draft_get", id)
	}
	if !reviewerProjection && !draftOwnedByAuthenticatedActor(ctx, draft) {
		return articleDraftNotFoundDenied("article_draft_get", draft.ID)
	}
	if reviewerProjection && draft.AuthorID == "" {
		// Reviewer projections must carry Lesser's authoritative owner id.
		// Missing ownership metadata is an incomplete snapshot, not license to
		// widen access or return partially bound source.
		return articleDraftNotFoundDenied("article_draft_get", draft.ID)
	}

	return articleDraftSingleResult("article_draft_get", "read", draft, params, nil)
}

func handleArticleDraftList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
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

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	includeContent := params.View == readViewStandard
	conn, err := client.ListArticleDrafts(ctx, token, limit, in.Cursor, includeContent)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_list", err)
	}
	return articleDraftListResult(conn, limit, params)
}

func handleArticleDraftPreview(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
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

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	// Use Lesser's caller-authorized review projection for the DRAFT-state
	// preflight. Unlike draft(id:), draftReview(id:) authorizes both the owner
	// and active reviewers. Lesser then repeats that authorization inside
	// draftPreview(id:) and enforces the Article boundary in its renderer.
	// Body never infers or caches grant state.
	review, err := client.ReadArticleDraftReview(ctx, token, id)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_preview", err)
	}
	if review == nil || strings.TrimSpace(review.Status) != cmsapi.DraftStatusDraft {
		return articleDraftNotFoundDenied("article_draft_preview", id)
	}
	preview, err := client.PreviewArticleDraft(ctx, token, id)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_preview", err)
	}

	return articleDraftPreviewResult(preview, params)
}

func articleDraftOwnerLookupNotFound(err error) bool {
	if err == nil {
		return false
	}
	var draftNotFound *cmsapi.DraftNotFoundError
	if errors.As(err, &draftNotFound) {
		return true
	}
	var gqlErr *cmsapi.GraphQLErrors
	if errors.As(err, &gqlErr) {
		code, status := articleDraftGraphQLErrorContract(gqlErr)
		return status == http.StatusNotFound || strings.EqualFold(strings.TrimSpace(code), "not_found")
	}
	var apiErr *lesserapi.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

func articleDraftFromCallerAuthorizedReview(review *cmsapi.DraftReview) *cmsapi.Draft {
	if review == nil || review.Content == nil {
		return nil
	}
	ownerID := ""
	if review.OwnerID != nil {
		ownerID = strings.TrimSpace(*review.OwnerID)
	}
	return &cmsapi.Draft{
		ID:            strings.TrimSpace(review.DraftID),
		AuthorID:      ownerID,
		ContentType:   cmsapi.ObjectTypeArticle,
		Title:         review.Title,
		Slug:          review.Slug,
		Content:       *review.Content,
		ContentFormat: strings.TrimSpace(review.ContentFormat),
		Status:        strings.TrimSpace(review.Status),
		ScheduledAt:   review.ScheduledAt,
		ContentHash:   strings.TrimSpace(review.ContentHash),
		Revision:      review.Revision,
		CreatedAt:     strings.TrimSpace(review.CreatedAt),
		UpdatedAt:     strings.TrimSpace(review.UpdatedAt),
	}
}

func articleCMS(ctx context.Context) (*cmsapi.Client, error) {
	_ = ctx
	return cmsapi.Default()
}

type articleDraftViewParams struct {
	View               string
	PreviewRunes       int
	MaxOutputBytes     int
	ExplicitBudgetHint bool
}

func parseArticleDraftViewParams(args json.RawMessage) (articleDraftViewParams, error) {
	shared, err := parseSharedReadParams(args)
	if err != nil {
		return articleDraftViewParams{}, err
	}
	view := strings.ToLower(strings.TrimSpace(shared.View))
	if view == "" {
		view = readViewCompact
	}
	if view != readViewCompact && view != readViewStandard {
		return articleDraftViewParams{}, fmt.Errorf("view must be compact or standard")
	}
	previewRunes := articleDraftPreviewRunes
	if shared.PreviewChars > 0 {
		previewRunes = shared.PreviewChars
	}
	budget := shared.MaxOutputBytes
	explicitBudget := budget > 0
	if budget <= 0 && view == readViewCompact {
		budget = articleDraftDefaultBudgetBytes
	}
	return articleDraftViewParams{
		View:               view,
		PreviewRunes:       previewRunes,
		MaxOutputBytes:     budget,
		ExplicitBudgetHint: explicitBudget,
	}, nil
}

func normalizeArticleDraftContentFormat(value string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", cmsapi.ContentFormatMarkdown:
		return cmsapi.ContentFormatMarkdown, nil
	case cmsapi.ContentFormatHTML:
		return cmsapi.ContentFormatHTML, nil
	default:
		return "", fmt.Errorf("content_format must be MARKDOWN or HTML")
	}
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func articleDraftSingleResult(toolName string, operation string, draft *cmsapi.Draft, params articleDraftViewParams, fallbackContent *string) (*mcpruntime.ToolResult, error) {
	if draft == nil {
		return nil, fmt.Errorf("%s returned no draft", toolName)
	}
	shaped := shapeArticleDraft(draft, params, fallbackContent)
	policy := articleDraftPolicyMetadata()
	if toolName == "article_draft_get" {
		policy["readAuthorization"] = "lesser_owner_or_active_reviewer"
		policy["reviewerProjection"] = "caller_authorized_draftReview_snapshot"
	}
	payload := map[string]any{
		"tool":      toolName,
		"operation": operation,
		"source":    "lesser_cms_graphql",
		"view":      params.View,
		"draft":     shaped,
		"draftRef":  compactArticleDraftRef(draft, params, fallbackContent),
		"omitted":   articleDraftOmissions(params.View, false),
		"budget":    articleDraftBudget(params),
		"policy":    policy,
	}
	text := map[string]any{
		"tool":      toolName,
		"operation": operation,
		"draftId":   draft.ID,
		"view":      params.View,
	}
	return articleDraftStructuredResult(toolName, params.View, fmt.Sprintf("Article draft %s: %s", operation, draft.ID), payload, text, params.MaxOutputBytes)
}

func articleDraftListResult(conn *cmsapi.DraftConnection, limit int, params articleDraftViewParams) (*mcpruntime.ToolResult, error) {
	if conn == nil {
		conn = &cmsapi.DraftConnection{}
	}
	drafts := make([]map[string]any, 0, len(conn.Edges))
	for _, edge := range conn.Edges {
		cursor := strings.TrimSpace(edge.Cursor)
		if edge.Node == nil {
			if cursor == "" {
				continue
			}
			drafts = append(drafts, compactArticleDraftCursorRef(cursor))
			continue
		}
		item := shapeArticleDraft(edge.Node, params, nil)
		if cursor != "" {
			item["cursor"] = cursor
		}
		drafts = append(drafts, item)
	}
	nextCursor := ""
	if conn.PageInfo.EndCursor != nil && strings.TrimSpace(*conn.PageInfo.EndCursor) != "" && conn.PageInfo.HasNextPage {
		nextCursor = strings.TrimSpace(*conn.PageInfo.EndCursor)
	}
	payload := map[string]any{
		"tool":       "article_draft_list",
		"operation":  "list",
		"source":     "lesser_cms_graphql",
		"view":       params.View,
		"drafts":     drafts,
		"count":      len(drafts),
		"limit":      limit,
		"nextCursor": nextCursor,
		"pageInfo":   conn.PageInfo,
		"totalCount": conn.TotalCount,
		"omitted":    articleDraftListOmissions(),
		"budget":     articleDraftBudget(params),
		"policy":     articleDraftListPolicyMetadata(),
	}
	text := map[string]any{
		"tool":  "article_draft_list",
		"count": len(drafts),
		"view":  params.View,
	}
	if nextCursor != "" {
		text["nextCursor"] = nextCursor
	}
	return articleDraftStructuredResult("article_draft_list", params.View, fmt.Sprintf("%d Article draft refs", len(drafts)), payload, text, params.MaxOutputBytes)
}

func articleDraftPreviewResult(preview *cmsapi.DraftPreview, params articleDraftViewParams) (*mcpruntime.ToolResult, error) {
	if preview == nil {
		return nil, fmt.Errorf("article_draft_preview returned no preview")
	}
	shaped := shapeArticleDraftPreview(preview, params)
	payload := map[string]any{
		"tool":       "article_draft_preview",
		"operation":  "preview",
		"source":     "lesser_cms_graphql",
		"view":       params.View,
		"preview":    shaped,
		"previewRef": compactArticleDraftPreview(preview, params),
		"omitted":    articleDraftPreviewOmissions(preview, params.View),
		"budget":     articleDraftPreviewBudget(params, preview),
		"policy":     articleDraftPreviewPolicyMetadata(),
	}
	text := map[string]any{
		"tool":    "article_draft_preview",
		"draftId": strings.TrimSpace(preview.DraftID),
		"success": preview.Success,
		"view":    params.View,
	}
	if len(preview.Errors) > 0 {
		text["errors"] = preview.Errors
	}
	summary := fmt.Sprintf("Article draft preview: %s", strings.TrimSpace(preview.DraftID))
	if !preview.Success {
		summary = fmt.Sprintf("Article draft preview render failed: %s", strings.TrimSpace(preview.DraftID))
	}
	return articleDraftStructuredResult("article_draft_preview", params.View, summary, payload, text, params.MaxOutputBytes)
}

func shapeArticleDraft(draft *cmsapi.Draft, params articleDraftViewParams, fallbackContent *string) map[string]any {
	if params.View == readViewStandard {
		return standardArticleDraft(draft)
	}
	return compactArticleDraftRef(draft, params, fallbackContent)
}

func shapeArticleDraftPreview(preview *cmsapi.DraftPreview, params articleDraftViewParams) map[string]any {
	if params.View == readViewStandard {
		return standardArticleDraftPreview(preview)
	}
	return compactArticleDraftPreview(preview, params)
}

func compactArticleDraftPreview(preview *cmsapi.DraftPreview, params articleDraftViewParams) map[string]any {
	out := baseArticleDraftPreview(preview)
	out["expand"] = map[string]any{
		"tool":           "article_draft_preview",
		"arguments":      map[string]any{"id": strings.TrimSpace(preview.DraftID), "view": readViewStandard},
		"resultPath":     "structuredContent.data.preview",
		"textResultPath": "payload.preview",
		"resultAccess":   toolResultAccessPath("payload.preview", "data.preview"),
	}
	if preview.Success && preview.RenderedHTML != nil && strings.TrimSpace(*preview.RenderedHTML) != "" {
		renderedPreview, truncated := compactStringWithTruncation(*preview.RenderedHTML, params.PreviewRunes)
		putIfNotEmpty(out, "renderedHtmlPreview", renderedPreview)
		out["renderedHtmlTruncated"] = truncated
	}
	return out
}

func standardArticleDraftPreview(preview *cmsapi.DraftPreview) map[string]any {
	out := baseArticleDraftPreview(preview)
	if preview.Success && preview.RenderedHTML != nil {
		out["renderedHtml"] = *preview.RenderedHTML
	}
	return out
}

func baseArticleDraftPreview(preview *cmsapi.DraftPreview) map[string]any {
	errorsList := append([]string(nil), preview.Errors...)
	if errorsList == nil {
		errorsList = []string{}
	}
	return map[string]any{
		"draftId":       strings.TrimSpace(preview.DraftID),
		"success":       preview.Success,
		"sourceFormat":  strings.TrimSpace(preview.SourceFormat),
		"sourceBytes":   preview.SourceBytes,
		"renderedBytes": preview.RenderedBytes,
		"errors":        errorsList,
	}
}

func compactArticleDraftCursorRef(draftID string) map[string]any {
	draftID = strings.TrimSpace(draftID)
	return map[string]any{
		"id":           draftID,
		"cursor":       draftID,
		"contentType":  cmsapi.ObjectTypeArticle,
		"status":       cmsapi.DraftStatusDraft,
		"depthSafeRef": true,
		"expand": map[string]any{
			"tool":           "article_draft_get",
			"arguments":      map[string]any{"id": draftID, "view": readViewStandard},
			"resultPath":     "structuredContent.data.draft",
			"textResultPath": "payload.draft",
			"resultAccess":   toolResultAccessPath("payload.draft", "data.draft"),
		},
	}
}

func compactArticleDraftRef(draft *cmsapi.Draft, params articleDraftViewParams, fallbackContent *string) map[string]any {
	out := map[string]any{
		"id":            strings.TrimSpace(draft.ID),
		"status":        strings.TrimSpace(draft.Status),
		"contentFormat": strings.TrimSpace(draft.ContentFormat),
		"revision":      draft.Revision,
		"expand": map[string]any{
			"tool":           "article_draft_get",
			"arguments":      map[string]any{"id": strings.TrimSpace(draft.ID), "view": readViewStandard},
			"resultPath":     "structuredContent.data.draft",
			"textResultPath": "payload.draft",
			"resultAccess":   toolResultAccessPath("payload.draft", "data.draft"),
		},
	}
	putIfNotEmpty(out, "title", stringPtrValue(draft.Title))
	putIfNotEmpty(out, "slug", stringPtrValue(draft.Slug))
	putIfNotEmpty(out, "objectId", stringPtrValue(draft.ObjectID))
	putIfNotEmpty(out, "contentHash", draft.ContentHash)
	putIfNotEmpty(out, "lastSavedAt", draft.LastSavedAt)
	putIfNotEmpty(out, "createdAt", draft.CreatedAt)
	putIfNotEmpty(out, "updatedAt", draft.UpdatedAt)
	if actedBy := cmsActorAttributionRef(draft.ActedBy); actedBy != nil {
		out["actedBy"] = actedBy
	}
	content := draft.Content
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

func standardArticleDraft(draft *cmsapi.Draft) map[string]any {
	out := map[string]any{
		"id":              strings.TrimSpace(draft.ID),
		"contentType":     strings.TrimSpace(draft.ContentType),
		"content":         draft.Content,
		"contentFormat":   strings.TrimSpace(draft.ContentFormat),
		"status":          strings.TrimSpace(draft.Status),
		"revision":        draft.Revision,
		"autosaveVersion": draft.AutosaveVersion,
	}
	putIfNotEmpty(out, "title", stringPtrValue(draft.Title))
	putIfNotEmpty(out, "slug", stringPtrValue(draft.Slug))
	putIfNotEmpty(out, "scheduledAt", stringPtrValue(draft.ScheduledAt))
	putIfNotEmpty(out, "objectId", stringPtrValue(draft.ObjectID))
	putIfNotEmpty(out, "contentHash", draft.ContentHash)
	putIfNotEmpty(out, "lastSavedAt", draft.LastSavedAt)
	putIfNotEmpty(out, "createdAt", draft.CreatedAt)
	putIfNotEmpty(out, "updatedAt", draft.UpdatedAt)
	if actedBy := cmsActorAttributionRef(draft.ActedBy); actedBy != nil {
		out["actedBy"] = actedBy
	}
	return out
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// cmsActorAttributionRef shapes Lesser's actedBy share-grant act-as
// attribution for MCP output. It returns nil when Lesser returned no
// attribution: owner-path responses stay byte-identical and Body never
// invents attribution Lesser did not return.
func cmsActorAttributionRef(actor *cmsapi.Actor) map[string]any {
	if actor == nil {
		return nil
	}
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", actor.ID)
	putIfNotEmpty(ref, "username", actor.Username)
	if len(ref) == 0 {
		return nil
	}
	return ref
}

func articleDraftOmissions(view string, list bool) []any {
	if view == readViewStandard {
		return []any{}
	}
	path := "draft.content"
	if list {
		path = "drafts[].content"
	}
	return []any{
		map[string]any{
			"path":      path,
			"reason":    "compact_default",
			"expansion": "call article_draft_get with view=standard",
		},
	}
}

func articleDraftListOmissions() []any {
	return []any{
		map[string]any{
			"path":      "drafts[].metadata",
			"reason":    "graphql_depth_budget",
			"expansion": "call article_draft_get with view=standard for each draft id",
		},
		map[string]any{
			"path":      "drafts[].content",
			"reason":    "graphql_depth_budget",
			"expansion": "call article_draft_get with view=standard for each draft id",
		},
	}
}

func articleDraftPreviewOmissions(preview *cmsapi.DraftPreview, view string) []any {
	if view == readViewStandard || preview == nil || !preview.Success || preview.RenderedHTML == nil {
		return []any{}
	}
	return []any{
		map[string]any{
			"path":      "preview.renderedHtml",
			"reason":    "compact_default",
			"expansion": "call article_draft_preview with view=standard",
		},
	}
}

func articleDraftBudget(params articleDraftViewParams) map[string]any {
	return map[string]any{
		"view":                params.View,
		"contentPreviewRunes": params.PreviewRunes,
		"maxOutputBytes":      params.MaxOutputBytes,
	}
}

func articleDraftPreviewBudget(params articleDraftViewParams, preview *cmsapi.DraftPreview) map[string]any {
	out := map[string]any{
		"view":                 params.View,
		"renderedPreviewRunes": params.PreviewRunes,
		"maxOutputBytes":       params.MaxOutputBytes,
	}
	if preview != nil {
		out["sourceBytes"] = preview.SourceBytes
		out["renderedBytes"] = preview.RenderedBytes
	}
	return out
}

func articleDraftPolicyMetadata() map[string]any {
	return map[string]any{
		"draftOnly":            true,
		"autoPublishes":        false,
		"canonicalArticleId":   "not_promised_until_publish",
		"publishToolAvailable": true,
		"publishTool":          "article_draft_publish",
	}
}

func articleDraftListPolicyMetadata() map[string]any {
	out := articleDraftPolicyMetadata()
	out["graphqlDepthSafe"] = true
	out["listSelection"] = "edges.cursor plus batched depth-safe draft(id:) triage metadata"
	out["expansion"] = "title and timestamps are included for triage; call article_draft_get only for full metadata/content"
	return out
}

func articleDraftPreviewPolicyMetadata() map[string]any {
	return map[string]any{
		"renderAuthority":         "lesser_article_renderer_sanitizer",
		"rendersLocally":          false,
		"rawDraftContentReturned": false,
		"rawDraftContentUsed":     false,
		"graphqlOperation":        "draftPreview",
		"readAuthorization":       "lesser_owner_or_active_reviewer",
		"statePreflight":          "caller_authorized_draftReview",
	}
}

func articleDraftStructuredResult(toolName string, view string, summary string, payload map[string]any, text map[string]any, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: summary,
		Data:    payload,
		Text:    text,
	})
	if err != nil {
		return nil, err
	}
	if maxOutputBytes <= 0 {
		return result, nil
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	return toolErrorResult("response_too_large", toolName+" response exceeds max_output_bytes", http.StatusRequestEntityTooLarge, map[string]any{
		"tool":                   toolName,
		"view":                   view,
		"measuredBytes":          measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":         maxOutputBytes,
		"contentTextBytes":       measurement.ContentTextBytes,
		"structuredContentBytes": measurement.StructuredContentBytes,
		"guidance":               "use compact view, reduce limit, reduce preview_chars, or increase max_output_bytes",
	})
}

func articleDraftToolResultFromError(toolName string, err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var articleNotFound *cmsapi.ArticleNotFoundError
	if errors.As(err, &articleNotFound) {
		return articleNotFoundToolResult(toolName, "Article not found", articleNotFound.Lookup)
	}
	var draftNotFound *cmsapi.DraftNotFoundError
	if errors.As(err, &draftNotFound) {
		return articleNotFoundToolResult(toolName, "Article draft not found", draftNotFound.Lookup)
	}
	var reviewNotFound *cmsapi.DraftReviewNotFoundError
	if errors.As(err, &reviewNotFound) {
		message := "Article draft not found"
		if toolName == "article_draft_review_read" {
			message = "Article draft review not found"
		}
		return articleNotFoundToolResult(toolName, message, reviewNotFound.Lookup)
	}
	var gqlErr *cmsapi.GraphQLErrors
	if errors.As(err, &gqlErr) {
		details := map[string]any{"source": "lesser_cms_graphql", "tool": toolName}
		if len(gqlErr.Errors) > 0 {
			details["graphqlErrors"] = gqlErr.Errors
		}
		code, status := articleDraftGraphQLErrorContract(gqlErr)
		return toolErrorResult(code, "Lesser CMS GraphQL returned errors", status, details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		return toolErrorResult("lesser_cms_http_error", "Lesser CMS API request failed", apiErr.Status, map[string]any{
			"source":       "lesser_cms_graphql",
			"tool":         toolName,
			"upstreamCode": apiErr.Status,
		})
	}
	return normalizedToolResultFromError(toolName, err)
}

func articleNotFoundToolResult(toolName, message, lookup string) (*mcpruntime.ToolResult, error) {
	details := map[string]any{
		"source": "lesser_cms_graphql",
		"tool":   toolName,
	}
	if lookup = strings.TrimSpace(articleNotFoundLookup(toolName, lookup)); lookup != "" {
		details["lookup"] = lookup
	}
	return toolErrorResult("not_found", message, http.StatusNotFound, details)
}

func articleNotFoundLookup(toolName, lookup string) string {
	lookup = strings.TrimSpace(lookup)
	switch toolName {
	case "article_draft_review_read":
		if lookup == "" || lookup == "id" {
			return "draft_id"
		}
	}
	return lookup
}

func articleDraftGraphQLErrorContract(gqlErr *cmsapi.GraphQLErrors) (string, int) {
	code := "lesser_cms_graphql_error"
	status := http.StatusBadGateway
	if gqlErr == nil {
		return code, status
	}
	for _, item := range gqlErr.Errors {
		if candidate, ok := item.Extensions["code"].(string); ok && strings.TrimSpace(candidate) != "" {
			code = strings.TrimSpace(candidate)
		}
		if candidate, err := nonNegativeIntFromAny(item.Extensions["http_status"], "http_status"); err == nil && candidate >= 400 && candidate <= 599 {
			status = candidate
		}
		if code != "lesser_cms_graphql_error" && status != http.StatusBadGateway {
			break
		}
	}
	return code, status
}

// draftOwnedByAuthenticatedActor returns true when the draft's authorId
// matches the authenticated actor identity. An empty AuthorID is treated
// as a failed ownership check (defense-in-depth: if the CMS did not
// populate authorId, do not proceed).
func draftOwnedByAuthenticatedActor(ctx context.Context, draft *cmsapi.Draft) bool {
	if draft == nil {
		return false
	}
	authenticated := authenticatedArticleAuthorID(ctx)
	if authenticated == "" {
		return false
	}
	for _, candidate := range draftAuthorCandidates(draft) {
		if strings.EqualFold(candidate, authenticated) {
			return true
		}
	}
	return false
}

func draftAuthorCandidates(draft *cmsapi.Draft) []string {
	if draft == nil {
		return nil
	}
	candidates := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			candidates = append(candidates, value)
		}
	}
	add(draft.AuthorID)
	if draft.Author != nil {
		add(draft.Author.Username)
		add(draft.Author.ID)
		if local := localActorIDSegment(draft.Author.ID); local != "" {
			add(local)
		}
	}
	return candidates
}

func localActorIDSegment(actorID string) string {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ""
	}
	actorID = strings.TrimRight(actorID, "/")
	idx := strings.LastIndex(actorID, "/")
	if idx < 0 || idx == len(actorID)-1 {
		return ""
	}
	return strings.TrimSpace(actorID[idx+1:])
}

// draftIsArticleDraft returns true only for the ARTICLE + DRAFT subset that
// the article_draft_* MCP tools are allowed to expose. Empty contentType/status
// values fail closed because Lesser's generic draft(id:) response includes both
// fields; missing metadata must not widen Article-only tool access.
func draftIsArticleDraft(draft *cmsapi.Draft) bool {
	if draft == nil {
		return false
	}
	return strings.TrimSpace(draft.ContentType) == cmsapi.ObjectTypeArticle &&
		strings.TrimSpace(draft.Status) == cmsapi.DraftStatusDraft
}

// articleDraftOwnershipDenied returns a toolErrorResult for a
// draft-ownership rejection (CSR-010 defense-in-depth). The error is
// shaped as a generic "not_found" to the caller, matching the behavior
// Lesser CMS returns for unauthorized draft access, so ownership checks
// are not distinguishable from legitimate not-found.
func articleDraftOwnershipDenied(toolName string, draftID string) (*mcpruntime.ToolResult, error) {
	return articleDraftNotFoundDenied(toolName, draftID)
}

// articleDraftNotFoundDenied returns the shared not_found shape for private
// draft boundary rejections, including ownership failures and non-ARTICLE or
// non-DRAFT records. The caller must not be able to distinguish those cases
// from a legitimate missing draft id.
func articleDraftNotFoundDenied(toolName string, draftID string) (*mcpruntime.ToolResult, error) {
	return toolErrorResult("not_found", toolName+" draft not found or not authorized", 404, map[string]any{
		"source":  "lesser_cms_graphql",
		"tool":    toolName,
		"draftId": draftID,
	})
}
