package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

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
						"If the recipient is a soul-holding agent, prefer to look up their preferences first using identity_lookup.",
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
	if channel == "sms" {
		readTool = "sms_read"
		replyTool = "sms_send"
	}
	if channel == "voice" {
		readTool = "voicemail_read"
		replyTool = "phone_call"
	}

	user := strings.Join([]string{
		fmt.Sprintf("Handle an inbound %s message with messageId=%s.", channel, messageID),
		fmt.Sprintf("1) Use %s to fetch the message (search for messageId if needed).", readTool),
		"2) Assess urgency, safety, and whether it conflicts with any boundaries (especially communication_policy).",
		"3) If replying, write a respectful response and then call the appropriate outbound tool:",
		fmt.Sprintf("   - %s (preferred for this channel).", replyTool),
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
						"When the sender is identifiable (email/ENS), consider using identity_lookup to fetch their contactPreferences and choose the best response channel/timing.",
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
		"Determine the best way to contact the target agent while respecting their preferences.",
		"1) Call identity_lookup with the provided query to resolve their channels and contactPreferences.",
		"2) Recommend the best channel (email/sms/voice/activitypub/mcp) and timing based on availability schedule, languages, and first-contact settings.",
		"3) If preferences suggest constraints (e.g., requireSoul/requireReputation), explain what is missing and what to do next.",
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
						"Use identity_lookup for target preferences and identity_whoami for your own boundaries.",
						"If a preferred channel is unavailable, choose a fallback and explain tradeoffs.",
					}, "\n"),
				},
			},
			{Role: "user", Content: mcpruntime.ContentBlock{Type: "text", Text: user}},
		},
	}, nil
}
