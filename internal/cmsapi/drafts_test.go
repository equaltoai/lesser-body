package cmsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArticleDraftOperationsBuildM0GraphQLContract(t *testing.T) {
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
			input := op.Variables["input"].(map[string]any)
			if input["contentType"] != ObjectTypeArticle || input["contentFormat"] != ContentFormatMarkdown {
				t.Fatalf("create input = %+v", input)
			}
			_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","contentFormat":"MARKDOWN","status":"DRAFT"}}}`))
		case "BodyUpdateArticleDraft":
			if op.Variables["id"] != "draft-1" {
				t.Fatalf("update variables = %+v", op.Variables)
			}
			input := op.Variables["input"].(map[string]any)
			if input["contentFormat"] != ContentFormatHTML {
				t.Fatalf("update input = %+v", input)
			}
			_, _ = w.Write([]byte(`{"data":{"updateDraft":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","content":"<p>Hello</p>","contentFormat":"HTML","status":"DRAFT"}}}`))
		case "BodyArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","content":"body","contentFormat":"MARKDOWN","status":"DRAFT"}}}`))
		case "BodyArticleDrafts":
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"node":{"id":"draft-1","contentType":"ARTICLE","title":"Hello","contentFormat":"MARKDOWN","status":"DRAFT"},"cursor":"draft-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	created, err := client.CreateArticleDraft(context.Background(), "token", CreateDraftInput{Title: stringPtr("Hello"), Content: "body"}, false)
	if err != nil {
		t.Fatalf("CreateArticleDraft: %v", err)
	}
	if created.ID != "draft-1" || created.Content != "" {
		t.Fatalf("compact create should decode draft metadata without content, got %+v", created)
	}
	updated, err := client.UpdateArticleDraft(context.Background(), "token", " draft-1 ", UpdateDraftInput{ContentFormat: stringPtr(ContentFormatHTML)}, true)
	if err != nil {
		t.Fatalf("UpdateArticleDraft: %v", err)
	}
	if updated.Content != "<p>Hello</p>" || updated.ContentFormat != ContentFormatHTML {
		t.Fatalf("standard update draft = %+v", updated)
	}
	got, err := client.GetArticleDraft(context.Background(), "token", "draft-1", true)
	if err != nil || got.Content != "body" {
		t.Fatalf("GetArticleDraft = %+v, %v", got, err)
	}
	listed, err := client.ListArticleDrafts(context.Background(), "token", 2, "cursor-1", false)
	if err != nil {
		t.Fatalf("ListArticleDrafts: %v", err)
	}
	if len(listed.Edges) != 1 || listed.Edges[0].Node.Content != "" {
		t.Fatalf("compact list = %+v", listed.Edges)
	}

	if len(operations) != 4 {
		t.Fatalf("operations = %d", len(operations))
	}
	if !strings.Contains(operations[0].Query, "createDraft") || strings.Contains(operations[0].Query, " content ") {
		t.Fatalf("compact create query = %s", operations[0].Query)
	}
	if !strings.Contains(operations[1].Query, "updateDraft") || !strings.Contains(operations[1].Query, " content") {
		t.Fatalf("standard update query = %s", operations[1].Query)
	}
	if !strings.Contains(operations[3].Query, "myDrafts(contentType: ARTICLE, status: DRAFT") || strings.Contains(operations[3].Query, " content ") {
		t.Fatalf("compact list query = %s", operations[3].Query)
	}
}

func stringPtr(value string) *string { return &value }
