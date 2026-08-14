package cmsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishedArticleOperationsBuildM1GraphQLContract(t *testing.T) {
	var operations []Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyPublishArticleDraft":
			if op.Variables["id"] != "draft-1" {
				t.Fatalf("publish variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"publishDraft":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
		case "BodyUpdateArticle":
			if op.Variables["id"] != "https://example.com/articles/hello" {
				t.Fatalf("update variables = %+v", op.Variables)
			}
			input := op.Variables["input"].(map[string]any)
			if input["contentFormat"] != ContentFormatHTML || input["title"] != "Updated" {
				t.Fatalf("update input = %+v", input)
			}
			_, _ = w.Write([]byte(`{"data":{"updateArticle":{"id":"https://example.com/articles/hello","slug":"hello","title":"Updated","content":"<p>Hello</p>","contentFormat":"HTML","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:01:00Z"}}}`))
		case "BodyArticle":
			if op.Variables["id"] != "https://example.com/articles/hello" {
				t.Fatalf("get variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"article":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","content":"body","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
		case "BodyArticleBySlug":
			if op.Variables["slug"] != "hello" {
				t.Fatalf("get by slug variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"articleBySlug":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}}`))
		case "BodyArticles":
			if op.Variables["authorId"] != "agent1" {
				t.Fatalf("list variables = %+v", op.Variables)
			}
			switch op.Variables["after"] {
			case "cursor-1":
				if op.Variables["first"] != float64(2) {
					t.Fatalf("compact list first = %+v", op.Variables["first"])
				}
				_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[{"cursor":"article-cursor-2","node":{"id":"https://example.com/articles/hello","slug":"hello","title":"Hello","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:00:00Z","createdAt":"2026-05-19T23:00:00Z","updatedAt":"2026-05-19T23:00:00Z"}}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"startCursor":"article-cursor-2","endCursor":"article-cursor-2"},"totalCount":2}}}`))
			case "cursor-2":
				if op.Variables["first"] != float64(3) {
					t.Fatalf("standard list first = %+v", op.Variables["first"])
				}
				_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[{"cursor":"article-cursor-3","node":{"id":"https://example.com/articles/second","slug":"second","title":"Second","content":"body","contentFormat":"MARKDOWN","readingTimeMinutes":1,"wordCount":10,"publishedAt":"2026-05-19T23:01:00Z","createdAt":"2026-05-19T23:01:00Z","updatedAt":"2026-05-19T23:01:00Z"}}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":true,"startCursor":"article-cursor-3","endCursor":"article-cursor-3"},"totalCount":2}}}`))
			default:
				t.Fatalf("list after variable = %+v", op.Variables)
			}
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	published, err := client.PublishArticleDraft(context.Background(), "token", " draft-1 ", false)
	if err != nil {
		t.Fatalf("PublishArticleDraft: %v", err)
	}
	if published.ID != "https://example.com/articles/hello" || published.Content != "" {
		t.Fatalf("compact publish should return canonical id without content, got %+v", published)
	}
	updated, err := client.UpdateArticle(context.Background(), "token", "https://example.com/articles/hello", UpdateArticleInput{
		Title:         stringPtr("Updated"),
		ContentFormat: stringPtr(ContentFormatHTML),
	}, true)
	if err != nil {
		t.Fatalf("UpdateArticle: %v", err)
	}
	if updated.Content != "<p>Hello</p>" || updated.ContentFormat != ContentFormatHTML {
		t.Fatalf("standard update article = %+v", updated)
	}
	got, err := client.GetArticle(context.Background(), "token", "https://example.com/articles/hello", true)
	if err != nil || got.Content != "body" {
		t.Fatalf("GetArticle = %+v, %v", got, err)
	}
	gotBySlug, err := client.GetArticleBySlug(context.Background(), "token", "hello", false)
	if err != nil || gotBySlug.ID != "https://example.com/articles/hello" {
		t.Fatalf("GetArticleBySlug = %+v, %v", gotBySlug, err)
	}
	compactList, err := client.ListArticles(context.Background(), "token", 2, "cursor-1", "agent1", false)
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}
	if len(compactList.Edges) != 1 || compactList.Edges[0].Cursor != "article-cursor-2" || compactList.Edges[0].Node == nil {
		t.Fatalf("compact list should decode populated edge nodes, got %+v", compactList.Edges)
	}
	if compactList.Edges[0].Node.ID != "https://example.com/articles/hello" || compactList.Edges[0].Node.Content != "" {
		t.Fatalf("compact list node = %+v", compactList.Edges[0].Node)
	}
	if !compactList.PageInfo.HasNextPage || compactList.PageInfo.EndCursor == nil || *compactList.PageInfo.EndCursor != "article-cursor-2" || compactList.TotalCount != 2 {
		t.Fatalf("compact list pagination = %+v, totalCount=%d", compactList.PageInfo, compactList.TotalCount)
	}

	standardList, err := client.ListArticles(context.Background(), "token", 3, "cursor-2", "agent1", true)
	if err != nil {
		t.Fatalf("ListArticles with content: %v", err)
	}
	if len(standardList.Edges) != 1 || standardList.Edges[0].Node == nil || standardList.Edges[0].Node.Content != "body" {
		t.Fatalf("standard list should decode node content, got %+v", standardList.Edges)
	}
	if standardList.PageInfo.HasNextPage || !standardList.PageInfo.HasPreviousPage || standardList.PageInfo.StartCursor == nil || *standardList.PageInfo.StartCursor != "article-cursor-3" {
		t.Fatalf("standard list pagination = %+v", standardList.PageInfo)
	}

	if len(operations) != 6 {
		t.Fatalf("operations = %d", len(operations))
	}
	if !strings.Contains(operations[0].Query, "publishDraft") || strings.Contains(operations[0].Query, " content ") {
		t.Fatalf("compact publish query = %s", operations[0].Query)
	}
	if !strings.Contains(operations[1].Query, "updateArticle") || !strings.Contains(operations[1].Query, " content") {
		t.Fatalf("standard update query = %s", operations[1].Query)
	}
	if !strings.Contains(operations[4].Query, "articles(authorId: $authorId") || !strings.Contains(operations[4].Query, "edges { cursor node {") {
		t.Fatalf("compact list query = %s", operations[4].Query)
	}
	if strings.Contains(operations[4].Query, " content ") {
		t.Fatalf("compact list query must omit content, got %s", operations[4].Query)
	}
	if !strings.Contains(operations[5].Query, "edges { cursor node {") || !strings.Contains(operations[5].Query, " content ") {
		t.Fatalf("standard list query must select node content, got %s", operations[5].Query)
	}
}

func TestGetArticleMissingReturnsArticleNotFoundErrorForIDAndURL(t *testing.T) {
	for _, tc := range []struct {
		name       string
		locator    string
		wantLookup string
	}{
		{name: "id", locator: "article-123", wantLookup: "id"},
		{name: "canonical url in id field", locator: "https://example.com/articles/missing", wantLookup: "id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"article":null}}`))
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			got, err := client.GetArticle(context.Background(), "token", tc.locator, true)
			if got != nil {
				t.Fatalf("GetArticle = %+v, want nil on missing article", got)
			}

			var notFound *ArticleNotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("GetArticle error = %T %v, want *ArticleNotFoundError", err, err)
			}
			if notFound.Lookup != tc.wantLookup || notFound.Value != tc.locator {
				t.Fatalf("ArticleNotFoundError = %+v, want lookup=%q value=%q", notFound, tc.wantLookup, tc.locator)
			}
		})
	}
}

func TestGetArticleBySlugMissingReturnsArticleNotFoundError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"articleBySlug":null}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	got, err := client.GetArticleBySlug(context.Background(), "token", "missing-slug", true)
	if got != nil {
		t.Fatalf("GetArticleBySlug = %+v, want nil on missing article", got)
	}

	var notFound *ArticleNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetArticleBySlug error = %T %v, want *ArticleNotFoundError", err, err)
	}
	if notFound.Lookup != "slug" || notFound.Value != "missing-slug" {
		t.Fatalf("ArticleNotFoundError = %+v, want lookup=%q value=%q", notFound, "slug", "missing-slug")
	}
}
