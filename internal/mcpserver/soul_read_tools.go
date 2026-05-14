package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	soulReadMaxMatches                      = 3
	soulReadDefaultClaimLevel               = "self-declared"
	soulReadPrivateMintConversationsBlock   = "mintConversations"
	soulReadPrivateDefaultConversationLimit = 20
	soulReadPrivateMaxConversationLimit     = 50
	soulReadPrivateConversationIDMaxLen     = 128
	// Keep Body's private single-read MCP response below AppTheory's stream
	// event limit so callers receive a stable tool error before the MCP stream
	// store is asked to persist an undeliverable event.
	soulReadPrivateSingleDefaultMaxStreamEventBytes = 10 * 1024 * 1024
	soulReadPrivateSingleDeliveryHeadroomBytes      = 64 * 1024
)

var soulReadPrivateConversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type soulReadInput struct {
	Query                 string   `json:"query"`
	AgentID               string   `json:"agentId"`
	ENSName               string   `json:"ensName"`
	Self                  bool     `json:"self,omitempty"`
	Limit                 int      `json:"limit,omitempty"`
	IncludePrivate        []string `json:"include_private,omitempty"`
	MintConversationID    string   `json:"mintConversationId,omitempty"`
	MintConversationLimit int      `json:"mintConversationLimit,omitempty"`
	IncludeRaw            bool     `json:"include_raw,omitempty"`
}

type soulReadPrivateRequest struct {
	IncludeMintConversations bool
	ConversationID           string
	Limit                    int
}

func handleSoulRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in soulReadInput
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}

	agentIDInput := strings.TrimSpace(in.AgentID)
	ensNameInput := strings.TrimSpace(in.ENSName)
	queryInput := strings.TrimSpace(in.Query)
	query := firstNonEmpty(agentIDInput, ensNameInput, queryInput)
	accessMode := "public"

	if in.Self && query != "" {
		return toolErrorResult("invalid_request", "self cannot be combined with query, agentId, or ensName", 400, nil)
	}
	if !in.Self && query == "" {
		return toolErrorResult("invalid_request", "query, agentId, or ensName is required", 400, nil)
	}
	privateReq, err := soulReadPrivateRequestFromInput(in)
	if err != nil {
		return identityToolResultFromError(err)
	}
	if ensNameInput != "" && !looksLikeENSName(ensNameInput) {
		return toolErrorResult("invalid_request", "ensName must be a public ENS name", 400, map[string]any{"query": ensNameInput})
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
	if in.Self {
		agentID, err := authenticatedAgentID(ctx)
		if err != nil {
			return identityToolResultFromError(err)
		}
		if agentID == "" {
			return toolErrorResult("not_found", "no bound soul found for this agent", 404, nil)
		}
		query = "self"
		accessMode = "self"
		agentIDs = append(agentIDs, agentID)
	} else if agentIDInput != "" || isSoulAgentID(query) {
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
		soul, err := soulReadPayload(ctx, client, agentID, in.IncludeRaw, privateReq)
		if err != nil {
			return identityToolResultFromError(err)
		}
		souls = append(souls, soul)
	}

	payload := map[string]any{
		"query":  query,
		"count":  len(souls),
		"access": soulReadAccess(accessMode, privateReq.privateBlocks()),
		"souls":  souls,
	}
	return soulReadToolResult(payload, privateReq)
}

func soulReadToolResult(payload map[string]any, privateReq soulReadPrivateRequest) (*mcpruntime.ToolResult, error) {
	textPayload := any(payload)
	if privateReq.IncludeMintConversations && privateReq.ConversationID != "" {
		textPayload = soulReadPrivateSingleTextPayload(payload)
	}
	b, err := json.Marshal(textPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal soul_read tool result: %w", err)
	}
	structured := map[string]any{"data": payload}
	result := &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		StructuredContent: structured,
	}

	if privateReq.IncludeMintConversations && privateReq.ConversationID != "" {
		responseBytes, err := mcpruntime.MarshalResponse(mcpruntime.NewResultResponse("soul_read_delivery_probe", result))
		if err != nil {
			return nil, fmt.Errorf("marshal soul_read MCP delivery envelope: %w", err)
		}
		maxResponseBytes := soulReadPrivateSingleMaxMCPResponseBytes()
		if len(responseBytes) > maxResponseBytes {
			return toolErrorResult("response_too_large", "soul_read private mint-conversation response exceeds MCP delivery limit", http.StatusRequestEntityTooLarge, map[string]any{
				"source":        "lesser_private_self_scope",
				"privateBlock":  soulReadPrivateMintConversationsBlock,
				"mode":          "single",
				"measuredBytes": len(responseBytes),
				"maxBytes":      maxResponseBytes,
			})
		}
	}

	return result, nil
}

func soulReadPrivateSingleMaxMCPResponseBytes() int {
	streamMax := soulReadPrivateSingleDefaultMaxStreamEventBytes
	if raw := strings.TrimSpace(os.Getenv("MCP_STREAM_MAX_EVENT_BYTES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			streamMax = n
		}
	}
	if streamMax <= soulReadPrivateSingleDeliveryHeadroomBytes {
		return streamMax / 2
	}
	return streamMax - soulReadPrivateSingleDeliveryHeadroomBytes
}

func soulReadPrivateSingleTextPayload(payload map[string]any) map[string]any {
	b, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return payload
	}

	souls, _ := out["souls"].([]any)
	for _, soulAny := range souls {
		soul, _ := soulAny.(map[string]any)
		privateBlocks, _ := soul["private"].(map[string]any)
		mint, _ := privateBlocks[soulReadPrivateMintConversationsBlock].(map[string]any)
		if mint == nil || mint["mode"] != "single" {
			continue
		}
		conversation, _ := mint["conversation"].(map[string]any)
		if conversation == nil {
			continue
		}
		omit := map[string]any{
			"omitted": true,
			"reason":  "available_in_structured_content",
		}
		if _, ok := conversation["messages"]; ok {
			conversation["messages"] = omit
		}
		if _, ok := conversation["producedDeclarations"]; ok {
			conversation["producedDeclarations"] = omit
		}
		mint["textContentPolicy"] = map[string]any{
			"privateFieldsOmitted": []any{"messages", "producedDeclarations"},
			"fullContentLocation":  "structuredContent.data",
		}
	}
	return out
}

func soulReadPrivateRequestFromInput(in soulReadInput) (soulReadPrivateRequest, error) {
	out := soulReadPrivateRequest{}
	privateBlocks := map[string]struct{}{}
	for _, raw := range in.IncludePrivate {
		block := strings.TrimSpace(raw)
		switch block {
		case "":
			continue
		case soulReadPrivateMintConversationsBlock:
			privateBlocks[block] = struct{}{}
		default:
			return out, &toolUserError{Code: "invalid_request", Message: "unsupported include_private block: " + block, Status: 400}
		}
	}

	_, wantsMintConversations := privateBlocks[soulReadPrivateMintConversationsBlock]
	conversationID := strings.TrimSpace(in.MintConversationID)
	if conversationID != "" && !wantsMintConversations {
		return out, &toolUserError{Code: "invalid_request", Message: "mintConversationId requires include_private=[\"mintConversations\"]", Status: 400}
	}
	if in.MintConversationLimit != 0 && !wantsMintConversations {
		return out, &toolUserError{Code: "invalid_request", Message: "mintConversationLimit requires include_private=[\"mintConversations\"]", Status: 400}
	}
	if !wantsMintConversations {
		return out, nil
	}
	if !in.Self {
		return out, &toolUserError{Code: "invalid_request", Message: "private mint-conversation expansion requires self=true", Status: 400}
	}
	if conversationID != "" {
		if len(conversationID) > soulReadPrivateConversationIDMaxLen || !soulReadPrivateConversationIDPattern.MatchString(conversationID) {
			return out, &toolUserError{Code: "invalid_request", Message: "mintConversationId must be an opaque safe path value", Status: 400}
		}
		if in.MintConversationLimit > 0 {
			return out, &toolUserError{Code: "invalid_request", Message: "mintConversationLimit cannot be combined with mintConversationId", Status: 400}
		}
		out.ConversationID = conversationID
	}

	limit := in.MintConversationLimit
	if limit < 0 || limit > soulReadPrivateMaxConversationLimit {
		return out, &toolUserError{Code: "invalid_request", Message: "mintConversationLimit must be between 1 and 50", Status: 400}
	}
	if limit == 0 {
		limit = soulReadPrivateDefaultConversationLimit
	}
	out.IncludeMintConversations = true
	out.Limit = limit
	return out, nil
}

func (r soulReadPrivateRequest) privateBlocks() []any {
	if !r.IncludeMintConversations {
		return nil
	}
	return []any{soulReadPrivateMintConversationsBlock}
}

func soulReadAccess(mode string, privateBlocks []any) map[string]any {
	mode = strings.ToLower(strings.TrimSpace(mode))
	callerRelation := "public"
	resolution := "public_lookup"
	authorization := "mcp_read_scope"
	publicOnly := true
	privateExpansion := false
	if mode == "self" {
		callerRelation = "self"
		resolution = "bound_caller"
	} else {
		mode = "public"
	}
	if len(privateBlocks) > 0 {
		publicOnly = false
		privateExpansion = true
		authorization = "lesser_self_scope_instance_trust"
	}
	out := map[string]any{
		"mode":             mode,
		"callerRelation":   callerRelation,
		"publicOnly":       publicOnly,
		"privateExpansion": privateExpansion,
		"authorization":    authorization,
		"resolution":       resolution,
	}
	if len(privateBlocks) > 0 {
		out["privateBlocks"] = privateBlocks
	}
	return out
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

func soulReadPayload(ctx context.Context, client *soulapi.Client, agentID string, includeRaw bool, privateReq soulReadPrivateRequest) (map[string]any, error) {
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
	registration := normalizeSoulReadRegistrationEnvelope(registrationRaw)

	capabilitiesPath := agentPath + "/capabilities"
	capabilitiesRaw, capabilitiesSource := soulReadOptional(ctx, client, "capabilities", capabilitiesPath)
	sources = append(sources, capabilitiesSource)

	boundariesPath := agentPath + "/boundaries"
	boundariesRaw, boundariesSource := soulReadOptional(ctx, client, "boundaries", boundariesPath)
	sources = append(sources, boundariesSource)

	transparencyPath := agentPath + "/transparency"
	transparencyRaw, transparencySource := soulReadOptional(ctx, client, "transparency", transparencyPath)
	sources = append(sources, transparencySource)

	avatar := firstMap(agent, "avatar")
	if len(avatar) == 0 {
		avatar = firstMap(registration, "avatar")
	}

	payload := map[string]any{
		"agentId":         agentID,
		"identity":        normalizeSoulReadIdentity(agentID, agent),
		"registration":    normalizeSoulReadRegistration(registration, agent),
		"capabilities":    normalizeSoulReadCapabilities(capabilitiesRaw, registration),
		"boundaries":      normalizeSoulReadBoundaries(boundariesRaw, registration),
		"transparency":    normalizeSoulReadTransparency(transparencyRaw, registration),
		"channels":        normalizeSoulReadChannels(agent, registration),
		"avatar":          normalizeSoulReadAvatar(avatar),
		"sources":         sources,
		"sourceEndpoints": soulReadSourceEndpoints(sources),
		"deferred":        deferred,
	}
	if includeRaw {
		payload["_raw"] = map[string]any{
			"identity":     sanitizeSoulReadRawPayload(agentRaw),
			"registration": sanitizeSoulReadRawPayload(registrationRaw),
			"capabilities": sanitizeSoulReadRawPayload(capabilitiesRaw),
			"boundaries":   sanitizeSoulReadRawPayload(boundariesRaw),
			"transparency": sanitizeSoulReadRawPayload(transparencyRaw),
		}
	}
	if privateReq.IncludeMintConversations {
		privatePayload, err := soulReadPrivateMintConversations(ctx, privateReq)
		if err != nil {
			return nil, err
		}
		payload["private"] = map[string]any{
			soulReadPrivateMintConversationsBlock: privatePayload,
		}
	}
	return payload, nil
}

func sanitizeSoulReadRawPayload(raw any) any {
	switch typed := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			if soulReadRawFieldIsPrivateReachability(key) {
				out[key] = soulReadPrivateReachabilityRedaction(key)
				continue
			}
			out[key] = sanitizeSoulReadRawPayload(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeSoulReadRawPayload(item))
		}
		return out
	default:
		return raw
	}
}

func soulReadRawFieldIsPrivateReachability(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("_", "", "-", "").Replace(key)
	switch {
	case key == "channels":
		return true
	case strings.Contains(key, "contactpreference"):
		return true
	case strings.Contains(key, "email"):
		return true
	case strings.Contains(key, "phone"):
		return true
	default:
		return false
	}
}

func soulReadPrivateReachabilityRedaction(key string) map[string]any {
	return map[string]any{
		"redacted": true,
		"reason":   "private_reachability",
		"field":    strings.TrimSpace(key),
	}
}

func soulReadPrivateMintConversations(ctx context.Context, privateReq soulReadPrivateRequest) (map[string]any, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: 500}
	}

	path := "/api/v1/souls/bound/me/mint-conversations"
	query := url.Values{}
	mode := "list"
	if privateReq.ConversationID != "" {
		path += "/" + url.PathEscape(privateReq.ConversationID)
		mode = "single"
	} else {
		query.Set("limit", strconv.Itoa(privateReq.Limit))
	}

	raw, headers, err := client.DoJSONWithHeaders(ctx, "GET", path, query, token, nil)
	if err != nil {
		return nil, soulReadPrivateLesserError(err, headers)
	}

	var out map[string]any
	if mode == "single" {
		out = normalizeSoulReadMintConversationSingle(raw)
	} else {
		out = normalizeSoulReadMintConversationList(raw)
	}
	out["mode"] = mode
	out["source"] = soulReadSource(soulReadPrivateMintConversationsBlock, path, "ok", "")
	return out, nil
}

func normalizeSoulReadRegistrationEnvelope(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	if nested := firstMap(m, "registration"); nested != nil {
		return nested
	}
	return m
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

func soulReadSourceEndpoints(sources []map[string]any) map[string]any {
	out := map[string]any{}
	for _, source := range sources {
		block, _ := source["block"].(string)
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		entry := map[string]any{}
		for _, key := range []string{"endpoint", "status", "reason"} {
			value, _ := source[key].(string)
			if strings.TrimSpace(value) != "" {
				entry[key] = strings.TrimSpace(value)
			}
		}
		out[block] = entry
	}
	return out
}

func soulReadPrivateLesserError(err error, headers http.Header) error {
	if err == nil {
		return nil
	}
	if failure := lesserAuthFailureFromError(err); failure != nil {
		return failure
	}

	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		message, upstreamCode, parsed := soulReadPrivateLesserErrorMessage(apiErr.Body)
		details := map[string]any{
			"source":         "lesser_private_self_scope",
			"upstreamStatus": apiErr.Status,
		}
		if upstreamCode != "" {
			details["upstreamErrorCode"] = upstreamCode
		}
		if parsed != nil {
			details["apiError"] = parsed
		}
		if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
			details["retryAfter"] = retryAfter
		}
		return &toolUserError{
			Code:    soulReadPrivateToolErrorCode(apiErr.Status, upstreamCode),
			Message: message,
			Status:  apiErr.Status,
			Details: details,
		}
	}
	return &toolUserError{Code: "upstream_error", Message: err.Error(), Status: 500}
}

func soulReadPrivateLesserErrorMessage(body []byte) (message string, upstreamCode string, parsed map[string]any) {
	raw := strings.TrimSpace(string(body))
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			upstreamCode = firstNonEmptyStringMap(parsed, "error_code", "code")
			message = firstNonEmptyStringMap(parsed, "error_description", "error", "message")
			if message != "" {
				return message, upstreamCode, parsed
			}
		}
	}
	message, parsed = commExtractAPIErrorMessage(body)
	upstreamCode = firstNonEmptyStringMap(parsed, "error_code", "code")
	return message, upstreamCode, parsed
}

func soulReadPrivateToolErrorCode(status int, upstreamCode string) string {
	switch strings.ToUpper(strings.TrimSpace(upstreamCode)) {
	case "SOUL_PRIVATE_RATE_LIMITED":
		return "rate_limited"
	case "SOUL_PRIVATE_RESPONSE_TOO_LARGE":
		return "response_too_large"
	case "SOUL_PRIVATE_TRUST_NOT_CONFIGURED":
		return "not_configured"
	case "SOUL_PRIVATE_INSTANCE_TRUST_REJECTED":
		return "conflict"
	case "SOUL_BOUND_AGENT_NOT_FOUND", "SOUL_BOUND_AGENT_NOT_AVAILABLE", "SOUL_PRIVATE_CONVERSATION_NOT_FOUND":
		return "not_found"
	case "SOUL_PRIVATE_LIMIT_INVALID", "SOUL_PRIVATE_QUERY_UNSUPPORTED", "SOUL_PRIVATE_CURSOR_UNSUPPORTED", "SOUL_PRIVATE_CONVERSATION_ID_REQUIRED", "SOUL_PRIVATE_CONVERSATION_ID_INVALID", "SOUL_PRIVATE_REQUEST_BODY_UNSUPPORTED", "SOUL_PRIVATE_INVALID_REQUEST":
		return "invalid_request"
	}
	switch status {
	case 400, 422:
		return "invalid_request"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 413:
		return "response_too_large"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "unknown_error"
	}
}

func normalizeSoulReadMintConversationList(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	out := map[string]any{}
	putFirstAny(out, "version", m, "version")
	putFirstAny(out, "count", m, "count")
	putFirstAny(out, "limit", m, "limit")
	putFirstAny(out, "nextCursor", m, "next_cursor", "nextCursor")
	items := firstArrayFromAny(raw, "conversations", "items", "results")
	conversations := make([]any, 0, len(items))
	for _, item := range items {
		conversation, _ := item.(map[string]any)
		if conversation == nil {
			continue
		}
		conversations = append(conversations, normalizeSoulReadMintConversation(conversation, false))
	}
	out["conversations"] = conversations
	return out
}

func normalizeSoulReadMintConversationSingle(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	conversation := firstMap(m, "conversation")
	if conversation == nil {
		conversation = m
	}
	out := map[string]any{}
	putFirstAny(out, "version", m, "version")
	out["conversation"] = normalizeSoulReadMintConversation(conversation, true)
	return out
}

func normalizeSoulReadMintConversation(raw map[string]any, includePrivateContent bool) map[string]any {
	out := map[string]any{}
	putFirstAny(out, "agentId", raw, "agent_id", "agentId")
	putFirstAny(out, "conversationId", raw, "conversation_id", "conversationId")
	putFirstAny(out, "model", raw, "model")
	putFirstAny(out, "status", raw, "status")
	putFirstAny(out, "usage", raw, "usage")
	putFirstAny(out, "chargedCredits", raw, "charged_credits", "chargedCredits")
	putFirstAny(out, "createdAt", raw, "created_at", "createdAt")
	putFirstAny(out, "completedAt", raw, "completed_at", "completedAt")
	if includePrivateContent {
		putFirstAny(out, "messages", raw, "messages")
		putFirstAny(out, "producedDeclarations", raw, "produced_declarations", "producedDeclarations")
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
		switch typed := item.(type) {
		case string:
			name := strings.TrimSpace(typed)
			if name == "" {
				continue
			}
			out = append(out, map[string]any{
				"name":       name,
				"claimLevel": soulReadDefaultClaimLevel,
			})
		case map[string]any:
			capability := map[string]any{}
			putFirstAny(capability, "name", typed, "capability", "name")
			putFirstAny(capability, "scope", typed, "scope")
			putFirstAny(capability, "constraints", typed, "constraints")
			putFirstAny(capability, "claimLevel", typed, "claim_level", "claimLevel")
			putFirstAny(capability, "lastValidated", typed, "last_validated", "lastValidated")
			putFirstAny(capability, "validationRef", typed, "validation_ref", "validationRef")
			putFirstAny(capability, "degradesTo", typed, "degrades_to", "degradesTo")
			if _, ok := capability["name"]; ok {
				if _, ok := capability["claimLevel"]; !ok {
					capability["claimLevel"] = soulReadDefaultClaimLevel
				}
			}
			if len(capability) > 0 {
				out = append(out, capability)
			}
		default:
			continue
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
			switch value := typed[key].(type) {
			case []any:
				return value
			case map[string]any:
				if items := firstArrayFromAny(value, "items", "results"); len(items) > 0 {
					return items
				}
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
