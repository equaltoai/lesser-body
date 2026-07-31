package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
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
			t.Fatalf("Authorization = %q", got)
		}
		var op cmsapi.Operation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodySubmitArticleDraftForReview":
			_, _ = w.Write([]byte(`{"data":{"shareDraftForReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","grant":{"grantedAt":"2026-07-31T12:00:00Z"},"verdicts":[]}}}`))
		case "BodyArticleDraftReviewQueue":
			_, _ = w.Write([]byte(`{"data":{"sharedDraftReviews":{"edges":[{"node":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","verdicts":[]},"cursor":"queue-1"}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"endCursor":"queue-1"},"totalCount":2}}}`))
		case "BodyArticleDraftReview":
			_, _ = w.Write([]byte(`{"data":{"draftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewStatus":"APPROVED","verdicts":[{"verdict":"APPROVED","recordedAt":"2026-07-31T12:01:00Z"}]}}}`))
		case "BodySubmitArticleDraftReviewVerdict":
			_, _ = w.Write([]byte(`{"data":{"submitDraftReview":{"draftId":"draft-1","contentFormat":"MARKDOWN","status":"DRAFT","updatedAt":"2026-07-31T12:00:00Z","createdAt":"2026-07-31T11:00:00Z","reviewedBy":{"id":"https://example.com/users/reviewer","username":"reviewer"},"reviewStatus":"CHANGES_REQUESTED","editorNotes":"revise","verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"revise","recordedAt":"2026-07-31T12:02:00Z"}]}}}`))
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

	state, err := handleArticleDraftReviewRead(ctx, json.RawMessage(`{"draft_id":"draft-1"}`))
	if err != nil {
		t.Fatalf("state handler: %v", err)
	}
	assertReviewResult(t, state, "state", "state")

	verdict, err := handleArticleDraftReviewVerdict(ctx, json.RawMessage(`{"draft_id":"draft-1","verdict":"CHANGES_REQUESTED","notes":"revise"}`))
	if err != nil {
		t.Fatalf("verdict handler: %v", err)
	}
	assertReviewResult(t, verdict, "verdict_submitted", "")

	if len(operations) != 4 {
		t.Fatalf("operations = %d", len(operations))
	}
	if operations[0].Variables["reviewer"] != "reviewer" || operations[3].Variables["verdict"] != cmsapi.DraftReviewVerdictChangesRequested {
		t.Fatalf("delegated variables = first:%+v verdict:%+v", operations[0].Variables, operations[3].Variables)
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
