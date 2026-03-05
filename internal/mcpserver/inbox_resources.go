package mcpserver

import (
	"context"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func resourceEmailInbox(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	items, nextSince, err := readCommNotifications(ctx, token, "inbound", 20, "")
	if err != nil {
		return nil, err
	}
	messages := commMessagesFromNotifications(items, "email")
	return resourceJSON("agent://email/inbox", map[string]any{
		"count":     len(messages),
		"messages":  messages,
		"nextSince": nextSince,
		"notes":     unreadNotes(),
	})
}

func resourceEmailSent(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	items, nextSince, err := readCommNotifications(ctx, token, "outbound", 20, "")
	if err != nil {
		return nil, err
	}
	messages := commMessagesFromNotifications(items, "email")
	return resourceJSON("agent://email/sent", map[string]any{
		"count":     len(messages),
		"messages":  messages,
		"nextSince": nextSince,
		"strategy":  "notification-backed (type=communication:outbound) when available",
		"notes": map[string]any{
			"sentStrategy": "This resource reads instance notifications with type communication:outbound. If the instance does not emit outbound communication notifications, the list may be empty.",
		},
	})
}

func resourceSmsMessages(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	items, nextSince, err := readCommNotifications(ctx, token, "inbound", 20, "")
	if err != nil {
		return nil, err
	}
	messages := commMessagesFromNotifications(items, "sms")
	return resourceJSON("agent://sms/messages", map[string]any{
		"count":     len(messages),
		"messages":  messages,
		"nextSince": nextSince,
		"notes":     unreadNotes(),
	})
}

func resourceVoicemail(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	items, nextSince, err := readCommNotifications(ctx, token, "inbound", 20, "")
	if err != nil {
		return nil, err
	}
	messages := commMessagesFromNotifications(items, "voicemail")
	return resourceJSON("agent://voicemail", map[string]any{
		"count":     len(messages),
		"messages":  messages,
		"nextSince": nextSince,
		"notes":     unreadNotes(),
	})
}
