package cmsapi

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Canonical promo package lifecycle statuses (Lesser's PromoPackageStatus enum).
const (
	PromoPackageStatusDraft     = "DRAFT"
	PromoPackageStatusReleasing = "RELEASING"
	PromoPackageStatusReleased  = "RELEASED"
)

// Canonical promo package visibility (Lesser's PromoPackageVisibility enum).
const (
	PromoPackageVisibilityPublic   = "PUBLIC"
	PromoPackageVisibilityUnlisted = "UNLISTED"
)

// Canonical promo review verdicts (Lesser's PromoPackageReviewVerdict enum).
const (
	PromoPackageVerdictApproved         = "APPROVED"
	PromoPackageVerdictChangesRequested = "CHANGES_REQUESTED"
)

// Canonical promo package asset states (Lesser's PromoPackageAssetState enum).
const (
	PromoPackageAssetStatePublished   = "PUBLISHED"
	PromoPackageAssetStateMissing     = "MISSING"
	PromoPackageAssetStateWithdrawn   = "WITHDRAWN"
	PromoPackageAssetStateSuperseded  = "SUPERSEDED"
	PromoPackageAssetStateUnavailable = "UNAVAILABLE"
	PromoPackageAssetStateRejected    = "REJECTED"
)

// Canonical promo review grant statuses (Lesser's PromoPackageGrantStatus enum).
const (
	PromoPackageGrantActive  = "ACTIVE"
	PromoPackageGrantRevoked = "REVOKED"
	PromoPackageGrantExpired = "EXPIRED"
)

// Promo blocking-reason constants (Lesser's release-gate vocabulary, surfaced
// verbatim in releaseEligibility.blockingReasons and release error details).
const (
	PromoBlockingReasonApprovalRequired  = "REVIEW_APPROVAL_REQUIRED"
	PromoBlockingReasonPrincipalRequired = "PRINCIPAL_APPROVAL_REQUIRED"
	PromoBlockingReasonAssetMissing      = "ASSET_MISSING"
	PromoBlockingReasonAssetNotPublished = "ASSET_NOT_PUBLISHED"
	PromoBlockingReasonAssetDigestChange = "ASSET_DIGEST_CHANGED"
	PromoBlockingReasonReleasing         = "PACKAGE_RELEASING"
)

// PromoBlockingReasons is the bounded set of blocking-reason constants body
// recognizes when extracting them from release error text.
var PromoBlockingReasons = []string{
	PromoBlockingReasonApprovalRequired,
	PromoBlockingReasonPrincipalRequired,
	PromoBlockingReasonAssetMissing,
	PromoBlockingReasonAssetNotPublished,
	PromoBlockingReasonAssetDigestChange,
	PromoBlockingReasonReleasing,
}

// PromoPackageNotFoundError reports an unknown or unauthorized promo package
// lookup. Lookup names the caller-visible input field (`package_id`).
type PromoPackageNotFoundError struct {
	Lookup string
	Value  string
}

func (e *PromoPackageNotFoundError) Error() string {
	if e == nil {
		return "promo package not found"
	}
	return fmt.Sprintf("promo package %q not found", e.Value)
}

// PromoPackageAsset is one ordered package binding: the media id, the canonical
// sha256 digest bound at review time, the M2 durable published serving
// snapshot, and Lesser's resolved asset state.
type PromoPackageAsset struct {
	MediaID      string                    `json:"mediaId"`
	ContentHash  *string                   `json:"contentHash,omitempty"`
	PublishedURL *string                   `json:"publishedUrl,omitempty"`
	State        string                    `json:"state"`
	Width        *int                      `json:"width,omitempty"`
	Height       *int                      `json:"height,omitempty"`
	MimeType     *string                   `json:"mimeType,omitempty"`
	Provenance   *EditorialMediaProvenance `json:"provenance,omitempty"`
}

// PromoPackageReviewGrant is Lesser's reviewer grant projection (7-day bounded
// expiry, fail-closed like the M2 grants).
type PromoPackageReviewGrant struct {
	ReviewerID string  `json:"reviewerId"`
	GrantedAt  string  `json:"grantedAt"`
	ExpiresAt  *string `json:"expiresAt,omitempty"`
	Status     string  `json:"status"`
	RevokedAt  *string `json:"revokedAt,omitempty"`
}

// PromoPackageVerdictRecord is Lesser's immutable hash-bound review decision.
type PromoPackageVerdictRecord struct {
	Verdict     string  `json:"verdict"`
	Notes       *string `json:"notes,omitempty"`
	ContentHash *string `json:"contentHash,omitempty"`
	ReviewerID  string  `json:"reviewerId"`
	RecordedAt  string  `json:"recordedAt"`
	Current     bool    `json:"current"`
	Stale       bool    `json:"stale"`
}

// PromoPackageReleaseEligibility is Lesser's authoritative release-gate
// decision: blockingReasons carries the doctrine reasons verbatim
// (REVIEW_APPROVAL_REQUIRED / PRINCIPAL_APPROVAL_REQUIRED / ASSET_* /
// PACKAGE_RELEASING).
type PromoPackageReleaseEligibility struct {
	Eligible                  bool     `json:"eligible"`
	BlockingReasons           []string `json:"blockingReasons"`
	ReviewersApproved         bool     `json:"reviewersApproved"`
	PrincipalApprovalRequired bool     `json:"principalApprovalRequired"`
	PrincipalApproved         bool     `json:"principalApproved"`
}

// PromoPackageReview is the caller-authorized review surface. Non-owner callers
// (active reviewers) see only their own grant and verdict records — Lesser
// filters owner-or-self; body transports the filtered surface verbatim.
type PromoPackageReview struct {
	PackageID                 string                         `json:"packageId"`
	ContentHash               string                         `json:"contentHash"`
	Assets                    []PromoPackageAsset            `json:"assets"`
	ActiveReviewerIDs         []string                       `json:"activeReviewerIds"`
	ReleaseEligible           bool                           `json:"releaseEligible"`
	ReleaseBlockingReasons    []string                       `json:"releaseBlockingReasons"`
	ReviewersApproved         bool                           `json:"reviewersApproved"`
	PrincipalApprovalRequired bool                           `json:"principalApprovalRequired"`
	PrincipalApproved         bool                           `json:"principalApproved"`
	GrantCount                int                            `json:"grantCount"`
	GrantsTruncated           bool                           `json:"grantsTruncated"`
	Grants                    []PromoPackageReviewGrant      `json:"grants"`
	Verdicts                  []PromoPackageVerdictRecord    `json:"verdicts"`
	ReleaseEligibility        PromoPackageReleaseEligibility `json:"releaseEligibility"`
}

// PromoPackage is Lesser's promo package record as consumed by body's promo
// tools. releasedStatusId is the outbound post reference created by the
// release transition; nil until released.
type PromoPackage struct {
	ID               string              `json:"id"`
	OwnerID          string              `json:"ownerId"`
	ArticleID        string              `json:"articleId"`
	PostText         string              `json:"postText"`
	Visibility       string              `json:"visibility"`
	ContentHash      string              `json:"contentHash"`
	Status           string              `json:"status"`
	ReleasedStatusID *string             `json:"releasedStatusId,omitempty"`
	Assets           []PromoPackageAsset `json:"assets"`
	CreatedAt        string              `json:"createdAt"`
	UpdatedAt        string              `json:"updatedAt"`
	Review           *PromoPackageReview `json:"review,omitempty"`
}

// PromoPackageReleaseResult is Lesser's releasePromoPackage response: the
// stamped package plus the created outbound Status reference.
type PromoPackageReleaseResult struct {
	Package  *PromoPackage `json:"package"`
	StatusID string        `json:"statusId"`
	URL      *string       `json:"url,omitempty"`
}

// PromoPackageComposeInput mirrors Lesser's ComposePromoPackageInput. An empty
// PackageID creates a new package; a set PackageID replaces that package's
// content (re-hashing and staling prior approvals).
type PromoPackageComposeInput struct {
	PackageID     string
	ArticleID     string
	PostText      string
	Visibility    string
	AssetMediaIDs []string
}

// PromoPackageErrorClass classifies a failed promo operation from Lesser's
// stable service error text so the tool layer can render the correct envelope
// without re-deriving package state.
type PromoPackageErrorClass int

const (
	PromoPackageErrorUnknown PromoPackageErrorClass = iota
	PromoPackageErrorNotFound
	PromoPackageErrorAlreadyReleased
	// PromoPackageErrorReleasing is the PACKAGE_RELEASING reservation: release
	// and composition are refused until an operator reconciles the reservation.
	PromoPackageErrorReleasing
	// PromoPackageErrorStampFailed is PromoPackageStampError semantics: the
	// outbound Status WAS created but the releasing -> released stamp failed.
	// The surfaced status ID means a post exists — never retry-safe.
	PromoPackageErrorStampFailed
	PromoPackageErrorApprovalRequired
	PromoPackageErrorPrincipalApprovalRequired
	PromoPackageErrorAssetUnavailable
	// PromoPackageErrorContentChanged is the submit-hash mismatch: the package
	// was recomposed after the reviewer inspected it.
	PromoPackageErrorContentChanged
	PromoPackageErrorConflict
	PromoPackageErrorOwnerSelfReview
	PromoPackageErrorValidation
	PromoPackageErrorUnavailable
)

// PromoPackageClassifiedError wraps a lesser promo failure with its classified
// lane, the surfaced status ID (stamp-failure only), and any blocking reasons
// extracted from the upstream message.
type PromoPackageClassifiedError struct {
	Class           PromoPackageErrorClass
	Message         string
	StatusID        string
	BlockingReasons []string
}

func (e *PromoPackageClassifiedError) Error() string {
	if e == nil {
		return "promo package failure"
	}
	if e.Message != "" {
		return e.Message
	}
	return "promo package failure"
}

var promoStampStatusIDPattern = regexp.MustCompile(`created status (\S+) but could not stamp`)

// classifyPromoMessage maps Lesser's stable promo service error text onto a
// class, extracting the surfaced status ID and blocking reasons where present.
// The strings are part of the contract between body and lesser; matching is
// scoped to the bounded sentinel set, never free-form upstream text.
func classifyPromoMessage(message string) (PromoPackageErrorClass, string, []string) {
	msg := strings.ToLower(strings.TrimSpace(message))
	reasons := extractPromoBlockingReasons(message)
	switch {
	case msg == "":
		return PromoPackageErrorUnknown, "", reasons
	case strings.Contains(msg, "created status") && strings.Contains(msg, "could not stamp"):
		statusID := ""
		if match := promoStampStatusIDPattern.FindStringSubmatch(message); len(match) == 2 {
			statusID = match[1]
		}
		return PromoPackageErrorStampFailed, statusID, reasons
	case strings.Contains(msg, "release is already in progress"):
		return PromoPackageErrorReleasing, "", reasons
	case strings.Contains(msg, "is already released"):
		return PromoPackageErrorAlreadyReleased, "", reasons
	case strings.Contains(msg, "content changed since the reviewer inspected it"):
		return PromoPackageErrorContentChanged, "", reasons
	case strings.Contains(msg, "changed concurrently"):
		return PromoPackageErrorConflict, "", reasons
	case strings.Contains(msg, "requires approval from every required reviewer"):
		return PromoPackageErrorApprovalRequired, "", reasons
	case strings.Contains(msg, "requires an active approval from the instance principal"):
		return PromoPackageErrorPrincipalApprovalRequired, "", reasons
	case strings.Contains(msg, "cannot serve the exact approved bytes"):
		return PromoPackageErrorAssetUnavailable, "", reasons
	case strings.Contains(msg, "owner cannot review their own package"):
		return PromoPackageErrorOwnerSelfReview, "", reasons
	// Lesser's compose admission lookup failures (buildPromoPackageContent in
	// pkg/services/cms/promo_package.go): a foreign (not-the-composer) asset, an
	// unknown media id, or an unknown article id is a caller-correctable request
	// failure, never a package-id not-found. A create carries no package id at
	// all, so these must classify to the validation lane BEFORE the generic
	// "not found" bucket, which is reserved for package-id lookups only.
	case strings.Contains(msg, "does not belong to the composer"),
		strings.Contains(msg, "asset lookup failed"),
		strings.Contains(msg, "article lookup failed"):
		return PromoPackageErrorValidation, "", reasons
	case strings.Contains(msg, "not found"):
		return PromoPackageErrorNotFound, "", reasons
	// Lesser's compose/share admission sentinels (pkg/services/cms/promo_package.go).
	// These are caller-correctable request failures, not upstream outages, so they
	// classify to the validation lane instead of falling through to Unknown/502.
	case strings.Contains(msg, "post text is required"),
		strings.Contains(msg, "article reference is required"),
		strings.Contains(msg, "must reference a published article"),
		strings.Contains(msg, "requires at least one published asset"),
		strings.Contains(msg, "reviewer are required"),
		strings.Contains(msg, "grant is not active"),
		strings.Contains(msg, "submit requires the inspected content hash"):
		return PromoPackageErrorValidation, "", reasons
	case strings.Contains(msg, "validation"), strings.Contains(msg, "invalid"), strings.Contains(msg, "visibility"), strings.Contains(msg, "exceeds"):
		return PromoPackageErrorValidation, "", reasons
	case strings.Contains(msg, "unavailable"), strings.Contains(msg, "capability"):
		return PromoPackageErrorUnavailable, "", reasons
	default:
		return PromoPackageErrorUnknown, "", reasons
	}
}

// extractPromoBlockingReasons returns the known blocking-reason constants that
// appear in Lesser's error text (release gate errors join the sentinel with a
// "blocking reasons: ..." detail, and asset errors list the ASSET_* reasons
// after a colon). Matching is bounded to the known constant set so unrelated
// uppercase tokens never leak into the envelope.
func extractPromoBlockingReasons(message string) []string {
	upper := strings.ToUpper(strings.TrimSpace(message))
	var reasons []string
	seen := make(map[string]struct{}, len(PromoBlockingReasons))
	for _, reason := range PromoBlockingReasons {
		if strings.Contains(upper, reason) {
			seen[reason] = struct{}{}
		}
	}
	for _, reason := range PromoBlockingReasons {
		if _, ok := seen[reason]; ok {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func classifyPromoFailure(err error) error {
	if err == nil {
		return nil
	}
	var gqlErr *GraphQLErrors
	if !errors.As(err, &gqlErr) {
		return err
	}
	if len(gqlErr.Errors) == 0 {
		return err
	}
	// Lesser's structured AppError extensions (code/http_status) are the
	// authoritative lane signal: a per-request act-as FORBIDDEN, a validation
	// BAD_REQUEST, or any other AppError must surface its real code/status (the
	// tool layer renders these through the shared articleDraftGraphQLErrorContract
	// lane), never fall through to message-sentinel classification or render
	// Unknown/502. Message classification exists for Lesser's extension-less
	// plain errors only.
	if promoHasStructuredExtensions(gqlErr) {
		return err
	}
	message := strings.TrimSpace(gqlErr.Errors[0].Message)
	class, statusID, reasons := classifyPromoMessage(message)
	return &PromoPackageClassifiedError{
		Class:           class,
		Message:         message,
		StatusID:        statusID,
		BlockingReasons: reasons,
	}
}

// promoHasStructuredExtensions reports whether any GraphQL error carries
// Lesser's structured AppError extensions — a non-empty code string or a
// 4xx/5xx http_status. Presence means the error is an AppError projection whose
// extensions are the authoritative envelope signal; extension-less errors keep
// the message-sentinel classification lane.
func promoHasStructuredExtensions(gqlErr *GraphQLErrors) bool {
	if gqlErr == nil {
		return false
	}
	for _, item := range gqlErr.Errors {
		if code, ok := item.Extensions["code"].(string); ok && strings.TrimSpace(code) != "" {
			return true
		}
		if status, ok := item.Extensions["http_status"].(float64); ok && status >= 400 && status <= 599 {
			return true
		}
	}
	return false
}

type promoPackageResponse struct {
	PromoPackage *PromoPackage `json:"promoPackage"`
}

type composePromoPackageResponse struct {
	ComposePromoPackage *PromoPackage `json:"composePromoPackage"`
}

type sharePromoPackageForReviewResponse struct {
	SharePromoPackageForReview *PromoPackageReview `json:"sharePromoPackageForReview"`
}

type submitPromoPackageReviewResponse struct {
	SubmitPromoPackageReview *PromoPackageReview `json:"submitPromoPackageReview"`
}

type releasePromoPackageResponse struct {
	ReleasePromoPackage *PromoPackageReleaseResult `json:"releasePromoPackage"`
}

// GetPromoPackage resolves one promo package for its owner or an active
// reviewer grant. An unrelated caller receives not-found (Lesser's
// owner-or-grant authorization is the only gate; pre-release packages are
// never world-readable). Reviewer-filtered grants/verdicts surface verbatim.
func (c *Client) GetPromoPackage(ctx context.Context, bearerToken, packageID string) (*PromoPackage, error) {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" {
		return nil, fmt.Errorf("package id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyPromoPackage($id: ID!) { promoPackage(id: $id) { " + promoPackageFields() + " } }",
		OperationName: "BodyPromoPackage",
		Variables:     map[string]any{"id": packageID},
	})
	if err != nil {
		return nil, classifyPromoFailure(err)
	}
	var data promoPackageResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.PromoPackage == nil {
		return nil, &PromoPackageNotFoundError{Lookup: "package_id", Value: packageID}
	}
	normalizePromoPackage(data.PromoPackage)
	return data.PromoPackage, nil
}

// ComposePromoPackage creates a promo package or replaces its content. Every
// content change re-hashes and stales prior approvals, so release stays blocked
// until the changed package is re-reviewed and re-authorized. Lesser validates
// the published-article reference, the notes size limit, the public/unlisted
// visibility, and that every asset id is in the PUBLISHED durable state.
func (c *Client) ComposePromoPackage(ctx context.Context, bearerToken string, input PromoPackageComposeInput) (*PromoPackage, error) {
	input.PackageID = strings.TrimSpace(input.PackageID)
	input.ArticleID = strings.TrimSpace(input.ArticleID)
	input.PostText = strings.TrimSpace(input.PostText)
	if input.ArticleID == "" {
		return nil, fmt.Errorf("article id is required")
	}
	if input.PostText == "" {
		return nil, fmt.Errorf("post text is required")
	}
	variables := map[string]any{
		"articleId":     input.ArticleID,
		"postText":      input.PostText,
		"visibility":    strings.ToUpper(strings.TrimSpace(input.Visibility)),
		"assetMediaIds": trimmedNonEmptyStrings(input.AssetMediaIDs),
	}
	if input.PackageID != "" {
		variables["packageId"] = input.PackageID
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyComposePromoPackage($input: ComposePromoPackageInput!) { composePromoPackage(input: $input) { " + promoPackageFields() + " } }",
		OperationName: "BodyComposePromoPackage",
		Variables:     map[string]any{"input": variables},
	})
	if err != nil {
		return nil, classifyPromoFailure(err)
	}
	var data composePromoPackageResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.ComposePromoPackage == nil {
		return nil, fmt.Errorf("composePromoPackage returned no package")
	}
	normalizePromoPackage(data.ComposePromoPackage)
	return data.ComposePromoPackage, nil
}

// SharePromoPackageForReview shares a package with a reviewer (7-day bounded
// grant) and returns the owner-authorized review state.
func (c *Client) SharePromoPackageForReview(ctx context.Context, bearerToken, packageID, reviewer string) (*PromoPackageReview, error) {
	packageID = strings.TrimSpace(packageID)
	reviewer = strings.TrimSpace(reviewer)
	if packageID == "" {
		return nil, fmt.Errorf("package id is required")
	}
	if reviewer == "" {
		return nil, fmt.Errorf("reviewer is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodySharePromoPackageForReview($packageId: ID!, $reviewer: String!) { sharePromoPackageForReview(packageId: $packageId, reviewer: $reviewer) { " + promoReviewFields() + " } }",
		OperationName: "BodySharePromoPackageForReview",
		Variables:     map[string]any{"packageId": packageID, "reviewer": reviewer},
	})
	if err != nil {
		return nil, classifyPromoFailure(err)
	}
	var data sharePromoPackageForReviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.SharePromoPackageForReview == nil {
		return nil, fmt.Errorf("sharePromoPackageForReview returned no review")
	}
	normalizePromoReview(data.SharePromoPackageForReview)
	return data.SharePromoPackageForReview, nil
}

// SubmitPromoPackageReview records a hash-bound reviewer verdict on the exact
// current package content. contentHash is REQUIRED (Lesser's argument is
// String! non-null): it carries the hash the reviewer actually inspected, and
// a mismatch (the package was recomposed after inspection) rejects the submit
// with a conflict instead of blessing unseen content.
func (c *Client) SubmitPromoPackageReview(ctx context.Context, bearerToken, packageID, verdict string, notes *string, contentHash string) (*PromoPackageReview, error) {
	packageID = strings.TrimSpace(packageID)
	verdict = strings.ToUpper(strings.TrimSpace(verdict))
	contentHash = strings.TrimSpace(contentHash)
	if packageID == "" {
		return nil, fmt.Errorf("package id is required")
	}
	if verdict != PromoPackageVerdictApproved && verdict != PromoPackageVerdictChangesRequested {
		return nil, fmt.Errorf("invalid promo review verdict")
	}
	if contentHash == "" {
		return nil, fmt.Errorf("content hash is required")
	}
	variables := map[string]any{
		"packageId":   packageID,
		"verdict":     verdict,
		"contentHash": contentHash,
	}
	if notes != nil {
		variables["notes"] = strings.TrimSpace(*notes)
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodySubmitPromoPackageReview($packageId: ID!, $verdict: PromoPackageReviewVerdict!, $notes: String, $contentHash: String!) { submitPromoPackageReview(packageId: $packageId, verdict: $verdict, notes: $notes, contentHash: $contentHash) { " + promoReviewFields() + " } }",
		OperationName: "BodySubmitPromoPackageReview",
		Variables:     variables,
	})
	if err != nil {
		return nil, classifyPromoFailure(err)
	}
	var data submitPromoPackageReviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.SubmitPromoPackageReview == nil {
		return nil, fmt.Errorf("submitPromoPackageReview returned no review")
	}
	normalizePromoReview(data.SubmitPromoPackageReview)
	return data.SubmitPromoPackageReview, nil
}

// ReleasePromoPackage releases an approved package: the outbound public/unlisted
// Status is created with the exact approved PUBLISHED assets. Failures carry
// Lesser's structured semantics: PromoPackageStampError (post created, stamp
// failed — surfaced status ID must be reconciled, never retried) and the
// PACKAGE_RELEASING reservation both classify to the reconcile lane.
func (c *Client) ReleasePromoPackage(ctx context.Context, bearerToken, packageID string) (*PromoPackageReleaseResult, error) {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" {
		return nil, fmt.Errorf("package id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyReleasePromoPackage($packageId: ID!) { releasePromoPackage(packageId: $packageId) { package { " + promoPackageFields() + " } statusId url } }",
		OperationName: "BodyReleasePromoPackage",
		Variables:     map[string]any{"packageId": packageID},
	})
	if err != nil {
		return nil, classifyPromoFailure(err)
	}
	var data releasePromoPackageResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.ReleasePromoPackage == nil || data.ReleasePromoPackage.Package == nil {
		return nil, fmt.Errorf("releasePromoPackage returned no package")
	}
	normalizePromoPackage(data.ReleasePromoPackage.Package)
	return data.ReleasePromoPackage, nil
}

func promoProvenanceFields() string {
	return "provenance { origin tool responsibleActorId responsibleActor { id username } sourceReferences rightsLicenseNotes createdAt updatedAt recordedAt contentIntegrity }"
}

func promoAssetFields() string {
	return "mediaId contentHash publishedUrl state width height mimeType " + promoProvenanceFields()
}

func promoReviewFields() string {
	return "packageId contentHash assets { " + promoAssetFields() + " } activeReviewerIds releaseEligible releaseBlockingReasons reviewersApproved principalApprovalRequired principalApproved grantCount grantsTruncated grants { reviewerId grantedAt expiresAt status revokedAt } verdicts { verdict notes contentHash reviewerId recordedAt current stale } releaseEligibility { eligible blockingReasons reviewersApproved principalApprovalRequired principalApproved }"
}

func promoPackageFields() string {
	return "id ownerId articleId postText visibility contentHash status releasedStatusId assets { " + promoAssetFields() + " } createdAt updatedAt review { " + promoReviewFields() + " }"
}

func normalizePromoPackage(pkg *PromoPackage) {
	if pkg == nil {
		return
	}
	if pkg.Assets == nil {
		pkg.Assets = []PromoPackageAsset{}
	}
	normalizePromoReview(pkg.Review)
}

func normalizePromoReview(review *PromoPackageReview) {
	if review == nil {
		return
	}
	if review.Assets == nil {
		review.Assets = []PromoPackageAsset{}
	}
	if review.ActiveReviewerIDs == nil {
		review.ActiveReviewerIDs = []string{}
	}
	if review.ReleaseBlockingReasons == nil {
		review.ReleaseBlockingReasons = []string{}
	}
	if review.Grants == nil {
		review.Grants = []PromoPackageReviewGrant{}
	}
	if review.Verdicts == nil {
		review.Verdicts = []PromoPackageVerdictRecord{}
	}
	if review.ReleaseEligibility.BlockingReasons == nil {
		review.ReleaseEligibility.BlockingReasons = []string{}
	}
}

// trimmedNonEmptyStrings returns the trimmed non-empty strings in input order.
func trimmedNonEmptyStrings(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(input))
	for _, value := range input {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
