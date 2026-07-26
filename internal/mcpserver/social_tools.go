package mcpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/memory"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/oklog/ulid/v2"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	notificationCursorMemoryPrefix       = "notification_cursor:"
	notificationCursorMemoryTag          = "notification_cursor"
	notificationCommunicationInbound     = "communication:inbound"
	notificationActorFilterCursorPrefix  = "actor-filter:v1:"
	notificationReadDefaultLimit         = 30
	notificationReadMaxLimit             = 80
	notificationReadMaxTypes             = 8
	notificationCompactMaxOutputBytes    = 8000
	notificationCompactPreviewRunes      = 24
	notificationContentPreviewRunes      = 500
	notificationCommPreviewRunes         = 240
	conversationReadDefaultLimit         = 20
	conversationReadMaxLimit             = 80
	conversationCompactMaxOutputBytes    = 8000
	conversationCompactPreviewRunes      = 16
	conversationGetDefaultLimit          = 20
	conversationGetMaxLimit              = 80
	conversationGetCompactMaxOutputBytes = 12000
	conversationGetCompactPreviewRunes   = 160
	socialCompactListPreviewRunes        = 48
	timelineCompactMaxOutputBytes        = 6000
	postSearchCompactMaxOutputBytes      = 8000
)

var notificationCursorEventIDs = struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}{
	entropy: ulid.Monotonic(rand.Reader, 0),
}

var notificationSupportedTypes = []string{
	"mention",
	"reply",
	"favourite",
	"favorite",
	"reblog",
	"follow",
	"follow_request",
	"poll",
	"status",
	"update",
	"admin.sign_up",
	"admin.report",
	notificationCommunicationInbound,
}

var notificationUpstreamFilterTypes = map[string]struct{}{
	"mention":                        {},
	"reply":                          {},
	"favourite":                      {},
	"reblog":                         {},
	"follow":                         {},
	"follow_request":                 {},
	"poll":                           {},
	"status":                         {},
	"update":                         {},
	"admin.sign_up":                  {},
	"admin.report":                   {},
	notificationCommunicationInbound: {},
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
		{Def: postGetDef(), Handler: handlePostGet},
		{Def: followersListDef(), Handler: handleFollowersList},
		{Def: followingListDef(), Handler: handleFollowingList},
		{Def: conversationsReadDef(), Handler: handleConversationsRead},
		{Def: conversationGetDef(), Handler: handleConversationGet},
		{Def: directMessagesReadDef(), Handler: handleDirectMessagesRead},
		{Def: messageRequestsListDef(), Handler: handleMessageRequestsList},
		{Def: messageRequestAcceptDef(), Handler: handleMessageRequestAccept},
		{Def: messageRequestDeclineDef(), Handler: handleMessageRequestDecline},
		{Def: notificationsReadDef(), Handler: handleNotificationsRead},
		{Def: notificationGetDef(), Handler: handleNotificationGet},
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
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if err := validateSocialListReadView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

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
	if readParams.View == readViewCompact {
		return socialCompactTimelineResult(in.Timeline, strings.TrimSpace(in.Since), in.Limit, out, readParams)
	}
	return toolJSONResult(out, nil)
}

func handlePostSearch(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if err := validateSocialListReadView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

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
	if readParams.View == readViewCompact {
		return socialCompactPostSearchResult(in.Query, in.Limit, out, readParams)
	}
	return toolJSONResult(out, nil)
}

func validateSocialListReadView(view string) error {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "", readViewStandard, readViewFull, readViewCompact:
		return nil
	default:
		return fmt.Errorf("invalid view (expected compact, standard, or full)")
	}
}

func socialCompactTimelineResult(timeline string, since string, limit int, raw any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected timeline response")
	}

	previewRunes := socialCompactPreviewRunes(params)
	statuses, statusIDs, err := compactSocialStatusRefsFromItems(items, previewRunes)
	if err != nil {
		return nil, err
	}
	budget := socialCompactOutputBudget(params, timelineCompactMaxOutputBytes)
	payload := map[string]any{
		"view":     readViewCompact,
		"timeline": timeline,
		"count":    len(statuses),
		"statuses": statuses,
		"omitted":  socialCompactStatusListOmissions("statuses"),
		"budget":   socialCompactBudgetMetadata(budget, previewRunes),
	}
	if strings.TrimSpace(since) != "" {
		payload["since"] = strings.TrimSpace(since)
	}
	if limit > 0 {
		payload["limit"] = limit
	}

	return socialCompactListToolResult("timeline_read", fmt.Sprintf("%d compact %s timeline statuses", len(statuses), timeline), payload, map[string]any{
		"tool":      "timeline_read",
		"view":      readViewCompact,
		"timeline":  timeline,
		"count":     len(statuses),
		"statusIds": statusIDs,
		"omitted":   socialCompactStatusListTextOmissions("statuses"),
	}, budget)
}

func socialCompactPostSearchResult(query string, limit int, raw any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	search, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected search response")
	}
	statusesRaw, ok := search["statuses"].([]any)
	if !ok && search["statuses"] != nil {
		return nil, fmt.Errorf("unexpected search statuses response")
	}

	previewRunes := socialCompactPreviewRunes(params)
	statuses, statusIDs, err := compactSocialStatusRefsFromItems(statusesRaw, previewRunes)
	if err != nil {
		return nil, err
	}
	budget := socialCompactOutputBudget(params, postSearchCompactMaxOutputBytes)
	payload := map[string]any{
		"view":     readViewCompact,
		"query":    query,
		"count":    len(statuses),
		"statuses": statuses,
		"omitted":  socialCompactStatusListOmissions("statuses"),
		"budget":   socialCompactBudgetMetadata(budget, previewRunes),
	}
	if limit > 0 {
		payload["limit"] = limit
	}
	if accounts := compactSocialAccountRefsFromItems(search["accounts"]); len(accounts) > 0 {
		payload["accounts"] = accounts
	}
	if hashtags := compactSocialHashtagsFromItems(search["hashtags"]); len(hashtags) > 0 {
		payload["hashtags"] = hashtags
	}

	return socialCompactListToolResult("post_search", fmt.Sprintf("%d compact post search results", len(statuses)), payload, map[string]any{
		"tool":      "post_search",
		"view":      readViewCompact,
		"query":     query,
		"count":     len(statuses),
		"statusIds": statusIDs,
		"omitted":   socialCompactStatusListTextOmissions("statuses"),
	}, budget)
}

func compactSocialStatusRefsFromItems(items []any, previewRunes int) ([]any, []string, error) {
	statuses := make([]any, 0, len(items))
	statusIDs := make([]string, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			return nil, nil, fmt.Errorf("unexpected status item")
		}
		ref := compactSocialStatusRefWithPreview(raw, previewRunes)
		if ref == nil {
			return nil, nil, fmt.Errorf("unexpected empty status item")
		}
		statuses = append(statuses, ref)
		if strings.TrimSpace(ref.ID) != "" {
			statusIDs = append(statusIDs, strings.TrimSpace(ref.ID))
		}
	}
	return statuses, statusIDs, nil
}

func compactSocialAccountRefsFromItems(raw any) []any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	accounts := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		ref := compactSocialAccountRef(raw)
		if ref != nil {
			accounts = append(accounts, ref)
		}
	}
	return accounts
}

func compactSocialHashtagsFromItems(raw any) []any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	hashtags := make([]any, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if value := strings.TrimSpace(typed); value != "" {
				hashtags = append(hashtags, value)
			}
		case map[string]any:
			hashtag := map[string]any{}
			putIfNotEmpty(hashtag, "name", firstNonEmptyStringMap(typed, "name", "tag"))
			putIfNotEmpty(hashtag, "url", firstNonEmptyStringMap(typed, "url"))
			if len(hashtag) > 0 {
				hashtags = append(hashtags, hashtag)
			}
		}
	}
	return hashtags
}

func socialCompactPreviewRunes(params sharedReadParams) int {
	if params.PreviewChars > 0 {
		return params.PreviewChars
	}
	return socialCompactListPreviewRunes
}

func socialCompactOutputBudget(params sharedReadParams, defaultBudget int) int {
	if params.MaxOutputBytes > 0 {
		return params.MaxOutputBytes
	}
	return defaultBudget
}

func socialCompactBudgetMetadata(maxOutputBytes int, previewRunes int) map[string]any {
	return map[string]any{
		"maxOutputBytes":      maxOutputBytes,
		"contentPreviewRunes": previewRunes,
		"enforcement":         "response_too_large",
	}
}

func socialCompactStatusListOmissions(statusPath string) []any {
	return []any{
		map[string]any{
			"path":      statusPath + "[].content",
			"reason":    "content_preview",
			"expansion": statusPath + "[].omitted[].expand",
		},
		map[string]any{
			"path":      statusPath + "[].account",
			"reason":    "author_ref_only",
			"expansion": statusPath + "[].expand with post_get(id, view=full)",
		},
	}
}

func socialCompactStatusListTextOmissions(statusPath string) []any {
	return []any{
		map[string]any{
			"path":      statusPath + "[].content",
			"reason":    "content_preview",
			"expansion": "structuredContent.data." + statusPath + "[].omitted[].expand",
		},
		map[string]any{
			"path":      statusPath + "[].account",
			"reason":    "author_ref_only",
			"expansion": "structuredContent.data." + statusPath + "[].expand with view=full",
		},
	}
}

func socialCompactListToolResult(toolName string, summary string, payload map[string]any, text map[string]any, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: summary,
		Data:    payload,
		Text:    text,
	})
	if err != nil {
		return nil, err
	}
	if maxOutputBytes <= 0 {
		return result, nil
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	return toolErrorResult("response_too_large", toolName+" compact response exceeds max_output_bytes", http.StatusRequestEntityTooLarge, map[string]any{
		"tool":                 toolName,
		"view":                 readViewCompact,
		"measuredBytes":        measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":       maxOutputBytes,
		"contentTextBytes":     measurement.ContentTextBytes,
		"structuredBytes":      measurement.StructuredContentBytes,
		"guidance":             "reduce limit or increase max_output_bytes",
		"omittedFieldMetadata": "available under structuredContent.data.omitted for successful compact responses",
	})
}

func handlePostGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		ID   string `json:"id"`
		View string `json:"view,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return nil, invalidParams("missing id")
	}
	view := strings.ToLower(strings.TrimSpace(in.View))
	if view == "" {
		view = readViewStandard
	}
	switch view {
	case readViewStandard, readViewFull:
	default:
		return nil, invalidParams("invalid view (expected standard or full)")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/statuses/"+url.PathEscape(in.ID), nil, token, nil)
	if err != nil {
		return authToolResultFromError(err)
	}
	status, ok := out.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected status response")
	}

	statusID := firstNonEmptyStringMap(status, "id")
	if statusID == "" {
		statusID = in.ID
	}
	payload := map[string]any{
		"id":        statusID,
		"view":      view,
		"source":    "lesser-api",
		"statusRef": compactSocialStatusRef(status),
	}
	if view == readViewFull {
		payload["status"] = status
	} else {
		payload["status"] = socialStatusStandardPayload(status)
	}
	return toolJSONResult(payload, nil)
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
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if err := validateConversationReadView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

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
	includeRaw := (in.IncludeRaw || readParams.View == readViewFull) && readParams.View != readViewCompact
	conversations, err := socialConversationsFromAPI(out, includeRaw)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"conversations": conversations,
		"count":         len(conversations),
		"limit":         limit,
		"includeRaw":    includeRaw,
	}
	if readParams.View == readViewCompact {
		return socialCompactConversationsResult(payload, conversations, readParams)
	}
	return toolJSONResult(payload, nil)
}

func validateConversationReadView(view string) error {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "", readViewStandard, readViewFull, readViewCompact:
		return nil
	default:
		return fmt.Errorf("invalid view (expected compact, standard, or full)")
	}
}

func socialCompactConversationsResult(standardPayload map[string]any, conversations []any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	previewRunes := conversationCompactPreviewRunes
	if params.PreviewChars > 0 {
		previewRunes = params.PreviewChars
	}

	refs := make([]any, 0, len(conversations))
	for _, item := range conversations {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			return nil, fmt.Errorf("unexpected conversation item")
		}
		ref := compactSocialConversationRef(raw, previewRunes)
		if ref == nil {
			continue
		}
		refs = append(refs, ref)
	}

	budget := socialCompactOutputBudget(params, conversationCompactMaxOutputBytes)
	payload := map[string]any{
		"view":          readViewCompact,
		"count":         len(refs),
		"limit":         standardPayload["limit"],
		"conversations": refs,
		"includeRaw":    false,
		"omitted":       compactConversationListOmissions(),
		"budget":        socialCompactBudgetMetadata(budget, previewRunes),
	}
	text := map[string]any{
		"tool":  "conversations_read",
		"view":  readViewCompact,
		"count": len(refs),
	}
	return toolStructuredFirstResultWithBudget("conversations_read", fmt.Sprintf("%d compact conversations", len(refs)), payload, text, nil, false, budget)
}

func handleConversationGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if readParams.View == "" {
		readParams.View = readViewCompact
	}
	if err := validateConversationGetView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

	var in struct {
		ConversationID string `json:"conversationId,omitempty"`
		ID             string `json:"id,omitempty"`
		Limit          int    `json:"limit,omitempty"`
		Cursor         string `json:"cursor,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	conversationID := strings.TrimSpace(in.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(in.ID)
	}
	if conversationID == "" {
		return nil, invalidParams("missing conversationId")
	}
	limit := boundedConversationGetLimit(in.Limit)

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
	if cursor := strings.TrimSpace(in.Cursor); cursor != "" {
		query.Set("max_id", cursor)
	}

	out, headers, err := client.DoJSONWithHeaders(ctx, "GET", "/api/v1/conversations/"+url.PathEscape(conversationID), query, token, nil)
	if err != nil {
		return conversationGetToolResultFromError(conversationID, err)
	}
	raw, err := conversationDetailFromAPI(out)
	if err != nil {
		return nil, err
	}
	includeRaw := readParams.View == readViewFull
	conversation := normalizeSocialConversationDetail(raw, includeRaw)
	if id := strings.TrimSpace(firstNonEmptyStringMap(conversation, "id")); id == "" {
		conversation["id"] = conversationID
	}

	payload := map[string]any{
		"id":           firstNonEmptyStringMap(conversation, "id"),
		"view":         readParams.View,
		"source":       "lesser-api",
		"conversation": conversation,
		"limit":        limit,
	}
	if nextCursor := nextNotificationCursorFromHeaders(headers); nextCursor != "" {
		payload["nextCursor"] = nextCursor
	}
	if readParams.View == readViewCompact {
		return socialCompactConversationGetResult(payload, conversation, readParams)
	}
	return socialConversationGetStructuredResult(payload, readParams)
}

func handleDirectMessagesRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if readParams.View == "" {
		readParams.View = readViewCompact
	}
	if err := validateConversationGetView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

	var in struct {
		Counterpart string `json:"counterpart,omitempty"`
		Limit       int    `json:"limit,omitempty"`
		Cursor      string `json:"cursor,omitempty"`
		UnreadOnly  bool   `json:"unreadOnly,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	counterpart := strings.TrimSpace(in.Counterpart)
	if counterpart == "" {
		return nil, invalidParams("missing counterpart")
	}
	limit := boundedConversationGetLimit(in.Limit)

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("counterpart", counterpart)
	query.Set("limit", strconv.Itoa(limit))
	if cursor := strings.TrimSpace(in.Cursor); cursor != "" {
		query.Set("max_id", cursor)
	}

	out, headers, err := client.DoJSONWithHeaders(ctx, "GET", "/api/v1/conversations/lookup", query, token, nil)
	if err != nil {
		return directMessagesReadToolResultFromError(counterpart, err)
	}
	raw, err := conversationDetailFromAPI(out)
	if err != nil {
		return nil, err
	}
	includeRaw := readParams.View == readViewFull
	conversation := normalizeSocialConversationDetail(raw, includeRaw)
	if in.UnreadOnly {
		if unread, ok := conversationUnreadFlag(conversation); !ok || !unread {
			conversation = directMessagesConversationWithoutBodies(conversation)
		}
	}

	payload := map[string]any{
		"counterpart":  counterpart,
		"id":           firstNonEmptyStringMap(conversation, "id"),
		"view":         readParams.View,
		"source":       "lesser-api",
		"conversation": conversation,
		"limit":        limit,
		"unreadOnly":   in.UnreadOnly,
	}
	if nextCursor := nextNotificationCursorFromHeaders(headers); nextCursor != "" {
		payload["nextCursor"] = nextCursor
	}
	if unread, ok := conversationUnreadFlag(conversation); ok {
		payload["unread"] = unread
	}
	if messages := conversationMessagesFromMap(conversation); len(messages) > 0 {
		payload["messages"] = messages
		payload["count"] = len(messages)
	} else {
		payload["messages"] = []any{}
		payload["count"] = 0
	}
	if in.UnreadOnly {
		unread, ok := conversationUnreadFlag(conversation)
		payload["unreadOnlyMatched"] = ok && unread
	}

	if readParams.View == readViewCompact {
		return socialCompactDirectMessagesReadResult(payload, conversation, readParams)
	}
	return socialDirectMessagesReadStructuredResult(payload, readParams)
}

func validateConversationGetView(view string) error {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case readViewStandard, readViewFull, readViewCompact:
		return nil
	default:
		return fmt.Errorf("invalid view (expected compact, standard, or full)")
	}
}

func socialConversationGetStructuredResult(payload map[string]any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	view := strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(payload, "view")))
	if view == "" {
		view = readViewStandard
	}
	budget := params.MaxOutputBytes
	text := map[string]any{
		"tool": "conversation_get",
		"view": view,
		"id":   firstNonEmptyStringMap(payload, "id"),
	}
	return toolStructuredFirstResultWithBudget("conversation_get", "conversation details", payload, text, nil, false, budget)
}

func socialCompactConversationGetResult(standardPayload map[string]any, conversation map[string]any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	previewRunes := conversationGetCompactPreviewRunes
	if params.PreviewChars > 0 {
		previewRunes = params.PreviewChars
	}

	ref := compactSocialConversationDetailRef(conversation, previewRunes)
	if ref == nil {
		ref = map[string]any{}
	}
	id := firstNonEmptyStringMap(ref, "id")
	if id == "" {
		id = firstNonEmptyStringMap(standardPayload, "id")
		putIfNotEmpty(ref, "id", id)
	}

	budget := socialCompactOutputBudget(params, conversationGetCompactMaxOutputBytes)
	payload := map[string]any{
		"id":           id,
		"view":         readViewCompact,
		"source":       standardPayload["source"],
		"conversation": ref,
		"limit":        standardPayload["limit"],
		"includeRaw":   false,
		"omitted":      compactConversationGetOmissions(),
		"budget":       socialCompactBudgetMetadata(budget, previewRunes),
	}
	if nextCursor, _ := standardPayload["nextCursor"].(string); strings.TrimSpace(nextCursor) != "" {
		payload["nextCursor"] = strings.TrimSpace(nextCursor)
	}
	text := map[string]any{
		"tool": "conversation_get",
		"view": readViewCompact,
		"id":   id,
	}
	messageCount := 0
	if refs, _ := ref["messageRefs"].([]any); len(refs) > 0 {
		messageCount = len(refs)
	}
	return toolStructuredFirstResultWithBudget("conversation_get", fmt.Sprintf("conversation %s with %d compact message previews", id, messageCount), payload, text, nil, false, budget)
}

func socialDirectMessagesReadStructuredResult(payload map[string]any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	view := strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(payload, "view")))
	if view == "" {
		view = readViewStandard
	}
	budget := params.MaxOutputBytes
	text := map[string]any{
		"tool":        "direct_messages_read",
		"view":        view,
		"counterpart": firstNonEmptyStringMap(payload, "counterpart"),
		"id":          firstNonEmptyStringMap(payload, "id"),
		"count":       payload["count"],
	}
	return toolStructuredFirstResultWithBudget("direct_messages_read", "direct message conversation details", payload, text, nil, false, budget)
}

func socialCompactDirectMessagesReadResult(standardPayload map[string]any, conversation map[string]any, params sharedReadParams) (*mcpruntime.ToolResult, error) {
	previewRunes := conversationGetCompactPreviewRunes
	if params.PreviewChars > 0 {
		previewRunes = params.PreviewChars
	}

	ref := compactSocialConversationDetailRef(conversation, previewRunes)
	if ref == nil {
		ref = map[string]any{}
	}
	id := firstNonEmptyStringMap(ref, "id")
	if id == "" {
		id = firstNonEmptyStringMap(standardPayload, "id")
		putIfNotEmpty(ref, "id", id)
	}

	messageRefs := []any{}
	if refs, _ := ref["messageRefs"].([]any); len(refs) > 0 {
		messageRefs = refs
	}

	budget := socialCompactOutputBudget(params, conversationGetCompactMaxOutputBytes)
	payload := map[string]any{
		"counterpart":  firstNonEmptyStringMap(standardPayload, "counterpart"),
		"id":           id,
		"view":         readViewCompact,
		"source":       standardPayload["source"],
		"conversation": ref,
		"messages":     messageRefs,
		"count":        len(messageRefs),
		"limit":        standardPayload["limit"],
		"includeRaw":   false,
		"unreadOnly":   standardPayload["unreadOnly"],
		"omitted":      compactDirectMessagesReadOmissions(),
		"budget":       socialCompactBudgetMetadata(budget, previewRunes),
	}
	if unread, ok := standardPayload["unread"].(bool); ok {
		payload["unread"] = unread
	}
	if matched, ok := standardPayload["unreadOnlyMatched"].(bool); ok {
		payload["unreadOnlyMatched"] = matched
	}
	if nextCursor, _ := standardPayload["nextCursor"].(string); strings.TrimSpace(nextCursor) != "" {
		payload["nextCursor"] = strings.TrimSpace(nextCursor)
	}
	text := map[string]any{
		"tool":        "direct_messages_read",
		"view":        readViewCompact,
		"counterpart": payload["counterpart"],
		"id":          id,
		"count":       len(messageRefs),
	}
	return toolStructuredFirstResultWithBudget("direct_messages_read", fmt.Sprintf("direct messages with %s: %d compact message previews", payload["counterpart"], len(messageRefs)), payload, text, nil, false, budget)
}

func conversationGetToolResultFromError(conversationID string, err error) (*mcpruntime.ToolResult, error) {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return toolErrorResult("not_found", "conversation not found", http.StatusNotFound, map[string]any{
			"conversationId": strings.TrimSpace(conversationID),
			"source":         "lesser-api",
			"upstreamCode":   apiErr.Status,
		})
	}
	return authToolResultFromError(err)
}

func directMessagesReadToolResultFromError(counterpart string, err error) (*mcpruntime.ToolResult, error) {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return toolErrorResult("not_found", "direct message conversation not found", http.StatusNotFound, map[string]any{
			"counterpart":  strings.TrimSpace(counterpart),
			"source":       "lesser-api",
			"upstreamCode": apiErr.Status,
			"suggestedFallbacks": []any{
				"confirm the teammate local id, acct, or actor URL with identity_lookup",
				"ask the teammate to start a direct-message thread",
				"use email_search only as a fallback coordination path",
			},
		})
	}
	return authToolResultFromError(err)
}

func compactSocialConversationRef(raw map[string]any, previewRunes int) map[string]any {
	if raw == nil {
		return nil
	}
	if previewRunes <= 0 {
		previewRunes = conversationCompactPreviewRunes
	}

	id := strings.TrimSpace(firstNonEmptyStringMap(raw, "id"))
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", id)
	if id != "" {
		ref["expand"] = socialConversationGetExpansion(id, readViewCompact, "structuredContent.data.conversation")
	}
	if unread, ok := firstBoolMap(raw, "unread", "is_unread", "isUnread"); ok {
		ref["unread"] = unread
		ref["read"] = !unread
	}
	if read, ok := firstBoolMap(raw, "read", "is_read", "isRead"); ok {
		ref["read"] = read
		if _, hasUnread := ref["unread"]; !hasUnread {
			ref["unread"] = !read
		}
	}
	putIfNotEmpty(ref, "updatedAt", firstNonEmptyStringMap(raw, "updatedAt", "updated_at"))
	if participants := compactConversationParticipantRefs(raw["participants"]); len(participants) > 0 {
		ref["participantRefs"] = participants
	}
	if post := firstMap(raw, "lastPost", "last_status", "lastStatus"); post != nil {
		if postRef := compactConversationLastPostRef(post, previewRunes); postRef != nil {
			ref["lastPostRef"] = postRef
		}
	}
	missing := missingSocialRefFields(map[string]string{
		"id":              id,
		"updatedAt":       firstNonEmptyStringMap(raw, "updatedAt", "updated_at"),
		"participantRefs": stableConversationRefValue(ref["participantRefs"]),
		"lastPostRef":     stableConversationRefValue(ref["lastPostRef"]),
	})
	if _, hasRead := ref["read"]; !hasRead {
		missing = append(missing, "read")
	}
	if len(missing) > 0 {
		ref["missingFields"] = missing
	}
	if len(ref) == 0 {
		return nil
	}
	return ref
}

func compactSocialConversationDetailRef(raw map[string]any, previewRunes int) map[string]any {
	ref := compactSocialConversationRef(raw, previewRunes)
	if ref == nil {
		ref = map[string]any{}
	}
	if messages := compactConversationMessageRefs(conversationMessagesFromMap(raw), previewRunes); len(messages) > 0 {
		ref["messageRefs"] = messages
		ref["messageCount"] = len(messages)
	}
	if len(ref) == 0 {
		return nil
	}
	return ref
}

func compactConversationMessageRefs(messages []any, previewRunes int) []any {
	if len(messages) == 0 {
		return nil
	}
	refs := make([]any, 0, len(messages))
	for _, item := range messages {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		ref := compactSocialStatusRefWithPreview(raw, previewRunes)
		if ref == nil {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

func conversationMessagesFromMap(raw map[string]any) []any {
	if raw == nil {
		return nil
	}
	for _, key := range []string{"messages", "statuses", "items"} {
		if messages, ok := raw[key].([]any); ok {
			return messages
		}
	}
	return nil
}

func compactConversationParticipantRefs(raw any) []any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	refs := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		if ref := compactConversationParticipantRef(raw); ref != nil {
			refs = append(refs, ref)
		}
	}
	return refs
}

func compactConversationParticipantRef(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	id := firstNonEmptyStringMap(raw, "id")
	acct := firstNonEmptyStringMap(raw, "acct")
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", id)
	putIfNotEmpty(ref, "acct", acct)
	missing := missingSocialRefFields(map[string]string{
		"id":   id,
		"acct": acct,
	})
	if len(missing) > 0 {
		ref["missingFields"] = missing
	}
	if len(ref) == 0 {
		return nil
	}
	return ref
}

func compactConversationLastPostRef(raw map[string]any, previewRunes int) map[string]any {
	if raw == nil {
		return nil
	}
	if previewRunes <= 0 {
		previewRunes = conversationCompactPreviewRunes
	}
	id := firstNonEmptyStringMap(raw, "id")
	content := rawSocialStatusContent(raw)
	preview, truncated := compactStringWithTruncation(content, previewRunes)
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", id)
	putIfNotEmpty(ref, "createdAt", firstNonEmptyStringMap(raw, "createdAt", "created_at"))
	putIfNotEmpty(ref, "visibility", firstNonEmptyStringMap(raw, "visibility"))
	putIfNotEmpty(ref, "contentPreview", preview)
	ref["contentTruncated"] = truncated
	if id != "" {
		ref["expand"] = socialPostGetExpansion(id, readViewStandard, "structuredContent.data.status")
	} else if content != "" {
		ref["missingFields"] = []string{"id"}
		ref["omitted"] = []map[string]any{{
			"path":   "content",
			"reason": "missing_post_id_for_expansion",
		}}
	}
	if len(ref) == 1 {
		return nil
	}
	return ref
}

func stableConversationRefValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return "present"
	case map[string]any:
		if len(typed) == 0 {
			return ""
		}
		return "present"
	default:
		return "present"
	}
}

func compactConversationListOmissions() []any {
	return []any{
		map[string]any{
			"path":   "conversations[].participants",
			"reason": "participant_ref_only",
		},
		map[string]any{
			"path":      "conversations[].lastPost.content",
			"reason":    "last_post_preview",
			"expansion": "structuredContent.data.conversations[].lastPostRef.expand when lastPostRef.id is present",
		},
		map[string]any{
			"path":      "conversations[].messages",
			"reason":    "conversation_get_required",
			"expansion": "structuredContent.data.conversations[].expand",
		},
		map[string]any{
			"path":      "conversations[]._raw",
			"reason":    "debug_payload",
			"expansion": "call conversations_read with view=full or include_raw=true",
		},
	}
}

func compactConversationGetOmissions() []any {
	return []any{
		map[string]any{
			"path":   "conversation.participants",
			"reason": "participant_ref_only",
		},
		map[string]any{
			"path":      "conversation.messageRefs[].content",
			"reason":    "message_content_preview",
			"expansion": "structuredContent.data.conversation.messageRefs[].expand when messageRefs[].id is present",
		},
		map[string]any{
			"path":      "conversation._raw",
			"reason":    "debug_payload",
			"expansion": "call conversation_get with view=full",
		},
	}
}

func compactDirectMessagesReadOmissions() []any {
	return []any{
		map[string]any{
			"path":   "conversation.participants",
			"reason": "participant_ref_only",
		},
		map[string]any{
			"path":      "messages[].content",
			"reason":    "message_content_preview",
			"expansion": "structuredContent.data.messages[].expand when messages[].id is present, or call conversation_get with view=standard/full",
		},
		map[string]any{
			"path":      "conversation._raw",
			"reason":    "debug_payload",
			"expansion": "call direct_messages_read with view=full",
		},
	}
}

func conversationUnreadFlag(raw map[string]any) (bool, bool) {
	if raw == nil {
		return false, false
	}
	return firstBoolMap(raw, "unread", "is_unread", "isUnread")
}

func directMessagesConversationWithoutBodies(raw map[string]any) map[string]any {
	out := cloneStringAnyMap(raw)
	delete(out, "messages")
	delete(out, "lastPost")
	delete(out, "_raw")
	return out
}

func handleNotificationsRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	readParams, err := parseSharedReadParams(args)
	if err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if err := validateNotificationReadView(readParams.View); err != nil {
		return nil, invalidParams(err.Error())
	}

	var in struct {
		Types              []string `json:"types,omitempty"`
		Actor              string   `json:"actor,omitempty"`
		Since              *string  `json:"since"`
		Cursor             string   `json:"cursor,omitempty"`
		Limit              int      `json:"limit,omitempty"`
		IncludeRaw         bool     `json:"include_raw,omitempty"`
		IncludeDiagnostics bool     `json:"include_diagnostics,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	startedAt := time.Now()
	requestedTypes, upstreamTypes, err := normalizeRequestedNotificationTypes(in.Types)
	if err != nil {
		return nil, err
	}
	if !runtimeAllowsCommunicationNotifications(ctx) && notificationTypesIncludeCommunication(requestedTypes) {
		return toolErrorResult("runtime_boundary", "communication notification types are unavailable to this runtime profile", 403, map[string]any{
			"surface":               "notifications_read.types",
			"type":                  notificationCommunicationInbound,
			"profile":               runtimeProfileName(ctx),
			"communicationsEnabled": false,
		})
	}
	limit := boundedNotificationReadLimit(in.Limit)
	actorFilter := newNotificationActorFilter(in.Actor)
	fetchLimit := limit
	if actorFilter.Active {
		fetchLimit = notificationActorFilterFetchLimit(limit)
	}
	explicitSince := in.Since != nil
	requestedSince := trimDeref(in.Since)
	effectiveSince := requestedSince
	effectiveCursor := strings.TrimSpace(in.Cursor)
	sinceTime, hasTemporalSince := parseNotificationSince(requestedSince)

	if explicitSince && requestedSince != "" && !hasTemporalSince && effectiveCursor == "" {
		effectiveCursor = requestedSince
	}
	actorCursorOffset := 0
	if actorFilter.Active {
		parsedCursor, parsedOffset, err := parseNotificationActorFilterCursor(effectiveCursor, actorFilter)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		effectiveCursor = parsedCursor
		actorCursorOffset = parsedOffset
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
	list, nextCursor, err := readSocialNotifications(ctx, client, token, upstreamTypes, fetchLimit, effectiveCursor)
	apiDuration := time.Since(apiStartedAt)
	if err != nil {
		return authToolResultFromError(err)
	}

	normalizeStartedAt := time.Now()
	if !runtimeAllowsCommunicationNotifications(ctx) {
		list = filterRawCommunicationNotifications(list)
	}
	includeRaw := in.IncludeRaw || readParams.View == readViewFull
	notifications := socialNotificationsFromAPI(list, includeRaw)
	if hasTemporalSince {
		notifications = filterSocialNotificationsAfter(notifications, sinceTime)
	}
	if len(requestedTypes) > 0 {
		notifications = filterSocialNotificationsByType(notifications, requestedTypes)
	}
	actorMatchedCount := 0
	actorSameWindowCursor := ""
	if actorFilter.Active {
		notifications = filterSocialNotificationsByActor(notifications, actorFilter)
		actorMatchedCount = len(notifications)
	}
	sortSocialNotificationsNewestFirst(notifications)
	if actorFilter.Active {
		if actorCursorOffset > len(notifications) {
			notifications = nil
		} else {
			notifications = notifications[actorCursorOffset:]
		}
		if len(notifications) > limit {
			if cursor := notificationActorFilterCursor(actorFilter, effectiveCursor, actorCursorOffset+limit); cursor != "" {
				actorSameWindowCursor = cursor
			}
			notifications = notifications[:limit]
		}
	} else if len(notifications) > limit {
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
		"includeRaw":    includeRaw,
	}
	if actorFilter.Active {
		if actorSameWindowCursor != "" {
			payload["nextCursor"] = actorSameWindowCursor
		}
		payload["filter"] = notificationActorFilterMetadata(actorFilter, fetchLimit, limit, len(list), actorMatchedCount, len(notifications), actorCursorOffset, actorSameWindowCursor != "")
	}
	if in.IncludeDiagnostics {
		attachNotificationReadDiagnostics(payload, notificationReadDiagnostics{
			API:             apiDuration,
			Normalize:       normalizeDuration,
			Total:           time.Since(startedAt),
			UpstreamCount:   len(list),
			NormalizedCount: len(notifications),
			RawIncluded:     includeRaw,
		})
	}
	if readParams.View == readViewCompact {
		return socialCompactNotificationsResult(payload, notifications, readParams, in.IncludeDiagnostics)
	}
	return toolJSONResult(payload, nil)
}

func handleNotificationGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		ID   string `json:"id"`
		View string `json:"view,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return nil, invalidParams("missing id")
	}
	view := strings.ToLower(strings.TrimSpace(in.View))
	if view == "" {
		view = readViewStandard
	}
	switch view {
	case readViewStandard, readViewFull:
	default:
		return nil, invalidParams("invalid view (expected standard or full)")
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.DoJSON(ctx, "GET", "/api/v1/notifications/"+url.PathEscape(in.ID), nil, token, nil)
	if err != nil {
		return authToolResultFromError(err)
	}
	notification, ok := out.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected notification response")
	}
	if !runtimeAllowsCommunicationNotifications(ctx) && isRawCommunicationNotification(notification) {
		return toolErrorResult("runtime_boundary", "communication notification is unavailable to this runtime profile", 403, map[string]any{
			"surface":               "notification_get",
			"id":                    in.ID,
			"profile":               runtimeProfileName(ctx),
			"communicationsEnabled": false,
		})
	}

	notificationID := firstNonEmptyStringMap(notification, "id")
	if notificationID == "" {
		notificationID = in.ID
	}
	payload := map[string]any{
		"id":              notificationID,
		"view":            view,
		"source":          "lesser-api",
		"notificationRef": compactSocialNotificationRef(normalizeSocialNotification(notification, false), notificationCompactPreviewRunes),
	}
	if view == readViewFull {
		payload["notification"] = notification
	} else {
		payload["notification"] = normalizeSocialNotification(notification, false)
	}
	return toolJSONResult(payload, nil)
}

func validateNotificationReadView(view string) error {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "", readViewStandard, readViewFull, readViewCompact:
		return nil
	default:
		return fmt.Errorf("invalid view (expected compact, standard, or full)")
	}
}

func socialCompactNotificationsResult(standardPayload map[string]any, notifications []any, params sharedReadParams, includeDiagnostics bool) (*mcpruntime.ToolResult, error) {
	previewRunes := notificationCompactPreviewRunes
	if params.PreviewChars > 0 {
		previewRunes = params.PreviewChars
	}

	refs := make([]any, 0, len(notifications))
	for _, item := range notifications {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			return nil, fmt.Errorf("unexpected notification item")
		}
		ref := compactSocialNotificationRef(raw, previewRunes)
		if ref == nil {
			continue
		}
		refs = append(refs, ref)
	}

	budget := socialCompactOutputBudget(params, notificationCompactMaxOutputBytes)
	payload := map[string]any{
		"view":          readViewCompact,
		"since":         standardPayload["since"],
		"cursor":        standardPayload["cursor"],
		"nextSince":     standardPayload["nextSince"],
		"nextCursor":    standardPayload["nextCursor"],
		"count":         len(refs),
		"types":         standardPayload["types"],
		"notifications": refs,
		"includeRaw":    false,
		"omitted":       compactNotificationListOmissions(),
		"budget": map[string]any{
			"maxOutputBytes":      budget,
			"contentPreviewRunes": previewRunes,
			"enforcement":         "response_too_large",
		},
	}
	if filter, ok := standardPayload["filter"].(map[string]any); ok {
		payload["filter"] = filter
	}

	diagnostics := map[string]any(nil)
	if includeDiagnostics {
		if d, ok := standardPayload["diagnostics"].(map[string]any); ok {
			diagnostics = d
		}
	}
	text := map[string]any{
		"tool":  "notifications_read",
		"view":  readViewCompact,
		"count": len(refs),
	}
	return toolStructuredFirstResultWithBudget("notifications_read", fmt.Sprintf("%d compact notifications", len(refs)), payload, text, diagnostics, includeDiagnostics, budget)
}

func compactSocialNotificationRef(raw map[string]any, previewRunes int) map[string]any {
	if raw == nil {
		return nil
	}
	if previewRunes <= 0 {
		previewRunes = notificationCompactPreviewRunes
	}
	id := strings.TrimSpace(firstNonEmptyStringMap(raw, "id"))
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", id)
	putIfNotEmpty(ref, "type", normalizeSocialNotificationType(raw))
	putIfNotEmpty(ref, "createdAt", firstNonEmptyStringMap(raw, "createdAt", "created_at"))
	attachSocialNotificationReadState(ref, raw)
	if actor := compactSocialAccountRef(firstMap(raw, "actor", "account")); actor != nil {
		ref["actorRef"] = actor
	}
	if post := firstMap(raw, "targetPost", "status", "post"); post != nil {
		if statusRef := compactNotificationStatusRef(post, previewRunes); statusRef != nil {
			ref["targetPostRef"] = statusRef
		}
	}
	if communication := compactSocialNotificationCommunicationRef(raw); communication != nil {
		ref["communication"] = communication
	}
	missing := missingSocialRefFields(map[string]string{
		"id":        id,
		"createdAt": firstNonEmptyStringMap(raw, "createdAt", "created_at"),
	})
	if typ := strings.TrimSpace(normalizeSocialNotificationType(raw)); typ == "" {
		missing = append(missing, "type")
	}
	if len(missing) > 0 {
		ref["missingFields"] = missing
	}
	if id != "" {
		ref["expand"] = socialNotificationGetExpansion(id, readViewStandard, "structuredContent.data.notification")
	}
	if len(ref) == 0 {
		return nil
	}
	return ref
}

func compactNotificationStatusRef(raw map[string]any, previewRunes int) map[string]any {
	if raw == nil {
		return nil
	}
	if previewRunes <= 0 {
		previewRunes = notificationCompactPreviewRunes
	}
	id := firstNonEmptyStringMap(raw, "id")
	content := rawSocialStatusContent(raw)
	preview, truncated := compactStringWithTruncation(content, previewRunes)
	ref := map[string]any{}
	putIfNotEmpty(ref, "id", id)
	putIfNotEmpty(ref, "url", firstNonEmptyStringMap(raw, "url", "uri"))
	putIfNotEmpty(ref, "createdAt", firstNonEmptyStringMap(raw, "created_at", "createdAt"))
	putIfNotEmpty(ref, "visibility", firstNonEmptyStringMap(raw, "visibility"))
	putIfNotEmpty(ref, "contentPreview", preview)
	ref["contentTruncated"] = truncated
	if expandID := compactNotificationStatusPostGetID(raw); expandID != "" {
		ref["expand"] = socialPostGetExpansion(expandID, readViewStandard, "structuredContent.data.status")
	}
	if len(ref) == 1 {
		return nil
	}
	return ref
}

func compactNotificationStatusPostGetID(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	for _, key := range []string{
		"statusId",
		"statusID",
		"status_id",
		"canonicalStatusId",
		"canonicalStatusID",
		"canonical_status_id",
		"lookupId",
		"lookupID",
		"lookup_id",
	} {
		if id := strings.TrimSpace(stringFromMap(raw, key)); id != "" {
			return id
		}
	}
	id := strings.TrimSpace(firstNonEmptyStringMap(raw, "id"))
	if notificationTargetStatusIDLooksGenerated(id) {
		return ""
	}
	return id
}

func notificationTargetStatusIDLooksGenerated(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 12 {
		return false
	}
	for _, ch := range id {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func compactSocialNotificationCommunicationRef(raw map[string]any) map[string]any {
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
	if preview := compactString(firstNonEmpty(commPreview(raw), commBody(raw)), notificationCommPreviewRunes); preview != "" {
		out["preview"] = preview
	}
	return out
}

func socialNotificationGetExpansion(id string, view string, resultPath string) *SocialExpansionRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	view = strings.ToLower(strings.TrimSpace(view))
	if view == "" {
		view = readViewStandard
	}
	return &SocialExpansionRef{
		Tool: "notification_get",
		Arguments: map[string]any{
			"id":   id,
			"view": view,
		},
		ResultPath: strings.TrimSpace(resultPath),
	}
}

func compactNotificationListOmissions() []any {
	return []any{
		map[string]any{
			"path":      "notifications[].notification",
			"reason":    "notification_ref_only",
			"expansion": "structuredContent.data.notifications[].expand",
		},
		map[string]any{
			"path":                  "notifications[].raw",
			"reason":                "debug_payload",
			"expansionTool":         "notification_get",
			"expansionArgsTemplate": map[string]any{"id": "structuredContent.data.notifications[].id", "view": readViewFull},
			"resultPath":            "structuredContent.data.notification",
		},
		map[string]any{
			"path":      "notifications[].targetPost.content",
			"reason":    "target_post_preview",
			"expansion": "structuredContent.data.notifications[].targetPostRef.expand when present; otherwise structuredContent.data.notifications[].expand",
		},
	}
}

func toolStructuredFirstResultWithBudget(toolName string, summary string, payload map[string]any, text map[string]any, diagnostics map[string]any, includeDiagnostics bool, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary:            summary,
		Data:               payload,
		Text:               text,
		Diagnostics:        diagnostics,
		IncludeDiagnostics: includeDiagnostics,
	})
	if err != nil {
		return nil, err
	}
	if maxOutputBytes <= 0 {
		return result, nil
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	return toolErrorResult("response_too_large", toolName+" compact response exceeds max_output_bytes", http.StatusRequestEntityTooLarge, map[string]any{
		"tool":                 toolName,
		"view":                 readViewCompact,
		"measuredBytes":        measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":       maxOutputBytes,
		"contentTextBytes":     measurement.ContentTextBytes,
		"structuredBytes":      measurement.StructuredContentBytes,
		"guidance":             "reduce limit or increase max_output_bytes",
		"omittedFieldMetadata": "available under structuredContent.data.omitted for successful compact responses",
	})
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
	// Lesser #1221 confirms the canonical profile PATCH contract treats
	// locked/bot/discoverable as optional patch fields and preserves existing
	// flags when they are omitted. Body intentionally forwards only the fields
	// represented in this tool schema rather than inventing local profile state.

	out, err := client.DoJSON(ctx, "PATCH", "/api/v1/accounts/update_credentials", nil, token, body)
	if err != nil {
		return profileUpdateToolResultFromError("profile_update", err)
	}
	return toolJSONResult(out, nil)
}

func profileUpdateAPIErrorSummary(body []byte) (string, map[string]any) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return "Lesser profile update request failed", nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "Lesser profile update request failed", map[string]any{"bodyBytes": len([]byte(raw))}
	}
	safe := map[string]any{}
	for _, key := range []string{"error", "error_description", "message", "code"} {
		if value := extractString(parsed, key); value != "" {
			safe[key] = value
		}
	}
	if status, ok := parsed["status"].(float64); ok {
		safe["status"] = status
	}
	message := extractString(parsed, "error_description")
	if message == "" {
		message = extractString(parsed, "message")
	}
	if message == "" {
		message = extractString(parsed, "error")
	}
	if message == "" {
		message = "Lesser profile update request failed"
	}
	return message, safe
}

func profileUpdateToolResultFromError(stage string, err error) (*mcpruntime.ToolResult, error) {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		message, safeAPIError := profileUpdateAPIErrorSummary(apiErr.Body)
		if strings.TrimSpace(message) == "" {
			message = "Lesser profile update request failed"
		}
		details := map[string]any{
			"source":       "lesser_profile_update",
			"stage":        strings.TrimSpace(stage),
			"upstreamCode": apiErr.Status,
		}
		if len(safeAPIError) > 0 {
			details["apiError"] = safeAPIError
		}
		return toolErrorResult("lesser_profile_update_http_error", message, apiErr.Status, details)
	}
	return toolErrorResult("lesser_profile_update_error", "Lesser profile update request failed", 0, map[string]any{
		"source": "lesser_profile_update",
		"stage":  strings.TrimSpace(stage),
	})
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
				"limit":{"type":"integer","minimum":1,"maximum":200},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Omitted/standard/full preserve the current upstream-shaped response; compact returns bounded StatusRef entries with post_get expansion metadata."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional compact MCP response budget. Compact responses that exceed the budget return response_too_large instead of silently dropping fields."}
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
				"limit":{"type":"integer","minimum":1,"maximum":200},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Omitted/standard/full preserve the current upstream-shaped response; compact returns bounded StatusRef entries with post_get expansion metadata."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional compact MCP response budget. Compact responses that exceed the budget return response_too_large instead of silently dropping fields."}
			},
			"required":["query"]
		}`),
	}
}

func postGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "post_get",
		Description:  "Expand a compact social status reference by reading a post through Lesser.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: postGetOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Stable Lesser status/post id from a StatusRef."},
				"view":{"type":"string","enum":["standard","full"],"description":"standard returns normalized status fields; full returns the upstream Lesser status payload."}
			},
			"required":["id"]
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
		Description: "Read recent social notifications with normalized actor/source and target post data. Optional actor filtering is MCP-side and bounded; use direct_messages_read as the primary DM retrieval path.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"types":{"type":"array","maxItems":8,"description":"Optional normalized notification types to return. Supported values include communication:inbound for host-backed inbound email/SMS/voice notifications; favorite is accepted as an alias for favourite. At most 8 values may be supplied; duplicates still count against the request budget.","items":{"type":"string","enum":["mention","reply","favourite","favorite","reblog","follow","follow_request","poll","status","update","admin.sign_up","admin.report","communication:inbound"]}},
				"actor":{"type":"string","description":"Optional MCP-side actor/source filter. Matches normalized social actor id, username/local id, acct, or actor URL; communication notifications match available sender soul agent id, agent id, email/address, or identifier metadata. Body over-fetches bounded notification pages from Lesser and declares the strategy in the response."},
				"since":{"type":"string"},
				"cursor":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"include_raw":{"type":"boolean","description":"Include verbose upstream notification payloads under _raw for audit/debug use. Defaults to false and increases response size."},
				"include_diagnostics":{"type":"boolean","description":"Include timing and response-size diagnostics for Ops probes. Defaults to false."},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Omitted/standard preserve the current normalized response; full includes upstream _raw payloads; compact returns bounded notification refs with notification_get expansion metadata and conditional post_get expansion metadata only for directly resolvable target posts."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact content preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional compact MCP response budget. Compact responses that exceed the budget return response_too_large instead of silently dropping fields."}
			}
		}`),
	}
}

func notificationGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "notification_get",
		Description:  "Expand a compact notification reference by reading a notification through Lesser.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: notificationGetOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"Stable Lesser notification id from a compact notification ref."},
				"view":{"type":"string","enum":["standard","full"],"description":"standard returns normalized notification fields; full returns the upstream Lesser notification payload."}
			},
			"required":["id"]
		}`),
	}
}

func conversationsReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "conversations_read",
		Description: "Read direct message conversation summaries. Use compact for bounded refs that expand through conversation_get; include_raw/full only for audit/debug.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80},
				"include_raw":{"type":"boolean","description":"Include verbose upstream conversation payloads under _raw. Defaults to false."},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Omitted/standard preserve the current normalized response; full includes upstream _raw payloads; compact returns bounded conversation refs with conversation_get expansion metadata."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact last-post preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional compact MCP response budget. Compact responses that exceed the budget return response_too_large instead of silently dropping fields."}
			}
		}`),
	}
}

func conversationGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "conversation_get",
		Description:  "Expand a specific direct-message conversation into bounded recent message previews. Defaults to compact; use standard/full only when message content or raw Lesser payloads are explicitly needed.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: conversationGetOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"conversationId":{"type":"string","description":"Stable Lesser conversation id from conversations_read compact expansion metadata."},
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Maximum recent messages to return. Defaults to 20."},
				"cursor":{"type":"string","description":"Optional pagination cursor; forwarded to Lesser as max_id."},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Defaults to compact previews. standard includes normalized recent message content; full also includes the upstream Lesser payload under _raw."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact message preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to 12000 bytes and return response_too_large instead of silently dropping fields."}
			},
			"required":["conversationId"]
		}`),
	}
}

func directMessagesReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "direct_messages_read",
		Description:  "Read recent direct-message previews from a named counterpart using Lesser's one-to-one conversation lookup. Examples: recent DMs from Ops with {\"counterpart\":\"ops\",\"limit\":10,\"view\":\"compact\"}; unread DMs from Medic with {\"counterpart\":\"medic\",\"unreadOnly\":true,\"view\":\"compact\"}. Defaults to compact and returns explicit not_found rather than scanning unrelated surfaces.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: directMessagesReadOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"counterpart":{"type":"string","description":"Named counterpart to resolve via Lesser lookup. Accepts local username, acct, or actor URL where Lesser supports it."},
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Maximum recent messages to return. Defaults to 20."},
				"cursor":{"type":"string","description":"Optional pagination cursor; forwarded to Lesser as max_id."},
				"unreadOnly":{"type":"boolean","description":"When true, return previews only if the matched one-to-one conversation is currently unread; read conversations return zero message previews."},
				"view":{"type":"string","enum":["compact","standard","full"],"description":"Optional projection. Defaults to compact previews. standard includes normalized recent message content; full also includes the upstream Lesser payload under _raw."},
				"preview_chars":{"type":"integer","minimum":0,"description":"Optional compact message preview character budget. Zero means the tool default."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Compact responses default to 12000 bytes and return response_too_large instead of silently dropping fields."}
			},
			"required":["counterpart"]
		}`),
	}
}

func notificationDismissDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "notification_dismiss",
		Description:  "Dismiss a notification or all notifications, marking them as read.",
		OutputSchema: genericDataObjectOutputSchema(),
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
	if len(in) > notificationReadMaxTypes {
		return nil, nil, invalidParams(fmt.Sprintf("too many notification types requested; maximum %d", notificationReadMaxTypes))
	}

	requested := make([]string, 0, len(in))
	upstream := make([]string, 0, len(in))
	seenRequested := make(map[string]struct{}, len(in))
	seenUpstream := make(map[string]struct{}, len(in))
	requiresUntypedRead := false

	for _, typ := range in {
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "", "all":
			continue
		case "favorite":
			typ = "favourite"
		}
		if !allowedNotificationType(typ) {
			return nil, nil, invalidParams("unsupported notification type: " + typ + "; supported values: " + supportedNotificationTypesText())
		}
		if _, ok := seenRequested[typ]; !ok {
			if len(requested) >= notificationReadMaxTypes {
				return nil, nil, invalidParams("too many notification types requested")
			}
			seenRequested[typ] = struct{}{}
			requested = append(requested, typ)
		}
		if !upstreamNotificationType(typ) {
			requiresUntypedRead = true
			continue
		}
		if _, ok := seenUpstream[typ]; !ok {
			seenUpstream[typ] = struct{}{}
			upstream = append(upstream, typ)
		}
	}

	if requiresUntypedRead {
		upstream = nil
	}
	return requested, upstream, nil
}

func allowedNotificationType(typ string) bool {
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return false
	}
	for _, allowed := range notificationSupportedTypes {
		if typ == allowed {
			return true
		}
	}
	return false
}

func upstreamNotificationType(typ string) bool {
	_, ok := notificationUpstreamFilterTypes[strings.TrimSpace(typ)]
	return ok
}

func notificationTypesIncludeCommunication(types []string) bool {
	for _, typ := range types {
		if isCommunicationNotificationType(typ) {
			return true
		}
	}
	return false
}

func isCommunicationNotificationType(typ string) bool {
	typ = strings.ToLower(strings.TrimSpace(typ))
	return typ == notificationCommunicationInbound || strings.HasPrefix(typ, "communication:")
}

func runtimeAllowsCommunicationNotifications(ctx context.Context) bool {
	resolved, ok := runtimepolicy.FromContext(ctx)
	if !ok {
		return false
	}
	return resolved.CommunicationsEnabled
}

func runtimeProfileName(ctx context.Context) string {
	resolved, ok := runtimepolicy.FromContext(ctx)
	if !ok || strings.TrimSpace(string(resolved.Profile)) == "" {
		return "unknown"
	}
	return string(resolved.Profile)
}

func filterRawCommunicationNotifications(items []any) []any {
	if len(items) == 0 {
		return items
	}

	filtered := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			filtered = append(filtered, item)
			continue
		}
		if isRawCommunicationNotification(raw) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func isRawCommunicationNotification(raw map[string]any) bool {
	if raw == nil {
		return false
	}
	if isCommunicationNotificationType(normalizeSocialNotificationType(raw)) {
		return true
	}
	return notificationChannel(raw) != ""
}

func supportedNotificationTypesText() string {
	return strings.Join(notificationSupportedTypes, ", ")
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

func boundedConversationGetLimit(limit int) int {
	switch {
	case limit <= 0:
		return conversationGetDefaultLimit
	case limit > conversationGetMaxLimit:
		return conversationGetMaxLimit
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

func conversationDetailFromAPI(raw any) (map[string]any, error) {
	conversation, ok := raw.(map[string]any)
	if !ok || conversation == nil {
		return nil, fmt.Errorf("unexpected conversation response")
	}

	if nested := firstMap(conversation, "conversation"); nested != nil {
		merged := cloneStringAnyMap(nested)
		for _, key := range []string{"messages", "statuses", "items", "nextCursor", "next_cursor"} {
			if _, exists := merged[key]; !exists {
				if value, ok := conversation[key]; ok {
					merged[key] = value
				}
			}
		}
		return merged, nil
	}
	return conversation, nil
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

func normalizeSocialConversationDetail(raw map[string]any, includeRaw bool) map[string]any {
	out := normalizeSocialConversation(raw, includeRaw)
	if messages := normalizeSocialConversationMessages(conversationMessagesFromMap(raw)); len(messages) > 0 {
		out["messages"] = messages
	}
	if nextCursor := firstNonEmptyStringMap(raw, "nextCursor", "next_cursor"); nextCursor != "" {
		out["nextCursor"] = nextCursor
	}
	return out
}

func normalizeSocialConversationMessages(items []any) []any {
	if len(items) == 0 {
		return nil
	}
	messages := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok || raw == nil {
			continue
		}
		messages = append(messages, socialStatusStandardPayload(raw))
	}
	return messages
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
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

type notificationActorFilter struct {
	Raw        string
	Active     bool
	Candidates map[string]struct{}
}

type notificationActorFilterCursorPayload struct {
	Version        int    `json:"v"`
	Actor          string `json:"actor"`
	UpstreamCursor string `json:"upstreamCursor,omitempty"`
	Offset         int    `json:"offset,omitempty"`
}

func newNotificationActorFilter(raw string) notificationActorFilter {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return notificationActorFilter{}
	}
	candidates := notificationActorFilterCandidates(raw)
	return notificationActorFilter{
		Raw:        raw,
		Active:     len(candidates) > 0,
		Candidates: candidates,
	}
}

func notificationActorFilterFetchLimit(limit int) int {
	limit = boundedNotificationReadLimit(limit)
	overFetch := limit * 4
	if overFetch < limit {
		overFetch = limit
	}
	return boundedNotificationReadLimit(overFetch)
}

func parseNotificationActorFilterCursor(cursor string, filter notificationActorFilter) (string, int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" || !strings.HasPrefix(cursor, notificationActorFilterCursorPrefix) {
		return cursor, 0, nil
	}
	encoded := strings.TrimPrefix(cursor, notificationActorFilterCursorPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", 0, fmt.Errorf("invalid actor filter cursor")
	}
	var payload notificationActorFilterCursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return "", 0, fmt.Errorf("invalid actor filter cursor")
	}
	if payload.Version != 1 || payload.Offset < 0 {
		return "", 0, fmt.Errorf("invalid actor filter cursor")
	}
	if !notificationActorFilterSameActor(payload.Actor, filter) {
		return "", 0, fmt.Errorf("actor filter cursor does not match actor")
	}
	return strings.TrimSpace(payload.UpstreamCursor), payload.Offset, nil
}

func notificationActorFilterCursor(filter notificationActorFilter, upstreamCursor string, offset int) string {
	if !filter.Active || offset <= 0 {
		return ""
	}
	payload := notificationActorFilterCursorPayload{
		Version:        1,
		Actor:          filter.Raw,
		UpstreamCursor: strings.TrimSpace(upstreamCursor),
		Offset:         offset,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return notificationActorFilterCursorPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func notificationActorFilterSameActor(actor string, filter notificationActorFilter) bool {
	if !filter.Active {
		return false
	}
	for candidate := range notificationActorFilterCandidates(actor) {
		if _, ok := filter.Candidates[candidate]; ok {
			return true
		}
	}
	return false
}

func filterSocialNotificationsByActor(items []any, filter notificationActorFilter) []any {
	if !filter.Active || len(items) == 0 {
		return items
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		notification, _ := item.(map[string]any)
		if notificationActorMatches(notification, filter) {
			out = append(out, notification)
		}
	}
	return out
}

func notificationActorFilterMetadata(filter notificationActorFilter, overFetchLimit int, requestedLimit int, upstreamCount int, matchedCount int, returnedCount int, windowOffset int, sameWindowContinuation bool) map[string]any {
	continuation := "upstream_cursor"
	if sameWindowContinuation {
		continuation = "same_overfetch_window"
	}
	out := map[string]any{
		"actor":          filter.Raw,
		"strategy":       "mcp_side_overfetch",
		"requestedLimit": requestedLimit,
		"overFetchLimit": overFetchLimit,
		"upstreamCount":  upstreamCount,
		"matchedCount":   matchedCount,
		"returnedCount":  returnedCount,
		"windowOffset":   windowOffset,
		"continuation":   continuation,
		"matchFields": []any{
			"actor.id",
			"actor.username",
			"actor.acct",
			"actor.url",
			"targetPost.author.*",
			"communication.from.soulAgentId",
			"communication.from.agentId",
			"communication.from.email",
			"communication.from.address",
			"communication.from.identifier",
		},
	}
	if sameWindowContinuation {
		out["sameWindowRemainder"] = matchedCount - (windowOffset + returnedCount)
	}
	return out
}

func notificationActorMatches(notification map[string]any, filter notificationActorFilter) bool {
	if notification == nil || !filter.Active {
		return false
	}
	for _, actor := range []map[string]any{
		firstMap(notification, "actor", "account"),
		firstMap(firstMap(notification, "targetPost", "status", "post"), "author", "account", "actor"),
		firstMap(firstMap(notification, "communication"), "from"),
	} {
		if notificationActorMapMatches(actor, filter.Candidates) {
			return true
		}
	}
	return false
}

func notificationActorMapMatches(raw map[string]any, candidates map[string]struct{}) bool {
	if raw == nil || len(candidates) == 0 {
		return false
	}
	for _, key := range []string{
		"id",
		"username",
		"acct",
		"url",
		"uri",
		"actorUrl",
		"actorURL",
		"actor_url",
		"soulAgentId",
		"soul_agent_id",
		"agentId",
		"agentID",
		"agent_id",
		"email",
		"address",
		"identifier",
		"name",
	} {
		if notificationActorValueMatches(firstNonEmptyStringMap(raw, key), candidates) {
			return true
		}
	}
	return false
}

func notificationActorValueMatches(value string, candidates map[string]struct{}) bool {
	for candidate := range notificationActorFilterCandidates(value) {
		if _, ok := candidates[candidate]; ok {
			return true
		}
	}
	return false
}

func notificationActorFilterCandidates(value string) map[string]struct{} {
	out := map[string]struct{}{}
	value = strings.TrimSpace(value)
	if value == "" {
		return out
	}
	add := func(candidate string) {
		candidate = normalizeNotificationActorCandidate(candidate)
		if candidate != "" {
			out[candidate] = struct{}{}
		}
	}

	add(value)
	add(strings.TrimPrefix(value, "@"))

	withoutAt := strings.TrimPrefix(strings.TrimSpace(value), "@")
	if at := strings.Index(withoutAt, "@"); at > 0 {
		add(withoutAt[:at])
		add(withoutAt)
	}

	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		add(parsed.String())
		add(parsed.Host + parsed.EscapedPath())
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) > 0 {
			add(strings.TrimPrefix(parts[len(parts)-1], "@"))
		}
	}

	return out
}

func normalizeNotificationActorCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimRight(value, "/")
	return strings.ToLower(value)
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
	putIfNotEmpty(out, "url", firstNonEmptyStringMap(raw, "url", "uri", "actorUrl", "actorURL", "actor_url"))
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
		Name:         "post_create",
		Description:  "Create a new post.",
		OutputSchema: genericDataObjectOutputSchema(),
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
		Name:         "post_boost",
		Description:  "Boost/reblog a post.",
		OutputSchema: genericDataObjectOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"post_id":{"type":"string"}},
			"required":["post_id"]
		}`),
	}
}

func postFavoriteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "post_favorite",
		Description:  "Favorite a post.",
		OutputSchema: genericDataObjectOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"post_id":{"type":"string"}},
			"required":["post_id"]
		}`),
	}
}

func followDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "follow",
		Description:  "Follow an account.",
		OutputSchema: genericDataObjectOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"account_id":{"type":"string"}},
			"required":["account_id"]
		}`),
	}
}

func unfollowDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "unfollow",
		Description:  "Unfollow an account.",
		OutputSchema: genericDataObjectOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"account_id":{"type":"string"}},
			"required":["account_id"]
		}`),
	}
}

func profileUpdateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "profile_update",
		Description:  "Update display name, bio, and avatar (best-effort).",
		OutputSchema: genericDataObjectOutputSchema(),
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
