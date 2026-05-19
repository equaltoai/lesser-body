package cmsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

const graphQLEndpoint = "/api/graphql"

// Client is the internal CMS/GraphQL path for Lesser Article/Draft work.
//
// It deliberately does not register MCP tools or encode Article-specific
// operations. Public Article tools can layer typed operations on top once the
// Lesser Article contract is stable.
type Client struct {
	lesser *lesserapi.Client
}

// Default returns a CMS client backed by the configured Lesser API client.
func Default() (*Client, error) {
	lesser, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}
	return New(lesser)
}

// New wraps an existing Lesser API client.
func New(lesser *lesserapi.Client) (*Client, error) {
	if lesser == nil {
		return nil, fmt.Errorf("lesser cms client requires lesser api client")
	}
	return &Client{lesser: lesser}, nil
}

// Operation is a generic GraphQL operation against Lesser's CMS schema.
type Operation struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
}

// Response is a raw GraphQL response envelope. Data is intentionally left as
// json.RawMessage so future Article/Draft operations can evolve independently
// of this transport boundary.
type Response struct {
	Data       json.RawMessage `json:"data,omitempty"`
	Errors     []Error         `json:"errors,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// Error is a single GraphQL error item returned by Lesser.
type Error struct {
	Message    string         `json:"message"`
	Locations  []Location     `json:"locations,omitempty"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Location identifies a GraphQL source location for an error.
type Location struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// GraphQLErrors reports GraphQL-level failures from a successful HTTP response.
type GraphQLErrors struct {
	Errors []Error
	Data   json.RawMessage
}

func (e *GraphQLErrors) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "lesser cms graphql error"
	}
	msg := strings.TrimSpace(e.Errors[0].Message)
	if msg == "" {
		msg = "unknown graphql error"
	}
	if len(e.Errors) == 1 {
		return "lesser cms graphql error: " + msg
	}
	return fmt.Sprintf("lesser cms graphql error: %s (+%d more)", msg, len(e.Errors)-1)
}

// Execute posts a GraphQL operation to Lesser /api/graphql using the caller's
// OAuth bearer token. HTTP failures are returned as lesserapi.APIError;
// GraphQL errors are returned as *GraphQLErrors with the response preserved.
func (c *Client) Execute(ctx context.Context, bearerToken string, op Operation) (*Response, error) {
	if c == nil || c.lesser == nil {
		return nil, fmt.Errorf("lesser cms client not initialized")
	}
	op.Query = strings.TrimSpace(op.Query)
	if op.Query == "" {
		return nil, fmt.Errorf("graphql query is required")
	}

	raw, err := c.lesser.DoRawJSON(ctx, "POST", graphQLEndpoint, nil, bearerToken, op)
	if err != nil {
		return nil, err
	}

	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal graphql response: %w", err)
	}
	if len(resp.Errors) > 0 {
		return &resp, &GraphQLErrors{Errors: resp.Errors, Data: resp.Data}
	}
	return &resp, nil
}
