package lesserapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const soulBindingsPath = "/api/v1/souls/bindings"

const (
	// SoulAuthorityModelInstanceTrust is Lesser/Host's managed instance-key authority model hint.
	SoulAuthorityModelInstanceTrust = "instance_trust"
	// SoulAnchorStateHostedOffchain is Lesser/Host's hosted/off-chain anchor state hint.
	SoulAnchorStateHostedOffchain = "hosted_offchain"
	// SoulOperationalBindingHostedBound is Lesser/Host's hosted-bound operational binding hint.
	SoulOperationalBindingHostedBound = "hosted_bound_soul"
)

// SoulBindingEvidence carries body/Ptah correlation evidence for Lesser's
// hosted soul/body binding ceremony. Lesser treats this as a hint; Lesser and
// Host remain the authority for binding state.
type SoulBindingEvidence struct {
	Source          string `json:"source,omitempty"`
	HostRequestID   string `json:"host_request_id,omitempty"`
	DeclarationHash string `json:"declaration_hash,omitempty"`
	IssuedAt        string `json:"issued_at,omitempty"`
}

// SoulBindingRequest is the typed request body for
// POST /api/v1/souls/bindings.
type SoulBindingRequest struct {
	ActorUsername      string              `json:"actor_username"`
	SoulAgentID        string              `json:"soul_agent_id"`
	BodyActorID        string              `json:"body_actor_id,omitempty"`
	HostRegistrationID string              `json:"host_registration_id,omitempty"`
	HostConversationID string              `json:"host_conversation_id,omitempty"`
	AuthorityModel     string              `json:"authority_model,omitempty"`
	AnchorState        string              `json:"anchor_state,omitempty"`
	OperationalBinding string              `json:"operational_binding,omitempty"`
	PrincipalAddress   string              `json:"principal_address,omitempty"`
	Evidence           SoulBindingEvidence `json:"evidence,omitempty"`
}

// SoulBindingAgent is the Host-refetched identity block returned by Lesser for
// the hosted soul/body binding ceremony.
type SoulBindingAgent struct {
	AgentID            string `json:"agent_id"`
	Domain             string `json:"domain"`
	LocalID            string `json:"local_id"`
	AuthorityModel     string `json:"authority_model"`
	AnchorState        string `json:"anchor_state"`
	OperationalBinding string `json:"operational_binding"`
	LifecycleStatus    string `json:"lifecycle_status"`
	PublishedVersion   int    `json:"published_version,omitempty"`
}

// SoulAgentBinding is Lesser's local soul/body binding projection.
type SoulAgentBinding struct {
	AgentUsername    string    `json:"agent_username"`
	PrincipalAddress string    `json:"principal_address,omitempty"`
	BoundAt          time.Time `json:"bound_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SoulBindingIdempotency describes Lesser's POST replay scope and canonical
// payload hash.
type SoulBindingIdempotency struct {
	Key         string `json:"key"`
	Replayed    bool   `json:"replayed"`
	PayloadHash string `json:"payload_hash"`
}

// SoulBindingLinks contains follow-up links returned by Lesser for POST
// responses.
type SoulBindingLinks struct {
	Status string `json:"status"`
}

// SoulBindingResponse is the typed response for Lesser's soul-binding POST and
// GET endpoints.
type SoulBindingResponse struct {
	Version      string                  `json:"version"`
	Status       string                  `json:"status"`
	BindingState string                  `json:"binding_state"`
	Agent        SoulBindingAgent        `json:"agent"`
	Binding      SoulAgentBinding        `json:"binding"`
	Idempotency  *SoulBindingIdempotency `json:"idempotency,omitempty"`
	Links        *SoulBindingLinks       `json:"links,omitempty"`
}

// CreateSoulBinding creates or confirms a Lesser-local hosted soul/body
// binding through Lesser's server-to-server body/Ptah integration endpoint.
// The bearer must be the dedicated soul-binding integration credential, not a
// user OAuth token. The Idempotency-Key header is required and is never sent
// blank.
func (c *Client) CreateSoulBinding(ctx context.Context, integrationBearer string, idempotencyKey string, req SoulBindingRequest) (*SoulBindingResponse, error) {
	integrationBearer = strings.TrimSpace(integrationBearer)
	if integrationBearer == "" {
		return nil, fmt.Errorf("soul-binding integration bearer is required")
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, fmt.Errorf("soul-binding idempotency key is required")
	}
	if strings.TrimSpace(req.ActorUsername) == "" {
		return nil, fmt.Errorf("soul-binding actor username is required")
	}
	if strings.TrimSpace(req.SoulAgentID) == "" {
		return nil, fmt.Errorf("soul-binding soul agent id is required")
	}

	raw, _, err := c.doRawJSONWithRequestHeaders(ctx, http.MethodPost, soulBindingsPath, nil, integrationBearer, req, http.Header{
		"Idempotency-Key": []string{idempotencyKey},
	})
	if err != nil {
		return nil, err
	}
	return decodeSoulBindingResponse(raw)
}

// InitiateSoulBinding is an alias for CreateSoulBinding for ceremony-oriented
// callers.
func (c *Client) InitiateSoulBinding(ctx context.Context, integrationBearer string, idempotencyKey string, req SoulBindingRequest) (*SoulBindingResponse, error) {
	return c.CreateSoulBinding(ctx, integrationBearer, idempotencyKey, req)
}

// GetSoulBinding fetches Lesser's current hosted soul/body binding status. The
// bearer must be the dedicated soul-binding integration credential, not a user
// OAuth token. actorUsername is optional and, when present, is sent as
// actor_username to let Lesser fail closed on mismatches.
func (c *Client) GetSoulBinding(ctx context.Context, integrationBearer string, agentID string, actorUsername string) (*SoulBindingResponse, error) {
	integrationBearer = strings.TrimSpace(integrationBearer)
	if integrationBearer == "" {
		return nil, fmt.Errorf("soul-binding integration bearer is required")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("soul-binding agent id is required")
	}

	var query url.Values
	if actorUsername = strings.TrimSpace(actorUsername); actorUsername != "" {
		query = url.Values{"actor_username": []string{actorUsername}}
	}

	raw, err := c.DoRawJSON(ctx, http.MethodGet, soulBindingsPath+"/"+url.PathEscape(agentID), query, integrationBearer, nil)
	if err != nil {
		return nil, err
	}
	return decodeSoulBindingResponse(raw)
}

// GetSoulBindingStatus is an alias for GetSoulBinding for ceremony-status
// callers.
func (c *Client) GetSoulBindingStatus(ctx context.Context, integrationBearer string, agentID string, actorUsername string) (*SoulBindingResponse, error) {
	return c.GetSoulBinding(ctx, integrationBearer, agentID, actorUsername)
}

func decodeSoulBindingResponse(raw []byte) (*SoulBindingResponse, error) {
	var out SoulBindingResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal soul-binding response: %w", err)
	}
	return &out, nil
}
