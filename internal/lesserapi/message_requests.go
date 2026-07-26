package lesserapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const messageRequestGraphQLEndpoint = "/api/graphql"

const messageRequestsListQuery = `query BodyMessageRequests($first: Int!, $after: Cursor) {
  conversations(folder: REQUESTS, first: $first, after: $after) {
    id
    unread
    accounts { id username domain displayName }
    viewerMetadata { requestState requestedAt acceptedAt declinedAt }
    lastStatus { id content createdAt }
    createdAt
    updatedAt
  }
}`

const messageRequestAcceptMutation = `mutation BodyAcceptMessageRequest($conversationId: ID!) {
  acceptMessageRequest(conversationId: $conversationId) {
    id
    unread
    accounts { id username domain displayName }
    viewerMetadata { requestState requestedAt acceptedAt declinedAt }
    lastStatus { id content createdAt }
    createdAt
    updatedAt
  }
}`

const messageRequestDeclineMutation = `mutation BodyDeclineMessageRequest($conversationId: ID!) {
  declineMessageRequest(conversationId: $conversationId)
}`

// MessageRequestAccount is the bounded actor identity carried on a Lesser
// message-request conversation. It intentionally excludes profile bodies and
// private reachability fields.
type MessageRequestAccount struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Domain      string `json:"domain,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// MessageRequestViewerMetadata is Lesser's recipient-scoped request state.
type MessageRequestViewerMetadata struct {
	RequestState string `json:"requestState"`
	RequestedAt  string `json:"requestedAt,omitempty"`
	AcceptedAt   string `json:"acceptedAt,omitempty"`
	DeclinedAt   string `json:"declinedAt,omitempty"`
}

// MessageRequestLastStatus is a bounded message snapshot. MCP shaping further
// truncates Content before returning it to a caller.
type MessageRequestLastStatus struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

// MessageRequestConversation is the exact Lesser GraphQL selection consumed by
// the actor-scoped MCP message-request tools.
type MessageRequestConversation struct {
	ID             string                       `json:"id"`
	Unread         bool                         `json:"unread"`
	Accounts       []MessageRequestAccount      `json:"accounts"`
	ViewerMetadata MessageRequestViewerMetadata `json:"viewerMetadata"`
	LastStatus     *MessageRequestLastStatus    `json:"lastStatus,omitempty"`
	CreatedAt      string                       `json:"createdAt"`
	UpdatedAt      string                       `json:"updatedAt"`
}

type messageRequestGraphQLOperation struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

type messageRequestGraphQLResponse struct {
	Data   json.RawMessage                  `json:"data,omitempty"`
	Errors []messageRequestGraphQLErrorItem `json:"errors,omitempty"`
}

type messageRequestGraphQLErrorItem struct {
	Message    string         `json:"message,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// MessageRequestGraphQLErrors reports a GraphQL-level failure without
// reflecting Lesser's error messages into logs or MCP responses.
type MessageRequestGraphQLErrors struct {
	Count int
	Codes []string
}

func (e *MessageRequestGraphQLErrors) Error() string {
	if e == nil || e.Count <= 0 {
		return "lesser message-request graphql error"
	}
	return fmt.Sprintf("lesser message-request graphql error (%d item(s))", e.Count)
}

// ListMessageRequests reads the authenticated recipient's pending request
// folder. Lesser remains the authorization and lifecycle authority.
func (c *Client) ListMessageRequests(ctx context.Context, bearerToken string, first int, after string) ([]MessageRequestConversation, error) {
	variables := map[string]any{"first": first}
	if after = strings.TrimSpace(after); after != "" {
		variables["after"] = after
	}

	var data struct {
		Conversations []MessageRequestConversation `json:"conversations"`
	}
	if err := c.executeMessageRequestGraphQL(ctx, bearerToken, messageRequestGraphQLOperation{
		Query:         messageRequestsListQuery,
		OperationName: "BodyMessageRequests",
		Variables:     variables,
	}, &data); err != nil {
		return nil, err
	}
	if data.Conversations == nil {
		return []MessageRequestConversation{}, nil
	}
	return data.Conversations, nil
}

// AcceptMessageRequest moves a recipient-owned pending request to Lesser's
// inbox and returns the recipient-scoped conversation state.
func (c *Client) AcceptMessageRequest(ctx context.Context, bearerToken string, conversationID string) (*MessageRequestConversation, error) {
	var data struct {
		Conversation *MessageRequestConversation `json:"acceptMessageRequest"`
	}
	if err := c.executeMessageRequestGraphQL(ctx, bearerToken, messageRequestGraphQLOperation{
		Query:         messageRequestAcceptMutation,
		OperationName: "BodyAcceptMessageRequest",
		Variables:     map[string]any{"conversationId": strings.TrimSpace(conversationID)},
	}, &data); err != nil {
		return nil, err
	}
	if data.Conversation == nil {
		return nil, fmt.Errorf("lesser message-request accept returned no conversation")
	}
	return data.Conversation, nil
}

// DeclineMessageRequest hides a recipient-owned pending request through
// Lesser's canonical decline mutation.
func (c *Client) DeclineMessageRequest(ctx context.Context, bearerToken string, conversationID string) (bool, error) {
	var data struct {
		Declined bool `json:"declineMessageRequest"`
	}
	if err := c.executeMessageRequestGraphQL(ctx, bearerToken, messageRequestGraphQLOperation{
		Query:         messageRequestDeclineMutation,
		OperationName: "BodyDeclineMessageRequest",
		Variables:     map[string]any{"conversationId": strings.TrimSpace(conversationID)},
	}, &data); err != nil {
		return false, err
	}
	return data.Declined, nil
}

func (c *Client) executeMessageRequestGraphQL(ctx context.Context, bearerToken string, op messageRequestGraphQLOperation, data any) error {
	if c == nil {
		return fmt.Errorf("lesser api client not initialized")
	}
	if strings.TrimSpace(op.Query) == "" || strings.TrimSpace(op.OperationName) == "" {
		return fmt.Errorf("lesser message-request graphql operation is incomplete")
	}

	raw, err := c.DoRawJSON(ctx, "POST", messageRequestGraphQLEndpoint, nil, bearerToken, op)
	if err != nil {
		return err
	}
	var response messageRequestGraphQLResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("unmarshal lesser message-request graphql response: %w", err)
	}
	if len(response.Errors) > 0 {
		return newMessageRequestGraphQLErrors(response.Errors)
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		return fmt.Errorf("lesser message-request graphql response missing data")
	}
	if err := json.Unmarshal(response.Data, data); err != nil {
		return fmt.Errorf("unmarshal lesser message-request graphql data: %w", err)
	}
	return nil
}

func newMessageRequestGraphQLErrors(items []messageRequestGraphQLErrorItem) *MessageRequestGraphQLErrors {
	codes := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		code, _ := item.Extensions["code"].(string)
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return &MessageRequestGraphQLErrors{Count: len(items), Codes: codes}
}
