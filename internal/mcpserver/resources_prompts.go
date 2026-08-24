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
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
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
					{Name: "query", Description: "ENS name, agentId, current-instance local ID, explicit @user@domain ActivityPub handle, or canonical actor URL. Private email/phone lookup currently fails closed.", Required: true},
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
	// verify_credentials is act-as-enabled upstream, so share-grant callers
	// resolve the agent's account via the caller bearer + X-Lesser-Act-As.
	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authResourceContentsFromError("agent://profile", err)
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}
	out, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return lesserResourceContentsFromError("agent://profile", err)
	}
	return resourceJSON("agent://profile", out)
}

func resourceTimeline(kind string) mcpruntime.ResourceHandler {
	return func(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			return nil, invalidParams("missing timeline")
		}

		// Only the home timeline is act-as-enabled upstream. Local and
		// federated public timelines have no act-as surface and stay
		// fail-closed for share-grant callers with the unchanged gated
		// error shape (mirrors the timeline_read tool gating).
		var token string
		var err error
		if kind == "home" {
			ctx, token, err = requireActAsScopedOAuthBearer(ctx)
		} else {
			token, err = requireOwnerScopedOAuthBearer(ctx)
		}
		if err != nil {
			return authResourceContentsFromError("agent://timeline/"+kind, err)
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
			return lesserResourceContentsFromError("agent://timeline/"+kind, err)
		}
		return resourceJSON("agent://timeline/"+kind, out)
	}
}

func resourceFollowers(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOwnerScopedOAuthBearer(ctx)
	if err != nil {
		return authResourceContentsFromError("agent://followers", err)
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return lesserResourceContentsFromError("agent://followers", err)
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
		return lesserResourceContentsFromError("agent://followers", err)
	}
	return resourceJSON("agent://followers", out)
}

func resourceFollowing(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOwnerScopedOAuthBearer(ctx)
	if err != nil {
		return authResourceContentsFromError("agent://following", err)
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return lesserResourceContentsFromError("agent://following", err)
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
		return lesserResourceContentsFromError("agent://following", err)
	}
	return resourceJSON("agent://following", out)
}

func resourceNotifications(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	// The notifications stream is act-as-enabled upstream, so share-grant
	// callers read the agent's notifications via caller bearer + X-Lesser-Act-As.
	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authResourceContentsFromError("agent://notifications", err)
	}
	client, err := lesserapi.Default()
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(20))
	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications", query, token, nil)
	if err != nil {
		return lesserResourceContentsFromError("agent://notifications", err)
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
		_ = srv

		p := auth.PrincipalFromToolContext(ctx)
		scopes := []string{}
		if p != nil && p.Claims != nil {
			scopes = append([]string(nil), p.Claims.Scopes...)
		}

		runtime := runtimepolicy.ResolveForActor(ctx, "")
		if fromContext, ok := runtimepolicy.FromContext(ctx); ok {
			runtime = fromContext
		}
		tools := runtimepolicy.ToolsForProfile(runtime.Profile)
		resources := runtimepolicy.ResourcesForProfile(runtime.Profile)
		prompts := runtimepolicy.PromptsForProfile(runtime.Profile)

		return resourceJSON("agent://capabilities", map[string]any{
			"scopes":    scopes,
			"runtime":   runtime,
			"tools":     tools,
			"resources": resources,
			"prompts":   prompts,
			"contracts": runtimepolicy.Contracts(),
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

const maxCompletionValues = 20

var (
	toneCompletionValues = []string{
		"neutral",
		"friendly",
		"formal",
		"concise",
		"playful",
		"careful",
	}
	periodCompletionValues = []string{
		"last hour",
		"today",
		"last day",
		"this week",
		"last week",
	}
)

func promptCompletion(ctx context.Context, req mcpruntime.CompletionRequest) (*mcpruntime.CompletionResult, error) {
	profile := completionProfile(ctx)
	promptName := strings.TrimSpace(req.Ref.Name)
	argumentName := strings.TrimSpace(req.Argument.Name)
	if promptName == "" || argumentName == "" || !runtimepolicy.PromptAllowed(profile, promptName) {
		return completionValues(nil), nil
	}

	var values []string
	switch promptName {
	case "compose_post":
		switch argumentName {
		case "tone":
			values = toneCompletionValues
		case "max_length":
			values = []string{"280", "500", "1000"}
		}
	case "summarize_timeline":
		switch argumentName {
		case "timeline":
			values = []string{"home", "local", "federated"}
		case "period":
			values = periodCompletionValues
		}
	case "draft_reply":
		if argumentName == "tone" {
			values = toneCompletionValues
		}
	case "memory_reflect":
		if argumentName == "period" {
			values = periodCompletionValues
		}
	case "compose_email":
		if argumentName == "tone" {
			values = toneCompletionValues
		}
	case "handle_inbound":
		switch argumentName {
		case "channel":
			values = []string{"email", "sms", "voice"}
		case "intent":
			values = []string{"summarize", "reply if appropriate", "archive if no action is needed"}
		}
	}

	return completionValues(filterCompletionValues(values, req.Argument.Value)), nil
}

func resourceCompletion(ctx context.Context, req mcpruntime.CompletionRequest) (*mcpruntime.CompletionResult, error) {
	profile := completionProfile(ctx)
	argumentName := strings.TrimSpace(req.Argument.Name)
	if !supportsResourceCompletionRef(req.Ref.URI, argumentName) {
		return completionValues(nil), nil
	}

	resources := runtimepolicy.ResourcesForProfile(profile)
	values := make([]string, 0, len(resources))
	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if resource == "" {
			continue
		}
		switch argumentName {
		case "resource", "path":
			values = append(values, strings.TrimPrefix(resource, "agent://"))
		default:
			values = append(values, resource)
		}
	}

	return completionValues(filterCompletionValues(values, req.Argument.Value)), nil
}

func completionProfile(ctx context.Context) runtimepolicy.Profile {
	if resolved, ok := runtimepolicy.FromContext(ctx); ok && strings.TrimSpace(string(resolved.Profile)) != "" {
		return resolved.Profile
	}
	return runtimepolicy.ProfileSouled
}

func supportsResourceCompletionRef(uri string, argumentName string) bool {
	uri = strings.TrimSpace(uri)
	switch strings.TrimSpace(argumentName) {
	case "uri":
		return uri == "{uri}" || strings.Contains(uri, "{uri}")
	case "resource":
		return strings.Contains(uri, "{resource}")
	case "path":
		return strings.Contains(uri, "{path}")
	default:
		return false
	}
}

func filterCompletionValues(values []string, rawPrefix string) []string {
	prefix := strings.ToLower(strings.TrimSpace(rawPrefix))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), prefix) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func completionValues(values []string) *mcpruntime.CompletionResult {
	if values == nil {
		values = []string{}
	}
	total := len(values)
	hasMore := false
	if total > maxCompletionValues {
		hasMore = true
		values = copyCompletionValues(values[:maxCompletionValues])
	} else {
		values = copyCompletionValues(values)
	}
	return &mcpruntime.CompletionResult{
		Completion: mcpruntime.Completion{
			Values:  values,
			Total:   &total,
			HasMore: &hasMore,
		},
	}
}

func copyCompletionValues(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	return out
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
