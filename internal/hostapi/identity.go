package hostapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const publicAgentIdentityPath = "/api/v1/soul/agents/"

// IdentityClient is the Host public identity projection consumed by Body when
// it must verify a Host-finalized soul without trusting caller-supplied local
// actor strings.
type IdentityClient interface {
	GetAgentIdentity(ctx context.Context, agentID string) (*AgentIdentity, error)
}

// AgentIdentity is the allowlisted subset of Host's public
// GET /api/v1/soul/agents/{agentId} response that Body needs for Ptah binding
// and registry projection repair.
type AgentIdentity struct {
	AgentID                string
	Domain                 string
	LocalID                string
	AuthorityModel         string
	AnchorState            string
	OperationalBinding     string
	LifecycleStatus        string
	Status                 string
	PublishedVersion       int64
	SelfDescriptionVersion int64
}

// GetAgentIdentity fetches Host's public soul identity projection. This route
// is public; Body deliberately does not forward the Ptah caller's OAuth bearer.
func (c *Client) GetAgentIdentity(ctx context.Context, agentID string) (*AgentIdentity, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("host identity client is not configured")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("host identity agent id is required")
	}
	if len(agentID) > 256 {
		return nil, errors.New("host identity agent id is too long")
	}

	raw, err := c.client.DoJSON(ctx, http.MethodGet, publicAgentIdentityPath+url.PathEscape(agentID), nil, "", nil)
	if err != nil {
		return nil, sanitizeError(err)
	}
	envelope, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("lesser-host identity response was not an object")
	}
	agent := objectValue(envelope, "agent")
	if len(agent) == 0 {
		return nil, errors.New("lesser-host identity response did not include agent")
	}
	out := &AgentIdentity{
		AgentID:                firstString(agent, "agent_id", "agentId"),
		Domain:                 firstString(agent, "domain", "domain_normalized", "domainNormalized"),
		LocalID:                firstString(agent, "local_id", "localId"),
		AuthorityModel:         firstString(agent, "authority_model", "authorityModel"),
		AnchorState:            firstString(agent, "anchor_state", "anchorState"),
		OperationalBinding:     firstString(agent, "operational_binding", "operationalBinding"),
		LifecycleStatus:        firstString(agent, "lifecycle_status", "lifecycleStatus"),
		Status:                 firstString(agent, "status"),
		PublishedVersion:       firstPositiveInt64(firstInt64(agent, "published_version", "publishedVersion"), firstInt64(envelope, "published_version", "publishedVersion")),
		SelfDescriptionVersion: firstInt64(agent, "self_description_version", "selfDescriptionVersion"),
	}
	if out.AgentID == "" {
		return nil, fmt.Errorf("lesser-host identity response did not include agent_id")
	}
	return out, nil
}

func firstInt64(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value := int64Value(m[key]); value > 0 {
			return value
		}
	}
	return 0
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, _ := m[key].(string); strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}
