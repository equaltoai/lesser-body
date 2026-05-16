package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	mailboxDefaultLimit = 20
	mailboxMaxLimit     = 100
)

type commMailboxDependencies struct {
	agentID    string
	client     *soulapi.Client
	commBearer string
}

type commMailboxListOptions struct {
	ChannelType     string
	Direction       string
	UnreadOnly      bool
	Limit           int
	Cursor          string
	ThreadID        string
	Query           string
	IncludeRaw      bool
	IncludeArchived bool
	IncludeDeleted  bool
	Archived        *bool
	Read            *bool
	Deleted         *bool
}

func loadCommMailboxDependencies(ctx context.Context, operation boundOperation) (*commMailboxDependencies, error) {
	if _, err := requireOAuthBearer(ctx); err != nil {
		return nil, err
	}

	identity, err := authorizedAgentChannelsPayload(ctx, operation)
	if err != nil {
		return nil, err
	}
	agentID, _ := identity["agentId"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, &toolUserError{Code: "upstream_error", Message: "unable to resolve agentId", Status: 502}
	}

	client, err := soulapi.Default()
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: http.StatusInternalServerError}
	}

	commBearer, err := requireCommAPIBearer(ctx)
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: http.StatusInternalServerError, Details: map[string]any{
			"requiredEnv": []string{"LESSER_HOST_INSTANCE_KEY", "LESSER_HOST_INSTANCE_KEY_ARN"},
		}}
	}

	return &commMailboxDependencies{agentID: agentID, client: client, commBearer: commBearer}, nil
}

func commMailboxToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}
	var invalidErr *InvalidParamsError
	if errors.As(err, &invalidErr) {
		return toolErrorResult("invalid_request", invalidErr.Error(), http.StatusBadRequest, nil)
	}
	var userErr *toolUserError
	if errors.As(err, &userErr) {
		return toolErrorResult(userErr.Code, userErr.Message, userErr.Status, userErr.Details)
	}
	return commToolResultFromError(err)
}

func commMailboxResourceContentsFromError(uri string, err error) ([]mcpruntime.ResourceContent, error) {
	if err == nil {
		return resourceJSON(uri, map[string]any{})
	}
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return resourceJSON(uri, map[string]any{"error": failure.payload()})
	}
	var userErr *toolUserError
	if errors.As(err, &userErr) {
		return resourceJSON(uri, map[string]any{"error": map[string]any{
			"code":    userErr.Code,
			"message": userErr.Message,
			"status":  userErr.Status,
			"details": userErr.Details,
		}})
	}
	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)
		if strings.TrimSpace(message) == "" {
			message = commErrorCodeForStatus(apiErr.Status)
		}
		details := map[string]any{}
		if parsed != nil {
			details["apiError"] = parsed
		}
		if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
			details["retryAfterSeconds"] = retryAfter
		}
		return resourceJSON(uri, map[string]any{"error": map[string]any{
			"code":    commErrorCodeForStatus(apiErr.Status),
			"message": message,
			"status":  apiErr.Status,
			"details": details,
		}})
	}
	return resourceJSON(uri, map[string]any{"error": map[string]any{
		"code":    "upstream_error",
		"message": err.Error(),
		"status":  http.StatusBadGateway,
	}})
}

func listHostMailboxMessages(ctx context.Context, deps *commMailboxDependencies, opts commMailboxListOptions) (map[string]any, error) {
	if deps == nil || deps.client == nil {
		return nil, errors.New("mailbox dependencies not initialized")
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(mailboxLimit(opts.Limit)))
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if strings.TrimSpace(opts.ChannelType) != "" {
		query.Set("channelType", strings.ToLower(strings.TrimSpace(opts.ChannelType)))
	}
	if strings.TrimSpace(opts.Direction) != "" {
		query.Set("direction", strings.ToLower(strings.TrimSpace(opts.Direction)))
	}
	if opts.UnreadOnly {
		query.Set("unreadOnly", "true")
	}
	if strings.TrimSpace(opts.ThreadID) != "" {
		query.Set("threadId", strings.TrimSpace(opts.ThreadID))
	}
	if strings.TrimSpace(opts.Query) != "" {
		query.Set("query", strings.TrimSpace(opts.Query))
	}
	// Body-facing inbox resources default to hiding archived/deleted mail; callers can opt in.
	if opts.Archived != nil {
		query.Set("archived", strconv.FormatBool(*opts.Archived))
		if *opts.Archived {
			query.Set("includeArchived", "true")
		} else {
			query.Set("includeArchived", "false")
		}
	} else {
		query.Set("includeArchived", strconv.FormatBool(opts.IncludeArchived))
	}
	if opts.Read != nil {
		query.Set("read", strconv.FormatBool(*opts.Read))
	}
	if opts.Deleted != nil {
		query.Set("deleted", strconv.FormatBool(*opts.Deleted))
		if *opts.Deleted {
			query.Set("includeDeleted", "true")
		}
	} else if opts.IncludeDeleted {
		query.Set("includeDeleted", "true")
	}

	raw, err := deps.client.DoJSON(ctx, "GET", mailboxMessagesPath(deps.agentID), query, deps.commBearer, nil)
	if err != nil {
		return nil, err
	}
	return normalizeMailboxListResult(raw, opts.IncludeRaw), nil
}

func getHostMailboxMessage(ctx context.Context, deps *commMailboxDependencies, messageRef string, includeRaw bool) (map[string]any, error) {
	messageRef = strings.TrimSpace(messageRef)
	if messageRef == "" {
		return nil, invalidParams("messageId is required")
	}
	raw, err := deps.client.DoJSON(ctx, "GET", mailboxMessagePath(deps.agentID, messageRef), nil, deps.commBearer, nil)
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	message := normalizeMailboxMessage(m["message"], includeRaw)
	return map[string]any{"message": message}, nil
}

func getHostMailboxContent(ctx context.Context, deps *commMailboxDependencies, messageRef string) (map[string]any, error) {
	messageRef = strings.TrimSpace(messageRef)
	if messageRef == "" {
		return nil, invalidParams("messageId is required")
	}
	raw, err := deps.client.DoJSON(ctx, "GET", mailboxMessagePath(deps.agentID, messageRef)+"/content", nil, deps.commBearer, nil)
	if err != nil {
		return nil, err
	}
	return normalizeMailboxContentResult(raw), nil
}

func mutateHostMailboxMessage(ctx context.Context, deps *commMailboxDependencies, messageRef string, action string) (map[string]any, error) {
	messageRef = strings.TrimSpace(messageRef)
	action = strings.ToLower(strings.TrimSpace(action))
	if messageRef == "" {
		return nil, invalidParams("messageId is required")
	}
	switch action {
	case "read", "unread", "archive", "unarchive", "delete":
	default:
		return nil, invalidParams("invalid action")
	}
	raw, err := deps.client.DoJSON(ctx, "POST", mailboxMessagePath(deps.agentID, messageRef)+"/"+url.PathEscape(action), nil, deps.commBearer, map[string]any{})
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	message := normalizeMailboxMessage(m["message"], false)
	return map[string]any{
		"messageId":  firstNonEmptyString(message, "messageId", "messageRef", "deliveryId"),
		"messageRef": firstNonEmptyString(message, "messageRef", "deliveryId", "messageId"),
		"action":     action,
		"message":    message,
		"state":      message["state"],
	}, nil
}

func replyHostMailboxMessage(ctx context.Context, deps *commMailboxDependencies, messageRef string, payload map[string]any) (map[string]any, error) {
	messageRef = strings.TrimSpace(messageRef)
	if messageRef == "" {
		return nil, invalidParams("messageId is required")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := deps.client.DoJSON(ctx, "POST", mailboxMessagePath(deps.agentID, messageRef)+"/reply", nil, deps.commBearer, payload)
	if err != nil {
		return nil, err
	}
	return normalizeCommSendResult(raw, nil), nil
}

func normalizeMailboxListResult(raw any, includeRaw bool) map[string]any {
	m, _ := raw.(map[string]any)
	messagesRaw, _ := m["messages"].([]any)
	messages := make([]any, 0, len(messagesRaw))
	for _, item := range messagesRaw {
		messages = append(messages, normalizeMailboxMessage(item, includeRaw))
	}

	out := map[string]any{
		"source":     "lesser-host-mailbox",
		"messages":   messages,
		"count":      countFromMap(m, "count", len(messages)),
		"hasMore":    boolFromMap(m, "hasMore"),
		"nextCursor": strings.TrimSpace(stringFromMap(m, "nextCursor")),
		"notes":      mailboxListNotes(),
	}
	if v := strings.TrimSpace(stringFromMap(m, "instanceSlug")); v != "" {
		out["instanceSlug"] = v
	}
	if v := strings.TrimSpace(stringFromMap(m, "agentId")); v != "" {
		out["agentId"] = v
	}
	// Legacy pagination alias for clients still using notification-backed names.
	out["nextSince"] = out["nextCursor"]
	return out
}

func normalizeMailboxMessage(raw any, includeRaw bool) map[string]any {
	m, _ := raw.(map[string]any)
	messageRef := strings.TrimSpace(stringFromMap(m, "messageRef"))
	deliveryID := strings.TrimSpace(stringFromMap(m, "deliveryId"))
	hostMessageID := strings.TrimSpace(stringFromMap(m, "messageId"))
	if messageRef == "" {
		messageRef = deliveryID
	}
	if messageRef == "" {
		messageRef = hostMessageID
	}
	channel := strings.TrimSpace(stringFromMap(m, "channelType"))
	if channel == "" {
		channel = strings.TrimSpace(stringFromMap(m, "channel"))
	}
	preview := strings.TrimSpace(stringFromMap(m, "preview"))
	createdAt := strings.TrimSpace(stringFromMap(m, "createdAt"))

	out := map[string]any{
		"messageId":     messageRef,
		"messageRef":    messageRef,
		"deliveryId":    deliveryID,
		"hostMessageId": hostMessageID,
		"threadId":      strings.TrimSpace(stringFromMap(m, "threadId")),
		"channel":       channel,
		"channelType":   channel,
		"direction":     strings.TrimSpace(stringFromMap(m, "direction")),
		"status":        strings.TrimSpace(stringFromMap(m, "status")),
		"from":          mapFromAny(m["from"]),
		"to":            mapFromAny(m["to"]),
		"subject":       strings.TrimSpace(stringFromMap(m, "subject")),
		"preview":       preview,
		"body":          preview,
		"bodyIsPreview": true,
		"content":       mapFromAny(m["content"]),
		"state":         mapFromAny(m["state"]),
		"createdAt":     createdAt,
		"receivedAt":    createdAt,
		"updatedAt":     strings.TrimSpace(stringFromMap(m, "updatedAt")),
	}
	if includeRaw {
		out["_raw"] = m
	}
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(out["direction"])), "outbound") {
		out["sentAt"] = createdAt
	}
	if provider := strings.TrimSpace(stringFromMap(m, "provider")); provider != "" {
		out["provider"] = provider
	}
	if providerMessageID := strings.TrimSpace(stringFromMap(m, "providerMessageId")); providerMessageID != "" {
		out["providerMessageId"] = providerMessageID
	}
	return out
}

func normalizeMailboxContentResult(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	messageRef := strings.TrimSpace(stringFromMap(m, "messageRef"))
	deliveryID := strings.TrimSpace(stringFromMap(m, "deliveryId"))
	hostMessageID := strings.TrimSpace(stringFromMap(m, "messageId"))
	if messageRef == "" {
		messageRef = deliveryID
	}
	if messageRef == "" {
		messageRef = hostMessageID
	}
	return map[string]any{
		"source":        "lesser-host-mailbox",
		"messageId":     messageRef,
		"messageRef":    messageRef,
		"deliveryId":    deliveryID,
		"hostMessageId": hostMessageID,
		"contentType":   strings.TrimSpace(stringFromMap(m, "contentType")),
		"sha256":        strings.TrimSpace(stringFromMap(m, "sha256")),
		"bytes":         m["bytes"],
		"body":          rawStringFromMap(m, "body"),
		"instanceSlug":  strings.TrimSpace(stringFromMap(m, "instanceSlug")),
		"agentId":       strings.TrimSpace(stringFromMap(m, "agentId")),
	}
}

func mailboxMessagesPath(agentID string) string {
	return "/api/v1/soul/comm/mailbox/" + url.PathEscape(strings.TrimSpace(agentID)) + "/messages"
}

func mailboxMessagePath(agentID string, messageRef string) string {
	return mailboxMessagesPath(agentID) + "/" + url.PathEscape(strings.TrimSpace(messageRef))
}

func mailboxLimit(limit int) int {
	if limit <= 0 {
		return mailboxDefaultLimit
	}
	if limit > mailboxMaxLimit {
		return mailboxMaxLimit
	}
	return limit
}

func mailboxCursor(cursor string, since string) string {
	cursor = strings.TrimSpace(cursor)
	if cursor != "" {
		return cursor
	}
	return strings.TrimSpace(since)
}

func optionalBoolArg(args json.RawMessage, key string) *bool {
	var raw map[string]any
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	v, ok := raw[key]
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

func validateMailboxReadFilters(unreadOnly bool, read *bool) error {
	if unreadOnly && read != nil && *read {
		return invalidParams("read=true conflicts with unreadOnly=true")
	}
	return nil
}

func mailboxListNotes() map[string]any {
	return map[string]any{
		"authority":       "lesser-host Soul Comm Mailbox",
		"bodyField":       "body is a redacted preview in list/get results; call email_get_content for full content when content.available is true",
		"messageIdRef":    "messageId is an opaque host messageRef suitable for get/content/state/reply calls",
		"legacySinceName": "nextSince is an alias of nextCursor for older clients; pass it back as cursor or since",
	}
}

func mapFromAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, value := range m {
		out[k] = value
	}
	return out
}

func boolFromMap(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true") || strings.TrimSpace(b) == "1"
	default:
		return false
	}
}

func countFromMap(m map[string]any, key string, fallback int) int {
	if m == nil {
		return fallback
	}
	switch v := m[key].(type) {
	case float64:
		if v >= 0 {
			return int(v)
		}
	case int:
		if v >= 0 {
			return v
		}
	case json.Number:
		if i, err := v.Int64(); err == nil && i >= 0 {
			return int(i)
		}
	}
	return fallback
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(stringFromMap(m, key)); v != "" {
			return v
		}
	}
	return ""
}

func rawStringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw, _ := m[key].(string)
	return raw
}

func outboundIdempotencyKey(ctx context.Context, explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit
	}
	return resolveOutboundCommIdempotencyKey(ctx, "")
}
