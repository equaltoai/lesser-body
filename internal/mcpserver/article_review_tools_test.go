package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func TestArticleDraftReviewToolContract(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := registerArticleReviewTools(registry); err != nil {
		t.Fatalf("registerArticleReviewTools: %v", err)
	}
	want := map[string]string{
		"article_draft_review_submit":  ScopeWrite,
		"article_draft_review_read":    ScopeRead,
		"article_draft_review_verdict": ScopeWrite,
	}
	for _, def := range registry.List() {
		scope, ok := want[def.Name]
		if !ok {
			t.Fatalf("unexpected review tool %q", def.Name)
		}
		delete(want, def.Name)
		if len(def.InputSchema) == 0 || len(def.OutputSchema) == 0 {
			t.Errorf("%s must publish input and output schemas", def.Name)
		}
		for _, contractText := range []string{"unanimous current approval from every active reviewer", "active approval from the configured instance principal"} {
			if !strings.Contains(def.Description, contractText) {
				t.Errorf("%s description missing publish-gate rule %q", def.Name, contractText)
			}
			if !strings.Contains(string(def.OutputSchema), contractText) {
				t.Errorf("%s output schema missing publish-gate rule %q", def.Name, contractText)
			}
		}
		if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil {
			t.Errorf("%s must publish a read-only hint", def.Name)
		} else if gotReadOnly := *def.Annotations.ReadOnlyHint; gotReadOnly != (scope == ScopeRead) {
			t.Errorf("%s readOnlyHint = %v, scope = %s", def.Name, gotReadOnly, scope)
		}
		if got := RequiredScopesForTool(def.Name); len(got) != 1 || got[0] != scope {
			t.Errorf("%s scopes = %v, want [%s]", def.Name, got, scope)
		}
		for _, profile := range []runtimepolicy.Profile{runtimepolicy.ProfileDrone, runtimepolicy.ProfileSouled} {
			if !runtimepolicy.ToolAllowed(profile, def.Name) {
				t.Errorf("%s must be available to %s", def.Name, profile)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing review tools: %+v", want)
	}
}

func TestArticleDraftReviewHandlersDelegateToLesser(t *testing.T) {
	var operations []cmsapi.Operation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want exact caller bearer", got)
			http.Error(w, "unexpected Authorization header", http.StatusInternalServerError)
			return
		}
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Errorf("decode operation: %v", err)
			http.Error(w, "invalid operation", http.StatusInternalServerError)
			return
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodySubmitArticleDraftForReview":
			_, _ = w.Write([]byte(`{"data":{"shareDraftForReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","contentHash":"sha256:submit","revision":6,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":false,"publishBlockingReasons":["reviewer_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"}],"grant":{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T12:00:00Z","status":"ACTIVE"},"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":["reviewer_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		case "BodyArticleDraftReviewQueue":
			_, _ = w.Write([]byte(`{"data":{"sharedDraftReviews":{"edges":[{"node":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","contentHash":"sha256:queue","revision":7,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":true,"publishBlockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T11:30:00Z","status":"ACTIVE"}],"verdicts":[{"verdict":"APPROVED","contentHash":"sha256:queue","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T12:01:00Z","current":true,"stale":false}],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}},"cursor":"queue-1"}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"endCursor":"queue-1"},"totalCount":2}}}`))
		case "BodyArticleDraftReview":
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewStatus":"APPROVED","contentHash":"sha256:state","revision":8,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":false,"publishBlockingReasons":["principal_approval_required"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T11:30:00Z","status":"ACTIVE"}],"verdicts":[{"verdict":"APPROVED","contentHash":"sha256:state","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T12:01:00Z","current":true,"stale":false}],"publishEligibility":{"eligible":false,"blockingReasons":["principal_approval_required"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		case "BodySubmitArticleDraftReviewVerdict":
			_, _ = w.Write([]byte(`{"data":{"submitDraftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:02:00Z","createdAt":"2026-07-31T11:00:00Z","reviewedBy":{"id":"https://example.com/users/reviewer","username":"reviewer"},"reviewStatus":"CHANGES_REQUESTED","editorNotes":"revise","contentHash":"sha256:verdict","revision":9,"activeReviewerIds":["https://example.com/users/reviewer"],"publishEligible":false,"publishBlockingReasons":["reviewer_changes_requested","principal_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"grantedAt":"2026-07-31T11:30:00Z","status":"ACTIVE"}],"verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"revise","contentHash":"sha256:verdict","reviewerId":"https://example.com/users/reviewer","reviewer":{"id":"https://example.com/users/reviewer","username":"reviewer"},"recordedAt":"2026-07-31T12:02:00Z","current":true,"stale":false}],"publishEligibility":{"eligible":false,"blockingReasons":["reviewer_changes_requested","principal_approval_required"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`))
		default:
			t.Errorf("unexpected operation %q", op.OperationName)
			http.Error(w, "unexpected operation", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	ctx := articleDraftTestContext()
	submit, err := handleArticleDraftReviewSubmit(ctx, json.RawMessage(`{"draft_id":"draft-1","reviewer":"reviewer"}`))
	if err != nil {
		t.Fatalf("submit handler: %v", err)
	}
	assertReviewResult(t, submit, "submitted", "")

	queue, err := handleArticleDraftReviewRead(ctx, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatalf("queue handler: %v", err)
	}
	assertReviewResult(t, queue, "queue", "queue")
	assertAuthoritativeReviewProjection(t, queue, "queue", "sha256:queue", true)

	state, err := handleArticleDraftReviewRead(ctx, json.RawMessage(`{"draft_id":"draft-1"}`))
	if err != nil {
		t.Fatalf("state handler: %v", err)
	}
	assertReviewResult(t, state, "state", "state")
	assertAuthoritativeReviewProjection(t, state, "state", "sha256:state", false)

	verdict, err := handleArticleDraftReviewVerdict(ctx, json.RawMessage(`{"draft_id":"draft-1","verdict":"CHANGES_REQUESTED","notes":"revise"}`))
	if err != nil {
		t.Fatalf("verdict handler: %v", err)
	}
	assertReviewResult(t, verdict, "verdict_submitted", "")
	assertAuthoritativeReviewProjection(t, verdict, "single", "sha256:verdict", false)

	if len(operations) != 4 {
		t.Fatalf("operations = %d", len(operations))
	}
	if operations[0].Variables["reviewer"] != "reviewer" || operations[3].Variables["verdict"] != cmsapi.DraftReviewVerdictChangesRequested {
		t.Fatalf("delegated variables = first:%+v verdict:%+v", operations[0].Variables, operations[3].Variables)
	}
}

func TestArticleDraftReviewDefaultQueueFitsDefaultBudget(t *testing.T) {
	budget := reviewOutputBudget(0)
	for _, tc := range []struct {
		name  string
		count int
	}{
		{name: "n=1", count: 1},
		{name: "n=5", count: 5},
		{name: "n=default-limit", count: articleDraftReviewDefaultLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queue := &cmsapi.DraftReviewConnection{
				Edges:      make([]cmsapi.DraftReviewEdge, 0, tc.count),
				TotalCount: tc.count,
			}
			for i := 0; i < tc.count; i++ {
				queue.Edges = append(queue.Edges, cmsapi.DraftReviewEdge{
					Node:   realisticDraftReviewFixture(i, 1),
					Cursor: fmt.Sprintf("review-queue-cursor-%02d", i),
				})
			}

			result, err := articleDraftReviewQueueResult(queue, tc.count, budget)
			if err != nil {
				t.Fatalf("articleDraftReviewQueueResult: %v", err)
			}
			if result.IsError {
				t.Fatalf("realistic queue with %d reviews exceeded the documented default budget: %+v", tc.count, result.StructuredContent)
			}
			measurement, err := measureToolResultPayload(result)
			if err != nil {
				t.Fatalf("measureToolResultPayload: %v", err)
			}
			if measurement.JSONRPCEnvelopeBytes > budget {
				t.Fatalf("realistic queue envelope at n=%d measured %d bytes, budget %d", tc.count, measurement.JSONRPCEnvelopeBytes, budget)
			}
			t.Logf("realistic queue envelope at n=%d: %d bytes", tc.count, measurement.JSONRPCEnvelopeBytes)
		})
	}
}

// Lesser keeps immutable verdict history without a finite record cap. Two pins
// the first ordinary re-review round without pretending the history is bounded.
const realisticDraftReviewPinnedVerdictCount = 2

func TestArticleDraftReviewBudgetGuardRejectsPinnedVerdictHistoryDrift(t *testing.T) {
	budget := reviewOutputBudget(0)
	queue := &cmsapi.DraftReviewConnection{
		Edges:      make([]cmsapi.DraftReviewEdge, 0, articleDraftReviewDefaultLimit),
		TotalCount: articleDraftReviewDefaultLimit,
	}
	for i := 0; i < articleDraftReviewDefaultLimit; i++ {
		queue.Edges = append(queue.Edges, cmsapi.DraftReviewEdge{
			Node:   realisticDraftReviewFixture(i, realisticDraftReviewPinnedVerdictCount),
			Cursor: fmt.Sprintf("review-queue-cursor-%02d", i),
		})
	}

	result, err := articleDraftReviewQueueResult(queue, articleDraftReviewDefaultLimit, budget)
	if err != nil {
		t.Fatalf("articleDraftReviewQueueResult: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected pinned %d-verdict history to trip the %d-byte default budget guard", realisticDraftReviewPinnedVerdictCount, budget)
	}

	errorPayload, ok := result.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("budget guard error payload = %+v", result.StructuredContent)
	}
	details, ok := errorPayload["details"].(map[string]any)
	if !ok {
		t.Fatalf("budget guard details = %+v", errorPayload)
	}
	measuredBytes, ok := details["measuredBytes"].(int)
	if errorPayload["code"] != "response_too_large" || errorPayload["status"] != 413 ||
		!strings.Contains(fmt.Sprint(errorPayload["message"]), "exceeds max_output_bytes") ||
		!ok || measuredBytes <= budget ||
		details["maxOutputBytes"] != budget ||
		details["guidance"] != "reduce queue limit or increase max_output_bytes" {
		t.Fatalf("budget guard must fail loudly with measured recovery details: %+v", errorPayload)
	}
	t.Logf("realistic queue envelope at n=%d with %d verdicts/review: %d bytes (budget %d)", articleDraftReviewDefaultLimit, realisticDraftReviewPinnedVerdictCount, measuredBytes, budget)
}

func realisticDraftReviewFixture(index, verdictCount int) *cmsapi.DraftReview {
	title := fmt.Sprintf("Review-ready architecture note %02d: preserve Lesser authority", index)
	subtitle := "An agent-generated draft with reviewer attribution and editorial context"
	excerpt := "Body transports Lesser's caller-authorized review state without creating local review semantics."
	reviewStatus := cmsapi.DraftReviewVerdictChangesRequested
	editorNotes := "Clarify the authority boundary and retain the deployment evidence."
	verdictNotes := "Revise the authority paragraph before requesting publication."
	verdictContentHash := "sha256:realistic-review-content"
	verdicts := make([]cmsapi.DraftReviewVerdictRecord, 0, verdictCount)
	for range verdictCount {
		verdicts = append(verdicts, cmsapi.DraftReviewVerdictRecord{
			Verdict:     cmsapi.DraftReviewVerdictChangesRequested,
			Notes:       &verdictNotes,
			ContentHash: &verdictContentHash,
			ReviewerID:  "https://example.com/users/reviewer",
			Reviewer:    &cmsapi.Actor{ID: "https://example.com/users/reviewer", Username: "reviewer"},
			RecordedAt:  "2026-07-31T12:01:00Z",
			Current:     true,
			Stale:       false,
		})
	}
	return &cmsapi.DraftReview{
		DraftID:       fmt.Sprintf("draft-realistic-review-%02d", index),
		Title:         &title,
		Subtitle:      &subtitle,
		Excerpt:       &excerpt,
		ContentFormat: "MARKDOWN",
		Status:        "DRAFT",
		UpdatedAt:     "2026-07-31T12:00:00Z",
		CreatedAt:     "2026-07-31T11:00:00Z",
		GeneratedBy:   &cmsapi.Actor{ID: "https://example.com/users/author", Username: "author"},
		ReviewedBy:    &cmsapi.Actor{ID: "https://example.com/users/reviewer", Username: "reviewer"},
		ReviewStatus:  &reviewStatus,
		EditorNotes:   &editorNotes,
		ContentHash:   "sha256:realistic-review-content",
		Revision:      8,
		ActiveReviewerIDs: []string{
			"https://example.com/users/reviewer",
		},
		PublishEligible:           false,
		PublishBlockingReasons:    []string{"reviewer_changes_requested", "principal_approval_required"},
		ReviewersApproved:         false,
		PrincipalApprovalRequired: true,
		PrincipalApproved:         false,
		GrantCount:                1,
		Grants: []cmsapi.DraftReviewGrant{{
			ReviewerID: "https://example.com/users/reviewer",
			Reviewer:   &cmsapi.Actor{ID: "https://example.com/users/reviewer", Username: "reviewer"},
			GrantedAt:  "2026-07-31T11:30:00Z",
			Status:     "ACTIVE",
		}},
		Grant: &cmsapi.DraftReviewGrant{
			ReviewerID: "https://example.com/users/reviewer",
			Reviewer:   &cmsapi.Actor{ID: "https://example.com/users/reviewer", Username: "reviewer"},
			GrantedAt:  "2026-07-31T11:30:00Z",
			Status:     "ACTIVE",
		},
		Verdicts: verdicts,
		PublishEligibility: cmsapi.DraftPublishEligibility{
			Eligible:                  false,
			BlockingReasons:           []string{"reviewer_changes_requested", "principal_approval_required"},
			ReviewersApproved:         false,
			PrincipalApprovalRequired: true,
			PrincipalApproved:         false,
		},
	}
}

func TestArticleDraftReviewHandlersRejectInvalidModesAndVerdicts(t *testing.T) {
	ctx := articleDraftTestContext()
	for name, call := range map[string]func() error{
		"submit missing reviewer": func() error {
			_, err := handleArticleDraftReviewSubmit(ctx, json.RawMessage(`{"draft_id":"draft-1"}`))
			return err
		},
		"state with queue cursor": func() error {
			_, err := handleArticleDraftReviewRead(ctx, json.RawMessage(`{"draft_id":"draft-1","cursor":"next"}`))
			return err
		},
		"queue limit": func() error {
			_, err := handleArticleDraftReviewRead(ctx, json.RawMessage(`{"limit":81}`))
			return err
		},
		"invalid verdict": func() error {
			_, err := handleArticleDraftReviewVerdict(ctx, json.RawMessage(`{"draft_id":"draft-1","verdict":"MAYBE"}`))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("expected invalid params error")
			}
		})
	}
}

func assertReviewResult(t *testing.T, result *mcpruntime.ToolResult, operation, mode string) {
	t.Helper()
	if result == nil || len(result.Content) != 1 || result.StructuredContent == nil {
		t.Fatalf("invalid result: %+v", result)
	}
	data, ok := result.StructuredContent["data"].(map[string]any)
	if !ok || data["operation"] != operation {
		t.Fatalf("operation data = %+v, want %q", data, operation)
	}
	if mode != "" && data["mode"] != mode {
		t.Fatalf("mode data = %+v, want %q", data, mode)
	}
	if !strings.Contains(result.Content[0].Text, `"payload"`) {
		t.Fatalf("text result must carry accessible payload: %s", result.Content[0].Text)
	}
}

func assertAuthoritativeReviewProjection(t *testing.T, result *mcpruntime.ToolResult, mode, contentHash string, eligible bool) {
	t.Helper()
	data, ok := result.StructuredContent["data"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent.data = %+v", result.StructuredContent)
	}
	var review *cmsapi.DraftReview
	if mode == "state" || mode == "single" {
		review, _ = data["review"].(*cmsapi.DraftReview)
	} else {
		reviews, _ := data["reviews"].([]map[string]any)
		if len(reviews) == 1 {
			review, _ = reviews[0]["review"].(*cmsapi.DraftReview)
		}
	}
	if review == nil {
		t.Fatalf("%s result missing review projection: %+v", mode, data)
	}
	if review.ContentHash != contentHash || len(review.ActiveReviewerIDs) != 1 || len(review.Grants) != 1 || review.Grants[0].Reviewer == nil || review.Grants[0].Reviewer.Username != "reviewer" ||
		len(review.Verdicts) != 1 || review.Verdicts[0].ContentHash == nil || *review.Verdicts[0].ContentHash != contentHash || review.Verdicts[0].Reviewer == nil || !review.Verdicts[0].Current || review.Verdicts[0].Stale ||
		review.PublishEligible != eligible || review.PublishEligibility.Eligible != eligible {
		t.Fatalf("%s result omitted Lesser authoritative review state: %+v", mode, review)
	}
}
