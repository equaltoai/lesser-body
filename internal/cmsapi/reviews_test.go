package cmsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArticleDraftReviewOperationsBuildM2aGraphQLContract(t *testing.T) {
	var operations []Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodySubmitArticleDraftForReview":
			if op.Variables["draftId"] != "draft-1" || op.Variables["reviewer"] != "reviewer" {
				t.Fatalf("share variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"shareDraftForReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","grant":{"grantedAt":"2026-07-31T12:00:00Z"},"verdicts":[]}}}`))
		case "BodyArticleDraftReview":
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","verdicts":null}}}`))
		case "BodyArticleDraftReviewQueue":
			_, _ = w.Write([]byte(`{"data":{"sharedDraftReviews":{"edges":[{"node":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","verdicts":[]},"cursor":"queue-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		case "BodySubmitArticleDraftReviewVerdict":
			if op.Variables["verdict"] != DraftReviewVerdictChangesRequested || op.Variables["notes"] != "revise intro" {
				t.Fatalf("verdict variables = %+v", op.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"submitDraftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewedBy":{"id":"https://example.com/users/reviewer","username":"reviewer"},"reviewStatus":"CHANGES_REQUESTED","editorNotes":"revise intro","verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"revise intro","recordedAt":"2026-07-31T12:01:00Z"}]}}}`))
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	shared, err := client.SubmitArticleDraftForReview(context.Background(), "token", " draft-1 ", " reviewer ")
	if err != nil || shared.Grant == nil || shared.Grant.GrantedAt == "" || shared.Verdicts == nil {
		t.Fatalf("SubmitArticleDraftForReview = %+v, %v", shared, err)
	}
	state, err := client.ReadArticleDraftReview(context.Background(), "token", "draft-1")
	if err != nil || state.DraftID != "draft-1" || state.Verdicts == nil || len(state.Verdicts) != 0 {
		t.Fatalf("ReadArticleDraftReview = %+v, %v", state, err)
	}
	queue, err := client.ListArticleDraftReviews(context.Background(), "token", 10, " queue-cursor ")
	if err != nil || len(queue.Edges) != 1 || queue.Edges[0].Node == nil || queue.Edges[0].Node.DraftID != "draft-1" {
		t.Fatalf("ListArticleDraftReviews = %+v, %v", queue, err)
	}
	notes := " revise intro "
	verdict, err := client.SubmitArticleDraftReviewVerdict(context.Background(), "token", "draft-1", "changes_requested", &notes)
	if err != nil || verdict.ReviewStatus == nil || *verdict.ReviewStatus != DraftReviewVerdictChangesRequested || verdict.ReviewedBy == nil || verdict.ReviewedBy.Username != "reviewer" {
		t.Fatalf("SubmitArticleDraftReviewVerdict = %+v, %v", verdict, err)
	}

	if len(operations) != 4 {
		t.Fatalf("operations = %d", len(operations))
	}
	for _, op := range operations {
		for _, want := range []string{"draftId", "generatedBy { id username }", "reviewedBy { id username }", "reviewStatus", "editorNotes", "grant { grantedAt }", "verdicts { verdict notes recordedAt }"} {
			if !strings.Contains(op.Query, want) {
				t.Fatalf("%s query missing %q: %s", op.OperationName, want, op.Query)
			}
		}
		if strings.Contains(op.Query, "grant { reviewer") || strings.Contains(op.Query, "verdicts { reviewer") {
			t.Fatalf("%s exceeds Lesser's agent depth-3 projection: %s", op.OperationName, op.Query)
		}
	}
	if !strings.Contains(operations[2].Query, "sharedDraftReviews(first: $first, after: $after)") || !strings.Contains(operations[2].Query, "edges { node {") {
		t.Fatalf("queue query = %s", operations[2].Query)
	}
}

func TestArticleDraftReviewClientValidatesInputsBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	if _, err := client.SubmitArticleDraftForReview(context.Background(), "token", "", "reviewer"); err == nil {
		t.Fatal("expected missing draft id rejection")
	}
	if _, err := client.SubmitArticleDraftForReview(context.Background(), "token", "draft-1", ""); err == nil {
		t.Fatal("expected missing reviewer rejection")
	}
	if _, err := client.ReadArticleDraftReview(context.Background(), "token", " "); err == nil {
		t.Fatal("expected missing state draft id rejection")
	}
	if _, err := client.SubmitArticleDraftReviewVerdict(context.Background(), "token", "draft-1", "MAYBE", nil); err == nil {
		t.Fatal("expected invalid verdict rejection")
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}
