package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

// Response-shape tests for the issue #593 read surface: draft reads carry
// editorial-media bindings (compact presence notes + standard full list), and
// article reads carry the featured image (compact presence note + standard
// hero object), with the preview tool passing composed HTML through untouched.

func mediaUsageFixture(role string) cmsapi.EditorialMediaUsage {
	url := "https://cdn.example.com/hero.jpg"
	return cmsapi.EditorialMediaUsage{
		MediaID:      "media-hero",
		Role:         role,
		State:        cmsapi.EditorialMediaStateReady,
		PublishedURL: &url,
		Caption:      stringPtr("Hero image"),
	}
}

func draftWithMediaFixture() *cmsapi.Draft {
	draft := &cmsapi.Draft{
		ID:            "draft-1",
		AuthorID:      "alice",
		ContentType:   cmsapi.ObjectTypeArticle,
		Title:         stringPtr("Hello"),
		ContentFormat: cmsapi.ContentFormatMarkdown,
		Status:        cmsapi.DraftStatusDraft,
		ContentHash:   "sha256:draft",
		Revision:      3,
	}
	draft.EditorialMedia = []cmsapi.EditorialMediaUsage{
		mediaUsageFixture(cmsapi.EditorialMediaRoleHero),
		{MediaID: "media-inline", Role: cmsapi.EditorialMediaRoleInline, State: cmsapi.EditorialMediaStateReady},
		mediaUsageFixture(cmsapi.EditorialMediaRoleHero), // duplicate role must dedupe in the compact note
	}
	return draft
}

func articleWithFeaturedImageFixture() *cmsapi.Article {
	og := "https://cdn.example.com/og.jpg"
	width := 1200
	height := 630
	return &cmsapi.Article{
		ID:            "https://example.com/articles/hello",
		Slug:          "hello",
		Title:         "Hello",
		ContentFormat: cmsapi.ContentFormatMarkdown,
		OGImage:       &og,
		FeaturedImage: &cmsapi.Media{
			ID:          "media-hero",
			Type:        "IMAGE",
			URL:         "https://cdn.example.com/hero.jpg",
			Description: stringPtr("Hero image"),
			Width:       &width,
			Height:      &height,
			MimeType:    stringPtr("image/jpeg"),
		},
	}
}

func TestCompactDraftRefNotesEditorialMediaPresenceAndRoles(t *testing.T) {
	params := articleDraftViewParams{View: readViewCompact, PreviewRunes: articleDraftPreviewRunes, MaxOutputBytes: articleDraftDefaultBudgetBytes}

	withMedia := compactArticleDraftRef(draftWithMediaFixture(), params, nil)
	if intFromAny(withMedia["editorialMediaCount"]) != 3 {
		t.Fatalf("editorialMediaCount = %#v, want 3", withMedia["editorialMediaCount"])
	}
	roles := stringSliceFromAny(withMedia["editorialMediaRoles"])
	if len(roles) != 2 || roles[0] != cmsapi.EditorialMediaRoleHero || roles[1] != cmsapi.EditorialMediaRoleInline {
		t.Fatalf("editorialMediaRoles = %#v, want deduped [HERO INLINE]", roles)
	}

	withoutMedia := compactArticleDraftRef(&cmsapi.Draft{ID: "draft-1"}, params, nil)
	if _, hasCount := withoutMedia["editorialMediaCount"]; hasCount {
		t.Fatalf("draft without bindings must not note media presence: %#v", withoutMedia)
	}
	if _, hasRoles := withoutMedia["editorialMediaRoles"]; hasRoles {
		t.Fatalf("draft without bindings must not note media roles: %#v", withoutMedia)
	}
}

func TestStandardDraftSurfacesEditorialMediaBindings(t *testing.T) {
	withMedia := standardArticleDraft(draftWithMediaFixture())
	media, ok := withMedia["editorialMedia"].([]cmsapi.EditorialMediaUsage)
	if !ok || len(media) != 3 {
		t.Fatalf("standard draft editorialMedia = %#v, want 3 bindings", withMedia["editorialMedia"])
	}
	if intFromAny(withMedia["editorialMediaCount"]) != 3 {
		t.Fatalf("editorialMediaCount = %#v, want 3", withMedia["editorialMediaCount"])
	}

	withoutMedia := standardArticleDraft(&cmsapi.Draft{ID: "draft-1"})
	empty, ok := withoutMedia["editorialMedia"].([]cmsapi.EditorialMediaUsage)
	if !ok || len(empty) != 0 {
		t.Fatalf("standard draft without bindings must surface an empty list: %#v", withoutMedia["editorialMedia"])
	}
	if intFromAny(withoutMedia["editorialMediaCount"]) != 0 {
		t.Fatalf("editorialMediaCount = %#v, want 0", withoutMedia["editorialMediaCount"])
	}
}

func TestCompactArticleRefNotesFeaturedImagePresence(t *testing.T) {
	params := articleDraftViewParams{View: readViewCompact, PreviewRunes: articleDraftPreviewRunes, MaxOutputBytes: articleDraftDefaultBudgetBytes}

	withHero := compactArticleRef(articleWithFeaturedImageFixture(), params, nil)
	if withHero["featuredImagePresent"] != true {
		t.Fatalf("featuredImagePresent = %#v, want true", withHero["featuredImagePresent"])
	}
	if withHero["featuredImageUrl"] != "https://cdn.example.com/hero.jpg" {
		t.Fatalf("featuredImageUrl = %#v", withHero["featuredImageUrl"])
	}

	withoutHero := compactArticleRef(&cmsapi.Article{ID: "https://example.com/articles/hello"}, params, nil)
	if _, hasPresent := withoutHero["featuredImagePresent"]; hasPresent {
		t.Fatalf("article without hero must not note featured image presence: %#v", withoutHero)
	}
}

func TestStandardArticleSurfacesFeaturedImageAndKeepsOGImage(t *testing.T) {
	out := standardArticle(articleWithFeaturedImageFixture())
	if out["ogImage"] != "https://cdn.example.com/og.jpg" {
		t.Fatalf("ogImage = %#v, want preserved", out["ogImage"])
	}
	featured, ok := out["featuredImage"].(map[string]any)
	if !ok {
		t.Fatalf("featuredImage = %#v, want object", out["featuredImage"])
	}
	if featured["id"] != "media-hero" || featured["type"] != "IMAGE" || featured["url"] != "https://cdn.example.com/hero.jpg" || featured["description"] != "Hero image" || intFromAny(featured["width"]) != 1200 || intFromAny(featured["height"]) != 630 || featured["mimeType"] != "image/jpeg" {
		t.Fatalf("featuredImage object = %#v", featured)
	}

	withoutHero := standardArticle(&cmsapi.Article{ID: "https://example.com/articles/hello"})
	if _, hasFeatured := withoutHero["featuredImage"]; hasFeatured {
		t.Fatalf("article without hero must not surface featuredImage: %#v", withoutHero)
	}
}

func TestCompactPreviewNotesRenderedHTMLImagePresence(t *testing.T) {
	params := articleDraftViewParams{View: readViewCompact, PreviewRunes: articleDraftPreviewRunes, MaxOutputBytes: articleDraftDefaultBudgetBytes}

	withImages := compactArticleDraftPreview(&cmsapi.DraftPreview{
		DraftID:       "draft-1",
		Success:       true,
		RenderedHTML:  stringPtr(`<article><figure><img src="https://cdn.example.com/hero.jpg" alt="Hero"></figure><p>Body</p></article>`),
		SourceFormat:  cmsapi.ContentFormatMarkdown,
		RenderedBytes: 90,
	}, params)
	if withImages["renderedHtmlContainsImages"] != true {
		t.Fatalf("renderedHtmlContainsImages = %#v, want true for composed <figure><img>", withImages["renderedHtmlContainsImages"])
	}

	withoutImages := compactArticleDraftPreview(&cmsapi.DraftPreview{
		DraftID:       "draft-1",
		Success:       true,
		RenderedHTML:  stringPtr("<article><h1>Hello</h1><p>No media</p></article>"),
		SourceFormat:  cmsapi.ContentFormatMarkdown,
		RenderedBytes: 40,
	}, params)
	if withoutImages["renderedHtmlContainsImages"] != false {
		t.Fatalf("renderedHtmlContainsImages = %#v, want false for plain HTML", withoutImages["renderedHtmlContainsImages"])
	}
}

func TestArticleDraftGetReturnsBindingsOnOwnerPath(t *testing.T) {
	draft := draftWithMediaFixture()
	newArticleDraftGraphQLTestServer(t, *draft)

	result, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_get: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_draft_get result = %+v", result)
	}
	data := articleListStructuredData(t, result)
	shaped, _ := data["draft"].(map[string]any)
	media, ok := shaped["editorialMedia"].([]cmsapi.EditorialMediaUsage)
	if !ok || len(media) != 3 {
		t.Fatalf("standard owner-path draft must surface bindings: %#v", shaped["editorialMedia"])
	}
	if intFromAny(shaped["editorialMediaCount"]) != 3 {
		t.Fatalf("editorialMediaCount = %#v, want 3", shaped["editorialMediaCount"])
	}

	compact, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1"}`))
	if err != nil {
		t.Fatalf("compact article_draft_get: %v", err)
	}
	compactData := articleListStructuredData(t, compact)
	ref, _ := compactData["draftRef"].(map[string]any)
	if intFromAny(ref["editorialMediaCount"]) != 3 {
		t.Fatalf("compact draftRef must note media presence: %#v", ref)
	}
	roles := stringSliceFromAny(ref["editorialMediaRoles"])
	if len(roles) != 2 {
		t.Fatalf("compact draftRef roles = %#v, want 2 deduped roles", roles)
	}
}

func TestArticleGetReturnsFeaturedImage(t *testing.T) {
	var operations []cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		if op.OperationName != "BodyArticle" {
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"article": articleWithFeaturedImageFixture()}})
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	standard, err := handleArticleGet(articleDraftTestContext(), json.RawMessage(`{"id":"https://example.com/articles/hello","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_get: %v", err)
	}
	if standard == nil || standard.IsError {
		t.Fatalf("article_get result = %+v", standard)
	}
	data := articleListStructuredData(t, standard)
	shaped, _ := data["article"].(map[string]any)
	featured, ok := shaped["featuredImage"].(map[string]any)
	if !ok || featured["url"] != "https://cdn.example.com/hero.jpg" {
		t.Fatalf("standard article must surface featuredImage: %#v", shaped["featuredImage"])
	}
	if shaped["ogImage"] != "https://cdn.example.com/og.jpg" {
		t.Fatalf("ogImage must survive: %#v", shaped["ogImage"])
	}
	if !strings.Contains(operations[0].Query, "featuredImage {") || !strings.Contains(operations[0].Query, "ogImage") {
		t.Fatalf("article query must select featuredImage and ogImage: %s", operations[0].Query)
	}

	compact, err := handleArticleGet(articleDraftTestContext(), json.RawMessage(`{"id":"https://example.com/articles/hello"}`))
	if err != nil {
		t.Fatalf("compact article_get: %v", err)
	}
	compactData := articleListStructuredData(t, compact)
	ref, _ := compactData["articleRef"].(map[string]any)
	if ref["featuredImagePresent"] != true || ref["featuredImageUrl"] != "https://cdn.example.com/hero.jpg" {
		t.Fatalf("compact articleRef must note hero presence: %#v", ref)
	}
}

func TestArticleDraftPreviewPassthroughKeepsRenderedHTML(t *testing.T) {
	rendered := `<article><figure><img src="https://cdn.example.com/hero.jpg" alt="Hero"></figure><p>Body</p></article>`
	preview := &cmsapi.DraftPreview{
		DraftID:       "draft-1",
		Success:       true,
		RenderedHTML:  &rendered,
		SourceFormat:  cmsapi.ContentFormatMarkdown,
		SourceBytes:   12,
		RenderedBytes: len(rendered),
	}
	out := standardArticleDraftPreview(preview)
	if out["renderedHtml"] != rendered {
		t.Fatalf("standard preview must pass renderedHtml through verbatim: %#v", out["renderedHtml"])
	}
}

// TestArticleDraftPreviewOptsIntoAccessURLMinting pins the lesser access-URL
// contract end-to-end at the tool surface: article_draft_preview is the ONLY
// read that opts into minting (includeAccessUrls: true on draftPreview), while
// the same handler's draftReview preflight keeps the no-minting default — so
// composed media appears in renderedHtml once lesser#1514 deploys, and no other
// read mints short-lived bearer URLs.
func TestArticleDraftPreviewOptsIntoAccessURLMinting(t *testing.T) {
	var operations []cmsapi.Operation
	rendered := `<article><figure><img src="https://cdn.example.com/hero.jpg" alt="Hero"></figure></article>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyArticleDraftReview":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draftReview": map[string]any{
				"draftId": "draft-1",
				"status":  "DRAFT",
			}}})
		case "BodyArticleDraftPreview":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draftPreview": map[string]any{
				"draftId":       "draft-1",
				"success":       true,
				"renderedHtml":  rendered,
				"sourceFormat":  "MARKDOWN",
				"sourceBytes":   12,
				"renderedBytes": len(rendered),
				"errors":        []string{},
			}}})
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

	result, err := handleArticleDraftPreview(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_preview: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_draft_preview result = %+v", result)
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %d, want 2 (review preflight + preview read)", len(operations))
	}
	if operations[0].OperationName != "BodyArticleDraftReview" || strings.Contains(operations[0].Query, "includeAccessUrls") {
		t.Fatalf("review preflight must keep the no-minting default: %s", operations[0].Query)
	}
	if operations[1].OperationName != "BodyArticleDraftPreview" || !strings.Contains(operations[1].Query, "draftPreview(id: $id, includeAccessUrls: true)") {
		t.Fatalf("preview read must opt into access-URL minting: %s", operations[1].Query)
	}
}

func stringSliceFromAny(raw any) []string {
	switch items := raw.(type) {
	case []string:
		return items
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if value, ok := item.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func TestArticleDraftGetReviewerPathReturnsBindings(t *testing.T) {
	var operations []cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"draft":null}}`))
		case "BodyArticleDraftReview":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"draftReview": map[string]any{
				"draftId":           "draft-1",
				"ownerId":           "alice",
				"title":             "Review me",
				"content":           "# Review me",
				"contentFormat":     "MARKDOWN",
				"status":            "DRAFT",
				"contentHash":       "sha256:review",
				"revision":          4,
				"activeReviewerIds": []string{"reviewer"},
				"editorialMedia":    []map[string]any{{"mediaId": "media-hero", "role": "HERO", "state": "READY"}},
			}}})
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

	result, err := handleArticleDraftGet(articleDraftTestContext(), json.RawMessage(`{"id":"draft-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("article_draft_get: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("article_draft_get result = %+v", result)
	}
	data := articleListStructuredData(t, result)
	shaped, _ := data["draft"].(map[string]any)
	media, ok := shaped["editorialMedia"].([]cmsapi.EditorialMediaUsage)
	if !ok || len(media) != 1 || media[0].MediaID != "media-hero" {
		t.Fatalf("reviewer-path draft must surface bindings: %#v", shaped["editorialMedia"])
	}
	if !strings.Contains(operations[1].Query, "editorialMedia {") {
		t.Fatalf("reviewer fallback query must select editorialMedia: %s", operations[1].Query)
	}
	if strings.Contains(operations[1].Query, "renderedHtml") {
		t.Fatalf("reviewer fallback query must not transport rendering: %s", operations[1].Query)
	}
}
