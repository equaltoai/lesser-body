package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const (
	articleDraftReviewDefaultLimit = 5
	articleDraftReviewMaxLimit     = 80
	articleDraftReviewBudgetBytes  = 24000
	articleDraftReviewViewCompact  = "compact"
	articleDraftReviewViewStandard = "standard"
)

const articleDraftReviewPublishGateNote = "Every Article draft created through MCP is agent-generated, so Lesser requires unanimous current approval from every active reviewer plus active approval from the configured instance principal before publishing."

func registerArticleReviewTools(r *mcpruntime.ToolRegistry) error {
	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: articleDraftReviewSubmitDef(), Handler: handleArticleDraftReviewSubmit},
		{Def: articleDraftReviewReadDef(), Handler: handleArticleDraftReviewRead},
		{Def: articleDraftReviewVerdictDef(), Handler: handleArticleDraftReviewVerdict},
	} {
		if err := registerTool(r, tool.Def, tool.Handler); err != nil {
			return err
		}
	}
	return nil
}

func articleDraftReviewSubmitDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_review_submit",
		Description:  "Submit an Article draft to one Lesser reviewer by creating or refreshing Lesser's revocable review grant. Lesser remains the queue, authorization, approval, attribution, and publish-gate authority. " + articleDraftReviewPublishGateNote,
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleDraftReviewSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id owned by the authenticated actor."},
				"reviewer":{"type":"string","description":"Lesser reviewer username accepted by shareDraftForReview."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","reviewer"],
			"additionalProperties":false
		}`),
	}
}

func articleDraftReviewReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_review_read",
		Description:  "Read Lesser's review state for draft_id, or omit draft_id to list the authenticated caller's active compact review queue. State mode defaults to compact metadata; view=standard returns Lesser's complete, untruncated caller-authorized source and canonical rendering with the same content binding, grants, verdict staleness, and publish eligibility. " + articleDraftReviewPublishGateNote,
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: articleDraftReviewReadOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Optional Lesser CMS draft id. When present, returns that caller-authorized review state; when omitted, lists the caller's active queue."},
				"view":{"type":"string","enum":["compact","standard"],"default":"compact","description":"State mode only. compact returns bounded review metadata; standard returns complete untruncated Lesser-authoritative source, canonical rendered HTML/render errors, content binding, grants, verdict staleness, and publish eligibility."},
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Queue mode only. Maximum review items to return; defaults to 5 so a realistic default page fits the 24000-byte response budget."},
				"cursor":{"type":"string","description":"Queue mode only. Pagination cursor from a previous article_draft_review_read response."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"additionalProperties":false
		}`),
	}
}

func articleDraftReviewVerdictDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "article_draft_review_verdict",
		Description:  "Submit an APPROVED or CHANGES_REQUESTED verdict, with optional notes, through Lesser's caller-authorized review contract. Body transports Lesser's resulting authoritative publish eligibility without calculating it. " + articleDraftReviewPublishGateNote,
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: articleDraftReviewSingleOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id in the authenticated caller's active review queue."},
				"verdict":{"type":"string","enum":["APPROVED","CHANGES_REQUESTED"],"description":"Lesser's canonical review verdict."},
				"notes":{"type":"string","description":"Optional reviewer notes recorded by Lesser with the verdict."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","verdict"],
			"additionalProperties":false
		}`),
	}
}

func handleArticleDraftReviewSubmit(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string `json:"draft_id"`
		Reviewer       string `json:"reviewer"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.Reviewer = strings.TrimSpace(in.Reviewer)
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if in.Reviewer == "" {
		return nil, invalidParams("reviewer is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	// Lesser's shareDraftForReview mutation owns grant-creation authorization.
	// Unlike sibling draft tools, this handler deliberately does not apply
	// draftOwnedByAuthenticatedActor (CSR-010) or re-derive Lesser's decision.
	review, err := client.SubmitArticleDraftForReview(ctx, token, in.DraftID, in.Reviewer)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_review_submit", err)
	}
	return articleDraftReviewSingleResult("article_draft_review_submit", "submitted", review, reviewOutputBudget(in.MaxOutputBytes))
}

func handleArticleDraftReviewRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string `json:"draft_id,omitempty"`
		View           string `json:"view,omitempty"`
		Limit          int    `json:"limit,omitempty"`
		Cursor         string `json:"cursor,omitempty"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if len(strings.TrimSpace(string(args))) > 0 && strings.TrimSpace(string(args)) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.View = strings.ToLower(strings.TrimSpace(in.View))
	if in.View == "" {
		in.View = articleDraftReviewViewCompact
	}
	in.Cursor = strings.TrimSpace(in.Cursor)
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}
	if in.DraftID != "" && (in.Limit != 0 || in.Cursor != "") {
		return nil, invalidParams("limit and cursor are only valid when draft_id is omitted")
	}
	if in.View != articleDraftReviewViewCompact && in.View != articleDraftReviewViewStandard {
		return nil, invalidParams("view must be compact or standard")
	}
	if in.DraftID == "" && in.View != articleDraftReviewViewCompact {
		return nil, invalidParams("view=standard requires draft_id; review queues are compact metadata only")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	budget := reviewOutputBudget(in.MaxOutputBytes)
	if in.DraftID != "" {
		var review *cmsapi.DraftReview
		if in.View == articleDraftReviewViewStandard {
			review, err = client.ReadArticleDraftReviewStandard(ctx, token, in.DraftID)
		} else {
			review, err = client.ReadArticleDraftReview(ctx, token, in.DraftID)
		}
		if err != nil {
			return articleDraftToolResultFromError("article_draft_review_read", err)
		}
		return articleDraftReviewStateViewResult(review, in.View, budget)
	}
	limit := in.Limit
	if limit == 0 {
		limit = articleDraftReviewDefaultLimit
	}
	if limit < 1 || limit > articleDraftReviewMaxLimit {
		return nil, invalidParams(fmt.Sprintf("limit must be between 1 and %d", articleDraftReviewMaxLimit))
	}
	queue, err := client.ListArticleDraftReviews(ctx, token, limit, in.Cursor)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_review_read", err)
	}
	return articleDraftReviewQueueResult(queue, limit, budget)
}

func handleArticleDraftReviewVerdict(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string  `json:"draft_id"`
		Verdict        string  `json:"verdict"`
		Notes          *string `json:"notes,omitempty"`
		MaxOutputBytes int     `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.Verdict = strings.ToUpper(strings.TrimSpace(in.Verdict))
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if in.Verdict != cmsapi.DraftReviewVerdictApproved && in.Verdict != cmsapi.DraftReviewVerdictChangesRequested {
		return nil, invalidParams("verdict must be APPROVED or CHANGES_REQUESTED")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}
	if in.Notes != nil {
		trimmed := strings.TrimSpace(*in.Notes)
		in.Notes = &trimmed
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := articleCMS(ctx)
	if err != nil {
		return nil, err
	}
	review, err := client.SubmitArticleDraftReviewVerdict(ctx, token, in.DraftID, in.Verdict, in.Notes)
	if err != nil {
		return articleDraftToolResultFromError("article_draft_review_verdict", err)
	}
	return articleDraftReviewSingleResult("article_draft_review_verdict", "verdict_submitted", review, reviewOutputBudget(in.MaxOutputBytes))
}

func articleDraftReviewSingleResult(toolName, operation string, review *cmsapi.DraftReview, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if review == nil {
		return nil, fmt.Errorf("%s returned no review", toolName)
	}
	payload := map[string]any{
		"tool":      toolName,
		"operation": operation,
		"source":    "lesser_cms_graphql",
		"review":    review,
	}
	return articleDraftReviewResult(toolName, operation, fmt.Sprintf("Article draft review %s: %s", operation, review.DraftID), payload, maxOutputBytes)
}

func articleDraftReviewStateResult(review *cmsapi.DraftReview, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	return articleDraftReviewStateViewResult(review, articleDraftReviewViewCompact, maxOutputBytes)
}

func articleDraftReviewStateViewResult(review *cmsapi.DraftReview, view string, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if review == nil {
		return nil, fmt.Errorf("article_draft_review_read returned no review")
	}
	payload := map[string]any{
		"tool":      "article_draft_review_read",
		"operation": "state",
		"source":    "lesser_cms_graphql",
		"mode":      "state",
		"view":      view,
		"review":    review,
		"count":     1,
	}
	return articleDraftReviewResult("article_draft_review_read", "state", "Article draft review state: "+review.DraftID, payload, maxOutputBytes)
}

func articleDraftReviewQueueResult(queue *cmsapi.DraftReviewConnection, limit, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if queue == nil {
		queue = &cmsapi.DraftReviewConnection{Edges: []cmsapi.DraftReviewEdge{}}
	}
	reviews := make([]map[string]any, 0, len(queue.Edges))
	for _, edge := range queue.Edges {
		if edge.Node == nil {
			continue
		}
		reviews = append(reviews, map[string]any{"review": edge.Node, "cursor": strings.TrimSpace(edge.Cursor)})
	}
	nextCursor := ""
	if queue.PageInfo.HasNextPage && queue.PageInfo.EndCursor != nil {
		nextCursor = strings.TrimSpace(*queue.PageInfo.EndCursor)
	}
	payload := map[string]any{
		"tool":       "article_draft_review_read",
		"operation":  "queue",
		"source":     "lesser_cms_graphql",
		"mode":       "queue",
		"view":       articleDraftReviewViewCompact,
		"reviews":    reviews,
		"count":      len(reviews),
		"limit":      limit,
		"nextCursor": nextCursor,
		"pageInfo":   queue.PageInfo,
		"totalCount": queue.TotalCount,
	}
	return articleDraftReviewResult("article_draft_review_read", "queue", fmt.Sprintf("%d Article draft reviews", len(reviews)), payload, maxOutputBytes)
}

func articleDraftReviewResult(toolName, operation, summary string, payload map[string]any, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: summary,
		Data:    payload,
		Text: map[string]any{
			"tool":      toolName,
			"operation": operation,
		},
	})
	if err != nil {
		return nil, err
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	guidance := "increase max_output_bytes"
	if operation == "queue" {
		guidance = "reduce queue limit or increase max_output_bytes"
	} else if operation == "state" && payload["view"] == articleDraftReviewViewStandard {
		guidance = "increase max_output_bytes; standard review evidence is never truncated"
	}
	return toolErrorResult("response_too_large", toolName+" response exceeds max_output_bytes", 413, map[string]any{
		"tool":                   toolName,
		"operation":              operation,
		"measuredBytes":          measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":         maxOutputBytes,
		"contentTextBytes":       measurement.ContentTextBytes,
		"structuredContentBytes": measurement.StructuredContentBytes,
		"guidance":               guidance,
	})
}

func reviewOutputBudget(requested int) int {
	if requested > 0 {
		return requested
	}
	return articleDraftReviewBudgetBytes
}
