package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func handleEmailSend(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		To      string   `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
		CC      []string `json:"cc,omitempty"`
		BCC     []string `json:"bcc,omitempty"`
		ReplyTo string   `json:"replyTo,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}
	in.To = strings.TrimSpace(in.To)
	in.Subject = strings.TrimSpace(in.Subject)
	in.Body = strings.TrimSpace(in.Body)
	in.ReplyTo = strings.TrimSpace(in.ReplyTo)
	if in.To == "" || in.Subject == "" || in.Body == "" {
		return toolErrorResult("invalid_request", "to, subject, and body are required", 400, nil)
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return toolErrorResult("unauthorized", err.Error(), 401, nil)
	}

	identity, err := whoamiChannelsPayload(ctx, token)
	if err != nil {
		return identityToolResultFromError(err)
	}
	agentID, _ := identity["agentId"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return toolErrorResult("upstream_error", "unable to resolve agentId", 502, nil)
	}

	client, err := soulapi.Default()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	advisory := commBoundaryAdvisoryForEmail(ctx, client, agentID)

	body := map[string]any{
		"channel": "email",
		"agentId": agentID,
		"to":      in.To,
		"subject": in.Subject,
		"body":    in.Body,
		"cc":      normalizeStringSlice(in.CC),
		"bcc":     normalizeStringSlice(in.BCC),
		"replyTo": in.ReplyTo,
	}
	if body["cc"] == nil {
		delete(body, "cc")
	}
	if body["bcc"] == nil {
		delete(body, "bcc")
	}
	if strings.TrimSpace(in.ReplyTo) == "" {
		delete(body, "replyTo")
	}

	out, err := client.DoJSON(ctx, "POST", "/api/v1/soul/comm/send", nil, token, body)
	if err != nil {
		return commToolResultFromError(err)
	}

	normalized := normalizeCommSendResult(out, advisory)
	_ = maybeHydrateCommStatus(ctx, client, token, normalized)
	return toolJSONResult(normalized)
}

func handleEmailReply(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		MessageID string `json:"messageId"`
		Body      string `json:"body"`
		ReplyAll  bool   `json:"replyAll,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}
	in.MessageID = strings.TrimSpace(in.MessageID)
	in.Body = strings.TrimSpace(in.Body)
	if in.MessageID == "" || in.Body == "" {
		return toolErrorResult("invalid_request", "messageId and body are required", 400, nil)
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return toolErrorResult("unauthorized", err.Error(), 401, nil)
	}

	identity, err := whoamiChannelsPayload(ctx, token)
	if err != nil {
		return identityToolResultFromError(err)
	}
	agentID, _ := identity["agentId"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return toolErrorResult("upstream_error", "unable to resolve agentId", 502, nil)
	}

	client, err := soulapi.Default()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), 500, nil)
	}

	advisory := commBoundaryAdvisoryForEmail(ctx, client, agentID)

	body := map[string]any{
		"channel":   "email",
		"agentId":   agentID,
		"body":      in.Body,
		"inReplyTo": in.MessageID,
		"replyAll":  in.ReplyAll,
	}

	out, err := client.DoJSON(ctx, "POST", "/api/v1/soul/comm/send", nil, token, body)
	if err != nil {
		return commToolResultFromError(err)
	}

	normalized := normalizeCommSendResult(out, advisory)
	_ = maybeHydrateCommStatus(ctx, client, token, normalized)
	return toolJSONResult(normalized)
}

func normalizeCommSendResult(raw any, advisory map[string]any) map[string]any {
	out := map[string]any{
		"messageId": "",
		"status":    "",
		"result":    raw,
	}

	if m, ok := raw.(map[string]any); ok {
		if v, _ := m["messageId"].(string); strings.TrimSpace(v) != "" {
			out["messageId"] = strings.TrimSpace(v)
		} else if v, _ := m["message_id"].(string); strings.TrimSpace(v) != "" {
			out["messageId"] = strings.TrimSpace(v)
		} else if v, _ := m["id"].(string); strings.TrimSpace(v) != "" {
			out["messageId"] = strings.TrimSpace(v)
		}

		if v, _ := m["status"].(string); strings.TrimSpace(v) != "" {
			out["status"] = strings.TrimSpace(v)
		}
	}

	if len(advisory) > 0 {
		out["advisory"] = advisory
	}

	return out
}

func maybeHydrateCommStatus(ctx context.Context, client *soulapi.Client, bearerToken string, payload map[string]any) error {
	if client == nil || payload == nil {
		return nil
	}
	messageID, _ := payload["messageId"].(string)
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	status, _ := payload["status"].(string)
	if strings.TrimSpace(status) != "" {
		return nil
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/soul/comm/status/"+url.PathEscape(messageID), nil, bearerToken, nil)
	if err != nil {
		return err
	}
	m, ok := out.(map[string]any)
	if !ok {
		return nil
	}
	if v, _ := m["status"].(string); strings.TrimSpace(v) != "" {
		payload["status"] = strings.TrimSpace(v)
	}
	payload["delivery"] = m
	return nil
}

func commBoundaryAdvisoryForEmail(ctx context.Context, client *soulapi.Client, agentID string) map[string]any {
	if client == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}

	regAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(strings.TrimSpace(agentID))+"/registration", nil, "", nil)
	if err != nil {
		return nil
	}
	reg, _ := regAny.(map[string]any)
	boundaries, _ := reg["boundaries"].([]any)
	if len(boundaries) == 0 {
		return nil
	}

	relevant := make([]any, 0, len(boundaries))
	for _, b := range boundaries {
		m, _ := b.(map[string]any)
		category, _ := m["category"].(string)
		category = strings.ToLower(strings.TrimSpace(category))
		if category != "communication_policy" {
			continue
		}
		channel, _ := m["channel"].(string)
		channel = strings.ToLower(strings.TrimSpace(channel))
		if channel != "" && channel != "email" {
			continue
		}
		relevant = append(relevant, m)
	}

	if len(relevant) == 0 {
		return nil
	}

	return map[string]any{
		"communicationPolicyBoundaries": relevant,
	}
}

func normalizeStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
