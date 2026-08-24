package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// promoTestContext returns a tool context carrying an OAuth bearer; tests
// override the LESSER_API_BASE_URL stub (see newMediaGraphQLStub).
func promoTestContext() context.Context {
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "alice",
	}, "test-token")
}

// promoTestContextWithShareCaller models a reviewer acting through the
// agent-share act-as seam: the actor route value names the agent, and the
// share caller is the grantee admitted by the actor-binding middleware.
func promoTestContextWithShareCaller(caller string) context.Context {
	ctx := auth.WithToolActor(promoTestContext(), "agent1")
	return WithShareCaller(ctx, caller)
}

func promoContentHash(seed string) string {
	hex := strings.Repeat(seed, 64)
	if len(hex) > 64 {
		hex = hex[:64]
	}
	return "sha256:" + hex
}

// promoFixturePackage builds a Lesser promo package fixture as the typed
// projection body consumes, so the wire JSON always matches the contract shape.
func promoFixturePackage(status string, eligible bool, releasedStatusID string) *cmsapi.PromoPackage {
	blocking := []string{}
	if !eligible {
		blocking = []string{cmsapi.PromoBlockingReasonApprovalRequired}
	}
	pkg := &cmsapi.PromoPackage{
		ID:               "pkg-1",
		OwnerID:          "alice",
		ArticleID:        "https://example.com/articles/1",
		PostText:         "Launching!",
		Visibility:       cmsapi.PromoPackageVisibilityPublic,
		ContentHash:      promoContentHash("a"),
		Status:           status,
		ReleasedStatusID: nil,
		Assets: []cmsapi.PromoPackageAsset{{
			MediaID:      "media-1",
			ContentHash:  strPtr(promoContentHash("b")),
			PublishedURL: strPtr("https://cdn.example.com/media-1.png"),
			State:        cmsapi.PromoPackageAssetStatePublished,
			Width:        intPtr(1200),
			Height:       intPtr(800),
			MimeType:     strPtr("image/png"),
		}},
		CreatedAt: "2026-08-24T12:00:00Z",
		UpdatedAt: "2026-08-24T12:00:00Z",
		Review: &cmsapi.PromoPackageReview{
			PackageID:                 "pkg-1",
			ContentHash:               promoContentHash("a"),
			Assets:                    []cmsapi.PromoPackageAsset{},
			ActiveReviewerIDs:         []string{"reviewer"},
			ReleaseEligible:           eligible,
			ReleaseBlockingReasons:    blocking,
			ReviewersApproved:         eligible,
			PrincipalApprovalRequired: true,
			PrincipalApproved:         false,
			GrantCount:                1,
			GrantsTruncated:           false,
			Grants: []cmsapi.PromoPackageReviewGrant{{
				ReviewerID: "reviewer",
				GrantedAt:  "2026-08-24T12:05:00Z",
				ExpiresAt:  strPtr("2026-08-31T12:05:00Z"),
				Status:     cmsapi.PromoPackageGrantActive,
			}},
			Verdicts: []cmsapi.PromoPackageVerdictRecord{},
			ReleaseEligibility: cmsapi.PromoPackageReleaseEligibility{
				Eligible:                  eligible,
				BlockingReasons:           blocking,
				ReviewersApproved:         eligible,
				PrincipalApprovalRequired: true,
				PrincipalApproved:         false,
			},
		},
	}
	if releasedStatusID != "" {
		pkg.ReleasedStatusID = strPtr(releasedStatusID)
	}
	return pkg
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func promoPackageStubBody(t *testing.T, pkg *cmsapi.PromoPackage) string {
	t.Helper()
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal promo fixture: %v", err)
	}
	return `{"data":{"promoPackage":` + string(raw) + `}}`
}

func promoReviewFixtureJSON() string {
	return `{"packageId":"pkg-1","contentHash":"` + promoContentHash("a") + `","assets":[],"activeReviewerIds":["reviewer"],"releaseEligible":false,"releaseBlockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"reviewer","grantedAt":"2026-08-24T12:05:00Z","expiresAt":"2026-08-31T12:05:00Z","status":"ACTIVE","revokedAt":null}],"verdicts":[],"releaseEligibility":{"eligible":false,"blockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}`
}

// TestPromoToolsRegisterAndClassify pins the six-tool promo surface: every
// tool registers with an output schema, annotations, and an explicit scope
// classification (write for the mutating lanes, read for the state/read lanes).
func TestPromoToolsRegisterAndClassify(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := registerPromoTools(registry); err != nil {
		t.Fatalf("registerPromoTools: %v", err)
	}

	wantScopes := map[string][]string{
		"promo_compose":       {ScopeWrite},
		"promo_review_share":  {ScopeWrite},
		"promo_review_submit": {ScopeWrite},
		"promo_state":         {ScopeRead},
		"promo_release":       {ScopeWrite},
		"promo_read":          {ScopeRead},
	}
	registered := map[string]struct{}{}
	for _, def := range registry.List() {
		registered[def.Name] = struct{}{}
		scopes, ok := wantScopes[def.Name]
		if !ok {
			continue
		}
		delete(wantScopes, def.Name)
		if got := RequiredScopesForTool(def.Name); !sameScopes(got, scopes) {
			t.Errorf("%s scopes = %v, want %v", def.Name, got, scopes)
		}
		if len(def.OutputSchema) == 0 {
			t.Errorf("%s has no output schema", def.Name)
		}
		if def.Annotations == nil {
			t.Errorf("%s has no annotations", def.Name)
		}
		if strings.Contains(def.Name, "release") && len(def.InputSchema) == 0 {
			t.Errorf("%s has no input schema", def.Name)
		}
	}
	for name := range wantScopes {
		t.Errorf("promo tool %s was not registered", name)
	}
	if len(registered) != 6 {
		t.Errorf("expected 6 promo tools, got %d", len(registered))
	}
}

// TestPromoEnvelopeStateMapping pins the lifecycle mapping table: states are
// derived strictly from Lesser-authoritative fields, RELEASING is its own
// envelope state, and a surfaced status ID never fabricates a released state.
func TestPromoEnvelopeStateMapping(t *testing.T) {
	eligible := &cmsapi.PromoPackageReview{ReleaseEligible: true}
	blocked := &cmsapi.PromoPackageReview{ReleaseEligible: false}
	for _, tc := range []struct {
		name string
		pkg  *cmsapi.PromoPackage
		want string
	}{
		{"nil_package", nil, promoStateUnknown},
		{"draft_no_review", &cmsapi.PromoPackage{Status: cmsapi.PromoPackageStatusDraft}, promoStateDraft},
		{"draft_blocked", &cmsapi.PromoPackage{Status: cmsapi.PromoPackageStatusDraft, Review: blocked}, promoStateDraft},
		{"draft_approved", &cmsapi.PromoPackage{Status: cmsapi.PromoPackageStatusDraft, Review: eligible}, promoStateApproved},
		{"releasing", &cmsapi.PromoPackage{Status: cmsapi.PromoPackageStatusReleasing, Review: eligible}, promoStateReleasing},
		{"released", &cmsapi.PromoPackage{Status: cmsapi.PromoPackageStatusReleased}, promoStateReleased},
		{"unknown_status", &cmsapi.PromoPackage{Status: "SOMETHING_NEW"}, promoStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promoEnvelopeStateForPackage(tc.pkg); got != tc.want {
				t.Fatalf("state → %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPromoBlockingReasonsPinsReleasingInjection ensures the PACKAGE_RELEASING
// reason is always present while the reservation is held, even if the review
// projection omits it, and is not duplicated when lesser already surfaced it.
func TestPromoBlockingReasonsPinsReleasingInjection(t *testing.T) {
	injected := promoBlockingReasons(&cmsapi.PromoPackage{
		Status: cmsapi.PromoPackageStatusReleasing,
		Review: &cmsapi.PromoPackageReview{ReleaseBlockingReasons: []string{cmsapi.PromoBlockingReasonApprovalRequired}},
	})
	if len(injected) != 2 || injected[0] != cmsapi.PromoBlockingReasonApprovalRequired || injected[1] != cmsapi.PromoBlockingReasonReleasing {
		t.Fatalf("releasing reasons must inject PACKAGE_RELEASING, got %v", injected)
	}
	dedup := promoBlockingReasons(&cmsapi.PromoPackage{
		Status: cmsapi.PromoPackageStatusReleasing,
		Review: &cmsapi.PromoPackageReview{ReleaseBlockingReasons: []string{cmsapi.PromoBlockingReasonReleasing}},
	})
	if len(dedup) != 1 || dedup[0] != cmsapi.PromoBlockingReasonReleasing {
		t.Fatalf("PACKAGE_RELEASING must not be duplicated, got %v", dedup)
	}
	passThrough := promoBlockingReasons(&cmsapi.PromoPackage{
		Status: cmsapi.PromoPackageStatusDraft,
		Review: &cmsapi.PromoPackageReview{ReleaseBlockingReasons: []string{cmsapi.PromoBlockingReasonPrincipalRequired}},
	})
	if len(passThrough) != 1 || passThrough[0] != cmsapi.PromoBlockingReasonPrincipalRequired {
		t.Fatalf("draft reasons must pass through verbatim, got %v", passThrough)
	}
}

// TestPromoStateReleasingSurfacesReconcileGuidance drives promo_state on a
// RELEASING package: the envelope state is releasing, the PACKAGE_RELEASING
// blocking reason is present, and the guidance is operator reconciliation with
// the runbook cited — never retryable.
func TestPromoStateReleasingSurfacesReconcileGuidance(t *testing.T) {
	pkg := promoFixturePackage(cmsapi.PromoPackageStatusReleasing, true, "")
	pkg.Review.ReleaseBlockingReasons = nil // Lesser projection omits it; body must inject
	stub := newMediaGraphQLStub(t, map[string]string{
		"BodyPromoPackage": promoPackageStubBody(t, pkg),
	})
	result, err := handlePromoState(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("promo_state failed: result=%+v err=%v", result, err)
	}
	if op := stub.lastBody("BodyPromoPackage"); op == nil || !strings.Contains(op.Query, "promoPackage(id: $id)") {
		t.Fatalf("promo_state must resolve via BodyPromoPackage: %+v", op)
	}
	data := structuredData(t, result)
	if got := data["state"]; got != promoStateReleasing {
		t.Fatalf("state = %v, want %q", got, promoStateReleasing)
	}
	reasons, _ := data["blockingReasons"].([]any)
	found := false
	for _, r := range reasons {
		if r == cmsapi.PromoBlockingReasonReleasing {
			found = true
		}
	}
	if !found {
		t.Fatalf("releasing state must surface PACKAGE_RELEASING blocking reason, got %v", reasons)
	}
	guidance, _ := data["guidance"].(string)
	for _, marker := range []string{"PACKAGE_RELEASING", "Do NOT retry", "reconcile", promoRunbookPath} {
		if !strings.Contains(strings.ToLower(guidance), strings.ToLower(marker)) {
			t.Fatalf("releasing guidance must mention %q, got %q", marker, guidance)
		}
	}
}

// TestPromoReadReleasedSurfacesOutboundPost drives promo_read on a released
// package: the state is released and the surfaced status id is the outbound
// post reference to expand with post_get.
func TestPromoReadReleasedSurfacesOutboundPost(t *testing.T) {
	pkg := promoFixturePackage(cmsapi.PromoPackageStatusReleased, true, "status-1")
	newMediaGraphQLStub(t, map[string]string{
		"BodyPromoPackage": promoPackageStubBody(t, pkg),
	})
	result, err := handlePromoRead(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("promo_read failed: result=%+v err=%v", result, err)
	}
	data := structuredData(t, result)
	if got := data["state"]; got != promoStateReleased {
		t.Fatalf("state = %v, want %q", got, promoStateReleased)
	}
	if got := data["releasedStatusId"]; got != "status-1" {
		t.Fatalf("releasedStatusId = %v, want status-1", got)
	}
	outbound, _ := data["outboundPost"].(map[string]any)
	if outbound == nil || outbound["statusId"] != "status-1" {
		t.Fatalf("outboundPost must carry the status id: %+v", outbound)
	}
	guidance, _ := data["guidance"].(string)
	if !strings.Contains(guidance, "post_get") {
		t.Fatalf("released guidance must point at post_get, got %q", guidance)
	}
}

// TestPromoReadNotReleasedGuidesToState drives promo_read on a package that is
// not released: no outbound post exists and the guidance says so plainly.
func TestPromoReadNotReleasedGuidesToState(t *testing.T) {
	pkg := promoFixturePackage(cmsapi.PromoPackageStatusDraft, false, "")
	newMediaGraphQLStub(t, map[string]string{
		"BodyPromoPackage": promoPackageStubBody(t, pkg),
	})
	result, err := handlePromoRead(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("promo_read failed: result=%+v err=%v", result, err)
	}
	data := structuredData(t, result)
	if got := data["state"]; got != promoStateDraft {
		t.Fatalf("state = %v, want %q", got, promoStateDraft)
	}
	if _, ok := data["releasedStatusId"]; ok {
		t.Fatalf("unreleased read must not surface a releasedStatusId: %+v", data)
	}
	guidance, _ := data["guidance"].(string)
	if !strings.Contains(strings.ToLower(guidance), "not released") {
		t.Fatalf("guidance must state the package is not released, got %q", guidance)
	}
}

// TestPromoReleaseErrorLanes pins the structured error envelope for the release
// lanes: the stamp failure (post EXISTS) and the releasing reservation both map
// to operator reconciliation with the runbook cited and retryable=false; the
// approval gates map to their own conflict lanes.
func TestPromoReleaseErrorLanes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		gqlResponse  string
		wantCode     string
		wantStatus   int
		wantRetry    bool
		wantStatusID string
		wantRunbook  bool
		guidance     []string
	}{
		{
			name:         "stamp_failure_post_exists",
			gqlResponse:  `{"data":null,"errors":[{"message":"promo package release created status status-42 but could not stamp it: dynamo condition failed","path":["releasePromoPackage"]}]}`,
			wantCode:     promoErrorReconcileRequired,
			wantStatus:   409,
			wantRetry:    false,
			wantStatusID: "status-42",
			wantRunbook:  true,
			guidance:     []string{"post EXISTS", "Do NOT retry", "reconcile"},
		},
		{
			name:        "releasing_reservation",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package release is already in progress","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorReconcileRequired,
			wantStatus:  409,
			wantRetry:   false,
			wantRunbook: true,
			guidance:    []string{"releasing reservation", "Do NOT retry", "reconcile"},
		},
		{
			name:        "approval_required",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package requires approval from every required reviewer\nblocking reasons: REVIEW_APPROVAL_REQUIRED","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorApprovalRequired,
			wantStatus:  409,
			wantRetry:   true,
			guidance:    []string{"promo_state", "blocking reasons"},
		},
		{
			name:        "principal_approval_required",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package release requires an active approval from the instance principal\nblocking reasons: PRINCIPAL_APPROVAL_REQUIRED","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorPrincipalRequired,
			wantStatus:  409,
			wantRetry:   true,
			guidance:    []string{"principal", "promo_state"},
		},
		{
			name:        "asset_unavailable",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package asset cannot serve the exact approved bytes: ASSET_MISSING, ASSET_DIGEST_CHANGED","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorAssetUnavailable,
			wantStatus:  409,
			wantRetry:   true,
			guidance:    []string{"ASSET_", "re-compose"},
		},
		{
			name:        "already_released",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package is already released","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorAlreadyReleased,
			wantStatus:  409,
			wantRetry:   false,
			guidance:    []string{"promo_read", "released status id"},
		},
		{
			name:        "conflict",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package changed concurrently","path":["releasePromoPackage"]}]}`,
			wantCode:    promoErrorConflict,
			wantStatus:  409,
			wantRetry:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newMediaGraphQLStub(t, map[string]string{
				"BodyReleasePromoPackage": tc.gqlResponse,
			})
			result, err := handlePromoRelease(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("release must fail, got %+v", result)
			}
			errorPayload, _ := result.StructuredContent["error"].(map[string]any)
			if errorPayload == nil {
				t.Fatalf("error envelope missing")
			}
			if got := errorPayload["code"]; got != tc.wantCode {
				t.Fatalf("code = %v, want %v", got, tc.wantCode)
			}
			if got := errorPayload["status"]; got != tc.wantStatus {
				t.Fatalf("status = %v, want %v", got, tc.wantStatus)
			}
			details, _ := errorPayload["details"].(map[string]any)
			if got := details["retryable"]; got != tc.wantRetry {
				t.Fatalf("retryable = %v, want %v", got, tc.wantRetry)
			}
			if tc.wantRunbook {
				if got := details["runbook"]; got != promoRunbookPath {
					t.Fatalf("runbook = %v, want %v", got, promoRunbookPath)
				}
			}
			if tc.wantStatusID != "" {
				if got := details["statusId"]; got != tc.wantStatusID {
					t.Fatalf("statusId = %v, want %v", got, tc.wantStatusID)
				}
			}
			for _, marker := range tc.guidance {
				text := ""
				if details != nil {
					if g, _ := details["guidance"].(string); g != "" {
						text = g
					}
				}
				if text == "" {
					if m, _ := errorPayload["message"].(string); m != "" {
						text = m
					}
				}
				if !strings.Contains(strings.ToLower(text), strings.ToLower(marker)) {
					t.Fatalf("error must mention %q, got %q", marker, text)
				}
			}
		})
	}
}

// TestPromoReviewSubmitRequiresInspectedContentHash pins the submit-hash
// requirement (lesser's argument is String! non-null): body rejects a missing
// or malformed content_hash before any lesser call, and a valid submit threads
// the inspected hash through the GraphQL contract.
func TestPromoReviewSubmitRequiresInspectedContentHash(t *testing.T) {
	hash := promoContentHash("a")

	t.Run("missing_hash_rejected_before_lesser_call", func(t *testing.T) {
		stub := newMediaGraphQLStub(t, map[string]string{})
		result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approved"}`))
		if err == nil || result != nil {
			t.Fatalf("missing content_hash must be rejected client-side: result=%+v err=%v", result, err)
		}
		if !strings.Contains(err.Error(), "content_hash") {
			t.Fatalf("error must name content_hash, got %v", err)
		}
		if ops := stub.operations(); len(ops) != 0 {
			t.Fatalf("no lesser call must happen for a missing hash, got %v", ops)
		}
	})

	t.Run("malformed_hash_rejected", func(t *testing.T) {
		newMediaGraphQLStub(t, map[string]string{})
		result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approved","content_hash":"not-a-digest"}`))
		if err == nil || result != nil {
			t.Fatalf("malformed content_hash must be rejected client-side: result=%+v err=%v", result, err)
		}
	})

	t.Run("valid_submit_threads_inspected_hash", func(t *testing.T) {
		stub := newMediaGraphQLStub(t, map[string]string{
			"BodySubmitPromoPackageReview": `{"data":{"submitPromoPackageReview":` + promoReviewFixtureJSON() + `}}`,
		})
		result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approved","content_hash":"`+hash+`"}`))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("valid submit failed: result=%+v err=%v", result, err)
		}
		data := structuredData(t, result)
		if got := data["contentHash"]; got != hash {
			t.Fatalf("result contentHash = %v, want %v", got, hash)
		}
		op := stub.lastBody("BodySubmitPromoPackageReview")
		if op == nil {
			t.Fatalf("no BodySubmitPromoPackageReview call observed")
		}
		if !strings.Contains(op.Query, "$contentHash: String!") {
			t.Fatalf("submit query must carry the non-null contentHash argument: %s", op.Query)
		}
		if op.Variables["contentHash"] != hash {
			t.Fatalf("submit variables contentHash = %v, want %v", op.Variables["contentHash"], hash)
		}
	})

	t.Run("invalid_verdict_rejected", func(t *testing.T) {
		newMediaGraphQLStub(t, map[string]string{})
		result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approvedd","content_hash":"`+hash+`"}`))
		if err == nil || result != nil {
			t.Fatalf("invalid verdict must be rejected client-side: result=%+v err=%v", result, err)
		}
	})
}

// TestPromoReviewSubmitContentChangedConflict pins the recomposed-package lane:
// a submit whose inspected hash no longer matches rejects with the content
// changed conflict and retry guidance, never blessing unseen content.
func TestPromoReviewSubmitContentChangedConflict(t *testing.T) {
	newMediaGraphQLStub(t, map[string]string{
		"BodySubmitPromoPackageReview": `{"data":null,"errors":[{"message":"promo package content changed since the reviewer inspected it","path":["submitPromoPackageReview"]}]}`,
	})
	result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approved","content_hash":"`+promoContentHash("a")+`"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("hash mismatch must fail, got %+v", result)
	}
	errorPayload, _ := result.StructuredContent["error"].(map[string]any)
	if got := errorPayload["code"]; got != promoErrorContentChanged {
		t.Fatalf("code = %v, want %v", got, promoErrorContentChanged)
	}
	if got := errorPayload["status"]; got != 409 {
		t.Fatalf("status = %v, want 409", got)
	}
}

// TestPromoComposeValidation pins body-side pre-validation before any lesser
// call: post-text size, visibility enum, and the ordered asset set rules.
func TestPromoComposeValidation(t *testing.T) {
	valid := `{"article_id":"https://example.com/articles/1","post_text":"Launching!","visibility":"public","asset_media_ids":["media-1"]}`
	for _, tc := range []struct {
		name string
		args string
	}{
		{"missing_article", `{"post_text":"Launching!","visibility":"public","asset_media_ids":["media-1"]}`},
		{"missing_post_text", `{"article_id":"https://example.com/articles/1","visibility":"public","asset_media_ids":["media-1"]}`},
		{"missing_assets", `{"article_id":"https://example.com/articles/1","post_text":"Launching!","visibility":"public"}`},
		{"bad_visibility", `{"article_id":"https://example.com/articles/1","post_text":"Launching!","visibility":"private","asset_media_ids":["media-1"]}`},
		{"duplicate_assets", `{"article_id":"https://example.com/articles/1","post_text":"Launching!","visibility":"public","asset_media_ids":["media-1","media-1"]}`},
		{"oversized_post_text", `{"article_id":"https://example.com/articles/1","post_text":"` + strings.Repeat("x", 5001) + `","visibility":"public","asset_media_ids":["media-1"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := newMediaGraphQLStub(t, map[string]string{})
			result, err := handlePromoCompose(promoTestContext(), json.RawMessage(tc.args))
			if err == nil || result != nil {
				t.Fatalf("invalid compose must be rejected client-side: result=%+v err=%v", result, err)
			}
			if ops := stub.operations(); len(ops) != 0 {
				t.Fatalf("no lesser call must happen for invalid compose, got %v", ops)
			}
		})
	}

	t.Run("valid_compose_threads_input", func(t *testing.T) {
		raw, err := json.Marshal(promoFixturePackage(cmsapi.PromoPackageStatusDraft, false, ""))
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		stub := newMediaGraphQLStub(t, map[string]string{
			"BodyComposePromoPackage": `{"data":{"composePromoPackage":` + string(raw) + `}}`,
		})
		result, err := handlePromoCompose(promoTestContext(), json.RawMessage(valid))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("valid compose failed: result=%+v err=%v", result, err)
		}
		data := structuredData(t, result)
		if got := data["packageId"]; got != "pkg-1" {
			t.Fatalf("packageId = %v, want pkg-1", got)
		}
		if got := data["contentHash"]; got != promoContentHash("a") {
			t.Fatalf("contentHash = %v", got)
		}
		op := stub.lastBody("BodyComposePromoPackage")
		if op == nil {
			t.Fatalf("no BodyComposePromoPackage call observed")
		}
		input, _ := op.Variables["input"].(map[string]any)
		if input["visibility"] != "PUBLIC" {
			t.Fatalf("compose must uppercase visibility to lesser's enum, got %v", input["visibility"])
		}
	})
}

// TestPromoComposeAdmissionLookupLanes pins the compose admission error lanes
// through the handler envelope: a foreign (not-the-composer) asset, an unknown
// media id, or an unknown article id is a caller-correctable validation failure
// (422 promo_validation with Lesser's message preserved) — never the package
// not-found bucket (404 with details.lookup package_id), which is reserved for
// package-id lookups.
func TestPromoComposeAdmissionLookupLanes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		gqlResponse string
		wantMessage string
	}{
		{
			name:        "foreign_asset",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package asset \"media-9\" does not belong to the composer","path":["composePromoPackage"]}]}`,
			wantMessage: `promo package asset "media-9" does not belong to the composer`,
		},
		{
			name:        "nonexistent_asset",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package asset lookup failed: media not found","path":["composePromoPackage"]}]}`,
			wantMessage: "promo package asset lookup failed: media not found",
		},
		{
			name:        "nonexistent_article",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package article lookup failed: article not found","path":["composePromoPackage"]}]}`,
			wantMessage: "promo package article lookup failed: article not found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newMediaGraphQLStub(t, map[string]string{
				"BodyComposePromoPackage": tc.gqlResponse,
			})
			result, err := handlePromoCompose(promoTestContext(), json.RawMessage(`{"article_id":"https://example.com/articles/1","post_text":"Launching!","visibility":"public","asset_media_ids":["media-9"]}`))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("compose admission failure must fail, got %+v", result)
			}
			errorPayload, _ := result.StructuredContent["error"].(map[string]any)
			if got := errorPayload["code"]; got != promoErrorValidation {
				t.Fatalf("code = %v, want %v", got, promoErrorValidation)
			}
			if got := errorPayload["status"]; got != 422 {
				t.Fatalf("status = %v, want 422", got)
			}
			details, _ := errorPayload["details"].(map[string]any)
			if got := details["message"]; got != tc.wantMessage {
				t.Fatalf("details.message = %v, want %q", got, tc.wantMessage)
			}
			if got := details["lookup"]; got != nil {
				t.Fatalf("validation lane must not carry the package-id lookup mislabel, got %v", got)
			}
		})
	}
}

// TestPromoActorIsolation covers the two isolation seams: a cross-actor read
// maps lesser's not-found to the 404 lane, and a share-grant caller submits
// through the X-Lesser-Act-As seam naming the actor route.
func TestPromoActorIsolation(t *testing.T) {
	t.Run("cross_actor_read_is_not_found", func(t *testing.T) {
		newMediaGraphQLStub(t, map[string]string{
			"BodyPromoPackage": `{"data":{"promoPackage":null}}`,
		})
		result, err := handlePromoState(promoTestContext(), json.RawMessage(`{"package_id":"someone-elses-pkg"}`))
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("cross-actor read must fail, got %+v", result)
		}
		errorPayload, _ := result.StructuredContent["error"].(map[string]any)
		if got := errorPayload["code"]; got != promoErrorNotFound {
			t.Fatalf("code = %v, want %v", got, promoErrorNotFound)
		}
		if got := errorPayload["status"]; got != 404 {
			t.Fatalf("status = %v, want 404", got)
		}
	})

	t.Run("share_caller_submits_via_act_as_seam", func(t *testing.T) {
		stub := newMediaGraphQLStub(t, map[string]string{
			"BodySubmitPromoPackageReview": `{"data":{"submitPromoPackageReview":` + promoReviewFixtureJSON() + `}}`,
		})
		ctx := promoTestContextWithShareCaller("reviewer")
		result, err := handlePromoReviewSubmit(ctx, json.RawMessage(`{"package_id":"pkg-1","verdict":"approved","content_hash":"`+promoContentHash("a")+`"}`))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("share-caller submit failed: result=%+v err=%v", result, err)
		}
		if got := stub.lastHeader(lesserapi.ActAsHeader); got != "agent1" {
			t.Fatalf("X-Lesser-Act-As = %q, want agent1", got)
		}
	})

	t.Run("owner_submit_carries_no_act_as", func(t *testing.T) {
		stub := newMediaGraphQLStub(t, map[string]string{
			"BodySubmitPromoPackageReview": `{"data":{"submitPromoPackageReview":` + promoReviewFixtureJSON() + `}}`,
		})
		result, err := handlePromoReviewSubmit(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1","verdict":"approved","content_hash":"`+promoContentHash("a")+`"}`))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("owner submit failed: result=%+v err=%v", result, err)
		}
		if got := stub.lastHeader(lesserapi.ActAsHeader); got != "" {
			t.Fatalf("owner path must not carry X-Lesser-Act-As, got %q", got)
		}
	})
}

// TestPromoActAsGrantLapseRenders403Lane pins the extensions lane through the
// handler envelope: Lesser's per-request act-as FORBIDDEN (a routine expired or
// revoked share grant) carries structured AppError extensions and must render
// 403 with Lesser's code — mirroring the article surface's
// articleDraftGraphQLErrorContract behavior — never a message-classified
// Unknown/502.
func TestPromoActAsGrantLapseRenders403Lane(t *testing.T) {
	newMediaGraphQLStub(t, map[string]string{
		"BodyPromoPackage": `{"data":null,"errors":[{"message":"no active agent share grant authorizes this caller","path":["promoPackage"],"extensions":{"code":"FORBIDDEN","http_status":403}}]}`,
	})
	result, err := handlePromoState(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("lapsed act-as grant must fail, got %+v", result)
	}
	errorPayload, _ := result.StructuredContent["error"].(map[string]any)
	if got := errorPayload["code"]; got != "FORBIDDEN" {
		t.Fatalf("code = %v, want FORBIDDEN", got)
	}
	if got := errorPayload["status"]; got != 403 {
		t.Fatalf("status = %v, want 403", got)
	}
}

// TestPromoExtensionsErrorSurfacesRealStatus pins the extensions lane for an
// extensions-bearing non-promo AppError: it renders its real status/code, never
// the message-classified Unknown/502 fallback.
func TestPromoExtensionsErrorSurfacesRealStatus(t *testing.T) {
	newMediaGraphQLStub(t, map[string]string{
		"BodyPromoPackage": `{"data":null,"errors":[{"message":"act-as resolution failed","path":["promoPackage"],"extensions":{"code":"INTERNAL","http_status":500}}]}`,
	})
	result, err := handlePromoRead(promoTestContext(), json.RawMessage(`{"package_id":"pkg-1"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("extensions error must fail, got %+v", result)
	}
	errorPayload, _ := result.StructuredContent["error"].(map[string]any)
	if got := errorPayload["code"]; got != "INTERNAL" {
		t.Fatalf("code = %v, want INTERNAL", got)
	}
	if got := errorPayload["status"]; got != 500 {
		t.Fatalf("status = %v, want 500", got)
	}
}
