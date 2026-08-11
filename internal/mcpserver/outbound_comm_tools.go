package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/google/uuid"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

type commSendDependencies struct {
	agentID    string
	client     *soulapi.Client
	commBearer string
	advisory   map[string]any
}

const botDisclosurePrefix = "[bot] "

func handleSmsSend(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return handleSmsSendWithProgress(ctx, args, nil)
}

func handleSmsSendStreaming(ctx context.Context, args json.RawMessage, emit func(mcpruntime.SSEEvent)) (*mcpruntime.ToolResult, error) {
	return handleSmsSendWithProgress(ctx, args, emit)
}

func handleSmsSendWithProgress(ctx context.Context, args json.RawMessage, emit func(mcpruntime.SSEEvent)) (*mcpruntime.ToolResult, error) {
	var in struct {
		To        string `json:"to"`
		Body      string `json:"body"`
		MessageID string `json:"messageId,omitempty"`
		InReplyTo string `json:"inReplyTo,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}

	in.To = normalizeCommPhoneE164(in.To)
	in.Body = strings.TrimSpace(in.Body)
	replyRef, err := resolveCommReplyReference(in.MessageID, in.InReplyTo)
	if err != nil {
		return toolErrorResult("invalid_request", err.Error(), 400, nil)
	}
	if in.To == "" || in.Body == "" {
		return toolErrorResult("invalid_request", "to and body are required", 400, nil)
	}
	in.Body = ensureBotDisclosurePrefix(in.Body)
	idempotencyKey := resolveOutboundCommIdempotencyKey(ctx, in.MessageID)

	deps, res, err := loadCommSendDependencies(ctx, "sms")
	if res != nil || err != nil {
		return res, err
	}

	emitCommProgress(emit, 1, 2, "sending sms")

	body := map[string]any{
		"to":   in.To,
		"body": in.Body,
	}
	if replyRef != "" {
		body["inReplyTo"] = replyRef
	}

	normalized, err := sendOutboundComm(ctx, deps, "sms", body, idempotencyKey)
	if err != nil {
		return commToolResultFromError(err)
	}
	emitCommProgress(emit, 2, 2, "sms queued")
	if replyRef != "" {
		normalized["inReplyTo"] = replyRef
	}
	return toolJSONResult(normalized, nil)
}

func handlePhoneCall(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		To                 string `json:"to"`
		Purpose            string `json:"purpose"`
		MaxDurationMinutes int    `json:"maxDurationMinutes,omitempty"`
		MessageID          string `json:"messageId,omitempty"`
		InReplyTo          string `json:"inReplyTo,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolErrorResult("invalid_request", "invalid args: "+err.Error(), 400, nil)
	}

	in.To = normalizeCommPhoneE164(in.To)
	in.Purpose = strings.TrimSpace(in.Purpose)
	replyRef, err := resolveCommReplyReference(in.MessageID, in.InReplyTo)
	if err != nil {
		return toolErrorResult("invalid_request", err.Error(), 400, nil)
	}
	if in.To == "" || in.Purpose == "" {
		return toolErrorResult("invalid_request", "to and purpose are required", 400, nil)
	}
	idempotencyKey := resolveOutboundCommIdempotencyKey(ctx, in.MessageID)

	deps, res, err := loadCommSendDependencies(ctx, "voice")
	if res != nil || err != nil {
		return res, err
	}

	body := map[string]any{
		"to":   in.To,
		"body": in.Purpose,
	}
	if replyRef != "" {
		body["inReplyTo"] = replyRef
	}

	normalized, err := sendOutboundComm(ctx, deps, "voice", body, idempotencyKey)
	if err != nil {
		if isUnsupportedVoiceGap(err) {
			return voiceGapToolResult(err)
		}
		return commToolResultFromError(err)
	}

	normalized["purpose"] = in.Purpose
	normalized["requestedMaxDurationMinutes"] = in.MaxDurationMinutes
	normalized["maxDurationEnforcedByHost"] = false
	if replyRef != "" {
		normalized["inReplyTo"] = replyRef
	}
	return toolJSONResult(normalized, nil)
}

func loadCommSendDependencies(ctx context.Context, channel string) (*commSendDependencies, *mcpruntime.ToolResult, error) {
	if _, err := requireOAuthBearer(ctx); err != nil {
		res, resErr := authToolResultFromError(err)
		return nil, res, resErr
	}

	identity, err := authorizedAgentChannelsPayload(ctx, boundOperationForOutboundChannel(channel))
	if err != nil {
		res, resErr := identityToolResultFromError(err)
		return nil, res, resErr
	}

	agentID, _ := identity["agentId"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		res, resErr := toolErrorResult("upstream_error", "unable to resolve agentId", 502, nil)
		return nil, res, resErr
	}

	client, err := soulapi.Default()
	if err != nil {
		res, resErr := toolErrorResult("not_configured", err.Error(), 500, nil)
		return nil, res, resErr
	}

	commBearer, err := requireCommAPIBearer(ctx)
	if err != nil {
		res, resErr := toolErrorResult("not_configured", err.Error(), 500, map[string]any{
			"requiredEnv": []string{"LESSER_HOST_INSTANCE_KEY", "LESSER_HOST_INSTANCE_KEY_ARN"},
		})
		return nil, res, resErr
	}

	return &commSendDependencies{
		agentID:    agentID,
		client:     client,
		commBearer: commBearer,
		advisory:   commBoundaryAdvisoryForChannel(ctx, client, agentID, channel),
	}, nil, nil
}

func boundOperationForOutboundChannel(channel string) boundOperation {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "email":
		return boundOperationEmailSend
	case "sms":
		return boundOperationSMSSend
	case "voice":
		return boundOperationVoiceRead
	default:
		return boundOperation(strings.TrimSpace(channel))
	}
}

func requireCommAPIBearer(ctx context.Context) (string, error) {
	token, err := auth.LesserHostInstanceKey(ctx)
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("LESSER_HOST_INSTANCE_KEY is required for host-backed communication")
	}
	return token, nil
}

func sendOutboundComm(ctx context.Context, deps *commSendDependencies, channel string, body map[string]any, idempotencyKey string) (map[string]any, error) {
	if deps == nil || deps.client == nil {
		return nil, errors.New("outbound communication dependencies not initialized")
	}

	idempotencyKey = resolveOutboundCommIdempotencyKey(ctx, idempotencyKey)
	payload := map[string]any{
		"channel":        channel,
		"agentId":        deps.agentID,
		"idempotencyKey": idempotencyKey,
	}
	if actedBy := sharedCallerActedBy(ctx); actedBy != "" {
		payload["actedBy"] = actedBy
	}
	for key, value := range body {
		payload[key] = value
	}

	out, err := deps.client.DoJSON(ctx, "POST", "/api/v1/soul/comm/send", nil, deps.commBearer, payload)
	if err != nil {
		return nil, err
	}

	normalized := normalizeCommSendResult(out, deps.advisory)
	normalized["idempotencyKey"] = idempotencyKey
	_ = maybeHydrateCommStatus(ctx, deps.client, deps.commBearer, normalized)
	return normalized, nil
}

func resolveOutboundCommIdempotencyKey(ctx context.Context, value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return uuid.NewString()
}

// sharedCallerActedBy returns the normalized real-caller username to attach as
// actedBy attribution on outbound comms when a share-grant caller acts through
// an agent route they do not own. Owner sends (caller == actor), non-OAuth
// principals, and contexts without an actor route value return "" so the
// owner payload stays byte-for-byte unchanged. actedBy is attribution only,
// never authorization; admission-time share-grant checks are the enforcement.
func sharedCallerActedBy(ctx context.Context) string {
	_, caller, shared := shareGrantActorCaller(ctx)
	if !shared {
		return ""
	}
	return caller
}

func emitCommProgress(emit func(mcpruntime.SSEEvent), progress int, total int, message string) {
	if emit == nil {
		return
	}
	emit(mcpruntime.SSEEvent{Data: map[string]any{
		"progress": progress,
		"total":    total,
		"message":  strings.TrimSpace(message),
	}})
}

func commBoundaryAdvisoryForChannel(ctx context.Context, client *soulapi.Client, agentID string, channel string) map[string]any {
	if client == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}

	regAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/agents/"+url.PathEscape(strings.TrimSpace(agentID))+"/registration", nil, "", nil)
	if err != nil {
		return nil
	}
	reg, _ := regAny.(map[string]any)
	boundaries, _ := reg["boundaries"].([]any)
	if len(boundaries) == 0 {
		return nil
	}

	channel = strings.ToLower(strings.TrimSpace(channel))
	relevant := make([]any, 0, len(boundaries))
	for _, boundary := range boundaries {
		m, _ := boundary.(map[string]any)
		category, _ := m["category"].(string)
		if strings.ToLower(strings.TrimSpace(category)) != "communication_policy" {
			continue
		}

		boundaryChannel, _ := m["channel"].(string)
		boundaryChannel = strings.ToLower(strings.TrimSpace(boundaryChannel))
		if boundaryChannel != "" && boundaryChannel != channel {
			continue
		}
		relevant = append(relevant, m)
	}
	if len(relevant) == 0 {
		return nil
	}

	return map[string]any{
		"communicationPolicyBoundaries": relevant,
	}
}

func normalizeCommPhoneE164(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ReplaceAll(raw, "(", "")
	raw = strings.ReplaceAll(raw, ")", "")
	raw = strings.ReplaceAll(raw, ".", "")
	return raw
}

func ensureBotDisclosurePrefix(value string) string {
	value = strings.TrimSpace(value)
	if hasBotDisclosurePrefix(value) {
		return value
	}
	return botDisclosurePrefix + value
}

func hasBotDisclosurePrefix(value string) bool {
	value = strings.TrimSpace(value)
	for value != "" {
		switch {
		case len(value) >= 3 && strings.EqualFold(value[:3], "re:"):
			value = strings.TrimSpace(value[3:])
		case len(value) >= 3 && strings.EqualFold(value[:3], "fw:"):
			value = strings.TrimSpace(value[3:])
		case len(value) >= 4 && strings.EqualFold(value[:4], "fwd:"):
			value = strings.TrimSpace(value[4:])
		default:
			return len(value) >= len(botDisclosurePrefix) && strings.EqualFold(value[:len(botDisclosurePrefix)], botDisclosurePrefix)
		}
	}
	return false
}

func resolveCommReplyReference(messageID string, inReplyTo string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	inReplyTo = strings.TrimSpace(inReplyTo)
	switch {
	case messageID != "" && inReplyTo != "" && messageID != inReplyTo:
		return "", errors.New("messageId and inReplyTo must match when both are provided")
	case inReplyTo != "":
		return inReplyTo, nil
	default:
		return messageID, nil
	}
}

func isUnsupportedVoiceGap(err error) bool {
	var apiErr *soulapi.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status != 503 {
		return false
	}
	message, _ := commExtractAPIErrorMessage(apiErr.Body)
	return strings.Contains(strings.ToLower(message), "channel not supported")
}

func voiceGapToolResult(err error) (*mcpruntime.ToolResult, error) {
	var apiErr *soulapi.APIError
	if !errors.As(err, &apiErr) {
		return commToolResultFromError(err)
	}

	message, parsed := commExtractAPIErrorMessage(apiErr.Body)
	if strings.TrimSpace(message) == "" {
		message = "lesser-host does not yet support outbound voice calls"
	}

	details := map[string]any{
		"gap":             "outbound_voice_not_supported",
		"channel":         "voice",
		"upstreamMessage": message,
	}
	if parsed != nil {
		details["apiError"] = parsed
	}

	return toolErrorResult("host_gap", "lesser-host does not yet support outbound voice calls for soul communication", apiErr.Status, details)
}
