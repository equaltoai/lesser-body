package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func handleEmailRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Folder     string `json:"folder,omitempty"`
		UnreadOnly bool   `json:"unreadOnly,omitempty"`
		Limit      int    `json:"limit,omitempty"`
		Since      string `json:"since,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	in.Folder = strings.ToLower(strings.TrimSpace(in.Folder))
	if in.Folder == "" {
		in.Folder = "inbox"
	}
	if in.Folder != "inbox" && in.Folder != "sent" {
		return nil, invalidParams("invalid folder (expected inbox or sent)")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}

	direction := "inbound"
	if in.Folder == "sent" {
		direction = "outbound"
	}

	items, nextSince, err := readCommNotifications(ctx, token, direction, in.Limit, in.Since)
	if err != nil {
		return nil, err
	}

	messages := commMessagesFromNotifications(items, "email")
	return toolJSONResult(map[string]any{
		"folder":     in.Folder,
		"unreadOnly": in.UnreadOnly,
		"since":      strings.TrimSpace(in.Since),
		"nextSince":  nextSince,
		"count":      len(messages),
		"messages":   messages,
		"notes":      unreadNotes(),
	})
}

func handleEmailSearch(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Query  string `json:"query"`
		Folder string `json:"folder,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return nil, invalidParams("missing query")
	}
	in.Folder = strings.ToLower(strings.TrimSpace(in.Folder))
	if in.Folder == "" {
		in.Folder = "inbox"
	}
	if in.Folder != "inbox" && in.Folder != "sent" {
		return nil, invalidParams("invalid folder (expected inbox or sent)")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}

	direction := "inbound"
	if in.Folder == "sent" {
		direction = "outbound"
	}

	items, _, err := readCommNotifications(ctx, token, direction, 200, "")
	if err != nil {
		return nil, err
	}
	messages := commMessagesFromNotifications(items, "email")

	filtered := make([]any, 0, len(messages))
	qLower := strings.ToLower(in.Query)
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg == nil {
			continue
		}
		subject, _ := msg["subject"].(string)
		body, _ := msg["body"].(string)
		from, _ := msg["from"].(map[string]any)
		fromAddr, _ := from["address"].(string)
		if strings.Contains(strings.ToLower(subject), qLower) ||
			strings.Contains(strings.ToLower(body), qLower) ||
			strings.Contains(strings.ToLower(fromAddr), qLower) {
			filtered = append(filtered, msg)
		}
		if in.Limit > 0 && len(filtered) >= in.Limit {
			break
		}
	}

	return toolJSONResult(map[string]any{
		"query":     in.Query,
		"folder":    in.Folder,
		"count":     len(filtered),
		"messages":  filtered,
		"notes":     unreadNotes(),
		"strategy":  "best-effort search over recent notification-backed messages",
		"maxWindow": 200,
	})
}

func handleEmailDelete(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		MessageID string `json:"messageId"`
		Action    string `json:"action"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.MessageID = strings.TrimSpace(in.MessageID)
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	if in.MessageID == "" {
		return nil, invalidParams("missing messageId")
	}
	if in.Action != "delete" && in.Action != "archive" {
		return nil, invalidParams("invalid action (expected delete or archive)")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	// Strategy: map delete/archive to dismissing the underlying notification.
	// messageId may refer to either:
	// - Lesser notification id (fast path), or
	// - comm-worker messageId embedded in the notification payload.
	notificationID, err := resolveNotificationIDForMessage(ctx, token, in.MessageID)
	if err != nil {
		return nil, err
	}
	if notificationID == "" {
		return toolJSONResult(map[string]any{
			"messageId": in.MessageID,
			"action":    in.Action,
			"dismissed": false,
			"notes":     "no matching notification found (message may already be dismissed/archived)",
		})
	}

	_, err = client.DoJSON(ctx, "POST", "/api/v1/notifications/"+url.PathEscape(notificationID)+"/dismiss", nil, token, map[string]any{})
	if err != nil {
		return nil, err
	}

	return toolJSONResult(map[string]any{
		"messageId":       in.MessageID,
		"notificationId":  notificationID,
		"action":          in.Action,
		"dismissed":       true,
		"dismissBehavior": "mapped to /api/v1/notifications/{id}/dismiss",
	})
}

func handleSmsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		UnreadOnly bool   `json:"unreadOnly,omitempty"`
		Limit      int    `json:"limit,omitempty"`
		Since      string `json:"since,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}

	items, nextSince, err := readCommNotifications(ctx, token, "inbound", in.Limit, in.Since)
	if err != nil {
		return nil, err
	}

	messages := commMessagesFromNotifications(items, "sms")
	return toolJSONResult(map[string]any{
		"unreadOnly": in.UnreadOnly,
		"since":      strings.TrimSpace(in.Since),
		"nextSince":  nextSince,
		"count":      len(messages),
		"messages":   messages,
		"notes":      unreadNotes(),
	})
}

func handleVoicemailRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		UnreadOnly bool `json:"unreadOnly,omitempty"`
		Limit      int  `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}

	items, _, err := readCommNotifications(ctx, token, "inbound", in.Limit, "")
	if err != nil {
		return nil, err
	}

	messages := commMessagesFromNotifications(items, "voicemail")
	return toolJSONResult(map[string]any{
		"unreadOnly": in.UnreadOnly,
		"count":      len(messages),
		"messages":   messages,
		"notes":      unreadNotes(),
	})
}

func readCommNotifications(ctx context.Context, bearerToken string, direction string, limit int, since string) ([]any, string, error) {
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction == "" {
		direction = "inbound"
	}
	if direction != "inbound" && direction != "outbound" {
		return nil, "", invalidParams("invalid direction")
	}

	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	client, err := lesserapi.Default()
	if err != nil {
		return nil, "", err
	}

	types := []string{"communication:" + direction}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if strings.TrimSpace(since) != "" {
		query.Set("max_id", strings.TrimSpace(since))
	}
	for _, typ := range types {
		query.Add("types[]", typ)
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications", query, bearerToken, nil)
	if err != nil {
		return nil, "", err
	}

	list, ok := out.([]any)
	if !ok {
		return nil, "", fmt.Errorf("unexpected notifications response")
	}

	nextSince := ""
	if len(list) > 0 {
		if m, ok := list[len(list)-1].(map[string]any); ok {
			nextSince = strings.TrimSpace(stringFromMap(m, "id"))
		}
	}
	return list, nextSince, nil
}

func commMessagesFromNotifications(items []any, wantChannel string) []any {
	wantChannel = strings.ToLower(strings.TrimSpace(wantChannel))
	out := make([]any, 0, len(items))
	for _, item := range items {
		n, ok := item.(map[string]any)
		if !ok || n == nil {
			continue
		}
		channel := notificationChannel(n)
		if channel == "" {
			continue
		}
		if wantChannel == "voicemail" {
			if channel != "voice" && channel != "voicemail" {
				continue
			}
		} else if channel != wantChannel {
			continue
		}

		msgID := commMessageID(n)
		if msgID == "" {
			msgID = strings.TrimSpace(stringFromMap(n, "id"))
		}

		out = append(out, map[string]any{
			"messageId":      msgID,
			"notificationId": strings.TrimSpace(stringFromMap(n, "id")),
			"channel":        channel,
			"from":           commFrom(n),
			"to":             commTo(n),
			"subject":        commSubject(n),
			"body":           commBody(n),
			"receivedAt":     commReceivedAt(n),
			"raw":            n,
		})
	}
	return out
}

func notificationChannel(n map[string]any) string {
	for _, key := range []string{"channel"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			if v, _ := m["channel"].(string); strings.TrimSpace(v) != "" {
				return strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	return ""
}

func commMessageID(n map[string]any) string {
	for _, key := range []string{"messageId", "message_id"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			for _, key := range []string{"messageId", "message_id"} {
				if v, _ := m[key].(string); strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	return ""
}

func commFrom(n map[string]any) map[string]any {
	for _, container := range []string{"from"} {
		if m, ok := n[container].(map[string]any); ok {
			return m
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			if from, ok := m["from"].(map[string]any); ok {
				return from
			}
		}
	}
	return map[string]any{}
}

func commTo(n map[string]any) any {
	for _, key := range []string{"to"} {
		if v, ok := n[key]; ok && v != nil {
			return v
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			if v, ok := m["to"]; ok && v != nil {
				return v
			}
		}
	}
	return nil
}

func commSubject(n map[string]any) string {
	for _, key := range []string{"subject"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			if v, _ := m["subject"].(string); strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func commBody(n map[string]any) string {
	for _, key := range []string{"body"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			if v, _ := m["body"].(string); strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func commReceivedAt(n map[string]any) string {
	for _, key := range []string{"receivedAt", "received_at"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, container := range []string{"data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			for _, key := range []string{"receivedAt", "received_at"} {
				if v, _ := m[key].(string); strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}

	return strings.TrimSpace(stringFromMap(n, "created_at"))
}

func resolveNotificationIDForMessage(ctx context.Context, bearerToken string, messageID string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", nil
	}

	client, err := lesserapi.Default()
	if err != nil {
		return "", err
	}

	query := url.Values{}
	query.Set("limit", "200")
	query.Add("types[]", "communication:inbound")
	query.Add("types[]", "communication:outbound")

	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications", query, bearerToken, nil)
	if err != nil {
		return "", err
	}

	list, ok := out.([]any)
	if !ok {
		return "", fmt.Errorf("unexpected notifications response")
	}

	for _, item := range list {
		n, _ := item.(map[string]any)
		if n == nil {
			continue
		}
		id := strings.TrimSpace(stringFromMap(n, "id"))
		if id == messageID {
			return id, nil
		}
		if commMessageID(n) == messageID {
			return id, nil
		}
	}

	return "", nil
}

func unreadNotes() map[string]any {
	return map[string]any{
		"unreadOnlyMapping": "Underlying Lesser notifications do not expose a separate read state. \"Unread\" maps to notifications not dismissed via /api/v1/notifications/{id}/dismiss.",
		"dismissBehavior":   "Dismissed notifications are typically removed from list results; historical access is best-effort.",
	}
}

func validateArgsObject(args json.RawMessage) error {
	raw := bytes.TrimSpace(args)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return invalidParams("invalid args: " + err.Error())
	}
	if v != nil {
		if _, ok := v.(map[string]any); !ok {
			return invalidParams("arguments must be an object")
		}
	}
	return nil
}
