package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

const (
	promoDefaultBudgetBytes = 24000

	// Lesser's notes size limit, mirrored as a fail-fast client-side cap.
	promoMaxPostTextBytes = 5000

	// Envelope states for the promo package lifecycle (the theory-report
	// vocabulary). Each state is derived strictly from Lesser-authoritative
	// fields; the mapping table is pinned in TestPromoEnvelopeStateMapping.
	//
	// "approved" is DRAFT with a current release eligibility: every required
	// reviewer approval, principal approval where required, and asset binding
	// are current for the exact reviewed content. "releasing" is the transient
	// release reservation — release and composition are refused until an
	// operator reconciles it (never retryable).
	promoStateDraft     = "draft"
	promoStateApproved  = "approved"
	promoStateReleasing = "releasing"
	promoStateReleased  = "released"
	promoStateUnknown   = "unknown"

	// Envelope error codes for the promo lanes.
	promoErrorNotFound          = "promo_not_found"
	promoErrorReconcileRequired = "promo_release_reconcile_required"
	promoErrorApprovalRequired  = "promo_release_approval_required"
	promoErrorPrincipalRequired = "promo_release_principal_approval_required"
	promoErrorAssetUnavailable  = "promo_release_asset_unavailable"
	promoErrorAlreadyReleased   = "promo_already_released"
	promoErrorContentChanged    = "promo_review_content_changed"
	promoErrorConflict          = "promo_conflict"
	promoErrorOwnerSelfReview   = "promo_owner_self_review"
	promoErrorValidation        = "promo_validation"

	// The promo package release recovery runbook path cited on the reconcile
	// lane (docs/operations/promo-package-release-recovery-runbook.md in the
	// lesser repo, which owns the reconciliation writes).
	promoRunbookPath = "docs/operations/promo-package-release-recovery-runbook.md"

	// Operator content doctrine (2026-08-24, binding). No content shall be
	// published by agents without principal approval; additional approvals,
	// once requested, are also required. Body transports the doctrine —
	// lesser enforces it at the release gate.
	promoContentDoctrine = "No content shall be published by agents without principal approval; additional approvals, once requested, are also required."
)

var promoContentHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func registerPromoTools(r *mcpruntime.ToolRegistry) error {
	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: promoComposeDef(), Handler: handlePromoCompose},
		{Def: promoReviewShareDef(), Handler: handlePromoReviewShare},
		{Def: promoReviewSubmitDef(), Handler: handlePromoReviewSubmit},
		{Def: promoStateDef(), Handler: handlePromoState},
		{Def: promoReleaseDef(), Handler: handlePromoRelease},
		{Def: promoReadDef(), Handler: handlePromoRead},
	} {
		if err := registerTool(r, tool.Def, tool.Handler); err != nil {
			return err
		}
	}
	return nil
}

func promoComposeDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_compose",
		Description:  "Create a promo package or replace its content through Lesser's composePromoPackage contract: outbound post text (public/unlisted only), a published-article reference, and an ORDERED set of PUBLISHED media asset IDs (attachment order). Every content change re-hashes the package and stales prior approvals, so release stays blocked until the changed package is re-reviewed and re-authorized. Compose only stages content — it never publishes. " + promoContentDoctrine + " Lesser owns admission (published-article ref, notes size limit, PUBLISHED-only assets); returns the new contentHash.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: promoComposeOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Existing package id to replace content of; omit to create a new package. Replacing content re-hashes and stales prior approvals."},
				"article_id":{"type":"string","description":"The published article this package promotes (Lesser's canonical object URL)."},
				"post_text":{"type":"string","description":"The outbound post content; max 5000 bytes (Lesser's notes limit)."},
				"visibility":{"type":"string","enum":["public","unlisted"],"description":"Outbound post visibility; public or unlisted only (promo attachment is structurally scoped to public/unlisted)."},
				"asset_media_ids":{"type":"array","items":{"type":"string"},"minItems":1,"description":"ORDERED PUBLISHED media asset ids; order is the attachment order on the post. Reordering re-requires review."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["article_id","post_text","visibility","asset_media_ids"],
			"additionalProperties":false
		}`),
	}
}

func promoReviewShareDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_review_share",
		Description:  "Share a promo package with one reviewer through Lesser's sharePromoPackageForReview contract, creating or refreshing the revocable 7-day review grant. " + promoContentDoctrine + " Once requested, the reviewer's approval is REQUIRED for release — revocation cannot delete a required approval.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: promoReviewShareOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Promo package id owned by the authenticated actor."},
				"reviewer":{"type":"string","description":"Lesser reviewer username to grant."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["package_id","reviewer"],
			"additionalProperties":false
		}`),
	}
}

func promoReviewSubmitDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_review_submit",
		Description:  "Record a hash-bound reviewer verdict on a shared promo package through Lesser's submitPromoPackageReview contract. content_hash is REQUIRED and must carry the contentHash the reviewer actually inspected (the hash promo_state/promo_read returned): a recomposed package (hash mismatch) rejects the submit with a conflict instead of blessing unseen content. APPROVED or CHANGES_REQUESTED are the only verdicts. " + promoContentDoctrine,
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: promoReviewSubmitOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Promo package id shared for review with the authenticated reviewer."},
				"verdict":{"type":"string","enum":["approved","changes_requested"],"description":"Lesser's canonical promo review verdict."},
				"content_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$","description":"The contentHash the reviewer actually inspected (returned by promo_state/promo_read). REQUIRED — Lesser's argument is non-null; a mismatch rejects the submit with a conflict."},
				"notes":{"type":"string","description":"Optional reviewer notes."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["package_id","verdict","content_hash"],
			"additionalProperties":false
		}`),
	}
}

func promoStateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_state",
		Description:  "Inspect one promo package's lifecycle state and approval matrix through Lesser's promoPackage query (owner or active reviewer grant). Surfaces the envelope state (draft/approved/releasing/released), status, the complete review surface — contentHash, active reviewers, grants and verdicts, release eligibility, and the blocking reasons (REVIEW_APPROVAL_REQUIRED / PRINCIPAL_APPROVAL_REQUIRED / PRINCIPAL_APPROVAL_UNAVAILABLE / ASSET_MISSING / ASSET_NOT_PUBLISHED / ASSET_DIGEST_CHANGED / PACKAGE_RELEASED / PACKAGE_RELEASING). Lesser filters grants/verdicts owner-or-self for non-owners; Body transports that filtered surface verbatim. PACKAGE_RELEASED means the package is already released (the review projection is read-only post-release); PACKAGE_RELEASING means a release is mid-flight or crashed — operator reconciliation, never retry.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: promoStateOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Promo package id (owner or active reviewer grant)."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["package_id"],
			"additionalProperties":false
		}`),
	}
}

func promoReleaseDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_release",
		Description:  "Release an approved promo package through Lesser's releasePromoPackage contract: creates the outbound public/unlisted post with the exact approved PUBLISHED assets and AI-authorship disclosure intact. " + promoContentDoctrine + " Release is blocked with explicit reasons until every required reviewer approval and (where required) the instance principal's approval are current for the exact reviewed content. A failed release never blindly retries: if the post WAS created but the package could not be stamped, the surfaced statusId means the post exists — the error presents operator reconciliation (see the promo package release recovery runbook), never retry-safe semantics.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: promoReleaseOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Promo package id owned by the authenticated actor."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["package_id"],
			"additionalProperties":false
		}`),
	}
}

func promoReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "promo_read",
		Description:  "Read a released promo package and its outbound post reference through Lesser's promoPackage query (owner or active reviewer grant): the full package (post text, visibility, contentHash, assets), the released status id (the outbound post created by the release transition), and the review surface. A package that is not yet released returns its current lifecycle state instead. Follow the released status id with post_get to expand the outbound post.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: promoReadOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"package_id":{"type":"string","description":"Promo package id (owner or active reviewer grant)."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["package_id"],
			"additionalProperties":false
		}`),
	}
}

func promoCMS(ctx context.Context) (*cmsapi.Client, error) {
	_ = ctx
	return cmsapi.Default()
}

func promoOutputBudget(requested int) int {
	if requested > 0 {
		return requested
	}
	return promoDefaultBudgetBytes
}

func handlePromoCompose(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PackageID      string   `json:"package_id,omitempty"`
		ArticleID      string   `json:"article_id"`
		PostText       string   `json:"post_text"`
		Visibility     string   `json:"visibility"`
		AssetMediaIDs  []string `json:"asset_media_ids"`
		MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PackageID = strings.TrimSpace(in.PackageID)
	in.ArticleID = strings.TrimSpace(in.ArticleID)
	in.PostText = strings.TrimSpace(in.PostText)
	in.Visibility = strings.ToUpper(strings.TrimSpace(in.Visibility))
	if in.ArticleID == "" {
		return nil, invalidParams("article_id is required")
	}
	if in.PostText == "" {
		return nil, invalidParams("post_text is required")
	}
	if len([]byte(in.PostText)) > promoMaxPostTextBytes {
		return nil, invalidParams(fmt.Sprintf("post_text must not exceed %d bytes (Lesser's notes limit)", promoMaxPostTextBytes))
	}
	if in.Visibility != cmsapi.PromoPackageVisibilityPublic && in.Visibility != cmsapi.PromoPackageVisibilityUnlisted {
		return nil, invalidParams("visibility must be public or unlisted (promo attachment is scoped to public/unlisted posts)")
	}
	if len(in.AssetMediaIDs) == 0 {
		return nil, invalidParams("asset_media_ids must name at least one PUBLISHED asset")
	}
	seen := make(map[string]struct{}, len(in.AssetMediaIDs))
	for i := range in.AssetMediaIDs {
		in.AssetMediaIDs[i] = strings.TrimSpace(in.AssetMediaIDs[i])
		if in.AssetMediaIDs[i] == "" {
			return nil, invalidParams("asset_media_ids must not contain empty ids")
		}
		if _, dup := seen[in.AssetMediaIDs[i]]; dup {
			return nil, invalidParams("asset_media_ids must not contain duplicates (the ordered binding set is a set)")
		}
		seen[in.AssetMediaIDs[i]] = struct{}{}
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := promoCMS(ctx)
	if err != nil {
		return nil, err
	}
	pkg, err := client.ComposePromoPackage(ctx, token, cmsapi.PromoPackageComposeInput{
		PackageID:     in.PackageID,
		ArticleID:     in.ArticleID,
		PostText:      in.PostText,
		Visibility:    strings.ToUpper(in.Visibility),
		AssetMediaIDs: in.AssetMediaIDs,
	})
	if err != nil {
		return promoToolResultFromError("promo_compose", err)
	}
	return promoComposeResult(pkg, promoOutputBudget(in.MaxOutputBytes))
}

func handlePromoReviewShare(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PackageID      string `json:"package_id"`
		Reviewer       string `json:"reviewer"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PackageID = strings.TrimSpace(in.PackageID)
	in.Reviewer = strings.TrimSpace(in.Reviewer)
	if in.PackageID == "" {
		return nil, invalidParams("package_id is required")
	}
	if in.Reviewer == "" {
		return nil, invalidParams("reviewer is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := promoCMS(ctx)
	if err != nil {
		return nil, err
	}
	review, err := client.SharePromoPackageForReview(ctx, token, in.PackageID, in.Reviewer)
	if err != nil {
		return promoToolResultFromError("promo_review_share", err)
	}
	return promoReviewShareResult(review, promoOutputBudget(in.MaxOutputBytes))
}

func handlePromoReviewSubmit(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PackageID      string  `json:"package_id"`
		Verdict        string  `json:"verdict"`
		ContentHash    string  `json:"content_hash"`
		Notes          *string `json:"notes,omitempty"`
		MaxOutputBytes int     `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PackageID = strings.TrimSpace(in.PackageID)
	in.Verdict = strings.ToUpper(strings.TrimSpace(in.Verdict))
	in.ContentHash = strings.TrimSpace(in.ContentHash)
	if in.PackageID == "" {
		return nil, invalidParams("package_id is required")
	}
	if in.Verdict != cmsapi.PromoPackageVerdictApproved && in.Verdict != cmsapi.PromoPackageVerdictChangesRequested {
		return nil, invalidParams("verdict must be approved or changes_requested")
	}
	if in.ContentHash == "" {
		return nil, invalidParams("content_hash is required: carry the contentHash the reviewer actually inspected (returned by promo_state/promo_read)")
	}
	if !promoContentHashPattern.MatchString(in.ContentHash) {
		return nil, invalidParams("content_hash must be a canonical sha256:<64-lowercase-hex> digest")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := promoCMS(ctx)
	if err != nil {
		return nil, err
	}
	review, err := client.SubmitPromoPackageReview(ctx, token, in.PackageID, in.Verdict, in.Notes, in.ContentHash)
	if err != nil {
		return promoToolResultFromError("promo_review_submit", err)
	}
	return promoReviewSubmitResult(review, promoOutputBudget(in.MaxOutputBytes))
}

func handlePromoState(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return handlePromoStateOrRead(ctx, args, "promo_state")
}

func handlePromoRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return handlePromoStateOrRead(ctx, args, "promo_read")
}

func handlePromoStateOrRead(ctx context.Context, args json.RawMessage, toolName string) (*mcpruntime.ToolResult, error) {
	var in struct {
		PackageID      string `json:"package_id"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PackageID = strings.TrimSpace(in.PackageID)
	if in.PackageID == "" {
		return nil, invalidParams("package_id is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := promoCMS(ctx)
	if err != nil {
		return nil, err
	}
	pkg, err := client.GetPromoPackage(ctx, token, in.PackageID)
	if err != nil {
		return promoToolResultFromError(toolName, err)
	}
	budget := promoOutputBudget(in.MaxOutputBytes)
	if toolName == "promo_read" {
		return promoReadResult(pkg, budget)
	}
	return promoStateResult(pkg, budget)
}

func handlePromoRelease(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PackageID      string `json:"package_id"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PackageID = strings.TrimSpace(in.PackageID)
	if in.PackageID == "" {
		return nil, invalidParams("package_id is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := promoCMS(ctx)
	if err != nil {
		return nil, err
	}
	result, err := client.ReleasePromoPackage(ctx, token, in.PackageID)
	if err != nil {
		return promoToolResultFromError("promo_release", err)
	}
	return promoReleaseResult(result, promoOutputBudget(in.MaxOutputBytes))
}

// promoEnvelopeStateForPackage maps Lesser's package status plus the current
// release eligibility onto the envelope vocabulary. The mapping table is
// pinned in TestPromoEnvelopeStateMapping.
func promoEnvelopeStateForPackage(pkg *cmsapi.PromoPackage) string {
	if pkg == nil {
		return promoStateUnknown
	}
	switch pkg.Status {
	case cmsapi.PromoPackageStatusReleasing:
		return promoStateReleasing
	case cmsapi.PromoPackageStatusReleased:
		return promoStateReleased
	case cmsapi.PromoPackageStatusDraft:
		if pkg.Review != nil && pkg.Review.ReleaseEligible {
			return promoStateApproved
		}
		return promoStateDraft
	default:
		return promoStateUnknown
	}
}

// promoBlockingReasons returns the release-blocking reasons for a package,
// ensuring the PACKAGE_RELEASING reason is present while the reservation is
// held even if the review projection omits it.
func promoBlockingReasons(pkg *cmsapi.PromoPackage) []string {
	if pkg == nil {
		return nil
	}
	var reasons []string
	if pkg.Review != nil {
		reasons = append(reasons, pkg.Review.ReleaseBlockingReasons...)
	}
	if pkg.Status == cmsapi.PromoPackageStatusReleasing {
		found := false
		for _, reason := range reasons {
			if reason == cmsapi.PromoBlockingReasonReleasing {
				found = true
				break
			}
		}
		if !found {
			reasons = append(reasons, cmsapi.PromoBlockingReasonReleasing)
		}
	}
	return reasons
}

func promoComposeResult(pkg *cmsapi.PromoPackage, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("promo_compose returned no package")
	}
	payload := map[string]any{
		"tool":        "promo_compose",
		"operation":   "composed",
		"source":      "lesser_cms_graphql",
		"packageId":   pkg.ID,
		"contentHash": pkg.ContentHash,
		"package":     pkg,
		"guidance":    "Content staged. Every content change re-hashes the package and stales prior approvals; release stays blocked until the changed package is re-reviewed and re-authorized. " + promoContentDoctrine,
	}
	return promoStructuredResult("promo_compose", "composed",
		"Promo package composed; keep the contentHash for review and release", payload, maxOutputBytes)
}

func promoReviewShareResult(review *cmsapi.PromoPackageReview, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if review == nil {
		return nil, fmt.Errorf("promo_review_share returned no review")
	}
	payload := map[string]any{
		"tool":      "promo_review_share",
		"operation": "shared",
		"source":    "lesser_cms_graphql",
		"packageId": review.PackageID,
		"review":    review,
		"guidance":  "Once requested, the reviewer's approval is REQUIRED for release — revocation cannot delete a required approval. " + promoContentDoctrine,
	}
	return promoStructuredResult("promo_review_share", "shared",
		"Promo package shared for review", payload, maxOutputBytes)
}

func promoReviewSubmitResult(review *cmsapi.PromoPackageReview, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if review == nil {
		return nil, fmt.Errorf("promo_review_submit returned no review")
	}
	payload := map[string]any{
		"tool":        "promo_review_submit",
		"operation":   "verdict_submitted",
		"source":      "lesser_cms_graphql",
		"packageId":   review.PackageID,
		"contentHash": review.ContentHash,
		"review":      review,
	}
	return promoStructuredResult("promo_review_submit", "verdict_submitted",
		"Promo package review verdict recorded", payload, maxOutputBytes)
}

func promoStateResult(pkg *cmsapi.PromoPackage, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("promo_state returned no package")
	}
	state := promoEnvelopeStateForPackage(pkg)
	payload := map[string]any{
		"tool":      "promo_state",
		"operation": "state",
		"source":    "lesser_cms_graphql",
		"packageId": pkg.ID,
		"state":     state,
		"status":    pkg.Status,
		"package":   pkg,
	}
	if reasons := promoBlockingReasons(pkg); len(reasons) > 0 {
		payload["blockingReasons"] = reasons
	}
	if state == promoStateReleasing {
		payload["guidance"] = "PACKAGE_RELEASING: a release is mid-flight or crashed between reservation and stamp. Release and composition are refused. Do NOT retry — an operator must reconcile the reservation per " + promoRunbookPath + "."
	}
	return promoStructuredResult("promo_state", "state",
		"Promo package state: "+state, payload, maxOutputBytes)
}

func promoReadResult(pkg *cmsapi.PromoPackage, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("promo_read returned no package")
	}
	state := promoEnvelopeStateForPackage(pkg)
	payload := map[string]any{
		"tool":      "promo_read",
		"operation": "read",
		"source":    "lesser_cms_graphql",
		"packageId": pkg.ID,
		"state":     state,
		"status":    pkg.Status,
		"package":   pkg,
	}
	if pkg.ReleasedStatusID != nil && strings.TrimSpace(*pkg.ReleasedStatusID) != "" {
		payload["releasedStatusId"] = *pkg.ReleasedStatusID
		payload["outboundPost"] = map[string]any{"statusId": *pkg.ReleasedStatusID}
		payload["guidance"] = "The outbound post is live. Expand the released status id with post_get({\"id\":\"<releasedStatusId>\"}) to read the post."
	} else if state == promoStateReleased {
		payload["guidance"] = "Released; status id unavailable — lesser-side anomaly, see state details."
	} else if state == promoStateReleasing {
		payload["guidance"] = "PACKAGE_RELEASING: a release is mid-flight or crashed between reservation and stamp. Do NOT retry — an operator must reconcile the reservation per " + promoRunbookPath + "."
	} else {
		payload["guidance"] = "This package is not released yet (state " + state + "). No outbound post exists."
	}
	return promoStructuredResult("promo_read", "read",
		"Promo package read: "+state, payload, maxOutputBytes)
}

func promoReleaseResult(result *cmsapi.PromoPackageReleaseResult, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if result == nil || result.Package == nil {
		return nil, fmt.Errorf("promo_release returned no package")
	}
	payload := map[string]any{
		"tool":      "promo_release",
		"operation": "released",
		"source":    "lesser_cms_graphql",
		"packageId": result.Package.ID,
		"statusId":  result.StatusID,
		"package":   result.Package,
		"guidance":  "The outbound public/unlisted post is live with the exact approved PUBLISHED assets. AI-origin assets carry the instance principal's authorization as agent attribution. Expand the post with post_get({\"id\":\"" + result.StatusID + "\"}).",
	}
	if result.URL != nil {
		payload["url"] = *result.URL
	}
	return promoStructuredResult("promo_release", "released",
		"Promo package released; outbound post created", payload, maxOutputBytes)
}

func promoStructuredResult(toolName, operation, summary string, payload map[string]any, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: summary,
		Data:    payload,
		Text: map[string]any{
			"tool":      toolName,
			"operation": operation,
		},
	})
	if err != nil {
		return nil, err
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	return toolErrorResult("response_too_large", toolName+" response exceeds max_output_bytes", 413, map[string]any{
		"tool":                   toolName,
		"operation":              operation,
		"measuredBytes":          measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":         maxOutputBytes,
		"contentTextBytes":       measurement.ContentTextBytes,
		"structuredContentBytes": measurement.StructuredContentBytes,
		"guidance":               "increase max_output_bytes",
	})
}

// promoToolResultFromError maps promo-surface failures onto the shared
// structured-error envelope.
func promoToolResultFromError(toolName string, err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var notFound *cmsapi.PromoPackageNotFoundError
	if errors.As(err, &notFound) {
		return toolErrorResult(promoErrorNotFound, "Promo package not found or not authorized", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": notFound.Lookup,
			"value":  notFound.Value,
		})
	}
	var classified *cmsapi.PromoPackageClassifiedError
	if errors.As(err, &classified) {
		return promoClassifiedErrorResult(toolName, classified)
	}
	var gqlErr *cmsapi.GraphQLErrors
	if errors.As(err, &gqlErr) {
		details := map[string]any{"source": "lesser_cms_graphql", "tool": toolName}
		if len(gqlErr.Errors) > 0 {
			details["graphqlErrors"] = gqlErr.Errors
		}
		code, status := articleDraftGraphQLErrorContract(gqlErr)
		return toolErrorResult(code, "Lesser CMS GraphQL returned errors", status, details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		return toolErrorResult("lesser_cms_http_error", "Lesser CMS API request failed", apiErr.Status, map[string]any{
			"source":       "lesser_cms_graphql",
			"tool":         toolName,
			"upstreamCode": apiErr.Status,
		})
	}
	return normalizedToolResultFromError(toolName, err)
}

// promoClassifiedErrorResult maps lesser's classified promo failures onto the
// envelope vocabulary. The stamp-failure and releasing-reservation lanes are
// operator reconciliation: never retryable, with the runbook cited and the
// surfaced status ID presented when a post exists.
func promoClassifiedErrorResult(toolName string, classified *cmsapi.PromoPackageClassifiedError) (*mcpruntime.ToolResult, error) {
	switch classified.Class {
	case cmsapi.PromoPackageErrorNotFound:
		return toolErrorResult(promoErrorNotFound, "Promo package not found or not authorized", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": "package_id",
		})
	case cmsapi.PromoPackageErrorStampFailed:
		details := map[string]any{
			"source":    "lesser_cms_graphql",
			"tool":      toolName,
			"wedge":     "stamp_failed",
			"statusId":  classified.StatusID,
			"runbook":   promoRunbookPath,
			"retryable": false,
			"guidance":  "The release created the outbound post but could not stamp the package. The surfaced statusId means the post EXISTS. Do NOT retry release — a retry creates a second public post. An operator must reconcile the releasing reservation per the runbook (wedge A: stamp to released using the surfaced status ID).",
		}
		return toolErrorResult(promoErrorReconcileRequired,
			"Promo package release created a post but the stamp failed; operator reconciliation required", http.StatusConflict, details)
	case cmsapi.PromoPackageErrorReleasing:
		details := map[string]any{
			"source":         "lesser_cms_graphql",
			"tool":           toolName,
			"wedge":          "releasing_reservation",
			"runbook":        promoRunbookPath,
			"retryable":      false,
			"blockingReason": cmsapi.PromoBlockingReasonReleasing,
			"guidance":       "The package is in the transient releasing reservation (a release is mid-flight or crashed between reservation and stamp). Do NOT retry release — it could create a second public post. An operator must reconcile the reservation per the runbook.",
		}
		return toolErrorResult(promoErrorReconcileRequired,
			"Promo package release is in progress; operator reconciliation required", http.StatusConflict, details)
	case cmsapi.PromoPackageErrorApprovalRequired:
		details := map[string]any{
			"source":    "lesser_cms_graphql",
			"tool":      toolName,
			"retryable": true,
			"guidance":  "Release is blocked until every required reviewer approval is current for the exact reviewed content. Read promo_state for the blocking reasons, then re-review or re-compose.",
		}
		if len(classified.BlockingReasons) > 0 {
			details["blockingReasons"] = classified.BlockingReasons
		}
		return toolErrorResult(promoErrorApprovalRequired,
			"Promo package release requires approval from every required reviewer", http.StatusConflict, details)
	case cmsapi.PromoPackageErrorPrincipalApprovalRequired:
		details := map[string]any{
			"source":    "lesser_cms_graphql",
			"tool":      toolName,
			"retryable": true,
			"guidance":  "A non-principal release requires an active current approval from the instance principal (the operator content doctrine). Read promo_state for the approval matrix.",
		}
		if len(classified.BlockingReasons) > 0 {
			details["blockingReasons"] = classified.BlockingReasons
		}
		return toolErrorResult(promoErrorPrincipalRequired,
			"Promo package release requires an active approval from the instance principal", http.StatusConflict, details)
	case cmsapi.PromoPackageErrorAssetUnavailable:
		details := map[string]any{
			"source":    "lesser_cms_graphql",
			"tool":      toolName,
			"retryable": true,
			"guidance":  "An asset can no longer serve the exact approved bytes (missing, not published, or digest changed). Read promo_state for the ASSET_* blocking reasons; re-compose or re-review before releasing.",
		}
		if len(classified.BlockingReasons) > 0 {
			details["blockingReasons"] = classified.BlockingReasons
		}
		return toolErrorResult(promoErrorAssetUnavailable,
			"Promo package asset cannot serve the exact approved bytes", http.StatusConflict, details)
	case cmsapi.PromoPackageErrorAlreadyReleased:
		return toolErrorResult(promoErrorAlreadyReleased,
			"Promo package is already released; re-release is refused", http.StatusConflict, map[string]any{
				"source":    "lesser_cms_graphql",
				"tool":      toolName,
				"retryable": false,
				"guidance":  "The package is released. Read it with promo_read; the released status id is the outbound post reference.",
			})
	case cmsapi.PromoPackageErrorContentChanged:
		return toolErrorResult(promoErrorContentChanged,
			"Promo package content changed since the reviewer inspected it; the verdict was not recorded", http.StatusConflict, map[string]any{
				"source":    "lesser_cms_graphql",
				"tool":      toolName,
				"retryable": true,
				"guidance":  "The package was recomposed after inspection. Re-read the current contentHash with promo_state/promo_read, inspect the new content, and submit again with that hash.",
			})
	case cmsapi.PromoPackageErrorConflict:
		return toolErrorResult(promoErrorConflict,
			"Promo package changed concurrently; retry after re-reading state", http.StatusConflict, map[string]any{
				"source":    "lesser_cms_graphql",
				"tool":      toolName,
				"retryable": true,
			})
	case cmsapi.PromoPackageErrorOwnerSelfReview:
		return toolErrorResult(promoErrorOwnerSelfReview,
			"Promo package owner cannot review their own package", http.StatusUnprocessableEntity, map[string]any{
				"source":    "lesser_cms_graphql",
				"tool":      toolName,
				"retryable": false,
			})
	case cmsapi.PromoPackageErrorValidation:
		return toolErrorResult(promoErrorValidation,
			"Lesser rejected the promo package request", http.StatusUnprocessableEntity, map[string]any{
				"source":    "lesser_cms_graphql",
				"tool":      toolName,
				"message":   classified.Message,
				"retryable": true,
				"guidance":  "Correct the request (published-article reference, notes size limit, public/unlisted visibility, PUBLISHED-only ordered asset ids) and retry.",
			})
	case cmsapi.PromoPackageErrorUnavailable:
		details := map[string]any{
			"source":   "lesser_cms_graphql",
			"tool":     toolName,
			"upstream": classified.Message,
		}
		return toolErrorResult("lesser_cms_graphql_error", "Lesser CMS GraphQL returned errors", http.StatusBadGateway, details)
	default:
		details := map[string]any{
			"source":   "lesser_cms_graphql",
			"tool":     toolName,
			"upstream": classified.Message,
		}
		return toolErrorResult("lesser_cms_graphql_error", "Lesser CMS GraphQL returned errors", http.StatusBadGateway, details)
	}
}
