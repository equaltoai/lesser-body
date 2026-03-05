package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

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

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return toolErrorResult("unauthorized", err.Error(), 401, nil)
	}

	payload, err := whoamiChannelsPayload(ctx, token)
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
		searchQ := normalizeSoulLookupQuery(q)
		query := url.Values{}
		query.Set("q", searchQ)
		query.Set("limit", "5")
		out, err := client.DoJSON(ctx, "GET", "/api/v1/soul/search", query, "", nil)
		if err != nil {
			return identityToolResultFromError(err)
		}

		resp, ok := out.(map[string]any)
		if !ok {
			return toolErrorResult("upstream_error", "unexpected soul search response", 502, nil)
		}
		results, _ := resp["results"].([]any)
		for _, r := range results {
			rm, _ := r.(map[string]any)
			id, _ := rm["agent_id"].(string)
			id = normalizeSoulAgentID(id)
			if id != "" {
				agentIDs = append(agentIDs, id)
			}
			if len(agentIDs) >= 3 {
				break
			}
		}
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

func whoamiChannelsPayload(ctx context.Context, bearerToken string) (map[string]any, error) {
	client, err := soulapi.Default()
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: 500}
	}

	mineAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/mine", nil, bearerToken, nil)
	if err != nil {
		return nil, err
	}
	mine, ok := mineAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected agents/mine response")
	}

	items, _ := mine["agents"].([]any)
	if len(items) == 0 {
		return nil, &toolUserError{Code: "not_found", Message: "no soul agents found for this identity", Status: 404}
	}

	instanceDomain := inferInstanceDomain()
	candidates := make([]map[string]any, 0, len(items))
	for _, item := range items {
		im, _ := item.(map[string]any)
		agent, _ := im["agent"].(map[string]any)
		if agent == nil {
			continue
		}
		domain, _ := agent["domain"].(string)
		domain = strings.ToLower(strings.TrimSpace(domain))
		if instanceDomain != "" && domain != "" && domain == instanceDomain {
			candidates = append(candidates, im)
		}
	}

	chosen := map[string]any{}
	switch {
	case len(candidates) == 1:
		chosen, _ = candidates[0]["agent"].(map[string]any)
	case len(candidates) > 1:
		details := map[string]any{"instanceDomain": instanceDomain}
		return nil, &toolUserError{Code: "ambiguous_agent", Message: "multiple soul agents match this instance domain", Status: 400, Details: details}
	case len(items) == 1:
		im, _ := items[0].(map[string]any)
		chosen, _ = im["agent"].(map[string]any)
	default:
		available := []string{}
		for _, item := range items {
			im, _ := item.(map[string]any)
			agent, _ := im["agent"].(map[string]any)
			domain, _ := agent["domain"].(string)
			domain = strings.TrimSpace(domain)
			if domain != "" {
				available = append(available, domain)
			}
		}
		details := map[string]any{
			"instanceDomain":    instanceDomain,
			"availableDomains":  available,
			"resolutionHintEnv": []string{"LESSER_API_BASE_URL", "MCP_ENDPOINT"},
		}
		return nil, &toolUserError{Code: "ambiguous_agent", Message: "multiple soul agents found; unable to infer the correct one for this instance", Status: 400, Details: details}
	}

	agentID := normalizeSoulAgentID(stringFromMap(chosen, "agent_id"))
	if agentID == "" {
		return nil, fmt.Errorf("agents/mine missing agent_id")
	}

	payload, err := agentChannelsPayload(ctx, client, agentID)
	if err != nil {
		return nil, err
	}
	return payload, nil
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

	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
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

func inferInstanceDomain() string {
	for _, envKey := range []string{"LESSER_API_BASE_URL", "MCP_ENDPOINT"} {
		raw := strings.TrimSpace(os.Getenv(envKey))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || strings.TrimSpace(u.Host) == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if strings.HasPrefix(host, "api.") {
			host = strings.TrimPrefix(host, "api.")
		}
		if host != "" {
			return host
		}
	}
	return ""
}

func normalizeSoulLookupQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}

	lower := strings.ToLower(q)
	if localID, ok := localIDFromManagedEmail(lower); ok {
		return localID
	}
	if localID, ok := localIDFromManagedENS(lower); ok {
		return localID
	}
	return q
}

func localIDFromManagedEmail(q string) (string, bool) {
	parts := strings.Split(q, "@")
	if len(parts) != 2 {
		return "", false
	}
	localPart := strings.TrimSpace(parts[0])
	domain := strings.TrimSpace(parts[1])
	if localPart == "" || domain != "lessersoul.ai" {
		return "", false
	}
	localPart = strings.Split(localPart, "+")[0]
	localPart = strings.TrimSpace(localPart)
	if localPart == "" {
		return "", false
	}
	return localPart, true
}

func localIDFromManagedENS(q string) (string, bool) {
	if !strings.HasSuffix(q, ".lessersoul.eth") {
		return "", false
	}
	localID := strings.TrimSuffix(q, ".lessersoul.eth")
	localID = strings.TrimSuffix(localID, ".")
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return "", false
	}
	if strings.Contains(localID, ".") {
		parts := strings.Split(localID, ".")
		localID = strings.TrimSpace(parts[0])
	}
	return localID, localID != ""
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
