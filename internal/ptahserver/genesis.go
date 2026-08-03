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
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/equaltoai/lesser-body/internal/actorendpoint"
	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const (
	toolAgentGenesisBegin             = "agent_genesis_begin"
	toolAgentGenesisList              = "agent_genesis_list"
	toolAgentGenesisRead              = "agent_genesis_read"
	toolAgentGenesisAdvance           = "agent_genesis_advance"
	toolAgentGenesisRecover           = "agent_genesis_recover"
	toolAgentGenesisFinalizePreflight = "agent_genesis_finalize_preflight"
	toolAgentGenesisFinalize          = "agent_genesis_finalize"

	genesisMaxMessageRunes = 8192
	genesisMaxReviewRunes  = 65536
	genesisMaxPathID       = 128

	// lesser-host HostedGenesisSession Failure.Validate bounds retry guidance
	// to one hour. Body preserves valid Host values and never clamps them into
	// a different recovery instruction.
	genesisMaxRecoveryRetryAfterSeconds = 3600
)

type agentGenesisBeginInput struct {
	Domain       string   `json:"domain"`
	LocalID      string   `json:"local_id"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type agentGenesisAdvanceInput struct {
	RegistrationID  string                              `json:"registration_id"`
	ConversationID  string                              `json:"conversation_id,omitempty"`
	Model           string                              `json:"model,omitempty"`
	Message         string                              `json:"message"`
	CandidateAction *hostapi.DeclarationCandidateAction `json:"candidate_action,omitempty"`
	IdempotencyKey  string                              `json:"idempotency_key,omitempty"`
	CorrelationID   string                              `json:"correlation_id,omitempty"`
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
					"operation":{"type":"string","enum":["begin","list","read","advance","recover","finalize_preflight","finalize"]},
					"status":{"type":"string","description":"Canonical lane status. agent_genesis_begin reports lesser-host's SoulAgentRegistration vocabulary (pending, completed); the conversation tools report Host's HostedGenesisSession vocabulary. Values Body cannot classify are reported as unknown.","enum":["begin","pending","completed","not_available","created","assistant_turn_ready","in_progress","declaration_ready","published","failed","restart_soul_bootstrap","read","advance","recover","finalize_preflight","finalize","unknown"]},
					"failure":{"type":"object","properties":{
							"code":{"type":"string","enum":["llm_unavailable","assistant_turn_failed","invalid_completion_state","missing_produced_declarations","invalid_produced_declarations","tenant_boundary_violation","operator_action_required","microvm_unavailable","producer_contract_missing","soul_bootstrap_restart_required","host_genesis_unavailable","invalid_request","unauthorized","forbidden","not_found","conflict","not_configured","insufficient_scope","owner_operator_required","host_genesis_projection_invalid","host_genesis_declaration_invalid","agent_registry_error","agent_soul_seed_error","agent_instructions_seed_error"]},
						"recovery":{"type":"object","properties":{
							"action":{"type":"string","enum":["refresh_state","retry_same_step","restart_soul_bootstrap","operator_action"]},
							"max_attempts":{"type":"integer","minimum":0,"maximum":10},
							"retry_after_seconds":{"type":"integer","minimum":0,"maximum":3600},
							"reason":{"type":"string","maxLength":256}
						}}
					}},
					"conversation":{"type":"object","description":"Sanitized compact Host HostedGenesisSession projection.","properties":{
						"failure":{"type":"object","properties":{
							"code":{"type":"string","enum":["llm_unavailable","assistant_turn_failed","invalid_completion_state","missing_produced_declarations","invalid_produced_declarations","tenant_boundary_violation","operator_action_required","microvm_unavailable"]},
							"recovery":{"type":"object","properties":{
								"action":{"type":"string","enum":["refresh_state","retry_same_step","restart_soul_bootstrap","operator_action"]},
								"max_attempts":{"type":"integer","minimum":0,"maximum":10},
								"retry_after_seconds":{"type":"integer","minimum":0,"maximum":3600},
								"reason":{"type":"string","maxLength":256}
							}}
						}},
						"declaration_candidate":{"type":"object","additionalProperties":false,"properties":{
							"version":{"type":"string","const":"hosted-genesis-declaration-candidate.v1"},
							"phase":{"type":"string","enum":["section","review","affirmed","finalized"]},
							"current_section":{"type":"string","enum":["identity","philosophy","discipline","boundaries","soul"]},
							"completed_sections":{"type":"array","maxItems":5,"uniqueItems":true,"items":{"type":"string","enum":["identity","philosophy","discipline","boundaries","soul"]}},
							"revision":{"type":"integer","minimum":0},
							"candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
							"review":{"type":"object","additionalProperties":false,"properties":{
								"renderer_version":{"type":"string","const":"hosted-genesis-owner-review.v1"},
								"candidate_revision":{"type":"integer","minimum":0},
								"candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
								"review_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
								"review_text":{"type":"string","minLength":1,"maxLength":65536}
							},"required":["renderer_version","candidate_revision","candidate_hash","review_hash","review_text"]}
						},"required":["version","phase","revision","candidate_hash"]}
					}},
					"guidance":{"type":"object","additionalProperties":false,"properties":{
						"next_tool":{"type":"string"},
						"alternate_next_tool":{"type":"string"},
						"forbidden_next_tool":{"type":"string"},
						"status":{"type":"string"},
						"fresh_lane":{"type":"boolean"},
						"wait":{"type":"boolean"},
						"poll_after_seconds":{"type":"integer"},
						"expected_wait_seconds":{"type":"integer"},
						"progress":{"type":"string"},
						"progress_percent":{"type":"integer"},
						"reason":{"type":"string","maxLength":256},
						"recovery_action":{"type":"string"},
						"instruction":{"type":"string"},
						"candidate_revision":{"type":"integer","minimum":0},
						"candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
						"review_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
						"allowed_actions":{"type":"array","items":{"type":"string","enum":["affirm","edit"]}},
						"candidate_actions":{"type":"array","minItems":6,"maxItems":6,"uniqueItems":true,"items":{
							"type":"object","additionalProperties":false,
							"properties":{
								"intent":{"type":"string","enum":["affirm","edit"]},
								"section":{"type":"string","enum":["identity","philosophy","discipline","boundaries","soul"]},
								"description":{"type":"string"},
								"message_guidance":{"type":"string"},
								"candidate_action":{"type":"object","additionalProperties":false,"properties":{
									"action":{"type":"string","enum":["affirm","edit"]},
									"section":{"type":"string","enum":["identity","philosophy","discipline","boundaries","soul"]},
									"candidate_revision":{"type":"integer","minimum":0},
									"candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
									"review_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}
								},"required":["action","candidate_revision","candidate_hash","review_hash"],"allOf":[
									{"if":{"properties":{"action":{"const":"edit"}},"required":["action"]},"then":{"required":["section"]}},
									{"if":{"properties":{"action":{"const":"affirm"}},"required":["action"]},"then":{"not":{"required":["section"]}}}
								]}
							},
							"required":["intent","description","message_guidance","candidate_action"],
							"allOf":[
								{"if":{"properties":{"intent":{"const":"edit"}},"required":["intent"]},"then":{"required":["section"]}},
								{"if":{"properties":{"intent":{"const":"affirm"}},"required":["intent"]},"then":{"not":{"required":["section"]}}}
							]
						}}
					}}
				}
			},
			"error":{"type":"object","description":"Structured tool error when isError=true.","properties":{"code":{"type":"string","enum":["host_genesis_unavailable","invalid_request","unauthorized","forbidden","not_found","conflict","not_configured","insufficient_scope","owner_operator_required","host_genesis_projection_invalid","host_genesis_declaration_invalid","agent_registry_error","agent_soul_seed_error","agent_instructions_seed_error"]}}}
		}
	}`)
}

func agentGenesisBeginDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisBegin,
		Title:       "Begin Host-backed agent genesis",
		Description: "Begin a new agent registration through lesser-host's instance-trust genesis flow. Host derives the new agent identity; this tool does not delegate or require a pre-existing Lesser agent account. First fetch the read-only operating playbook with agent_genesis_skill_get. Next: call agent_genesis_advance with the returned registration_id and persist the Host conversation_id. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
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
		Description: "List Host-backed Ptah genesis conversation summaries for one agent and return a deterministic recovery/navigation index. Body consumes Host's HostedGenesisSession summary endpoint, sanitizes summary-only fields, selects the newest actionable non-terminal lane as recommended_start, and gives exact next-tool arguments. Start here when registration_id/conversation_id are unclear. This replaces the former producer_contract_missing placeholder without fabricating local state. Requires explicit instance owner/operator OAuth authority and read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Host/Lesser agent id whose Host HostedGenesisSession conversation summaries should be listed."},
				"limit":{"type":"integer","minimum":1,"maximum":50,"description":"Optional Host summary page size."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","properties":{
					"operation":{"type":"string","enum":["list"]},
					"status":{"type":"string","enum":["ok","not_available"]},
					"agent_id":{"type":"string"},
					"conversations":{"type":"array","items":{"type":"object","properties":{
						"registration_id":{"type":"string"},
						"conversation_id":{"type":"string"},
						"status":{"type":"string"},
						"latest_turn_id":{"type":"string"},
						"message_count":{"type":"integer"},
						"created_at":{"type":"string"},
						"updated_at":{"type":"string"},
						"recommended_next_tool":{"type":"string"},
						"recommended_arguments":{"type":"object"},
						"instruction":{"type":"string"},
						"terminal":{"type":"boolean"},
						"recoverable_hint":{"type":"string"},
						"restart_hint":{"type":"string"}
					}}},
					"recommended_start":{"type":"object"},
					"start_here":{"type":"object"},
					"guidance":{"type":"object","properties":{"next_tool":{"type":"string"},"instruction":{"type":"string"}}}
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
		Description:  "Read the durable lesser-host HostedGenesisSession projection for a Ptah genesis conversation. Follow structuredContent.data.guidance. in_progress is read-only: wait poll_after_seconds when present, then read again. During candidate review, inspect the exact lossless declaration_candidate.review.review_text and use the returned structural candidate_action bindings; free-form affirmation text has no authority. Requires explicit instance owner/operator OAuth authority and read scope; Host is the sole state authority.",
		Annotations:  readOnlyToolAnnotations(),
		InputSchema:  genesisConversationInputSchema(),
		OutputSchema: genesisOutputSchema(),
	}
}

func agentGenesisAdvanceDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisAdvance,
		Title:       "Advance Host-backed agent genesis",
		Description: "Submit the owner's next message to lesser-host's durable genesis/mint conversation when Host reports assistant_turn_ready. During candidate section phase, send the normal owner message; provider declaration tools remain private to Host's AppTheory MicroVM. During candidate review, candidate_action is mandatory: affirm forbids section, while edit requires the exact section and an owner revision message; both bind the exact returned revision and hashes. Free-form affirmation text has no authority. Persist and reuse conversation_id. in_progress is wait/read-only. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"registration_id":{"type":"string","description":"Host registration id returned by agent_genesis_begin."},
				"conversation_id":{"type":"string","description":"Host conversation id returned by the first advance; omit only for the first turn."},
				"model":{"type":"string","description":"Optional Host model alias. When omitted, lesser-host applies its configured default alias."},
				"message":{"type":"string","description":"Owner's next genesis conversation message."},
				"candidate_action":{"type":"object","description":"Structural owner action required only for Host candidate review.","additionalProperties":false,"properties":{
					"action":{"type":"string","enum":["affirm","edit"]},
					"section":{"type":"string","enum":["identity","philosophy","discipline","boundaries","soul"]},
					"candidate_revision":{"type":"integer","minimum":0},
					"candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
					"review_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}
				},"required":["action","candidate_revision","candidate_hash","review_hash"],"allOf":[
					{"if":{"properties":{"action":{"const":"edit"}},"required":["action"]},"then":{"required":["section"]}},
					{"if":{"properties":{"action":{"const":"affirm"}},"required":["action"]},"then":{"not":{"required":["section"]}}}
				]},
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
		Description:  "Ask lesser-host to reconcile or safely retry a typed recovery state for the durable genesis conversation only when Host returns failure.recovery.action=retry_same_step. Wait Host's bounded retry_after_seconds when present, then call this tool exactly once. Host owns the recovery state machine; refresh_state maps to one agent_genesis_read, restart_soul_bootstrap maps to a fresh agent_genesis_begin lane, and operator_action requires operator contact with no automatic write. Body never reruns or substitutes the conversation locally. Requires explicit instance owner/operator OAuth authority and write scope.",
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
		Description:  "Finalize and publish the Host-owned instance-trust genesis result. Before publication Body reads and hash-verifies Host's finalized canonical declaration; after Host publishes the identity, Body idempotently writes the Host-derived Ptah registry row, deterministically seeds the matching Panonomous soul-document v2 as a published snapshot, and create-only seeds a default agent_instructions operating draft. Declaration application is provider-free: no MicroVM or model is invoked. Requires explicit instance owner/operator OAuth authority and write scope; no x402 payment is used.",
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
	if in.AgentID, err = requiredGenesisString(in.AgentID, "agent_id", genesisMaxPathID); err != nil {
		return mustToolErrorResult("invalid_request", "agent_genesis_list agent_id is invalid", http.StatusBadRequest, nil), nil
	}
	if in.Limit != nil && (*in.Limit < 1 || *in.Limit > 50) {
		return mustToolErrorResult("invalid_request", "agent_genesis_list limit must be between 1 and 50", http.StatusBadRequest, nil), nil
	}
	client, result, err := cfg.genesisClientForTool(toolAgentGenesisList)
	if result != nil || err != nil {
		return result, err
	}
	bearer, result, err := hostGenesisBearer(ctx, toolAgentGenesisList)
	if result != nil || err != nil {
		return result, err
	}
	genesisAudit(ctx, toolAgentGenesisList, actor, true)
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	raw, err := client.ListConversations(ctx, bearer, in.AgentID, limit)
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisList, err)
	}
	data := sanitizeGenesisListResponse(in.AgentID, limit, raw)
	text := map[string]any{
		"summary":         "Host-backed Ptah genesis recovery index",
		"operation":       "list",
		"status":          "ok",
		"agent_id":        in.AgentID,
		"source":          "lesser_host",
		"state_authority": "Host HostedGenesisSession",
		"guidance":        data["guidance"],
	}
	if start, ok := data["recommended_start"].(map[string]any); ok && len(start) > 0 {
		text["recommended_start"] = start
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
		CandidateAction: in.CandidateAction,
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
	subjectID, result := authenticatedSubjectID(auth.PrincipalFromToolContext(ctx), toolAgentGenesisFinalize)
	if result != nil {
		return result, nil
	}

	genesisAudit(ctx, toolAgentGenesisFinalize, actor, true)
	// Read and verify the exact finalized candidate before Host publication.
	// This is a deterministic Body-side precondition, not a second declaration
	// producer and not a MicroVM/model path.
	declarationRaw, err := client.ReadConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisFinalize, err)
	}
	soulDocument, source, err := transformFinalizedHostedGenesisDeclaration(declarationRaw, in.RegistrationID, in.ConversationID)
	if err != nil {
		return mustToolErrorResult("host_genesis_declaration_invalid", "lesser-host finalized declaration did not pass Body's deterministic hash/schema application gate", http.StatusBadGateway, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	instructionsSeed, err := renderHostedGenesisInstructions(source)
	if err != nil {
		return mustToolErrorResult("host_genesis_declaration_invalid", "lesser-host finalized declaration did not pass Body's deterministic instructions application gate", http.StatusBadGateway, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	contentStore, err := cfg.content()
	if err != nil || contentStore == nil {
		return mustToolErrorResult("not_configured", "Body/Ptah agent content storage is required before Host genesis publication", http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}

	raw, err := client.FinalizeConversation(ctx, bearer, in.RegistrationID, in.ConversationID)
	if err != nil {
		return genesisToolResultFromError(toolAgentGenesisFinalize, err)
	}
	data, sanitizeErr := sanitizeGenesisResponse("finalize", raw)
	if sanitizeErr != nil {
		return genesisProjectionInvalidResult(toolAgentGenesisFinalize)
	}
	if finalizedAgentID := finalizedGenesisAgentID(data); finalizedAgentID == "" || finalizedAgentID != source.AgentID {
		return mustToolErrorResult("host_genesis_projection_invalid", "lesser-host finalized agent_id did not match the hash-verified declaration source", http.StatusBadGateway, map[string]any{
			"source": "lesser_host_genesis",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
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
	seeded, seedCreated, err := contentStore.SeedPublished(ctx, agentcontent.SeedPublishedInput{
		Account:            actor,
		AgentID:            source.AgentID,
		SoulDocument:       soulDocument,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		slog.WarnContext(ctx, "ptah genesis finalized soul seed failed",
			"tool", toolAgentGenesisFinalize,
			"source", "agent_content",
			"agent_id", source.AgentID,
			"error", "soul seed write failed",
		)
		return mustToolErrorResult("agent_soul_seed_error", "Host finalized the genesis agent, but Body failed to seed the published Panonomous soul-document v2; retry agent_genesis_finalize to repair materialization readiness", http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	data["soul_seed"] = finalizedSoulSeedSummary(seeded, seedCreated)
	seededInstructions, instructionsCreated, err := contentStore.SeedInstructions(ctx, agentcontent.SeedInstructionsInput{
		Account:            actor,
		AgentID:            source.AgentID,
		Content:            instructionsSeed,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil || seededInstructions == nil {
		slog.WarnContext(ctx, "ptah genesis finalized instructions seed failed",
			"tool", toolAgentGenesisFinalize,
			"source", "agent_content",
			"agent_id", source.AgentID,
			"error", "instructions seed write failed",
		)
		return mustToolErrorResult("agent_instructions_seed_error", "Host finalized the genesis agent, but Body failed to seed the default agent_instructions draft; retry agent_genesis_finalize to repair materialization readiness", http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
			"tool":   toolAgentGenesisFinalize,
		}), nil
	}
	data["instructions_seed"] = finalizedInstructionsSeedSummary(seededInstructions, instructionsCreated, instructionsSeed, source.CandidateHash)
	return genesisSuccessResultFromData(toolAgentGenesisFinalize, "finalize", data)
}

func finalizedSoulSeedSummary(record *agentcontent.Record, created bool) map[string]any {
	if record == nil {
		return map[string]any{}
	}
	summary := map[string]any{
		"source":          "agent_content",
		"schema_version":  agentcontent.SoulDocumentSchemaVersion,
		"agent_id":        record.AgentID,
		"soul_version":    record.SoulVersion,
		"lifecycle_state": string(record.LifecycleState),
		"created":         created,
		"idempotent":      true,
	}
	if record.Document != nil && record.Document.Provenance != nil {
		summary["provenance_source"] = record.Document.Provenance.Source
		summary["declaration_candidate_hash"] = record.Document.Provenance.DeclarationCandidateHash
	}
	return summary
}

func finalizedInstructionsSeedSummary(record *agentcontent.Record, created bool, seedContent, candidateHash string) map[string]any {
	if record == nil {
		return map[string]any{}
	}
	matched := record.Content == seedContent
	return map[string]any{
		"source":                     "agent_content",
		"seed_version":               hostedGenesisInstructionsSeedV1,
		"declaration_candidate_hash": candidateHash,
		"agent_id":                   record.AgentID,
		"version":                    record.Version,
		"lifecycle_state":            string(record.LifecycleState),
		"created":                    created,
		"matched_seed":               matched,
		"owner_authored_preserved":   !created && !matched,
		"idempotent":                 true,
	}
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err == nil {
		if raw, present := fields["candidate_action"]; present && strings.TrimSpace(string(raw)) == "null" {
			return in, mustToolErrorResult("invalid_request", "agent_genesis_advance candidate_action must be an object", http.StatusBadRequest, nil), nil
		}
	}
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
	if in.Message, err = requiredGenesisMessage(in.Message); err != nil {
		return in, mustToolErrorResult("invalid_request", "agent_genesis_advance requires a bounded message", http.StatusBadRequest, nil), nil
	}
	if in.CandidateAction != nil {
		if err := in.CandidateAction.Validate(); err != nil {
			return in, mustToolErrorResult("invalid_request", "agent_genesis_advance candidate_action is invalid", http.StatusBadRequest, nil), nil
		}
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
	data, err := sanitizeGenesisResponse(operation, raw)
	if err != nil {
		return genesisProjectionInvalidResult(toolName)
	}
	return genesisSuccessResultFromData(toolName, operation, data)
}

func genesisProjectionInvalidResult(toolName string) (*mcpruntime.ToolResult, error) {
	return mustToolErrorResult("host_genesis_projection_invalid", "lesser-host returned an invalid genesis projection", http.StatusBadGateway, map[string]any{
		"source": "lesser_host_genesis",
		"tool":   toolName,
	}), nil
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
	if soulSeed, ok := data["soul_seed"].(map[string]any); ok && len(soulSeed) > 0 {
		text["soul_seed"] = soulSeed
	}
	if instructionsSeed, ok := data["instructions_seed"].(map[string]any); ok && len(instructionsSeed) > 0 {
		text["instructions_seed"] = instructionsSeed
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
	if status := strings.ToLower(strings.TrimSpace(nestedString(data, "registration", "status"))); status != "" {
		// Unlike the conversation projection, Body does not validate Host's
		// registration status before propagating it, so an unrecognized value
		// would otherwise leave a successful — and already side-effecting —
		// begin failing its own output schema at a strict client. Report the
		// unclassifiable case as "unknown" and keep the raw Host value under
		// data.registration.status.
		if validGenesisRegistrationStatus(status) {
			return status
		}
		return "unknown"
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
	projectedLocalID := firstNonEmpty(nestedString(data, "agent", "local_id"), nestedString(data, "agent", "localId"), nestedString(data, "registration", "local_id"), nestedString(data, "publication", "local_id"), nestedString(data, "promotion", "local_id"), stringValue(data, "local_id"))
	existing, getErr := registry.Get(ctx, actor, agentID)
	var expectedLocalID *string
	switch {
	case getErr == nil && existing != nil && strings.TrimSpace(existing.LocalID) != "" && strings.TrimSpace(projectedLocalID) != "":
		expected := strings.TrimSpace(existing.LocalID)
		expectedLocalID = &expected
		if err := actorendpoint.Validate(projectedLocalID, existing.LocalID); err != nil {
			return nil, false, mustToolErrorResult("actor_endpoint_divergence", "agent_genesis_finalize replay refused to overwrite a registry local_id that disagrees with the Host-derived projection", http.StatusConflict, map[string]any{
				"source":          "agent_registry_replay",
				"tool":            toolAgentGenesisFinalize,
				"operator_action": "verify the authoritative Lesser actor and repair the divergent source before retrying; Body will not rewrite either value silently",
			}), nil
		}
	case getErr == nil && existing != nil:
		expected := strings.TrimSpace(existing.LocalID)
		expectedLocalID = &expected
	case getErr != nil && !errors.Is(getErr, agentregistry.ErrAgentNotFound):
		return nil, false, mustToolErrorResult("agent_registry_error", "Body failed to read the existing registry row before replay-safe genesis finalization", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry_replay",
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
		Account:                actor,
		AgentID:                agentID,
		HostRegistrationID:     registrationID,
		HostConversationID:     conversationID,
		Domain:                 firstNonEmpty(nestedString(data, "agent", "domain"), nestedString(data, "registration", "domain"), nestedString(data, "publication", "domain"), nestedString(data, "promotion", "domain"), stringValue(data, "domain")),
		LocalID:                projectedLocalID,
		AuthorityModel:         firstNonEmpty(nestedString(data, "agent", "authority_model"), nestedString(data, "publication", "authority_model"), nestedString(data, "promotion", "authority_model"), stringValue(data, "authority_model")),
		AnchorState:            firstNonEmpty(nestedString(data, "agent", "anchor_state"), nestedString(data, "publication", "anchor_state"), nestedString(data, "promotion", "anchor_state"), stringValue(data, "anchor_state")),
		OperationalBinding:     firstNonEmpty(nestedString(data, "agent", "operational_binding"), nestedString(data, "publication", "operational_binding"), nestedString(data, "promotion", "operational_binding"), stringValue(data, "operational_binding")),
		LifecycleStatus:        firstNonEmpty(nestedString(data, "agent", "lifecycle_status"), nestedString(data, "agent", "status"), nestedString(data, "publication", "lifecycle_status"), nestedString(data, "promotion", "lifecycle_status"), stringValue(data, "lifecycle_status"), stringValue(data, "status")),
		PublishedVersion:       firstNonZeroInt64(nestedInt64(data, "publication", "published_version"), nestedInt64(data, "promotion", "published_version"), nestedInt64(data, "agent", "published_version"), int64Value(data["published_version"])),
		SelfDescriptionVersion: firstNonZeroInt64(nestedInt64(data, "agent", "self_description_version"), nestedInt64(data, "agent", "selfDescriptionVersion"), int64Value(data["self_description_version"])),
		ExpectedLocalID:        expectedLocalID,
	})
	if err != nil {
		if errors.Is(err, agentregistry.ErrFinalizedLocalIDChanged) {
			return nil, false, mustToolErrorResult("actor_endpoint_divergence", "agent_genesis_finalize replay refused because the registry local_id changed during finalization", http.StatusConflict, map[string]any{
				"source":          "agent_registry_replay",
				"tool":            toolAgentGenesisFinalize,
				"operator_action": "verify the authoritative Lesser actor and retry from the corrected registry state; Body did not overwrite the concurrent change",
			}), nil
		}
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
	if guidance := genesisRecoveryActionGuidance(data); len(guidance) > 0 {
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
	case genesisProcessingWaitStatus(status):
		guidance["next_tool"] = toolAgentGenesisRead
		guidance["wait"] = true
		guidance["forbidden_next_tool"] = toolAgentGenesisAdvance
		if pollAfter, ok := genesisPollAfterSeconds(data); ok {
			guidance["poll_after_seconds"] = pollAfter
			guidance["expected_wait_seconds"] = pollAfter
		}
		if progress := firstNonEmpty(nestedString(data, "conversation", "progress"), stringValue(data, "progress")); progress != "" {
			guidance["progress"] = progress
		}
		if progressPercent, ok := genesisProgressPercent(data); ok {
			guidance["progress_percent"] = progressPercent
		}
		guidance["instruction"] = genesisProcessingWaitInstruction(data)
	case operation == "finalize_preflight":
		guidance["next_tool"] = toolAgentGenesisFinalize
		guidance["instruction"] = "Host preflight succeeded. Call agent_genesis_finalize; Body deterministically hash-verifies and transforms the finalized declaration before publication, then writes the Host-derived registry row, published v2 soul seed, and create-only default agent_instructions draft after Host publishes the identity. No MicroVM or model runs in declaration application."
	case operation == "finalize" || status == "published":
		guidance["next_tool"] = toolAgentGet
		guidance["alternate_next_tool"] = toolAgentList
		guidance["instruction"] = "This Host lane is published and terminal. Verify the minted agent, published Panonomous soul-document v2 seed, and draft default instructions seed with agent_get for the returned agent_id, or agent_list for the account-scoped registry view. Ba install planning needs no manual content-authoring step."
	case status == "declaration_ready":
		guidance["next_tool"] = toolAgentGenesisFinalizePreflight
		guidance["instruction"] = "Host reports declaration_ready. Call agent_genesis_finalize_preflight directly, then agent_genesis_finalize only after the preflight succeeds."
	case genesisOwnerInputStatus(status) && genesisCandidatePhase(data) == "review":
		return genesisCandidateReviewGuidance(status, data)
	case genesisOwnerInputStatus(status):
		guidance["next_tool"] = toolAgentGenesisAdvance
		guidance["alternate_next_tool"] = toolAgentGenesisRead
		candidate := nestedMap(data, "conversation", "declaration_candidate")
		completed, _ := candidate["completed_sections"].([]any)
		currentSection := stringValue(candidate, "current_section")
		if len(completed) == len(genesisDeclarationSections) {
			guidance["instruction"] = "Host reopened current_section=" + currentSection + " after review. Call agent_genesis_advance with a normal owner revision message for this exact current_section; Host's five provider declaration tools remain inside its AppTheory MicroVM and are never Body-local tools. Optionally read first to refresh the Host projection."
		} else {
			guidance["instruction"] = "Host candidate phase is section. Call agent_genesis_advance with the next normal owner message for current_section; Host's five provider declaration tools remain inside its AppTheory MicroVM and are never Body-local tools. Optionally read first to refresh the Host projection."
		}
	default:
		guidance["next_tool"] = toolAgentGenesisRead
		guidance["instruction"] = "Poll agent_genesis_read and follow the Host status; Body does not substitute a local genesis state machine."
	}
	return guidance
}

func genesisCandidatePhase(data map[string]any) string {
	return nestedString(data, "conversation", "declaration_candidate", "phase")
}

func genesisCandidateReviewGuidance(status string, data map[string]any) map[string]any {
	candidate := nestedMap(data, "conversation", "declaration_candidate")
	review := nestedMap(candidate, "review")
	revision, _ := exactNonNegativeInt(candidate["revision"])
	candidateHash := stringValue(candidate, "candidate_hash")
	reviewHash := stringValue(review, "review_hash")
	bindings := map[string]any{
		"candidate_revision": revision,
		"candidate_hash":     candidateHash,
		"review_hash":        reviewHash,
	}
	affirm := cloneMap(bindings)
	affirm["action"] = "affirm"
	actions := []any{
		map[string]any{
			"intent":           "affirm",
			"description":      "Accept the exact lossless Host review bound by the advertised revision and hashes.",
			"message_guidance": "Supply a bounded owner decision message; the structural candidate_action, not its prose, carries authority.",
			"candidate_action": affirm,
		},
	}
	for _, section := range genesisDeclarationSections {
		edit := cloneMap(bindings)
		edit["action"] = "edit"
		edit["section"] = section
		actions = append(actions, map[string]any{
			"intent":           "edit",
			"section":          section,
			"description":      "Reopen the exact " + section + " section while preserving the advertised review bindings.",
			"message_guidance": "Supply a bounded owner revision message describing the requested " + section + " change.",
			"candidate_action": edit,
		})
	}
	return map[string]any{
		"status":              status,
		"fresh_lane":          false,
		"next_tool":           toolAgentGenesisAdvance,
		"alternate_next_tool": toolAgentGenesisRead,
		"candidate_revision":  revision,
		"candidate_hash":      candidateHash,
		"review_hash":         reviewHash,
		"allowed_actions":     []any{"affirm", "edit"},
		"candidate_actions":   actions,
		"instruction":         "Inspect the exact lossless conversation.declaration_candidate.review.review_text. Select one advertised candidate_actions entry, then call agent_genesis_advance with only its nested candidate_action unchanged. affirm forbids section. edit requires one exact section; each edit action supplies it and requires an owner revision message. Free-form or canonical affirmation phrases have zero authority.",
	}
}

func genesisProcessingWaitStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress":
		return true
	default:
		return false
	}
}

func genesisOwnerInputStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "assistant_turn_ready":
		return true
	default:
		return false
	}
}

func genesisProcessingWaitInstruction(data map[string]any) string {
	waitText := "wait for Host's expected delay"
	if pollAfter, ok := genesisPollAfterSeconds(data); ok {
		waitText = fmt.Sprintf("wait poll_after_seconds=%d seconds", pollAfter)
	}
	return "Host is processing. Do not call " + toolAgentGenesisAdvance + " again and do not nudge; " + waitText + ", then call " + toolAgentGenesisRead + ". Only call " + toolAgentGenesisAdvance + " after Host reports assistant_turn_ready; review phase additionally requires structural candidate_action."
}

func genesisPollAfterSeconds(data map[string]any) (int, bool) {
	for _, candidate := range []map[string]any{nestedMap(data, "conversation"), data} {
		if len(candidate) == 0 {
			continue
		}
		value, ok := firstField(candidate, "poll_after_seconds", "pollAfterSeconds")
		if !ok {
			continue
		}
		switch number := value.(type) {
		case int:
			if number >= 0 {
				return number, true
			}
		case int64:
			if number >= 0 {
				return int(number), true
			}
		case float64:
			if number >= 0 {
				return int(number), true
			}
		}
	}
	return 0, false
}

func genesisProgressPercent(data map[string]any) (int, bool) {
	for _, candidate := range []map[string]any{nestedMap(data, "conversation"), data} {
		if len(candidate) == 0 {
			continue
		}
		value, ok := firstField(candidate, "progress_percent", "progressPercent")
		if !ok {
			continue
		}
		switch number := value.(type) {
		case int:
			if number >= 0 {
				return number, true
			}
		case int64:
			if number >= 0 {
				return int(number), true
			}
		case float64:
			if number >= 0 {
				return int(number), true
			}
		}
	}
	return 0, false
}

// genesisRecoveryActionGuidance projects lesser-host's exact RecoveryAction
// vocabulary. Host remains state authority; Body chooses no fallback write for
// unknown or operator-owned actions.
func genesisRecoveryActionGuidance(data map[string]any) map[string]any {
	recovery := nestedMap(data, "conversation", "failure", "recovery")
	if len(recovery) == 0 {
		recovery = nestedMap(data, "failure", "recovery")
	}
	action, _ := recovery["action"].(string)
	if action == "" {
		return nil
	}
	out := map[string]any{
		"status": firstNonEmpty(
			nestedString(data, "conversation", "status"),
			stringValue(data, "status"),
			"failed",
		),
		"fresh_lane": false,
	}
	switch action {
	case "retry_same_step":
		out["next_tool"] = toolAgentGenesisRecover
		if retryAfter, ok := genesisRecoveryRetryAfterSeconds(recovery); ok {
			out["wait"] = true
			out["poll_after_seconds"] = retryAfter
			out["expected_wait_seconds"] = retryAfter
			out["instruction"] = fmt.Sprintf("Host requested retry_same_step: wait retry_after_seconds=%d seconds, then call %s exactly once for this registration_id/conversation_id. Keep the same lane; do not start a fresh lane or poll %s instead.", retryAfter, toolAgentGenesisRecover, toolAgentGenesisRead)
		} else {
			out["instruction"] = "Host requested retry_same_step: call " + toolAgentGenesisRecover + " exactly once for this registration_id/conversation_id. Keep the same lane; do not start a fresh lane or poll " + toolAgentGenesisRead + " instead."
		}
	case "restart_soul_bootstrap":
		out["status"] = "restart_soul_bootstrap"
		out["next_tool"] = toolAgentGenesisBegin
		out["forbidden_next_tool"] = toolAgentGenesisRecover
		out["fresh_lane"] = true
		out["instruction"] = "Host requested restart_soul_bootstrap: call agent_genesis_begin again for a fresh genesis lane using the intended domain/local_id. Do not call agent_genesis_recover for this action."
	case "refresh_state":
		out["next_tool"] = toolAgentGenesisRead
		out["instruction"] = "Host requested refresh_state: call " + toolAgentGenesisRead + " exactly once to refresh this lane, then follow the newly returned Host status and recovery action. Do not call a Genesis write tool for refresh_state and do not poll endlessly."
	case "operator_action":
		out["instruction"] = "Host requested operator_action: Stop automatic Genesis tool calls and contact the instance operator with the Host-provided safe recovery reason when present. Do not call a Genesis write tool and do not poll " + toolAgentGenesisRead + " endlessly."
	default:
		out["recovery_action"] = action
		out["instruction"] = "Host returned an unrecognized failure.recovery.action: Stop automatic Genesis tool calls and contact the instance operator. Body will not normalize the Host value or select a fallback read/write tool."
	}
	if reason := stringValue(recovery, "reason"); reason != "" {
		out["reason"] = reason
	}
	return out
}

func genesisRecoveryRetryAfterSeconds(recovery map[string]any) (int, bool) {
	value, ok := firstField(recovery, "retry_after_seconds", "retryAfterSeconds")
	if !ok {
		return 0, false
	}
	var seconds int64
	switch number := value.(type) {
	case int:
		seconds = int64(number)
	case int64:
		seconds = number
	case float64:
		if number != float64(int64(number)) {
			return 0, false
		}
		seconds = int64(number)
	default:
		return 0, false
	}
	if seconds < 0 || seconds > genesisMaxRecoveryRetryAfterSeconds {
		return 0, false
	}
	return int(seconds), true
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

func sanitizeGenesisResponse(operation string, raw map[string]any) (map[string]any, error) {
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
		conversationEnvelope := len(conversation) > 0
		if len(conversation) == 0 {
			conversation = raw
		}
		safe, err := sanitizeGenesisConversation(conversation, conversationEnvelope)
		if err != nil {
			return nil, err
		}
		if len(safe) > 0 {
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
	return data, nil
}

func sanitizeGenesisListResponse(agentID string, limit int, raw map[string]any) map[string]any {
	conversations := sanitizeGenesisConversationSummaries(raw["conversations"], agentID)
	start := genesisListStartHere(conversations, agentID)
	guidance := map[string]any{
		"instruction": "When registration_id or conversation_id are unclear, start with agent_genesis_list for the Host-backed recovery index, then follow recommended_start.recommended_next_tool with recommended_start.recommended_arguments. Do not guess hidden Host failure details from list summaries.",
	}
	if nextTool := stringValue(start, "recommended_next_tool"); nextTool != "" {
		guidance["next_tool"] = nextTool
	}
	data := map[string]any{
		"source":            "lesser_host",
		"state_authority":   "Host HostedGenesisSession",
		"flow":              "genesis_conversation",
		"operation":         "list",
		"status":            "ok",
		"agent_id":          agentID,
		"conversations":     conversations,
		"recommended_start": start,
		"start_here":        start,
		"guidance":          guidance,
		"producer_contract": map[string]any{
			"host_pr":          fiveBodyHostPR,
			"host_head_sha":    fiveBodyHostHeadSHA,
			"host_endpoint":    "GET /api/v1/soul/instance/agents/{agentId}/mint-conversations",
			"safe_behavior":    "summary_only_hosted_genesis_session_index",
			"schema_version":   fiveBodySchemaVersion,
			"guidance_version": fiveBodyGuidanceVersion,
		},
	}
	if limit > 0 {
		data["limit"] = limit
	}
	copyIntField(data, "count", raw, "count")
	if version := firstString(raw, "version"); version != "" {
		data["version"] = version
	}
	return data
}

func sanitizeGenesisConversationSummaries(raw any, agentID string) []map[string]any {
	items, _ := raw.([]any)
	if len(items) == 0 {
		if typed, ok := raw.([]map[string]any); ok {
			items = make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, item)
			}
		}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		summary := sanitizeGenesisConversationSummary(mapValue(item), agentID)
		if len(summary) > 0 {
			out = append(out, summary)
		}
	}
	return out
}

func sanitizeGenesisConversationSummary(raw map[string]any, agentID string) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	for output, keys := range map[string][]string{
		"registration_id": {"registration_id", "registrationId"},
		"conversation_id": {"conversation_id", "conversationId"},
		"status":          {"status"},
		"latest_turn_id":  {"latest_turn_id", "latestTurnId"},
		"created_at":      {"created_at", "createdAt"},
		"updated_at":      {"updated_at", "updatedAt"},
	} {
		copyStringField(out, output, raw, keys...)
	}
	copyIntField(out, "message_count", raw, "message_count", "messageCount")
	if len(out) == 0 {
		return nil
	}
	genesisApplyListRecommendation(out, agentID)
	return out
}

func genesisApplyListRecommendation(summary map[string]any, agentID string) {
	status := strings.ToLower(strings.TrimSpace(stringValue(summary, "status")))
	registrationID := stringValue(summary, "registration_id")
	conversationID := stringValue(summary, "conversation_id")
	genesisArgs := func() map[string]any {
		args := map[string]any{}
		if registrationID != "" {
			args["registration_id"] = registrationID
		}
		if conversationID != "" {
			args["conversation_id"] = conversationID
		}
		return args
	}
	agentArgs := func() map[string]any {
		args := map[string]any{}
		if agentID != "" {
			args["agent_id"] = agentID
		}
		return args
	}
	setGenesis := func(toolName string, instruction string) {
		summary["recommended_next_tool"] = toolName
		summary["recommended_arguments"] = genesisArgs()
		summary["instruction"] = instruction
	}
	summary["terminal"] = false
	switch {
	case genesisProcessingWaitStatus(status) || status == "created":
		setGenesis(toolAgentGenesisRead, "Host is processing this lane. This list summary does not include exact poll timing; call agent_genesis_read with the listed ids to get poll_after_seconds when Host provides it, then wait/read only. Do not call agent_genesis_advance again and do not nudge while status remains in_progress.")
		summary["wait"] = true
		summary["forbidden_next_tool"] = toolAgentGenesisAdvance
		summary["recoverable_hint"] = "processing_wait_read_only"
	case genesisOwnerInputStatus(status):
		setGenesis(toolAgentGenesisRead, "Host is waiting for owner/operator input. Call agent_genesis_read first with the listed ids to load candidate phase and exact review bindings. Then follow read guidance: section uses a normal owner message; review requires structural candidate_action.")
		summary["alternate_next_tool"] = toolAgentGenesisAdvance
		summary["alternate_arguments"] = genesisArgs()
	case status == "failed":
		setGenesis(toolAgentGenesisRead, "This lane failed, but list summaries intentionally do not include typed failure.recovery details. Call agent_genesis_read with the listed ids first, then follow the read response's failure.recovery action; do not guess recover vs restart from the list.")
		summary["recoverable_hint"] = "unknown_until_read"
		summary["restart_hint"] = "unknown_until_read"
	case status == "declaration_ready":
		setGenesis(toolAgentGenesisFinalizePreflight, "Host reports declaration_ready. Call agent_genesis_finalize_preflight directly with the listed ids, then finalize only after preflight succeeds.")
		summary["recoverable_hint"] = "finalization_preflight_ready"
	case genesisTerminalListStatus(status):
		summary["recommended_next_tool"] = toolAgentGet
		summary["alternate_next_tool"] = toolAgentList
		summary["recommended_arguments"] = agentArgs()
		summary["terminal"] = true
		summary["recoverable_hint"] = "not_applicable_terminal"
		summary["restart_hint"] = "not_applicable_terminal"
		summary["instruction"] = "This lane is terminal/finalized for list recovery. Do not recover or advance it; verify the minted agent with agent_get for agent_id, or use agent_list for the account-scoped registry view."
	default:
		setGenesis(toolAgentGenesisRead, "Status is not enough to safely choose a mutating tool from the list summary. Call agent_genesis_read with the listed ids and follow the Host-backed read guidance.")
		summary["recoverable_hint"] = "read_required"
	}
}

func genesisTerminalListStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "published":
		return true
	default:
		return false
	}
}

func genesisListStartHere(conversations []map[string]any, agentID string) map[string]any {
	for _, conversation := range conversations {
		terminal, _ := conversation["terminal"].(bool)
		if terminal {
			continue
		}
		nextTool := stringValue(conversation, "recommended_next_tool")
		args, _ := conversation["recommended_arguments"].(map[string]any)
		if nextTool == "" || len(args) == 0 {
			continue
		}
		start := map[string]any{
			"registration_id":           stringValue(conversation, "registration_id"),
			"conversation_id":           stringValue(conversation, "conversation_id"),
			"status":                    stringValue(conversation, "status"),
			"recommended_next_tool":     nextTool,
			"recommended_arguments":     args,
			"instruction":               stringValue(conversation, "instruction"),
			"selection":                 "newest_actionable_non_terminal",
			"state_authority":           "Host HostedGenesisSession",
			"do_not_guess_hidden_state": true,
		}
		if latestTurnID := stringValue(conversation, "latest_turn_id"); latestTurnID != "" {
			start["latest_turn_id"] = latestTurnID
		}
		if alternate := stringValue(conversation, "alternate_next_tool"); alternate != "" {
			start["alternate_next_tool"] = alternate
		}
		return start
	}
	return map[string]any{
		"status":                "no_actionable_lane",
		"recommended_next_tool": toolAgentGenesisBegin,
		"recommended_arguments": map[string]any{},
		"agent_id":              agentID,
		"instruction":           "No actionable non-terminal Host genesis lane was returned for this agent_id. For finalized agents use agent_get/agent_list; to create a fresh genesis lane call agent_genesis_begin with the intended domain/local_id. Do not invent registration_id/conversation_id values.",
	}
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

func sanitizeGenesisConversation(raw map[string]any, strictStatus bool) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if strictStatus {
		statusRaw, present := raw["status"]
		status, ok := statusRaw.(string)
		if !present || !ok || !validGenesisConversationStatus(status) {
			return nil, errors.New("host genesis conversation status is invalid")
		}
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
		"progress_percent":   {"progress_percent", "progressPercent"},
		"created_at":         {"created_at", "createdAt"},
		"updated_at":         {"updated_at", "updatedAt"},
		"completed_at":       {"completed_at", "completedAt"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			out[output] = value
		}
	}
	copyStringField(out, "progress", raw, "progress")
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
	if candidateRaw, present := raw["declaration_candidate"]; present {
		candidateObject, ok := candidateRaw.(map[string]any)
		if !ok || candidateObject == nil {
			return nil, errors.New("host declaration candidate is not an object")
		}
		candidate, err := sanitizeDeclarationCandidate(candidateObject)
		if err != nil {
			return nil, err
		}
		out["declaration_candidate"] = candidate
		if strictStatus {
			status, _ := raw["status"].(string)
			phase, _ := candidate["phase"].(string)
			if (status == "assistant_turn_ready" && phase != "section" && phase != "review") ||
				((status == "declaration_ready" || status == "published") && phase != "finalized") {
				return nil, errors.New("host declaration candidate phase does not match conversation status")
			}
		}
	} else if strictStatus && !validGenesisNoCandidateRestartProjection(raw) {
		return nil, errors.New("host declaration candidate is missing")
	}
	return out, nil
}

var (
	genesisDeclarationSections     = []string{"identity", "philosophy", "discipline", "boundaries", "soul"}
	genesisSHA256IdentifierPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// validGenesisRegistrationStatus mirrors lesser-host's SoulAgentRegistration
// status vocabulary (host-contract openapi.yaml: enum [pending, completed]),
// which agent_genesis_begin reports and the genesis output schema declares.
func validGenesisRegistrationStatus(status string) bool {
	switch status {
	case "pending", "completed":
		return true
	default:
		return false
	}
}

func validGenesisConversationStatus(status string) bool {
	switch status {
	case "created", "in_progress", "assistant_turn_ready", "declaration_ready", "published", "failed":
		return true
	default:
		return false
	}
}

// validGenesisNoCandidateRestartProjection recognizes the one strict nested
// Host projection that deliberately has no typed candidate: a terminal hard-cut
// failure for an untyped/stale lane. Host owns this migration decision and
// requires a fresh bootstrap; Body relays it without rebuilding candidate state.
func validGenesisNoCandidateRestartProjection(raw map[string]any) bool {
	status, ok := exactString(raw["status"])
	if !ok || status != "failed" {
		return false
	}
	failure, ok := raw["failure"].(map[string]any)
	if !ok || failure == nil || requireExactObjectKeys(failure,
		[]string{"code", "message", "retryable", "recovery"},
		[]string{"class"},
	) != nil {
		return false
	}
	code, ok := exactString(failure["code"])
	if !ok || !oneOf(code,
		"llm_unavailable",
		"assistant_turn_failed",
		"invalid_completion_state",
		"missing_produced_declarations",
		"invalid_produced_declarations",
		"tenant_boundary_violation",
		"operator_action_required",
		"microvm_unavailable",
	) {
		return false
	}
	if classRaw, present := failure["class"]; present {
		class, ok := exactString(classRaw)
		if !ok || !oneOf(class, "provider_timeout", "provider_canceled", "provider_api_failure", "invalid_provider_output", "parse_validation_failure") {
			return false
		}
	}
	message, ok := exactString(failure["message"])
	if !ok || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 512 {
		return false
	}
	retryable, ok := failure["retryable"].(bool)
	if !ok || retryable {
		return false
	}
	recovery, ok := failure["recovery"].(map[string]any)
	if !ok || recovery == nil || requireExactObjectKeys(recovery,
		[]string{"action"},
		[]string{"max_attempts", "retry_after_seconds", "reason"},
	) != nil {
		return false
	}
	action, ok := exactString(recovery["action"])
	if !ok || action != "restart_soul_bootstrap" {
		return false
	}
	if !validOptionalGenesisBoundedPositiveInt(recovery, "max_attempts", 10) ||
		!validOptionalGenesisBoundedPositiveInt(recovery, "retry_after_seconds", genesisMaxRecoveryRetryAfterSeconds) {
		return false
	}
	if reasonRaw, present := recovery["reason"]; present {
		reason, ok := exactString(reasonRaw)
		if !ok || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 128 {
			return false
		}
	}
	return true
}

func validOptionalGenesisBoundedPositiveInt(raw map[string]any, key string, maximum int) bool {
	value, present := raw[key]
	if !present {
		return true
	}
	number, ok := exactNonNegativeInt(value)
	return ok && number >= 1 && number <= int64(maximum)
}

func sanitizeDeclarationCandidate(raw map[string]any) (map[string]any, error) {
	if err := requireExactObjectKeys(raw,
		[]string{"version", "phase", "revision", "candidate_hash"},
		[]string{"current_section", "completed_sections", "review"},
	); err != nil {
		return nil, err
	}
	version, ok := exactString(raw["version"])
	if !ok || version != "hosted-genesis-declaration-candidate.v1" {
		return nil, errors.New("host declaration candidate version is invalid")
	}
	phase, ok := exactString(raw["phase"])
	if !ok || !oneOf(phase, "section", "review", "affirmed", "finalized") {
		return nil, errors.New("host declaration candidate phase is invalid")
	}
	revision, ok := exactNonNegativeInt(raw["revision"])
	if !ok {
		return nil, errors.New("host declaration candidate revision is invalid")
	}
	candidateHash, ok := exactString(raw["candidate_hash"])
	if !ok || !genesisSHA256IdentifierPattern.MatchString(candidateHash) {
		return nil, errors.New("host declaration candidate hash is invalid")
	}
	out := map[string]any{
		"version": version, "phase": phase, "revision": revision, "candidate_hash": candidateHash,
	}
	currentSection := ""
	if value, present := raw["current_section"]; present {
		currentSection, ok = exactString(value)
		if !ok || !oneOf(currentSection, genesisDeclarationSections...) {
			return nil, errors.New("host declaration candidate current section is invalid")
		}
		out["current_section"] = currentSection
	}
	completed := []any(nil)
	var err error
	if value, present := raw["completed_sections"]; present {
		completed, err = exactDeclarationSections(value)
		if err != nil {
			return nil, err
		}
		out["completed_sections"] = completed
	}
	var review map[string]any
	if value, present := raw["review"]; present {
		reviewRaw, ok := value.(map[string]any)
		if !ok || reviewRaw == nil {
			return nil, errors.New("host declaration candidate review is not an object")
		}
		review, err = sanitizeDeclarationCandidateReview(reviewRaw, revision, candidateHash)
		if err != nil {
			return nil, err
		}
		out["review"] = review
	}
	switch phase {
	case "section":
		if currentSection == "" || review != nil {
			return nil, errors.New("host declaration candidate section phase is inconsistent")
		}
		if len(completed) < len(genesisDeclarationSections) && currentSection != genesisDeclarationSections[len(completed)] {
			return nil, errors.New("host declaration candidate section order is inconsistent")
		}
	case "review", "affirmed", "finalized":
		if currentSection != "" || len(completed) != len(genesisDeclarationSections) || review == nil {
			return nil, errors.New("host declaration candidate review binding is inconsistent")
		}
	}
	return out, nil
}

func sanitizeDeclarationCandidateReview(raw map[string]any, candidateRevision int64, candidateHash string) (map[string]any, error) {
	if err := requireExactObjectKeys(raw,
		[]string{"renderer_version", "candidate_revision", "candidate_hash", "review_hash", "review_text"}, nil,
	); err != nil {
		return nil, err
	}
	rendererVersion, rendererOK := exactString(raw["renderer_version"])
	revision, revisionOK := exactNonNegativeInt(raw["candidate_revision"])
	reviewCandidateHash, candidateHashOK := exactString(raw["candidate_hash"])
	reviewHash, reviewHashOK := exactString(raw["review_hash"])
	reviewText, reviewTextOK := exactString(raw["review_text"])
	if !rendererOK || rendererVersion != "hosted-genesis-owner-review.v1" ||
		!revisionOK || revision != candidateRevision || !candidateHashOK || reviewCandidateHash != candidateHash ||
		!reviewHashOK || !genesisSHA256IdentifierPattern.MatchString(reviewHash) || !reviewTextOK ||
		utf8.RuneCountInString(reviewText) == 0 || utf8.RuneCountInString(reviewText) > genesisMaxReviewRunes {
		return nil, errors.New("host declaration candidate review is invalid")
	}
	digest := sha256.Sum256([]byte(reviewText))
	if reviewHash != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, errors.New("host declaration candidate review hash is inconsistent")
	}
	return map[string]any{
		"renderer_version": rendererVersion, "candidate_revision": revision,
		"candidate_hash": reviewCandidateHash, "review_hash": reviewHash, "review_text": reviewText,
	}, nil
}

func requireExactObjectKeys(raw map[string]any, required []string, optional []string) error {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if _, ok := raw[key]; !ok {
			return fmt.Errorf("host declaration candidate field %s is missing", key)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("host declaration candidate field %s is unknown", key)
		}
	}
	return nil
}

func exactDeclarationSections(value any) ([]any, error) {
	items, ok := value.([]any)
	if !ok || len(items) > len(genesisDeclarationSections) {
		return nil, errors.New("host declaration candidate completed sections are invalid")
	}
	out := make([]any, 0, len(items))
	for index, item := range items {
		section, ok := exactString(item)
		if !ok || index >= len(genesisDeclarationSections) || section != genesisDeclarationSections[index] {
			return nil, errors.New("host declaration candidate completed sections are invalid")
		}
		out = append(out, section)
	}
	return out, nil
}

func exactString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func exactNonNegativeInt(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), number >= 0
	case int64:
		return number, number >= 0
	case float64:
		if number < 0 || number > math.MaxInt64 || number != math.Trunc(number) {
			return 0, false
		}
		return int64(number), true
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil && parsed >= 0
	default:
		return 0, false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
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
		if action, ok := recovery["action"].(string); ok && action != "" {
			safeRecovery["action"] = action
		}
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
		"version":             {"version"},
		"agent_id":            {"agent_id", "agentId"},
		"domain":              {"domain_normalized", "domain"},
		"local_id":            {"local_id", "localId"},
		"authority_model":     {"authority_model", "authorityModel"},
		"anchor_state":        {"anchor_state", "anchorState"},
		"operational_binding": {"operational_binding", "operationalBinding"},
		"lifecycle_status":    {"lifecycle_status", "lifecycleStatus", "status"},
		"published_version":   {"published_version", "publishedVersion"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			data[output] = value
		}
	}
	if agent := sanitizeGenesisAgentIdentity(mapValue(raw, "agent")); len(agent) > 0 {
		data["agent"] = agent
		if id := stringValue(agent, "agent_id"); id != "" {
			data["agent_id"] = id
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

func sanitizeGenesisAgentIdentity(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]any{}
	for output, keys := range map[string][]string{
		"agent_id":                 {"agent_id", "agentId"},
		"domain":                   {"domain_normalized", "domain"},
		"local_id":                 {"local_id", "localId"},
		"authority_model":          {"authority_model", "authorityModel"},
		"anchor_state":             {"anchor_state", "anchorState"},
		"operational_binding":      {"operational_binding", "operationalBinding"},
		"lifecycle_status":         {"lifecycle_status", "lifecycleStatus"},
		"status":                   {"status"},
		"published_version":        {"published_version", "publishedVersion"},
		"self_description_version": {"self_description_version", "selfDescriptionVersion"},
	} {
		if value, ok := firstField(raw, keys...); ok && safeScalar(value) {
			out[output] = value
		}
	}
	return out
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
