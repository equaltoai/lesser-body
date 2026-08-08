package cmsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ContentFormatMarkdown = "MARKDOWN"
	ContentFormatHTML     = "HTML"
	ObjectTypeArticle     = "ARTICLE"
	DraftStatusDraft      = "DRAFT"
)

// Actor is the bounded Lesser GraphQL Actor projection needed by the CMS
// draft adapters. The full Actor shape remains Lesser-owned.
type Actor struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
}

// Draft is Lesser's CMS Draft type as consumed by body's Article draft tools.
// It intentionally models only the fields needed for the draft-only MCP slice.
type Draft struct {
	ID              string  `json:"id"`
	AuthorID        string  `json:"authorId,omitempty"` // legacy fallback; current Lesser exposes Author.
	Author          *Actor  `json:"author,omitempty"`
	ContentType     string  `json:"contentType,omitempty"`
	Title           *string `json:"title,omitempty"`
	Slug            *string `json:"slug,omitempty"`
	Content         string  `json:"content,omitempty"`
	ContentFormat   string  `json:"contentFormat,omitempty"`
	Status          string  `json:"status,omitempty"`
	ScheduledAt     *string `json:"scheduledAt,omitempty"`
	ObjectID        *string `json:"objectId,omitempty"`
	ContentHash     string  `json:"contentHash,omitempty"`
	Revision        int     `json:"revision,omitempty"`
	AutosaveVersion int     `json:"autosaveVersion,omitempty"`
	LastSavedAt     string  `json:"lastSavedAt,omitempty"`
	CreatedAt       string  `json:"createdAt,omitempty"`
	UpdatedAt       string  `json:"updatedAt,omitempty"`
}

// DraftPreview is Lesser's canonical ARTICLE draft preview contract. The HTML
// is rendered and sanitized by Lesser; body never renders draft source locally.
type DraftPreview struct {
	DraftID       string   `json:"draftId"`
	Success       bool     `json:"success"`
	RenderedHTML  *string  `json:"renderedHtml,omitempty"`
	SourceFormat  string   `json:"sourceFormat"`
	SourceBytes   int      `json:"sourceBytes"`
	RenderedBytes int      `json:"renderedBytes"`
	Errors        []string `json:"errors"`
}

// CreateDraftInput is the draft-only subset of Lesser CreateDraftInput that
// body exposes as Article authoring. contentType is forced to ARTICLE.
type CreateDraftInput struct {
	Title         *string `json:"title,omitempty"`
	Slug          *string `json:"slug,omitempty"`
	Content       string  `json:"content"`
	ContentFormat string  `json:"contentFormat,omitempty"`
	ObjectID      *string `json:"objectId,omitempty"`
}

// UpdateDraftInput is the draft-only subset of Lesser UpdateDraftInput.
type UpdateDraftInput struct {
	Title         *string `json:"title,omitempty"`
	Slug          *string `json:"slug,omitempty"`
	Content       *string `json:"content,omitempty"`
	ContentFormat *string `json:"contentFormat,omitempty"`
}

type draftResponse struct {
	Draft *Draft `json:"draft"`
}

type draftPreviewResponse struct {
	DraftPreview *DraftPreview `json:"draftPreview"`
}

type createDraftResponse struct {
	CreateDraft *Draft `json:"createDraft"`
}

type updateDraftResponse struct {
	UpdateDraft *Draft `json:"updateDraft"`
}

type draftConnectionResponse struct {
	MyDrafts DraftConnection `json:"myDrafts"`
}

// DraftConnection is Lesser's paginated myDrafts response.
type DraftConnection struct {
	Edges      []DraftEdge `json:"edges"`
	PageInfo   PageInfo    `json:"pageInfo"`
	TotalCount int         `json:"totalCount"`
}

// DraftEdge is one edge in Lesser's draft connection.
type DraftEdge struct {
	Node   *Draft `json:"node"`
	Cursor string `json:"cursor"`
}

// PageInfo is the GraphQL pagination shape returned by Lesser.
type PageInfo struct {
	HasNextPage     bool    `json:"hasNextPage"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	StartCursor     *string `json:"startCursor,omitempty"`
	EndCursor       *string `json:"endCursor,omitempty"`
}

// CreateArticleDraft creates an ARTICLE draft through Lesser's GraphQL CMS API.
func (c *Client) CreateArticleDraft(ctx context.Context, bearerToken string, input CreateDraftInput, includeContent bool) (*Draft, error) {
	variables := map[string]any{"input": createDraftVariables(input)}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyCreateArticleDraft($input: CreateDraftInput!) { createDraft(input: $input) { " + draftFields(includeContent) + " } }",
		OperationName: "BodyCreateArticleDraft",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data createDraftResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.CreateDraft == nil {
		return nil, fmt.Errorf("createDraft returned no draft")
	}
	return data.CreateDraft, nil
}

// UpdateArticleDraft updates an ARTICLE draft through Lesser's GraphQL CMS API.
func (c *Client) UpdateArticleDraft(ctx context.Context, bearerToken string, id string, input UpdateDraftInput, includeContent bool) (*Draft, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	variables := map[string]any{"id": id, "input": updateDraftVariables(input)}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyUpdateArticleDraft($id: ID!, $input: UpdateDraftInput!) { updateDraft(id: $id, input: $input) { " + draftFields(includeContent) + " } }",
		OperationName: "BodyUpdateArticleDraft",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data updateDraftResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.UpdateDraft == nil {
		return nil, fmt.Errorf("updateDraft returned no draft")
	}
	return data.UpdateDraft, nil
}

// GetArticleDraft reads a single ARTICLE draft through Lesser's GraphQL CMS API.
func (c *Client) GetArticleDraft(ctx context.Context, bearerToken string, id string, includeContent bool) (*Draft, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDraft($id: ID!) { draft(id: $id) { " + draftFields(includeContent) + " } }",
		OperationName: "BodyArticleDraft",
		Variables:     map[string]any{"id": id},
	})
	if err != nil {
		return nil, err
	}
	var data draftResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.Draft == nil {
		return nil, fmt.Errorf("draft %q not found", id)
	}
	return data.Draft, nil
}

// PreviewArticleDraft reads Lesser's canonical rendered/sanitized preview for a
// single ARTICLE draft. Authorization and ownership are enforced by Lesser on
// the same path as draft(id:).
func (c *Client) PreviewArticleDraft(ctx context.Context, bearerToken string, id string) (*DraftPreview, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDraftPreview($id: ID!) { draftPreview(id: $id) { draftId success renderedHtml sourceFormat sourceBytes renderedBytes errors } }",
		OperationName: "BodyArticleDraftPreview",
		Variables:     map[string]any{"id": id},
	})
	if err != nil {
		return nil, err
	}
	var data draftPreviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.DraftPreview == nil {
		return nil, fmt.Errorf("draftPreview returned no preview")
	}
	return data.DraftPreview, nil
}

// ListArticleDrafts reads the authenticated actor's ARTICLE drafts with status DRAFT.
func (c *Client) ListArticleDrafts(ctx context.Context, bearerToken string, first int, after string, includeContent bool) (*DraftConnection, error) {
	if first <= 0 {
		first = 20
	}
	variables := map[string]any{"first": first}
	if after = strings.TrimSpace(after); after != "" {
		variables["after"] = after
	}
	// Lesser's agent/CLI GraphQL profile currently enforces max depth 3. A
	// connection selection with edges.node reaches depth 4, so the first query
	// stays on edge cursors plus pageInfo. The cursor is the draft ID; a second
	// depth-safe batched root query hydrates triage metadata without N+1 calls.
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDrafts($first: Int, $after: Cursor) { myDrafts(contentType: ARTICLE, status: DRAFT, first: $first, after: $after) { edges { cursor } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } totalCount } }",
		OperationName: "BodyArticleDrafts",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data draftConnectionResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if err := c.hydrateDraftListEdges(ctx, bearerToken, data.MyDrafts.Edges, includeContent); err != nil {
		return nil, err
	}
	return &data.MyDrafts, nil
}

const draftListHydrationBatchSize = 20

func (c *Client) hydrateDraftListEdges(ctx context.Context, bearerToken string, edges []DraftEdge, includeContent bool) error {
	for start := 0; start < len(edges); start += draftListHydrationBatchSize {
		end := start + draftListHydrationBatchSize
		if end > len(edges) {
			end = len(edges)
		}
		variables := map[string]any{}
		definitions := make([]string, 0, end-start)
		selections := make([]string, 0, end-start)
		indexes := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			id := strings.TrimSpace(edges[i].Cursor)
			if id == "" {
				continue
			}
			alias := fmt.Sprintf("draft%d", len(indexes))
			variable := fmt.Sprintf("id%d", len(indexes))
			definitions = append(definitions, "$"+variable+": ID!")
			selections = append(selections, alias+": draft(id: $"+variable+") { "+draftFields(includeContent)+" }")
			variables[variable] = id
			indexes = append(indexes, i)
		}
		if len(indexes) == 0 {
			continue
		}
		query := "query BodyArticleDraftListDetails(" + strings.Join(definitions, ", ") + ") { " + strings.Join(selections, " ") + " }"
		resp, err := c.Execute(ctx, bearerToken, Operation{Query: query, OperationName: "BodyArticleDraftListDetails", Variables: variables})
		if err != nil {
			return err
		}
		var hydrated map[string]*Draft
		if err := unmarshalData(resp, &hydrated); err != nil {
			return err
		}
		for aliasIndex, edgeIndex := range indexes {
			if draft := hydrated[fmt.Sprintf("draft%d", aliasIndex)]; draft != nil {
				edges[edgeIndex].Node = draft
			}
		}
	}
	return nil
}

func createDraftVariables(input CreateDraftInput) map[string]any {
	out := map[string]any{
		"contentType": ObjectTypeArticle,
		"content":     input.Content,
	}
	if input.Title != nil {
		out["title"] = *input.Title
	}
	if input.Slug != nil {
		out["slug"] = *input.Slug
	}
	if format := normalizeContentFormat(input.ContentFormat); format != "" {
		out["contentFormat"] = format
	} else {
		out["contentFormat"] = ContentFormatMarkdown
	}
	if input.ObjectID != nil {
		out["objectId"] = *input.ObjectID
	}
	return out
}

func updateDraftVariables(input UpdateDraftInput) map[string]any {
	out := map[string]any{}
	if input.Title != nil {
		out["title"] = *input.Title
	}
	if input.Slug != nil {
		out["slug"] = *input.Slug
	}
	if input.Content != nil {
		out["content"] = *input.Content
	}
	if input.ContentFormat != nil {
		out["contentFormat"] = normalizeContentFormat(*input.ContentFormat)
	}
	return out
}

func normalizeContentFormat(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case ContentFormatHTML:
		return ContentFormatHTML
	case ContentFormatMarkdown, "":
		return ContentFormatMarkdown
	default:
		return strings.ToUpper(strings.TrimSpace(value))
	}
}

func draftFields(includeContent bool) string {
	fields := "id author { id username } contentType title slug contentFormat status scheduledAt objectId contentHash revision autosaveVersion lastSavedAt createdAt updatedAt"
	if includeContent {
		fields += " content"
	}
	return fields
}

func unmarshalData(resp *Response, out any) error {
	if resp == nil {
		return fmt.Errorf("graphql response is nil")
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return fmt.Errorf("graphql response data is empty")
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("unmarshal graphql data: %w", err)
	}
	return nil
}
