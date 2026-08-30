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
			_, _ = w.Write([]byte(`{"data":{"createDraft":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","title":"Hello","contentFormat":"MARKDOWN","status":"DRAFT","contentHash":"sha256:create","revision":1}}}`))
		case "BodyUpdateArticleDraft":
			if op.Variables["id"] != "draft-1" {
				t.Fatalf("update variables = %+v", op.Variables)
			}
			input := op.Variables["input"].(map[string]any)
			if input["contentFormat"] != ContentFormatHTML {
				t.Fatalf("update input = %+v", input)
			}
			_, _ = w.Write([]byte(`{"data":{"updateDraft":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","title":"Hello","content":"<p>Hello</p>","contentFormat":"HTML","status":"DRAFT","contentHash":"sha256:update","revision":2}}}`))
		case "BodyArticleDraft":
			_, _ = w.Write([]byte(`{"data":{"draft":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","title":"Hello","content":"body","contentFormat":"MARKDOWN","status":"DRAFT","contentHash":"sha256:get","revision":3}}}`))
		case "BodyArticleDrafts":
			_, _ = w.Write([]byte(`{"data":{"myDrafts":{"edges":[{"cursor":"draft-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		case "BodyArticleDraftListDetails":
			if !strings.Contains(op.Query, "draft0: draft(id: $id0)") || strings.Contains(op.Query, " content ") {
				t.Fatalf("compact hydration query = %s", op.Query)
			}
			if op.Variables["id0"] != "draft-1" {
				t.Fatalf("hydration variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"draft0":{"id":"draft-1","author":{"id":"https://example.com/users/alice","username":"alice"},"contentType":"ARTICLE","title":"Hello","contentFormat":"MARKDOWN","status":"DRAFT","contentHash":"sha256:list","revision":4,"lastSavedAt":"2026-05-20T00:01:00Z","createdAt":"2026-05-20T00:00:00Z","updatedAt":"2026-05-20T00:01:00Z"}}}`))
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
	if created.ID != "draft-1" || created.Content != "" || created.Author == nil || created.Author.Username != "alice" || created.ContentHash != "sha256:create" || created.Revision != 1 {
		t.Fatalf("compact create should decode draft metadata without content, got %+v", created)
	}
	updated, err := client.UpdateArticleDraft(context.Background(), "token", " draft-1 ", UpdateDraftInput{ContentFormat: stringPtr(ContentFormatHTML)}, true)
	if err != nil {
		t.Fatalf("UpdateArticleDraft: %v", err)
	}
	if updated.Content != "<p>Hello</p>" || updated.ContentFormat != ContentFormatHTML || updated.ContentHash != "sha256:update" || updated.Revision != 2 {
		t.Fatalf("standard update draft = %+v", updated)
	}
	got, err := client.GetArticleDraft(context.Background(), "token", "draft-1", true)
	if err != nil || got.Content != "body" || got.ContentHash != "sha256:get" || got.Revision != 3 {
		t.Fatalf("GetArticleDraft = %+v, %v", got, err)
	}
	listed, err := client.ListArticleDrafts(context.Background(), "token", 2, "cursor-1", false)
	if err != nil {
		t.Fatalf("ListArticleDrafts: %v", err)
	}
	if len(listed.Edges) != 1 || listed.Edges[0].Cursor != "draft-1" || listed.Edges[0].Node == nil || listed.Edges[0].Node.Title == nil || *listed.Edges[0].Node.Title != "Hello" || listed.Edges[0].Node.UpdatedAt == "" || listed.Edges[0].Node.ContentHash != "sha256:list" || listed.Edges[0].Node.Revision != 4 {
		t.Fatalf("compact list should hydrate depth-safe triage metadata, got %+v", listed.Edges)
	}

	if len(operations) != 5 {
		t.Fatalf("operations = %d", len(operations))
	}
	if !strings.Contains(operations[0].Query, "createDraft") || strings.Contains(operations[0].Query, " content ") {
		t.Fatalf("compact create query = %s", operations[0].Query)
	}
	if strings.Contains(operations[0].Query, "authorId") || !strings.Contains(operations[0].Query, "author { id username }") {
		t.Fatalf("compact create query must consume Lesser Draft.author, got %s", operations[0].Query)
	}
	if !strings.Contains(operations[1].Query, "updateDraft") || !strings.Contains(operations[1].Query, " content") {
		t.Fatalf("standard update query = %s", operations[1].Query)
	}
	if !strings.Contains(operations[3].Query, "myDrafts(contentType: ARTICLE, status: DRAFT") || !strings.Contains(operations[3].Query, "edges { cursor }") {
		t.Fatalf("compact list query = %s", operations[3].Query)
	}
	if strings.Contains(operations[3].Query, "node {") || strings.Contains(operations[3].Query, "authorId") || strings.Contains(operations[3].Query, " content ") {
		t.Fatalf("list query must stay within Lesser agent depth-3 and avoid legacy authorId, got %s", operations[3].Query)
	}
	if !strings.Contains(operations[4].Query, "draft0: draft(id: $id0)") || strings.Contains(operations[4].Query, " content ") {
		t.Fatalf("list hydration must stay depth-safe and omit compact content, got %s", operations[4].Query)
	}
	for _, op := range operations {
		if op.OperationName == "BodyArticleDrafts" {
			continue
		}
		for _, want := range []string{"contentHash", "revision"} {
			if !strings.Contains(op.Query, want) {
				t.Fatalf("%s query missing Lesser v1.6.4 field %q: %s", op.OperationName, want, op.Query)
			}
		}
	}
}

func TestArticleDraftPreviewOperationBuildsM45GraphQLContract(t *testing.T) {
	var operations []Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		if op.OperationName != "BodyArticleDraftPreview" {
			t.Fatalf("operationName = %q", op.OperationName)
		}
		if op.Variables["id"] != "draft-1" {
			t.Fatalf("variables = %+v", op.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"draftPreview":{"draftId":"draft-1","success":true,"renderedHtml":"<h1>Hello</h1>","sourceFormat":"MARKDOWN","sourceBytes":12,"renderedBytes":14,"errors":[]}}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	preview, err := client.PreviewArticleDraft(context.Background(), "token", " draft-1 ")
	if err != nil {
		t.Fatalf("PreviewArticleDraft: %v", err)
	}
	if preview.DraftID != "draft-1" || !preview.Success || preview.RenderedHTML == nil || *preview.RenderedHTML != "<h1>Hello</h1>" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.SourceFormat != ContentFormatMarkdown || preview.SourceBytes != 12 || preview.RenderedBytes != 14 || len(preview.Errors) != 0 {
		t.Fatalf("preview metadata = %+v", preview)
	}
	if len(operations) != 1 {
		t.Fatalf("operations = %d", len(operations))
	}
	query := operations[0].Query
	for _, want := range []string{"draftPreview(id: $id, includeAccessUrls: true)", "draftId", "success", "renderedHtml", "sourceFormat", "sourceBytes", "renderedBytes", "errors"} {
		if !strings.Contains(query, want) {
			t.Fatalf("preview query missing %q: %s", want, query)
		}
	}
	if strings.Contains(query, "draft(id:") {
		t.Fatalf("preview query must use draftPreview contract, got %s", query)
	}
}

func TestGetArticleDraftMissingReturnsDraftNotFoundError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"draft":null}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	got, err := client.GetArticleDraft(context.Background(), "token", "missing-draft", true)
	if got != nil {
		t.Fatalf("GetArticleDraft = %+v, want nil on missing draft", got)
	}

	var notFound *DraftNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("GetArticleDraft error = %T %v, want *DraftNotFoundError", err, err)
	}
	if notFound.Lookup != "id" || notFound.Value != "missing-draft" {
		t.Fatalf("DraftNotFoundError = %+v, want lookup=%q value=%q", notFound, "id", "missing-draft")
	}
}

func stringPtr(value string) *string { return &value }
