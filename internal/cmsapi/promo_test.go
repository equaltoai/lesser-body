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

func promoFixturePackageJSON() string {
	return `{"id":"pkg-1","ownerId":"alice","articleId":"https://example.com/articles/1","postText":"Launching!","visibility":"PUBLIC","contentHash":"sha256:` + strings.Repeat("a", 64) + `","status":"DRAFT","releasedStatusId":null,"assets":[{"mediaId":"media-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","publishedUrl":"https://cdn.example.com/media-1.png","state":"PUBLISHED","width":1200,"height":800,"mimeType":"image/png"}],"createdAt":"2026-08-24T12:00:00Z","updatedAt":"2026-08-24T12:00:00Z","review":{"packageId":"pkg-1","contentHash":"sha256:` + strings.Repeat("a", 64) + `","assets":[{"mediaId":"media-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","publishedUrl":"https://cdn.example.com/media-1.png","state":"PUBLISHED"}],"activeReviewerIds":["reviewer"],"releaseEligible":false,"releaseBlockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"reviewer","grantedAt":"2026-08-24T12:05:00Z","expiresAt":"2026-08-31T12:05:00Z","status":"ACTIVE","revokedAt":null}],"verdicts":[{"verdict":"CHANGES_REQUESTED","notes":"revise","contentHash":"sha256:old","reviewerId":"reviewer","recordedAt":"2026-08-24T12:06:00Z","current":false,"stale":true}],"releaseEligibility":{"eligible":false,"blockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}`
}

func promoFixtureReviewJSON() string {
	return `{"packageId":"pkg-1","contentHash":"sha256:` + strings.Repeat("a", 64) + `","assets":[],"activeReviewerIds":["reviewer"],"releaseEligible":false,"releaseBlockingReasons":["PRINCIPAL_APPROVAL_REQUIRED"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false,"grantCount":1,"grantsTruncated":false,"grants":[{"reviewerId":"reviewer","grantedAt":"2026-08-24T12:05:00Z","expiresAt":"2026-08-31T12:05:00Z","status":"ACTIVE","revokedAt":null}],"verdicts":[],"releaseEligibility":{"eligible":false,"blockingReasons":["PRINCIPAL_APPROVAL_REQUIRED"],"reviewersApproved":true,"principalApprovalRequired":true,"principalApproved":false}}`
}

func newPromoTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newTestClient(t, server.URL)
}

func TestComposePromoPackageThreadsContractVariables(t *testing.T) {
	var saw Operation
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"composePromoPackage":` + promoFixturePackageJSON() + `}}`))
	})

	created, err := client.ComposePromoPackage(context.Background(), "token", PromoPackageComposeInput{
		ArticleID:     "https://example.com/articles/1",
		PostText:      "Launching!",
		Visibility:    PromoPackageVisibilityPublic,
		AssetMediaIDs: []string{"media-1", "media-2"},
	})
	if err != nil {
		t.Fatalf("ComposePromoPackage: %v", err)
	}
	if created.ContentHash == "" || created.Status != PromoPackageStatusDraft {
		t.Fatalf("compose result = %+v", created)
	}
	input, _ := saw.Variables["input"].(map[string]any)
	if input["articleId"] != "https://example.com/articles/1" || input["postText"] != "Launching!" {
		t.Fatalf("compose input = %+v", input)
	}
	if input["visibility"] != PromoPackageVisibilityPublic {
		t.Fatalf("compose visibility = %v", input["visibility"])
	}
	assets, _ := input["assetMediaIds"].([]any)
	if len(assets) != 2 || assets[0] != "media-1" || assets[1] != "media-2" {
		t.Fatalf("compose assetMediaIds must preserve order = %+v", assets)
	}
	if _, ok := input["packageId"]; ok {
		t.Fatalf("create compose must not send packageId: %+v", input)
	}
	if !strings.Contains(saw.Query, "composePromoPackage(input: $input)") {
		t.Fatalf("compose query = %s", saw.Query)
	}
}

func TestComposePromoPackageUpdateThreadsPackageID(t *testing.T) {
	var saw Operation
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"composePromoPackage":` + promoFixturePackageJSON() + `}}`))
	})
	if _, err := client.ComposePromoPackage(context.Background(), "token", PromoPackageComposeInput{
		PackageID:     "pkg-1",
		ArticleID:     "https://example.com/articles/1",
		PostText:      "Revised",
		Visibility:    PromoPackageVisibilityUnlisted,
		AssetMediaIDs: []string{"media-1"},
	}); err != nil {
		t.Fatalf("ComposePromoPackage update: %v", err)
	}
	input, _ := saw.Variables["input"].(map[string]any)
	if input["packageId"] != "pkg-1" || input["visibility"] != PromoPackageVisibilityUnlisted {
		t.Fatalf("update compose input = %+v", input)
	}
}

func TestGetPromoPackageParsesReviewerFilteredSurface(t *testing.T) {
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"promoPackage":` + promoFixturePackageJSON() + `}}`))
	})
	pkg, err := client.GetPromoPackage(context.Background(), "token", "pkg-1")
	if err != nil {
		t.Fatalf("GetPromoPackage: %v", err)
	}
	if pkg.ID != "pkg-1" || pkg.Review == nil {
		t.Fatalf("package = %+v", pkg)
	}
	// Reviewer-filtered surface transports verbatim: the grant + stale verdict
	// are the caller-authorized view, exactly as lesser emitted them.
	if len(pkg.Review.Grants) != 1 || pkg.Review.Grants[0].ReviewerID != "reviewer" {
		t.Fatalf("review grants = %+v", pkg.Review.Grants)
	}
	if len(pkg.Review.Verdicts) != 1 || !pkg.Review.Verdicts[0].Stale || pkg.Review.Verdicts[0].Current {
		t.Fatalf("review verdicts = %+v", pkg.Review.Verdicts)
	}
	if len(pkg.Review.ReleaseBlockingReasons) != 1 || pkg.Review.ReleaseBlockingReasons[0] != PromoBlockingReasonApprovalRequired {
		t.Fatalf("blocking reasons = %+v", pkg.Review.ReleaseBlockingReasons)
	}
}

func TestGetPromoPackageNilDataIsNotFound(t *testing.T) {
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"promoPackage":null}}`))
	})
	_, err := client.GetPromoPackage(context.Background(), "token", "pkg-9")
	var notFound *PromoPackageNotFoundError
	if !errors.As(err, &notFound) || notFound.Lookup != "package_id" || notFound.Value != "pkg-9" {
		t.Fatalf("nil package must classify as typed not-found, got %v", err)
	}
}

func TestSharePromoPackageForReviewThreadsReviewer(t *testing.T) {
	var saw Operation
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"sharePromoPackageForReview":` + promoFixtureReviewJSON() + `}}`))
	})
	review, err := client.SharePromoPackageForReview(context.Background(), "token", "pkg-1", "reviewer")
	if err != nil {
		t.Fatalf("SharePromoPackageForReview: %v", err)
	}
	if review.PackageID != "pkg-1" || review.PrincipalApprovalRequired != true {
		t.Fatalf("review = %+v", review)
	}
	if saw.Variables["packageId"] != "pkg-1" || saw.Variables["reviewer"] != "reviewer" {
		t.Fatalf("share variables = %+v", saw.Variables)
	}
}

func TestSubmitPromoPackageReviewRequiresAndThreadsContentHash(t *testing.T) {
	var saw Operation
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"submitPromoPackageReview":` + promoFixtureReviewJSON() + `}}`))
	})
	hash := "sha256:" + strings.Repeat("a", 64)
	review, err := client.SubmitPromoPackageReview(context.Background(), "token", "pkg-1", PromoPackageVerdictApproved, nil, hash)
	if err != nil {
		t.Fatalf("SubmitPromoPackageReview: %v", err)
	}
	if review.ContentHash != hash {
		t.Fatalf("review contentHash = %q", review.ContentHash)
	}
	if saw.Variables["contentHash"] != hash || saw.Variables["verdict"] != PromoPackageVerdictApproved {
		t.Fatalf("submit variables = %+v", saw.Variables)
	}
	if !strings.Contains(saw.Query, "$contentHash: String!") {
		t.Fatalf("submit query must carry the non-null contentHash argument: %s", saw.Query)
	}

	if _, err := client.SubmitPromoPackageReview(context.Background(), "token", "pkg-1", PromoPackageVerdictApproved, nil, "  "); err == nil {
		t.Fatal("missing contentHash must be rejected client-side")
	}
}

func TestReleasePromoPackageSuccessSurface(t *testing.T) {
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"releasePromoPackage":{"package":` + promoFixturePackageJSON() + `,"statusId":"status-1","url":"status-1"}}}`))
	})
	result, err := client.ReleasePromoPackage(context.Background(), "token", "pkg-1")
	if err != nil {
		t.Fatalf("ReleasePromoPackage: %v", err)
	}
	if result.StatusID != "status-1" || result.Package == nil || result.URL == nil || *result.URL != "status-1" {
		t.Fatalf("release result = %+v", result)
	}
}

func TestClassifyPromoReleaseErrorLanes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		gqlResponse  string
		wantClass    PromoPackageErrorClass
		wantStatusID string
		wantReasons  []string
	}{
		{
			name:         "stamp_failure_surfaces_status_id",
			gqlResponse:  `{"data":null,"errors":[{"message":"promo package release created status status-42 but could not stamp it: dynamo condition failed","path":["releasePromoPackage"]}]}`,
			wantClass:    PromoPackageErrorStampFailed,
			wantStatusID: "status-42",
		},
		{
			name:        "releasing_reservation",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package release is already in progress","path":["releasePromoPackage"]}]}`,
			wantClass:   PromoPackageErrorReleasing,
		},
		{
			name:        "approval_required_with_blocking_reasons",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package requires approval from every required reviewer\nblocking reasons: REVIEW_APPROVAL_REQUIRED, ASSET_DIGEST_CHANGED","path":["releasePromoPackage"]}]}`,
			wantClass:   PromoPackageErrorApprovalRequired,
			wantReasons: []string{PromoBlockingReasonApprovalRequired, PromoBlockingReasonAssetDigestChange},
		},
		{
			name:        "principal_approval_required",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package release requires an active approval from the instance principal\nblocking reasons: PRINCIPAL_APPROVAL_REQUIRED","path":["releasePromoPackage"]}]}`,
			wantClass:   PromoPackageErrorPrincipalApprovalRequired,
			wantReasons: []string{PromoBlockingReasonPrincipalRequired},
		},
		{
			name:        "asset_unavailable_with_reasons",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package asset cannot serve the exact approved bytes: ASSET_MISSING, ASSET_NOT_PUBLISHED","path":["releasePromoPackage"]}]}`,
			wantClass:   PromoPackageErrorAssetUnavailable,
			wantReasons: []string{PromoBlockingReasonAssetMissing, PromoBlockingReasonAssetNotPublished},
		},
		{
			name:        "already_released",
			gqlResponse: `{"data":null,"errors":[{"message":"promo package is already released","path":["releasePromoPackage"]}]}`,
			wantClass:   PromoPackageErrorAlreadyReleased,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.gqlResponse))
			})
			_, err := client.ReleasePromoPackage(context.Background(), "token", "pkg-1")
			var classified *PromoPackageClassifiedError
			if !errors.As(err, &classified) {
				t.Fatalf("expected classified promo error, got %T: %v", err, err)
			}
			if classified.Class != tc.wantClass {
				t.Fatalf("class = %v, want %v", classified.Class, tc.wantClass)
			}
			if classified.StatusID != tc.wantStatusID {
				t.Fatalf("statusID = %q, want %q", classified.StatusID, tc.wantStatusID)
			}
			if len(classified.BlockingReasons) != len(tc.wantReasons) {
				t.Fatalf("blocking reasons = %v, want %v", classified.BlockingReasons, tc.wantReasons)
			}
			for i := range tc.wantReasons {
				if classified.BlockingReasons[i] != tc.wantReasons[i] {
					t.Fatalf("blocking reasons = %v, want %v", classified.BlockingReasons, tc.wantReasons)
				}
			}
		})
	}
}

func TestClassifyPromoSubmitContentChangedConflict(t *testing.T) {
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"promo package content changed since the reviewer inspected it","path":["submitPromoPackageReview"]}]}`))
	})
	_, err := client.SubmitPromoPackageReview(context.Background(), "token", "pkg-1", PromoPackageVerdictApproved, nil, "sha256:stale")
	var classified *PromoPackageClassifiedError
	if !errors.As(err, &classified) || classified.Class != PromoPackageErrorContentChanged {
		t.Fatalf("submit-hash mismatch must classify as content-changed conflict, got %v", err)
	}
}

func TestClassifyPromoOwnerSelfReview(t *testing.T) {
	client := newPromoTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"promo package owner cannot review their own package","path":["submitPromoPackageReview"]}]}`))
	})
	_, err := client.SubmitPromoPackageReview(context.Background(), "token", "pkg-1", PromoPackageVerdictApproved, nil, "sha256:x")
	var classified *PromoPackageClassifiedError
	if !errors.As(err, &classified) || classified.Class != PromoPackageErrorOwnerSelfReview {
		t.Fatalf("owner self-review must classify distinctly, got %v", err)
	}
}
