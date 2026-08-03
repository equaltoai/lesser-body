package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const identityLookupPromptQueryForms = "ENS name, full agentId, current-instance local ID, explicit remote ActivityPub handle such as @steward@remote.example, or canonical actor URL such as https://remote.example/users/steward; private email/phone reachability lookup currently fails closed until lesser-host exposes a body-facing resolver"

func promptComposeEmail(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		To      string `json:"to"`
		Subject string `json:"subject,omitempty"`
		Context string `json:"context,omitempty"`
		Tone    string `json:"tone,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	to := strings.TrimSpace(in.To)
	if to == "" {
		return nil, invalidParams("missing to")
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = "(draft)"
	}
	tone := strings.TrimSpace(in.Tone)
	if tone == "" {
		tone = "neutral"
	}
	contextText := strings.TrimSpace(in.Context)

	user := fmt.Sprintf("Compose an email to %s. Subject: %s. Tone: %s.", to, subject, tone)
	if contextText != "" {
		user += " Context: " + contextText
	}

	return &mcpruntime.PromptResult{
		Description: "Compose an email, then call email_send with the final subject/body.",
		Messages: []mcpruntime.PromptMessage{
			{
				Role: "system",
				Content: mcpruntime.ContentBlock{
					Type: "text",
					Text: strings.Join([]string{
						"You are operating an agent's communication channels via lesser-body MCP.",
						"Before composing/sending, fetch your current preferences and boundaries:",
						"- Call identity_whoami and/or read agent://channels/preferences.",
						"If the recipient is a soul-holding agent, use identity_lookup with a " + identityLookupPromptQueryForms + " to resolve their public identity; do not assume private channel preferences unless they are provided by the user or message context.",
						"Respect communication_policy boundaries (e.g., no unsolicited outbound) and first-contact expectations (e.g., disclose you are an AI agent).",
						"Keep the email concise and avoid secrets.",
					}, "\n"),
				},
			},
			{Role: "user", Content: mcpruntime.ContentBlock{Type: "text", Text: user}},
		},
	}, nil
}

func promptHandleInbound(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		Channel   string `json:"channel"`
		MessageID string `json:"messageId"`
		Intent    string `json:"intent,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	channel := strings.ToLower(strings.TrimSpace(in.Channel))
	if channel == "" {
		return nil, invalidParams("missing channel")
	}
	if channel != "email" && channel != "sms" && channel != "voice" {
		return nil, invalidParams("invalid channel (expected email, sms, or voice)")
	}
	messageID := strings.TrimSpace(in.MessageID)
	if messageID == "" {
		return nil, invalidParams("missing messageId")
	}

	intent := strings.TrimSpace(in.Intent)
	if intent == "" {
		intent = "respond appropriately"
	}

	readTool := "email_read"
	replyTool := "email_reply"
	replyStep := strings.Join([]string{
		"3) If replying, write a respectful response and then call the appropriate outbound tool:",
		fmt.Sprintf("   - %s (preferred for this channel).", replyTool),
		fmt.Sprintf("   - Pass messageId=%s so the host can treat it as an in-thread reply.", messageID),
	}, "\n")
	if channel == "sms" {
		readTool = "sms_read"
		replyTool = "sms_send"
		replyStep = strings.Join([]string{
			"3) If replying, write a respectful response and then call the appropriate outbound tool:",
			fmt.Sprintf("   - %s (preferred for this channel).", replyTool),
			fmt.Sprintf("   - Pass messageId=%s so the host can treat it as an in-thread reply.", messageID),
		}, "\n")
	}
	if channel == "voice" {
		readTool = "voicemail_read"
		replyStep = strings.Join([]string{
			"3) If replying, do not place an outbound voice call; that capability is disabled.",
			"   - Use identity_lookup when possible to resolve the sender's public soul identity.",
			"   - Prefer email_send or sms_send only when the user/message context provides a safe recipient identifier and preference signal.",
		}, "\n")
	}

	user := strings.Join([]string{
		fmt.Sprintf("Handle an inbound %s message with messageId=%s.", channel, messageID),
		fmt.Sprintf("1) Use %s to fetch the message (search for messageId if needed).", readTool),
		"2) Assess urgency, safety, and whether it conflicts with any boundaries (especially communication_policy).",
		replyStep,
		"4) If you should not reply, explain why and consider archiving via email_delete when applicable.",
	}, "\n")

	return &mcpruntime.PromptResult{
		Description: "Process an inbound communication and respond (or refuse) while respecting boundaries and preferences.",
		Messages: []mcpruntime.PromptMessage{
			{
				Role: "system",
				Content: mcpruntime.ContentBlock{
					Type: "text",
					Text: strings.Join([]string{
						"Use the agent's declared boundaries and contact preferences as constraints.",
						"Fetch your own channels/preferences via identity_whoami or agent://channels/preferences if needed.",
						"When the sender is identifiable, consider using identity_lookup with their " + identityLookupPromptQueryForms + " to resolve public identity. Use only explicit preference signals from the message/context for response channel/timing.",
						"Never invent message contents; read them via tools/resources.",
					}, "\n"),
				},
			},
			{Role: "user", Content: mcpruntime.ContentBlock{Type: "text", Text: user + "\nIntent: " + intent}},
		},
	}, nil
}

func promptRespectPreferences(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, invalidParams("missing query")
	}

	user := strings.Join([]string{
		"Determine the best way to contact the target agent while respecting known preferences.",
		"1) Call identity_lookup with a " + identityLookupPromptQueryForms + " to resolve their public identity summary.",
		"2) Recommend the best channel (email/sms/activitypub/mcp) and timing using only explicit preference signals from the user, message context, or prior consent.",
		"   - Treat voice as receive-only because outbound phone_call is intentionally disabled.",
		"3) If preferences are unknown, say so and choose the least intrusive channel with clear AI-agent disclosure.",
		"Query: " + q,
	}, "\n")

	return &mcpruntime.PromptResult{
		Description: "Suggest the best communication approach using the target's declared contact preferences.",
		Messages: []mcpruntime.PromptMessage{
			{
				Role: "system",
				Content: mcpruntime.ContentBlock{
					Type: "text",
					Text: strings.Join([]string{
						"Prefer boundary-respecting, preference-respecting communication.",
						"Use identity_lookup for target identity resolution with a supported query form, and identity_whoami for your own boundaries.",
						"If a preferred channel is unavailable, choose a fallback and explain tradeoffs.",
					}, "\n"),
				},
			},
			{Role: "user", Content: mcpruntime.ContentBlock{Type: "text", Text: user}},
		},
	}, nil
}
