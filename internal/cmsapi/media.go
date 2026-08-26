package cmsapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Canonical upload-grant lifecycle statuses (Lesser's UploadGrantStatus enum).
const (
	UploadGrantStatusMinted       = "MINTED"
	UploadGrantStatusUsed         = "USED"
	UploadGrantStatusExpired      = "EXPIRED"
	UploadGrantStatusFailedDigest = "FAILED_DIGEST"
)

// Canonical editorial media states (Lesser's EditorialMediaState enum).
const (
	EditorialMediaStateMissing     = "MISSING"
	EditorialMediaStateProcessing  = "PROCESSING"
	EditorialMediaStateReady       = "READY"
	EditorialMediaStateRejected    = "REJECTED"
	EditorialMediaStateWithdrawn   = "WITHDRAWN"
	EditorialMediaStateSuperseded  = "SUPERSEDED"
	EditorialMediaStateUnavailable = "UNAVAILABLE"
)

// Canonical editorial media roles (Lesser's EditorialMediaRole enum).
const (
	EditorialMediaRoleHero       = "HERO"
	EditorialMediaRoleInline     = "INLINE"
	EditorialMediaRoleSocialCard = "SOCIAL_CARD"
)

// UploadGrant is Lesser's owner-scoped, one-time, hash-bound upload grant.
type UploadGrant struct {
	ID             string  `json:"id"`
	OwnerID        string  `json:"ownerId"`
	ContentType    string  `json:"contentType"`
	MaxSizeBytes   int     `json:"maxSizeBytes"`
	DeclaredSHA256 string  `json:"declaredSha256"`
	Status         string  `json:"status"`
	PresignedURL   *string `json:"presignedUrl,omitempty"`
	MediaID        *string `json:"mediaId,omitempty"`
	GrantedAt      string  `json:"grantedAt"`
	ExpiresAt      string  `json:"expiresAt"`
	UsedAt         *string `json:"usedAt,omitempty"`
	FailureReason  *string `json:"failureReason,omitempty"`
}

// UploadGrantMedia is the internal editorial media record admitted by a
// successful finalize.
type UploadGrantMedia struct {
	MediaID     string `json:"mediaId"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	ContentHash string `json:"contentHash"`
	Status      string `json:"status"`
	Visibility  string `json:"visibility"`
}

// UploadGrantFinalizeResult is Lesser's finalizeUploadGrant response.
type UploadGrantFinalizeResult struct {
	Grant *UploadGrant      `json:"grant"`
	Media *UploadGrantMedia `json:"media"`
}

// EditorialMediaProvenance is Lesser's internal provenance summary.
type EditorialMediaProvenance struct {
	Origin             string   `json:"origin"`
	Tool               *string  `json:"tool,omitempty"`
	ResponsibleActorID string   `json:"responsibleActorId"`
	ResponsibleActor   *Actor   `json:"responsibleActor,omitempty"`
	SourceReferences   []string `json:"sourceReferences"`
	RightsLicenseNotes *string  `json:"rightsLicenseNotes,omitempty"`
	CreatedAt          *string  `json:"createdAt,omitempty"`
	UpdatedAt          *string  `json:"updatedAt,omitempty"`
	RecordedAt         string   `json:"recordedAt"`
	ContentIntegrity   string   `json:"contentIntegrity"`
}

// EditorialMediaUsage is one draft-bound editorial media association with its
// per-usage caption/credit/alt and Lesser-authoritative state.
type EditorialMediaUsage struct {
	MediaID          string                    `json:"mediaId"`
	Role             string                    `json:"role"`
	InlinePosition   *int                      `json:"inlinePosition,omitempty"`
	Caption          *string                   `json:"caption,omitempty"`
	CreditLine       *string                   `json:"creditLine,omitempty"`
	AltText          *string                   `json:"altText,omitempty"`
	EffectiveAltText *string                   `json:"effectiveAltText,omitempty"`
	Focus            *string                   `json:"focus,omitempty"`
	State            string                    `json:"state"`
	Width            *int                      `json:"width,omitempty"`
	Height           *int                      `json:"height,omitempty"`
	MimeType         *string                   `json:"mimeType,omitempty"`
	ContentHash      *string                   `json:"contentHash,omitempty"`
	PublishedURL     *string                   `json:"publishedUrl,omitempty"`
	PublishedAt      *string                   `json:"publishedAt,omitempty"`
	AccessURL        *string                   `json:"accessUrl,omitempty"`
	AccessExpiresAt  *string                   `json:"accessExpiresAt,omitempty"`
	Provenance       *EditorialMediaProvenance `json:"provenance,omitempty"`
}

// EditorialMediaAccess is Lesser's grant-scoped exact-asset read.
type EditorialMediaAccess struct {
	MediaID     string `json:"mediaId"`
	URL         string `json:"url"`
	ExpiresAt   string `json:"expiresAt"`
	ContentHash string `json:"contentHash"`
}

// DraftMediaState is the caller-authorized draft projection used by the media
// tools: the exact ordered editorial-media bindings, the content binding for
// review staleness (verdicts' contentHash vs the draft's), active reviewers,
// and Lesser's publish eligibility (BOUND_MEDIA_* blocking reasons surface
// here). All fields are Lesser-authoritative state transported verbatim.
type DraftMediaState struct {
	DraftID            string                     `json:"draftId"`
	ContentHash        string                     `json:"contentHash"`
	Revision           int                        `json:"revision"`
	EditorialMedia     []EditorialMediaUsage      `json:"editorialMedia"`
	ActiveReviewerIDs  []string                   `json:"activeReviewerIds"`
	Verdicts           []DraftReviewVerdictRecord `json:"verdicts"`
	PublishEligibility DraftPublishEligibility    `json:"publishEligibility"`
}

// MediaUsageInput mirrors Lesser's EditorialMediaUsageInput.
type MediaUsageInput struct {
	MediaID        string
	Role           string
	InlinePosition *int
	Caption        *string
	CreditLine     *string
	AltText        *string
	Focus          *string
}

// UploadGrantNotFoundError reports an unknown or unowned upload grant.
type UploadGrantNotFoundError struct {
	Lookup string
	Value  string
}

func (e *UploadGrantNotFoundError) Error() string {
	if e == nil {
		return "upload grant not found"
	}
	return fmt.Sprintf("upload grant %q not found", e.Value)
}

// MediaBindingNotFoundError reports a media_id that is not bound to the given
// draft, or a draft/media combination the caller is not authorized to read.
type MediaBindingNotFoundError struct {
	Lookup string
	Value  string
}

func (e *MediaBindingNotFoundError) Error() string {
	if e == nil {
		return "media binding not found"
	}
	return fmt.Sprintf("media binding %q not found", e.Value)
}

// UploadGrantErrorClass classifies a failed finalize (or mint) from Lesser's
// stable service error text so the tool layer can render the correct envelope
// without re-deriving grant state.
type UploadGrantErrorClass int

const (
	UploadGrantErrorUnknown UploadGrantErrorClass = iota
	UploadGrantErrorExpired
	UploadGrantErrorNotMinted
	UploadGrantErrorDigestMismatch
	UploadGrantErrorObjectMissing
	UploadGrantErrorObjectEmpty
	UploadGrantErrorValidation
	UploadGrantErrorUnavailable
	UploadGrantErrorCreateFailed
	UploadGrantErrorNotFound
)

// UploadGrantClassifiedError wraps a lesser upload-grant failure with its
// classified lane.
type UploadGrantClassifiedError struct {
	Class   UploadGrantErrorClass
	Message string
}

func (e *UploadGrantClassifiedError) Error() string {
	if e == nil {
		return "upload grant failure"
	}
	if e.Message != "" {
		return e.Message
	}
	return "upload grant failure"
}

// classifyUploadGrantMessage maps Lesser's stable upload-grant service error
// text (pkg/services/media/upload_grant.go sentinels) onto a class. The strings
// are part of the contract between body and lesser; matching is scoped to the
// bounded sentinel set, never free-form upstream text.
func classifyUploadGrantMessage(message string) UploadGrantErrorClass {
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case msg == "":
		return UploadGrantErrorUnknown
	case strings.Contains(msg, "expired"):
		return UploadGrantErrorExpired
	case strings.Contains(msg, "not minted"), strings.Contains(msg, "already consumed"):
		return UploadGrantErrorNotMinted
	case strings.Contains(msg, "do not match"), strings.Contains(msg, "digest"):
		return UploadGrantErrorDigestMismatch
	case strings.Contains(msg, "not found; put"), strings.Contains(msg, "object not found"):
		return UploadGrantErrorObjectMissing
	case strings.Contains(msg, "is empty"), strings.Contains(msg, "object is empty"):
		return UploadGrantErrorObjectEmpty
	// Lesser's ErrUploadGrantNotFound sentinel ("upload grant not found") is a
	// GraphQL error, never data:null; it must land on the not-found class so the
	// tool layer renders media_not_found/404 instead of the unknown/502 lane.
	// This case sits after the object-missing/empty cases because those
	// sentinels also contain "not found"/"is empty" substrings.
	case strings.Contains(msg, "not found"):
		return UploadGrantErrorNotFound
	case strings.Contains(msg, "validation"), strings.Contains(msg, "content type"), strings.Contains(msg, "sha256"), strings.Contains(msg, "max size"):
		return UploadGrantErrorValidation
	case strings.Contains(msg, "unavailable"), strings.Contains(msg, "capability"):
		return UploadGrantErrorUnavailable
	case strings.Contains(msg, "failed to create"), strings.Contains(msg, "media create"):
		return UploadGrantErrorCreateFailed
	default:
		return UploadGrantErrorUnknown
	}
}

type mintUploadGrantResponse struct {
	MintUploadGrant *UploadGrant `json:"mintUploadGrant"`
}

type finalizeUploadGrantResponse struct {
	FinalizeUploadGrant *UploadGrantFinalizeResult `json:"finalizeUploadGrant"`
}

type uploadGrantQueryResponse struct {
	UploadGrant *UploadGrant `json:"uploadGrant"`
}

type draftEditorialMediaAccessResponse struct {
	DraftEditorialMediaAccess *EditorialMediaAccess `json:"draftEditorialMediaAccess"`
}

type draftMediaStateResponse struct {
	DraftReview *DraftMediaState `json:"draftReview"`
}

// draftMediaMutationResult mirrors the Draft projection returned by
// setDraftEditorialMedia. Lesser's setDraftEditorialMedia returns Draft!, which
// exposes id (not draftId) and carries no review-state fields
// (activeReviewerIds/verdicts/publishEligibility exist only on DraftReview), so
// this result type is deliberately smaller than DraftMediaState. SetDraftEditorialMedia
// maps it onto DraftMediaState for the media tools' binding projection.
type draftMediaMutationResult struct {
	ID             string                `json:"id"`
	ContentHash    string                `json:"contentHash"`
	Revision       int                   `json:"revision"`
	EditorialMedia []EditorialMediaUsage `json:"editorialMedia"`
}

type setDraftEditorialMediaResponse struct {
	SetDraftEditorialMedia *draftMediaMutationResult `json:"setDraftEditorialMedia"`
}

// MintUploadGrant requests a one-time, hash-bound, presigned-companion upload
// grant from Lesser. Lesser owns mint admission (image/* only, bounded size,
// 64-lowercase-hex sha256); body transports the caller's bearer untouched.
// Execute failures surface classified so the tool layer renders the stable
// body-owned mint lane.
func (c *Client) MintUploadGrant(ctx context.Context, bearerToken, contentType string, maxSizeBytes int, sha256 string) (*UploadGrant, error) {
	contentType = strings.TrimSpace(contentType)
	sha256 = strings.ToLower(strings.TrimSpace(sha256))
	if contentType == "" {
		return nil, fmt.Errorf("content type is required")
	}
	if sha256 == "" {
		return nil, fmt.Errorf("declared sha256 is required")
	}
	if maxSizeBytes <= 0 {
		return nil, fmt.Errorf("max size bytes must be positive")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyMintUploadGrant($input: MintUploadGrantInput!) { mintUploadGrant(input: $input) { " + uploadGrantFields() + " } }",
		OperationName: "BodyMintUploadGrant",
		Variables:     map[string]any{"input": map[string]any{"contentType": contentType, "maxSizeBytes": maxSizeBytes, "sha256": sha256}},
	})
	if err != nil {
		// Classified for symmetry with finalize/media_state: lesser mint
		// validation failures carry AppError extensions, but the wrap renders
		// every failure through the same stable body-owned lane and keeps the
		// not-found classification honest if lesser ever returns it on mint.
		return nil, classifyUploadGrantFailure(err)
	}
	var data mintUploadGrantResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.MintUploadGrant == nil {
		return nil, &UploadGrantNotFoundError{Lookup: "grant_id", Value: "mint"}
	}
	return data.MintUploadGrant, nil
}

// FinalizeUploadGrant asks Lesser to verify the uploaded bytes against the
// grant (digest, size cap, SVG safety) and admit the editorial media record.
// Only finalize is one-time; failures surface classified so the tool layer can
// render re-mint guidance.
func (c *Client) FinalizeUploadGrant(ctx context.Context, bearerToken, grantID string) (*UploadGrantFinalizeResult, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return nil, fmt.Errorf("grant id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodyFinalizeUploadGrant($grantId: ID!) { finalizeUploadGrant(grantId: $grantId) { grant { " + uploadGrantFields() + " } media { " + uploadGrantMediaFields() + " } } }",
		OperationName: "BodyFinalizeUploadGrant",
		Variables:     map[string]any{"grantId": grantID},
	})
	if err != nil {
		return nil, classifyUploadGrantFailure(err)
	}
	var data finalizeUploadGrantResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.FinalizeUploadGrant == nil || data.FinalizeUploadGrant.Media == nil {
		// Lesser's owner-scoped finalize returns ErrUploadGrantNotFound as a
		// GraphQL error ("upload grant not found") for an unknown or unowned
		// grant; it never returns data:null on this path. This nil-result guard
		// is a defensive fallback only — classifyUploadGrantFailure above has
		// already classified the real not-found lane.
		return nil, &UploadGrantNotFoundError{Lookup: "grant_id", Value: grantID}
	}
	return data.FinalizeUploadGrant, nil
}

// GetUploadGrant inspects one grant's lifecycle state (owner-scoped). While the
// grant is MINTED, Lesser re-presigns the PUT URL, so the returned URL is not
// stable; clients must not cache-bust on it. Lesser reports an unknown or
// unowned grant as an extensions-free GraphQL error ("upload grant not found"),
// which is classified here so the media_state grant lane renders
// media_not_found/404 instead of the unclassified 502 lane.
func (c *Client) GetUploadGrant(ctx context.Context, bearerToken, grantID string) (*UploadGrant, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return nil, fmt.Errorf("grant id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyUploadGrant($grantId: ID!) { uploadGrant(grantId: $grantId) { " + uploadGrantFields() + " } }",
		OperationName: "BodyUploadGrant",
		Variables:     map[string]any{"grantId": grantID},
	})
	if err != nil {
		// Lesser's owner-scoped uploadGrant query returns ErrUploadGrantNotFound
		// ("upload grant not found") as an extensions-free GraphQL error for an
		// unknown or unowned grant; classify so the media_state grant lane renders
		// media_not_found/404 instead of the unclassified 502 lane.
		return nil, classifyUploadGrantFailure(err)
	}
	var data uploadGrantQueryResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.UploadGrant == nil {
		return nil, &UploadGrantNotFoundError{Lookup: "grant_id", Value: grantID}
	}
	return data.UploadGrant, nil
}

// ReadDraftEditorialMedia reads the exact ordered editorial-media bindings of a
// draft the caller owns or reviews. The same projection serves media_state and
// the read-modify-write base for attach/detach/reorder.
//
// The projection explicitly requests includeAccessUrls:true because the media
// tools' documented contract promises the per-usage short-lived access URL on
// this read (media_state surfaces accessUrl/accessExpiresAt). Since lesser M4
// (fold-in: access-URL minting scoping) draftReview mints those URLs ONLY on
// explicit opt-in — the argument defaults to false — passing it keeps the M3
// media_state promise intact; the exact-asset draftEditorialMediaAccess lane
// remains the reviewer read for one asset.
func (c *Client) ReadDraftEditorialMedia(ctx context.Context, bearerToken, draftID string) (*DraftMediaState, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, buildDraftEditorialMediaOperation(draftID))
	if err != nil {
		return nil, err
	}
	var data draftMediaStateResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.DraftReview == nil {
		return nil, &DraftReviewNotFoundError{Lookup: "id", Value: draftID}
	}
	normalizeDraftMediaState(data.DraftReview)
	return data.DraftReview, nil
}

// ReadDraftEditorialMediaAccess mints Lesser's grant-scoped short-lived read
// for one EXACT asset bound to an authorized draft (owner or active reviewer).
// This is the reviewer read path.
func (c *Client) ReadDraftEditorialMediaAccess(ctx context.Context, bearerToken, draftID, mediaID string) (*EditorialMediaAccess, error) {
	draftID = strings.TrimSpace(draftID)
	mediaID = strings.TrimSpace(mediaID)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	if mediaID == "" {
		return nil, fmt.Errorf("media id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyDraftEditorialMediaAccess($draftId: ID!, $mediaId: ID!) { draftEditorialMediaAccess(draftId: $draftId, mediaId: $mediaId) { mediaId url expiresAt contentHash } }",
		OperationName: "BodyDraftEditorialMediaAccess",
		Variables:     map[string]any{"draftId": draftID, "mediaId": mediaID},
	})
	if err != nil {
		return nil, err
	}
	var data draftEditorialMediaAccessResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.DraftEditorialMediaAccess == nil {
		return nil, &MediaBindingNotFoundError{Lookup: "media_id", Value: mediaID}
	}
	return data.DraftEditorialMediaAccess, nil
}

// SetDraftEditorialMedia replaces the complete ordered editorial-media
// association for a draft (Lesser's setDraftEditorialMedia contract). Lesser
// validates ownership and per-usage roles; body always sends the full list.
// The mutation returns Draft!, so the result is the Draft binding projection
// (draftId/contentHash/revision/editorialMedia); review-state fields are not
// available on this path — read them through ReadDraftEditorialMedia when the
// caller is an owner or active reviewer.
func (c *Client) SetDraftEditorialMedia(ctx context.Context, bearerToken, draftID string, usages []MediaUsageInput) (*DraftMediaState, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	inputs := make([]map[string]any, 0, len(usages))
	for _, usage := range usages {
		input := map[string]any{
			"mediaId": strings.TrimSpace(usage.MediaID),
			"role":    strings.ToUpper(strings.TrimSpace(usage.Role)),
		}
		if usage.InlinePosition != nil {
			input["inlinePosition"] = *usage.InlinePosition
		}
		if usage.Caption != nil {
			input["caption"] = strings.TrimSpace(*usage.Caption)
		}
		if usage.CreditLine != nil {
			input["creditLine"] = strings.TrimSpace(*usage.CreditLine)
		}
		if usage.AltText != nil {
			input["altText"] = strings.TrimSpace(*usage.AltText)
		}
		if usage.Focus != nil {
			input["focus"] = strings.TrimSpace(*usage.Focus)
		}
		inputs = append(inputs, input)
	}
	resp, err := c.Execute(ctx, bearerToken, buildSetDraftEditorialMediaOperation(draftID, inputs))
	if err != nil {
		return nil, err
	}
	var data setDraftEditorialMediaResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.SetDraftEditorialMedia == nil {
		return nil, &DraftReviewNotFoundError{Lookup: "id", Value: draftID}
	}
	// Draft exposes id (not draftId); map it onto the media tools' binding
	// projection so handlers keep rendering the draftId surface unchanged.
	state := &DraftMediaState{
		DraftID:        data.SetDraftEditorialMedia.ID,
		ContentHash:    data.SetDraftEditorialMedia.ContentHash,
		Revision:       data.SetDraftEditorialMedia.Revision,
		EditorialMedia: data.SetDraftEditorialMedia.EditorialMedia,
	}
	normalizeDraftMediaState(state)
	return state, nil
}

// GraphQL documents the media client sends, with %s holding the selection set
// produced by the field builders below. They are named constants so the
// schema-conformance test (media_schema_test.go) validates the EXACT documents
// that go over the wire — a builder change or a template regression cannot
// escape pre-resolver schema validation (issue #589).
const (
	bodyDraftEditorialMediaQueryTemplate    = "query BodyDraftEditorialMedia($id: ID!) { draftReview(id: $id, includeAccessUrls: true) { %s } }"
	bodySetDraftEditorialMediaQueryTemplate = "mutation BodySetDraftEditorialMedia($draftId: ID!, $media: [EditorialMediaUsageInput!]!) { setDraftEditorialMedia(draftId: $draftId, media: $media) { %s } }"
)

// buildDraftEditorialMediaOperation assembles the media_state read operation.
// It is the single assembly point for the operation the client sends, so the
// schema-conformance test validates exactly what goes over the wire.
func buildDraftEditorialMediaOperation(draftID string) Operation {
	return Operation{
		Query:         fmt.Sprintf(bodyDraftEditorialMediaQueryTemplate, draftMediaStateFields()),
		OperationName: "BodyDraftEditorialMedia",
		Variables:     map[string]any{"id": draftID},
	}
}

// buildSetDraftEditorialMediaOperation assembles the attach/detach/reorder
// mutation operation. setDraftEditorialMedia returns Draft!, so the selection
// must be the Draft projection (draftBindingFields) — the DraftReview
// projection (draftMediaStateFields) is schema-invalid on this path and made
// every draft-media mutation 422 pre-resolver (issue #589). It is the single
// assembly point for the operation the client sends.
func buildSetDraftEditorialMediaOperation(draftID string, inputs []map[string]any) Operation {
	return Operation{
		Query:         fmt.Sprintf(bodySetDraftEditorialMediaQueryTemplate, draftBindingFields()),
		OperationName: "BodySetDraftEditorialMedia",
		Variables:     map[string]any{"draftId": draftID, "media": inputs},
	}
}

func uploadGrantFields() string {
	return "id ownerId contentType maxSizeBytes declaredSha256 status presignedUrl mediaId grantedAt expiresAt usedAt failureReason"
}

func uploadGrantMediaFields() string {
	return "mediaId contentType size contentHash status visibility"
}

func editorialMediaUsageFields() string {
	return "mediaId role inlinePosition caption creditLine altText effectiveAltText focus state width height mimeType contentHash publishedUrl publishedAt accessUrl accessExpiresAt provenance { origin tool responsibleActorId responsibleActor { id username } sourceReferences rightsLicenseNotes createdAt updatedAt recordedAt contentIntegrity }"
}

// draftMediaStateFields is the DraftReview projection used by the media_state
// read path (draftReview returns DraftReview). It is NOT valid on Draft:
// activeReviewerIds, verdicts, and publishEligibility exist only on
// DraftReview, and the type exposes draftId (not id). Mutations that return
// Draft must use draftBindingFields instead.
func draftMediaStateFields() string {
	return "draftId contentHash revision editorialMedia { " + editorialMediaUsageFields() + " } activeReviewerIds verdicts { verdict notes contentHash reviewerId reviewer { id username } recordedAt current stale } publishEligibility { eligible blockingReasons reviewersApproved principalApprovalRequired principalApproved }"
}

// draftBindingFields is the Draft projection for the setDraftEditorialMedia
// mutation family. setDraftEditorialMedia returns Draft!, which exposes id
// (not draftId) and carries no review-state fields; selecting
// activeReviewerIds/verdicts/publishEligibility on Draft makes gqlgen reject
// the whole mutation with HTTP 422 pre-resolver. The media tools render only
// this binding projection (draftId/contentHash/revision/editorialMedia) from
// the result.
func draftBindingFields() string {
	return "id contentHash revision editorialMedia { " + editorialMediaUsageFields() + " }"
}

func normalizeDraftMediaState(state *DraftMediaState) {
	if state == nil {
		return
	}
	if state.EditorialMedia == nil {
		state.EditorialMedia = []EditorialMediaUsage{}
	}
	if state.ActiveReviewerIDs == nil {
		state.ActiveReviewerIDs = []string{}
	}
	if state.Verdicts == nil {
		state.Verdicts = []DraftReviewVerdictRecord{}
	}
	if state.PublishEligibility.BlockingReasons == nil {
		state.PublishEligibility.BlockingReasons = []string{}
	}
	for i := range state.EditorialMedia {
		usage := &state.EditorialMedia[i]
		if usage.Provenance != nil && usage.Provenance.SourceReferences == nil {
			usage.Provenance.SourceReferences = []string{}
		}
	}
}

func classifyUploadGrantFailure(err error) error {
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
	message := strings.TrimSpace(gqlErr.Errors[0].Message)
	return &UploadGrantClassifiedError{
		Class:   classifyUploadGrantMessage(message),
		Message: message,
	}
}
