package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

func resourceEmailInbox(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	out, err := resourceMailboxList(ctx, "agent://email/inbox", commMailboxListOptions{ChannelType: "email", Direction: "inbound", Limit: 20})
	if err != nil {
		return nil, err
	}
	out["folder"] = "inbox"
	return resourceJSON("agent://email/inbox", out)
}

func resourceEmailSent(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	out, err := resourceMailboxList(ctx, "agent://email/sent", commMailboxListOptions{ChannelType: "email", Direction: "outbound", Limit: 20})
	if err != nil {
		return nil, err
	}
	out["folder"] = "sent"
	return resourceJSON("agent://email/sent", out)
}

func resourceSmsMessages(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	out, err := resourceMailboxList(ctx, "agent://sms/messages", commMailboxListOptions{ChannelType: "sms", Direction: "inbound", Limit: 20})
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://sms/messages", out)
}

func resourceVoicemail(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	out, err := resourceMailboxList(ctx, "agent://voicemail", commMailboxListOptions{ChannelType: "voice", Direction: "inbound", Limit: 20})
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://voicemail", out)
}

func resourceMailboxList(ctx context.Context, uri string, opts commMailboxListOptions) (map[string]any, error) {
	deps, err := loadCommMailboxDependencies(ctx, boundOperationForMailboxResource(opts))
	if err != nil {
		contents, resErr := commMailboxResourceContentsFromError(uri, err)
		if resErr != nil {
			return nil, resErr
		}
		return resourceContentsPayload(contents), nil
	}
	out, err := listHostMailboxMessages(ctx, deps, opts)
	if err != nil {
		contents, resErr := commMailboxResourceContentsFromError(uri, err)
		if resErr != nil {
			return nil, resErr
		}
		return resourceContentsPayload(contents), nil
	}
	return out, nil
}

func boundOperationForMailboxResource(opts commMailboxListOptions) boundOperation {
	switch strings.ToLower(strings.TrimSpace(opts.ChannelType)) {
	case "email":
		return boundOperationEmailRead
	case "sms":
		return boundOperationSMSRead
	case "voice", "voicemail":
		return boundOperationVoiceRead
	default:
		return boundOperationChannelsRead
	}
}

func resourceContentsPayload(contents []mcpruntime.ResourceContent) map[string]any {
	if len(contents) == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(contents[0].Text), &payload)
	if payload == nil {
		return map[string]any{}
	}
	return payload
}
