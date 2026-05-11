package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const soulReadMaxMatches = 3

func handleSoulRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Query      string `json:"query"`
		AgentID    string `json:"agentId"`
		ENSName    string `json:"ensName"`
		Limit      int    `json:"limit,omitempty"`
		IncludeRaw bool   `json:"include_raw,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}

	query := firstNonEmpty(strings.TrimSpace(in.AgentID), strings.TrimSpace(in.ENSName), strings.TrimSpace(in.Query))
	if query == "" {
		return toolErrorResult("invalid_request", "query, agentId, or ensName is required", 400, nil)
	}
	if strings.TrimSpace(in.ENSName) != "" && !looksLikeENSName(in.ENSName) {
		return toolErrorResult("invalid_request", "ensName must be a public ENS name", 400, map[string]any{"query": strings.TrimSpace(in.ENSName)})
	}
	if looksLikeEmail(query) {
		return identityToolResultFromError(privateReachabilityUnavailableError("email", "soul_read"))
	}
	if looksLikePhoneIdentifier(query) {
		return identityToolResultFromError(privateReachabilityUnavailableError("phone", "soul_read"))
	}

	limit := boundedSoulReadLimit(in.Limit)
	client, err := soulapi.Default()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	agentIDs := []string{}
	if in.AgentID != "" || isSoulAgentID(query) {
		agentID := normalizeSoulAgentID(query)
		if agentID == "" {
			return toolErrorResult("invalid_request", "agentId must be a full soul agent ID", 400, map[string]any{"query": query})
		}
		agentIDs = append(agentIDs, agentID)
	} else {
		resolved, err := lookupAgentIDs(ctx, client, query)
		if err != nil {
			return identityToolResultFromError(err)
		}
		agentIDs = append(agentIDs, resolved...)
		if len(agentIDs) == 0 {
			return toolErrorResult("not_found", "no matching soul agent found", 404, map[string]any{"query": query})
		}
	}
	if len(agentIDs) > limit {
		agentIDs = agentIDs[:limit]
	}

	souls := make([]any, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		soul, err := soulReadPayload(ctx, client, agentID, in.IncludeRaw)
		if err != nil {
			return identityToolResultFromError(err)
		}
		souls = append(souls, soul)
	}

	return toolJSONResult(map[string]any{
		"query": query,
		"count": len(souls),
		"souls": souls,
	}, nil)
}

func boundedSoulReadLimit(limit int) int {
	switch {
	case limit <= 0:
		return 1
	case limit > soulReadMaxMatches:
		return soulReadMaxMatches
	default:
		return limit
	}
}

func soulReadPayload(ctx context.Context, client *soulapi.Client, agentID string, includeRaw bool) (map[string]any, error) {
	agentID = normalizeSoulAgentID(agentID)
	if agentID == "" {
		return nil, &toolUserError{Code: "invalid_request", Message: "missing agentId", Status: 400}
	}

	sources := []map[string]any{}
	deferred := []map[string]any{
		{"field": "channels.email", "status": "unavailable", "reason": "deferred_private_reachability"},
		{"field": "channels.phone", "status": "unavailable", "reason": "deferred_private_reachability"},
		{"field": "contactPreferences", "status": "unavailable", "reason": "deferred_private_reachability"},
	}

	agentPath := "/api/v1/soul/agents/" + url.PathEscape(agentID)
	agentRaw, err := client.DoJSON(ctx, "GET", agentPath, nil, "", nil)
	if err != nil {
		return nil, err
	}
	agentEnvelope, _ := agentRaw.(map[string]any)
	agent := firstMap(agentEnvelope, "agent")
	if agent == nil {
		agent = agentEnvelope
	}
	sources = append(sources, soulReadSource("identity", agentPath, "ok", ""))

	registrationPath := agentPath + "/registration"
	registrationRaw, registrationSource := soulReadOptional(ctx, client, "registration", registrationPath)
	sources = append(sources, registrationSource)
	registration, _ := registrationRaw.(map[string]any)

	capabilitiesPath := agentPath + "/capabilities"
	capabilitiesRaw, capabilitiesSource := soulReadOptional(ctx, client, "capabilities", capabilitiesPath)
	sources = append(sources, capabilitiesSource)

	boundariesPath := agentPath + "/boundaries"
	boundariesRaw, boundariesSource := soulReadOptional(ctx, client, "boundaries", boundariesPath)
	sources = append(sources, boundariesSource)

	transparencyPath := agentPath + "/transparency"
	transparencyRaw, transparencySource := soulReadOptional(ctx, client, "transparency", transparencyPath)
	sources = append(sources, transparencySource)

	payload := map[string]any{
		"agentId":      agentID,
		"identity":     normalizeSoulReadIdentity(agentID, agent),
		"registration": normalizeSoulReadRegistration(registration, agent),
		"capabilities": normalizeSoulReadCapabilities(capabilitiesRaw, registration),
		"boundaries":   normalizeSoulReadBoundaries(boundariesRaw, registration),
		"transparency": normalizeSoulReadTransparency(transparencyRaw, registration),
		"channels":     normalizeSoulReadChannels(agent, registration),
		"avatar":       normalizeSoulReadAvatar(firstMap(agent, "avatar")),
		"sources":      sources,
		"deferred":     deferred,
	}
	if includeRaw {
		payload["_raw"] = map[string]any{
			"identity":     agentRaw,
			"registration": registrationRaw,
			"capabilities": capabilitiesRaw,
			"boundaries":   boundariesRaw,
			"transparency": transparencyRaw,
		}
	}
	return payload, nil
}

func soulReadOptional(ctx context.Context, client *soulapi.Client, block string, path string) (any, map[string]any) {
	out, err := client.DoJSON(ctx, "GET", path, nil, "", nil)
	if err == nil {
		return out, soulReadSource(block, path, "ok", "")
	}

	status := "unavailable"
	reason := "public_endpoint_unavailable"
	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
		reason = identityErrorCodeForStatus(apiErr.Status)
		return nil, soulReadSource(block, path, status, reason)
	}
	return nil, soulReadSource(block, path, status, reason)
}

func soulReadSource(block string, endpoint string, status string, reason string) map[string]any {
	out := map[string]any{
		"block":    strings.TrimSpace(block),
		"endpoint": strings.TrimSpace(endpoint),
		"status":   strings.TrimSpace(status),
	}
	if reason != "" {
		out["reason"] = strings.TrimSpace(reason)
	}
	return out
}

func normalizeSoulReadIdentity(agentID string, agent map[string]any) map[string]any {
	out := map[string]any{"agentId": agentID}
	putFirstAny(out, "domain", agent, "domain")
	putFirstAny(out, "localId", agent, "local_id", "localId")
	putFirstAny(out, "ensName", agent, "ens_name", "ensName")
	putFirstAny(out, "wallet", agent, "wallet")
	putFirstAny(out, "tokenId", agent, "token_id", "tokenId")
	putFirstAny(out, "metaUri", agent, "meta_uri", "metaUri")
	putFirstAny(out, "status", agent, "status")
	putFirstAny(out, "lifecycleStatus", agent, "lifecycle_status", "lifecycleStatus")
	putFirstAny(out, "lifecycleReason", agent, "lifecycle_reason", "lifecycleReason")
	putFirstAny(out, "successorAgentId", agent, "successor_agent_id", "successorAgentId")
	putFirstAny(out, "predecessorAgentId", agent, "predecessor_agent_id", "predecessorAgentId")
	putFirstAny(out, "principalAddress", agent, "principal_address", "principalAddress")
	putFirstAny(out, "selfDescriptionVersion", agent, "self_description_version", "selfDescriptionVersion")
	putFirstAny(out, "mintTxHash", agent, "mint_tx_hash", "mintTxHash")
	putFirstAny(out, "mintedAt", agent, "minted_at", "mintedAt")
	putFirstAny(out, "updatedAt", agent, "updated_at", "updatedAt")
	return out
}

func normalizeSoulReadRegistration(reg map[string]any, agent map[string]any) map[string]any {
	out := map[string]any{"rawAvailable": reg != nil}
	if reg == nil {
		out["status"] = "unavailable"
		out["reason"] = "public_endpoint_unavailable"
		putFirstAny(out, "metaUri", agent, "meta_uri", "metaUri")
		return out
	}
	putFirstAny(out, "url", reg, "url")
	putFirstAny(out, "metaUri", reg, "meta_uri", "metaUri")
	if _, ok := out["metaUri"]; !ok {
		putFirstAny(out, "metaUri", agent, "meta_uri", "metaUri")
	}
	putFirstAny(out, "version", reg, "version")
	putFirstAny(out, "selfDescription", reg, "selfDescription", "self_description")
	putFirstAny(out, "principal", reg, "principal")
	return out
}

func normalizeSoulReadCapabilities(raw any, reg map[string]any) []any {
	items := firstArrayFromAny(raw, "capabilities", "items", "results")
	if len(items) == 0 && reg != nil {
		items = firstArrayFromAny(reg, "capabilities")
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		capability := map[string]any{}
		putFirstAny(capability, "name", m, "capability", "name")
		putFirstAny(capability, "scope", m, "scope")
		putFirstAny(capability, "constraints", m, "constraints")
		putFirstAny(capability, "claimLevel", m, "claim_level", "claimLevel")
		putFirstAny(capability, "lastValidated", m, "last_validated", "lastValidated")
		putFirstAny(capability, "validationRef", m, "validation_ref", "validationRef")
		putFirstAny(capability, "degradesTo", m, "degrades_to", "degradesTo")
		if len(capability) > 0 {
			out = append(out, capability)
		}
	}
	return out
}

func normalizeSoulReadBoundaries(raw any, reg map[string]any) []any {
	items := firstArrayFromAny(raw, "boundaries", "items", "results")
	if len(items) == 0 && reg != nil {
		items = firstArrayFromAny(reg, "boundaries")
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		boundary := map[string]any{}
		putFirstAny(boundary, "id", m, "id", "boundary_id", "boundaryId")
		putFirstAny(boundary, "category", m, "category")
		putFirstAny(boundary, "statement", m, "statement")
		putFirstAny(boundary, "rationale", m, "rationale")
		putFirstAny(boundary, "supersedes", m, "supersedes")
		putFirstAny(boundary, "version", m, "version", "added_in_version", "addedInVersion")
		putFirstAny(boundary, "addedInVersion", m, "added_in_version", "addedInVersion")
		putFirstAny(boundary, "issuedAt", m, "issued_at", "issuedAt", "added_at", "addedAt")
		if len(boundary) > 0 {
			out = append(out, boundary)
		}
	}
	return out
}

func normalizeSoulReadTransparency(raw any, reg map[string]any) map[string]any {
	m, _ := raw.(map[string]any)
	if nested := firstMap(m, "transparency"); nested != nil {
		m = nested
	}
	if len(m) == 0 && reg != nil {
		m = firstMap(reg, "transparency")
	}
	if len(m) == 0 {
		return map[string]any{"status": "unavailable", "reason": "public_endpoint_unavailable"}
	}
	return normalizeSoulReadMap(m)
}

func normalizeSoulReadChannels(agent map[string]any, reg map[string]any) map[string]any {
	ensName := firstNonEmptyStringMap(agent, "ens_name", "ensName")
	if ensName == "" && reg != nil {
		channels := firstMap(reg, "channels")
		ens := firstMap(channels, "ens")
		ensName = firstNonEmptyStringMap(ens, "name", "ensName", "ens_name")
	}
	ens := map[string]any{"status": "unavailable", "reason": "ens_not_declared"}
	if ensName != "" {
		ens = map[string]any{"status": "available", "name": ensName}
	}
	return map[string]any{
		"ens":   ens,
		"email": map[string]any{"status": "unavailable", "reason": "deferred_private_reachability"},
		"phone": map[string]any{"status": "unavailable", "reason": "deferred_private_reachability"},
	}
}

func normalizeSoulReadAvatar(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"status": "unavailable", "reason": "avatar_not_available"}
	}
	out := map[string]any{"status": "available"}
	putFirstAny(out, "tokenUri", raw, "token_uri", "tokenUri")
	putFirstAny(out, "image", raw, "image")
	putFirstAny(out, "currentStyleId", raw, "current_style_id", "currentStyleId")
	putFirstAny(out, "currentStyleName", raw, "current_style_name", "currentStyleName")
	putFirstAny(out, "currentRendererAddress", raw, "current_renderer_address", "currentRendererAddress")
	items := firstArrayFromAny(raw, "styles")
	styles := make([]any, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		style := map[string]any{}
		putFirstAny(style, "styleId", m, "style_id", "styleId")
		putFirstAny(style, "styleName", m, "style_name", "styleName")
		putFirstAny(style, "rendererAddress", m, "renderer_address", "rendererAddress")
		putFirstAny(style, "image", m, "image")
		putFirstAny(style, "selected", m, "selected")
		if len(style) > 0 {
			styles = append(styles, style)
		}
	}
	if len(styles) > 0 {
		out["styles"] = styles
	}
	return out
}

func normalizeSoulReadMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[snakeToLowerCamel(key)] = normalizeSoulReadValue(value)
	}
	return out
}

func normalizeSoulReadValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeSoulReadMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, normalizeSoulReadValue(item))
		}
		return out
	default:
		return value
	}
}

func snakeToLowerCamel(key string) string {
	key = strings.TrimSpace(key)
	if !strings.Contains(key, "_") {
		return key
	}
	parts := strings.Split(key, "_")
	out := parts[0]
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		out += strings.ToUpper(part[:1]) + part[1:]
	}
	return out
}

func firstArrayFromAny(raw any, keys ...string) []any {
	switch typed := raw.(type) {
	case []any:
		return typed
	case map[string]any:
		for _, key := range keys {
			if items, ok := typed[key].([]any); ok {
				return items
			}
		}
	}
	return nil
}

func putFirstAny(dest map[string]any, key string, src map[string]any, names ...string) {
	if dest == nil || src == nil || strings.TrimSpace(key) == "" {
		return
	}
	for _, name := range names {
		value, ok := src[name]
		if !ok || value == nil {
			continue
		}
		if s, ok := value.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			dest[key] = s
			return
		}
		dest[key] = value
		return
	}
}
