package lesserapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const agentsListPath = "/api/v1/agents"

// AgentDirectoryEntry is the public, source-backed representation returned by
// Lesser's GET /api/v1/agents endpoint. Deliberately private fields from
// Lesser's richer model (agent_owner, delegated_scopes, and
// identity_semantics.soul_agent_id) are not represented here. OAuth or
// delegated credentials are likewise not part of this type.
type AgentDirectoryEntry struct {
	Username          string                     `json:"username"`
	DisplayName       string                     `json:"display_name"`
	Bio               string                     `json:"bio,omitempty"`
	CreatedAt         *time.Time                 `json:"created_at,omitempty"`
	Verified          bool                       `json:"verified"`
	VerifiedAt        *time.Time                 `json:"verified_at,omitempty"`
	QuarantineStatus  string                     `json:"quarantine_status,omitempty"`
	QuarantineStart   *time.Time                 `json:"quarantine_start,omitempty"`
	QuarantineEnd     *time.Time                 `json:"quarantine_end,omitempty"`
	QuarantineActive  bool                       `json:"quarantine_active"`
	AgentType         string                     `json:"agent_type"`
	AgentVersion      string                     `json:"agent_version"`
	AgentCapabilities AgentDirectoryCapabilities `json:"agent_capabilities"`
	MCPAccess         AgentDirectoryMCPAccess    `json:"mcp_access"`
	IdentitySemantics AgentDirectoryIdentity     `json:"identity_semantics"`
}

// AgentDirectoryCapabilities contains the public capability summary from
// Lesser's agent directory response.
type AgentDirectoryCapabilities struct {
	CanPost           bool     `json:"can_post"`
	CanReply          bool     `json:"can_reply"`
	CanBoost          bool     `json:"can_boost"`
	CanFollow         bool     `json:"can_follow"`
	CanDM             bool     `json:"can_dm"`
	RestrictedDomains []string `json:"restricted_domains,omitempty"`
	MaxPostsPerHour   int      `json:"max_posts_per_hour"`
	RequiresApproval  bool     `json:"requires_approval"`
}

// AgentDirectoryMCPAccess contains the public, client-neutral access links
// advertised by Lesser for an agent.
type AgentDirectoryMCPAccess struct {
	MCPURL                 string   `json:"mcp_url"`
	ProtectedResourceURL   string   `json:"protected_resource_url"`
	AuthorizationServerURL string   `json:"authorization_server_url"`
	RegistrationURL        string   `json:"registration_url"`
	Scopes                 []string `json:"scopes"`
	Guidance               []string `json:"guidance"`
}

// AgentDirectoryIdentity contains only the public identity semantics. The
// source's soul_agent_id is intentionally omitted because anonymous Lesser
// responses redact it as private binding metadata.
type AgentDirectoryIdentity struct {
	IdentityState             string `json:"identity_state"`
	IdentityLabel             string `json:"identity_label"`
	LifecycleState            string `json:"lifecycle_state"`
	SoulBindingState          string `json:"soul_binding_state"`
	ContinuityState           string `json:"continuity_state"`
	ContinuitySummary         string `json:"continuity_summary"`
	BodyIdentityPreserved     bool   `json:"body_identity_preserved"`
	TimelinePresencePreserved bool   `json:"timeline_presence_preserved"`
	MemoryReferencesPreserved bool   `json:"memory_references_preserved"`
	AttributionLabel          string `json:"attribution_label"`
	ModerationLabel           string `json:"moderation_label"`
}

// ListAgents reads Lesser's public live-agent directory. The endpoint is
// intentionally called without a caller bearer: Body consumes the same public
// view as an anonymous Lesser client and never forwards a Ptah OAuth token.
func (c *Client) ListAgents(ctx context.Context) ([]AgentDirectoryEntry, error) {
	body, err := c.DoRawJSON(ctx, http.MethodGet, agentsListPath, nil, "", nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return []AgentDirectoryEntry{}, nil
	}

	var agents []AgentDirectoryEntry
	if err := json.Unmarshal(body, &agents); err != nil {
		return nil, fmt.Errorf("decode Lesser live-agent directory: %w", err)
	}
	if agents == nil {
		agents = []AgentDirectoryEntry{}
	}
	return agents, nil
}
