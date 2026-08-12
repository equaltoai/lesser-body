package cmsapi

import (
	"context"
	"fmt"
	"strings"
)

// Article is Lesser's CMS Article type as consumed by body's published Article
// tools. It intentionally models only scalar fields needed for bounded MCP
// responses; nested author/media/series/category shapes remain Lesser-owned.
type Article struct {
	ID                 string  `json:"id"`
	Slug               string  `json:"slug,omitempty"`
	Title              string  `json:"title,omitempty"`
	Subtitle           *string `json:"subtitle,omitempty"`
	Excerpt            *string `json:"excerpt,omitempty"`
	Content            string  `json:"content,omitempty"`
	ContentFormat      string  `json:"contentFormat,omitempty"`
	ReadingTimeMinutes int     `json:"readingTimeMinutes,omitempty"`
	WordCount          int     `json:"wordCount,omitempty"`
	CanonicalURL       *string `json:"canonicalUrl,omitempty"`
	SEOTitle           *string `json:"seoTitle,omitempty"`
	SEODescription     *string `json:"seoDescription,omitempty"`
	OGImage            *string `json:"ogImage,omitempty"`
	EditorNotes        *string `json:"editorNotes,omitempty"`
	ReviewStatus       *string `json:"reviewStatus,omitempty"`
	ActedBy            *Actor  `json:"actedBy,omitempty"` // Lesser share-grant act-as attribution; resolved only for article-author or instance-admin viewers, nil otherwise.
	PublishedAt        string  `json:"publishedAt,omitempty"`
	CreatedAt          string  `json:"createdAt,omitempty"`
	UpdatedAt          string  `json:"updatedAt,omitempty"`
}

// UpdateArticleInput is the bounded subset of Lesser UpdateArticleInput exposed
// through body. Slug changes are intentionally not surfaced because canonical
// published Article slugs are immutable in Lesser M1.
type UpdateArticleInput struct {
	Title         *string `json:"title,omitempty"`
	Content       *string `json:"content,omitempty"`
	ContentFormat *string `json:"contentFormat,omitempty"`
	Subtitle      *string `json:"subtitle,omitempty"`
	Excerpt       *string `json:"excerpt,omitempty"`
}

type articleResponse struct {
	Article *Article `json:"article"`
}

type articleBySlugResponse struct {
	ArticleBySlug *Article `json:"articleBySlug"`
}

type publishDraftResponse struct {
	PublishDraft *Article `json:"publishDraft"`
}

type updateArticleResponse struct {
	UpdateArticle *Article `json:"updateArticle"`
}

type articleConnectionResponse struct {
	Articles ArticleConnection `json:"articles"`
}

// ArticleConnection is Lesser's paginated articles response.
type ArticleConnection struct {
	Edges      []ArticleEdge `json:"edges"`
	PageInfo   PageInfo      `json:"pageInfo"`
	TotalCount int           `json:"totalCount"`
}

// ArticleEdge is one edge in Lesser's article connection.
type ArticleEdge struct {
	Node   *Article `json:"node"`
	Cursor string   `json:"cursor"`
}

// PublishArticleDraft publishes an existing ARTICLE draft through Lesser's
// GraphQL CMS API and returns Lesser's canonical published Article.
func (c *Client) PublishArticleDraft(ctx context.Context, bearerToken string, id string, includeContent bool) (*Article, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyPublishArticleDraft($id: ID!) { publishDraft(id: $id) { " + articleFields(includeContent) + " } }",
		OperationName: "BodyPublishArticleDraft",
		Variables:     map[string]any{"id": id},
	})
	if err != nil {
		return nil, err
	}
	var data publishDraftResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.PublishDraft == nil {
		return nil, fmt.Errorf("publishDraft returned no article")
	}
	return data.PublishDraft, nil
}

// UpdateArticle updates a published Article through Lesser's GraphQL CMS API.
func (c *Client) UpdateArticle(ctx context.Context, bearerToken string, id string, input UpdateArticleInput, includeContent bool) (*Article, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("article id is required")
	}
	variables := map[string]any{"id": id, "input": updateArticleVariables(input)}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyUpdateArticle($id: ID!, $input: UpdateArticleInput!) { updateArticle(id: $id, input: $input) { " + articleFields(includeContent) + " } }",
		OperationName: "BodyUpdateArticle",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data updateArticleResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.UpdateArticle == nil {
		return nil, fmt.Errorf("updateArticle returned no article")
	}
	return data.UpdateArticle, nil
}

// GetArticle reads a single published Article by canonical Article ID.
func (c *Client) GetArticle(ctx context.Context, bearerToken string, id string, includeContent bool) (*Article, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("article id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticle($id: ID!) { article(id: $id) { " + articleFields(includeContent) + " } }",
		OperationName: "BodyArticle",
		Variables:     map[string]any{"id": id},
	})
	if err != nil {
		return nil, err
	}
	var data articleResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.Article == nil {
		return nil, fmt.Errorf("article %q not found", id)
	}
	return data.Article, nil
}

// GetArticleBySlug reads one published Article through Lesser's canonical
// Article-by-slug resolver. Prefer GetArticle when the canonical ID is known.
func (c *Client) GetArticleBySlug(ctx context.Context, bearerToken string, slug string, includeContent bool) (*Article, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("article slug is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleBySlug($slug: String!) { articleBySlug(slug: $slug) { " + articleFields(includeContent) + " } }",
		OperationName: "BodyArticleBySlug",
		Variables:     map[string]any{"slug": slug},
	})
	if err != nil {
		return nil, err
	}
	var data articleBySlugResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.ArticleBySlug == nil {
		return nil, fmt.Errorf("article slug %q not found", slug)
	}
	return data.ArticleBySlug, nil
}

// ListArticles reads the authenticated actor's published Articles by author ID.
func (c *Client) ListArticles(ctx context.Context, bearerToken string, first int, after string, authorID string, includeContent bool) (*ArticleConnection, error) {
	if first <= 0 {
		first = 20
	}
	variables := map[string]any{"first": first}
	if after = strings.TrimSpace(after); after != "" {
		variables["after"] = after
	}
	if authorID = strings.TrimSpace(authorID); authorID != "" {
		variables["authorId"] = authorID
	}
	// Lesser's agent/CLI GraphQL profile currently enforces max depth 3. A
	// connection selection with edges.node reaches depth 4, so list stays on
	// edge cursors plus pageInfo until Lesser exposes a depth-safe Article list
	// item contract (tracked in equaltoai/lesser#1221).
	_ = includeContent
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticles($authorId: ID, $first: Int, $after: Cursor) { articles(authorId: $authorId, first: $first, after: $after) { edges { cursor } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } totalCount } }",
		OperationName: "BodyArticles",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data articleConnectionResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	return &data.Articles, nil
}

func updateArticleVariables(input UpdateArticleInput) map[string]any {
	out := map[string]any{}
	if input.Title != nil {
		out["title"] = *input.Title
	}
	if input.Content != nil {
		out["content"] = *input.Content
	}
	if input.ContentFormat != nil {
		out["contentFormat"] = normalizeContentFormat(*input.ContentFormat)
	}
	if input.Subtitle != nil {
		out["subtitle"] = *input.Subtitle
	}
	if input.Excerpt != nil {
		out["excerpt"] = *input.Excerpt
	}
	return out
}

func articleFields(includeContent bool) string {
	fields := "id slug title subtitle excerpt contentFormat readingTimeMinutes wordCount canonicalUrl seoTitle seoDescription ogImage editorNotes reviewStatus actedBy { id username } publishedAt createdAt updatedAt"
	if includeContent {
		fields += " content"
	}
	return fields
}
