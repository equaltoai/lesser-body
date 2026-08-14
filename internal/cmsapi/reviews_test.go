package cmsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
			_, _ = w.Write([]byte(`{"data":{"shareDraftForReview":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","contentHash":"sha256:submit","revision":4,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":false,"publishBlockingReasons":["reviewer_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"}],"grant":{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"},"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":["reviewer_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		case "BodyArticleDraftReview":
			if op.Variables["id"] != callerDraftID {
				t.Errorf("state variables = %+v", op.Variables)
				http.Error(w, "unexpected state variables", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewStatus":"APPROVED","contentHash":"sha256:state","revision":5,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":false,"publishBlockingReasons":["principal_approval_required"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"}],"verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"lesser state remains authoritative","contentHash":"sha256:old","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T11:59:00Z","current":false,"stale":true}],"publishEligibility":{"eligible":false,"blockingReasons":["principal_approval_required"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		case "BodyArticleDraftReviewQueue":
			_, _ = w.Write([]byte(`{"data":{"sharedDraftReviews":{"edges":[{"node":{"draftId":"caller-draft-id-must-stay-in-variables","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","contentHash":"sha256:queue","revision":6,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":true,"publishBlockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"}],"verdicts":[{"verdict":"APPROVED","contentHash":"sha256:queue","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T12:01:00Z","current":true,"stale":false}],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}},"cursor":"queue-1"}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false},"totalCount":1}}}`))
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
	wantVerdicts := []byte(`[{"verdict":"CHANGES_REQUESTED","notes":"lesser state remains authoritative","contentHash":"sha256:old","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T11:59:00Z","current":false,"stale":true}]`)
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
	if state.ContentHash != "sha256:state" || state.Revision != 5 || len(state.ActiveReviewerIDs) != 1 ||
		state.GrantCount != 1 || state.GrantsTruncated || len(state.Grants) != 1 || state.Grants[0].Reviewer == nil || state.Grants[0].Reviewer.Username != "reviewer" ||
		state.PublishEligible || len(state.PublishBlockingReasons) != 1 || state.PublishEligibility.Eligible || state.PublishEligibility.PrincipalApproved {
		t.Fatalf("Lesser authoritative review projection changed in transit: %+v", state)
	}
	queue, err := client.ListArticleDraftReviews(context.Background(), "token", 10, " queue-cursor ")
	if err != nil || len(queue.Edges) != 1 || queue.Edges[0].Node == nil || queue.Edges[0].Node.DraftID != callerDraftID {
		t.Fatalf("ListArticleDraftReviews = %+v, %v", queue, err)
	}
	queueReview := queue.Edges[0].Node
	if queueReview.ContentHash != "sha256:queue" || queueReview.Revision != 6 || !queueReview.PublishEligible ||
		len(queueReview.Grants) != 1 || queueReview.Grants[0].Reviewer == nil || queueReview.Grants[0].Reviewer.Username != "reviewer" ||
		len(queueReview.Verdicts) != 1 || !queueReview.Verdicts[0].Current || queueReview.Verdicts[0].Stale || !queueReview.PublishEligibility.Eligible {
		t.Fatalf("queue omitted Lesser authoritative review projection: %+v", queueReview)
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
		for _, want := range []string{
			"draftId", "generatedBy { id username }", "reviewedBy { id username }", "reviewStatus", "editorNotes",
			"contentHash", "revision", "activeReviewerIds", "publishEligible", "publishBlockingReasons", "reviewersApproved", "principalApprovalRequired", "principalApproved",
			"grantCount", "grantsTruncated", "grants { reviewerId reviewer { id username } grantedAt status revokedAt }",
			"grant { reviewerId reviewer { id username } grantedAt status revokedAt }",
			"verdicts { verdict notes contentHash reviewerId reviewer { id username } recordedAt current stale }",
			"publishEligibility { eligible blockingReasons reviewersApproved principalApprovalRequired principalApproved }",
		} {
			if !strings.Contains(op.Query, want) {
				t.Fatalf("%s query missing %q: %s", op.OperationName, want, op.Query)
			}
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

func TestReadArticleDraftReviewMissingReturnsDraftReviewNotFoundError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"draftReview":null}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	got, err := client.ReadArticleDraftReview(context.Background(), "token", "missing-review")
	if got != nil {
		t.Fatalf("ReadArticleDraftReview = %+v, want nil on missing review", got)
	}

	var notFound *DraftReviewNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("ReadArticleDraftReview error = %T %v, want *DraftReviewNotFoundError", err, err)
	}
	if notFound.Lookup != "id" || notFound.Value != "missing-review" {
		t.Fatalf("DraftReviewNotFoundError = %+v, want lookup=%q value=%q", notFound, "id", "missing-review")
	}
}
