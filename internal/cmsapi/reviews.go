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

// DraftReview is the bounded projection of Lesser's review workflow contract.
// Review grants, verdict history, attribution, and publish eligibility remain
// Lesser-owned; Body only transports the fields needed by MCP clients.
type DraftReview struct {
	DraftID       string                     `json:"draftId"`
	Title         *string                    `json:"title,omitempty"`
	Subtitle      *string                    `json:"subtitle,omitempty"`
	Excerpt       *string                    `json:"excerpt,omitempty"`
	ContentFormat string                     `json:"contentFormat"`
	Status        string                     `json:"status"`
	ScheduledAt   *string                    `json:"scheduledAt,omitempty"`
	UpdatedAt     string                     `json:"updatedAt"`
	CreatedAt     string                     `json:"createdAt"`
	GeneratedBy   *Actor                     `json:"generatedBy,omitempty"`
	ReviewedBy    *Actor                     `json:"reviewedBy,omitempty"`
	ReviewStatus  *string                    `json:"reviewStatus,omitempty"`
	EditorNotes   *string                    `json:"editorNotes,omitempty"`
	Grant         *DraftReviewGrant          `json:"grant,omitempty"`
	Verdicts      []DraftReviewVerdictRecord `json:"verdicts"`
}

// DraftReviewGrant is the caller-visible active grant metadata returned by
// Lesser. Reviewer identity is intentionally not reconstructed by Body.
type DraftReviewGrant struct {
	GrantedAt string `json:"grantedAt"`
}

// DraftReviewVerdictRecord is Lesser's immutable review decision projection.
// The latest reviewer attribution is also available through DraftReview.ReviewedBy.
type DraftReviewVerdictRecord struct {
	Verdict    string  `json:"verdict"`
	Notes      *string `json:"notes,omitempty"`
	RecordedAt string  `json:"recordedAt"`
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
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, fmt.Errorf("draft id is required")
	}
	resp, err := c.Execute(ctx, bearerToken, Operation{
		Query:         "query BodyArticleDraftReview($id: ID!) { draftReview(id: $id) { " + draftReviewFields() + " } }",
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
		return nil, fmt.Errorf("draft review %q not found", draftID)
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
	return "draftId title subtitle excerpt contentFormat status scheduledAt updatedAt createdAt generatedBy { id username } reviewedBy { id username } reviewStatus editorNotes grant { grantedAt } verdicts { verdict notes recordedAt }"
}

func normalizeDraftReview(review *DraftReview) {
	if review == nil {
		return
	}
	if review.Verdicts == nil {
		review.Verdicts = []DraftReviewVerdictRecord{}
	}
}
