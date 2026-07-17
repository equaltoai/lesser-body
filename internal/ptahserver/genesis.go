package ptahserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	toolAgentGenesisBegin             = "agent_genesis_begin"
	toolAgentGenesisRead              = "agent_genesis_read"
	toolAgentGenesisAdvance           = "agent_genesis_advance"
	toolAgentGenesisRecover           = "agent_genesis_recover"
	toolAgentGenesisComplete          = "agent_genesis_complete"
	toolAgentGenesisFinalizePreflight = "agent_genesis_finalize_preflight"
	toolAgentGenesisFinalize          = "agent_genesis_finalize"

	genesisMaxMessageRunes = 8192
	genesisMaxPathID       = 128
)

type agentGenesisBeginInput struct {
	Domain       string   `json:"domain"`
	LocalID      string   `json:"local_id"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type agentGenesisAdvanceInput struct {
	RegistrationID string `json:"registration_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	Model          string `json:"model,omitempty"`
	Message        string `json:"message"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
}

type agentGenesisConversationInput struct {
	RegistrationID string `json:"registration_id"`
	ConversationID string `json:"conversation_id"`
}

func genesisOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{"type":"object","description":"Sanitized Host-backed genesis state. Conversation messages, when present, are structured data only."},
			"error":{"type":"object","description":"Structured tool error when isError=true."}
		}
	}`)
}

func agentGenesisBeginDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisBegin,
		Title:       "Begin Host-backed agent genesis",
		Description: "Begin a new agent registration through lesser-host's instance-trust genesis flow. Host derives the new agent identity; this tool does not delegate or require a pre-existing Lesser agent account. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"domain":{"type":"string","description":"Managed instance domain for the new Host registration."},
				"local_id":{"type":"string","description":"New agent local identifier. Host derives the agent identity from domain and local_id."},
				"capabilities":{"type":"array","items":{"type":"string"},"description":"Optional Host genesis capability names."}
			},
			"required":["domain","local_id"],
			"additionalProperties":false
		}`),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisRead,
		Title:        "Read Host-backed agent genesis",
		Description:  "Read the durable lesser-host HostedGenesisSession projection for an in-progress or completed Ptah genesis conversation. Requires explicit instance owner/operator OAuth authority and read scope; the Host projection is the source of truth.",
		Annotations:  readOnlyToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisAdvanceDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisAdvance,
		Title:       "Advance Host-backed agent genesis",
		Description: "Submit the owner's next message to lesser-host's durable genesis/mint conversation. Persist and reuse the Host conversation_id returned by the first call; this is a new-agent genesis flow, not existing-agent delegation. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"registration_id":{"type":"string","description":"Host registration id returned by agent_genesis_begin."},
				"conversation_id":{"type":"string","description":"Host conversation id returned by the first advance; omit only for the first turn."},
				"model":{"type":"string","description":"Host model identifier. Required when starting the first conversation."},
				"message":{"type":"string","description":"Owner's next genesis conversation message."},
				"idempotency_key":{"type":"string","description":"Optional client idempotency key for this Host turn."},
				"correlation_id":{"type":"string","description":"Optional client-safe correlation id."}
			},
			"required":["registration_id","message"],
			"additionalProperties":false
		}`),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisRecoverDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisRecover,
		Title:        "Recover Host-backed agent genesis",
		Description:  "Ask lesser-host to reconcile or safely retry a typed recovery state for the durable genesis conversation. Host owns the recovery state machine; Body never reruns or substitutes the conversation locally. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  additiveMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisCompleteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisComplete,
		Title:        "Complete Host-backed agent genesis",
		Description:  "Ask lesser-host to perform the durable declaration-extraction handoff for the genesis conversation. Body sends no caller-supplied declarations; Host's produced_declarations checkpoint remains authoritative. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  additiveMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisFinalizePreflightDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisFinalizePreflight,
		Title:        "Preflight Host-backed agent genesis finalization",
		Description:  "Read lesser-host's finalization readiness for a declaration-ready genesis conversation. Instance-trust finalization does not require a wallet signature or self-attestation. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  idempotentMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisFinalizeDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisFinalize,
		Title:        "Finalize Host-backed agent genesis",
		Description:  "Finalize and publish the Host-owned instance-trust genesis result. Host publishes the new agent identity; Body does not sign a wallet, accept a private declaration, or delegate an existing Lesser account. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
		Annotations:  additiveMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func genesisConversationInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"registration_id":{"type":"string","description":"Host registration id returned by agent_genesis_begin."},
			"conversation_id":{"type":"string","description":"Host durable conversation id returned by agent_genesis_advance."}
		},
		"required":["registration_id","conversation_id"],
		"additionalProperties":false
	}`)
}

func (cfg config) handleAgentGenesisBegin(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisBegin, true)
	if result != nil || err != nil {
		return result, err
	}
	in, result, err := parseAgentGenesisBeginInput(args)
	if result != nil || err != nil {
		return result, err
	}
	client, result, err := cfg.genesisClientForTool(toolAgentGenesisBegin)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolAgentGenesisBegin)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolAgentGenesisBegin, actor, true)
	raw, err := client.BeginRegistration(ctx, bearer, hostapi.RegistrationBeginRequest{
		Domain:       in.Domain,
		LocalID:      in.LocalID,
		Capabilities: in.Capabilities,
	})
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisBegin, err)
	}
	return genesisSuccessResult(toolAgentGenesisBegin, "begin", raw)
}

func (cfg config) handleAgentGenesisRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisRead, false)
	if result != nil || err != nil {
		return result, err
	}
	in, result, err := parseAgentGenesisConversationInput(args)
	if result != nil || err != nil {
		return result, err
	}
	client, result, err := cfg.genesisClientForTool(toolAgentGenesisRead)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolAgentGenesisRead)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolAgentGenesisRead, actor, false)
	raw, err := client.ReadConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisRead, err)
	}
	return genesisSuccessResult(toolAgentGenesisRead, "read", raw)
}

func (cfg config) handleAgentGenesisAdvance(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisAdvance, true)
	if result != nil || err != nil {
		return result, err
	}
	in, result, err := parseAgentGenesisAdvanceInput(args)
	if result != nil || err != nil {
		return result, err
	}
	client, result, err := cfg.genesisClientForTool(toolAgentGenesisAdvance)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolAgentGenesisAdvance)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolAgentGenesisAdvance, actor, true)
	raw, err := client.AdvanceConversation(ctx, bearer, in.RegistrationID, hostapi.MintConversationRequest{
		ConversationID:  in.ConversationID,
		Model:           in.Model,
		Message:         in.Message,
		IdempotencyKey:  in.IdempotencyKey,
		CorrelationID:   in.CorrelationID,
		LesserRequestID: auth.RequestIDFromToolContext(ctx),
	})
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisAdvance, err)
	}
	return genesisSuccessResult(toolAgentGenesisAdvance, "advance", raw)
}

func (cfg config) handleAgentGenesisRecover(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return cfg.handleGenesisConversationAction(ctx, args, toolAgentGenesisRecover, func(client hostapi.GenesisClient, bearer string, in agentGenesisConversationInput) (map[string]any, error) {
		return client.RecoverConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	})
}

func (cfg config) handleAgentGenesisComplete(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return cfg.handleGenesisConversationAction(ctx, args, toolAgentGenesisComplete, func(client hostapi.GenesisClient, bearer string, in agentGenesisConversationInput) (map[string]any, error) {
		return client.CompleteConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	})
}

func (cfg config) handleAgentGenesisFinalizePreflight(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return cfg.handleGenesisConversationAction(ctx, args, toolAgentGenesisFinalizePreflight, func(client hostapi.GenesisClient, bearer string, in agentGenesisConversationInput) (map[string]any, error) {
		return client.FinalizePreflight(ctx, bearer, in.RegistrationID, in.ConversationID)
	})
}

func (cfg config) handleAgentGenesisFinalize(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	return cfg.handleGenesisConversationAction(ctx, args, toolAgentGenesisFinalize, func(client hostapi.GenesisClient, bearer string, in agentGenesisConversationInput) (map[string]any, error) {
		return client.FinalizeConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	})
}

type genesisConversationAction func(client hostapi.GenesisClient, bearer string, in agentGenesisConversationInput) (map[string]any, error)

func (cfg config) handleGenesisConversationAction(ctx context.Context, args json.RawMessage, toolName string, action genesisConversationAction) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolName, true)
	if result != nil || err != nil {
		return result, err
	}
	in, result, err := parseAgentGenesisConversationInput(args)
	if result != nil || err != nil {
		return result, err
	}
	client, result, err := cfg.genesisClientForTool(toolName)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolName)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolName, actor, true)
	raw, err := action(client, bearer, in)
	if err != nil {
		return genesisToolResultFromError(toolName, err)
	}
	return genesisSuccessResult(toolName, genesisOperationForTool(toolName), raw)
}

func genesisOperationForTool(toolName string) string {
	switch toolName {
	case toolAgentGenesisBegin:
		return "begin"
	case toolAgentGenesisRead:
		return "read"
	case toolAgentGenesisAdvance:
		return "advance"
	case toolAgentGenesisRecover:
		return "recover"
	case toolAgentGenesisComplete:
		return "complete"
	case toolAgentGenesisFinalizePreflight:
		return "finalize_preflight"
	case toolAgentGenesisFinalize:
		return "finalize"
	default:
		return "unknown"
	}
}

func authorizeGenesis(ctx context.Context, toolName string, write bool) (string, *mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actor, result, err := authenticatedAccountHolderActor(principal, toolName)
	if result != nil || err != nil {
		genesisAudit(ctx, toolName, actor, false)
		return actor, result, err
	}
	if write {
		if !principalHasWriteScope(principal) {
			genesisAudit(ctx, toolName, actor, false)
			return actor, mustToolErrorResult("insufficient_scope", toolName+" requires write scope", http.StatusForbidden, map[string]any{
				"requiredScopes": []string{"write"},
				"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
			}), nil
		}
	} else if !principalHasReadScope(principal) {
		genesisAudit(ctx, toolName, actor, false)
		return actor, mustToolErrorResult("insufficient_scope", toolName+" requires read scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"read"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		}), nil
	}
	if !instancex402.IsInstanceOperator(principal) {
		genesisAudit(ctx, toolName, actor, false)
		return actor, mustToolErrorResult("owner_operator_required", toolName+" requires explicit instance owner/operator OAuth authority; write scope alone is insufficient", http.StatusForbidden, map[string]any{
			"source":            "lesser_body_ptah",
			"requiredAuthority": "instance_owner_or_operator",
		}), nil
	}
	return actor, nil, nil
}

func (cfg config) genesisClientForTool(toolName string) (hostapi.GenesisClient, *mcpruntime.ToolResult, error) {
	client, err := cfg.genesis()
	if err != nil || client == nil {
		return nil, mustToolErrorResult("not_configured", "lesser-host genesis client is not configured", http.StatusInternalServerError, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolName,
		}), nil
	}
	return client, nil, nil
}

func hostGenesisBearer(ctx context.Context, toolName string) (string, *mcpruntime.ToolResult, error) {
	bearer, err := auth.LesserHostInstanceKey(ctx)
	if err != nil || strings.TrimSpace(bearer) == "" {
		return "", mustToolErrorResult("not_configured", "lesser-host instance authentication is not configured", http.StatusInternalServerError, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolName,
		}), nil
	}
	return strings.TrimSpace(bearer), nil, nil
}

func parseAgentGenesisBeginInput(args json.RawMessage) (agentGenesisBeginInput, *mcpruntime.ToolResult, error) {
	var in agentGenesisBeginInput
	if result := decodeGenesisInput(args, &in); result != nil {
		return in, result, nil
	}
	var err error
	if in.Domain, err = requiredGenesisString(in.Domain, "domain", 255); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_begin requires a valid domain", http.StatusBadRequest, nil), nil
	}
	if in.LocalID, err = requiredGenesisString(in.LocalID, "local_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_begin requires a valid local_id", http.StatusBadRequest, nil), nil
	}
	if len(in.Capabilities) > 64 {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_begin capabilities are too many", http.StatusBadRequest, nil), nil
	}
	capabilities := make([]string, 0, len(in.Capabilities))
	for _, capability := range in.Capabilities {
		capability, err = requiredGenesisString(capability, "capability", 64)
		if err != nil {
			return in, mustToolErrorResult("invalid_request", "agent_genesis_begin contains an invalid capability", http.StatusBadRequest, nil), nil
		}
		capabilities = append(capabilities, capability)
	}
	in.Capabilities = capabilities
	return in, nil, nil
}

func parseAgentGenesisAdvanceInput(args json.RawMessage) (agentGenesisAdvanceInput, *mcpruntime.ToolResult, error) {
	var in agentGenesisAdvanceInput
	if result := decodeGenesisInput(args, &in); result != nil {
		return in, result, nil
	}
	var err error
	if in.RegistrationID, err = requiredGenesisString(in.RegistrationID, "registration_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance requires a valid registration_id", http.StatusBadRequest, nil), nil
	}
	if in.ConversationID, err = optionalGenesisString(in.ConversationID, "conversation_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance conversation_id is invalid", http.StatusBadRequest, nil), nil
	}
	if in.Model, err = optionalGenesisString(in.Model, "model", 128); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance model is invalid", http.StatusBadRequest, nil), nil
	}
	if in.ConversationID == "" && in.Model == "" {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance requires model for the first conversation turn", http.StatusBadRequest, nil), nil
	}
	if in.Message, err = requiredGenesisMessage(in.Message); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance requires a bounded message", http.StatusBadRequest, nil), nil
	}
	if in.IdempotencyKey, err = optionalGenesisString(in.IdempotencyKey, "idempotency_key", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance idempotency_key is invalid", http.StatusBadRequest, nil), nil
	}
	if in.CorrelationID, err = optionalGenesisString(in.CorrelationID, "correlation_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance correlation_id is invalid", http.StatusBadRequest, nil), nil
	}
	return in, nil, nil
}

func parseAgentGenesisConversationInput(args json.RawMessage) (agentGenesisConversationInput, *mcpruntime.ToolResult, error) {
	var in agentGenesisConversationInput
	if result := decodeGenesisInput(args, &in); result != nil {
		return in, result, nil
	}
	var err error
	if in.RegistrationID, err = requiredGenesisString(in.RegistrationID, "registration_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "genesis tools require a valid registration_id", http.StatusBadRequest, nil), nil
	}
	if in.ConversationID, err = requiredGenesisString(in.ConversationID, "conversation_id", genesisMaxPathID); err != nil {
		return in, mustToolErrorResult("invalid_request", "genesis tools require a valid conversation_id", http.StatusBadRequest, nil), nil
	}
	return in, nil, nil
}

func decodeGenesisInput(args json.RawMessage, dst any) *mcpruntime.ToolResult {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return mustToolErrorResult("invalid_request", "invalid genesis tool arguments", http.StatusBadRequest, nil)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return mustToolErrorResult("invalid_request", "invalid genesis tool arguments", http.StatusBadRequest, nil)
	}
	return nil
}

func requiredGenesisString(value string, label string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return "", fmt.Errorf("%s is invalid", label)
	}
	return value, nil
}

func optionalGenesisString(value string, label string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > max {
		return "", fmt.Errorf("%s is invalid", label)
	}
	return value, nil
}

func requiredGenesisMessage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > genesisMaxMessageRunes {
		return "", errors.New("message is invalid")
	}
	return value, nil
}

func genesisSuccessResult(toolName string, operation string, raw map[string]any) (*mcpruntime.ToolResult, error) {
	data := sanitizeGenesisResponse(operation, raw)
	text := map[string]any{
		"summary":               "Host-backed Ptah genesis state updated",
		"operation":             operation,
		"source":                "lesser_host",
		"state_authority":       "Host HostedGenesisSession",
		"flow":                  "genesis_conversation",
		"existing_agent_create": false,
		"data":                  map[string]any{"location": "structuredContent.data"},
	}
	if conversation, ok := data["conversation"].(map[string]any); ok {
		for _, key := range []string{"registration_id", "conversation_id", "agent_id", "status"} {
			if value, present := conversation[key]; present {
				text[key] = value
			}
		}
	}
	if registration, ok := data["registration"].(map[string]any); ok {
		for _, key := range []string{"registration_id", "agent_id", "authority_model"} {
			if value, present := registration[key]; present {
				text[key] = value
			}
		}
	}
	if agentID, ok := data["agent_id"].(string); ok && agentID != "" {
		text["agent_id"] = agentID
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func genesisToolResultFromError(toolName string, err error) (*mcpruntime.ToolResult, error) {
	status := http.StatusBadGateway
	code := "host_genesis_unavailable"
	details := map[string]any{
		"source": "lesser_host_genesis",
		"tool":   toolName,
	}
	var apiErr *hostapi.APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		status = apiErr.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
		switch status {
		case http.StatusBadRequest:
			code = "invalid_request"
		case http.StatusUnauthorized:
			code = "unauthorized"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "conflict"
		}
		if apiErr.Code != "" {
			details["hostCode"] = apiErr.Code
		}
	}
	return toolErrorResult(code, "lesser-host genesis request failed", status, details)
}

func genesisAudit(ctx context.Context, toolName string, actor string, allowed bool) {
	attrs := []any{
		"tool", toolName,
		"allowed", allowed,
		"authority", "instance_owner_or_operator",
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		sum := sha256.Sum256([]byte(strings.ToLower(actor)))
		attrs = append(attrs, "actor_hash", hex.EncodeToString(sum[:]))
	}
	if allowed {
		slog.InfoContext(ctx, "ptah genesis tool invocation", attrs...)
		return
	}
	slog.WarnContext(ctx, "ptah genesis authorization rejected", attrs...)
}

func sanitizeGenesisResponse(operation string, raw map[string]any) map[string]any {
	data := map[string]any{
		"source":                "lesser_host",
		"state_authority":       "Host HostedGenesisSession",
		"flow":                  "genesis_conversation",
		"existing_agent_create": false,
	}
	switch operation {
	case "begin":
		if registration := sanitizeGenesisRegistration(mapValue(raw, "registration")); len(registration) > 0 {
			data["registration"] = registration
			if id := stringValue(registration, "registration_id"); id != "" {
				data["registration_id"] = id
			}
			if id := stringValue(registration, "agent_id"); id != "" {
				data["agent_id"] = id
			}
		}
		if promotion := sanitizeGenesisPromotion(mapValue(raw, "promotion")); len(promotion) > 0 {
			data["promotion"] = promotion
		}
	default:
		conversation := mapValue(raw, "conversation")
		if len(conversation) == 0 {
			conversation = raw
		}
		if safe := sanitizeGenesisConversation(conversation); len(safe) > 0 {
			data["conversation"] = safe
		}
		if agentID := firstString(raw, "agent_id", "agentId"); agentID != "" {
			data["agent_id"] = agentID
		}
		if operation == "finalize" {
			data = sanitizeGenesisFinalize(data, raw)
		} else if operation == "finalize_preflight" {
			data = sanitizeGenesisPreflight(data, raw)
		}
	}
	return data
}

func sanitizeGenesisRegistration(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	copyStringField(out, "registration_id", raw, "id", "registration_id", "registrationId")
	copyStringField(out, "agent_id", raw, "agent_id", "agentId")
	copyStringField(out, "domain", raw, "domain_normalized", "domain")
	copyStringField(out, "local_id", raw, "local_id", "localId")
	copyStringField(out, "authority_model", raw, "authority_model", "authorityModel")
	copyStringField(out, "status", raw, "status")
	copyBoolField(out, "dns_verified", raw, "dns_verified", "dnsVerified")
	copyBoolField(out, "https_verified", raw, "https_verified", "httpsVerified")
	if capabilities := stringSliceField(raw, "capabilities"); len(capabilities) > 0 {
		out["capabilities"] = capabilities
	}
	return out
}

func sanitizeGenesisPromotion(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	for output, keys := range map[string][]string{
		"agent_id":                   {"agent_id", "agentId"},
		"registration_id":            {"registration_id", "registrationId"},
		"stage":                      {"stage"},
		"request_status":             {"request_status", "requestStatus"},
		"review_status":              {"review_status", "reviewStatus"},
		"readiness_status":           {"readiness_status", "readinessStatus"},
		"authority_model":            {"authority_model", "authorityModel"},
		"anchor_state":               {"anchor_state", "anchorState"},
		"latest_conversation_id":     {"latest_conversation_id", "latestConversationId"},
		"latest_conversation_status": {"latest_conversation_status", "latestConversationStatus"},
		"published_version":          {"published_version", "publishedVersion"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			out[output] = value
		}
	}
	return out
}

func sanitizeGenesisConversation(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	for output, keys := range map[string][]string{
		"registration_id":    {"registration_id", "registrationId"},
		"conversation_id":    {"conversation_id", "conversationId"},
		"agent_id":           {"agent_id", "agentId"},
		"status":             {"status"},
		"latest_turn_id":     {"latest_turn_id", "latestTurnId"},
		"message_count":      {"message_count", "messageCount"},
		"messages_truncated": {"messages_truncated", "messagesTruncated"},
		"request_id":         {"request_id", "requestId"},
		"poll_after_seconds": {"poll_after_seconds", "pollAfterSeconds"},
		"created_at":         {"created_at", "createdAt"},
		"updated_at":         {"updated_at", "updatedAt"},
		"completed_at":       {"completed_at", "completedAt"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			out[output] = value
		}
	}
	if messages, truncated := sanitizeGenesisMessages(raw["messages"]); len(messages) > 0 {
		out["messages"] = messages
		if truncated {
			// The Host projection is bounded, but Body still returns only the
			// latest turn so a tools/call result cannot become a full transcript.
			out["messages_truncated"] = true
		}
	}
	if produced := sanitizeGenesisProducedDeclarations(mapValue(raw, "produced_declarations", "producedDeclarations")); len(produced) > 0 {
		out["produced_declarations"] = produced
	}
	if failure := sanitizeGenesisFailure(mapValue(raw, "failure")); len(failure) > 0 {
		out["failure"] = failure
	}
	if traceIDs := sanitizeGenesisTraceIDs(mapValue(raw, "trace_ids", "traceIds")); len(traceIDs) > 0 {
		out["trace_ids"] = traceIDs
	}
	return out
}

func sanitizeGenesisMessages(raw any) ([]map[string]any, bool) {
	items, _ := raw.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		message := mapValue(item)
		if len(message) == 0 {
			continue
		}
		entry := map[string]any{}
		copyStringField(entry, "id", message, "id")
		copyStringField(entry, "role", message, "role")
		copyStringField(entry, "content", message, "content")
		copyIntField(entry, "order", message, "order")
		copyStringField(entry, "created_at", message, "created_at", "createdAt")
		copyBoolField(entry, "truncated", message, "truncated")
		if _, ok := entry["content"]; ok {
			out = append(out, entry)
		}
	}
	if len(out) > 1 {
		return out[len(out)-1:], true
	}
	return out, false
}

func sanitizeGenesisProducedDeclarations(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{"ready": true}
	copyStringField(out, "declaration_id", raw, "declaration_id", "declarationId")
	copyStringField(out, "declaration_hash", raw, "declaration_hash", "declarationHash")
	copyStringField(out, "produced_at", raw, "produced_at", "producedAt")
	if evidence := mapValue(raw, "evidence"); len(evidence) > 0 {
		safeEvidence := map[string]any{}
		for output, keys := range map[string][]string{
			"source":          {"source"},
			"registration_id": {"registration_id", "registrationId"},
			"conversation_id": {"conversation_id", "conversationId"},
			"agent_id":        {"agent_id", "agentId"},
			"message_count":   {"message_count", "messageCount"},
			"model":           {"model"},
			"request_id":      {"request_id", "requestId"},
		} {
			if value, ok := firstField(evidence, keys...); ok && safeScalar(value) {
				safeEvidence[output] = value
			}
		}
		if len(safeEvidence) > 0 {
			out["evidence"] = safeEvidence
		}
	}
	return out
}

func sanitizeGenesisFailure(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	copyStringField(out, "code", raw, "code")
	copyBoolField(out, "retryable", raw, "retryable")
	if recovery := mapValue(raw, "recovery"); len(recovery) > 0 {
		safeRecovery := map[string]any{}
		copyStringField(safeRecovery, "action", recovery, "action")
		copyIntField(safeRecovery, "max_attempts", recovery, "max_attempts", "maxAttempts")
		copyIntField(safeRecovery, "retry_after_seconds", recovery, "retry_after_seconds", "retryAfterSeconds")
		if len(safeRecovery) > 0 {
			out["recovery"] = safeRecovery
		}
	}
	return out
}

func sanitizeGenesisTraceIDs(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	for output, keys := range map[string][]string{
		"host_request_id":   {"host_request_id", "hostRequestId"},
		"correlation_id":    {"correlation_id", "correlationId"},
		"idempotency_key":   {"idempotency_key", "idempotencyKey"},
		"lesser_request_id": {"lesser_request_id", "lesserRequestId"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			out[output] = value
		}
	}
	return out
}

func sanitizeGenesisPreflight(data map[string]any, raw map[string]any) map[string]any {
	data = cloneMap(data)
	for output, keys := range map[string][]string{
		"version":          {"version"},
		"authority_model":  {"authority_model", "authorityModel"},
		"anchor_state":     {"anchor_state", "anchorState"},
		"issued_at":        {"issued_at", "issuedAt"},
		"expected_version": {"expected_version", "expectedVersion"},
		"next_version":     {"next_version", "nextVersion"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			data[output] = value
		}
	}
	if requirements, ok := raw["boundary_requirements"].([]any); ok {
		data["boundary_requirement_count"] = len(requirements)
	}
	return data
}

func sanitizeGenesisFinalize(data map[string]any, raw map[string]any) map[string]any {
	data = cloneMap(data)
	for output, keys := range map[string][]string{
		"version":           {"version"},
		"agent_id":          {"agent_id", "agentId"},
		"published_version": {"published_version", "publishedVersion"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			data[output] = value
		}
	}
	if publication := sanitizeGenesisPromotion(mapValue(raw, "publication")); len(publication) > 0 {
		data["publication"] = publication
	}
	if promotion := sanitizeGenesisPromotion(mapValue(raw, "promotion")); len(promotion) > 0 {
		data["promotion"] = promotion
	}
	return data
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func mapValue(value any, keys ...string) map[string]any {
	if len(keys) == 0 {
		output, _ := value.(map[string]any)
		return output
	}
	m, _ := value.(map[string]any)
	for _, key := range keys {
		if nested, ok := m[key].(map[string]any); ok {
			return nested
		}
	}
	return nil
}

func firstField(m map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := m[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func firstString(m map[string]any, keys ...string) string {
	value, _ := firstField(m, keys...)
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringValue(m map[string]any, key string) string {
	text, _ := m[key].(string)
	return strings.TrimSpace(text)
}

func copyStringField(out map[string]any, output string, in map[string]any, keys ...string) {
	if value := firstString(in, keys...); value != "" {
		out[output] = value
	}
}

func copyBoolField(out map[string]any, output string, in map[string]any, keys ...string) {
	if value, ok := firstField(in, keys...); ok {
		if boolean, ok := value.(bool); ok {
			out[output] = boolean
		}
	}
}

func copyIntField(out map[string]any, output string, in map[string]any, keys ...string) {
	if value, ok := firstField(in, keys...); ok {
		switch number := value.(type) {
		case int:
			out[output] = number
		case int64:
			out[output] = number
		case float64:
			out[output] = int(number)
		}
	}
}

func stringSliceField(in map[string]any, key string) []string {
	values, _ := in[key].([]any)
	if len(values) == 0 {
		if stringsValues, ok := in[key].([]string); ok {
			return append([]string(nil), stringsValues...)
		}
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

func safeScalar(value any) bool {
	switch value.(type) {
	case string, bool, int, int64, float64:
		return true
	default:
		return false
	}
}
