package ptahserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/actorendpoint"
	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

const (
	// EnvSoulBindingIntegrationBearer is the dedicated server-to-server bearer
	// Body/Ptah uses for Lesser's hosted soul/body binding API. It must not be a
	// caller OAuth token.
	EnvSoulBindingIntegrationBearer = "LESSER_SOUL_BINDING_INTEGRATION_BEARER"
	// EnvSoulBindingIntegrationBearerARN points at the Secrets Manager secret
	// containing EnvSoulBindingIntegrationBearer. Managed deployments should
	// prefer this ARN-backed path so raw bearer values never appear in templates
	// or Lambda environment snapshots.
	EnvSoulBindingIntegrationBearerARN = "LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN"

	toolAgentBindSoul = "agent_bind_soul"
	toolAgentGet      = "agent_get"
	toolAgentList     = "agent_list"

	toolAgentSoulGet     = "agent_soul_get"
	toolAgentSoulUpsert  = "agent_soul_upsert"
	toolAgentSoulPublish = "agent_soul_publish"
	toolAgentSoulArchive = "agent_soul_archive"

	toolAgentInstructionsGet     = "agent_instructions_get"
	toolAgentInstructionsUpsert  = "agent_instructions_upsert"
	toolAgentInstructionsArchive = "agent_instructions_archive"
)

type soulBindingClient interface {
	InitiateSoulBinding(ctx context.Context, integrationBearer string, idempotencyKey string, req lesserapi.SoulBindingRequest) (*lesserapi.SoulBindingResponse, error)
}

type hostIdentityClient interface {
	GetAgentIdentity(ctx context.Context, agentID string) (*hostapi.AgentIdentity, error)
}

// AgentContentStore is the body-owned Ptah content dependency used by Ptah
// authoring tools. The production implementation is internal/agentcontent.Store,
// which is TableTheory-backed over INSTANCE_CONTENT_TABLE.
type AgentContentStore interface {
	Get(ctx context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error)
	Upsert(ctx context.Context, in agentcontent.UpsertInput) (*agentcontent.Record, error)
	Publish(ctx context.Context, in agentcontent.PublishInput) (*agentcontent.Record, error)
	SeedPublished(ctx context.Context, in agentcontent.SeedPublishedInput) (*agentcontent.Record, bool, error)
	SeedInstructions(ctx context.Context, in agentcontent.SeedInstructionsInput) (*agentcontent.Record, bool, error)
	Archive(ctx context.Context, in agentcontent.ArchiveInput) (*agentcontent.Record, error)
}

// AgentRegistry is the body-owned Ptah registry dependency used by Ptah agent
// registry tools. It is exported so instance-plane integration tests can inject
// a TableTheory-backed fake store without changing instanceapp behavior.
type AgentRegistry interface {
	Create(ctx context.Context, in agentregistry.CreateInput) (*agentregistry.Agent, error)
	UpsertFinalized(ctx context.Context, in agentregistry.FinalizedInput) (*agentregistry.Agent, bool, error)
	Get(ctx context.Context, account string, agentID string) (*agentregistry.Agent, error)
	List(ctx context.Context, in agentregistry.ListInput) (*agentregistry.ListResult, error)
}

// AgentLiveClient is the read-only Lesser public live-agent directory
// dependency used by Ptah agent_list. Its implementation is the typed client
// in internal/lesserapi; it never receives the caller's Ptah bearer token.
type AgentLiveClient interface {
	ListAgents(ctx context.Context) ([]lesserapi.AgentDirectoryEntry, error)
}

type config struct {
	soulBinding        soulBindingClient
	soulBindingFactory func() (soulBindingClient, error)

	genesisClient        hostapi.GenesisClient
	genesisFactory       func() (hostapi.GenesisClient, error)
	hostIdentityClient   hostIdentityClient
	hostIdentityFactory  func() (hostIdentityClient, error)
	agentRegistry        AgentRegistry
	agentRegistryFactory func() (AgentRegistry, error)
	agentLiveClient      AgentLiveClient
	agentLiveFactory     func() (AgentLiveClient, error)

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

// WithGenesisClient injects the lesser-host registration/mint-conversation
// client used by the Ptah genesis tools.
func WithGenesisClient(client hostapi.GenesisClient) Option {
	return func(cfg *config) {
		cfg.genesisClient = client
	}
}

// WithHostIdentityClient injects the lesser-host public identity client used by
// agent_bind_soul to repair/verify Host-derived local actor mappings.
func WithHostIdentityClient(client hostIdentityClient) Option {
	return func(cfg *config) {
		cfg.hostIdentityClient = client
	}
}

// WithAgentRegistryStore injects the body-owned agent registry store used by
// Ptah agent registry tools.
func WithAgentRegistryStore(store AgentRegistry) Option {
	return func(cfg *config) {
		cfg.agentRegistry = store
	}
}

// WithAgentLiveClient injects the read-only Lesser public live-agent client
// used by agent_list.
func WithAgentLiveClient(client AgentLiveClient) Option {
	return func(cfg *config) {
		cfg.agentLiveClient = client
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
	agentLiveFactoryMu           sync.RWMutex
	agentLiveFactoryForTests     func() (AgentLiveClient, error)
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

// SetAgentLiveClientFactoryForTests overrides the production Lesser live-agent
// client factory for tests that construct the instance app, whose public New
// function intentionally does not expose tool-registration dependency
// injection.
func SetAgentLiveClientFactoryForTests(factory func() (AgentLiveClient, error)) func() {
	agentLiveFactoryMu.Lock()
	previous := agentLiveFactoryForTests
	agentLiveFactoryForTests = factory
	agentLiveFactoryMu.Unlock()

	return func() {
		agentLiveFactoryMu.Lock()
		agentLiveFactoryForTests = previous
		agentLiveFactoryMu.Unlock()
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

	if err := registerTool(r, agentBindSoulDef(), cfg.handleAgentBindSoul); err != nil {
		return err
	}
	if err := registerTool(r, agentGetDef(), cfg.handleAgentGet); err != nil {
		return err
	}
	if err := registerTool(r, agentListDef(), cfg.handleAgentList); err != nil {
		return err
	}
	if err := registerTool(r, agentSoulGetDef(), cfg.handleAgentSoulGet); err != nil {
		return err
	}
	if err := registerTool(r, agentSoulUpsertDef(), cfg.handleAgentSoulUpsert); err != nil {
		return err
	}
	if err := registerTool(r, agentSoulPublishDef(), cfg.handleAgentSoulPublish); err != nil {
		return err
	}
	if err := registerTool(r, agentSoulArchiveDef(), cfg.handleAgentSoulArchive); err != nil {
		return err
	}
	if err := registerTool(r, agentInstructionsGetDef(), cfg.handleAgentInstructionsGet); err != nil {
		return err
	}
	if err := registerTool(r, agentInstructionsUpsertDef(), cfg.handleAgentInstructionsUpsert); err != nil {
		return err
	}
	if err := registerTool(r, agentInstructionsArchiveDef(), cfg.handleAgentInstructionsArchive); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisSkillGetDef(), cfg.handleAgentGenesisSkillGet); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisBeginDef(), cfg.handleAgentGenesisBegin); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisListDef(), cfg.handleAgentGenesisList); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisReadDef(), cfg.handleAgentGenesisRead); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisAdvanceDef(), cfg.handleAgentGenesisAdvance); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisRecoverDef(), cfg.handleAgentGenesisRecover); err != nil {
		return err
	}
	if err := registerTool(r, agentGenesisFinalizePreflightDef(), cfg.handleAgentGenesisFinalizePreflight); err != nil {
		return err
	}
	return registerTool(r, agentGenesisFinalizeDef(), cfg.handleAgentGenesisFinalize)
}

func defaultConfig() config {
	return config{
		soulBindingFactory: func() (soulBindingClient, error) {
			return lesserapi.Default()
		},
		genesisFactory: func() (hostapi.GenesisClient, error) {
			return hostapi.Default()
		},
		hostIdentityFactory: func() (hostIdentityClient, error) {
			return hostapi.Default()
		},
		agentRegistryFactory: defaultAgentRegistry,
		agentLiveFactory:     defaultAgentLiveClient,
		agentContentFactory: func() (AgentContentStore, error) {
			return agentcontent.Default()
		},
		integrationBearerFn: func(ctx context.Context) (string, error) {
			return auth.SecretValueFromEnvOrARN(ctx, EnvSoulBindingIntegrationBearer, EnvSoulBindingIntegrationBearerARN)
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

func defaultAgentLiveClient() (AgentLiveClient, error) {
	agentLiveFactoryMu.RLock()
	factory := agentLiveFactoryForTests
	agentLiveFactoryMu.RUnlock()
	if factory != nil {
		return factory()
	}
	return lesserapi.Default()
}

func agentBindSoulDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentBindSoul,
		Title:       "Bind agent soul",
		Description: "Orchestrate Lesser's hosted soul/body binding ceremony for a Host-finalized local agent actor under the authenticated account-holder principal. Requires write scope, resolves the target actor from Host-derived Ptah registry/identity state, and delegates all binding state writes to Lesser. For a newly minted Host-genesis agent, call agent_genesis_finalize first so Body can write the Host-derived Ptah registry row, then use agent_get or agent_list to verify visibility.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"soul_agent_id":{"type":"string","description":"Full Lesser Soul agent identifier whose Host-derived local_id selects the Lesser agent actor to bind."},
				"idempotency_key":{"type":"string","description":"Caller-supplied replay key forwarded as Lesser's Idempotency-Key header."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal; it is not the Lesser binding target."},
				"body_actor_id":{"type":"string","description":"Optional Body/Ptah target actor correlation id. Accepts body://ptah/{local_id} or {local_id} only when it matches Host-derived registry/identity state. Defaults to body://ptah/{local_id}."},
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

func agentGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGet,
		Title:       "Get registered agent",
		Description: "Read a Body/Ptah account-scoped agent registry entry for the authenticated account-holder principal, including Host-genesis provenance and account-scoped content version/summary metadata when available. Requires read scope.",
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
				"data":{"type":"object","description":"Account-scoped registry summary, Host-derived provenance, and source-backed content-version/content-summary metadata when available."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentListDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentList,
		Title:       "List registered agents",
		Description: "List the authenticated account-holder's Body/Ptah registry entries, including Host-finalized minted agents, merged with Lesser's public live-agent directory. Requires read scope; live entries contain public metadata only and wallet-less Host-genesis visibility comes from Body's Host-derived registry row unless Lesser adds a separate minted-agent listing surface.",
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
				"data":{"type":"object","description":"Merged Body/Ptah registry and public Lesser live-agent entries with stable pagination metadata."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

func agentSoulGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulGet,
		Title:       "Get agent soul document",
		Description: "Read the current account-scoped Panonomous soul-document v2 record for the authenticated account-holder principal, including its server-owned draft/published/archived lifecycle. Requires read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"agent_id":{"type":"string","minLength":1,"maxLength":128,"not":{"pattern":"[|=/]"},"description":"Route-local account-scoped registry agent_id whose agent_soul content is read. It is not local_id/agent_username or soul_agent_id."},
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
		Description: "Create a new account-scoped Panonomous soul-document v2 draft through the canonical internal/agentcontent Store. Every successful upsert creates a new soul_version; a published snapshot is never edited in place. body-only v1-compatible authoring remains valid. Requires write scope.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"schema_version":{"type":"string","const":"lessersoul.panonomous.soul-document.v2"},
				"agent_id":{"type":"string","minLength":1,"maxLength":128,"not":{"pattern":"[|=/]"},"description":"Route-local account-scoped registry agent_id. It is not local_id/agent_username or soul_agent_id."},
				"body":{"type":"string","minLength":1,"maxLength":49152,"description":"Canonical Markdown-friendly body, bounded to 49152 UTF-8 bytes by the store."},
				"content":{"type":"string","minLength":1,"maxLength":49152,"description":"Deprecated compatibility alias for body. Supply exactly one of body or content."},
				"summary":{"type":"string","minLength":1,"maxLength":2048,"description":"Optional trimmed summary, bounded to 2048 UTF-8 bytes by the store."},
				"structure":{"type":"object","additionalProperties":false,"required":["five_bodies"],"properties":{
					"five_bodies":{"$ref":"#/$defs/fiveBodies"}
				}},
				"provenance":{"type":"object","additionalProperties":false,"required":["declaration_schema_version","declaration_candidate_hash","registration_id","conversation_id","model","source"],"properties":{
					"declaration_schema_version":{"type":"string","minLength":1},
					"declaration_candidate_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
					"registration_id":{"type":"string","minLength":1},
					"conversation_id":{"type":"string","minLength":1},
					"model":{"type":"string","minLength":1},
					"source":{"type":"string","enum":["host_genesis_finalize","ptah_seed","owner"]}
				}},
				"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
			},
			"required":["agent_id"],
			"oneOf":[
				{"required":["body"],"not":{"required":["content"]}},
				{"required":["content"],"not":{"required":["body"]}}
			],
			"additionalProperties":false,
			"$defs":{
				"declarationSection":{"type":"object","additionalProperties":false,"required":["summary"],"properties":{
					"summary":{"type":"string"},
					"notes":{"type":"array","items":{"type":"string"}}
				}},
				"refusal":{"type":"object","additionalProperties":false,"required":["bypass","invariant","closestSafePath"],"properties":{
					"bypass":{"type":"string"},
					"invariant":{"type":"string"},
					"closestSafePath":{"type":"string"}
				}},
				"soulSection":{"type":"object","additionalProperties":false,"required":["summary","refusals"],"properties":{
					"summary":{"type":"string"},
					"notes":{"type":"array","items":{"type":"string"}},
					"refusals":{"type":"array","minItems":1,"items":{"$ref":"#/$defs/refusal"}}
				}},
				"fiveBodies":{"type":"object","additionalProperties":false,"required":["identity","philosophy","discipline","boundaries","soul"],"properties":{
					"identity":{"$ref":"#/$defs/declarationSection"},
					"philosophy":{"$ref":"#/$defs/declarationSection"},
					"discipline":{"$ref":"#/$defs/declarationSection"},
					"boundaries":{"$ref":"#/$defs/declarationSection"},
					"soul":{"$ref":"#/$defs/soulSection"}
				}}
			}
		}`),
		OutputSchema: agentSoulOutputSchema(),
	}
}

func agentSoulPublishDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulPublish,
		Title:       "Publish agent soul",
		Description: "Explicitly transition the current account-scoped Panonomous soul-document v2 draft to a published immutable snapshot. Replaying publication of the same snapshot is idempotent. Requires write scope.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"agent_id":{"type":"string","minLength":1,"maxLength":128,"not":{"pattern":"[|=/]"},"description":"Route-local account-scoped registry agent_id whose current draft is published. It is not local_id/agent_username or soul_agent_id."},
					"actor_username":{"type":"string","description":"Optional explicit account-holder actor username. When supplied it must match the authenticated principal."}
				},
			"required":["agent_id"],
			"additionalProperties":false
		}`),
		OutputSchema: agentSoulOutputSchema(),
	}
}

func agentSoulArchiveDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentSoulArchive,
		Title:       "Archive published agent soul",
		Description: "Idempotently retire the current published account-scoped Panonomous soul-document v2 snapshot from rendering. Drafts must be published explicitly before archival. Requires write scope.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"agent_id":{"type":"string","minLength":1,"maxLength":128,"not":{"pattern":"[|=/]"},"description":"Route-local account-scoped registry agent_id whose published agent_soul record is archived. It is not local_id/agent_username or soul_agent_id."},
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
			"data":{"type":"object","description":"Structured account-scoped Panonomous soul-document v2 record and lifecycle idempotency metadata when applicable."},
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

type agentGetInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentListInput struct {
	Limit  *int   `json:"limit"`
	Cursor string `json:"cursor"`
}

const (
	agentListCursorPrefix       = "ptah-agent-list-v1:"
	maxAgentRegistryListPages   = 1000
	agentListLiveSourceCode     = "lesser_live"
	agentListRegistrySourceCode = "ptah_registry"
	agentListMergedSourceCode   = "merged"
)

type agentListCursor struct {
	After                string
	LegacyRegistryCursor string
}

type encodedAgentListCursor struct {
	Version int    `json:"version"`
	After   string `json:"after"`
}

type mergedAgentListEntry struct {
	Key      string
	Registry *agentregistry.Agent
	Live     *lesserapi.AgentDirectoryEntry
}

func decodeAgentListCursor(raw string) (agentListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return agentListCursor{}, nil
	}
	if !strings.HasPrefix(raw, agentListCursorPrefix) {
		// Cursors issued by the previous registry-only implementation remain
		// readable as a one-time compatibility path. The next response always
		// emits the merged-view cursor format.
		return agentListCursor{LegacyRegistryCursor: raw}, nil
	}

	encoded := strings.TrimPrefix(raw, agentListCursorPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return agentListCursor{}, fmt.Errorf("decode merged agent cursor: %w", err)
	}
	var cursor encodedAgentListCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return agentListCursor{}, fmt.Errorf("decode merged agent cursor: %w", err)
	}
	if cursor.Version != 1 || strings.TrimSpace(cursor.After) == "" {
		return agentListCursor{}, fmt.Errorf("merged agent cursor is invalid")
	}
	return agentListCursor{After: strings.TrimSpace(cursor.After)}, nil
}

func encodeAgentListCursor(after string) string {
	payload, _ := json.Marshal(encodedAgentListCursor{Version: 1, After: after})
	return agentListCursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

type agentSoulGetInput struct {
	AgentID       string `json:"agent_id"`
	ActorUsername string `json:"actor_username"`
}

type agentSoulUpsertInput struct {
	SchemaVersion string                      `json:"schema_version"`
	AgentID       string                      `json:"agent_id"`
	Body          *string                     `json:"body"`
	Content       *string                     `json:"content"`
	Summary       *string                     `json:"summary"`
	Structure     *agentcontent.SoulStructure `json:"structure"`
	Provenance    *agentcontent.Provenance    `json:"provenance"`
	ActorUsername string                      `json:"actor_username"`
}

type agentSoulPublishInput = agentSoulGetInput

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
	accountActor, errResult, err := authenticatedAccountHolderActor(principal, toolAgentBindSoul)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_bind_soul requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentBindSoulInput(args, accountActor)
	if errResult != nil || err != nil {
		return errResult, err
	}

	bindingActor, errResult, err := cfg.resolveSoulBindingActor(ctx, accountActor, &in)
	if errResult != nil || err != nil {
		return errResult, err
	}

	integrationBearer, err := cfg.integrationBearer(ctx)
	if err != nil {
		return toolErrorResult("not_configured", "soul-binding integration bearer could not be resolved", http.StatusInternalServerError, map[string]any{
			"source":      "lesser_body_ptah",
			"requiredEnv": []string{EnvSoulBindingIntegrationBearer, EnvSoulBindingIntegrationBearerARN},
		})
	}
	if integrationBearer == "" {
		return toolErrorResult("not_configured", EnvSoulBindingIntegrationBearer+" or "+EnvSoulBindingIntegrationBearerARN+" is required", http.StatusInternalServerError, map[string]any{
			"source":      "lesser_body_ptah",
			"requiredEnv": []string{EnvSoulBindingIntegrationBearer, EnvSoulBindingIntegrationBearerARN},
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
		"actor_username", bindingActor,
		"soul_agent_id", in.SoulAgentID,
		"idempotency_key_present", in.IdempotencyKey != "",
	)

	req := lesserapi.SoulBindingRequest{
		ActorUsername:      bindingActor,
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
	if resp == nil ||
		strings.ToLower(strings.TrimSpace(resp.BindingState)) != "bound" ||
		!strings.EqualFold(strings.TrimSpace(resp.Agent.AgentID), strings.TrimSpace(in.SoulAgentID)) {
		return toolErrorResult("actor_endpoint_authority_unavailable", "Lesser did not return an active authoritative actor binding", http.StatusConflict, map[string]any{
			"source": "lesser_soul_binding",
		})
	}
	if err := actorendpoint.Validate(bindingActor, resp.Binding.AgentUsername); err != nil {
		return toolErrorResult("actor_endpoint_divergence", "agent_bind_soul refused a bound actor response that disagrees with the registry local_id", http.StatusConflict, map[string]any{
			"source":          "lesser_soul_binding",
			"operator_action": "repair the registry projection or authoritative Lesser actor binding before retrying",
		})
	}
	return soulBindingSuccessResult(bindingActor, in.IdempotencyKey, resp)
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
	contentStore, unavailableReason := cfg.contentStoreForMetadata()
	contentMetadata := loadAgentContentMetadata(ctx, contentStore, unavailableReason, actorUsername, agent.AgentID)
	return agentGetSuccessResult(actorUsername, agent, contentMetadata)
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
	cursor, err := decodeAgentListCursor(in.Cursor)
	if err != nil {
		return toolErrorResult("invalid_request", "cursor is invalid", http.StatusBadRequest, map[string]any{
			"source": "agent_list",
		})
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

	registryAgents, err := listAllAgentRegistryEntries(ctx, registry, actorUsername, cursor.LegacyRegistryCursor)
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

	liveClient, err := cfg.liveAgents()
	if err != nil {
		return agentLiveSourceError(err)
	}
	if liveClient == nil {
		return agentLiveSourceError(fmt.Errorf("Lesser live-agent client is nil"))
	}
	liveAgents, err := liveClient.ListAgents(ctx)
	if err != nil {
		return agentLiveSourceError(err)
	}

	entries := mergeAgentListEntries(actorUsername, registryAgents, liveAgents)
	start := 0
	if cursor.After != "" {
		start = sort.Search(len(entries), func(index int) bool {
			return entries[index].Key > cursor.After
		})
	}
	if start > len(entries) {
		start = len(entries)
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}
	pageEntries := entries[start:end]
	hasMore := end < len(entries)
	nextCursor := ""
	if hasMore && len(pageEntries) > 0 {
		nextCursor = encodeAgentListCursor(pageEntries[len(pageEntries)-1].Key)
	}
	return cfg.agentListSuccessResult(ctx, actorUsername, limit, pageEntries, nextCursor, hasMore)
}

func listAllAgentRegistryEntries(ctx context.Context, registry AgentRegistry, account string, startCursor string) ([]*agentregistry.Agent, error) {
	cursor := strings.TrimSpace(startCursor)
	seenCursors := map[string]struct{}{}
	if cursor != "" {
		seenCursors[cursor] = struct{}{}
	}
	agents := make([]*agentregistry.Agent, 0)

	for pageNumber := 0; pageNumber < maxAgentRegistryListPages; pageNumber++ {
		page, err := registry.List(ctx, agentregistry.ListInput{
			Account: account,
			Limit:   agentregistry.MaxListLimit,
			Cursor:  cursor,
		})
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, fmt.Errorf("agent registry returned a nil page")
		}
		agents = append(agents, page.Agents...)
		if !page.HasMore {
			return agents, nil
		}

		nextCursor := strings.TrimSpace(page.NextCursor)
		if nextCursor == "" {
			return nil, fmt.Errorf("agent registry pagination did not provide a next cursor")
		}
		if _, ok := seenCursors[nextCursor]; ok {
			return nil, fmt.Errorf("agent registry pagination cursor repeated")
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return nil, fmt.Errorf("agent registry pagination exceeded safety bound")
}

func agentLiveSourceError(err error) (*mcpruntime.ToolResult, error) {
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		slog.Warn("ptah agent_list Lesser live-agent source failed", "upstream_status", apiErr.Status)
	} else {
		slog.Warn("ptah agent_list Lesser live-agent source unavailable", "error", "source request failed")
	}
	return toolErrorResult("agent_live_source_error", "Body failed to read Lesser's public live-agent directory", http.StatusBadGateway, map[string]any{
		"source": agentListLiveSourceCode,
	})
}

func mergeAgentListEntries(account string, registryAgents []*agentregistry.Agent, liveAgents []lesserapi.AgentDirectoryEntry) []mergedAgentListEntry {
	orderedLive := make([]lesserapi.AgentDirectoryEntry, 0, len(liveAgents))
	for _, agent := range liveAgents {
		if normalizeAgentIdentity(agent.Username) == "" {
			continue
		}
		orderedLive = append(orderedLive, agent)
	}
	sort.SliceStable(orderedLive, func(left, right int) bool {
		leftKey := normalizeAgentIdentity(orderedLive[left].Username)
		rightKey := normalizeAgentIdentity(orderedLive[right].Username)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if orderedLive[left].Username != orderedLive[right].Username {
			return orderedLive[left].Username < orderedLive[right].Username
		}
		return orderedLive[left].DisplayName < orderedLive[right].DisplayName
	})

	liveByIdentity := make(map[string]*lesserapi.AgentDirectoryEntry, len(orderedLive))
	for index := range orderedLive {
		identity := normalizeAgentIdentity(orderedLive[index].Username)
		if _, exists := liveByIdentity[identity]; exists {
			continue
		}
		liveByIdentity[identity] = &orderedLive[index]
	}

	orderedRegistry := make([]*agentregistry.Agent, 0, len(registryAgents))
	for _, agent := range registryAgents {
		if agent == nil || normalizeActorUsername(agent.Account) != normalizeActorUsername(account) || normalizeAgentIdentity(agent.AgentID) == "" {
			continue
		}
		orderedRegistry = append(orderedRegistry, agent)
	}
	sort.SliceStable(orderedRegistry, func(left, right int) bool {
		leftKey := registryLiveIdentity(orderedRegistry[left])
		rightKey := registryLiveIdentity(orderedRegistry[right])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return strings.ToLower(strings.TrimSpace(orderedRegistry[left].AgentID)) < strings.ToLower(strings.TrimSpace(orderedRegistry[right].AgentID))
	})

	entries := make([]mergedAgentListEntry, 0, len(orderedRegistry)+len(liveByIdentity))
	seenKeys := make(map[string]struct{}, len(orderedRegistry)+len(liveByIdentity))
	matchedLive := make(map[string]struct{}, len(liveByIdentity))
	for _, agent := range orderedRegistry {
		identity := registryLiveIdentity(agent)
		live := liveByIdentity[identity]
		key := "registry:" + strings.ToLower(strings.TrimSpace(agent.AgentID))
		if live != nil {
			key = "agent:" + identity
			matchedLive[identity] = struct{}{}
		}
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		entries = append(entries, mergedAgentListEntry{Key: key, Registry: agent, Live: live})
	}
	for index := range orderedLive {
		identity := normalizeAgentIdentity(orderedLive[index].Username)
		if _, matched := matchedLive[identity]; matched {
			continue
		}
		key := "agent:" + identity
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		entries = append(entries, mergedAgentListEntry{Key: key, Live: &orderedLive[index]})
	}

	sort.SliceStable(entries, func(left, right int) bool {
		return entries[left].Key < entries[right].Key
	})
	return entries
}

func registryLiveIdentity(agent *agentregistry.Agent) string {
	if agent == nil {
		return ""
	}
	if localID := normalizeAgentIdentity(agent.LocalID); localID != "" {
		return localID
	}
	return normalizeAgentIdentity(agent.AgentID)
}

func normalizeAgentIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" && parsed.Path != "" {
		value = parsed.Path
	}
	value = strings.Trim(value, "/")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	return strings.TrimPrefix(value, "@")
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

	body := ""
	if in.Body != nil {
		body = *in.Body
	} else if in.Content != nil {
		body = *in.Content
	}
	document := &agentcontent.SoulDocument{
		SchemaVersion: in.SchemaVersion,
		AgentID:       in.AgentID,
		Body:          body,
		Summary:       in.Summary,
		Structure:     in.Structure,
		Provenance:    in.Provenance,
	}
	if err := agentcontent.ValidateSoulDocumentDraft(document, in.AgentID); err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	slog.InfoContext(ctx, "ptah tool invocation",
		"tool", toolAgentSoulUpsert,
		"actor_username", actorUsername,
		"content_bytes", len([]byte(body)),
	)

	record, err := store.Upsert(ctx, agentcontent.UpsertInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		Type:               agentcontent.ContentTypeAgentSoul,
		SoulDocument:       document,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	return agentSoulSuccessResult("Ptah agent_soul draft upserted", actorUsername, record, nil)
}

func (cfg config) handleAgentSoulPublish(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal, toolAgentSoulPublish)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_soul_publish requires write scope", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}
	subjectID, errResult := authenticatedSubjectID(principal, toolAgentSoulPublish)
	if errResult != nil {
		return errResult, nil
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
		"tool", toolAgentSoulPublish,
		"actor_username", actorUsername,
	)
	current, err := store.Get(ctx, actorUsername, in.AgentID, agentcontent.ContentTypeAgentSoul)
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	alreadyPublished := current != nil && current.LifecycleState == agentcontent.LifecycleStatePublished
	record, err := store.Publish(ctx, agentcontent.PublishInput{
		Account:            actorUsername,
		AgentID:            in.AgentID,
		UpdatedBySubjectID: subjectID,
	})
	if err != nil {
		return agentContentToolResultFromError(err, agentcontent.ContentTypeAgentSoul)
	}
	return agentSoulSuccessResult("Ptah agent_soul snapshot published", actorUsername, record, map[string]any{
		"already_published": alreadyPublished,
		"idempotent":        true,
	})
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
	return in, nil, nil
}

func (cfg config) resolveSoulBindingActor(ctx context.Context, accountActor string, in *agentBindSoulInput) (string, *mcpruntime.ToolResult, error) {
	accountActor = normalizeActorUsername(accountActor)
	if in == nil || strings.TrimSpace(in.SoulAgentID) == "" {
		return "", mustToolErrorResult("invalid_request", "soul_agent_id is required", http.StatusBadRequest, nil), nil
	}

	registry, err := cfg.registry()
	if err != nil || registry == nil {
		return "", mustToolErrorResult("not_configured", "Body/Ptah agent registry is required to verify Host-derived local actor mapping", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
			"tool":   toolAgentBindSoul,
		}), nil
	}

	agent, err := registry.Get(ctx, accountActor, in.SoulAgentID)
	if err != nil {
		if errors.Is(err, agentregistry.ErrAgentNotFound) {
			return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable for this account-scoped soul_agent_id; call agent_genesis_finalize first or retry after Host finalization is visible", "agent_registry"), nil
		}
		return "", mustToolErrorResult("agent_registry_error", "Body failed to read the account-scoped Ptah registry row before soul binding", http.StatusInternalServerError, map[string]any{
			"source": "agent_registry",
			"tool":   toolAgentBindSoul,
		}), nil
	}
	if agent == nil || normalizeActorUsername(agent.Account) != accountActor || strings.TrimSpace(agent.AgentID) != strings.TrimSpace(in.SoulAgentID) {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable for this account-scoped soul_agent_id", "agent_registry"), nil
	}
	if strings.TrimSpace(agent.Source) != agentregistry.SourceHostGenesisFinalize {
		return "", hostActorMappingUnavailableResult("agent_bind_soul requires a Host-finalized Ptah registry row for the supplied soul_agent_id", "agent_registry"), nil
	}

	localActor := normalizeSoulBindingLocalActor(agent.LocalID)
	if localActor == "" {
		var errResult *mcpruntime.ToolResult
		agent, localActor, errResult, err = cfg.refetchSoulBindingActorFromHost(ctx, accountActor, registry, agent)
		if errResult != nil || err != nil {
			return "", errResult, err
		}
		if agent == nil || localActor == "" {
			return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable for this soul_agent_id", "lesser_host_identity"), nil
		}
	}

	if in.BodyActorID != "" {
		claimedLocalActor, parseErr := normalizeBodyActorIDLocalActor(in.BodyActorID)
		if parseErr != nil {
			return "", mustToolErrorResult("invalid_request", parseErr.Error(), http.StatusBadRequest, map[string]any{
				"source": "lesser_body_ptah",
				"field":  "body_actor_id",
			}), nil
		}
		if claimedLocalActor != localActor {
			return "", mustToolErrorResult("forbidden", "body_actor_id does not match the Host-derived local actor for this account-scoped soul_agent_id", http.StatusForbidden, map[string]any{
				"source": "agent_registry",
				"tool":   toolAgentBindSoul,
			}), nil
		}
	}
	in.BodyActorID = canonicalBodyActorID(localActor)
	return localActor, nil, nil
}

func (cfg config) refetchSoulBindingActorFromHost(ctx context.Context, accountActor string, registry AgentRegistry, existing *agentregistry.Agent) (*agentregistry.Agent, string, *mcpruntime.ToolResult, error) {
	client, err := cfg.hostIdentity()
	if err != nil || client == nil {
		return nil, "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable because lesser-host identity lookup is not configured", "lesser_host_identity"), nil
	}
	identity, err := client.GetAgentIdentity(ctx, existing.AgentID)
	if err != nil {
		return nil, "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; Body could not refetch lesser-host public identity for this soul_agent_id", "lesser_host_identity"), nil
	}
	localActor, errResult := verifiedLocalActorFromHostIdentity(existing.AgentID, identity)
	if errResult != nil {
		return nil, "", errResult, nil
	}
	if strings.TrimSpace(existing.LocalID) != "" {
		if err := actorendpoint.Validate(existing.LocalID, identity.LocalID); err != nil {
			return nil, "", mustToolErrorResult("actor_endpoint_divergence", "agent_bind_soul refused to overwrite a registry local_id that disagrees with the Host identity projection", http.StatusConflict, map[string]any{
				"source":          "agent_registry_host_refetch",
				"tool":            toolAgentBindSoul,
				"operator_action": "verify the authoritative Lesser actor and repair the divergent source before retrying; Body will not rewrite either value silently",
			}), nil
		}
	}

	expectedLocalID := strings.TrimSpace(existing.LocalID)
	updated, _, updateErr := registry.UpsertFinalized(ctx, agentregistry.FinalizedInput{
		Account:                accountActor,
		AgentID:                existing.AgentID,
		HostRegistrationID:     existing.HostRegistrationID,
		HostConversationID:     existing.HostConversationID,
		Domain:                 identity.Domain,
		LocalID:                identity.LocalID,
		AuthorityModel:         identity.AuthorityModel,
		AnchorState:            identity.AnchorState,
		OperationalBinding:     identity.OperationalBinding,
		LifecycleStatus:        firstNonEmpty(identity.LifecycleStatus, identity.Status),
		PublishedVersion:       firstNonZeroInt64(identity.PublishedVersion, existing.PublishedVersion),
		SelfDescriptionVersion: firstNonZeroInt64(identity.SelfDescriptionVersion, existing.SelfDescriptionVersion),
		ExpectedLocalID:        &expectedLocalID,
	})
	if updateErr != nil {
		if errors.Is(updateErr, agentregistry.ErrFinalizedLocalIDChanged) {
			return nil, "", mustToolErrorResult("actor_endpoint_divergence", "agent_bind_soul refused because the registry local_id changed during Host identity refetch", http.StatusConflict, map[string]any{
				"source":          "agent_registry_host_refetch",
				"tool":            toolAgentBindSoul,
				"operator_action": "retry from the corrected registry state; Body did not overwrite the concurrent change",
			}), nil
		}
		slog.WarnContext(ctx, "ptah soul binding Host identity registry repair failed",
			"tool", toolAgentBindSoul,
			"source", "agent_registry",
			"error", "registry update failed",
		)
		return nil, "", mustToolErrorResult("agent_registry_error", "agent_bind_soul verified a Host actor mapping but could not persist the registry repair", http.StatusInternalServerError, map[string]any{
			"source":          "agent_registry_host_refetch",
			"tool":            toolAgentBindSoul,
			"operator_action": "restore the Body/Ptah registry write path and retry; Body did not submit the soul binding to Lesser",
		}), nil
	}
	if updated == nil {
		return existing, localActor, nil, nil
	}
	return updated, localActor, nil, nil
}

func verifiedLocalActorFromHostIdentity(agentID string, identity *hostapi.AgentIdentity) (string, *mcpruntime.ToolResult) {
	if identity == nil {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; lesser-host identity response was empty", "lesser_host_identity")
	}
	if !strings.EqualFold(strings.TrimSpace(identity.AgentID), strings.TrimSpace(agentID)) {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; lesser-host identity did not match the supplied soul_agent_id", "lesser_host_identity")
	}
	localActor := normalizeSoulBindingLocalActor(identity.LocalID)
	if localActor == "" {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; lesser-host identity did not include a valid local_id", "lesser_host_identity")
	}
	if strings.TrimSpace(identity.AuthorityModel) != lesserapi.SoulAuthorityModelInstanceTrust ||
		strings.TrimSpace(identity.AnchorState) != lesserapi.SoulAnchorStateHostedOffchain ||
		strings.TrimSpace(identity.OperationalBinding) != lesserapi.SoulOperationalBindingHostedBound {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; lesser-host identity is not an active hosted/offchain bound soul", "lesser_host_identity")
	}
	if status := strings.ToLower(strings.TrimSpace(firstNonEmpty(identity.LifecycleStatus, identity.Status))); status != "active" {
		return "", hostActorMappingUnavailableResult("Host-derived local actor mapping is unavailable; lesser-host identity is not active", "lesser_host_identity")
	}
	return localActor, nil
}

func hostActorMappingUnavailableResult(message string, source string) *mcpruntime.ToolResult {
	return mustToolErrorResult("host_actor_mapping_unavailable", message, http.StatusConflict, map[string]any{
		"source": source,
		"tool":   toolAgentBindSoul,
	})
}

func normalizeSoulBindingLocalActor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "/:@") || strings.ContainsAny(value, " \t\r\n") || url.PathEscape(value) != value {
		return ""
	}
	return normalizeActorUsername(value)
}

func normalizeBodyActorIDLocalActor(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("body_actor_id is empty")
	}
	local := raw
	if strings.HasPrefix(strings.ToLower(raw), "body://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed == nil || parsed.Scheme != "body" || parsed.Host != "ptah" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("body_actor_id must be body://ptah/{local_id} or a local_id")
		}
		local = strings.Trim(parsed.Path, "/")
		if strings.Contains(local, "/") {
			return "", fmt.Errorf("body_actor_id must identify exactly one local actor")
		}
	}
	local = normalizeSoulBindingLocalActor(local)
	if local == "" {
		return "", fmt.Errorf("body_actor_id must be body://ptah/{local_id} or a valid local_id")
	}
	return local, nil
}

func canonicalBodyActorID(localActor string) string {
	return "body://ptah/" + normalizeSoulBindingLocalActor(localActor)
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
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return agentSoulGetInput{}, mustToolErrorResult("invalid_request", "invalid args: expected one JSON object", http.StatusBadRequest, nil), nil
	}
	if field := explicitJSONNullField([]byte(raw)); field != "" {
		return agentSoulGetInput{}, mustToolErrorResult("invalid_request", field+" must not be null", http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if err := agentcontent.ValidateSoulAgentID(in.AgentID); err != nil {
		return agentSoulGetInput{}, mustToolErrorResult("invalid_request", err.Error(), http.StatusBadRequest, nil), nil
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
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", "invalid args: expected one JSON object", http.StatusBadRequest, nil), nil
	}
	if field := explicitJSONNullField([]byte(raw)); field != "" {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", field+" must not be null", http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.SchemaVersion = strings.TrimSpace(in.SchemaVersion)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if err := agentcontent.ValidateSoulAgentID(in.AgentID); err != nil {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", err.Error(), http.StatusBadRequest, nil), nil
	}
	if (in.Body == nil) == (in.Content == nil) {
		return agentSoulUpsertInput{}, mustToolErrorResult("invalid_request", "exactly one of body or content is required", http.StatusBadRequest, nil), nil
	}
	if in.Summary != nil {
		summary := strings.TrimSpace(*in.Summary)
		in.Summary = &summary
	}
	if in.ActorUsername != "" && in.ActorUsername != actorUsername {
		return agentSoulUpsertInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return in, nil, nil
}

func explicitJSONNullField(raw []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ""
	}
	names := make([]string, 0, len(fields))
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
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
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return agentSoulArchiveInput{}, mustToolErrorResult("invalid_request", "invalid args: expected one JSON object", http.StatusBadRequest, nil), nil
	}
	if field := explicitJSONNullField([]byte(raw)); field != "" {
		return agentSoulArchiveInput{}, mustToolErrorResult("invalid_request", field+" must not be null", http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)
	if err := agentcontent.ValidateSoulAgentID(in.AgentID); err != nil {
		return agentSoulArchiveInput{}, mustToolErrorResult("invalid_request", err.Error(), http.StatusBadRequest, nil), nil
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

func (cfg config) genesis() (hostapi.GenesisClient, error) {
	if cfg.genesisClient != nil {
		return cfg.genesisClient, nil
	}
	if cfg.genesisFactory == nil {
		return nil, fmt.Errorf("Host genesis client is not configured")
	}
	return cfg.genesisFactory()
}

func (cfg config) hostIdentity() (hostIdentityClient, error) {
	if cfg.hostIdentityClient != nil {
		return cfg.hostIdentityClient, nil
	}
	if cfg.hostIdentityFactory == nil {
		return nil, fmt.Errorf("Host identity client is not configured")
	}
	return cfg.hostIdentityFactory()
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

func (cfg config) liveAgents() (AgentLiveClient, error) {
	if cfg.agentLiveClient != nil {
		return cfg.agentLiveClient, nil
	}
	if cfg.agentLiveFactory == nil {
		return nil, fmt.Errorf("Lesser live-agent client is not configured")
	}
	return cfg.agentLiveFactory()
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

func agentGetSuccessResult(actorUsername string, agent *agentregistry.Agent, contentMetadata agentContentMetadata) (*mcpruntime.ToolResult, error) {
	registrySummary := registryAgentSummary(agent)
	data := map[string]any{
		"actor_username":  actorUsername,
		"registry":        registrySummary,
		"content_version": contentMetadata.Version,
		"content_summary": contentMetadata.Summary,
	}

	text := map[string]any{
		"summary":         "Ptah registry entry read",
		"registry":        registrySummary,
		"content_version": contentMetadata.Version,
		"content_summary": contentMetadata.Summary,
		"data":            map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func (cfg config) agentListSuccessResult(ctx context.Context, actorUsername string, limit int, entries []mergedAgentListEntry, nextCursor string, hasMore bool) (*mcpruntime.ToolResult, error) {
	items := make([]map[string]any, 0, len(entries))
	contentStore, unavailableReason := cfg.contentStoreForMetadata()
	for _, entry := range entries {
		source := agentListLiveSourceCode
		contentMetadata := unavailableContentMetadataForSource(agentListLiveSourceCode)
		if entry.Registry != nil && entry.Live != nil {
			source = agentListMergedSourceCode
			contentMetadata = loadAgentContentMetadata(ctx, contentStore, unavailableReason, actorUsername, entry.Registry.AgentID)
		} else if entry.Registry != nil {
			source = agentListRegistrySourceCode
			contentMetadata = loadAgentContentMetadata(ctx, contentStore, unavailableReason, actorUsername, entry.Registry.AgentID)
		}
		item := map[string]any{
			"content_version": contentMetadata.Version,
			"content_summary": contentMetadata.Summary,
			"source":          source,
		}
		if entry.Registry != nil {
			item["registry"] = registryAgentSummary(entry.Registry)
		}
		if entry.Live != nil {
			liveSummary, err := mapFromJSON(entry.Live)
			if err != nil {
				return nil, err
			}
			item["live_agent"] = liveSummary
		}
		items = append(items, item)
	}
	count := len(items)

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
		"summary":    "Ptah registry and Lesser live-agent entries listed",
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
			"account":           recordSummary["account"],
			"agent_id":          recordSummary["agent_id"],
			"version":           recordSummary["version"],
			"soul_version":      recordSummary["soul_version"],
			"lifecycle_state":   recordSummary["lifecycle_state"],
			"content_bytes":     recordSummary["content_bytes"],
			"content_location":  "structuredContent.data.agent_soul.content",
			"document_location": "structuredContent.data.agent_soul.document",
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
	var validationErr *agentcontent.ValidationError
	var transitionErr *agentcontent.TransitionError
	var rewriteErr *agentcontent.SoulRewriteRequiredError
	switch {
	case errors.Is(err, agentcontent.ErrContentNotFound):
		return toolErrorResult("not_found", contentName+" record not found in this account-scoped Ptah content store", http.StatusNotFound, details)
	case errors.As(err, &rewriteErr):
		details["action"] = string(rewriteErr.Action)
		details["rewrite_tool"] = toolAgentSoulUpsert
		details["publish_tool"] = toolAgentSoulPublish
		return toolErrorResult(
			"agent_soul_rewrite_required",
			"pre-v2 opaque agent_soul must be rewritten via agent_soul_upsert, then published via agent_soul_publish",
			http.StatusConflict,
			details,
		)
	case errors.As(err, &sizeErr):
		details["limit_bytes"] = sizeErr.Limit
		details["actual_bytes"] = sizeErr.Actual
		return toolErrorResult("invalid_request", contentName+" content exceeds the per-record size limit", http.StatusBadRequest, details)
	case errors.As(err, &validationErr):
		details["field"] = validationErr.Field
		return toolErrorResult("invalid_request", contentName+" does not satisfy the Panonomous soul-document v2 contract", http.StatusBadRequest, details)
	case errors.As(err, &transitionErr):
		details["action"] = string(transitionErr.Action)
		details["from"] = string(transitionErr.From)
		details["to"] = string(transitionErr.To)
		message := contentName + " lifecycle transition is not allowed"
		switch transitionErr.Action {
		case agentcontent.ContentActionArchive:
			message += "; publish the current draft explicitly before archival"
		case agentcontent.ContentActionPublish:
			message += "; only a current draft can be published, so create a new draft with agent_soul_upsert"
		}
		return toolErrorResult("invalid_lifecycle_transition", message, http.StatusConflict, details)
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
	out := map[string]any{
		"account":    agent.Account,
		"agent_id":   agent.AgentID,
		"created_at": formatTime(agent.CreatedAt),
		"updated_at": formatTime(agent.UpdatedAt),
	}
	if provenance := registryProvenanceSummary(agent); len(provenance) > 0 {
		out["provenance"] = provenance
	}
	if hostIdentity := registryHostIdentitySummary(agent); len(hostIdentity) > 0 {
		out["host_identity"] = hostIdentity
	}
	return out
}

func registryProvenanceSummary(agent *agentregistry.Agent) map[string]any {
	if agent == nil {
		return nil
	}
	source := strings.TrimSpace(agent.Source)
	if source == "" {
		return nil
	}
	out := map[string]any{"source": source}
	if authority := strings.TrimSpace(agent.SourceAuthority); authority != "" {
		out["authority"] = authority
	}
	if operation := strings.TrimSpace(agent.SourceOperation); operation != "" {
		out["operation"] = operation
	}
	if registrationID := strings.TrimSpace(agent.HostRegistrationID); registrationID != "" {
		out["registration_id"] = registrationID
	}
	if conversationID := strings.TrimSpace(agent.HostConversationID); conversationID != "" {
		out["conversation_id"] = conversationID
	}
	if source == agentregistry.SourceHostGenesisFinalize || source == agentregistry.SourceHostRecovery {
		out["system_derived"] = true
		out["caller_claimed"] = false
		out["state_authority"] = "Host HostedGenesisSession"
	}
	if source == agentregistry.SourceHostRecovery {
		out["classification"] = agent.RecoveryClassification
		out["migration_read_sha256"] = agent.MigrationReadSHA256
		out["historical_publication_sha"] = false
		if !agent.RecoveryProducedAt.IsZero() {
			out["produced_at"] = formatTime(agent.RecoveryProducedAt)
		}
		out["version_count"] = agent.RecoveryVersionCount
	}
	return out
}

func registryHostIdentitySummary(agent *agentregistry.Agent) map[string]any {
	if agent == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range map[string]any{
		"domain":                   agent.Domain,
		"local_id":                 agent.LocalID,
		"authority_model":          agent.AuthorityModel,
		"anchor_state":             agent.AnchorState,
		"operational_binding":      agent.OperationalBinding,
		"lifecycle_status":         agent.LifecycleStatus,
		"published_version":        agent.PublishedVersion,
		"self_description_version": agent.SelfDescriptionVersion,
	} {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				out[key] = strings.TrimSpace(typed)
			}
		case int64:
			if typed > 0 {
				out[key] = typed
			}
		}
	}
	return out
}

type agentContentMetadata struct {
	Version map[string]any
	Summary map[string]any
}

func (cfg config) contentStoreForMetadata() (AgentContentStore, string) {
	store, err := cfg.content()
	if err != nil || store == nil {
		return nil, "Body/Ptah content metadata is unavailable because the account-scoped agent content store is not configured."
	}
	return store, ""
}

func loadAgentContentMetadata(ctx context.Context, store AgentContentStore, unavailableReason string, account string, agentID string) agentContentMetadata {
	if store == nil {
		return unavailableContentMetadataForSourceReason("agentcontent", unavailableReason)
	}

	records := make([]*agentcontent.Record, 0, 2)
	for _, contentType := range []agentcontent.ContentType{agentcontent.ContentTypeAgentSoul, agentcontent.ContentTypeAgentInstructions} {
		record, err := store.Get(ctx, account, agentID, contentType)
		switch {
		case err == nil && record != nil:
			records = append(records, record)
		case err == nil || errors.Is(err, agentcontent.ErrContentNotFound):
			continue
		default:
			slog.WarnContext(ctx, "ptah agent content metadata read failed",
				"source", "agentcontent",
				"content_type", string(contentType),
				"error", "metadata read failed",
			)
			return unavailableContentMetadataForSourceReason("agentcontent", "Body/Ptah content metadata is unavailable because the content record could not be read safely.")
		}
	}
	if len(records) == 0 {
		return unavailableContentMetadataForSourceReason("agentcontent", "No account-scoped Body/Ptah agent_soul or agent_instructions content records are available for this agent.")
	}
	return availableContentMetadata(records)
}

func availableContentMetadata(records []*agentcontent.Record) agentContentMetadata {
	status := "available"
	if len(records) == 1 {
		status = "partial"
	}
	version := map[string]any{
		"status": status,
		"source": "agentcontent",
	}
	summary := map[string]any{
		"status": status,
		"source": "agentcontent",
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		key := string(record.Type)
		if key == "" {
			continue
		}
		version[key] = map[string]any{
			"version":         record.Version,
			"lifecycle_state": string(record.LifecycleState),
			"updated_at":      formatTime(record.UpdatedAt),
		}
		summary[key] = map[string]any{
			"content_bytes":   len([]byte(record.Content)),
			"lifecycle_state": string(record.LifecycleState),
			"updated_at":      formatTime(record.UpdatedAt),
		}
	}
	return agentContentMetadata{Version: version, Summary: summary}
}

func unavailableContentMetadataForSource(source string) agentContentMetadata {
	return unavailableContentMetadataForSourceReason(source, "")
}

func unavailableContentMetadataForSourceReason(source string, reason string) agentContentMetadata {
	return agentContentMetadata{
		Version: unavailableContentVersionForSourceReason(source, reason),
		Summary: unavailableContentSummaryForSourceReason(source, reason),
	}
}

func unavailableContentVersionForSourceReason(source string, reason string) map[string]any {
	if strings.TrimSpace(reason) == "" {
		reason = "Body/Ptah content version metadata is not available from this source."
	}
	if source == agentListLiveSourceCode {
		reason = "Lesser's public live-agent directory does not expose Body/Ptah content version metadata; a separate Lesser minted-agent listing contract is required for wallet-less Host-genesis agents if Lesser is to be the listing source."
	}
	return map[string]any{
		"status": "not_available",
		"source": source,
		"reason": reason,
	}
}

func unavailableContentSummaryForSourceReason(source string, reason string) map[string]any {
	if strings.TrimSpace(reason) == "" {
		reason = "Body/Ptah content summary metadata is not available from this source."
	}
	if source == agentListLiveSourceCode {
		reason = "Lesser's public live-agent directory does not expose Body/Ptah content summary metadata; a separate Lesser minted-agent listing contract is required for wallet-less Host-genesis agents if Lesser is to be the listing source."
	}
	return map[string]any{
		"status": "not_available",
		"source": source,
		"reason": reason,
	}
}

func agentContentRecordSummary(record *agentcontent.Record) map[string]any {
	if record == nil {
		return map[string]any{}
	}
	summary := map[string]any{
		"account":               record.Account,
		"agent_id":              record.AgentID,
		"type":                  string(record.Type),
		"content":               record.Content,
		"content_bytes":         len([]byte(record.Content)),
		"version":               record.Version,
		"soul_version":          record.SoulVersion,
		"lifecycle_state":       string(record.LifecycleState),
		"created_at":            formatTime(record.CreatedAt),
		"updated_at":            formatTime(record.UpdatedAt),
		"updated_by_subject_id": record.UpdatedBySubjectID,
	}
	if record.Document != nil {
		summary["document"] = record.Document
	}
	return summary
}

func agentSoulSchemaMarker() map[string]any {
	return map[string]any{
		"content_type":   string(agentcontent.ContentTypeAgentSoul),
		"status":         "stable",
		"schema_version": agentcontent.SoulDocumentSchemaVersion,
		"schema_url":     agentcontent.SoulDocumentSchemaURL,
	}
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
