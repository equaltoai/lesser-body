package ptahserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	// EnvSoulBindingIntegrationBearer is the dedicated server-to-server bearer
	// Body/Ptah uses for Lesser's hosted soul/body binding API. It must not be a
	// caller OAuth token.
	EnvSoulBindingIntegrationBearer = "LESSER_SOUL_BINDING_INTEGRATION_BEARER"

	toolAgentBindSoul = "agent_bind_soul"
	toolAgentCreate   = "agent_create"
	toolAgentGet      = "agent_get"
	toolAgentList     = "agent_list"

	toolAgentSoulGet     = "agent_soul_get"
	toolAgentSoulUpsert  = "agent_soul_upsert"
	toolAgentSoulArchive = "agent_soul_archive"

	toolAgentInstructionsGet     = "agent_instructions_get"
	toolAgentInstructionsUpsert  = "agent_instructions_upsert"
	toolAgentInstructionsArchive = "agent_instructions_archive"

	agentSoulProvisionalMarker = "provisional_agent_soul_schema_pending_lesser_soul_s1"
	agentSoulProvisionalText   = "Provisional schema marker " + agentSoulProvisionalMarker + ": agent_soul content is an opaque Ptah-authored draft string pending lesser-soul S1 governance-policy unblock; clients must not treat it as the final Panonomous soul contract."
)

type soulBindingClient interface {
	InitiateSoulBinding(ctx context.Context, integrationBearer string, idempotencyKey string, req lesserapi.SoulBindingRequest) (*lesserapi.SoulBindingResponse, error)
}

type agentDelegateClient interface {
	DelegateAgent(ctx context.Context, bearerToken string, req lesserapi.AgentDelegationRequest) (*lesserapi.AgentDelegationResponse, error)
}

// AgentContentStore is the body-owned Ptah content dependency used by Ptah
// authoring tools. The production implementation is internal/agentcontent.Store,
// which is TableTheory-backed over INSTANCE_CONTENT_TABLE.
type AgentContentStore interface {
	Get(ctx context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error)
	Upsert(ctx context.Context, in agentcontent.UpsertInput) (*agentcontent.Record, error)
	Archive(ctx context.Context, in agentcontent.ArchiveInput) (*agentcontent.Record, error)
}

// AgentRegistry is the body-owned Ptah registry dependency used by Ptah agent
// registry tools. It is exported so instance-plane integration tests can inject
// a TableTheory-backed fake store without changing instanceapp behavior.
type AgentRegistry interface {
	Create(ctx context.Context, in agentregistry.CreateInput) (*agentregistry.Agent, error)
	Get(ctx context.Context, account string, agentID string) (*agentregistry.Agent, error)
	List(ctx context.Context, in agentregistry.ListInput) (*agentregistry.ListResult, error)
}

type config struct {
	soulBinding        soulBindingClient
	soulBindingFactory func() (soulBindingClient, error)

	agentDelegateClient  agentDelegateClient
	agentDelegateFactory func() (agentDelegateClient, error)
	agentRegistry        AgentRegistry
	agentRegistryFactory func() (AgentRegistry, error)

	agentContent        AgentContentStore
	agentContentFactory func() (AgentContentStore, error)

	integrationBearerFn func(context.Context) (string, error)
}

// Option configures Ptah tool registration. It is primarily used by tests to
// inject a fake Lesser client or dedicated integration bearer without relying on
// process environment.
type Option func(*config)

// WithSoulBindingClient injects the Lesser soul-binding client used by
// agent_bind_soul.
func WithSoulBindingClient(client soulBindingClient) Option {
	return func(cfg *config) {
		cfg.soulBinding = client
	}
}

// WithAgentDelegateClient injects the Lesser delegate client used by
// agent_create.
func WithAgentDelegateClient(client agentDelegateClient) Option {
	return func(cfg *config) {
		cfg.agentDelegateClient = client
	}
}

// WithAgentRegistryStore injects the body-owned agent registry store used by
// Ptah agent registry tools.
func WithAgentRegistryStore(store AgentRegistry) Option {
	return func(cfg *config) {
		cfg.agentRegistry = store
	}
}

// WithAgentContentStore injects the body-owned agent content store used by
// Ptah authoring tools.
func WithAgentContentStore(store AgentContentStore) Option {
	return func(cfg *config) {
		cfg.agentContent = store
	}
}

// WithIntegrationBearer injects the dedicated Lesser soul-binding integration
// bearer used by agent_bind_soul.
func WithIntegrationBearer(bearer string) Option {
	return func(cfg *config) {
		cfg.integrationBearerFn = func(context.Context) (string, error) {
			return strings.TrimSpace(bearer), nil
		}
	}
}

var (
	agentRegistryFactoryMu       sync.RWMutex
	agentRegistryFactoryForTests func() (AgentRegistry, error)
)

// SetAgentRegistryFactoryForTests overrides the production registry factory for
// tests that construct the instance app, whose public New function intentionally
// does not expose tool-registration dependency injection.
func SetAgentRegistryFactoryForTests(factory func() (AgentRegistry, error)) func() {
	agentRegistryFactoryMu.Lock()
	previous := agentRegistryFactoryForTests
	agentRegistryFactoryForTests = factory
	agentRegistryFactoryMu.Unlock()

	return func() {
		agentRegistryFactoryMu.Lock()
		agentRegistryFactoryForTests = previous
		agentRegistryFactoryMu.Unlock()
	}
}

// RegisterTools statically registers the Ptah instance-plane tool surface.
func RegisterTools(r *mcpruntime.ToolRegistry, opts ...Option) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if err := r.RegisterTool(agentBindSoulDef(), cfg.handleAgentBindSoul); err != nil {
		return err
	}
	if err := r.RegisterTool(agentCreateDef(), cfg.handleAgentCreate); err != nil {
		return err
	}
	if err := r.RegisterTool(agentGetDef(), cfg.handleAgentGet); err != nil {
		return err
	}
	if err := r.RegisterTool(agentListDef(), cfg.handleAgentList); err != nil {
		return err
	}
	if err := r.RegisterTool(agentSoulGetDef(), cfg.handleAgentSoulGet); err != nil {
		return err
	}
	if err := r.RegisterTool(agentSoulUpsertDef(), cfg.handleAgentSoulUpsert); err != nil {
		return err
	}
	if err := r.RegisterTool(agentSoulArchiveDef(), cfg.handleAgentSoulArchive); err != nil {
		return err
	}
	if err := r.RegisterTool(agentInstructionsGetDef(), cfg.handleAgentInstructionsGet); err != nil {
		return err
	}
	if err := r.RegisterTool(agentInstructionsUpsertDef(), cfg.handleAgentInstructionsUpsert); err != nil {
		return err
	}
	return r.RegisterTool(agentInstructionsArchiveDef(), cfg.handleAgentInstructionsArchive)
}

func defaultConfig() config {
	return config{
		soulBindingFactory: func() (soulBindingClient, error) {
			return lesserapi.Default()
		},
		agentDelegateFactory: func() (agentDelegateClient, error) {
			return lesserapi.Default()
		},
		agentRegistryFactory: defaultAgentRegistry,
		agentContentFactory: func() (AgentContentStore, error) {
			return agentcontent.Default()
		},
		integrationBearerFn: func(context.Context) (string, error) {
			return strings.TrimSpace(os.Getenv(EnvSoulBindingIntegrationBearer)), nil
		},
	}
}

func defaultAgentRegistry() (AgentRegistry, error) {
	agentRegistryFactoryMu.RLock()
	factory := agentRegistryFactoryForTests
	agentRegistryFactoryMu.RUnlock()
	if factory != nil {
		return factory()
	}
	return agentregistry.Default()
}

func agentBindSoulDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentBindSoul,
		Title:       "Bind agent soul",
		Description: "Orchestrate Lesser's hosted soul/body binding ceremony for the authenticated account-holder principal. Requires write scope and delegates all binding state writes to Lesser.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"soul_agent_id":{"type":"string","description":"Full Lesser Soul agent identifier to bind to the authenticated account-holder actor."},
				"idempotency_key":{"type":"string","description":"Caller-supplied replay key forwarded as Lesser's Idempotency-Key header."},
				"actor_username":{"type":"string","description":"Optional explicit actor username. When supplied it must match the authenticated account-holder principal."},
				"body_actor_id":{"type":"string","description":"Optional Body/Ptah actor correlation id. Defaults to body://ptah/{actor_username}."},
				"host_registration_id":{"type":"string","description":"Optional Host registration id for ceremony correlation."},
				"host_conversation_id":{"type":"string","description":"Optional Host conversation id for ceremony correlation."},
				"principal_address":{"type":"string","description":"Optional principal wallet/address evidence already verified by Host/Lesser."},
				"evidence":{"type":"object","properties":{
					"host_request_id":{"type":"string"},
					"declaration_hash":{"type":"string"},
					"issued_at":{"type":"string","description":"RFC3339 timestamp supplied by the authoritative ceremony source."}
				},"additionalProperties":false}
			},
			"required":["soul_agent_id","idempotency_key"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","description":"Structured Lesser soul-binding response, idempotency metadata, status link, and agent summary."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentCreateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentCreate,
		Title:       "Create agent runtime delegation",
		Description: "Delegate runtime credentials for an existing Lesser local agent account, then create a body-owned account-scoped Ptah registry entry. Requires an account-holder OAuth principal with write scope.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_username":{"type":"string","description":"Existing Lesser local agent account username to delegate. This tool does not create the Lesser account."},
				"scopes":{"type":"array","items":{"type":"string"},"minItems":1,"description":"Requested delegated OAuth scopes for the agent runtime session."},
				"display_name":{"type":"string","description":"Optional display name passed through to Lesser's delegation endpoint; current Lesser behavior does not create or update accounts with it."},
				"bio":{"type":"string","description":"Optional bio passed through to Lesser's delegation endpoint; current Lesser behavior does not create or update accounts with it."},
				"expires_in":{"type":"integer","description":"Optional access-token/runtime-session TTL in seconds. Lesser validates bounds."},
				"device_label":{"type":"string","description":"Optional runtime-session display label for Lesser."},
				"agent_info":{"description":"Optional future/legacy agent metadata passed to Lesser. Current Lesser delegation accepts but ignores it for existing-agent delegation."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_username","scopes"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","description":"Safe account and registry summary plus structured delegated token credentials."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGet,
		Title:       "Get registered agent",
		Description: "Read a Body/Ptah account-scoped agent registry entry for the authenticated account-holder principal. Requires read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id to read from the authenticated account-holder's Ptah registry partition."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","description":"Account-scoped registry summary plus source-truth content-version/content-summary availability placeholders."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentList,
		Title:       "List registered agents",
		Description: "List Body/Ptah account-scoped agent registry entries for the authenticated account-holder principal. Requires read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type":"object",
			"properties":{
				"limit":{"type":"integer","minimum":1,"maximum":%d,"description":"Optional page size. Defaults to %d."},
				"cursor":{"type":"string","description":"Optional opaque pagination cursor returned by a prior agent_list call."}
			},
			"additionalProperties":false
		}`, agentregistry.MaxListLimit, agentregistry.DefaultListLimit)),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","description":"Account-scoped registry entries and pagination metadata."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentSoulGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulGet,
		Title:       "Get draft agent soul",
		Description: "Read the current account-scoped Ptah agent_soul draft/archived record for the authenticated account-holder principal. Requires read scope. " + agentSoulProvisionalText,
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_soul content is read from the authenticated account-holder's Ptah content partition."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: agentSoulOutputSchema(),
	}
}

func agentSoulUpsertDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulUpsert,
		Title:       "Upsert draft agent soul",
		Description: "Create or update an account-scoped Ptah agent_soul draft through the canonical internal/agentcontent Store. Requires write scope and increments the per-content version via the store. " + agentSoulProvisionalText,
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_soul draft is written in the authenticated account-holder's Ptah content partition."},
				"content":{"type":"string","description":"Provisional agent_soul draft payload. Opaque string pending lesser-soul S1; maximum 65536 bytes."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id","content"],
			"additionalProperties":false
		}`),
		OutputSchema: agentSoulOutputSchema(),
	}
}

func agentSoulArchiveDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulArchive,
		Title:       "Archive draft agent soul",
		Description: "Idempotently archive the current account-scoped Ptah agent_soul record through the canonical internal/agentcontent Store. Requires write scope. " + agentSoulProvisionalText,
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_soul record is archived in the authenticated account-holder's Ptah content partition."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: agentSoulOutputSchema(),
	}
}

func agentSoulOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{"type":"object","description":"Structured account-scoped agent_soul record, provisional schema marker, and archive idempotency metadata when applicable."},
			"error":{"type":"object","description":"Structured tool error when isError=true."}
		}
	}`)
}

func agentInstructionsGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentInstructionsGet,
		Title:       "Get draft agent instructions",
		Description: "Read the current account-scoped Ptah agent_instructions draft/archived record for the authenticated account-holder principal. Requires read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_instructions content is read from the authenticated account-holder's Ptah content partition."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: agentInstructionsOutputSchema(),
	}
}

func agentInstructionsUpsertDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentInstructionsUpsert,
		Title:       "Upsert draft agent instructions",
		Description: "Create or update an account-scoped Ptah agent_instructions draft through the canonical internal/agentcontent Store. Requires write scope and increments the per-content version via the store.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_instructions draft is written in the authenticated account-holder's Ptah content partition."},
				"content":{"type":"string","description":"Agent instructions draft payload. Maximum 65536 bytes."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id","content"],
			"additionalProperties":false
		}`),
		OutputSchema: agentInstructionsOutputSchema(),
	}
}

func agentInstructionsArchiveDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentInstructionsArchive,
		Title:       "Archive draft agent instructions",
		Description: "Idempotently archive the current account-scoped Ptah agent_instructions record through the canonical internal/agentcontent Store. Requires write scope.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Agent id whose agent_instructions record is archived in the authenticated account-holder's Ptah content partition."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: agentInstructionsOutputSchema(),
	}
}

func agentInstructionsOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{"type":"object","description":"Structured account-scoped agent_instructions record and archive idempotency metadata when applicable."},
			"error":{"type":"object","description":"Structured tool error when isError=true."}
		}
	}`)
}

type agentBindSoulInput struct {
	SoulAgentID        string                  `json:"soul_agent_id"`
	IdempotencyKey     string                  `json:"idempotency_key"`
	ActorUsername      string                  `json:"actor_username"`
	BodyActorID        string                  `json:"body_actor_id"`
	HostRegistrationID string                  `json:"host_registration_id"`
	HostConversationID string                  `json:"host_conversation_id"`
	PrincipalAddress   string                  `json:"principal_address"`
	Evidence           agentBindSoulEvidenceIn `json:"evidence"`
}

type agentBindSoulEvidenceIn struct {
	HostRequestID   string `json:"host_request_id"`
	DeclarationHash string `json:"declaration_hash"`
	IssuedAt        string `json:"issued_at"`
}

type agentCreateInput struct {
	AgentUsername string   `json:"agent_username"`
	Scopes        []string `json:"scopes"`
	DisplayName   string   `json:"display_name"`
	Bio           string   `json:"bio"`
	ExpiresIn     int      `json:"expires_in"`
	DeviceLabel   string   `json:"device_label"`
	AgentInfo     any      `json:"agent_info"`
	ActorUsername string   `json:"actor_username"`
}

type agentGetInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentListInput struct {
	Limit  *int   `json:"limit"`
	Cursor string `json:"cursor"`
}

type agentSoulGetInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentSoulUpsertInput struct {
	AgentID       string  `json:"agent_id"`
	Content       *string `json:"content"`
	ActorUsername string  `json:"actor_username"`
}

type agentSoulArchiveInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentInstructionsGetInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentInstructionsUpsertInput struct {
	AgentID       string  `json:"agent_id"`
	Content       *string `json:"content"`
	ActorUsername string  `json:"actor_username"`
}

type agentInstructionsArchiveInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

func (cfg config) handleAgentBindSoul(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentBindSoul)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_bind_soul requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentBindSoulInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	integrationBearer, err := cfg.integrationBearer(ctx)
	if err != nil {
		return nil, err
	}
	if integrationBearer == "" {
		return toolErrorResult("not_configured", EnvSoulBindingIntegrationBearer+" is required", http.StatusInternalServerError, map[string]any{
			"source": "lesser_body_ptah",
		})
	}

	client, err := cfg.soulBindingClient()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "lesser_body_ptah",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentBindSoul,
		"actor_username", actorUsername,
		"soul_agent_id", in.SoulAgentID,
		"idempotency_key_present", in.IdempotencyKey != "",
	)

	req := lesserapi.SoulBindingRequest{
		ActorUsername:      actorUsername,
		SoulAgentID:        in.SoulAgentID,
		BodyActorID:        in.BodyActorID,
		HostRegistrationID: in.HostRegistrationID,
		HostConversationID: in.HostConversationID,
		AuthorityModel:     lesserapi.SoulAuthorityModelInstanceTrust,
		AnchorState:        lesserapi.SoulAnchorStateHostedOffchain,
		OperationalBinding: lesserapi.SoulOperationalBindingHostedBound,
		PrincipalAddress:   in.PrincipalAddress,
		Evidence: lesserapi.SoulBindingEvidence{
			Source:          "ptah",
			HostRequestID:   in.Evidence.HostRequestID,
			DeclarationHash: in.Evidence.DeclarationHash,
			IssuedAt:        in.Evidence.IssuedAt,
		},
	}

	resp, err := client.InitiateSoulBinding(ctx, integrationBearer, in.IdempotencyKey, req)
	if err != nil {
		return soulBindingToolResultFromError(err)
	}
	return soulBindingSuccessResult(actorUsername, in.IdempotencyKey, resp)
}

func (cfg config) handleAgentCreate(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentCreate)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_create requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentCreateInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	callerBearer := strings.TrimSpace(auth.BearerTokenFromToolContext(ctx))
	if callerBearer == "" {
		return toolErrorResult("unauthorized", "agent_create requires the caller OAuth bearer for Lesser delegation", http.StatusUnauthorized, map[string]any{
			"source": "lesser_agent_delegate",
		})
	}
	if gateResult, err := instancex402.RequireGrant(ctx, instancex402.Requirement{
		Tool:       toolAgentCreate,
		Capability: instancex402.CapabilityAgentCreate,
		Account:    actorUsername,
	}); gateResult != nil || err != nil {
		return gateResult, err
	}

	client, err := cfg.agentDelegate()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "lesser_body_ptah",
		})
	}
	registry, err := cfg.registry()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentCreate,
		"actor_username", actorUsername,
		"agent_username", in.AgentUsername,
		"scope_count", len(in.Scopes),
		"bearer_present", true,
	)

	resp, err := client.DelegateAgent(ctx, callerBearer, lesserapi.AgentDelegationRequest{
		AgentUsername: in.AgentUsername,
		DisplayName:   in.DisplayName,
		Bio:           in.Bio,
		Scopes:        append([]string(nil), in.Scopes...),
		ExpiresIn:     in.ExpiresIn,
		DeviceLabel:   in.DeviceLabel,
		AgentInfo:     in.AgentInfo,
	})
	if err != nil {
		return agentDelegateToolResultFromError(err)
	}

	agentID := delegatedAgentID(resp)
	if agentID == "" {
		slog.WarnContext(ctx, "ptah agent_create lesser delegation response missing account id",
			"tool", toolAgentCreate,
			"actor_username", actorUsername,
			"agent_username", in.AgentUsername,
		)
		return agentRegistryPartialFailureResult("agent_registry_error", "Lesser delegated credentials but response did not include an account id for registry creation", http.StatusBadGateway, map[string]any{
			"source":                    "lesser_agent_delegate",
			"lesserDelegationSucceeded": true,
			"mintedCredentialsMayExist": true,
			"reconciliationRequired":    true,
		})
	}

	created, err := registry.Create(ctx, agentregistry.CreateInput{
		Account: actorUsername,
		AgentID: agentID,
	})
	if err != nil {
		if errors.Is(err, agentregistry.ErrAgentAlreadyExists) {
			slog.InfoContext(ctx, "ptah agent_create registry duplicate",
				"tool", toolAgentCreate,
				"actor_username", actorUsername,
				"agent_username", in.AgentUsername,
				"lesser_delegation_succeeded", true,
			)
			return toolErrorResult("agent_already_exists", "agent already exists in this account-scoped Ptah registry; Lesser may have minted fresh credentials before the duplicate was detected", http.StatusConflict, map[string]any{
				"source":                    "agent_registry",
				"lesserDelegationSucceeded": true,
				"mintedCredentialsMayExist": true,
				"reconciliationRequired":    true,
			})
		}
		slog.WarnContext(ctx, "ptah agent_create registry create failed after lesser delegation",
			"tool", toolAgentCreate,
			"actor_username", actorUsername,
			"agent_username", in.AgentUsername,
			"error", err.Error(),
			"lesser_delegation_succeeded", true,
		)
		return agentRegistryPartialFailureResult("agent_registry_error", "Lesser delegated credentials but Body failed to create the Ptah registry entry", http.StatusInternalServerError, map[string]any{
			"source":                    "agent_registry",
			"lesserDelegationSucceeded": true,
			"mintedCredentialsMayExist": true,
			"reconciliationRequired":    true,
		})
	}

	slog.InfoContext(ctx, "ptah agent_create completed",
		"tool", toolAgentCreate,
		"actor_username", actorUsername,
		"agent_username", in.AgentUsername,
		"agent_id", agentID,
	)
	return agentCreateSuccessResult(actorUsername, resp, created)
}

func (cfg config) handleAgentGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentGet)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasReadScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_get requires read scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"read"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentGetInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	registry, err := cfg.registry()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentGet,
		"actor_username", actorUsername,
	)

	agent, err := registry.Get(ctx, actorUsername, in.AgentID)
	if err != nil {
		if errors.Is(err, agentregistry.ErrAgentNotFound) {
			return toolErrorResult("not_found", "agent not found in this account-scoped Ptah registry", http.StatusNotFound, map[string]any{
				"source": "agent_registry",
			})
		}
		slog.WarnContext(ctx, "ptah agent_get registry read failed",
			"tool", toolAgentGet,
			"actor_username", actorUsername,
			"error", err.Error(),
		)
		return toolErrorResult("agent_registry_error", "Body failed to read the Ptah registry entry", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
		})
	}
	return agentGetSuccessResult(actorUsername, agent)
}

func (cfg config) handleAgentList(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentList)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasReadScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_list requires read scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"read"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentListInput(args)
	if errResult != nil || err != nil {
		return errResult, err
	}

	registry, err := cfg.registry()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
		})
	}

	limit := agentregistry.DefaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentList,
		"actor_username", actorUsername,
		"limit", limit,
		"cursor_present", strings.TrimSpace(in.Cursor) != "",
	)

	page, err := registry.List(ctx, agentregistry.ListInput{
		Account: actorUsername,
		Limit:   limit,
		Cursor:  in.Cursor,
	})
	if err != nil {
		switch {
		case errors.Is(err, agentregistry.ErrInvalidCursor):
			return toolErrorResult("invalid_request", "cursor is invalid", http.StatusBadRequest, map[string]any{
				"source": "agent_registry",
			})
		case errors.Is(err, agentregistry.ErrInvalidLimit):
			return toolErrorResult("invalid_request", "limit is invalid", http.StatusBadRequest, map[string]any{
				"source": "agent_registry",
			})
		default:
			slog.WarnContext(ctx, "ptah agent_list registry list failed",
				"tool", toolAgentList,
				"actor_username", actorUsername,
				"error", err.Error(),
			)
			return toolErrorResult("agent_registry_error", "Body failed to list Ptah registry entries", http.StatusInternalServerError, map[string]any{
				"source": "agent_registry",
			})
		}
	}
	return agentListSuccessResult(actorUsername, limit, page)
}

func (cfg config) handleAgentSoulGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentSoulGet)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasReadScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_soul_get requires read scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"read"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentSoulGetInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentSoulGet,
		"actor_username", actorUsername,
	)

	record, err := store.Get(ctx, actorUsername, in.AgentID, agentcontent.ContentTypeAgentSoul)
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	return agentSoulSuccessResult("Ptah agent_soul record read", actorUsername, record, nil)
}

func (cfg config) handleAgentSoulUpsert(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentSoulUpsert)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_soul_upsert requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}
	subjectID, errResult := authenticatedSubjectID(principal, toolAgentSoulUpsert)
	if errResult != nil {
		return errResult, nil
	}

	in, errResult, err := parseAgentSoulUpsertInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	content := ""
	if in.Content != nil {
		content = *in.Content
	}
	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentSoulUpsert,
		"actor_username", actorUsername,
		"content_bytes", len([]byte(content)),
	)

	record, err := store.Upsert(ctx, agentcontent.UpsertInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentSoul,
		Content:            content,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	return agentSoulSuccessResult("Ptah agent_soul draft upserted", actorUsername, record, nil)
}

func (cfg config) handleAgentSoulArchive(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentSoulArchive)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_soul_archive requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}
	subjectID, errResult := authenticatedSubjectID(principal, toolAgentSoulArchive)
	if errResult != nil {
		return errResult, nil
	}

	in, errResult, err := parseAgentSoulArchiveInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentSoulArchive,
		"actor_username", actorUsername,
	)

	current, err := store.Get(ctx, actorUsername, in.AgentID, agentcontent.ContentTypeAgentSoul)
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	alreadyArchived := current != nil && current.LifecycleState == agentcontent.LifecycleStateArchived
	record, err := store.Archive(ctx, agentcontent.ArchiveInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentSoul,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	return agentSoulSuccessResult("Ptah agent_soul record archived", actorUsername, record, map[string]any{
		"already_archived": alreadyArchived,
		"idempotent":       true,
	})
}

func (cfg config) handleAgentInstructionsGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentInstructionsGet)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasReadScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_instructions_get requires read scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"read"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentInstructionsGetInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentInstructionsGet,
		"actor_username", actorUsername,
	)

	record, err := store.Get(ctx, actorUsername, in.AgentID, agentcontent.ContentTypeAgentInstructions)
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentInstructions)
	}
	return agentInstructionsSuccessResult("Ptah agent_instructions record read", actorUsername, record, nil)
}

func (cfg config) handleAgentInstructionsUpsert(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentInstructionsUpsert)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_instructions_upsert requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}
	subjectID, errResult := authenticatedSubjectID(principal, toolAgentInstructionsUpsert)
	if errResult != nil {
		return errResult, nil
	}

	in, errResult, err := parseAgentInstructionsUpsertInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	content := ""
	if in.Content != nil {
		content = *in.Content
	}
	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentInstructionsUpsert,
		"actor_username", actorUsername,
		"content_bytes", len([]byte(content)),
	)

	record, err := store.Upsert(ctx, agentcontent.UpsertInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentInstructions,
		Content:            content,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentInstructions)
	}
	return agentInstructionsSuccessResult("Ptah agent_instructions draft upserted", actorUsername, record, nil)
}

func (cfg config) handleAgentInstructionsArchive(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentInstructionsArchive)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_instructions_archive requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}
	subjectID, errResult := authenticatedSubjectID(principal, toolAgentInstructionsArchive)
	if errResult != nil {
		return errResult, nil
	}

	in, errResult, err := parseAgentInstructionsArchiveInput(args, actorUsername)
	if errResult != nil || err != nil {
		return errResult, err
	}

	store, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentInstructionsArchive,
		"actor_username", actorUsername,
	)

	current, err := store.Get(ctx, actorUsername, in.AgentID, agentcontent.ContentTypeAgentInstructions)
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentInstructions)
	}
	alreadyArchived := current != nil && current.LifecycleState == agentcontent.LifecycleStateArchived
	record, err := store.Archive(ctx, agentcontent.ArchiveInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentInstructions,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentInstructions)
	}
	return agentInstructionsSuccessResult("Ptah agent_instructions record archived", actorUsername, record, map[string]any{
		"already_archived": alreadyArchived,
		"idempotent":       true,
	})
}

func authenticatedAccountHolderActor(principal *auth.Principal, toolName string) (string, *mcpruntime.ToolResult, error) {
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil || principal.Claims.IsAgent {
		return "", mustToolErrorResult("forbidden", toolName+" requires an account-holder OAuth principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	actorUsername := normalizeActorUsername(firstNonEmpty(principal.Claims.GetUsername(), principal.Identity))
	if actorUsername == "" {
		return "", mustToolErrorResult("forbidden", toolName+" requires an authenticated actor username", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return actorUsername, nil, nil
}

func authenticatedSubjectID(principal *auth.Principal, toolName string) (string, *mcpruntime.ToolResult) {
	subjectID := ""
	if principal != nil && principal.Claims != nil {
		subjectID = strings.TrimSpace(principal.Claims.Subject)
	}
	if subjectID == "" {
		return "", mustToolErrorResult("forbidden", toolName+" requires an authenticated subject id", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		})
	}
	return subjectID, nil
}

func parseAgentBindSoulInput(args json.RawMessage, actorUsername string) (agentBindSoulInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentBindSoulInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return agentBindSoulInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.SoulAgentID = strings.TrimSpace(in.SoulAgentID)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	in.BodyActorID = strings.TrimSpace(in.BodyActorID)
	in.HostRegistrationID = strings.TrimSpace(in.HostRegistrationID)
	in.HostConversationID = strings.TrimSpace(in.HostConversationID)
	in.PrincipalAddress = strings.TrimSpace(in.PrincipalAddress)
	in.Evidence.HostRequestID = strings.TrimSpace(in.Evidence.HostRequestID)
	in.Evidence.DeclarationHash = strings.TrimSpace(in.Evidence.DeclarationHash)
	in.Evidence.IssuedAt = strings.TrimSpace(in.Evidence.IssuedAt)

	if in.SoulAgentID == "" {
		return agentBindSoulInput{}, mustToolErrorResult("invalid_request", "soul_agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.IdempotencyKey == "" {
		return agentBindSoulInput{}, mustToolErrorResult("invalid_request", "idempotency_key is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentBindSoulInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	if in.BodyActorID == "" {
		in.BodyActorID = "body://ptah/" + actorUsername
	}
	return in, nil, nil
}

func parseAgentCreateInput(args json.RawMessage, actorUsername string) (agentCreateInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentCreateInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentCreateInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentUsername = strings.TrimSpace(in.AgentUsername)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Bio = strings.TrimSpace(in.Bio)
	in.DeviceLabel = strings.TrimSpace(in.DeviceLabel)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)

	if in.AgentUsername == "" {
		return agentCreateInput{}, mustToolErrorResult("invalid_request", "agent_username is required", http.StatusBadRequest, nil), nil
	}
	if len(in.Scopes) == 0 {
		return agentCreateInput{}, mustToolErrorResult("invalid_request", "scopes are required", http.StatusBadRequest, nil), nil
	}
	for i, scope := range in.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return agentCreateInput{}, mustToolErrorResult("invalid_request", "scopes cannot contain empty values", http.StatusBadRequest, nil), nil
		}
		in.Scopes[i] = scope
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentCreateInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentGetInput(args json.RawMessage, actorUsername string) (agentGetInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentGetInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentGetInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)

	if in.AgentID == "" {
		return agentGetInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentGetInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentListInput(args json.RawMessage) (agentListInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentListInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentListInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.Cursor = strings.TrimSpace(in.Cursor)
	if in.Limit != nil {
		switch limit := *in.Limit; {
		case limit <= 0:
			return agentListInput{}, mustToolErrorResult("invalid_request", "limit must be positive", http.StatusBadRequest, nil), nil
		case limit > agentregistry.MaxListLimit:
			return agentListInput{}, mustToolErrorResult("invalid_request", fmt.Sprintf("limit must be <= %d", agentregistry.MaxListLimit), http.StatusBadRequest, nil), nil
		}
	}
	return in, nil, nil
}

func parseAgentSoulGetInput(args json.RawMessage, actorUsername string) (agentSoulGetInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentSoulGetInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentSoulGetInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentSoulGetInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentSoulGetInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentSoulUpsertInput(args json.RawMessage, actorUsername string) (agentSoulUpsertInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentSoulUpsertInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.Content == nil {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", "content is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentSoulUpsertInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentSoulArchiveInput(args json.RawMessage, actorUsername string) (agentSoulArchiveInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentSoulArchiveInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentSoulArchiveInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentSoulArchiveInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentSoulArchiveInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentInstructionsGetInput(args json.RawMessage, actorUsername string) (agentInstructionsGetInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentInstructionsGetInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentInstructionsGetInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentInstructionsGetInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentInstructionsGetInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentInstructionsUpsertInput(args json.RawMessage, actorUsername string) (agentInstructionsUpsertInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentInstructionsUpsertInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentInstructionsUpsertInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentInstructionsUpsertInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.Content == nil {
		return agentInstructionsUpsertInput{}, mustToolErrorResult("invalid_request", "content is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentInstructionsUpsertInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func parseAgentInstructionsArchiveInput(args json.RawMessage, actorUsername string) (agentInstructionsArchiveInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentInstructionsArchiveInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentInstructionsArchiveInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if in.AgentID == "" {
		return agentInstructionsArchiveInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentInstructionsArchiveInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func (cfg config) soulBindingClient() (soulBindingClient, error) {
	if cfg.soulBinding != nil {
		return cfg.soulBinding, nil
	}
	if cfg.soulBindingFactory == nil {
		return nil, fmt.Errorf("soul-binding client is not configured")
	}
	return cfg.soulBindingFactory()
}

func (cfg config) agentDelegate() (agentDelegateClient, error) {
	if cfg.agentDelegateClient != nil {
		return cfg.agentDelegateClient, nil
	}
	if cfg.agentDelegateFactory == nil {
		return nil, fmt.Errorf("agent delegate client is not configured")
	}
	return cfg.agentDelegateFactory()
}

func (cfg config) registry() (AgentRegistry, error) {
	if cfg.agentRegistry != nil {
		return cfg.agentRegistry, nil
	}
	if cfg.agentRegistryFactory == nil {
		return nil, fmt.Errorf("agent registry store is not configured")
	}
	return cfg.agentRegistryFactory()
}

func (cfg config) content() (AgentContentStore, error) {
	if cfg.agentContent != nil {
		return cfg.agentContent, nil
	}
	if cfg.agentContentFactory == nil {
		return nil, fmt.Errorf("agent content store is not configured")
	}
	return cfg.agentContentFactory()
}

func (cfg config) integrationBearer(ctx context.Context) (string, error) {
	if cfg.integrationBearerFn == nil {
		return "", nil
	}
	bearer, err := cfg.integrationBearerFn(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(bearer), nil
}

func soulBindingSuccessResult(actorUsername string, idempotencyKey string, resp *lesserapi.SoulBindingResponse) (*mcpruntime.ToolResult, error) {
	respMap, err := mapFromJSON(resp)
	if err != nil {
		return nil, err
	}

	agentSummary := map[string]any{}
	if resp != nil {
		agentSummary = map[string]any{
			"agent_id":              resp.Agent.AgentID,
			"domain":                resp.Agent.Domain,
			"local_id":              resp.Agent.LocalID,
			"authority_model":       resp.Agent.AuthorityModel,
			"anchor_state":          resp.Agent.AnchorState,
			"operational_binding":   resp.Agent.OperationalBinding,
			"lifecycle_status":      resp.Agent.LifecycleStatus,
			"binding_state":         resp.BindingState,
			"status":                resp.Status,
			"actor_username":        actorUsername,
			"principal_bound_actor": resp.Binding.AgentUsername,
		}
	}

	var idempotency any
	var replayed bool
	if resp != nil && resp.Idempotency != nil {
		idempotencyMap, err := mapFromJSON(resp.Idempotency)
		if err != nil {
			return nil, err
		}
		idempotency = idempotencyMap
		replayed = resp.Idempotency.Replayed
	} else {
		idempotency = map[string]any{"key": idempotencyKey}
	}

	statusLink := ""
	if resp != nil && resp.Links != nil {
		statusLink = resp.Links.Status
	}

	data := map[string]any{
		"actor_username":  actorUsername,
		"lesser_response": respMap,
		"idempotency":     idempotency,
		"replayed":        replayed,
		"status_link":     statusLink,
		"agent_summary":   agentSummary,
	}

	text := map[string]any{
		"summary":     "Lesser soul/body binding orchestration completed",
		"replayed":    replayed,
		"status_link": statusLink,
		"agent":       agentSummary,
		"data":        map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func agentCreateSuccessResult(actorUsername string, resp *lesserapi.AgentDelegationResponse, registry *agentregistry.Agent) (*mcpruntime.ToolResult, error) {
	if resp == nil {
		return nil, fmt.Errorf("agent delegation response is nil")
	}
	accountMap, err := mapFromJSON(resp.Account)
	if err != nil {
		return nil, err
	}
	tokenMap, err := mapFromJSON(resp.Token)
	if err != nil {
		return nil, err
	}

	accountSummary := map[string]any{
		"id":           resp.Account.ID,
		"username":     resp.Account.Username,
		"acct":         resp.Account.Acct,
		"display_name": resp.Account.DisplayName,
		"bot":          resp.Account.Bot,
		"url":          resp.Account.URL,
	}
	tokenMetadata := map[string]any{
		"token_type":          resp.Token.TokenType,
		"expires_in":          resp.Token.ExpiresIn,
		"scope":               resp.Token.Scope,
		"created_at":          resp.Token.CreatedAt,
		"has_refresh_token":   resp.Token.RefreshToken != "",
		"credential_location": "structuredContent.data.token",
	}

	registrySummary := map[string]any{}
	if registry != nil {
		registrySummary = map[string]any{
			"account":    registry.Account,
			"agent_id":   registry.AgentID,
			"created_at": formatTime(registry.CreatedAt),
			"updated_at": formatTime(registry.UpdatedAt),
		}
	}

	data := map[string]any{
		"actor_username":       actorUsername,
		"current_behavior":     "delegates_existing_lesser_agent_account",
		"lesser_account":       accountMap,
		"account_summary":      accountSummary,
		"registry":             registrySummary,
		"token":                tokenMap,
		"token_metadata":       tokenMetadata,
		"reconciliation_notes": "Lesser delegation is non-idempotent; if registry creation fails after delegation, Body cannot roll back minted Lesser credentials.",
	}

	text := map[string]any{
		"summary":        "Lesser agent runtime credentials delegated and Ptah registry entry created",
		"account":        accountSummary,
		"registry":       registrySummary,
		"token_metadata": tokenMetadata,
		"data":           map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func agentGetSuccessResult(actorUsername string, agent *agentregistry.Agent) (*mcpruntime.ToolResult, error) {
	registrySummary := registryAgentSummary(agent)
	contentVersion := unavailableContentVersion()
	contentSummary := unavailableContentSummary()
	data := map[string]any{
		"actor_username":  actorUsername,
		"registry":        registrySummary,
		"content_version": contentVersion,
		"content_summary": contentSummary,
	}

	text := map[string]any{
		"summary":         "Ptah registry entry read",
		"registry":        registrySummary,
		"content_version": contentVersion,
		"content_summary": contentSummary,
		"data":            map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func agentListSuccessResult(actorUsername string, limit int, page *agentregistry.ListResult) (*mcpruntime.ToolResult, error) {
	agents := []*agentregistry.Agent(nil)
	nextCursor := ""
	hasMore := false
	count := 0
	if page != nil {
		agents = page.Agents
		nextCursor = page.NextCursor
		hasMore = page.HasMore
		count = page.Count
	}
	items := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		items = append(items, map[string]any{
			"registry":        registryAgentSummary(agent),
			"content_version": unavailableContentVersion(),
			"content_summary": unavailableContentSummary(),
		})
	}

	pagination := map[string]any{
		"limit":       limit,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
		"count":       count,
	}
	data := map[string]any{
		"actor_username": actorUsername,
		"agents":         items,
		"pagination":     pagination,
	}

	text := map[string]any{
		"summary":    "Ptah registry entries listed",
		"count":      count,
		"has_more":   hasMore,
		"pagination": pagination,
		"data":       map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func agentSoulSuccessResult(summary string, actorUsername string, record *agentcontent.Record, extra map[string]any) (*mcpruntime.ToolResult, error) {
	recordSummary := agentContentRecordSummary(record)
	data := map[string]any{
		"actor_username": actorUsername,
		"agent_soul":     recordSummary,
		"schema":         agentSoulSchemaMarker(),
	}
	for key, value := range extra {
		data[key] = value
	}

	text := map[string]any{
		"summary": summary,
		"agent_soul": map[string]any{
			"account":          recordSummary["account"],
			"agent_id":         recordSummary["agent_id"],
			"version":          recordSummary["version"],
			"lifecycle_state":  recordSummary["lifecycle_state"],
			"content_bytes":    recordSummary["content_bytes"],
			"schema_marker":    agentSoulProvisionalMarker,
			"content_location": "structuredContent.data.agent_soul.content",
		},
		"schema": agentSoulSchemaMarker(),
		"data":   map[string]any{"location": "structuredContent.data"},
	}
	if len(extra) > 0 {
		text["metadata"] = extra
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func agentInstructionsSuccessResult(summary string, actorUsername string, record *agentcontent.Record, extra map[string]any) (*mcpruntime.ToolResult, error) {
	recordSummary := agentContentRecordSummary(record)
	data := map[string]any{
		"actor_username":     actorUsername,
		"agent_instructions": recordSummary,
	}
	for key, value := range extra {
		data[key] = value
	}

	text := map[string]any{
		"summary": summary,
		"agent_instructions": map[string]any{
			"account":          recordSummary["account"],
			"agent_id":         recordSummary["agent_id"],
			"version":          recordSummary["version"],
			"lifecycle_state":  recordSummary["lifecycle_state"],
			"content_bytes":    recordSummary["content_bytes"],
			"content_location": "structuredContent.data.agent_instructions.content",
		},
		"data": map[string]any{"location": "structuredContent.data"},
	}
	if len(extra) > 0 {
		text["metadata"] = extra
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func soulBindingToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}

	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		details := map[string]any{
			"source":         "lesser_soul_binding",
			"upstreamStatus": apiErr.Status,
			"upstreamBody":   string(apiErr.Body),
		}
		if parsed := parseJSONObject(apiErr.Body); parsed != nil {
			details["upstreamJSON"] = parsed
		}
		return toolErrorResult(lesserAPIErrorCode(apiErr.Status), "Lesser soul-binding API request failed", apiErr.Status, details)
	}

	return toolErrorResult("upstream_error", err.Error(), http.StatusBadGateway, map[string]any{
		"source": "lesser_soul_binding",
	})
}

func agentDelegateToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}

	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		details := map[string]any{
			"source":         "lesser_agent_delegate",
			"upstreamStatus": apiErr.Status,
			"upstreamBody":   redactedAPIErrorBody(apiErr.Body),
		}
		if parsed := parseJSONObject(apiErr.Body); parsed != nil {
			redactCredentialFields(parsed)
			details["upstreamJSON"] = parsed
		}
		return toolErrorResult(lesserAPIErrorCode(apiErr.Status), "Lesser agent delegation API request failed", apiErr.Status, details)
	}

	return toolErrorResult("upstream_error", err.Error(), http.StatusBadGateway, map[string]any{
		"source": "lesser_agent_delegate",
	})
}

func agentContentToolResultFromError(err error, contentType agentcontent.ContentType) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}

	contentName := string(contentType)
	if contentName == "" {
		contentName = "agent content"
	}
	details := map[string]any{
		"source":       "agent_content",
		"content_type": contentName,
	}
	var sizeErr *agentcontent.SizeError
	switch {
	case errors.Is(err, agentcontent.ErrContentNotFound):
		return toolErrorResult("not_found", contentName+" record not found in this account-scoped Ptah content store", http.StatusNotFound, details)
	case errors.As(err, &sizeErr):
		details["limit_bytes"] = sizeErr.Limit
		details["actual_bytes"] = sizeErr.Actual
		return toolErrorResult("invalid_request", contentName+" content exceeds the per-record size limit", http.StatusBadRequest, details)
	case errors.Is(err, agentcontent.ErrInvalidContentType):
		return toolErrorResult("invalid_request", "invalid agent content type", http.StatusBadRequest, details)
	case errors.Is(err, agentcontent.ErrMissingUpdatedBySubjectID):
		return toolErrorResult("forbidden", contentName+" writes require an authenticated subject id", http.StatusForbidden, details)
	case errors.Is(err, agentcontent.ErrContentConflict):
		return toolErrorResult("conflict", contentName+" content update conflict; retry the operation", http.StatusConflict, details)
	case errors.Is(err, agentcontent.ErrInvalidLifecycleState):
		return toolErrorResult("internal", contentName+" content record has invalid lifecycle state", http.StatusInternalServerError, details)
	default:
		return toolErrorResult("internal", "Body failed to access the Ptah "+contentName+" content record", http.StatusInternalServerError, details)
	}
}

func agentRegistryPartialFailureResult(code string, message string, status int, details map[string]any) (*mcpruntime.ToolResult, error) {
	if details == nil {
		details = map[string]any{}
	}
	details["partialFailure"] = true
	return toolErrorResult(code, message, status, details)
}

func lesserAPIErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	default:
		if status >= 500 {
			return "upstream_unavailable"
		}
		return "upstream_error"
	}
}

func toolJSONTextResult(text any, structured map[string]any) (*mcpruntime.ToolResult, error) {
	b, err := json.Marshal(text)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result text: %w", err)
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		StructuredContent: structured,
	}, nil
}

func toolErrorResult(code string, message string, status int, details map[string]any) (*mcpruntime.ToolResult, error) {
	payload := map[string]any{
		"code":    firstNonEmpty(strings.TrimSpace(code), "unknown_error"),
		"message": firstNonEmpty(strings.TrimSpace(message), "error"),
	}
	if status != 0 {
		payload["status"] = status
	}
	if len(details) > 0 {
		payload["details"] = details
	}
	return toolErrorResultPayload(payload)
}

func mustToolErrorResult(code string, message string, status int, details map[string]any) *mcpruntime.ToolResult {
	res, err := toolErrorResult(code, message, status, details)
	if err != nil {
		panic(err)
	}
	return res
}

func toolErrorResultPayload(payload map[string]any) (*mcpruntime.ToolResult, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool error: %w", err)
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{Type: "text", Text: string(b)}},
		IsError: true,
		StructuredContent: map[string]any{
			"error": payload,
		},
	}, nil
}

func readOnlyToolAnnotations() *mcpruntime.ToolAnnotations {
	return &mcpruntime.ToolAnnotations{
		ReadOnlyHint:    boolHint(true),
		DestructiveHint: boolHint(false),
		IdempotentHint:  boolHint(true),
	}
}

func additiveMutationToolAnnotations() *mcpruntime.ToolAnnotations {
	return &mcpruntime.ToolAnnotations{
		ReadOnlyHint:    boolHint(false),
		DestructiveHint: boolHint(false),
		IdempotentHint:  boolHint(false),
	}
}

func idempotentMutationToolAnnotations() *mcpruntime.ToolAnnotations {
	return &mcpruntime.ToolAnnotations{
		ReadOnlyHint:    boolHint(false),
		DestructiveHint: boolHint(false),
		IdempotentHint:  boolHint(true),
	}
}

func boolHint(value bool) *bool {
	return &value
}

func principalHasReadScope(principal *auth.Principal) bool {
	if principal == nil || principal.Claims == nil {
		return false
	}
	for _, scope := range principal.Claims.Scopes {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "read", "write", "admin":
			return true
		}
	}
	return false
}

func principalHasWriteScope(principal *auth.Principal) bool {
	if principal == nil || principal.Claims == nil {
		return false
	}
	for _, scope := range principal.Claims.Scopes {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "write", "admin":
			return true
		}
	}
	return false
}

func normalizedScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func normalizeActorUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func registryAgentSummary(agent *agentregistry.Agent) map[string]any {
	if agent == nil {
		return map[string]any{}
	}
	return map[string]any{
		"account":    agent.Account,
		"agent_id":   agent.AgentID,
		"created_at": formatTime(agent.CreatedAt),
		"updated_at": formatTime(agent.UpdatedAt),
	}
}

func unavailableContentVersion() map[string]any {
	return map[string]any{
		"status": "not_available",
		"source": "agentregistry",
		"reason": "Body/Ptah registry records do not currently store source-backed content version metadata.",
	}
}

func unavailableContentSummary() map[string]any {
	return map[string]any{
		"status": "not_available",
		"source": "agentregistry",
		"reason": "Body/Ptah registry records do not currently store source-backed content summary metadata.",
	}
}

func agentContentRecordSummary(record *agentcontent.Record) map[string]any {
	if record == nil {
		return map[string]any{}
	}
	return map[string]any{
		"account":               record.Account,
		"agent_id":              record.AgentID,
		"type":                  string(record.Type),
		"content":               record.Content,
		"content_bytes":         len([]byte(record.Content)),
		"version":               record.Version,
		"lifecycle_state":       string(record.LifecycleState),
		"created_at":            formatTime(record.CreatedAt),
		"updated_at":            formatTime(record.UpdatedAt),
		"updated_by_subject_id": record.UpdatedBySubjectID,
	}
}

func agentSoulSchemaMarker() map[string]any {
	return map[string]any{
		"content_type": string(agentcontent.ContentTypeAgentSoul),
		"status":       "provisional",
		"marker":       agentSoulProvisionalMarker,
		"description":  agentSoulProvisionalText,
	}
}

func delegatedAgentID(resp *lesserapi.AgentDelegationResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Account.ID)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func mapFromJSON(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal structured value: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal structured value: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func parseJSONObject(raw []byte) map[string]any {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

var credentialFieldPattern = regexp.MustCompile(`(?i)("(?:access|refresh)_token"\s*:\s*)"[^"]*"`)

func redactedAPIErrorBody(raw []byte) string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		redactCredentialFields(decoded)
		if b, err := json.Marshal(decoded); err == nil {
			return string(b)
		}
	}
	return credentialFieldPattern.ReplaceAllString(string(raw), `${1}"<redacted>"`)
}

func redactCredentialFields(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "access_token", "refresh_token":
				v[key] = "<redacted>"
			default:
				redactCredentialFields(child)
			}
		}
	case []any:
		for _, child := range v {
			redactCredentialFields(child)
		}
	}
}
