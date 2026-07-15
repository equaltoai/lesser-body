package lesserapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const agentsDelegatePath = "/api/v1/agents/delegate"

// AgentDelegationRequest is the request payload for Lesser's
// POST /api/v1/agents/delegate endpoint. The endpoint delegates to an existing
// local agent account and mints a fresh runtime token/session on each success;
// it has no idempotency-key or replay contract.
type AgentDelegationRequest struct {
	AgentUsername string   `json:"agent_username"`
	DisplayName   string   `json:"display_name,omitempty"`
	Bio           string   `json:"bio,omitempty"`
	Scopes        []string `json:"scopes"`
	ExpiresIn     int      `json:"expires_in,omitempty"`
	DeviceLabel   string   `json:"device_label,omitempty"`
	AgentInfo     any      `json:"agent_info,omitempty"`
}

// AgentDelegationResponse is the response payload returned by Lesser when it
// mints delegated runtime credentials for the target agent.
type AgentDelegationResponse struct {
	Account AgentDelegationAccount `json:"account"`
	Token   AgentDelegationToken   `json:"token"`
}

// AgentDelegationAccount is Lesser's Mastodon-compatible account projection
// for the existing delegated agent account.
type AgentDelegationAccount struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Acct           string `json:"acct"`
	DisplayName    string `json:"display_name"`
	Locked         bool   `json:"locked"`
	Bot            bool   `json:"bot"`
	Discoverable   bool   `json:"discoverable"`
	Group          bool   `json:"group"`
	CreatedAt      string `json:"created_at"`
	Note           string `json:"note"`
	URL            string `json:"url"`
	Avatar         string `json:"avatar"`
	AvatarStatic   string `json:"avatar_static"`
	Header         string `json:"header"`
	HeaderStatic   string `json:"header_static"`
	FollowersCount int    `json:"followers_count"`
	FollowingCount int    `json:"following_count"`
	StatusesCount  int    `json:"statuses_count"`
	LastStatusAt   string `json:"last_status_at"`
	Emojis         []any  `json:"emojis"`
	Fields         []any  `json:"fields"`
}

// AgentDelegationToken is Lesser's OAuth token response for a delegated agent
// runtime session. Treat both token fields as credentials.
type AgentDelegationToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

// DelegateAgent calls Lesser's POST /api/v1/agents/delegate endpoint using the
// caller's manage-agent OAuth bearer. The bearer is sent only in the
// Authorization header, never in the JSON request body. This method performs
// exactly one HTTP request and intentionally does not retry: each successful
// call mints a fresh delegated token/session.
func (c *Client) DelegateAgent(ctx context.Context, bearerToken string, req AgentDelegationRequest) (*AgentDelegationResponse, error) {
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return nil, fmt.Errorf("agent delegation bearer is required")
	}
	if strings.TrimSpace(req.AgentUsername) == "" {
		return nil, fmt.Errorf("agent delegation agent username is required")
	}
	if len(req.Scopes) == 0 {
		return nil, fmt.Errorf("agent delegation scopes are required")
	}
	for _, scope := range req.Scopes {
		if strings.TrimSpace(scope) == "" {
			return nil, fmt.Errorf("agent delegation scopes cannot contain empty values")
		}
	}

	raw, err := c.DoRawJSON(ctx, http.MethodPost, agentsDelegatePath, nil, bearerToken, req)
	if err != nil {
		return nil, err
	}
	return decodeAgentDelegationResponse(raw)
}

func decodeAgentDelegationResponse(raw []byte) (*AgentDelegationResponse, error) {
	var out AgentDelegationResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal agent delegation response: %w", err)
	}
	return &out, nil
}
