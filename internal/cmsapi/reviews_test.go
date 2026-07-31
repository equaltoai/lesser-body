package cmsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArticleDraftReviewOperationsBuildM2aGraphQLContract(t *testing.T) {
	const callerDraftID = "caller-draft-id-must-stay-in-variables"
	const callerNotes = "caller notes must stay in GraphQL variables"
	var operations []Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusInternalServerError)
			return
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodySubmitArticleDraftForReview":
			if op.Variables["draftId"] != callerDraftID || op.Variables["reviewer"] != "reviewer" {
				t.Errorf("share variables = %+v", op.Variables)
				http.Error(w, "unexpected share variables", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"shareDraftForReview":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","grant":{"grantedAt":"2026-07-31T12:00:00Z"},"verdicts":[]}}}`))
		case "BodyArticleDraftReview":
			if op.Variables["id"] != callerDraftID {
				t.Errorf("state variables = %+v", op.Variables)
				http.Error(w, "unexpected state variables", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewStatus":"APPROVED","verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"lesser state remains authoritative","recordedAt":"2026-07-31T11:59:00Z"}]}}}`))
		case "BodyArticleDraftReviewQueue":
			_, _ = w.Write([]byte(`{"data":{"sharedDraftReviews":{"edges":[{"node":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","verdicts":[]},"cursor":"queue-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
		case "BodySubmitArticleDraftReviewVerdict":
			if op.Variables["draftId"] != callerDraftID || op.Variables["verdict"] != DraftReviewVerdictChangesRequested || op.Variables["notes"] != callerNotes {
				t.Errorf("verdict variables = %+v", op.Variables)
				http.Error(w, "unexpected verdict variables", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"submitDraftReview":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewedBy":{"id":"https://example.com/users/reviewer","username":"reviewer"},"reviewStatus":"CHANGES_REQUESTED","editorNotes":"caller notes must stay in GraphQL variables","verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"caller notes must stay in GraphQL variables","recordedAt":"2026-07-31T12:01:00Z"}]}}}`))
		default:
			t.Errorf("unexpected operation %q", op.OperationName)
			http.Error(w, "unexpected operation", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	shared, err := client.SubmitArticleDraftForReview(context.Background(), "token", " "+callerDraftID+" ", " reviewer ")
	if err != nil || shared.Grant == nil || shared.Grant.GrantedAt == "" || shared.Verdicts == nil {
		t.Fatalf("SubmitArticleDraftForReview = %+v, %v", shared, err)
	}
	state, err := client.ReadArticleDraftReview(context.Background(), "token", callerDraftID)
	if err != nil || state.DraftID != callerDraftID || state.Verdicts == nil || len(state.Verdicts) != 1 {
		t.Fatalf("ReadArticleDraftReview = %+v, %v", state, err)
	}
	wantStatus := []byte(`"APPROVED"`)
	wantVerdicts := []byte(`[{"verdict":"CHANGES_REQUESTED","notes":"lesser state remains authoritative","recordedAt":"2026-07-31T11:59:00Z"}]`)
	gotStatus, err := json.Marshal(state.ReviewStatus)
	if err != nil {
		t.Fatalf("marshal reviewStatus: %v", err)
	}
	gotVerdicts, err := json.Marshal(state.Verdicts)
	if err != nil {
		t.Fatalf("marshal verdicts: %v", err)
	}
	if !bytes.Equal(gotStatus, wantStatus) || !bytes.Equal(gotVerdicts, wantVerdicts) {
		t.Fatalf("Lesser review state changed in transit: reviewStatus=%s verdicts=%s", gotStatus, gotVerdicts)
	}
	queue, err := client.ListArticleDraftReviews(context.Background(), "token", 10, " queue-cursor ")
	if err != nil || len(queue.Edges) != 1 || queue.Edges[0].Node == nil || queue.Edges[0].Node.DraftID != callerDraftID {
		t.Fatalf("ListArticleDraftReviews = %+v, %v", queue, err)
	}
	notes := " " + callerNotes + " "
	verdict, err := client.SubmitArticleDraftReviewVerdict(context.Background(), "token", callerDraftID, "changes_requested", &notes)
	if err != nil || verdict.ReviewStatus == nil || *verdict.ReviewStatus != DraftReviewVerdictChangesRequested || verdict.ReviewedBy == nil || verdict.ReviewedBy.Username != "reviewer" {
		t.Fatalf("SubmitArticleDraftReviewVerdict = %+v, %v", verdict, err)
	}

	if len(operations) != 4 {
		t.Fatalf("operations = %d", len(operations))
	}
	for _, op := range operations {
		for _, callerValue := range []string{callerDraftID, callerNotes} {
			if strings.Contains(op.Query, callerValue) {
				t.Fatalf("%s interpolated caller-controlled value %q into query: %s", op.OperationName, callerValue, op.Query)
			}
		}
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
