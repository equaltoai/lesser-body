package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func handleEmailRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}
	view := readParams.View
	if view == "" {
		view = readViewStandard
	}
	if err := validateMailboxListReadView(view); err != nil {
		return toolErrorResult("invalid_request", err.Error(), 400, map[string]any{"view": view})
	}

	var in struct {
		Folder          string `json:"folder,omitempty"`
		UnreadOnly      bool   `json:"unreadOnly,omitempty"`
		IncludeRaw      bool   `json:"include_raw,omitempty"`
		IncludeArchived bool   `json:"includeArchived,omitempty"`
		IncludeDeleted  bool   `json:"includeDeleted,omitempty"`
		Limit           int    `json:"limit,omitempty"`
		Cursor          string `json:"cursor,omitempty"`
		Since           string `json:"since,omitempty"`
		ThreadID        string `json:"threadId,omitempty"`
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
	read := optionalBoolArg(args, "read")
	if err := validateMailboxReadFilters(in.UnreadOnly, read); err != nil {
		return nil, err
	}
	archived := optionalBoolArg(args, "archived")
	deleted := optionalBoolArg(args, "deleted")

	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}

	direction := "inbound"
	if in.Folder == "sent" {
		direction = "outbound"
	}
	includeRaw := (in.IncludeRaw || view == readViewFull) && view != readViewCompact

	out, err := listHostMailboxMessages(ctx, deps, commMailboxListOptions{
		ChannelType:     "email",
		Direction:       direction,
		UnreadOnly:      in.UnreadOnly,
		IncludeRaw:      includeRaw,
		IncludeArchived: in.IncludeArchived,
		IncludeDeleted:  in.IncludeDeleted,
		Archived:        archived,
		Read:            read,
		Deleted:         deleted,
		Limit:           in.Limit,
		Cursor:          mailboxCursor(in.Cursor, in.Since),
		ThreadID:        in.ThreadID,
	})
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["folder"] = in.Folder
	out["unreadOnly"] = in.UnreadOnly
	out["includeArchived"] = in.IncludeArchived
	out["includeDeleted"] = in.IncludeDeleted
	if read != nil {
		out["read"] = *read
	}
	if archived != nil {
		out["archived"] = *archived
	}
	if deleted != nil {
		out["deleted"] = *deleted
	}
	out["cursor"] = strings.TrimSpace(in.Cursor)
	out["since"] = strings.TrimSpace(in.Since)
	if view == readViewCompact {
		return compactMailboxListToolResult("email_read", mailboxCompactListResult(out))
	}
	if view == readViewFull {
		out["view"] = readViewFull
	}
	return toolJSONResult(out, out)
}

func handleEmailGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		MessageID  string `json:"messageId"`
		IncludeRaw bool   `json:"include_raw,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := getHostMailboxMessage(ctx, deps, in.MessageID, in.IncludeRaw)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["source"] = "lesser-host-mailbox"
	return toolJSONResult(out, out)
}

func handleEmailGetContent(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := getHostMailboxContent(ctx, deps, in.MessageID)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	return toolJSONResult(out, out)
}

func handleEmailSearch(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Query           string `json:"query"`
		Folder          string `json:"folder,omitempty"`
		UnreadOnly      bool   `json:"unreadOnly,omitempty"`
		IncludeRaw      bool   `json:"include_raw,omitempty"`
		IncludeArchived bool   `json:"includeArchived,omitempty"`
		IncludeDeleted  bool   `json:"includeDeleted,omitempty"`
		Limit           int    `json:"limit,omitempty"`
		Cursor          string `json:"cursor,omitempty"`
		ThreadID        string `json:"threadId,omitempty"`
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
	read := optionalBoolArg(args, "read")
	if err := validateMailboxReadFilters(in.UnreadOnly, read); err != nil {
		return nil, err
	}
	archived := optionalBoolArg(args, "archived")
	deleted := optionalBoolArg(args, "deleted")

	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}

	direction := "inbound"
	if in.Folder == "sent" {
		direction = "outbound"
	}
	out, err := listHostMailboxMessages(ctx, deps, commMailboxListOptions{
		ChannelType:     "email",
		Direction:       direction,
		UnreadOnly:      in.UnreadOnly,
		IncludeRaw:      in.IncludeRaw,
		IncludeArchived: in.IncludeArchived,
		IncludeDeleted:  in.IncludeDeleted,
		Archived:        archived,
		Read:            read,
		Deleted:         deleted,
		Limit:           in.Limit,
		Cursor:          in.Cursor,
		ThreadID:        in.ThreadID,
		Query:           in.Query,
	})
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["query"] = in.Query
	out["folder"] = in.Folder
	out["unreadOnly"] = in.UnreadOnly
	out["includeArchived"] = in.IncludeArchived
	out["includeDeleted"] = in.IncludeDeleted
	if read != nil {
		out["read"] = *read
	}
	if archived != nil {
		out["archived"] = *archived
	}
	if deleted != nil {
		out["deleted"] = *deleted
	}
	out["strategy"] = "host bounded metadata/preview query"
	return toolJSONResult(out, out)
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

	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailManage)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := mutateHostMailboxMessage(ctx, deps, in.MessageID, in.Action)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["source"] = "lesser-host-mailbox"
	return toolJSONResult(out, out)
}

func handleEmailMarkRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return handleEmailReadState(ctx, args, "read")
}

func handleEmailMarkUnread(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return handleEmailReadState(ctx, args, "unread")
}

func handleEmailReadState(ctx context.Context, args json.RawMessage, action string) (*mcpruntime.ToolResult, error) {
	var in struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	deps, err := loadCommMailboxDependencies(ctx, boundOperationEmailManage)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := mutateHostMailboxMessage(ctx, deps, in.MessageID, action)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["source"] = "lesser-host-mailbox"
	return toolJSONResult(out, out)
}

func handleSmsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		UnreadOnly      bool   `json:"unreadOnly,omitempty"`
		IncludeRaw      bool   `json:"include_raw,omitempty"`
		IncludeArchived bool   `json:"includeArchived,omitempty"`
		IncludeDeleted  bool   `json:"includeDeleted,omitempty"`
		Limit           int    `json:"limit,omitempty"`
		Cursor          string `json:"cursor,omitempty"`
		Since           string `json:"since,omitempty"`
		ThreadID        string `json:"threadId,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	read := optionalBoolArg(args, "read")
	if err := validateMailboxReadFilters(in.UnreadOnly, read); err != nil {
		return nil, err
	}
	archived := optionalBoolArg(args, "archived")
	deleted := optionalBoolArg(args, "deleted")

	deps, err := loadCommMailboxDependencies(ctx, boundOperationSMSRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := listHostMailboxMessages(ctx, deps, commMailboxListOptions{
		ChannelType:     "sms",
		Direction:       "inbound",
		UnreadOnly:      in.UnreadOnly,
		IncludeRaw:      in.IncludeRaw,
		IncludeArchived: in.IncludeArchived,
		IncludeDeleted:  in.IncludeDeleted,
		Archived:        archived,
		Read:            read,
		Deleted:         deleted,
		Limit:           in.Limit,
		Cursor:          mailboxCursor(in.Cursor, in.Since),
		ThreadID:        in.ThreadID,
	})
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["unreadOnly"] = in.UnreadOnly
	out["includeArchived"] = in.IncludeArchived
	out["includeDeleted"] = in.IncludeDeleted
	if read != nil {
		out["read"] = *read
	}
	if archived != nil {
		out["archived"] = *archived
	}
	if deleted != nil {
		out["deleted"] = *deleted
	}
	out["cursor"] = strings.TrimSpace(in.Cursor)
	out["since"] = strings.TrimSpace(in.Since)
	return toolJSONResult(out, out)
}

func handleVoicemailRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		UnreadOnly      bool   `json:"unreadOnly,omitempty"`
		IncludeRaw      bool   `json:"include_raw,omitempty"`
		IncludeArchived bool   `json:"includeArchived,omitempty"`
		IncludeDeleted  bool   `json:"includeDeleted,omitempty"`
		Limit           int    `json:"limit,omitempty"`
		Cursor          string `json:"cursor,omitempty"`
		ThreadID        string `json:"threadId,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	read := optionalBoolArg(args, "read")
	if err := validateMailboxReadFilters(in.UnreadOnly, read); err != nil {
		return nil, err
	}
	archived := optionalBoolArg(args, "archived")
	deleted := optionalBoolArg(args, "deleted")

	deps, err := loadCommMailboxDependencies(ctx, boundOperationVoiceRead)
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out, err := listHostMailboxMessages(ctx, deps, commMailboxListOptions{
		ChannelType:     "voice",
		Direction:       "inbound",
		UnreadOnly:      in.UnreadOnly,
		IncludeRaw:      in.IncludeRaw,
		IncludeArchived: in.IncludeArchived,
		IncludeDeleted:  in.IncludeDeleted,
		Archived:        archived,
		Read:            read,
		Deleted:         deleted,
		Limit:           in.Limit,
		Cursor:          in.Cursor,
		ThreadID:        in.ThreadID,
	})
	if err != nil {
		return commMailboxToolResultFromError(err)
	}
	out["unreadOnly"] = in.UnreadOnly
	out["includeArchived"] = in.IncludeArchived
	out["includeDeleted"] = in.IncludeDeleted
	if read != nil {
		out["read"] = *read
	}
	if archived != nil {
		out["archived"] = *archived
	}
	if deleted != nil {
		out["deleted"] = *deleted
	}
	return toolJSONResult(out, out)
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
	for _, container := range []string{"communication", "data", "payload"} {
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
