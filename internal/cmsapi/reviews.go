package cmsapi

import (
	"context"
	"fmt"
	"strings"
)

const (
	DraftReviewVerdictApproved         = "APPROVED"
	DraftReviewVerdictChangesRequested = "CHANGES_REQUESTED"
)

// DraftReview is the caller-authorized projection of Lesser's review workflow
// contract. Compact queries leave the evidence pointers nil; standard state
// queries populate exact source and canonical rendering from the same snapshot
// as the binding, grant, verdict, and eligibility state.
type DraftReview struct {
	DraftID                   string                     `json:"draftId"`
	OwnerID                   *string                    `json:"ownerId,omitempty"`
	Title                     *string                    `json:"title,omitempty"`
	Slug                      *string                    `json:"slug,omitempty"`
	Content                   *string                    `json:"content,omitempty"`
	RenderedHTML              *string                    `json:"renderedHtml,omitempty"`
	RenderErrors              *[]string                  `json:"renderErrors,omitempty"`
	Subtitle                  *string                    `json:"subtitle,omitempty"`
	Excerpt                   *string                    `json:"excerpt,omitempty"`
	ContentFormat             string                     `json:"contentFormat"`
	Status                    string                     `json:"status"`
	ScheduledAt               *string                    `json:"scheduledAt,omitempty"`
	UpdatedAt                 string                     `json:"updatedAt"`
	CreatedAt                 string                     `json:"createdAt"`
	GeneratedBy               *Actor                     `json:"generatedBy,omitempty"`
	ReviewedBy                *Actor                     `json:"reviewedBy,omitempty"`
	ReviewStatus              *string                    `json:"reviewStatus,omitempty"`
	EditorNotes               *string                    `json:"editorNotes,omitempty"`
	ContentHash               string                     `json:"contentHash"`
	Revision                  int                        `json:"revision"`
	EditorialMedia            []EditorialMediaUsage      `json:"editorialMedia,omitempty"` // surfaced only on the article_draft_get reviewer projection (issue #593)
	ActiveReviewerIDs         []string                   `json:"activeReviewerIds"`
	PublishEligible           bool                       `json:"publishEligible"`
	PublishBlockingReasons    []string                   `json:"publishBlockingReasons"`
	ReviewersApproved         bool                       `json:"reviewersApproved"`
	PrincipalApprovalRequired bool                       `json:"principalApprovalRequired"`
	PrincipalApproved         bool                       `json:"principalApproved"`
	GrantCount                int                        `json:"grantCount"`
	GrantsTruncated           bool                       `json:"grantsTruncated"`
	Grants                    []DraftReviewGrant         `json:"grants"`
	Grant                     *DraftReviewGrant          `json:"grant,omitempty"`
	Verdicts                  []DraftReviewVerdictRecord `json:"verdicts"`
	PublishEligibility        DraftPublishEligibility    `json:"publishEligibility"`
}

// DraftReviewNotFoundError reports a missing caller-authorized draftReview
// lookup in a way the MCP tool layer can classify without parsing free-form
// error text. Lookup names the Lesser review-state identifier (`id`); the tool
// layer may remap that to a caller-facing argument name such as `draft_id`.
type DraftReviewNotFoundError struct {
	Lookup string
	Value  string
}

func (e *DraftReviewNotFoundError) Error() string {
	if e == nil {
		return "draft review not found"
	}
	return fmt.Sprintf("draft review %q not found", e.Value)
}

// DraftReviewGrant is Lesser's caller-visible grant and reviewer projection.
type DraftReviewGrant struct {
	ReviewerID string  `json:"reviewerId"`
	Reviewer   *Actor  `json:"reviewer,omitempty"`
	GrantedAt  string  `json:"grantedAt"`
	Status     string  `json:"status"`
	RevokedAt  *string `json:"revokedAt,omitempty"`
}

// DraftReviewVerdictRecord is Lesser's immutable review decision projection.
type DraftReviewVerdictRecord struct {
	Verdict     string  `json:"verdict"`
	Notes       *string `json:"notes,omitempty"`
	ContentHash *string `json:"contentHash,omitempty"`
	ReviewerID  string  `json:"reviewerId"`
	Reviewer    *Actor  `json:"reviewer,omitempty"`
	RecordedAt  string  `json:"recordedAt"`
	Current     bool    `json:"current"`
	Stale       bool    `json:"stale"`
}

// DraftPublishEligibility is Lesser's authoritative publish-gate decision.
type DraftPublishEligibility struct {
	Eligible                  bool     `json:"eligible"`
	BlockingReasons           []string `json:"blockingReasons"`
	ReviewersApproved         bool     `json:"reviewersApproved"`
	PrincipalApprovalRequired bool     `json:"principalApprovalRequired"`
	PrincipalApproved         bool     `json:"principalApproved"`
}

// DraftReviewConnection is Lesser's paginated sharedDraftReviews response.
type DraftReviewConnection struct {
	Edges      []DraftReviewEdge `json:"edges"`
	PageInfo   PageInfo          `json:"pageInfo"`
	TotalCount int               `json:"totalCount"`
}

// DraftReviewEdge is one caller-authorized item in Lesser's review queue.
type DraftReviewEdge struct {
	Node   *DraftReview `json:"node"`
	Cursor string       `json:"cursor"`
}

type draftReviewResponse struct {
	DraftReview *DraftReview `json:"draftReview"`
}

type sharedDraftReviewsResponse struct {
	SharedDraftReviews DraftReviewConnection `json:"sharedDraftReviews"`
}

type shareDraftForReviewResponse struct {
	ShareDraftForReview *DraftReview `json:"shareDraftForReview"`
}

type submitDraftReviewResponse struct {
	SubmitDraftReview *DraftReview `json:"submitDraftReview"`
}

// SubmitArticleDraftForReview delegates reviewer-grant creation or refresh to
// Lesser's canonical shareDraftForReview mutation.
func (c *Client) SubmitArticleDraftForReview(ctx context.Context, bearerToken, draftID, reviewer string) (*DraftReview, error) {
	draftID = strings.TrimSpace(draftID)
	reviewer = strings.TrimSpace(reviewer)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	if reviewer == "" {
		return nil, fmt.Errorf("reviewer is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodySubmitArticleDraftForReview($draftId: ID!, $reviewer: String!) { shareDraftForReview(draftId: $draftId, reviewer: $reviewer) { " + draftReviewFields() + " } }",
		OperationName: "BodySubmitArticleDraftForReview",
		Variables:     map[string]any{"draftId": draftID, "reviewer": reviewer},
	})
	if err != nil {
		return nil, err
	}
	var data shareDraftForReviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.ShareDraftForReview == nil {
		return nil, fmt.Errorf("shareDraftForReview returned no review")
	}
	normalizeDraftReview(data.ShareDraftForReview)
	return data.ShareDraftForReview, nil
}

// ReadArticleDraftReview delegates caller authorization and review-state
// resolution to Lesser's draftReview query.
func (c *Client) ReadArticleDraftReview(ctx context.Context, bearerToken, draftID string) (*DraftReview, error) {
	return c.readArticleDraftReview(ctx, bearerToken, draftID, draftReviewFields())
}

// ReadArticleDraftReviewSource selects the caller-authorized draft projection
// needed by article_draft_get without also transporting canonical rendering.
// The source, owner, hash, revision, grant, and eligibility state still come
// from one Lesser DraftReview snapshot. Editorial media bindings ride along so
// article_draft_get's reviewer path returns the same bindings as the owner
// path (issue #593); no other review tool selects them.
func (c *Client) ReadArticleDraftReviewSource(ctx context.Context, bearerToken, draftID string) (*DraftReview, error) {
	return c.readArticleDraftReview(ctx, bearerToken, draftID, draftReviewGetFields())
}

// ReadArticleDraftReviewStandard selects Lesser's complete caller-authorized
// review evidence. Content, rendering, binding, grant, verdict, and
// eligibility fields all come from one authoritative DraftReview snapshot.
func (c *Client) ReadArticleDraftReviewStandard(ctx context.Context, bearerToken, draftID string) (*DraftReview, error) {
	return c.readArticleDraftReview(ctx, bearerToken, draftID, draftReviewStandardFields())
}

func (c *Client) readArticleDraftReview(ctx context.Context, bearerToken, draftID, fields string) (*DraftReview, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDraftReview($id: ID!) { draftReview(id: $id) { " + fields + " } }",
		OperationName: "BodyArticleDraftReview",
		Variables:     map[string]any{"id": draftID},
	})
	if err != nil {
		return nil, err
	}
	var data draftReviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.DraftReview == nil {
		return nil, &DraftReviewNotFoundError{Lookup: "id", Value: draftID}
	}
	normalizeDraftReview(data.DraftReview)
	return data.DraftReview, nil
}

// ListArticleDraftReviews delegates the caller's active review queue to
// Lesser's sharedDraftReviews query.
func (c *Client) ListArticleDraftReviews(ctx context.Context, bearerToken string, first int, after string) (*DraftReviewConnection, error) {
	if first <= 0 {
		first = 20
	}
	variables := map[string]any{"first": first}
	if after = strings.TrimSpace(after); after != "" {
		variables["after"] = after
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDraftReviewQueue($first: Int, $after: Cursor) { sharedDraftReviews(first: $first, after: $after) { edges { node { " + draftReviewFields() + " } cursor } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } totalCount } }",
		OperationName: "BodyArticleDraftReviewQueue",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data sharedDraftReviewsResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.SharedDraftReviews.Edges == nil {
		data.SharedDraftReviews.Edges = []DraftReviewEdge{}
	}
	for i := range data.SharedDraftReviews.Edges {
		normalizeDraftReview(data.SharedDraftReviews.Edges[i].Node)
	}
	return &data.SharedDraftReviews, nil
}

// SubmitArticleDraftReviewVerdict delegates an immutable verdict to Lesser.
func (c *Client) SubmitArticleDraftReviewVerdict(ctx context.Context, bearerToken, draftID, verdict string, notes *string) (*DraftReview, error) {
	draftID = strings.TrimSpace(draftID)
	verdict = strings.ToUpper(strings.TrimSpace(verdict))
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	if verdict != DraftReviewVerdictApproved && verdict != DraftReviewVerdictChangesRequested {
		return nil, fmt.Errorf("invalid draft review verdict")
	}
	variables := map[string]any{"draftId": draftID, "verdict": verdict}
	if notes != nil {
		variables["notes"] = strings.TrimSpace(*notes)
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "mutation BodySubmitArticleDraftReviewVerdict($draftId: ID!, $verdict: DraftReviewVerdict!, $notes: String) { submitDraftReview(draftId: $draftId, verdict: $verdict, notes: $notes) { " + draftReviewFields() + " } }",
		OperationName: "BodySubmitArticleDraftReviewVerdict",
		Variables:     variables,
	})
	if err != nil {
		return nil, err
	}
	var data submitDraftReviewResponse
	if err := unmarshalData(resp, &data); err != nil {
		return nil, err
	}
	if data.SubmitDraftReview == nil {
		return nil, fmt.Errorf("submitDraftReview returned no review")
	}
	normalizeDraftReview(data.SubmitDraftReview)
	return data.SubmitDraftReview, nil
}

func draftReviewFields() string {
	// Keep the projection within Lesser's depth-3 agent/CLI profile. The
	// connection edge/node wrappers are transparent to Lesser's depth counter.
	return "draftId title subtitle excerpt contentFormat status scheduledAt updatedAt createdAt generatedBy { id username } reviewedBy { id username } reviewStatus editorNotes contentHash revision activeReviewerIds publishEligible publishBlockingReasons reviewersApproved principalApprovalRequired principalApproved grantCount grantsTruncated grants { reviewerId reviewer { id username } grantedAt status revokedAt } grant { reviewerId reviewer { id username } grantedAt status revokedAt } verdicts { verdict notes contentHash reviewerId reviewer { id username } recordedAt current stale } publishEligibility { eligible blockingReasons reviewersApproved principalApprovalRequired principalApproved }"
}

func draftReviewStandardFields() string {
	return draftReviewSourceFields() + " renderedHtml renderErrors"
}

func draftReviewSourceFields() string {
	return draftReviewFields() + " ownerId slug content"
}

// draftReviewGetFields is the article_draft_get reviewer projection: the source
// snapshot plus the editorial-media bindings. It deliberately does not extend
// draftReviewSourceFields (which also feeds ReadArticleDraftReviewStandard) so
// the review tools' selection stays unchanged (issue #593 read-surface scope).
func draftReviewGetFields() string {
	return draftReviewSourceFields() + " editorialMedia { " + editorialMediaUsageFields() + " }"
}

func normalizeDraftReview(review *DraftReview) {
	if review == nil {
		return
	}
	if review.ActiveReviewerIDs == nil {
		review.ActiveReviewerIDs = []string{}
	}
	if review.PublishBlockingReasons == nil {
		review.PublishBlockingReasons = []string{}
	}
	if review.Grants == nil {
		review.Grants = []DraftReviewGrant{}
	}
	if review.Verdicts == nil {
		review.Verdicts = []DraftReviewVerdictRecord{}
	}
	if review.PublishEligibility.BlockingReasons == nil {
		review.PublishEligibility.BlockingReasons = []string{}
	}
}
