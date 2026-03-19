package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

type verificationMarkers struct {
	agentIDs map[string]bool
	emails   map[string]bool
	phones   map[string]bool
	ensNames map[string]bool
}

func handleIdentityVerify(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Channel    string `json:"channel"`
		Identifier string `json:"identifier"`
		MessageID  string `json:"messageId,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}

	in.Channel = strings.ToLower(strings.TrimSpace(in.Channel))
	in.Identifier = strings.TrimSpace(in.Identifier)
	in.MessageID = strings.TrimSpace(in.MessageID)
	if in.Channel != "ens" && in.Channel != "email" && in.Channel != "phone" {
		return toolErrorResult("invalid_request", "channel must be one of ens, email, or phone", 400, nil)
	}
	if in.Identifier == "" {
		return toolErrorResult("invalid_request", "identifier is required", 400, nil)
	}

	client, err := soulapi.Default()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	resolvedIdentifier := normalizeVerificationIdentifier(in.Channel, in.Identifier)
	if resolvedIdentifier == "" {
		return toolErrorResult("invalid_request", "identifier format is invalid for the selected channel", 400, nil)
	}
	agent, channelsPayload, err := resolveIdentityForVerification(ctx, client, in.Channel, resolvedIdentifier)
	if err != nil {
		return identityToolResultFromError(err)
	}

	agentID := normalizeSoulAgentID(stringFromMap(agent, "agent_id"))
	if agentID == "" {
		return toolErrorResult("upstream_error", "resolved identity missing agent_id", 502, nil)
	}

	result := map[string]any{
		"channel":           in.Channel,
		"identifier":        resolvedIdentifier,
		"verificationScope": "identifier",
		"identityResolved":  true,
		"verified":          true,
		"agent":             summarizeResolvedAgent(agent),
		"channels":          mapOrEmpty(channelsPayload, "channels"),
		"contactPreferences": func() any {
			if v, ok := channelsPayload["contactPreferences"]; ok && v != nil {
				return v
			}
			return map[string]any{}
		}(),
	}

	if in.MessageID == "" {
		return toolJSONResult(result)
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}

	notification, err := findCommunicationNotification(ctx, token, in.MessageID)
	if err != nil {
		return identityVerifyNotificationResultFromError(err)
	}

	result["verificationScope"] = "message"
	result["messageId"] = in.MessageID
	if notification == nil {
		result["verified"] = false
		result["messageFound"] = false
		result["reason"] = "message_not_found"
		return toolJSONResult(result)
	}

	from := commFrom(notification)
	match, matchedBy := verifySenderAgainstResolvedIdentity(agentID, in.Channel, resolvedIdentifier, channelsPayload, from)
	result["messageFound"] = true
	result["verified"] = match
	result["message"] = map[string]any{
		"messageId":      firstNonEmpty(in.MessageID, commMessageID(notification)),
		"notificationId": strings.TrimSpace(stringFromMap(notification, "id")),
		"channel":        notificationChannel(notification),
		"from":           from,
		"to":             commTo(notification),
		"subject":        commSubject(notification),
		"body":           commBody(notification),
		"receivedAt":     commReceivedAt(notification),
	}
	if match {
		result["matchedBy"] = matchedBy
		return toolJSONResult(result)
	}

	result["reason"] = "sender_does_not_match_resolved_identity"
	return toolJSONResult(result)
}

func resolveIdentityForVerification(ctx context.Context, client *soulapi.Client, channel string, identifier string) (map[string]any, map[string]any, error) {
	if client == nil {
		return nil, nil, errors.New("soul api client is nil")
	}

	resolvePath := "/api/v1/soul/resolve/email/" + url.PathEscape(identifier)
	switch channel {
	case "ens":
		resolvePath = "/api/v1/soul/resolve/ens/" + url.PathEscape(identifier)
	case "phone":
		resolvePath = "/api/v1/soul/resolve/phone/" + url.PathEscape(identifier)
	}

	resolvedAny, err := client.DoJSON(ctx, "GET", resolvePath, nil, "", nil)
	if err != nil {
		return nil, nil, err
	}
	resolved, _ := resolvedAny.(map[string]any)
	agent, _ := resolved["agent"].(map[string]any)
	if agent == nil {
		return nil, nil, errors.New("unexpected resolve response")
	}

	agentID := normalizeSoulAgentID(stringFromMap(agent, "agent_id"))
	if agentID == "" {
		return nil, nil, errors.New("unexpected resolve response")
	}

	channelsAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(agentID)+"/channels", nil, "", nil)
	if err != nil {
		return nil, nil, err
	}
	channelsPayload, _ := channelsAny.(map[string]any)
	if channelsPayload == nil {
		channelsPayload = map[string]any{}
	}
	return agent, channelsPayload, nil
}

func summarizeResolvedAgent(agent map[string]any) map[string]any {
	return map[string]any{
		"agentId": normalizeSoulAgentID(stringFromMap(agent, "agent_id")),
		"domain":  stringFromMap(agent, "domain"),
		"localId": stringFromMap(agent, "local_id"),
		"status":  stringFromMap(agent, "status"),
	}
}

func findCommunicationNotification(ctx context.Context, bearerToken string, messageID string) (map[string]any, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, nil
	}

	if _, err := lesserapi.Default(); err != nil {
		return nil, err
	}

	for _, direction := range []string{"inbound", "outbound"} {
		items, _, err := readCommNotifications(ctx, bearerToken, direction, 200, "")
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			n, _ := item.(map[string]any)
			if n == nil {
				continue
			}
			if strings.TrimSpace(stringFromMap(n, "id")) == messageID || commMessageID(n) == messageID {
				return n, nil
			}
		}
	}

	return nil, nil
}

func verifySenderAgainstResolvedIdentity(agentID string, channel string, identifier string, channelsPayload map[string]any, from map[string]any) (bool, string) {
	expected := resolvedVerificationMarkers(agentID, channel, identifier, mapOrEmpty(channelsPayload, "channels"))
	actual := senderVerificationMarkers(from)

	if overlapKey(expected.agentIDs, actual.agentIDs) != "" {
		return true, "sender.agentId"
	}
	if overlapKey(expected.emails, actual.emails) != "" {
		return true, "sender.email"
	}
	if overlapKey(expected.phones, actual.phones) != "" {
		return true, "sender.phone"
	}
	if overlapKey(expected.ensNames, actual.ensNames) != "" {
		return true, "sender.ens"
	}
	return false, ""
}

func resolvedVerificationMarkers(agentID string, channel string, identifier string, channels map[string]any) verificationMarkers {
	out := newVerificationMarkers()
	out.addAgentID(agentID)

	email, _ := channels["email"].(map[string]any)
	out.addEmail(stringFromMap(email, "address"))
	phone, _ := channels["phone"].(map[string]any)
	out.addPhone(stringFromMap(phone, "number"))
	ens, _ := channels["ens"].(map[string]any)
	out.addENS(stringFromMap(ens, "name"))

	switch channel {
	case "email":
		out.addEmail(identifier)
	case "phone":
		out.addPhone(identifier)
	case "ens":
		out.addENS(identifier)
	}
	return out
}

func senderVerificationMarkers(from map[string]any) verificationMarkers {
	out := newVerificationMarkers()
	if from == nil {
		return out
	}

	for _, key := range []string{"soulAgentId", "soul_agent_id", "agentId", "agent_id"} {
		out.addAgentID(stringFromMap(from, key))
	}
	for _, key := range []string{"address", "email", "identifier", "name"} {
		value := stringFromMap(from, key)
		out.addEmail(value)
		out.addPhone(value)
		out.addENS(value)
	}
	for _, key := range []string{"number", "phone"} {
		out.addPhone(stringFromMap(from, key))
	}
	for _, key := range []string{"ens"} {
		out.addENS(stringFromMap(from, key))
	}

	return out
}

func newVerificationMarkers() verificationMarkers {
	return verificationMarkers{
		agentIDs: map[string]bool{},
		emails:   map[string]bool{},
		phones:   map[string]bool{},
		ensNames: map[string]bool{},
	}
}

func (m verificationMarkers) addAgentID(value string) {
	if normalized := normalizeSoulAgentID(value); normalized != "" {
		m.agentIDs[normalized] = true
	}
}

func (m verificationMarkers) addEmail(value string) {
	if normalized := normalizeVerificationEmail(value); normalized != "" {
		m.emails[normalized] = true
	}
}

func (m verificationMarkers) addPhone(value string) {
	if normalized := normalizeVerificationPhone(value); normalized != "" {
		m.phones[normalized] = true
	}
}

func (m verificationMarkers) addENS(value string) {
	if normalized := normalizeVerificationENS(value); normalized != "" {
		m.ensNames[normalized] = true
	}
}

func normalizeVerificationIdentifier(channel string, value string) string {
	switch channel {
	case "email":
		return normalizeVerificationEmail(value)
	case "phone":
		return normalizeVerificationPhone(value)
	case "ens":
		return normalizeVerificationENS(value)
	default:
		return strings.TrimSpace(value)
	}
}

func normalizeVerificationEmail(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Count(value, "@") != 1 {
		return ""
	}
	return value
}

func normalizeVerificationPhone(value string) string {
	value = normalizeCommPhoneE164(value)
	if value == "" || !strings.HasPrefix(value, "+") {
		return ""
	}
	return value
}

func normalizeVerificationENS(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if !strings.HasSuffix(value, ".eth") {
		return ""
	}
	return value
}

func overlapKey(a map[string]bool, b map[string]bool) string {
	for key := range a {
		if b[key] {
			return key
		}
	}
	return ""
}

func mapOrEmpty(m map[string]any, key string) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	if child, ok := m[key].(map[string]any); ok && child != nil {
		return child
	}
	return map[string]any{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func identityVerifyNotificationResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return toolErrorResult("upstream_error", "error", 500, nil)
	}

	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		code := identityErrorCodeForStatus(apiErr.Status)
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)
		details := map[string]any{}
		if parsed != nil {
			details["apiError"] = parsed
		}
		return toolErrorResult(code, message, apiErr.Status, details)
	}

	lowerErr := strings.ToLower(err.Error())
	if strings.Contains(lowerErr, "lesser_api_base_url") || strings.Contains(lowerErr, "mcp_endpoint") {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	return toolErrorResult("upstream_error", err.Error(), 0, nil)
}
