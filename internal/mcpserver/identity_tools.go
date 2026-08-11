package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
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

	payload, err := authorizedAgentChannelsPayload(ctx, boundOperationIdentitySelfRead)
	if err != nil {
		return identityToolResultFromError(err)
	}

	return toolJSONResult(payload, nil)
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
		payload, err := agentPublicIdentityPayload(ctx, client, agentID)
		if err != nil {
			return identityToolResultFromError(err)
		}
		matches = append(matches, payload)
	}

	return toolJSONResult(map[string]any{
		"query":   q,
		"matches": matches,
		"count":   len(matches),
	}, nil)
}

func whoamiChannelsPayload(ctx context.Context) (map[string]any, error) {
	return authorizedAgentChannelsPayload(ctx, boundOperationIdentitySelfRead)
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

	// Share-grant callers (admitted by the actor-binding middleware with a
	// per-request grant check when caller != actor) resolve agent context from
	// the actor route value. The admission-time grant check replaces the
	// bound-self verification below, which only proves the caller's own
	// binding. Owner requests (actor == caller) and requests without actor
	// context keep the exact pre-existing behavior.
	if actor := auth.ActorFromToolContext(ctx); actor != "" && !strings.EqualFold(actor, username) {
		agentID, err := soulbinding.ResolveAgentID(ctx, actor)
		if err != nil {
			return "", &toolUserError{Code: "upstream_error", Message: err.Error(), Status: 500}
		}
		return normalizeSoulAgentID(agentID), nil
	}

	agentID, err := soulbinding.ResolveAgentID(ctx, username)
	if err != nil {
		return "", &toolUserError{Code: "upstream_error", Message: err.Error(), Status: 500}
	}
	agentID = normalizeSoulAgentID(agentID)
	if agentID == "" {
		return "", nil
	}

	bearerToken := strings.TrimSpace(auth.BearerTokenFromToolContext(ctx))
	if bearerToken == "" {
		return "", oauthBearerRequiredFailure("missing_bearer_token")
	}
	if err := verifyAuthenticatedAgentWithLesser(ctx, bearerToken, agentID, username); err != nil {
		return "", err
	}

	return agentID, nil
}

func verifyAuthenticatedAgentWithLesser(ctx context.Context, bearerToken string, agentID string, username string) error {
	agentID = normalizeSoulAgentID(agentID)
	bearerToken = strings.TrimSpace(bearerToken)
	username = normalizeLocalAgentUsername(username)
	if agentID == "" {
		return newLocalAuthFailure("missing bound soul identity", "missing_bound_soul")
	}
	if bearerToken == "" {
		return oauthBearerRequiredFailure("missing_bearer_token")
	}
	if username == "" {
		return newLocalAuthFailure("missing authenticated agent identity", "missing_authenticated_identity")
	}

	client, err := lesser(ctx)
	if err != nil {
		return &toolUserError{Code: "not_configured", Message: err.Error(), Status: 500}
	}

	boundAny, err := client.DoJSON(ctx, "GET", "/api/v1/souls/bound/me", nil, bearerToken, nil)
	if err != nil {
		return boundSelfVerificationError(err)
	}
	bound, ok := boundAny.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected souls/bound/me response")
	}

	agent, _ := bound["agent"].(map[string]any)
	if normalizeSoulAgentID(stringFromMap(agent, "agent_id")) != agentID {
		return boundSoulNotAuthorizedFailure()
	}

	binding, _ := bound["binding"].(map[string]any)
	if strings.ToLower(stringFromMap(bound, "binding_state")) != "bound" ||
		normalizeLocalAgentUsername(stringFromMap(binding, "agent_username")) != username {
		return boundSoulNotAuthorizedFailure()
	}

	return nil
}

func boundSoulNotAuthorizedFailure() *mcpAuthFailure {
	return &mcpAuthFailure{
		Code:    "forbidden",
		Message: "OAuth identity is not authorized for this bound soul",
		Status:  403,
		Details: map[string]any{
			"source":          "lesser_oauth_passthrough",
			"reauthorize":     true,
			"authAction":      "reauthorize",
			"refreshRequired": false,
			"reason":          "soul_binding_not_authorized",
		},
	}
}

func boundSelfVerificationError(err error) error {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return failure
	}

	var apiErr *lesserapi.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	message, parsed := boundSelfAPIErrorMessage(apiErr.Body)
	if message == "" {
		message = "bound soul self-verification failed"
	}
	details := map[string]any{
		"source":       "lesser_bound_self",
		"upstreamCode": apiErr.Status,
	}
	if parsed != nil {
		details["apiError"] = parsed
		if reason := extractString(parsed, "error_code"); reason != "" {
			details["reason"] = reason
		}
	}

	return &toolUserError{
		Code:    identityErrorCodeForStatus(apiErr.Status),
		Message: message,
		Status:  apiErr.Status,
		Details: details,
	}
}

func boundSelfAPIErrorMessage(body []byte) (string, map[string]any) {
	message, parsed := commExtractAPIErrorMessage(body)
	if parsed == nil {
		return message, nil
	}
	if msg := firstNonEmpty(
		extractString(parsed, "error_description"),
		extractString(parsed, "error"),
		extractString(parsed, "message"),
	); msg != "" {
		return msg, parsed
	}
	return message, parsed
}

func normalizeLocalAgentUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func agentPublicIdentityPayload(ctx context.Context, client *soulapi.Client, agentID string) (map[string]any, error) {
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

	out := map[string]any{
		"agentId": agentID,
		"domain":  stringFromMap(agent, "domain"),
		"localId": stringFromMap(agent, "local_id"),
		"status":  stringFromMap(agent, "status"),
	}
	// CSR-007: managed soul email addresses are not exposed through the
	// public identity lookup path. The identity_lookup tool resolves agent
	// identity metadata only; per-channel contact details remain behind the
	// self-service whoami / agent://channels resource with bound-operation
	// policy enforcement.
	return out, nil
}

func publicManagedEmailPayload(ctx context.Context, client *soulapi.Client, agentID string, localID string) map[string]any {
	regAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID)+"/registration", nil, "", nil)
	if err != nil {
		return nil
	}
	reg, _ := regAny.(map[string]any)
	reg = normalizeSoulReadRegistrationEnvelope(reg)
	channels, _ := reg["channels"].(map[string]any)
	email, _ := channels["email"].(map[string]any)
	address := strings.TrimSpace(stringFromMap(email, "address"))
	if !isCurrentManagedSoulEmailAddress(address, localID) {
		return nil
	}
	return map[string]any{"address": address}
}

func isCurrentManagedSoulEmailAddress(address string, localID string) bool {
	address = strings.TrimSpace(address)
	if address == "" || strings.Contains(address, " ") {
		return false
	}
	localPart, domain, ok := strings.Cut(address, "@")
	if !ok || strings.TrimSpace(localPart) == "" || !strings.EqualFold(strings.TrimSpace(domain), "lessersoul.ai") {
		return false
	}
	normalizedLocalID := normalizeLookupLocalIDOrEmpty(localID)
	if normalizedLocalID == "" {
		return false
	}
	// Legacy bare aliases use <agent-local-id>@lessersoul.ai. They are inbound-only
	// aliases after Project 37 and must not be advertised as the current lookup channel.
	if strings.EqualFold(strings.TrimSpace(localPart), normalizedLocalID) {
		return false
	}
	return true
}

func normalizeLookupLocalIDOrEmpty(raw string) string {
	localID, ok := normalizeLookupLocalID(raw)
	if !ok {
		return ""
	}
	return localID
}

func agentChannelsPayload(ctx context.Context, client *soulapi.Client, agentID string) (map[string]any, error) {
	payload, _, err := agentChannelsPayloadWithRegistration(ctx, client, agentID)
	return payload, err
}

func agentChannelsPayloadWithRegistration(ctx context.Context, client *soulapi.Client, agentID string) (map[string]any, map[string]any, error) {
	if client == nil {
		return nil, nil, errors.New("soul api client is nil")
	}
	agentID = normalizeSoulAgentID(agentID)
	if agentID == "" {
		return nil, nil, &toolUserError{Code: "invalid_request", Message: "missing agentId", Status: 400}
	}

	agentAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID), nil, "", nil)
	if err != nil {
		return nil, nil, err
	}
	agentEnvelope, _ := agentAny.(map[string]any)
	agent, _ := agentEnvelope["agent"].(map[string]any)

	regAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID)+"/registration", nil, "", nil)
	if err != nil {
		return nil, nil, err
	}
	reg, _ := regAny.(map[string]any)
	reg = normalizeSoulReadRegistrationEnvelope(reg)

	channelsRaw, channelsPresent := reg["channels"]
	channels, channelsObject := channelsRaw.(map[string]any)
	contactPreferencesRaw, contactPreferencesPresent := reg["contactPreferences"]
	contactPreferences, contactPreferencesObject := contactPreferencesRaw.(map[string]any)
	channelProvisioning := objectProvisioningMetadata(channels, channelsPresent && channelsObject)
	contactPreferenceProvisioning := objectProvisioningMetadata(contactPreferences, contactPreferencesPresent && contactPreferencesObject)
	communications := "unprovisioned"
	if channelProvisioning["state"] == "present" {
		communications = "configured"
	}

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
		"provisioning": map[string]any{
			"channels":           channelProvisioning,
			"contactPreferences": contactPreferenceProvisioning,
			"communications":     communications,
		},
	}
	return out, reg, nil
}

func objectProvisioningMetadata(value map[string]any, present bool) map[string]any {
	state := "absent"
	if present {
		state = "empty"
		if len(value) > 0 {
			state = "present"
		}
	}
	return map[string]any{
		"state":           state,
		"present":         present,
		"configuredCount": len(value),
	}
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

func newExactQualifiedSoulLookupSearch(domain string, localID string) soulLookupSearch {
	domain = strings.ToLower(strings.TrimSpace(domain))
	localID = strings.ToLower(strings.TrimSpace(localID))
	return soulLookupSearch{Query: domain + "/" + localID}
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

	allowBareRemoteHandleFallback := false
	if agentID, err := resolveAgentIDByIdentifier(ctx, client, q); err == nil && agentID != "" {
		return []string{agentID}, nil
	} else if err != nil {
		var apiErr *soulapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return nil, err
		}
		if looksLikeENSName(q) {
			return nil, nil
		}
		allowBareRemoteHandleFallback = looksLikeEmail(q)
	}

	search, err := prepareSoulLookupSearch(ctx, client, q, false, allowBareRemoteHandleFallback)
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

func prepareSoulLookupSearch(ctx context.Context, client *soulapi.Client, q string, allowENSLikeLocalFallback bool, allowBareRemoteHandleFallback bool) (soulLookupSearch, error) {
	searchQ := normalizeSoulLookupQuery(q)
	if remoteSearch, handled, err := normalizeCanonicalRemoteActorURL(searchQ); handled || err != nil {
		if err != nil {
			return soulLookupSearch{}, err
		}
		return remoteSearch, nil
	}
	if remoteSearch, handled, err := normalizeRemoteActivityPubHandle(searchQ, allowBareRemoteHandleFallback); handled || err != nil {
		if err != nil {
			return soulLookupSearch{}, err
		}
		return remoteSearch, nil
	}
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

	return normalizeLookupLocalID(raw)
}

func normalizeLookupLocalID(raw string) (string, bool) {
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

func normalizeRemoteActivityPubHandle(raw string, allowBareRemoteHandleFallback bool) (soulLookupSearch, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return soulLookupSearch{}, false, nil
	}

	if strings.HasPrefix(raw, "@") {
		parts := strings.SplitN(strings.TrimPrefix(raw, "@"), "@", 2)
		if len(parts) != 2 {
			return soulLookupSearch{}, false, nil
		}
		localID, ok := normalizeLookupLocalID(parts[0])
		if !ok {
			return soulLookupSearch{}, true, unsupportedRemoteActorHandleError(raw)
		}
		domain, ok := normalizeLookupDomain(parts[1])
		if !ok {
			return soulLookupSearch{}, true, unsupportedRemoteActorHandleError(raw)
		}
		return newExactQualifiedSoulLookupSearch(domain, localID), true, nil
	}

	if !allowBareRemoteHandleFallback || strings.Count(raw, "@") != 1 {
		return soulLookupSearch{}, false, nil
	}

	parts := strings.SplitN(raw, "@", 2)
	localID, ok := normalizeLookupLocalID(parts[0])
	if !ok {
		return soulLookupSearch{}, true, unsupportedRemoteActorHandleError(raw)
	}
	domain, ok := normalizeLookupDomain(parts[1])
	if !ok {
		return soulLookupSearch{}, true, unsupportedRemoteActorHandleError(raw)
	}
	return newExactQualifiedSoulLookupSearch(domain, localID), true, nil
}

func normalizeLookupDomain(raw string) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
	if raw == "" || strings.ContainsAny(raw, "/@*") {
		return "", false
	}
	if raw == "localhost" || strings.HasSuffix(raw, ".localhost") || strings.HasSuffix(raw, ".local") || strings.HasSuffix(raw, ".internal") {
		return "", false
	}
	if !strings.Contains(raw, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(raw); err == nil {
		return "", false
	}

	parsed, err := url.Parse("https://" + raw)
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	if parsed.Hostname() != raw || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	labels := strings.Split(parsed.Hostname(), ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", false
		}
		for _, r := range label {
			if ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') || r == '-' {
				continue
			}
			return "", false
		}
	}
	tld := labels[len(labels)-1]
	allDigits := true
	for _, r := range tld {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "", false
	}
	return parsed.Hostname(), true
}

func normalizeCanonicalRemoteActorURL(raw string) (soulLookupSearch, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "://") {
		return soulLookupSearch{}, false, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return soulLookupSearch{}, true, unsupportedRemoteActorURLError(raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return soulLookupSearch{}, true, unsupportedRemoteActorURLError(raw)
	}

	domain, ok := normalizeLookupDomain(parsed.Hostname())
	if !ok {
		return soulLookupSearch{}, true, unsupportedRemoteActorURLError(raw)
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 2 || segments[0] != "users" {
		return soulLookupSearch{}, true, unsupportedRemoteActorURLError(raw)
	}

	localID, ok := normalizeLookupLocalID(segments[1])
	if !ok {
		return soulLookupSearch{}, true, unsupportedRemoteActorURLError(raw)
	}
	return newExactQualifiedSoulLookupSearch(domain, localID), true, nil
}

func unsupportedRemoteActorURLError(query string) error {
	return &toolUserError{
		Code:    "invalid_request",
		Message: "unsupported remote actor URL; supported form is https://<domain>/users/<localId>",
		Status:  400,
		Details: map[string]any{"query": strings.TrimSpace(query)},
	}
}

func unsupportedRemoteActorHandleError(query string) error {
	return &toolUserError{
		Code:    "invalid_request",
		Message: "unsupported remote actor handle; supported form is @<localId>@<public-domain>",
		Status:  400,
		Details: map[string]any{"query": strings.TrimSpace(query)},
	}
}

func resolveAgentIDByIdentifier(ctx context.Context, client *soulapi.Client, q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}

	var path string
	switch {
	case looksLikeEmail(q):
		if _, ok := emailLikeLookupDomain(q); !ok {
			return "", unsupportedRemoteActorHandleError(q)
		}
		return "", privateReachabilityUnavailableError("email", "identity_lookup")
	case looksLikePhoneIdentifier(q):
		return "", privateReachabilityUnavailableError("phone", "identity_lookup")
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

func privateReachabilityUnavailableError(channel string, toolName string) *toolUserError {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = "reachability"
	}
	toolName = strings.TrimSpace(toolName)

	details := map[string]any{
		"source":           "lesser_host_reachability",
		"channel":          channel,
		"requiredContract": "body_facing_instance_key_resolver",
	}
	if toolName != "" {
		details["tool"] = toolName
	}
	if channel == "email" {
		details["publicAlternatives"] = []string{
			"ENS name",
			"full agentId",
			"current-instance local ID",
			"explicit ActivityPub handle in @user@domain form",
			"canonical actor URL",
		}
	}

	return &toolUserError{
		Code:    "private_reachability_unavailable",
		Message: fmt.Sprintf("private %s reachability resolution requires a body-facing lesser-host resolver; request fails closed until that instance-authenticated contract is available", channel),
		Status:  501,
		Details: details,
	}
}

func looksLikeEmail(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" || strings.Contains(q, " ") {
		return false
	}
	parts := strings.Split(q, "@")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
}

func emailLikeLookupDomain(q string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(q), "@", 2)
	if len(parts) != 2 {
		return "", false
	}
	return normalizeLookupDomain(parts[1])
}

func looksLikePhoneIdentifier(q string) bool {
	return normalizeVerificationPhone(q) != ""
}

func looksLikeENSName(q string) bool {
	q = strings.TrimSpace(strings.TrimSuffix(q, "."))
	if q == "" || strings.Contains(q, " ") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(q), ".eth")
}
