package lesserapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const actorAccessSuffix = "/access"

// ActorAccessResponse is the typed response for Lesser's actor-admission
// endpoint GET /api/v1/agents/{username}/access.
type ActorAccessResponse struct {
	Actor        string `json:"actor"`
	Relationship string `json:"relationship"`
	Authorized   bool   `json:"authorized"`
	ActedBy      string `json:"acted_by"`
}

// GetActorAccess resolves the caller's admission relationship to the named
// agent by forwarding the caller's own bearer token to Lesser's actor-admission
// endpoint. Lesser derives the principal from the token's DelegatedBy claim and
// requires the token subject to equal agentUsername; body never supplies the
// principal from client input.
//
// An authorized response carries relationship "owner" or "grantee". Every
// negative (including a missing or non-local grant) returns 403, and an
// unauthenticated token returns 401; both surface as *APIError so the caller
// can fail closed without distinguishing denial reasons. A transport error,
// timeout, or malformed response also returns an error.
func (c *Client) GetActorAccess(ctx context.Context, agentUsername, bearerToken string) (*ActorAccessResponse, error) {
	agentUsername = strings.TrimSpace(agentUsername)
	if agentUsername == "" {
		return nil, fmt.Errorf("actor username is required")
	}
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return nil, fmt.Errorf("caller bearer token is required")
	}

	path := "/api/v1/agents/" + url.PathEscape(agentUsername) + actorAccessSuffix
	raw, err := c.DoRawJSON(ctx, http.MethodGet, path, nil, bearerToken, nil)
	if err != nil {
		return nil, err
	}

	var out ActorAccessResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode actor access response: %w", err)
	}
	return &out, nil
}
