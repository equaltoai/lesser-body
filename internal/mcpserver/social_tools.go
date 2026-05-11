package mcpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/memory"
	"github.com/oklog/ulid/v2"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	notificationCursorMemoryPrefix  = "notification_cursor:"
	notificationCursorMemoryTag     = "notification_cursor"
	notificationReadDefaultLimit    = 30
	notificationReadMaxLimit        = 80
	notificationReadMaxTypes        = 8
	notificationContentPreviewRunes = 500
	notificationCommPreviewRunes    = 240
	conversationReadDefaultLimit    = 20
	conversationReadMaxLimit        = 80
)

var notificationCursorEventIDs = struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}{
	entropy: ulid.Monotonic(rand.Reader, 0),
}

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
		{Def: conversationsReadDef(), Handler: handleConversationsRead},
		{Def: notificationsReadDef(), Handler: handleNotificationsRead},
		{Def: notificationDismissDef(), Handler: handleNotificationDismiss},
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

func toolJSONResult(payload any, structured map[string]any) (*mcpruntime.ToolResult, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	if structured == nil {
		structured = map[string]any{
			"data": payload,
		}
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		StructuredContent: structured,
	}, nil
}

func toolStructuredContent(payload any) (map[string]any, error) {
	if payload == nil {
		return map[string]any{}, nil
	}
	if structured, ok := payload.(map[string]any); ok {
		return structured, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal structured tool result: %w", err)
	}
	var structured map[string]any
	if err := json.Unmarshal(b, &structured); err != nil {
		return nil, fmt.Errorf("unmarshal structured tool result: %w", err)
	}
	if structured == nil {
		structured = map[string]any{}
	}
	return structured, nil
}

func requireOAuthBearer(ctx context.Context) (string, error) {
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || p.Type != auth.PrincipalTypeOAuthToken {
		return "", oauthBearerRequiredFailure("missing_oauth_bearer")
	}
	token := strings.TrimSpace(auth.BearerTokenFromToolContext(ctx))
	if token == "" {
		return "", oauthBearerRequiredFailure("missing_bearer_token")
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	account, err := client.DoJSON(ctx, "GET", "/api/v1/accounts/verify_credentials", nil, token, nil)
	if err != nil {
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
}

func handleConversationsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Limit      int  `json:"limit,omitempty"`
		IncludeRaw bool `json:"include_raw,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	limit := boundedConversationReadLimit(in.Limit)

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))

	out, err := client.DoJSON(ctx, "GET", "/api/v1/conversations", query, token, nil)
	if err != nil {
		return authToolResultFromError(err)
	}
	conversations, err := socialConversationsFromAPI(out, in.IncludeRaw)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"conversations": conversations,
		"count":         len(conversations),
		"limit":         limit,
		"includeRaw":    in.IncludeRaw,
	}
	return toolJSONResult(payload, nil)
}

func handleNotificationsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Types      []string `json:"types,omitempty"`
		Since      *string  `json:"since"`
		Cursor     string   `json:"cursor,omitempty"`
		Limit      int      `json:"limit,omitempty"`
		IncludeRaw bool     `json:"include_raw,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	startedAt := time.Now()
	requestedTypes, upstreamTypes, err := normalizeRequestedNotificationTypes(in.Types)
	if err != nil {
		return nil, err
	}
	limit := boundedNotificationReadLimit(in.Limit)
	explicitSince := in.Since != nil
	requestedSince := trimDeref(in.Since)
	effectiveSince := requestedSince
	effectiveCursor := strings.TrimSpace(in.Cursor)
	sinceTime, hasTemporalSince := parseNotificationSince(requestedSince)

	if explicitSince && requestedSince != "" && !hasTemporalSince && effectiveCursor == "" {
		effectiveCursor = requestedSince
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	apiStartedAt := time.Now()
	list, nextCursor, err := readSocialNotifications(ctx, client, token, upstreamTypes, limit, effectiveCursor)
	apiDuration := time.Since(apiStartedAt)
	if err != nil {
		return authToolResultFromError(err)
	}

	normalizeStartedAt := time.Now()
	notifications := socialNotificationsFromAPI(list, in.IncludeRaw)
	if hasTemporalSince {
		notifications = filterSocialNotificationsAfter(notifications, sinceTime)
	}
	if len(requestedTypes) > 0 {
		notifications = filterSocialNotificationsByType(notifications, requestedTypes)
	}
	sortSocialNotificationsNewestFirst(notifications)
	if len(notifications) > limit {
		notifications = notifications[:limit]
	}

	nextSince := ""
	if len(notifications) > 0 {
		if newest, ok := notifications[0].(map[string]any); ok {
			if hasTemporalSince || effectiveCursor == "" {
				if createdAt, ok := notificationCreatedAt(newest); ok {
					nextSince = createdAt.Format(time.RFC3339Nano)
				}
			}
		}
		if nextSince == "" {
			if oldest, ok := notifications[len(notifications)-1].(map[string]any); ok {
				nextSince = strings.TrimSpace(firstNonEmptyStringMap(oldest, "id"))
			}
		}
	} else if hasTemporalSince {
		nextSince = sinceTime.Format(time.RFC3339Nano)
	}

	normalizeDuration := time.Since(normalizeStartedAt)
	payload := map[string]any{
		"since":         effectiveSince,
		"cursor":        effectiveCursor,
		"nextSince":     nextSince,
		"nextCursor":    nextCursor,
		"count":         len(notifications),
		"types":         requestedTypes,
		"notifications": notifications,
		"includeRaw":    in.IncludeRaw,
	}
	attachNotificationReadDiagnostics(payload, notificationReadDiagnostics{
		API:             apiDuration,
		Normalize:       normalizeDuration,
		Total:           time.Since(startedAt),
		UpstreamCount:   len(list),
		NormalizedCount: len(notifications),
		RawIncluded:     in.IncludeRaw,
	})
	return toolJSONResult(payload, nil)
}

func handleNotificationDismiss(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		ID string `json:"id,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.ID = strings.TrimSpace(in.ID)

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	path := "/api/v1/notifications/clear"
	if in.ID != "" {
		path = "/api/v1/notifications/" + url.PathEscape(in.ID) + "/dismiss"
	}

	if _, err := client.DoJSON(ctx, "POST", path, nil, token, map[string]any{}); err != nil {
		var apiErr *lesserapi.APIError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			if in.ID != "" {
				return nil, fmt.Errorf("notification %q not found", in.ID)
			}
			return nil, fmt.Errorf("notifications not found")
		}
		return authToolResultFromError(err)
	}

	if in.ID == "" {
		_ = clearNotificationCursor(ctx)
	}

	return toolJSONResult(map[string]any{
		"ok":      true,
		"id":      in.ID,
		"dismiss": ternaryString(in.ID == "", "all", "single"),
	}, nil)
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
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/statuses/%s/reblog", url.PathEscape(in.PostID)), nil, token, map[string]any{})
	if err != nil {
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/statuses/%s/favourite", url.PathEscape(in.PostID)), nil, token, map[string]any{})
	if err != nil {
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/accounts/%s/follow", url.PathEscape(in.AccountID)), nil, token, map[string]any{})
	if err != nil {
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "POST", fmt.Sprintf("/api/v1/accounts/%s/unfollow", url.PathEscape(in.AccountID)), nil, token, map[string]any{})
	if err != nil {
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
		return authToolResultFromError(err)
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
		return authToolResultFromError(err)
	}
	return toolJSONResult(out, nil)
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
				"cursor":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"include_raw":{"type":"boolean","description":"Include verbose upstream notification payloads under _raw for audit/debug use. Defaults to false and increases response size."}
			}
		}`),
	}
}

func conversationsReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "conversations_read",
		Description: "Read compact, bounded direct message conversation summaries. Use include_raw only for audit/debug.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"include_raw":{"type":"boolean","description":"Include verbose upstream conversation payloads under _raw. Defaults to false."}
			}
		}`),
	}
}

func notificationDismissDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "notification_dismiss",
		Description: "Dismiss a notification or all notifications, marking them as read.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"ID of a specific notification to dismiss. Omit to dismiss all notifications."}
			}
		}`),
	}
}

func trimDeref(raw *string) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(*raw)
}

func readNotificationCursor(ctx context.Context) (string, error) {
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return "", fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return "", err
	}

	res, err := store.Query(ctx, strings.TrimSpace(p.Identity), memory.QueryInput{
		Query: notificationCursorMemoryPrefix,
		Limit: 1,
		Order: "desc",
	})
	if err != nil {
		return "", err
	}
	if len(res.Events) == 0 {
		return "", nil
	}
	return parseNotificationCursorContent(res.Events[0].Content), nil
}

func writeNotificationCursor(ctx context.Context, cursor string) error {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil
	}

	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return err
	}
	eventID, err := nextNotificationCursorEventID()
	if err != nil {
		return err
	}

	_, err = store.Append(ctx, strings.TrimSpace(p.Identity), memory.AppendInput{
		EventID: eventID,
		Content: notificationCursorMemoryPrefix + cursor,
		Tags:    []string{notificationCursorMemoryTag},
	})
	return err
}

func clearNotificationCursor(ctx context.Context) error {
	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return err
	}
	eventID, err := nextNotificationCursorEventID()
	if err != nil {
		return err
	}

	_, err = store.Append(ctx, strings.TrimSpace(p.Identity), memory.AppendInput{
		EventID: eventID,
		Content: notificationCursorMemoryPrefix,
		Tags:    []string{notificationCursorMemoryTag},
	})
	return err
}

func parseNotificationCursorContent(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, notificationCursorMemoryPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(content, notificationCursorMemoryPrefix))
}

func nextNotificationCursorEventID() (string, error) {
	notificationCursorEventIDs.mu.Lock()
	defer notificationCursorEventIDs.mu.Unlock()

	id, err := ulid.New(ulid.Timestamp(time.Now().UTC()), notificationCursorEventIDs.entropy)
	if err != nil {
		return "", fmt.Errorf("generate notification cursor event id: %w", err)
	}
	return id.String(), nil
}

func ternaryString(cond bool, yes string, no string) string {
	if cond {
		return yes
	}
	return no
}

func normalizeRequestedNotificationTypes(in []string) ([]string, []string, error) {
	if len(in) == 0 {
		return nil, nil, nil
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
		if !allowedNotificationType(typ) {
			return nil, nil, invalidParams("unsupported notification type: " + typ)
		}
		if _, ok := seenRequested[typ]; !ok {
			if len(requested) >= notificationReadMaxTypes {
				return nil, nil, invalidParams("too many notification types requested")
			}
			seenRequested[typ] = struct{}{}
			requested = append(requested, typ)
		}
		if _, ok := seenUpstream[typ]; !ok {
			seenUpstream[typ] = struct{}{}
			upstream = append(upstream, typ)
		}
	}

	return requested, upstream, nil
}

func allowedNotificationType(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "mention", "reply", "favourite", "reblog", "follow", "follow_request", "poll", "status", "update", "admin.sign_up", "admin.report":
		return true
	default:
		return false
	}
}

func boundedNotificationReadLimit(limit int) int {
	switch {
	case limit <= 0:
		return notificationReadDefaultLimit
	case limit > notificationReadMaxLimit:
		return notificationReadMaxLimit
	default:
		return limit
	}
}

func boundedConversationReadLimit(limit int) int {
	switch {
	case limit <= 0:
		return conversationReadDefaultLimit
	case limit > conversationReadMaxLimit:
		return conversationReadMaxLimit
	default:
		return limit
	}
}

func readSocialNotifications(ctx context.Context, client *lesserapi.Client, token string, upstreamTypes []string, limit int, cursor string) ([]any, string, error) {
	if client == nil {
		return nil, "", fmt.Errorf("lesser api client not initialized")
	}

	queries := upstreamTypes
	if len(queries) == 0 {
		queries = nil
	}

	if len(queries) <= 1 {
		return readSocialNotificationsByType(ctx, client, token, firstString(queries), limit, cursor)
	}

	capacityHint := len(queries) * max(1, boundedNotificationReadLimit(limit))
	combined := make([]map[string]any, 0, capacityHint)
	seenIDs := make(map[string]struct{}, capacityHint)
	for _, typ := range queries {
		items, _, err := readSocialNotificationsByType(ctx, client, token, typ, limit, cursor)
		if err != nil {
			return nil, "", err
		}
		for _, item := range items {
			raw, ok := item.(map[string]any)
			if !ok || raw == nil {
				continue
			}
			id := strings.TrimSpace(stringFromMap(raw, "id"))
			if id != "" {
				if _, ok := seenIDs[id]; ok {
					continue
				}
				seenIDs[id] = struct{}{}
			}
			combined = append(combined, raw)
		}
	}

	sort.SliceStable(combined, func(i, j int) bool {
		leftTime, leftOK := notificationCreatedAt(combined[i])
		rightTime, rightOK := notificationCreatedAt(combined[j])
		switch {
		case leftOK && rightOK && !leftTime.Equal(rightTime):
			return leftTime.After(rightTime)
		case leftOK != rightOK:
			return leftOK
		}
		return strings.TrimSpace(stringFromMap(combined[i], "id")) > strings.TrimSpace(stringFromMap(combined[j], "id"))
	})

	out := make([]any, 0, len(combined))
	for _, item := range combined {
		out = append(out, item)
	}
	return out, "", nil
}

func readSocialNotificationsByType(ctx context.Context, client *lesserapi.Client, token string, notificationType string, limit int, cursor string) ([]any, string, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if strings.TrimSpace(cursor) != "" {
		query.Set("max_id", strings.TrimSpace(cursor))
	}
	if strings.TrimSpace(notificationType) != "" {
		query.Add("types[]", strings.TrimSpace(notificationType))
	}

	out, headers, err := client.DoJSONWithHeaders(ctx, "GET", "/api/v1/notifications", query, token, nil)
	if err != nil {
		return nil, "", err
	}

	list, ok := out.([]any)
	if !ok {
		return nil, "", fmt.Errorf("unexpected notifications response")
	}
	return list, nextNotificationCursorFromHeaders(headers), nil
}

func socialConversationsFromAPI(raw any, includeRaw bool) ([]any, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected conversations response")
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		conversation, ok := item.(map[string]any)
		if !ok || conversation == nil {
			continue
		}
		out = append(out, normalizeSocialConversation(conversation, includeRaw))
	}
	return out, nil
}

func normalizeSocialConversation(raw map[string]any, includeRaw bool) map[string]any {
	out := map[string]any{
		"id": strings.TrimSpace(stringFromMap(raw, "id")),
	}
	if includeRaw {
		out["_raw"] = raw
	}
	if unread, ok := raw["unread"].(bool); ok {
		out["unread"] = unread
	}
	if updatedAt := firstNonEmptyStringMap(raw, "updated_at", "updatedAt"); updatedAt != "" {
		out["updatedAt"] = updatedAt
	}
	if accountsRaw, ok := raw["accounts"].([]any); ok {
		accounts := make([]any, 0, len(accountsRaw))
		for _, item := range accountsRaw {
			if account := normalizeSocialNotificationActor(mapFromAny(item)); account != nil {
				accounts = append(accounts, account)
			}
		}
		if len(accounts) > 0 {
			out["participants"] = accounts
		}
	}
	if last := normalizeSocialNotificationPost(firstMap(raw, "last_status", "lastStatus", "lastPost")); last != nil {
		out["lastPost"] = last
	}
	return out
}

func socialNotificationsFromAPI(items []any, includeRaw bool) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		out = append(out, normalizeSocialNotification(raw, includeRaw))
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

func filterSocialNotificationsAfter(items []any, since time.Time) []any {
	if since.IsZero() {
		return items
	}

	out := make([]any, 0, len(items))
	for _, item := range items {
		notification, _ := item.(map[string]any)
		createdAt, ok := notificationCreatedAt(notification)
		if !ok || !createdAt.After(since) {
			continue
		}
		out = append(out, notification)
	}
	return out
}

func sortSocialNotificationsNewestFirst(items []any) {
	sort.SliceStable(items, func(i, j int) bool {
		left, _ := items[i].(map[string]any)
		right, _ := items[j].(map[string]any)
		leftTime, leftOK := notificationCreatedAt(left)
		rightTime, rightOK := notificationCreatedAt(right)
		switch {
		case leftOK && rightOK && !leftTime.Equal(rightTime):
			return leftTime.After(rightTime)
		case leftOK != rightOK:
			return leftOK
		}
		return strings.TrimSpace(firstNonEmptyStringMap(left, "id")) > strings.TrimSpace(firstNonEmptyStringMap(right, "id"))
	})
}

type notificationReadDiagnostics struct {
	API             time.Duration
	Normalize       time.Duration
	Total           time.Duration
	UpstreamCount   int
	NormalizedCount int
	RawIncluded     bool
}

func attachNotificationReadDiagnostics(payload map[string]any, d notificationReadDiagnostics) {
	if payload == nil {
		return
	}

	metrics := map[string]any{
		"lesserAPIMs":      durationMillis(d.API),
		"normalizationMs":  durationMillis(d.Normalize),
		"marshalMs":        int64(0),
		"totalMs":          durationMillis(d.Total),
		"upstreamCount":    d.UpstreamCount,
		"normalizedCount":  d.NormalizedCount,
		"rawIncluded":      d.RawIncluded,
		"responseBytes":    0,
		"mcpPayloadBytes":  0,
		"instrumentation":  "best_effort",
		"sizeMeasurement":  "json_content_text_bytes; final MCP envelope measured before ToolResult wrapping",
		"diagnosticFields": []string{"lesserAPIMs", "normalizationMs", "marshalMs", "responseBytes", "mcpPayloadBytes"},
	}
	payload["diagnostics"] = metrics

	marshalStartedAt := time.Now()
	if b, err := json.Marshal(payload); err == nil {
		metrics["marshalMs"] = durationMillis(time.Since(marshalStartedAt))
		metrics["responseBytes"] = len(b)
	}
	if b, err := json.Marshal(map[string]any{"content": payload, "structuredContent": map[string]any{"data": payload}}); err == nil {
		metrics["mcpPayloadBytes"] = len(b)
	}
	if b, err := json.Marshal(payload); err == nil {
		metrics["responseBytes"] = len(b)
	}
}

func durationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d.Round(time.Millisecond).Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}

func normalizeSocialNotification(raw map[string]any, includeRaw bool) map[string]any {
	out := map[string]any{
		"id":   strings.TrimSpace(stringFromMap(raw, "id")),
		"type": normalizeSocialNotificationType(raw),
	}
	if includeRaw {
		out["_raw"] = raw
	}

	if createdAt := firstNonEmptyStringMap(raw, "created_at", "createdAt"); createdAt != "" {
		out["createdAt"] = createdAt
	}
	attachSocialNotificationReadState(out, raw)
	if actor := normalizeSocialNotificationActor(firstMap(raw, "account", "actor")); actor != nil {
		out["actor"] = actor
	}
	if post := normalizeSocialNotificationPost(firstMap(raw, "status", "post", "targetPost")); post != nil {
		out["targetPost"] = post
	}
	if comm := normalizeSocialNotificationCommunication(raw); comm != nil {
		out["communication"] = comm
	}

	return out
}

func attachSocialNotificationReadState(out map[string]any, raw map[string]any) {
	if read, ok := firstBoolMap(raw, "read", "is_read", "isRead"); ok {
		out["read"] = read
	}
	if unread, ok := firstBoolMap(raw, "unread", "is_unread", "isUnread"); ok {
		out["unread"] = unread
		if _, hasRead := out["read"]; !hasRead {
			out["read"] = !unread
		}
	}
	if readAt := firstNonEmptyStringMap(raw, "read_at", "readAt"); readAt != "" {
		out["readAt"] = readAt
	}
}

func firstBoolMap(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			v := strings.TrimSpace(typed)
			if strings.EqualFold(v, "true") || v == "1" {
				return true, true
			}
			if strings.EqualFold(v, "false") || v == "0" {
				return false, true
			}
		}
	}
	return false, false
}

func normalizeSocialNotificationType(raw map[string]any) string {
	rawType := strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(raw, "type", "notificationType")))
	switch rawType {
	case "favorite":
		return "favourite"
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
	putIfNotEmpty(out, "content", compactString(firstNonEmptyStringMap(raw, "content", "text"), notificationContentPreviewRunes))
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

func normalizeSocialNotificationCommunication(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	channel := notificationChannel(raw)
	if channel == "" {
		return nil
	}

	out := map[string]any{
		"channel": channel,
	}
	putIfNotEmpty(out, "messageId", commMessageID(raw))
	putIfNotEmpty(out, "subject", commSubject(raw))
	putIfNotEmpty(out, "receivedAt", commReceivedAt(raw))
	if from := compactCommunicationEndpoint(commFrom(raw)); from != nil {
		out["from"] = from
	}
	if to := compactCommunicationAny(commTo(raw)); to != nil {
		out["to"] = to
	}
	if preview := compactString(firstNonEmpty(commPreview(raw), commBody(raw)), notificationCommPreviewRunes); preview != "" {
		out["preview"] = preview
	}
	return out
}

func commPreview(n map[string]any) string {
	for _, key := range []string{"preview", "snippet", "summary"} {
		if v, _ := n[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, container := range []string{"communication", "data", "payload"} {
		if m, ok := n[container].(map[string]any); ok {
			for _, key := range []string{"preview", "snippet", "summary"} {
				if v, _ := m[key].(string); strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	return ""
}

func compactCommunicationEndpoint(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"name", "address", "email", "phone", "number", "identifier", "soulAgentId", "soul_agent_id", "agentId", "agent_id"} {
		putIfNotEmpty(out, key, firstNonEmptyStringMap(raw, key))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactCommunicationAny(raw any) any {
	switch v := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		return compactCommunicationEndpoint(v)
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if compact := compactCommunicationAny(item); compact != nil {
				out = append(out, compact)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return v
	}
}

func compactString(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
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

func notificationCreatedAt(raw map[string]any) (time.Time, bool) {
	value := firstNonEmptyStringMap(raw, "created_at", "createdAt")
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func parseNotificationSince(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func nextNotificationCursorFromHeaders(headers map[string][]string) string {
	for _, link := range headers["Link"] {
		if cursor := nextNotificationCursorFromLink(link); cursor != "" {
			return cursor
		}
	}
	return ""
}

func nextNotificationCursorFromLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) && !strings.Contains(part, `rel=next`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		u, err := url.Parse(part[start+1 : end])
		if err != nil {
			continue
		}
		if cursor := strings.TrimSpace(u.Query().Get("max_id")); cursor != "" {
			return cursor
		}
	}
	return ""
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
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
