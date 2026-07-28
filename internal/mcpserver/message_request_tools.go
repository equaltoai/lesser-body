package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	messageRequestDefaultLimit = 20
	messageRequestMaxLimit     = 80
	messageRequestPreviewRunes = 160
)

func handleMessageRequestsList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	if in.Limit < 0 || in.Limit > messageRequestMaxLimit {
		return nil, invalidParams(fmt.Sprintf("limit must be between 1 and %d", messageRequestMaxLimit))
	}
	limit := in.Limit
	if limit == 0 {
		limit = messageRequestDefaultLimit
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	conversations, err := client.ListMessageRequests(ctx, token, limit, strings.TrimSpace(in.Cursor))
	if err != nil {
		return messageRequestToolResultFromError("message_requests_list", "", err)
	}
	requests := make([]any, 0, len(conversations))
	for i := range conversations {
		requests = append(requests, messageRequestRef(conversations[i], true))
	}

	payload := map[string]any{
		"source":   "lesser-graphql",
		"folder":   "REQUESTS",
		"requests": requests,
		"count":    len(requests),
		"limit":    limit,
	}
	if cursor := strings.TrimSpace(in.Cursor); cursor != "" {
		payload["cursor"] = cursor
	}
	return toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: fmt.Sprintf("%d pending message request(s)", len(requests)),
		Data:    payload,
		Text: map[string]any{
			"tool":  "message_requests_list",
			"count": len(requests),
		},
	})
}

func handleMessageRequestAccept(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	conversationID, err := parseMessageRequestDecisionArgs(args)
	if err != nil {
		return nil, err
	}
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	conversation, err := client.AcceptMessageRequest(ctx, token, conversationID)
	if err != nil {
		return messageRequestToolResultFromError("message_request_accept", conversationID, err)
	}
	state := strings.ToUpper(strings.TrimSpace(conversation.ViewerMetadata.RequestState))
	if state != "ACCEPTED" {
		return toolErrorResult("upstream_contract_error", "Lesser did not confirm the message request as accepted", http.StatusBadGateway, map[string]any{
			"source":         "lesser-graphql",
			"conversationId": conversationID,
			"requestState":   state,
		})
	}

	ref := messageRequestRef(*conversation, false)
	payload := map[string]any{
		"source":         "lesser-graphql",
		"conversationId": conversationID,
		"decision":       "accepted",
		"requestState":   state,
		"conversation":   ref,
		"expand": map[string]any{
			"tool":           "conversation_get",
			"arguments":      map[string]any{"conversationId": conversationID, "view": "compact"},
			"resultPath":     "structuredContent.data.conversation",
			"textResultPath": "payload",
			"resultAccess":   toolResultAccessPath("payload", "data.conversation"),
		},
	}
	return toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: "message request accepted",
		Data:    payload,
		Text: map[string]any{
			"tool":           "message_request_accept",
			"conversationId": conversationID,
			"requestState":   state,
		},
	})
}

func handleMessageRequestDecline(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	conversationID, err := parseMessageRequestDecisionArgs(args)
	if err != nil {
		return nil, err
	}
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := lesser(ctx)
	if err != nil {
		return nil, err
	}

	declined, err := client.DeclineMessageRequest(ctx, token, conversationID)
	if err != nil {
		return messageRequestToolResultFromError("message_request_decline", conversationID, err)
	}
	if !declined {
		return toolErrorResult("upstream_contract_error", "Lesser did not confirm the message request as declined", http.StatusBadGateway, map[string]any{
			"source":         "lesser-graphql",
			"conversationId": conversationID,
		})
	}

	payload := map[string]any{
		"source":         "lesser-graphql",
		"conversationId": conversationID,
		"decision":       "declined",
		"requestState":   "DECLINED",
		"success":        true,
	}
	return toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: "message request declined",
		Data:    payload,
		Text: map[string]any{
			"tool":           "message_request_decline",
			"conversationId": conversationID,
			"requestState":   "DECLINED",
		},
	})
}

func parseMessageRequestDecisionArgs(args json.RawMessage) (string, error) {
	var in struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", invalidParams("invalid args: " + err.Error())
	}
	conversationID := strings.TrimSpace(in.ConversationID)
	if conversationID == "" {
		return "", invalidParams("missing conversationId")
	}
	return conversationID, nil
}

func messageRequestRef(conversation lesserapi.MessageRequestConversation, includeActions bool) map[string]any {
	state := strings.ToUpper(strings.TrimSpace(conversation.ViewerMetadata.RequestState))
	ref := map[string]any{
		"conversationId": strings.TrimSpace(conversation.ID),
		"unread":         conversation.Unread,
		"requestState":   state,
	}
	putIfNotEmpty(ref, "createdAt", conversation.CreatedAt)
	putIfNotEmpty(ref, "updatedAt", conversation.UpdatedAt)
	putIfNotEmpty(ref, "requestedAt", conversation.ViewerMetadata.RequestedAt)
	putIfNotEmpty(ref, "acceptedAt", conversation.ViewerMetadata.AcceptedAt)
	putIfNotEmpty(ref, "declinedAt", conversation.ViewerMetadata.DeclinedAt)

	accounts := make([]any, 0, len(conversation.Accounts))
	for _, account := range conversation.Accounts {
		accountRef := map[string]any{}
		putIfNotEmpty(accountRef, "id", account.ID)
		putIfNotEmpty(accountRef, "username", account.Username)
		putIfNotEmpty(accountRef, "domain", account.Domain)
		putIfNotEmpty(accountRef, "displayName", account.DisplayName)
		if len(accountRef) > 0 {
			accounts = append(accounts, accountRef)
		}
	}
	ref["accounts"] = accounts

	if last := conversation.LastStatus; last != nil {
		preview, truncated := compactStringWithTruncation(last.Content, messageRequestPreviewRunes)
		lastRef := map[string]any{"contentTruncated": truncated}
		putIfNotEmpty(lastRef, "id", last.ID)
		putIfNotEmpty(lastRef, "createdAt", last.CreatedAt)
		putIfNotEmpty(lastRef, "contentPreview", preview)
		ref["lastMessageRef"] = lastRef
	}
	if includeActions && strings.TrimSpace(conversation.ID) != "" {
		ref["actions"] = map[string]any{
			"accept": map[string]any{
				"tool":      "message_request_accept",
				"arguments": map[string]any{"conversationId": strings.TrimSpace(conversation.ID)},
			},
			"decline": map[string]any{
				"tool":      "message_request_decline",
				"arguments": map[string]any{"conversationId": strings.TrimSpace(conversation.ID)},
			},
		}
	}
	return ref
}

func messageRequestToolResultFromError(toolName string, conversationID string, err error) (*mcpruntime.ToolResult, error) {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var gqlErr *lesserapi.MessageRequestGraphQLErrors
	if errors.As(err, &gqlErr) {
		details := map[string]any{
			"source":     "lesser-graphql",
			"tool":       toolName,
			"errorCount": gqlErr.Count,
		}
		if len(gqlErr.Codes) > 0 {
			details["errorCodes"] = gqlErr.Codes
		}
		putIfNotEmpty(details, "conversationId", conversationID)
		return toolErrorResult("lesser_message_request_graphql_error", "Lesser message-request operation failed", http.StatusBadGateway, details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		code := "lesser_message_request_http_error"
		message := "Lesser message-request API request failed"
		if apiErr.Status == http.StatusNotFound {
			code = "not_found"
			message = "message request not found"
		}
		details := map[string]any{
			"source":       "lesser-graphql",
			"tool":         toolName,
			"upstreamCode": apiErr.Status,
		}
		putIfNotEmpty(details, "conversationId", conversationID)
		return toolErrorResult(code, message, apiErr.Status, details)
	}
	return toolErrorResult("lesser_message_request_error", "Lesser message-request operation failed", 0, map[string]any{
		"source": "lesser-graphql",
		"tool":   toolName,
	})
}

func messageRequestsListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "message_requests_list",
		Description:  "List the authenticated recipient's pending direct-message requests from Lesser. Returns bounded previews and explicit accept/decline actions; it does not expose full message bodies.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: messageRequestsListOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":80,"description":"Maximum pending requests to return. Defaults to 20."},
				"cursor":{"type":"string","description":"Optional opaque Lesser conversation cursor."}
			},
			"additionalProperties":false
		}`),
	}
}

func messageRequestAcceptDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "message_request_accept",
		Description:  "Accept a recipient-owned pending direct-message request through Lesser, moving the conversation into the recipient's inbox so subsequent direct messages can flow.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: messageRequestDecisionOutputSchema(),
		InputSchema:  messageRequestDecisionInputSchema(),
	}
}

func messageRequestDeclineDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "message_request_decline",
		Description:  "Decline a recipient-owned pending direct-message request through Lesser, hiding the request from the recipient's active request folder.",
		Annotations:  destructiveToolAnnotations(),
		OutputSchema: messageRequestDecisionOutputSchema(),
		InputSchema:  messageRequestDecisionInputSchema(),
	}
}

func messageRequestDecisionInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"conversationId":{"type":"string","minLength":1,"description":"Stable Lesser conversation id returned by message_requests_list."}
		},
		"required":["conversationId"],
		"additionalProperties":false
	}`)
}
