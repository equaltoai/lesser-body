package cmsapi

import (
	"context"
	"encoding/json"
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
			if op.Variables["authorId"] != "agent1" || op.Variables["after"] != "cursor-1" {
				t.Fatalf("list variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"articles":{"edges":[{"cursor":"article-cursor-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
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
	listed, err := client.ListArticles(context.Background(), "token", 2, "cursor-1", "agent1", false)
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}
	if len(listed.Edges) != 1 || listed.Edges[0].Cursor != "article-cursor-1" || listed.Edges[0].Node != nil {
		t.Fatalf("compact list should decode depth-safe cursors without nodes, got %+v", listed.Edges)
	}

	if len(operations) != 5 {
		t.Fatalf("operations = %d", len(operations))
	}
	if !strings.Contains(operations[0].Query, "publishDraft") || strings.Contains(operations[0].Query, " content ") {
		t.Fatalf("compact publish query = %s", operations[0].Query)
	}
	if !strings.Contains(operations[1].Query, "updateArticle") || !strings.Contains(operations[1].Query, " content") {
		t.Fatalf("standard update query = %s", operations[1].Query)
	}
	if !strings.Contains(operations[4].Query, "articles(authorId: $authorId") || !strings.Contains(operations[4].Query, "edges { cursor }") {
		t.Fatalf("compact list query = %s", operations[4].Query)
	}
	if strings.Contains(operations[4].Query, "node {") || strings.Contains(operations[4].Query, " content ") {
		t.Fatalf("list query must stay within Lesser agent depth-3 by avoiding edge nodes/content, got %s", operations[4].Query)
	}
}
