package cmsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Schema-validated selection-set conformance for the draft/article queries
// (issue #593). gqlgen validates selection sets against lesser's GraphQL
// schema PRE-RESOLVER: selecting a field that does not exist on the operation's
// return type makes the whole operation fail with HTTP 422 before any resolver
// runs. The mock-based tool tests cannot catch this because their fixtures echo
// whatever fields the client selects — so the selection sets built by
// draftFields/articleFields are validated here against a pinned excerpt of the
// real lesser schema (testdata/lesser_draft_article_schema.graphql) with the
// same field-existence resolver media_schema_test.go uses. A mock fixture can
// no longer hide an invalid selection set.
//
// The operations are captured from a live mock server running the real client
// methods, so the validation covers the exact documents that go over the wire —
// including the draftFields/articleFields additions for editorialMedia and
// featuredImage. The aliased list-hydration document (draft0: draft(id: $id0))
// cannot be parsed by the shared GraphQL mini-parser (it does not model
// aliases), so that operation is validated by selection-substring assertions
// against the same field builders the other draft operations use.

// loadDraftArticleSchemaFixture parses the pinned lesser draft/article schema
// excerpt.
func loadDraftArticleSchemaFixture(t *testing.T) *sdlSchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "lesser_draft_article_schema.graphql"))
	if err != nil {
		t.Fatalf("read draft/article schema fixture: %v", err)
	}
	tokens, err := tokenizeGraphQL(string(raw))
	if err != nil {
		t.Fatalf("tokenize draft/article schema fixture: %v", err)
	}
	schema, err := parseSDL(tokens)
	if err != nil {
		t.Fatalf("parse draft/article schema fixture: %v", err)
	}
	return schema
}

// TestDraftArticleSelectionSetsValidateAgainstLesserSchema drives the real
// cmsapi client against a mock server, captures every GraphQL document it
// sends, and validates each against the pinned lesser schema. It also asserts
// the new selections are present in the exact wire documents and that
// editorial-media bindings and the featured image decode through the client.
func TestDraftArticleSelectionSetsValidateAgainstLesserSchema(t *testing.T) {
	const (
		draftJSON   = `{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","title":"Hello","slug":"hello","contentFormat":"MARKDOWN","status":"DRAFT","contentHash":"sha256:draft","revision":3,"lastSavedAt":"2026-08-29T00:00:00Z","createdAt":"2026-08-29T00:00:00Z","updatedAt":"2026-08-29T00:01:00Z","editorialMedia":[{"mediaId":"media-hero","role":"HERO","state":"READY","publishedUrl":"https://cdn.example.com/hero.jpg","caption":"Hero"},{"mediaId":"media-inline","role":"INLINE","inlinePosition":2,"state":"READY"}]}`
		articleJSON = `{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"ogImage":"https://cdn.example.com/og.jpg","featuredImage":{"id":"media-hero","type":"IMAGE","url":"https://cdn.example.com/hero.jpg","description":"Hero image","width":1200,"height":630,"mimeType":"image/jpeg"},"publishedAt":"2026-08-29T00:00:00Z","createdAt":"2026-08-29T00:00:00Z","updatedAt":"2026-08-29T00:00:00Z"}`
		reviewJSON  = `{"data":{"draftReview":{"draftId":"draft-1","ownerId":"alice","title":"Hello","content":"body","contentFormat":"MARKDOWN","status":"DRAFT","contentHash":"sha256:draft","revision":3,"activeReviewerIds":[],"publishBlockingReasons":[],"grants":[],"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":[],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false},"editorialMedia":[{"mediaId":"media-hero","role":"HERO","state":"READY"}]}}}`
	)

	var operations []Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyCreateArticleDraft":
			_, _ = fmt.Fprintf(w, `{"data":{"createDraft":%s}}`, draftJSON)
		case "BodyUpdateArticleDraft":
			_, _ = fmt.Fprintf(w, `{"data":{"updateDraft":%s}}`, draftJSON)
		case "BodyArticleDraft":
			_, _ = fmt.Fprintf(w, `{"data":{"draft":%s}}`, draftJSON)
		case "BodyArticleDrafts":
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"cursor":"draft-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		case "BodyArticleDraftListDetails":
			_, _ = fmt.Fprintf(w, `{"data":{"draft0":%s}}`, draftJSON)
		case "BodyArticle":
			_, _ = fmt.Fprintf(w, `{"data":{"article":%s}}`, articleJSON)
		case "BodyArticleBySlug":
			_, _ = fmt.Fprintf(w, `{"data":{"articleBySlug":%s}}`, articleJSON)
		case "BodyArticles":
			_, _ = fmt.Fprintf(w, `{"data":{"articles":{"edges":[{"node":%s,"cursor":"article-cursor-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`, articleJSON)
		case "BodyPublishArticleDraft":
			_, _ = fmt.Fprintf(w, `{"data":{"publishDraft":%s}}`, articleJSON)
		case "BodyUpdateArticle":
			_, _ = fmt.Fprintf(w, `{"data":{"updateArticle":%s}}`, articleJSON)
		case "BodyArticleDraftReview":
			_, _ = w.Write([]byte(reviewJSON))
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	ctx := context.Background()
	const token = "test-token"

	title := stringPtr("Hello")
	if _, err := client.CreateArticleDraft(ctx, token, CreateDraftInput{Title: title, Content: "body"}, true); err != nil {
		t.Fatalf("CreateArticleDraft: %v", err)
	}
	if _, err := client.UpdateArticleDraft(ctx, token, "draft-1", UpdateDraftInput{Title: title}, false); err != nil {
		t.Fatalf("UpdateArticleDraft: %v", err)
	}
	draft, err := client.GetArticleDraft(ctx, token, "draft-1", true)
	if err != nil {
		t.Fatalf("GetArticleDraft: %v", err)
	}
	if len(draft.EditorialMedia) != 2 || draft.EditorialMedia[0].MediaID != "media-hero" || draft.EditorialMedia[0].Role != "HERO" || draft.EditorialMedia[1].Role != "INLINE" {
		t.Fatalf("draft editorial media bindings did not decode: %+v", draft.EditorialMedia)
	}
	if _, err := client.ListArticleDrafts(ctx, token, 5, "", false); err != nil {
		t.Fatalf("ListArticleDrafts: %v", err)
	}
	article, err := client.GetArticle(ctx, token, "https://example.com/articles/hello", true)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if article.FeaturedImage == nil || article.FeaturedImage.URL != "https://cdn.example.com/hero.jpg" || article.FeaturedImage.Type != "IMAGE" || article.FeaturedImage.Width == nil || *article.FeaturedImage.Width != 1200 {
		t.Fatalf("article featured image did not decode: %+v", article.FeaturedImage)
	}
	if article.OGImage == nil || *article.OGImage != "https://cdn.example.com/og.jpg" {
		t.Fatalf("ogImage should survive alongside featuredImage: %+v", article.OGImage)
	}
	if _, err := client.GetArticleBySlug(ctx, token, "hello", false); err != nil {
		t.Fatalf("GetArticleBySlug: %v", err)
	}
	if _, err := client.ListArticles(ctx, token, 5, "", "alice", true); err != nil {
		t.Fatalf("ListArticles: %v", err)
	}
	if _, err := client.PublishArticleDraft(ctx, token, "draft-1", false); err != nil {
		t.Fatalf("PublishArticleDraft: %v", err)
	}
	if _, err := client.UpdateArticle(ctx, token, "https://example.com/articles/hello", UpdateArticleInput{Title: title}, true); err != nil {
		t.Fatalf("UpdateArticle: %v", err)
	}
	review, err := client.ReadArticleDraftReviewSource(ctx, token, "draft-1")
	if err != nil {
		t.Fatalf("ReadArticleDraftReviewSource: %v", err)
	}
	if len(review.EditorialMedia) != 1 || review.EditorialMedia[0].MediaID != "media-hero" {
		t.Fatalf("reviewer source projection bindings did not decode: %+v", review.EditorialMedia)
	}

	if len(operations) != 11 {
		t.Fatalf("operations = %d, want 11", len(operations))
	}

	schema := loadDraftArticleSchemaFixture(t)
	for _, op := range operations {
		if op.OperationName == "BodyArticleDraftListDetails" {
			// The shared GraphQL mini-parser does not model aliases
			// (draft0: draft(id: $id0)); assert the selection by substring
			// against the same field builder the other draft operations use.
			if !strings.Contains(op.Query, "draft0: draft(id: $id0)") || !strings.Contains(op.Query, "editorialMedia {") {
				t.Fatalf("hydration query missing depth-safe alias or editorialMedia selection: %s", op.Query)
			}
			continue
		}
		doc, err := parseGraphQLDocument(op.Query)
		if err != nil {
			t.Fatalf("%s: parse query: %v\n%s", op.OperationName, err, op.Query)
		}
		if errs := validateDocument(doc, schema); len(errs) > 0 {
			t.Fatalf("%s query is schema-invalid (gqlgen would reject it with HTTP 422 pre-resolver):\n%s\n%s",
				op.OperationName, op.Query, strings.Join(errs, "\n"))
		}
	}

	for _, op := range operations {
		switch op.OperationName {
		case "BodyCreateArticleDraft", "BodyUpdateArticleDraft", "BodyArticleDraft", "BodyArticleDraftListDetails", "BodyArticleDraftReview":
			if !strings.Contains(op.Query, "editorialMedia {") {
				t.Fatalf("%s query missing editorialMedia selection: %s", op.OperationName, op.Query)
			}
			for _, want := range []string{"mediaId", "role", "state", "publishedUrl", "provenance {", "responsibleActor { id username }"} {
				if !strings.Contains(op.Query, want) {
					t.Fatalf("%s editorialMedia selection missing usage field %q: %s", op.OperationName, want, op.Query)
				}
			}
		case "BodyArticle", "BodyArticleBySlug", "BodyArticles", "BodyPublishArticleDraft", "BodyUpdateArticle":
			if !strings.Contains(op.Query, "featuredImage {") || !strings.Contains(op.Query, "ogImage") {
				t.Fatalf("%s query missing featuredImage (keeping ogImage): %s", op.OperationName, op.Query)
			}
			for _, want := range []string{"id", "type", "url", "description", "width", "height", "mimeType"} {
				if !strings.Contains(op.Query, want) {
					t.Fatalf("%s featuredImage selection missing field %q: %s", op.OperationName, want, op.Query)
				}
			}
		}
	}
}

// TestDraftArticleSchemaFixtureIsInternallyConsistent guards the fixture
// itself: every field return type must resolve to a defined type, and the
// draft/article root fields and projection types must exist. A silently
// permissive fixture would defeat the conformance guard.
func TestDraftArticleSchemaFixtureIsInternallyConsistent(t *testing.T) {
	schema := loadDraftArticleSchemaFixture(t)
	for typeName, fields := range schema.types {
		for fname, ftype := range fields {
			if !knownType(ftype, schema) {
				t.Errorf("fixture type %s field %s references undefined type %q", typeName, fname, ftype)
			}
		}
	}
	for _, want := range []string{"Draft", "Article", "Media", "DraftReview", "DraftConnection", "ArticleConnection", "Query", "Mutation", "EditorialMediaUsage"} {
		if schema.types[want] == nil {
			t.Errorf("fixture is missing type %q", want)
		}
	}
	for _, want := range []string{"draft", "myDrafts", "draftReview", "article", "articleBySlug", "articles"} {
		if schema.types["Query"][want] == "" {
			t.Errorf("fixture Query is missing root field %q", want)
		}
	}
	for _, want := range []string{"createDraft", "updateDraft", "publishDraft", "updateArticle"} {
		if schema.types["Mutation"][want] == "" {
			t.Errorf("fixture Mutation is missing root field %q", want)
		}
	}
	if schema.types["Draft"]["editorialMedia"] == "" {
		t.Errorf("fixture Draft is missing editorialMedia")
	}
	if schema.types["Article"]["featuredImage"] == "" {
		t.Errorf("fixture Article is missing featuredImage")
	}
}

// TestDraftArticleSchemaValidatorRejectsUnknownFields pins that the validator
// and fixture still model gqlgen's real pre-resolver existence check: selecting
// a field that does not exist on the resolved type is rejected. If this test
// ever stops failing, the fixture or the validator no longer models the real
// schema.
func TestDraftArticleSchemaValidatorRejectsUnknownFields(t *testing.T) {
	schema := loadDraftArticleSchemaFixture(t)
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "unknown field on Draft",
			query: `query { draft(id: "1") { bogusField } }`,
			want:  `Field "bogusField" doesn't exist on type "Draft"`,
		},
		{
			name:  "unknown field on EditorialMediaUsage",
			query: `query { draft(id: "1") { editorialMedia { bogus } } }`,
			want:  `Field "bogus" doesn't exist on type "EditorialMediaUsage"`,
		},
		{
			name:  "unknown field on Article",
			query: `query { article(id: "1") { bogusField } }`,
			want:  `Field "bogusField" doesn't exist on type "Article"`,
		},
		{
			name:  "unknown field on Media",
			query: `query { article(id: "1") { featuredImage { bogus } } }`,
			want:  `Field "bogus" doesn't exist on type "Media"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parseGraphQLDocument(tc.query)
			if err != nil {
				t.Fatalf("parse query: %v", err)
			}
			errs := validateDocument(doc, schema)
			if len(errs) == 0 {
				t.Fatalf("query validated cleanly — the schema conformance guard no longer models the real schema: %s", tc.query)
			}
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("expected rejection %q, got:\n%s", tc.want, strings.Join(errs, "\n"))
			}
		})
	}
}
