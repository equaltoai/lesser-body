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

	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	toolAgentGenesisBegin             = "agent_genesis_begin"
	toolAgentGenesisList              = "agent_genesis_list"
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
			"data":{
				"type":"object",
				"description":"Sanitized Host-backed genesis state. Conversation messages, when present, are structured data only.",
				"properties":{
					"operation":{"type":"string","enum":["begin","list","read","advance","recover","complete","finalize_preflight","finalize"]},
					"status":{"type":"string","enum":["begin","not_available","assistant_turn_ready","awaiting_owner","needs_owner_turn","in_progress","declaration_ready","ready_for_completion","complete","completed","finalization_ready","preflight_ok","ready_to_finalize","finalize_ready","published","finalized","active","failed","restart_soul_bootstrap","read","advance","recover","finalize_preflight","finalize","unknown"]},
					"failure":{"type":"object","properties":{
						"code":{"type":"string","enum":["producer_contract_missing","missing_produced_declarations","invalid_produced_declarations","soul_bootstrap_restart_required","host_genesis_unavailable","invalid_request","unauthorized","forbidden","not_found","conflict","not_configured","insufficient_scope","owner_operator_required","host_genesis_projection_invalid","agent_registry_error"]},
						"recovery":{"type":"object","properties":{"action":{"type":"string","enum":["restart_soul_bootstrap","retry","wait","contact_operator"]}}}
					}},
					"guidance":{"type":"object","properties":{"next_tool":{"type":"string"},"alternate_next_tool":{"type":"string"},"status":{"type":"string"},"fresh_lane":{"type":"boolean"},"instruction":{"type":"string"}}}
				}
			},
			"error":{"type":"object","description":"Structured tool error when isError=true.","properties":{"code":{"type":"string","enum":["host_genesis_unavailable","invalid_request","unauthorized","forbidden","not_found","conflict","not_configured","insufficient_scope","owner_operator_required","host_genesis_projection_invalid","agent_registry_error"]}}}
		}
	}`)
}

func agentGenesisBeginDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisBegin,
		Title:       "Begin Host-backed agent genesis",
		Description: "Begin a new agent registration through lesser-host's instance-trust genesis flow. Host derives the new agent identity; this tool does not delegate or require a pre-existing Lesser agent account. Next: call agent_genesis_advance with the returned registration_id and persist the Host conversation_id. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
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

func agentGenesisListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisList,
		Title:       "List Host-backed agent genesis conversations",
		Description: "Advertise the safe recovery/listing status for Host-backed Ptah genesis conversations. Body does not fabricate a local genesis list. Until Body has a Host list client surface for Host's instance mint-conversation summary endpoint, this tool returns status=not_available with failure.code=producer_contract_missing and directs callers to known registration_id/conversation_id reads or finalized agent registry visibility. Requires explicit instance owner/operator OAuth authority and read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Optional Host/Lesser agent id whose conversations would be listed when a producer client surface exists."},
				"limit":{"type":"integer","minimum":1,"maximum":50,"description":"Optional future page size; currently accepted only for forward-compatible clients."}
			},
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","properties":{
					"operation":{"type":"string","enum":["list"]},
					"status":{"type":"string","enum":["not_available"]},
					"conversations":{"type":"array","items":{"type":"object"}},
					"failure":{"type":"object","properties":{"code":{"type":"string","enum":["producer_contract_missing"]}}},
					"guidance":{"type":"object","properties":{"next_tool":{"type":"string","enum":["agent_genesis_read"]},"alternate_next_tool":{"type":"string","enum":["agent_list"]},"instruction":{"type":"string"}}}
				}},
				"error":{"type":"object","description":"Structured authorization/input error when isError=true."}
			}
		}`),
	}
}

func agentGenesisReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisRead,
		Title:        "Read Host-backed agent genesis",
		Description:  "Read the durable lesser-host HostedGenesisSession projection for an in-progress or completed Ptah genesis conversation. Follow structuredContent.data.guidance for state to next tool: advance, complete, preflight, finalize, or fresh begin. Requires explicit instance owner/operator OAuth authority and read scope; the Host projection is the source of truth.",
		Annotations:  readOnlyToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisAdvanceDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisAdvance,
		Title:       "Advance Host-backed agent genesis",
		Description: "Submit the owner's next message to lesser-host's durable genesis/mint conversation. Persist and reuse the Host conversation_id returned by the first call; this is a new-agent genesis flow, not existing-agent delegation. Next states: continue advance/read while in progress, call agent_genesis_complete when Host reports declaration_ready. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
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
		Description:  "Ask lesser-host to reconcile or safely retry a typed recovery state for the durable genesis conversation. Host owns the recovery state machine; Body never reruns or substitutes the conversation locally. If Host returns failure.recovery.action=restart_soul_bootstrap, do not call recover; call agent_genesis_begin again for a fresh lane. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  additiveMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisCompleteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisComplete,
		Title:        "Complete Host-backed agent genesis",
		Description:  "Ask lesser-host to perform the durable declaration-extraction handoff for the genesis conversation. Body sends no caller-supplied declarations; Host's produced_declarations checkpoint remains authoritative. Next: call agent_genesis_finalize_preflight, then agent_genesis_finalize after Host readiness. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  additiveMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisFinalizePreflightDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisFinalizePreflight,
		Title:        "Preflight Host-backed agent genesis finalization",
		Description:  "Read lesser-host's finalization readiness for a declaration-ready genesis conversation. Instance-trust finalization does not require a wallet signature or self-attestation. Next: call agent_genesis_finalize only after Host readiness. Requires explicit instance owner/operator OAuth authority and write scope.",
		Annotations:  idempotentMutationToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisFinalizeDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         toolAgentGenesisFinalize,
		Title:        "Finalize Host-backed agent genesis",
		Description:  "Finalize and publish the Host-owned instance-trust genesis result. Host publishes the new agent identity; Body then idempotently writes a Host-derived Ptah registry row for agent_get/agent_list visibility. Body does not sign a wallet, accept a private declaration, or delegate an existing Lesser account. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
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

func (cfg config) handleAgentGenesisList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisList, false)
	if result != nil || err != nil {
		return result, err
	}
	var in struct {
		AgentID string `json:"agent_id,omitempty"`
		Limit   *int   `json:"limit,omitempty"`
	}
	if result := decodeGenesisInput(args, &in); result != nil {
		return result, nil
	}
	if in.AgentID, err = optionalGenesisString(in.AgentID, "agent_id", genesisMaxPathID); err != nil {
		return mustToolErrorResult("invalid_request", "agent_genesis_list agent_id is invalid", http.StatusBadRequest, nil), nil
	}
	if in.Limit != nil && (*in.Limit < 1 || *in.Limit > 50) {
		return mustToolErrorResult("invalid_request", "agent_genesis_list limit must be between 1 and 50", http.StatusBadRequest, nil), nil
	}
	genesisAudit(ctx, toolAgentGenesisList, actor, true)
	data := map[string]any{
		"source":          "lesser_body_ptah",
		"state_authority": "Host HostedGenesisSession",
		"flow":            "genesis_conversation",
		"operation":       "list",
		"status":          "not_available",
		"conversations":   []map[string]any{},
		"failure": map[string]any{
			"code":   "producer_contract_missing",
			"source": "lesser_body_hostapi",
			"reason": "Host exposes an instance mint-conversation summary endpoint, but Body has no checked Host list client surface in this milestone. Body will not fabricate local genesis state.",
		},
		"guidance": map[string]any{
			"next_tool":           toolAgentGenesisRead,
			"alternate_next_tool": toolAgentList,
			"instruction":         "Use a known registration_id/conversation_id with agent_genesis_read, or use agent_list after agent_genesis_finalize writes the Host-derived Ptah registry row. Track a follow-up to add a Body Host list client for /api/v1/soul/instance/agents/{agentId}/mint-conversations.",
		},
		"producer_contract": map[string]any{
			"host_pr":          fiveBodyHostPR,
			"host_head_sha":    fiveBodyHostHeadSHA,
			"host_endpoint":    "GET /api/v1/soul/instance/agents/{agentId}/mint-conversations",
			"body_client_gap":  "internal/hostapi.GenesisClient has no list method yet",
			"safe_behavior":    "not_available_without_local_state",
			"model_allowlist":  "producer_contract_missing",
			"schema_version":   fiveBodySchemaVersion,
			"guidance_version": fiveBodyGuidanceVersion,
		},
	}
	if in.AgentID != "" {
		data["agent_id"] = in.AgentID
	}
	if in.Limit != nil {
		data["limit"] = *in.Limit
	}
	text := map[string]any{
		"summary":         "Host-backed Ptah genesis list is not yet available through Body",
		"operation":       "list",
		"status":          "not_available",
		"source":          "lesser_body_ptah",
		"state_authority": "Host HostedGenesisSession",
		"failure":         data["failure"],
		"guidance":        data["guidance"],
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
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
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisFinalize, true)
	if result != nil || err != nil {
		return result, err
	}
	in, result, err := parseAgentGenesisConversationInput(args)
	if result != nil || err != nil {
		return result, err
	}
	client, result, err := cfg.genesisClientForTool(toolAgentGenesisFinalize)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolAgentGenesisFinalize)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolAgentGenesisFinalize, actor, true)
	raw, err := client.FinalizeConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisFinalize, err)
	}
	data := sanitizeGenesisResponse("finalize", raw)
	registryAgent, created, result, err := cfg.registerFinalizedGenesisAgent(ctx, actor, in, data)
	if result != nil || err != nil {
		return result, err
	}
	if registryAgent != nil {
		data["registry"] = registryAgentSummary(registryAgent)
		data["registry_write"] = map[string]any{
			"source":     "agent_registry",
			"created":    created,
			"idempotent": true,
		}
	}
	return genesisSuccessResultFromData(toolAgentGenesisFinalize, "finalize", data)
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
	case toolAgentGenesisList:
		return "list"
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
	return genesisSuccessResultFromData(toolName, operation, data)
}

func genesisSuccessResultFromData(toolName string, operation string, data map[string]any) (*mcpruntime.ToolResult, error) {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["operation"]; !ok {
		data["operation"] = operation
	}
	if guidance := genesisNextToolGuidance(operation, data); len(guidance) > 0 {
		data["guidance"] = guidance
	}
	if strings.TrimSpace(stringValue(data, "status")) == "" {
		data["status"] = genesisCanonicalStatus(operation, data)
	}
	text := map[string]any{
		"summary":         "Host-backed Ptah genesis state updated",
		"operation":       operation,
		"source":          "lesser_host",
		"state_authority": "Host HostedGenesisSession",
		"flow":            "genesis_conversation",
		"data":            map[string]any{"location": "structuredContent.data"},
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
	if registry, ok := data["registry"].(map[string]any); ok && len(registry) > 0 {
		text["registry"] = registry
	}
	if guidance, ok := data["guidance"].(map[string]any); ok && len(guidance) > 0 {
		text["guidance"] = guidance
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func genesisCanonicalStatus(operation string, data map[string]any) string {
	if status := strings.TrimSpace(stringValue(data, "status")); status != "" {
		return strings.ToLower(status)
	}
	if status := strings.TrimSpace(nestedString(data, "conversation", "status")); status != "" {
		return strings.ToLower(status)
	}
	if status := strings.TrimSpace(nestedString(data, "registration", "status")); status != "" {
		return strings.ToLower(status)
	}
	if guidance := nestedMap(data, "guidance"); len(guidance) > 0 {
		if status := strings.TrimSpace(stringValue(guidance, "status")); status != "" {
			return strings.ToLower(status)
		}
	}
	return strings.ToLower(strings.TrimSpace(firstNonEmpty(operation, "unknown")))
}

func (cfg config) registerFinalizedGenesisAgent(ctx context.Context, actor string, in agentGenesisConversationInput, data map[string]any) (*agentregistry.Agent, bool, *mcpruntime.ToolResult, error) {
	agentID := finalizedGenesisAgentID(data)
	if agentID == "" {
		return nil, false, mustToolErrorResult("host_genesis_projection_invalid", "lesser-host finalized genesis response did not include a safe agent_id for registry visibility", http.StatusBadGateway, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	registry, err := cfg.registry()
	if err != nil || registry == nil {
		return nil, false, mustToolErrorResult("not_configured", "Body/Ptah agent registry is required to expose Host-finalized minted agents", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}

	registrationID := firstNonEmpty(
		in.RegistrationID,
		nestedString(data, "conversation", "registration_id"),
		nestedString(data, "publication", "registration_id"),
		nestedString(data, "promotion", "registration_id"),
		stringValue(data, "registration_id"),
	)
	conversationID := firstNonEmpty(
		in.ConversationID,
		nestedString(data, "conversation", "conversation_id"),
		nestedString(data, "publication", "latest_conversation_id"),
		nestedString(data, "promotion", "latest_conversation_id"),
	)

	agent, created, err := registry.UpsertFinalized(ctx, agentregistry.FinalizedInput{
		Account:            actor,
		AgentID:            agentID,
		HostRegistrationID: registrationID,
		HostConversationID: conversationID,
		Domain:             firstNonEmpty(nestedString(data, "publication", "domain"), nestedString(data, "promotion", "domain"), stringValue(data, "domain")),
		LocalID:            firstNonEmpty(nestedString(data, "publication", "local_id"), nestedString(data, "promotion", "local_id"), stringValue(data, "local_id")),
		AuthorityModel:     firstNonEmpty(nestedString(data, "publication", "authority_model"), nestedString(data, "promotion", "authority_model"), stringValue(data, "authority_model")),
		AnchorState:        firstNonEmpty(nestedString(data, "publication", "anchor_state"), nestedString(data, "promotion", "anchor_state"), stringValue(data, "anchor_state")),
		LifecycleStatus:    firstNonEmpty(nestedString(data, "publication", "lifecycle_status"), nestedString(data, "promotion", "lifecycle_status"), stringValue(data, "lifecycle_status")),
		PublishedVersion:   firstNonZeroInt64(nestedInt64(data, "publication", "published_version"), nestedInt64(data, "promotion", "published_version"), int64Value(data["published_version"])),
	})
	if err != nil {
		if errors.Is(err, agentregistry.ErrAgentAlreadyExists) {
			existing, getErr := registry.Get(ctx, actor, agentID)
			if getErr == nil {
				return existing, false, nil, nil
			}
		}
		slog.WarnContext(ctx, "ptah genesis finalized registry write failed",
			"tool", toolAgentGenesisFinalize,
			"source", "agent_registry",
			"error", "registry write failed",
		)
		return nil, false, mustToolErrorResult("agent_registry_error", "Host finalized the genesis agent, but Body failed to write the account-scoped Ptah registry row; retry agent_genesis_finalize to repair visibility", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	return agent, created, nil, nil
}

func finalizedGenesisAgentID(data map[string]any) string {
	return firstNonEmpty(
		stringValue(data, "agent_id"),
		nestedString(data, "conversation", "agent_id"),
		nestedString(data, "publication", "agent_id"),
		nestedString(data, "promotion", "agent_id"),
	)
}

func genesisNextToolGuidance(operation string, data map[string]any) map[string]any {
	if guidance := restartSoulBootstrapGuidance(data); len(guidance) > 0 {
		return guidance
	}
	status := strings.ToLower(firstNonEmpty(
		nestedString(data, "conversation", "status"),
		nestedString(data, "publication", "latest_conversation_status"),
		nestedString(data, "promotion", "latest_conversation_status"),
		stringValue(data, "status"),
	))
	guidance := map[string]any{
		"status":     firstNonEmpty(status, operation),
		"fresh_lane": false,
	}
	switch {
	case operation == "begin":
		guidance["next_tool"] = toolAgentGenesisAdvance
		guidance["instruction"] = "Call agent_genesis_advance with the returned registration_id and the first owner/operator message; persist the returned conversation_id."
	case status == "assistant_turn_ready" || status == "awaiting_owner" || status == "needs_owner_turn" || status == "in_progress":
		guidance["next_tool"] = toolAgentGenesisAdvance
		guidance["instruction"] = "Continue the Host-owned mint conversation with agent_genesis_advance, or poll with agent_genesis_read before deciding."
	case status == "declaration_ready" || status == "ready_for_completion":
		guidance["next_tool"] = toolAgentGenesisComplete
		guidance["instruction"] = "Call agent_genesis_complete so Host extracts and validates its durable produced_declarations checkpoint."
	case operation == "complete" || status == "complete" || status == "completed" || status == "finalization_ready":
		guidance["next_tool"] = toolAgentGenesisFinalizePreflight
		guidance["instruction"] = "Call agent_genesis_finalize_preflight, then agent_genesis_finalize only after Host reports readiness."
	case operation == "finalize_preflight" || status == "preflight_ok" || status == "ready_to_finalize" || status == "finalize_ready":
		guidance["next_tool"] = toolAgentGenesisFinalize
		guidance["instruction"] = "Call agent_genesis_finalize; Body will write a Host-derived Ptah registry row after Host publishes the identity."
	case operation == "finalize" || status == "published" || status == "finalized" || status == "active":
		guidance["next_tool"] = toolAgentGet
		guidance["alternate_next_tool"] = toolAgentList
		guidance["instruction"] = "Verify the minted agent with agent_get for the returned agent_id, or agent_list for the account-scoped registry view."
	default:
		guidance["next_tool"] = toolAgentGenesisRead
		guidance["instruction"] = "Poll agent_genesis_read and follow the Host status; Body does not substitute a local genesis state machine."
	}
	return guidance
}

func restartSoulBootstrapGuidance(data map[string]any) map[string]any {
	recovery := nestedMap(data, "conversation", "failure", "recovery")
	if len(recovery) == 0 {
		recovery = nestedMap(data, "failure", "recovery")
	}
	action := strings.ToLower(stringValue(recovery, "action"))
	if action != "restart_soul_bootstrap" {
		return nil
	}
	out := map[string]any{
		"status":      "restart_soul_bootstrap",
		"next_tool":   toolAgentGenesisBegin,
		"fresh_lane":  true,
		"instruction": "Host requested restart_soul_bootstrap: call agent_genesis_begin again for a fresh genesis lane using the intended domain/local_id. Do not call agent_genesis_recover for this action.",
	}
	if reason := stringValue(recovery, "reason"); reason != "" {
		out["reason"] = reason
	}
	return out
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
		"source":          "lesser_host",
		"state_authority": "Host HostedGenesisSession",
		"flow":            "genesis_conversation",
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
		"domain":                     {"domain_normalized", "domain"},
		"local_id":                   {"local_id", "localId"},
		"stage":                      {"stage"},
		"request_status":             {"request_status", "requestStatus"},
		"review_status":              {"review_status", "reviewStatus"},
		"readiness_status":           {"readiness_status", "readinessStatus"},
		"authority_model":            {"authority_model", "authorityModel"},
		"anchor_state":               {"anchor_state", "anchorState"},
		"operational_binding":        {"operational_binding", "operationalBinding"},
		"lifecycle_status":           {"lifecycle_status", "lifecycleStatus", "status"},
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
	copyStringField(out, "schema_version", raw, "schema_version", "schemaVersion")
	copyStringField(out, "guidance_version", raw, "guidance_version", "guidanceVersion")
	if review := mapValue(raw, "adversarial_review", "adversarialReview"); len(review) > 0 {
		safeReview := map[string]any{}
		copyStringField(safeReview, "version", review, "version")
		copyStringField(safeReview, "reviewer", review, "reviewer")
		copyStringField(safeReview, "result", review, "result")
		if len(safeReview) > 0 {
			out["adversarial_review"] = safeReview
		}
	}
	if evidence := mapValue(raw, "evidence"); len(evidence) > 0 {
		safeEvidence := map[string]any{}
		for output, keys := range map[string][]string{
			"source":           {"source"},
			"registration_id":  {"registration_id", "registrationId"},
			"conversation_id":  {"conversation_id", "conversationId"},
			"agent_id":         {"agent_id", "agentId"},
			"message_count":    {"message_count", "messageCount"},
			"model":            {"model"},
			"request_id":       {"request_id", "requestId"},
			"schema_version":   {"schema_version", "schemaVersion"},
			"guidance_version": {"guidance_version", "guidanceVersion"},
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
		copySafeRecoveryReasonField(safeRecovery, recovery)
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
		"domain":            {"domain_normalized", "domain"},
		"local_id":          {"local_id", "localId"},
		"authority_model":   {"authority_model", "authorityModel"},
		"anchor_state":      {"anchor_state", "anchorState"},
		"lifecycle_status":  {"lifecycle_status", "lifecycleStatus", "status"},
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

func nestedString(m map[string]any, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	nested := nestedMap(m, keys[:len(keys)-1]...)
	if len(nested) == 0 {
		return ""
	}
	return stringValue(nested, keys[len(keys)-1])
}

func nestedInt64(m map[string]any, keys ...string) int64 {
	if len(keys) == 0 {
		return 0
	}
	nested := nestedMap(m, keys[:len(keys)-1]...)
	if len(nested) == 0 {
		return 0
	}
	return int64Value(nested[keys[len(keys)-1]])
}

func nestedMap(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		if len(current) == 0 {
			return nil
		}
		next, _ := current[key].(map[string]any)
		current = next
	}
	return current
}

func int64Value(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int64:
		return number
	case float64:
		return int64(number)
	default:
		return 0
	}
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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

func copySafeRecoveryReasonField(out map[string]any, recovery map[string]any) {
	reason := firstString(recovery, "reason")
	if reason == "" {
		return
	}
	if safeRecoveryReason(reason) == "" {
		return
	}
	out["reason"] = safeRecoveryReason(reason)
}

func safeRecoveryReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 256 {
		return ""
	}
	lower := strings.ToLower(reason)
	for _, forbidden := range []string{
		"bearer ",
		"token",
		"secret",
		"signature",
		"transcript",
		"declaration",
		"wallet",
		"private",
	} {
		if strings.Contains(lower, forbidden) {
			return ""
		}
	}
	return reason
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
