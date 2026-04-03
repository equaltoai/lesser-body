package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

var localAgentIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,62}[a-z0-9]$`)

type toolUserError struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
}

func (e *toolUserError) Error() string {
	if e == nil {
		return "error"
	}
	return strings.TrimSpace(e.Message)
}

func handleIdentityWhoami(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	if raw := strings.TrimSpace(string(args)); raw != "" && raw != "{}" && raw != "null" {
		return toolErrorResult("invalid_request", "no arguments expected", 400, nil)
	}

	if _, err := requireOAuthBearer(ctx); err != nil {
		return authToolResultFromError(err)
	}

	payload, err := whoamiChannelsPayload(ctx)
	if err != nil {
		return identityToolResultFromError(err)
	}

	return toolJSONResult(payload)
}

func handleIdentityLookup(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return toolErrorResult("invalid_request", "missing query", 400, nil)
	}

	client, err := soulapi.Default()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	agentIDs := []string{}
	if isSoulAgentID(q) {
		agentIDs = append(agentIDs, normalizeSoulAgentID(q))
	} else {
		resolvedAgentIDs, err := lookupAgentIDs(ctx, client, q)
		if err != nil {
			return identityToolResultFromError(err)
		}
		agentIDs = append(agentIDs, resolvedAgentIDs...)
		if len(agentIDs) == 0 {
			return toolErrorResult("not_found", "no matching agent found", 404, map[string]any{"query": q})
		}
	}

	matches := make([]any, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		payload, err := agentChannelsPayload(ctx, client, agentID)
		if err != nil {
			return identityToolResultFromError(err)
		}
		matches = append(matches, payload)
	}

	return toolJSONResult(map[string]any{
		"query":   q,
		"matches": matches,
		"count":   len(matches),
	})
}

func whoamiChannelsPayload(ctx context.Context) (map[string]any, error) {
	client, err := soulapi.Default()
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: 500}
	}

	agentID, err := authenticatedAgentID(ctx)
	if err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, &toolUserError{Code: "not_found", Message: "no bound soul found for this agent", Status: 404}
	}

	payload, err := agentChannelsPayload(ctx, client, agentID)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func authenticatedAgentID(ctx context.Context) (string, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken {
		return "", oauthBearerRequiredFailure("missing_oauth_bearer")
	}

	username := strings.TrimSpace(principal.Identity)
	if username == "" && principal.Claims != nil {
		username = strings.TrimSpace(principal.Claims.GetUsername())
	}
	if username == "" {
		return "", newLocalAuthFailure("missing authenticated agent identity", "missing_authenticated_identity")
	}

	agentID, err := soulbinding.ResolveAgentID(ctx, username)
	if err != nil {
		return "", &toolUserError{Code: "upstream_error", Message: err.Error(), Status: 500}
	}
	return normalizeSoulAgentID(agentID), nil
}

func agentChannelsPayload(ctx context.Context, client *soulapi.Client, agentID string) (map[string]any, error) {
	if client == nil {
		return nil, errors.New("soul api client is nil")
	}
	agentID = normalizeSoulAgentID(agentID)
	if agentID == "" {
		return nil, &toolUserError{Code: "invalid_request", Message: "missing agentId", Status: 400}
	}

	agentAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID), nil, "", nil)
	if err != nil {
		return nil, err
	}
	agentEnvelope, _ := agentAny.(map[string]any)
	agent, _ := agentEnvelope["agent"].(map[string]any)

	regAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID)+"/registration", nil, "", nil)
	if err != nil {
		return nil, err
	}
	reg, _ := regAny.(map[string]any)

	channels, _ := reg["channels"].(map[string]any)
	contactPreferences, _ := reg["contactPreferences"].(map[string]any)

	out := map[string]any{
		"agentId": agentID,
		"domain":  stringFromMap(agent, "domain"),
		"localId": stringFromMap(agent, "local_id"),
		"status":  stringFromMap(agent, "status"),
		"channels": func() any {
			if channels == nil {
				return map[string]any{}
			}
			return channels
		}(),
		"contactPreferences": func() any {
			if contactPreferences == nil {
				return map[string]any{}
			}
			return contactPreferences
		}(),
	}
	return out, nil
}

func identityToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return toolErrorResult("upstream_error", "error", 500, nil)
	}

	var userErr *toolUserError
	if errors.As(err, &userErr) {
		return toolErrorResult(userErr.Code, userErr.Message, userErr.Status, userErr.Details)
	}

	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}

	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
		if failure := soulAuthFailureFromError(err); failure != nil {
			return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
		}
		code := identityErrorCodeForStatus(apiErr.Status)
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)
		details := map[string]any{}
		if parsed != nil {
			details["apiError"] = parsed
		}
		if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
			details["retryAfterSeconds"] = retryAfter
		}
		return toolErrorResult(code, message, apiErr.Status, details)
	}

	return toolErrorResult("upstream_error", err.Error(), 0, nil)
}

func identityErrorCodeForStatus(status int) string {
	switch status {
	case 400, 422:
		return "invalid_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "unknown_error"
	}
}

func normalizeSoulLookupQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return q
}

type soulLookupSearch struct {
	Query  string
	Domain string
}

func isSoulAgentID(q string) bool {
	q = strings.TrimSpace(q)
	if !strings.HasPrefix(q, "0x") {
		return false
	}
	if len(q) < 10 {
		return false
	}
	for _, r := range q[2:] {
		if ('0' <= r && r <= '9') || ('a' <= r && r <= 'f') || ('A' <= r && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func normalizeSoulAgentID(q string) string {
	q = strings.TrimSpace(q)
	if !strings.HasPrefix(q, "0x") {
		return ""
	}
	return strings.ToLower(q)
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw, _ := m[key].(string)
	return strings.TrimSpace(raw)
}

func lookupAgentIDs(ctx context.Context, client *soulapi.Client, q string) ([]string, error) {
	if client == nil {
		return nil, errors.New("soul api client is nil")
	}

	allowENSLikeLocalFallback := false
	if agentID, err := resolveAgentIDByIdentifier(ctx, client, q); err == nil && agentID != "" {
		return []string{agentID}, nil
	} else if err != nil {
		var apiErr *soulapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return nil, err
		}
		allowENSLikeLocalFallback = looksLikeENSName(q)
	}

	search, err := prepareSoulLookupSearch(ctx, client, q, allowENSLikeLocalFallback)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("q", search.Query)
	if search.Domain != "" {
		query.Set("domain", search.Domain)
	}
	query.Set("limit", "5")
	out, err := client.DoJSON(ctx, "GET", "/api/v1/soul/search", query, "", nil)
	if err != nil {
		return nil, err
	}

	resp, ok := out.(map[string]any)
	if !ok {
		return nil, errors.New("unexpected soul search response")
	}
	results, _ := resp["results"].([]any)
	agentIDs := make([]string, 0, len(results))
	for _, r := range results {
		rm, _ := r.(map[string]any)
		id, _ := rm["agent_id"].(string)
		id = normalizeSoulAgentID(id)
		if id == "" {
			continue
		}
		agentIDs = append(agentIDs, id)
		if len(agentIDs) >= 3 {
			break
		}
	}
	return agentIDs, nil
}

func prepareSoulLookupSearch(ctx context.Context, client *soulapi.Client, q string, allowENSLikeLocalFallback bool) (soulLookupSearch, error) {
	searchQ := normalizeSoulLookupQuery(q)
	localID, ok := normalizeCurrentInstanceLocalLookupQuery(searchQ, allowENSLikeLocalFallback)
	if !ok {
		return soulLookupSearch{Query: searchQ}, nil
	}

	domain, err := authenticatedAgentDomain(ctx, client)
	if err != nil {
		return soulLookupSearch{}, err
	}
	if domain == "" {
		return soulLookupSearch{}, &toolUserError{
			Code:    "invalid_request",
			Message: "current-instance local ID lookup requires a trustworthy instance domain; use a domain-qualified query instead",
			Status:  400,
			Details: map[string]any{"query": q},
		}
	}

	return soulLookupSearch{
		Query:  localID,
		Domain: domain,
	}, nil
}

func authenticatedAgentDomain(ctx context.Context, client *soulapi.Client) (string, error) {
	if client == nil {
		return "", errors.New("soul api client is nil")
	}

	agentID, err := authenticatedAgentID(ctx)
	if err != nil {
		return "", err
	}
	if agentID == "" {
		return "", &toolUserError{
			Code:    "invalid_request",
			Message: "current-instance local ID lookup requires an authenticated soul identity",
			Status:  400,
		}
	}

	agentAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID), nil, "", nil)
	if err != nil {
		return "", err
	}
	agentEnvelope, _ := agentAny.(map[string]any)
	agent, _ := agentEnvelope["agent"].(map[string]any)

	domain := stringFromMap(agent, "domain")
	if domain == "" {
		return "", &toolUserError{
			Code:    "invalid_request",
			Message: "current-instance local ID lookup requires a trustworthy instance domain; use a domain-qualified query instead",
			Status:  400,
			Details: map[string]any{"agentId": agentID},
		}
	}
	return domain, nil
}

func normalizeCurrentInstanceLocalLookupQuery(raw string, allowENSLikeLocalFallback bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || isSoulAgentID(raw) || looksLikeEmail(raw) {
		return "", false
	}
	if !allowENSLikeLocalFallback && looksLikeENSName(raw) {
		return "", false
	}

	localID := strings.ToLower(strings.TrimSpace(raw))
	localID = strings.TrimPrefix(localID, "@")
	localID = strings.TrimSuffix(localID, "/")

	if localID == "" || strings.ContainsAny(localID, "/:@") {
		return "", false
	}
	if len(localID) < 3 || len(localID) > 64 {
		return "", false
	}
	if !localAgentIDRE.MatchString(localID) {
		return "", false
	}
	return localID, true
}

func resolveAgentIDByIdentifier(ctx context.Context, client *soulapi.Client, q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}

	var path string
	switch {
	case looksLikeEmail(q):
		path = "/api/v1/soul/resolve/email/" + url.PathEscape(q)
	case looksLikeENSName(q):
		path = "/api/v1/soul/resolve/ens/" + url.PathEscape(q)
	default:
		return "", nil
	}

	out, err := client.DoJSON(ctx, "GET", path, nil, "", nil)
	if err != nil {
		return "", err
	}

	resp, _ := out.(map[string]any)
	agent, _ := resp["agent"].(map[string]any)
	return normalizeSoulAgentID(stringFromMap(agent, "agent_id")), nil
}

func looksLikeEmail(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" || strings.Contains(q, " ") {
		return false
	}
	parts := strings.Split(q, "@")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
}

func looksLikeENSName(q string) bool {
	q = strings.TrimSpace(strings.TrimSuffix(q, "."))
	if q == "" || strings.Contains(q, " ") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(q), ".eth")
}
