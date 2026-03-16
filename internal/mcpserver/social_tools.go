package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func registerSocialTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: profileReadDef(), Handler: handleProfileRead},
		{Def: timelineReadDef(), Handler: handleTimelineRead},
		{Def: postSearchDef(), Handler: handlePostSearch},
		{Def: followersListDef(), Handler: handleFollowersList},
		{Def: followingListDef(), Handler: handleFollowingList},
		{Def: notificationsReadDef(), Handler: handleNotificationsRead},
		{Def: postCreateDef(), Handler: handlePostCreate},
		{Def: postBoostDef(), Handler: handlePostBoost},
		{Def: postFavoriteDef(), Handler: handlePostFavorite},
		{Def: followDef(), Handler: handleFollow},
		{Def: unfollowDef(), Handler: handleUnfollow},
		{Def: profileUpdateDef(), Handler: handleProfileUpdate},
	} {
		if err := r.RegisterTool(tool.Def, tool.Handler); err != nil {
			return err
		}
	}

	return nil
}

func toolJSONResult(payload any) (*mcpruntime.ToolResult, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		StructuredContent: map[string]any{
			"data": payload,
		},
	}, nil
}

func requireOAuthBearer(ctx context.Context) (string, error) {
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || p.Type != auth.PrincipalTypeOAuthToken {
		return "", fmt.Errorf("oauth token required")
	}
	token := strings.TrimSpace(auth.BearerTokenFromToolContext(ctx))
	if token == "" {
		return "", fmt.Errorf("missing bearer token")
	}
	return token, nil
}

func lesser(ctx context.Context) (*lesserapi.Client, error) {
	_ = ctx
	return lesserapi.Default()
}

func handleProfileRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	if raw := bytes.TrimSpace(args); len(raw) > 0 {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
		if v != nil {
			if _, ok := v.(map[string]any); !ok {
				return nil, invalidParams("arguments must be an object")
			}
		}
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleTimelineRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Timeline string `json:"timeline"`
		Since    string `json:"since,omitempty"`
		Limit    int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Timeline = strings.ToLower(strings.TrimSpace(in.Timeline))
	if in.Timeline == "" {
		return nil, invalidParams("missing timeline")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	if strings.TrimSpace(in.Since) != "" {
		query.Set("max_id", strings.TrimSpace(in.Since))
	}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}

	path := ""
	switch in.Timeline {
	case "home":
		path = "/api/v1/timelines/home"
	case "local":
		path = "/api/v1/timelines/public"
		query.Set("local", "true")
	case "federated":
		path = "/api/v1/timelines/public"
	default:
		return nil, invalidParams("invalid timeline (expected home, local, federated)")
	}

	out, err := client.DoJSON(ctx, "GET", path, query, token, nil)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handlePostSearch(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return nil, invalidParams("missing query")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("q", in.Query)
	query.Set("type", "statuses")
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v2/search", query, token, nil)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleFollowersList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
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
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if strings.TrimSpace(in.Cursor) != "" {
		query.Set("max_id", strings.TrimSpace(in.Cursor))
	}

	out, err := client.DoJSON(ctx, "GET", fmt.Sprintf("/api/v1/accounts/%s/followers", id), query, token, nil)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleFollowingList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
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
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if strings.TrimSpace(in.Cursor) != "" {
		query.Set("max_id", strings.TrimSpace(in.Cursor))
	}

	out, err := client.DoJSON(ctx, "GET", fmt.Sprintf("/api/v1/accounts/%s/following", id), query, token, nil)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleNotificationsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Types []string `json:"types,omitempty"`
		Since string   `json:"since,omitempty"`
		Limit int      `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	requestedTypes, upstreamTypes := normalizeRequestedNotificationTypes(in.Types)

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	if in.Limit > 0 {
		query.Set("limit", strconv.Itoa(in.Limit))
	}
	if strings.TrimSpace(in.Since) != "" {
		query.Set("max_id", strings.TrimSpace(in.Since))
	}
	for _, typ := range upstreamTypes {
		query.Add("types[]", typ)
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications", query, token, nil)
	if err != nil {
		return nil, err
	}

	list, ok := out.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected notifications response")
	}

	nextSince := ""
	if len(list) > 0 {
		if item, ok := list[len(list)-1].(map[string]any); ok {
			nextSince = strings.TrimSpace(stringFromMap(item, "id"))
		}
	}

	notifications := socialNotificationsFromAPI(list)
	if len(requestedTypes) > 0 {
		notifications = filterSocialNotificationsByType(notifications, requestedTypes)
	}

	return toolJSONResult(map[string]any{
		"since":         strings.TrimSpace(in.Since),
		"nextSince":     nextSince,
		"count":         len(notifications),
		"types":         requestedTypes,
		"notifications": notifications,
	})
}

func handlePostCreate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Content    string `json:"content"`
		Visibility string `json:"visibility,omitempty"`
		InReplyTo  string `json:"in_reply_to,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return nil, invalidParams("missing content")
	}
	in.Visibility = strings.TrimSpace(in.Visibility)
	if in.Visibility == "" {
		in.Visibility = "public"
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"status":     in.Content,
		"visibility": in.Visibility,
	}
	if strings.TrimSpace(in.InReplyTo) != "" {
		body["in_reply_to_id"] = strings.TrimSpace(in.InReplyTo)
	}

	out, err := client.DoJSON(ctx, "POST", "/api/v1/statuses", nil, token, body)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handlePostBoost(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PostID string `json:"post_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PostID = strings.TrimSpace(in.PostID)
	if in.PostID == "" {
		return nil, invalidParams("missing post_id")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/statuses/%s/reblog", url.PathEscape(in.PostID)), nil, token, map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handlePostFavorite(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		PostID string `json:"post_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.PostID = strings.TrimSpace(in.PostID)
	if in.PostID == "" {
		return nil, invalidParams("missing post_id")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/statuses/%s/favourite", url.PathEscape(in.PostID)), nil, token, map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleFollow(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.AccountID = strings.TrimSpace(in.AccountID)
	if in.AccountID == "" {
		return nil, invalidParams("missing account_id")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/accounts/%s/follow", url.PathEscape(in.AccountID)), nil, token, map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleUnfollow(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.AccountID = strings.TrimSpace(in.AccountID)
	if in.AccountID == "" {
		return nil, invalidParams("missing account_id")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/accounts/%s/unfollow", url.PathEscape(in.AccountID)), nil, token, map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func handleProfileUpdate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DisplayName string `json:"display_name,omitempty"`
		Bio         string `json:"bio,omitempty"`
		AvatarURL   string `json:"avatar_url,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Bio = strings.TrimSpace(in.Bio)
	in.AvatarURL = strings.TrimSpace(in.AvatarURL)
	if in.DisplayName == "" && in.Bio == "" && in.AvatarURL == "" {
		return nil, invalidParams("no fields provided")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return nil, err
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if in.DisplayName != "" {
		body["display_name"] = in.DisplayName
	}
	if in.Bio != "" {
		body["note"] = in.Bio
	}
	if in.AvatarURL != "" {
		// Lesser’s JSON surface expects `avatar` (string) for non-multipart updates.
		body["avatar"] = in.AvatarURL
	}

	out, err := client.DoJSON(ctx, "PATCH", "/api/v1/accounts/update_credentials", nil, token, body)
	if err != nil {
		return nil, err
	}
	return toolJSONResult(out)
}

func profileReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "profile_read",
		Description: "Read the authenticated agent's profile.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func timelineReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "timeline_read",
		Description: "Read from home, local, or federated timeline.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"timeline":{"type":"string","enum":["home","local","federated"]},
				"since":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":200}
			},
			"required":["timeline"]
		}`),
	}
}

func postSearchDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "post_search",
		Description: "Search posts.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":200}
			},
			"required":["query"]
		}`),
	}
}

func followersListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "followers_list",
		Description: "List the agent's followers.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"cursor":{"type":"string"}
			}
		}`),
	}
}

func followingListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "following_list",
		Description: "List accounts the agent follows.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"cursor":{"type":"string"}
			}
		}`),
	}
}

func notificationsReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "notifications_read",
		Description: "Read recent social notifications with normalized actor and target post data.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"types":{"type":"array","items":{"type":"string"}},
				"since":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":80}
			}
		}`),
	}
}

func normalizeRequestedNotificationTypes(in []string) ([]string, []string) {
	if len(in) == 0 {
		return nil, nil
	}

	requested := make([]string, 0, len(in))
	upstream := make([]string, 0, len(in))
	seenRequested := make(map[string]struct{}, len(in))
	seenUpstream := make(map[string]struct{}, len(in))

	for _, typ := range in {
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "", "all":
			continue
		case "favorite":
			typ = "favourite"
		}
		if _, ok := seenRequested[typ]; !ok {
			seenRequested[typ] = struct{}{}
			requested = append(requested, typ)
		}

		upstreamType := typ
		if typ == "reply" {
			upstreamType = "mention"
		}
		if _, ok := seenUpstream[upstreamType]; !ok {
			seenUpstream[upstreamType] = struct{}{}
			upstream = append(upstream, upstreamType)
		}
	}

	return requested, upstream
}

func socialNotificationsFromAPI(items []any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		out = append(out, normalizeSocialNotification(raw))
	}
	return out
}

func filterSocialNotificationsByType(items []any, want []string) []any {
	if len(want) == 0 {
		return items
	}

	allowed := make(map[string]struct{}, len(want))
	for _, typ := range want {
		allowed[strings.ToLower(strings.TrimSpace(typ))] = struct{}{}
	}

	out := make([]any, 0, len(items))
	for _, item := range items {
		notification, _ := item.(map[string]any)
		typ, _ := notification["type"].(string)
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(typ))]; ok {
			out = append(out, notification)
		}
	}
	return out
}

func normalizeSocialNotification(raw map[string]any) map[string]any {
	out := map[string]any{
		"id":   strings.TrimSpace(stringFromMap(raw, "id")),
		"type": normalizeSocialNotificationType(raw),
		"raw":  raw,
	}

	if createdAt := firstNonEmptyStringMap(raw, "created_at", "createdAt"); createdAt != "" {
		out["createdAt"] = createdAt
	}
	if actor := normalizeSocialNotificationActor(firstMap(raw, "account", "actor")); actor != nil {
		out["actor"] = actor
	}
	if post := normalizeSocialNotificationPost(firstMap(raw, "status", "post", "targetPost")); post != nil {
		out["targetPost"] = post
	}

	return out
}

func normalizeSocialNotificationType(raw map[string]any) string {
	rawType := strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(raw, "type", "notificationType")))
	switch rawType {
	case "favorite":
		return "favourite"
	case "mention":
		post := firstMap(raw, "status", "post", "targetPost")
		if post != nil && firstNonEmptyStringMap(post, "in_reply_to_id", "inReplyToId") != "" {
			return "reply"
		}
	}
	return rawType
}

func normalizeSocialNotificationActor(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}

	out := map[string]any{}
	putIfNotEmpty(out, "id", firstNonEmptyStringMap(raw, "id"))
	putIfNotEmpty(out, "username", firstNonEmptyStringMap(raw, "username"))
	putIfNotEmpty(out, "acct", firstNonEmptyStringMap(raw, "acct"))
	putIfNotEmpty(out, "displayName", firstNonEmptyStringMap(raw, "display_name", "displayName"))
	putIfNotEmpty(out, "url", firstNonEmptyStringMap(raw, "url"))
	putIfNotEmpty(out, "avatar", firstNonEmptyStringMap(raw, "avatar", "avatar_static", "avatarStatic"))
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeSocialNotificationPost(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}

	out := map[string]any{}
	putIfNotEmpty(out, "id", firstNonEmptyStringMap(raw, "id"))
	putIfNotEmpty(out, "url", firstNonEmptyStringMap(raw, "url", "uri"))
	putIfNotEmpty(out, "content", firstNonEmptyStringMap(raw, "content", "text"))
	putIfNotEmpty(out, "createdAt", firstNonEmptyStringMap(raw, "created_at", "createdAt"))
	putIfNotEmpty(out, "inReplyToId", firstNonEmptyStringMap(raw, "in_reply_to_id", "inReplyToId"))
	putIfNotEmpty(out, "visibility", firstNonEmptyStringMap(raw, "visibility"))
	if author := normalizeSocialNotificationActor(firstMap(raw, "account")); author != nil {
		out["author"] = author
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstMap(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		value, _ := raw[key].(map[string]any)
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmptyStringMap(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringFromMap(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func putIfNotEmpty(dest map[string]any, key string, value string) {
	if dest == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	dest[key] = strings.TrimSpace(value)
}

func postCreateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "post_create",
		Description: "Create a new post.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{"type":"string"},
				"visibility":{"type":"string","enum":["public","unlisted","private","direct"]},
				"in_reply_to":{"type":"string"}
			},
			"required":["content"]
		}`),
	}
}

func postBoostDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "post_boost",
		Description: "Boost/reblog a post.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"post_id":{"type":"string"}},
			"required":["post_id"]
		}`),
	}
}

func postFavoriteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "post_favorite",
		Description: "Favorite a post.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"post_id":{"type":"string"}},
			"required":["post_id"]
		}`),
	}
}

func followDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "follow",
		Description: "Follow an account.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"account_id":{"type":"string"}},
			"required":["account_id"]
		}`),
	}
}

func unfollowDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "unfollow",
		Description: "Unfollow an account.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"account_id":{"type":"string"}},
			"required":["account_id"]
		}`),
	}
}

func profileUpdateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "profile_update",
		Description: "Update display name, bio, and avatar (best-effort).",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"display_name":{"type":"string"},
				"bio":{"type":"string"},
				"avatar_url":{"type":"string"}
			}
		}`),
	}
}
