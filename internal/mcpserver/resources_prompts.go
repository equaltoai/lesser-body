package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/memory"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func registerResources(srv *Server) error {
	if srv == nil || srv.Resources() == nil {
		return fmt.Errorf("resource registry is nil")
	}
	r := srv.Resources()

	for _, res := range []struct {
		Def     mcpruntime.ResourceDef
		Handler mcpruntime.ResourceHandler
	}{
		{Def: resourceDef("agent://profile", "profile", "Agent profile"), Handler: resourceProfile},
		{Def: resourceDef("agent://timeline/home", "timeline_home", "Home timeline"), Handler: resourceTimeline("home")},
		{Def: resourceDef("agent://timeline/local", "timeline_local", "Local timeline"), Handler: resourceTimeline("local")},
		{Def: resourceDef("agent://followers", "followers", "Followers"), Handler: resourceFollowers},
		{Def: resourceDef("agent://following", "following", "Following"), Handler: resourceFollowing},
		{Def: resourceDef("agent://notifications", "notifications", "Notifications"), Handler: resourceNotifications},
		{Def: resourceDef("agent://channels", "channels", "Communication channels"), Handler: resourceChannels},
		{Def: resourceDef("agent://channels/preferences", "channels_preferences", "Channel preferences"), Handler: resourceChannelPreferences},
		{Def: resourceDef("agent://email/inbox", "email_inbox", "Email inbox"), Handler: resourceEmailInbox},
		{Def: resourceDef("agent://email/sent", "email_sent", "Sent email"), Handler: resourceEmailSent},
		{Def: resourceDef("agent://sms/messages", "sms_messages", "SMS messages"), Handler: resourceSmsMessages},
		{Def: resourceDef("agent://voicemail", "voicemail", "Voicemail"), Handler: resourceVoicemail},
		{Def: resourceDef("agent://memory/recent", "memory_recent", "Recent memory events"), Handler: resourceMemoryRecent},
		{Def: resourceDef("agent://capabilities", "capabilities", "Capabilities (best-effort)"), Handler: resourceCapabilities(srv)},
		{Def: resourceDef("agent://config", "config", "Instance configuration (non-sensitive)"), Handler: resourceConfig},
	} {
		if err := r.RegisterResource(res.Def, res.Handler); err != nil {
			return err
		}
	}

	return nil
}

func registerPrompts(srv *Server) error {
	if srv == nil || srv.Prompts() == nil {
		return fmt.Errorf("prompt registry is nil")
	}
	r := srv.Prompts()

	for _, p := range []struct {
		Def     mcpruntime.PromptDef
		Handler mcpruntime.PromptHandler
	}{
		{
			Def: mcpruntime.PromptDef{
				Name:        "compose_post",
				Title:       "Compose post",
				Description: "Compose a post in the agent's voice.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "topic", Description: "Topic to write about."},
					{Name: "tone", Description: "Desired tone (e.g. friendly, formal, playful)."},
					{Name: "max_length", Description: "Maximum character length."},
				},
			},
			Handler: promptComposePost,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "summarize_timeline",
				Title:       "Summarize timeline",
				Description: "Summarize recent timeline activity.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "timeline", Description: "home|local|federated", Required: true},
					{Name: "period", Description: "Human description (e.g. last hour, today)."},
				},
			},
			Handler: promptSummarizeTimeline,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "draft_reply",
				Title:       "Draft reply",
				Description: "Draft a reply to a specific post.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "post_id", Description: "Target post/status id.", Required: true},
					{Name: "tone", Description: "Desired tone."},
				},
			},
			Handler: promptDraftReply,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "reputation_report",
				Title:       "Reputation report",
				Description: "Generate a human-readable reputation summary (best-effort).",
			},
			Handler: promptReputationReport,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "memory_reflect",
				Title:       "Memory reflect",
				Description: "Reflect on recent memory events to identify patterns.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "period", Description: "Time window (e.g. last day, last week)."},
				},
			},
			Handler: promptMemoryReflect,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "compose_email",
				Title:       "Compose email",
				Description: "Compose an email while respecting boundaries and preferences.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "to", Description: "Recipient email address.", Required: true},
					{Name: "subject", Description: "Email subject."},
					{Name: "context", Description: "Relevant context to include."},
					{Name: "tone", Description: "Desired tone (e.g. friendly, formal, concise)."},
				},
			},
			Handler: promptComposeEmail,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "handle_inbound",
				Title:       "Handle inbound",
				Description: "Handle an inbound email/SMS/voicemail while respecting boundaries and preferences.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "channel", Description: "email|sms|voice", Required: true},
					{Name: "messageId", Description: "Inbound message identifier.", Required: true},
					{Name: "intent", Description: "What you are trying to accomplish (optional)."},
				},
			},
			Handler: promptHandleInbound,
		},
		{
			Def: mcpruntime.PromptDef{
				Name:        "respect_preferences",
				Title:       "Respect preferences",
				Description: "Choose how to contact a target agent based on their declared contact preferences.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "query", Description: "ENS name, agentId, or email address.", Required: true},
				},
			},
			Handler: promptRespectPreferences,
		},
	} {
		if err := r.RegisterPrompt(p.Def, p.Handler); err != nil {
			return err
		}
	}

	return nil
}

func resourceDef(uri string, name string, title string) mcpruntime.ResourceDef {
	return mcpruntime.ResourceDef{
		URI:      uri,
		Name:     name,
		Title:    title,
		MimeType: "application/json",
	}
}

func resourceJSON(uri string, payload any) ([]mcpruntime.ResourceContent, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal resource: %w", err)
	}
	return []mcpruntime.ResourceContent{{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(b),
	}}, nil
}

func resourceProfile(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}
	out, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://profile", out)
}

func resourceTimeline(kind string) mcpruntime.ResourceHandler {
	return func(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			return nil, invalidParams("missing timeline")
		}

		token, err := requireOAuthBearer(ctx)
		if err != nil {
			return nil, err
		}
		client, err := lesserapi.Default()
		if err != nil {
			return nil, err
		}

		query := url.Values{}
		query.Set("limit", strconv.Itoa(20))

		path := ""
		switch kind {
		case "home":
			path = "/api/v1/timelines/home"
		case "local":
			path = "/api/v1/timelines/public"
			query.Set("local", "true")
		case "federated":
			path = "/api/v1/timelines/public"
		default:
			return nil, invalidParams("invalid timeline")
		}

		out, err := client.DoJSON(ctx, "GET", path, query, token, nil)
		if err != nil {
			return nil, err
		}
		return resourceJSON("agent://timeline/"+kind, out)
	}
}

func resourceFollowers(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return nil, err
	}
	accountMap, ok := account.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected verify_credentials response")
	}
	id, _ := accountMap["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("verify_credentials missing id")
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(20))
	out, err := client.DoJSON(ctx, "GET", fmt.Sprintf("/api/v1/accounts/%s/followers", id), query, token, nil)
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://followers", out)
}

func resourceFollowing(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return nil, err
	}
	accountMap, ok := account.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected verify_credentials response")
	}
	id, _ := accountMap["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("verify_credentials missing id")
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(20))
	out, err := client.DoJSON(ctx, "GET", fmt.Sprintf("/api/v1/accounts/%s/following", id), query, token, nil)
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://following", out)
}

func resourceNotifications(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(20))
	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications", query, token, nil)
	if err != nil {
		return nil, err
	}
	return resourceJSON("agent://notifications", out)
}

func resourceMemoryRecent(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return nil, fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return nil, err
	}

	out, err := store.Query(ctx, strings.TrimSpace(p.Identity), memory.QueryInput{
		Limit: 50,
		Order: "desc",
	})
	if err != nil {
		return nil, err
	}

	return resourceJSON("agent://memory/recent", out)
}

func resourceCapabilities(srv *Server) mcpruntime.ResourceHandler {
	return func(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
		p := auth.PrincipalFromToolContext(ctx)
		scopes := []string{}
		if p != nil && p.Claims != nil {
			scopes = p.Claims.Scopes
		}

		tools := []string{}
		if srv != nil && srv.Registry() != nil {
			for _, t := range srv.Registry().List() {
				tools = append(tools, strings.TrimSpace(t.Name))
			}
		}
		resources := []string{}
		if srv != nil && srv.Resources() != nil {
			for _, r := range srv.Resources().List() {
				resources = append(resources, strings.TrimSpace(r.URI))
			}
		}
		prompts := []string{}
		if srv != nil && srv.Prompts() != nil {
			for _, p := range srv.Prompts().List() {
				prompts = append(prompts, strings.TrimSpace(p.Name))
			}
		}

		return resourceJSON("agent://capabilities", map[string]any{
			"scopes":    scopes,
			"tools":     tools,
			"resources": resources,
			"prompts":   prompts,
		})
	}
}

func resourceConfig(_ context.Context) ([]mcpruntime.ResourceContent, error) {
	return resourceJSON("agent://config", map[string]any{
		"mcp_endpoint":      strings.TrimSpace(os.Getenv("MCP_ENDPOINT")),
		"service_version":   strings.TrimSpace(os.Getenv("SERVICE_VERSION")),
		"lesser_table_name": strings.TrimSpace(os.Getenv("LESSER_TABLE_NAME")),
	})
}

func promptComposePost(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		Topic     string `json:"topic,omitempty"`
		Tone      string `json:"tone,omitempty"`
		MaxLength string `json:"max_length,omitempty"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
	}
	topic := strings.TrimSpace(in.Topic)
	tone := strings.TrimSpace(in.Tone)
	if tone == "" {
		tone = "neutral"
	}

	user := "Compose a post for the agent."
	if topic != "" {
		user += " Topic: " + topic + "."
	}
	user += " Tone: " + tone + "."
	if ml := strings.TrimSpace(in.MaxLength); ml != "" {
		user += " Max length: " + ml + " characters."
	}

	return &mcpruntime.PromptResult{
		Description: "Compose a post and then call post_create with the final text.",
		Messages: []mcpruntime.PromptMessage{
			{
				Role:    "system",
				Content: mcpruntime.ContentBlock{Type: "text", Text: "You are operating an agent account on Lesser. Write concise, safe posts. Do not reveal secrets."},
			},
			{
				Role:    "user",
				Content: mcpruntime.ContentBlock{Type: "text", Text: user},
			},
		},
	}, nil
}

func promptSummarizeTimeline(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		Timeline string `json:"timeline"`
		Period   string `json:"period,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Timeline = strings.ToLower(strings.TrimSpace(in.Timeline))
	if in.Timeline == "" {
		return nil, invalidParams("missing timeline")
	}

	text := fmt.Sprintf("Summarize the %s timeline.", in.Timeline)
	if strings.TrimSpace(in.Period) != "" {
		text += " Period: " + strings.TrimSpace(in.Period) + "."
	}
	text += " First call the timeline_read tool, then summarize key themes and notable posts."

	return &mcpruntime.PromptResult{
		Messages: []mcpruntime.PromptMessage{{
			Role:    "user",
			Content: mcpruntime.ContentBlock{Type: "text", Text: text},
		}},
	}, nil
}

func promptDraftReply(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		PostID string `json:"post_id"`
		Tone   string `json:"tone,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PostID = strings.TrimSpace(in.PostID)
	if in.PostID == "" {
		return nil, invalidParams("missing post_id")
	}
	tone := strings.TrimSpace(in.Tone)
	if tone == "" {
		tone = "neutral"
	}

	return &mcpruntime.PromptResult{
		Description: "Draft a reply and then call post_create with in_reply_to set to the post id.",
		Messages: []mcpruntime.PromptMessage{{
			Role: "user",
			Content: mcpruntime.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("Draft a reply to post %s. Tone: %s. Keep it concise and non-sensitive. Then call post_create with in_reply_to=%s.", in.PostID, tone, in.PostID),
			},
		}},
	}, nil
}

func promptReputationReport(_ context.Context, _ json.RawMessage) (*mcpruntime.PromptResult, error) {
	return &mcpruntime.PromptResult{
		Description: "Best-effort: explain the agent's reputation if available; otherwise describe what is missing.",
		Messages: []mcpruntime.PromptMessage{{
			Role: "user",
			Content: mcpruntime.ContentBlock{
				Type: "text",
				Text: "Generate a human-readable reputation report for the agent. If you cannot access reputation data, explain what is missing and how to obtain it.",
			},
		}},
	}, nil
}

func promptMemoryReflect(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	var in struct {
		Period string `json:"period,omitempty"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
	}
	period := strings.TrimSpace(in.Period)
	if period == "" {
		period = "recent"
	}

	return &mcpruntime.PromptResult{
		Description: "Reflect on memories and propose next actions.",
		Messages: []mcpruntime.PromptMessage{{
			Role: "user",
			Content: mcpruntime.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("Review %s memory events (use memory_query). Identify patterns, risks, and suggested next actions.", period),
			},
		}},
	}, nil
}
