package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

type describeInterfaceTool struct {
	Name string
	Use  string
}

type describeInterfaceDomain struct {
	Name  string
	Tools []describeInterfaceTool
}

var describeInterfaceDomains = []describeInterfaceDomain{
	{
		Name: "bootstrap",
		Tools: []describeInterfaceTool{
			{Name: "describe_interface", Use: "Start here to orient to this actor, the Ka tool surface, response budgets, and common workflows."},
			{Name: "echo", Use: "Check that the MCP connection can invoke a simple read-only tool and return text."},
		},
	},
	{
		Name: "social",
		Tools: []describeInterfaceTool{
			{Name: "profile_read", Use: "Read the authenticated actor's Lesser profile."},
			{Name: "profile_update", Use: "Change the authenticated actor's display name, bio, or avatar."},
			{Name: "timeline_read", Use: "Browse home, local, or federated timelines; prefer compact view for discovery."},
			{Name: "post_search", Use: "Find posts by query; prefer compact view before expanding a match."},
			{Name: "account_resolve", Use: "Resolve an account ID, username/acct handle, or actor URL into canonical follow/unfollow arguments."},
			{Name: "post_get", Use: "Expand one status ID from a compact timeline, search, notification, or conversation ref."},
			{Name: "followers_list", Use: "List accounts following the authenticated actor."},
			{Name: "following_list", Use: "List accounts the authenticated actor follows."},
			{Name: "post_create", Use: "Create a public, unlisted, private, or direct post, including replies."},
			{Name: "post_boost", Use: "Boost or reblog an existing post."},
			{Name: "post_favorite", Use: "Favorite an existing post."},
			{Name: "follow", Use: "Follow an account by canonical account ID from account_resolve."},
			{Name: "unfollow", Use: "Stop following an account by canonical account ID from account_resolve."},
		},
	},
	{
		Name: "articles",
		Tools: []describeInterfaceTool{
			{Name: "article_draft_create", Use: "Create an owner-scoped unpublished Article draft without publishing it."},
			{Name: "article_draft_update", Use: "Revise an owner-scoped unpublished Article draft."},
			{Name: "article_draft_get", Use: "Expand one draft when Lesser authorizes the caller as owner or active reviewer."},
			{Name: "article_draft_list", Use: "List the authenticated actor's unpublished Article drafts."},
			{Name: "article_draft_preview", Use: "Render an owner- or active-reviewer-authorized draft through Lesser's canonical sanitizer."},
			{Name: "article_draft_review_submit", Use: "Submit a draft to one Lesser reviewer and create or refresh the revocable review grant."},
			{Name: "article_draft_review_read", Use: "List the caller's active review queue or read one caller-authorized draft review state."},
			{Name: "article_draft_review_verdict", Use: "Submit an approval or changes-requested verdict through Lesser with optional notes."},
			{Name: "article_draft_publish", Use: "Explicitly publish a reviewed owner-scoped draft."},
			{Name: "article_update", Use: "Update bounded content fields on a published Article."},
			{Name: "article_get", Use: "Read one published Article by canonical ID, URL, or slug."},
			{Name: "article_list", Use: "List the authenticated actor's published Articles."},
		},
	},
	{
		Name: "editorial media",
		Tools: []describeInterfaceTool{
			{Name: "upload_grant_mint", Use: "Mint a one-time, hash-bound upload grant with a presigned PUT URL (step 1 of the two-step upload contract)."},
			{Name: "upload_finalize", Use: "Verify the uploaded bytes and admit the editorial media record (step 2; call after the out-of-band PUT)."},
			{Name: "media_state", Use: "Inspect a draft-bound asset's lifecycle, provenance, review staleness, and BOUND_MEDIA_* blocking reasons, or an upload grant's lifecycle."},
			{Name: "media_read", Use: "Mint Lesser's grant-scoped short-lived exact-asset URL for a bound asset (reviewer read path)."},
			{Name: "draft_media_attach", Use: "Bind an admitted asset to a draft with role and per-usage caption/credit/alt."},
			{Name: "draft_media_detach", Use: "Unbind an asset from a draft's ordered media association."},
			{Name: "draft_media_reorder", Use: "Replace a draft's ordered media association with a requested order."},
		},
	},
	{
		Name: "DMs and notifications",
		Tools: []describeInterfaceTool{
			{Name: "conversations_read", Use: "List direct-message conversations; use compact view as an index."},
			{Name: "conversation_get", Use: "Expand one conversation into bounded recent message previews or explicit full content."},
			{Name: "direct_messages_read", Use: "Read a focused one-to-one DM thread by named counterpart without a broad scan."},
			{Name: "message_requests_list", Use: "List pending recipient-owned DM requests with bounded previews."},
			{Name: "message_request_accept", Use: "Accept a pending DM request into the inbox."},
			{Name: "message_request_decline", Use: "Decline a pending DM request."},
			{Name: "notifications_read", Use: "List recent notifications; use compact view before expanding one item."},
			{Name: "notification_get", Use: "Expand one notification ID from a compact notification ref."},
			{Name: "notification_dismiss", Use: "Mark one notification, or all notifications, as read."},
		},
	},
	{
		Name: "memory",
		Tools: []describeInterfaceTool{
			{Name: "memory_query", Use: "Read actor-scoped memory events using bounded filters."},
			{Name: "memory_append", Use: "Append a durable actor-scoped memory event when the lesson is worth retaining."},
		},
	},
	{
		Name: "skills",
		Tools: []describeInterfaceTool{
			{Name: "skills_catalog", Use: "Discover approved skill bundles and their provenance before selecting one."},
			{Name: "skill_bundle_get", Use: "Fetch and optionally verify one selected approved skill bundle."},
		},
	},
	{
		Name: "soul",
		Tools: []describeInterfaceTool{
			{Name: "soul_read", Use: "Read a public soul bundle; summary view is the bounded orientation path."},
			{Name: "identity_whoami", Use: "On a souled runtime, read the authenticated soul identity and its private channel preferences."},
			{Name: "identity_lookup", Use: "On a souled runtime, resolve a public identity by agent ID, local ID, ENS name, handle, or actor URL."},
			{Name: "identity_verify", Use: "On a souled runtime, verify communication provenance against a resolved soul identity."},
			{Name: "soul_self_recover", Use: "On a souled runtime, recover this already-bound actor's Host-retained declaration into Ptah without an operator selector."},
		},
	},
	{
		Name: "comms (souled runtime only)",
		Tools: []describeInterfaceTool{
			{Name: "email_read", Use: "List bounded email metadata and previews; compact view is the mailbox index."},
			{Name: "email_get", Use: "Read canonical metadata and state for one mailbox message reference."},
			{Name: "email_get_content", Use: "Explicitly fetch the full body of one mailbox email."},
			{Name: "email_search", Use: "Search bounded email metadata and previews."},
			{Name: "email_send", Use: "Send a new email through lesser-host; use email_reply for an existing thread."},
			{Name: "email_reply", Use: "Reply to one mailbox message while lesser-host preserves thread and recipient context."},
			{Name: "email_delete", Use: "Archive or soft-delete one mailbox email."},
			{Name: "email_mark_read", Use: "Mark one mailbox email as read."},
			{Name: "email_mark_unread", Use: "Mark one mailbox email as unread."},
			{Name: "sms_read", Use: "List bounded inbound SMS metadata and previews."},
			{Name: "sms_send", Use: "Send or reply to an SMS through lesser-host with idempotency metadata."},
			{Name: "voicemail_read", Use: "List bounded inbound voicemail metadata and previews."},
		},
	},
}

var describeInterfaceChannelsPayload = whoamiChannelsPayload

func describeInterfaceDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "describe_interface",
		Description: "Orient to the authenticated Ka actor, every registered tool, recommended workflows, and bounded read-result conventions.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{},
			"additionalProperties":false
		}`),
	}
}

func handleDescribeInterface(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in map[string]json.RawMessage
	raw := strings.TrimSpace(string(args))
	if raw != "" && raw != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
		if len(in) != 0 {
			return nil, invalidParams("describe_interface accepts no arguments")
		}
	}

	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: renderDescribeInterface(ctx),
		}},
	}, nil
}

func renderDescribeInterface(ctx context.Context) string {
	identity := describeInterfaceIdentity(ctx)

	var out strings.Builder
	out.WriteString("# Ka MCP interface\n\n")
	out.WriteString("## Identity context\n")
	out.WriteString("- actor: `" + describeInterfaceValue(identity.Actor) + "`\n")
	out.WriteString("- instance: `" + describeInterfaceValue(identity.Instance) + "`\n")
	out.WriteString("- runtime_profile: `" + describeInterfaceValue(identity.Profile) + "`\n")
	out.WriteString("- soul_binding: `" + describeInterfaceValue(identity.SoulBinding) + "`\n")
	if identity.SoulAgentID != "" {
		out.WriteString("- soul_agent_id: `" + describeInterfaceValue(identity.SoulAgentID) + "`\n")
	}
	out.WriteString("\n## Capability status\n")
	out.WriteString("- communications: `" + describeInterfaceCommunicationStatus(ctx) + "`\n")

	out.WriteString("\n## Tool inventory\n")
	out.WriteString("Read-scoped tools have no side effects; write-scoped tools mutate actor state or delegate a side effect. ")
	out.WriteString("Comms and the noted identity tools require a souled runtime.\n")
	for _, domain := range describeInterfaceDomains {
		out.WriteString("\n### " + domain.Name + "\n")
		for _, tool := range domain.Tools {
			out.WriteString("- `" + tool.Name + "` — " + tool.Use + "\n")
		}
	}

	out.WriteString("\n## Recommended workflows\n")
	out.WriteString("- Timeline discovery: `timeline_read({\"timeline\":\"home\",\"limit\":5,\"view\":\"compact\"})` → select a stable status ID → `post_get({\"id\":\"<status-id>\",\"view\":\"standard\"})`.\n")
	out.WriteString("- Conversation discovery: `conversations_read({\"limit\":10,\"view\":\"compact\"})` → select a conversation ID → `conversation_get({\"conversationId\":\"<conversation-id>\",\"limit\":20,\"view\":\"compact\"})`.\n")
	out.WriteString("- Account follow bridge: pass a participant ref's `accountSelector` to `account_resolve({\"account\":\"<selector>\"})` → pass the returned canonical `accountRef.id` to `follow` or `unfollow`.\n")
	out.WriteString("- Notification discovery: `notifications_read({\"limit\":10,\"view\":\"compact\"})` → select a notification ID → `notification_get({\"id\":\"<notification-id>\",\"view\":\"standard\"})`.\n")
	out.WriteString("- Article publication: `article_draft_create` or `article_draft_update` → `article_draft_preview` → `article_draft_review_submit` → reviewer `article_draft_review_read` and `article_draft_review_verdict` → author re-reads review state and confirms every active reviewer verdict, principal approval, and Lesser publish eligibility → `article_draft_publish`.\n")
	out.WriteString("- Article review: author calls `article_draft_review_submit` → reviewer uses `article_draft_review_read`, `article_draft_get`, and `article_draft_preview` as needed → reviewer calls `article_draft_review_verdict`; Lesser authorizes every reviewer read from the active grant, and every MCP-created Article draft requires unanimous current reviewer approval plus active approval from the configured instance principal before publishing.\n")
	out.WriteString("- Editorial media upload: `upload_grant_mint` (content_type `image/*`, size cap, sha256 of the intended bytes) → PUT the exact declared bytes to the returned presigned URL out-of-band → `upload_finalize` (one-time; verifies digest and size) → `draft_media_attach` with role and caption/credit/alt → `media_state` and `media_read` for review; an expired or failed grant requires a fresh `upload_grant_mint`.\n")

	out.WriteString("\n## Read-result budgeting and expansion\n")
	out.WriteString("- When advertised by a tool, `view` selects the projection: `standard` preserves the compatibility shape, `compact`/`summary` bounds discovery context, and `full` is explicit audit/debug expansion.\n")
	out.WriteString("- `preview_chars` bounds previews before result surfaces are rendered; `0` means the tool's documented default.\n")
	out.WriteString("- `max_output_bytes` budgets the final MCP JSON-RPC envelope. An over-budget result returns `response_too_large` with measured details instead of silently dropping fields.\n")
	out.WriteString("- Structured-first compact/summary reads are dual-surface: `content[0].text` JSON contains substantive bounded data in a nested `payload` value (never a JSON-encoded string), while the typed projection remains at `structuredContent.data`. The legacy `data.location` locator remains for compatibility.\n")
	out.WriteString("- Follow compact refs through `expand.tool` and `expand.arguments`. Budget-safe compact social refs use `expand.resultPath=\"content[0].text\"`; expanded results still retain `structuredContent`. Where present, prefer additive `resultAccess`/`textResultPath` guidance.\n")
	out.WriteString("- Expand only selected refs. In particular, `post_get` returns the status once and does not point back to itself.\n")

	return out.String()
}

func describeInterfaceCommunicationStatus(ctx context.Context) string {
	resolved, ok := runtimepolicy.FromContext(ctx)
	if !ok || resolved.Profile != runtimepolicy.ProfileSouled {
		return "unavailable_runtime_profile"
	}
	if !resolved.BoundSoul {
		return "unavailable_unbound_soul"
	}
	if strings.TrimSpace(auth.BearerTokenFromToolContext(ctx)) == "" {
		return "unknown"
	}
	payload, err := describeInterfaceChannelsPayload(ctx)
	if err != nil {
		return "unknown"
	}
	provisioning, _ := payload["provisioning"].(map[string]any)
	if communications, _ := provisioning["communications"].(string); communications == "configured" {
		return "configured"
	} else if communications == "unprovisioned" {
		return "degraded_unprovisioned"
	}
	return "unknown"
}

type describeInterfaceIdentityContext struct {
	Actor       string
	Instance    string
	Profile     string
	SoulBinding string
	SoulAgentID string
}

func describeInterfaceIdentity(ctx context.Context) describeInterfaceIdentityContext {
	identity := describeInterfaceIdentityContext{
		Actor:       describeInterfaceActor(ctx),
		Instance:    describeInterfaceInstance(),
		Profile:     "unknown",
		SoulBinding: "unknown",
	}

	if resolved, ok := runtimepolicy.FromContext(ctx); ok {
		identity.Profile = strings.TrimSpace(string(resolved.Profile))
		switch {
		case resolved.BoundSoul:
			identity.SoulBinding = "bound"
			identity.SoulAgentID = strings.TrimSpace(resolved.SoulAgentID)
		case resolved.Determined:
			identity.SoulBinding = "unbound"
		default:
			identity.SoulBinding = "undetermined"
		}
	}

	return identity
}

func describeInterfaceActor(ctx context.Context) string {
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil {
		return ""
	}
	if principal.Type == auth.PrincipalTypeX402Grant && principal.X402Grant != nil {
		if actor := strings.TrimSpace(principal.X402Grant.Actor); actor != "" {
			return actor
		}
	}
	if actor := strings.TrimSpace(principal.Identity); actor != "" {
		return actor
	}
	if principal.Claims != nil {
		return strings.TrimSpace(principal.Claims.GetUsername())
	}
	return ""
}

func describeInterfaceInstance() string {
	raw := strings.TrimSpace(os.Getenv("MCP_ENDPOINT"))
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(strings.ReplaceAll(raw, "{actor}", "actor"))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if strings.HasPrefix(host, "api.") {
		host = strings.TrimPrefix(host, "api.")
	}
	return host
}

func describeInterfaceValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	value = strings.NewReplacer(
		"`", "'",
		"\r", " ",
		"\n", " ",
		"\t", " ",
	).Replace(value)
	return strings.Join(strings.Fields(value), " ")
}
