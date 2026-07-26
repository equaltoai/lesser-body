package baserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	"github.com/equaltoai/lesser-body/internal/installpack"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	// ToolAgentLocalInstallPlan is the Ba instance-plane local install planning tool.
	ToolAgentLocalInstallPlan = "agent_local_install_plan"

	// InstallerGrantBoundRoute is the fixed instance-plane MCP route bound into
	// Ba local-install download grants. It intentionally does not come from caller
	// input or host headers.
	InstallerGrantBoundRoute = "/instance/ba/mcp"

	// EnvInstanceMCPEndpoint is injected by CDK as the canonical instance-plane
	// endpoint template, for example https://api.dev.example.com/instance/{surface}/mcp.
	EnvInstanceMCPEndpoint = "INSTANCE_MCP_ENDPOINT"

	DefaultNamespace = "equaltoai"

	planSchema       = "lesserbody.agent_local_install_plan.v1"
	packIDPrefix     = "agent-local-install/v1/"
	defaultRateLimit = 5
)

var defaultRateWindow = time.Minute

var (
	ErrAgentSoulPublicationRequired = errors.New("agent soul publication is required")
	ErrAgentContentMissing          = errors.New("required agent content is missing")
)

// AgentContentMissingError names the exact account-scoped record and Ptah
// authoring tool needed before Ba can render an install pack.
type AgentContentMissingError struct {
	AgentID     string
	ContentType agentcontent.ContentType
	FixTool     string
	NextTool    string
}

func (e *AgentContentMissingError) Error() string {
	if e == nil {
		return ErrAgentContentMissing.Error()
	}
	message := fmt.Sprintf(
		"%s: %s record for agent_id %s is missing; call %s",
		ErrAgentContentMissing,
		e.ContentType,
		e.AgentID,
		e.FixTool,
	)
	if strings.TrimSpace(e.NextTool) != "" {
		message += ", then call " + e.NextTool
	}
	return message
}

func (e *AgentContentMissingError) Unwrap() error { return ErrAgentContentMissing }

// AgentSoulPublicationRequiredError is the typed Ba materialization gate. It
// names the exact Ptah publish step for an existing draft or archived record.
type AgentSoulPublicationRequiredError struct {
	AgentID        string
	LifecycleState string
	PublishTool    string
}

func (e *AgentSoulPublicationRequiredError) Error() string {
	if e == nil {
		return ErrAgentSoulPublicationRequired.Error()
	}
	state := strings.TrimSpace(e.LifecycleState)
	if state == "" {
		state = "missing"
	}
	tool := strings.TrimSpace(e.PublishTool)
	if tool == "" {
		tool = "agent_soul_publish"
	}
	return fmt.Sprintf("%s: agent_id %s has lifecycle_state=%s; call %s after creating a draft", ErrAgentSoulPublicationRequired, e.AgentID, state, tool)
}

func (e *AgentSoulPublicationRequiredError) Unwrap() error {
	return ErrAgentSoulPublicationRequired
}

// AgentContentStore is the body-owned Ba content read dependency. Production
// uses internal/agentcontent.Store over INSTANCE_CONTENT_TABLE.
type AgentContentStore interface {
	Get(ctx context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error)
}

// DownloadGrantIssuer is the one-time download grant minting dependency used by
// agent_local_install_plan. Production uses internal/downloadgrant.Store.
type DownloadGrantIssuer interface {
	Issue(ctx context.Context, in downloadgrant.IssueInput) (*downloadgrant.IssuedGrant, error)
}

// Renderer is the deterministic install-pack renderer dependency.
type Renderer interface {
	Render(ctx context.Context, req installpack.Request) (*installpack.Pack, error)
}

type config struct {
	contentStore        AgentContentStore
	contentStoreFactory func() (AgentContentStore, error)
	grantIssuer         DownloadGrantIssuer
	grantIssuerFactory  func() (DownloadGrantIssuer, error)
	renderer            Renderer
	instanceEndpoint    string
	namespace           string
	rateLimiter         GrantMintLimiter
	now                 func() time.Time
}

// Option configures Ba tool registration.
type Option func(*config)

// WithAgentContentStore injects the content store used by Ba plan rendering.
func WithAgentContentStore(store AgentContentStore) Option {
	return func(cfg *config) {
		cfg.contentStore = store
	}
}

// WithDownloadGrantIssuer injects the grant issuer used by Ba plan rendering.
func WithDownloadGrantIssuer(issuer DownloadGrantIssuer) Option {
	return func(cfg *config) {
		cfg.grantIssuer = issuer
	}
}

// WithRenderer injects the deterministic install-pack renderer.
func WithRenderer(renderer Renderer) Option {
	return func(cfg *config) {
		cfg.renderer = renderer
	}
}

// WithInstanceEndpoint injects the canonical instance-plane endpoint template.
func WithInstanceEndpoint(endpoint string) Option {
	return func(cfg *config) {
		cfg.instanceEndpoint = strings.TrimSpace(endpoint)
	}
}

// WithNamespace injects the namespace recorded in Ba install-pack metadata and
// download-grant bindings. Empty values fall back to DefaultNamespace.
func WithNamespace(namespace string) Option {
	return func(cfg *config) {
		cfg.namespace = strings.TrimSpace(namespace)
	}
}

// WithRateLimiter injects the process-local grant minting limiter.
func WithRateLimiter(limiter GrantMintLimiter) Option {
	return func(cfg *config) {
		cfg.rateLimiter = limiter
	}
}

// WithClock injects a clock for tests.
func WithClock(now func() time.Time) Option {
	return func(cfg *config) {
		cfg.now = now
	}
}

func defaultConfig() config {
	return config{
		contentStoreFactory: func() (AgentContentStore, error) {
			return agentcontent.Default()
		},
		grantIssuerFactory: func() (DownloadGrantIssuer, error) {
			return downloadgrant.Default()
		},
		renderer:         installpack.NewRenderer(),
		instanceEndpoint: strings.TrimSpace(os.Getenv(EnvInstanceMCPEndpoint)),
		namespace:        DefaultNamespace,
		rateLimiter:      NewInMemoryGrantMintLimiter(defaultRateLimit, defaultRateWindow),
		now:              func() time.Time { return time.Now().UTC() },
	}
}

// RegisterTools statically registers the Ba instance-plane tool surface.
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
	return r.RegisterTool(agentLocalInstallPlanDef(), cfg.handleAgentLocalInstallPlan)
}

func agentLocalInstallPlanDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        ToolAgentLocalInstallPlan,
		Title:       "Plan local agent install pack",
		Description: "Render a deterministic Ba local-install pack for an account-scoped agent only when its current agent_soul lifecycle_state is published, mint a one-time header-free download grant, and return a TheoryMCP-compatible install-plan envelope. Typed five-body structure is rendered when present; otherwise the canonical Markdown body is used. Requires an account-holder OAuth principal with write scope.",
		Annotations: additiveMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"agent_id":{"type":"string","description":"Account-scoped agent id whose current agent_soul and agent_instructions records should be rendered into the local install pack."},
				"client":{"type":"string","enum":["claude_code","codex"],"description":"Local MCP client profile to render."},
				"profile":{"type":"string","enum":["claude_code","codex"],"description":"Optional profile alias. When supplied it must match client."},
				"actor_username":{"type":"string","description":"Optional explicit account-holder username. When supplied it must match the authenticated OAuth principal."}
			},
			"required":["agent_id","client"],
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{"type":"object","description":"Install-plan envelope containing grant id, header-free download URL, pack checksum/digest, resource metadata, manifest entries, mcp_server_name, merge/update guidance, expiration, and verification steps."},
				"error":{"type":"object","description":"Structured tool error when isError=true."}
			}
		}`),
	}
}

type agentLocalInstallPlanInput struct {
	AgentID       string `json:"agent_id"`
	Client        string `json:"client"`
	Profile       string `json:"profile"`
	ActorUsername string `json:"actor_username"`
}

func (cfg config) handleAgentLocalInstallPlan(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	account, errResult, err := authenticatedAccountHolderActor(principal, ToolAgentLocalInstallPlan)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if !principalHasWriteScope(principal) {
		return toolErrorResult("insufficient_scope", "agent_local_install_plan requires write scope because it mints a one-time download grant", http.StatusForbidden, map[string]any{
			"requiredScopes": []string{"write"},
			"grantedScopes":  normalizedScopes(principal.Claims.Scopes),
		})
	}

	in, errResult, err := parseAgentLocalInstallPlanInput(args, account)
	if errResult != nil || err != nil {
		return errResult, err
	}
	if gateResult, err := instancex402.RequireGrant(ctx, instancex402.Requirement{
		Tool:       ToolAgentLocalInstallPlan,
		Capability: instancex402.CapabilityInstallPlan,
		Account:    account,
	}); gateResult != nil || err != nil {
		return gateResult, err
	}

	contentStore, err := cfg.content()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "agent_content",
		})
	}

	actor, err := actorFromAgentID(in.AgentID)
	if err != nil {
		return toolErrorResult("invalid_request", err.Error(), http.StatusBadRequest, map[string]any{"source": "ba_install_plan"})
	}
	profile := installpack.Profile(in.Client)
	packID, err := PackIDForAgent(in.AgentID, in.Client)
	if err != nil {
		return toolErrorResult("invalid_request", err.Error(), http.StatusBadRequest, map[string]any{"source": "ba_install_plan"})
	}

	packInput, err := BuildPackInput(ctx, PackInputRequest{
		ContentStore:     contentStore,
		InstanceEndpoint: cfg.instanceEndpoint,
		Namespace:        cfg.normalizedNamespace(),
		Account:          account,
		AgentID:          in.AgentID,
		Actor:            actor,
		Client:           in.Client,
		Profile:          profile,
		PackID:           packID,
	})
	if err != nil {
		return packBuildToolResultFromError(err)
	}

	// Content readiness precedes the grant-mint limiter and issuer lookup:
	// unpublished or missing records are never grant attempts and always receive
	// the exact typed Ptah repair guidance.
	decision := cfg.rateLimit(account)
	if !decision.Allowed {
		return toolErrorResult("rate_limited", "agent_local_install_plan grant minting rate limit exceeded for this account", http.StatusTooManyRequests, map[string]any{
			"source":      "ba_grant_mint_rate_limit",
			"limit":       decision.Limit,
			"window":      decision.Window.String(),
			"reset_at":    formatTime(decision.ResetAt),
			"retry_after": maxInt(0, int(time.Until(decision.ResetAt).Seconds())),
		})
	}
	grantIssuer, err := cfg.grantIssuerStore()
	if err != nil {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, map[string]any{
			"source": "download_grant",
		})
	}

	pack, err := cfg.render(ctx, packInput.RenderRequest)
	if err != nil {
		return toolErrorResult("install_pack_render_failed", "Ba failed to render the local install pack", http.StatusInternalServerError, map[string]any{
			"source": "installpack",
		})
	}
	if pack == nil || len(pack.ZIPBytes) == 0 || pack.PackChecksum == "" {
		return toolErrorResult("install_pack_render_failed", "Ba rendered an empty local install pack", http.StatusInternalServerError, map[string]any{
			"source": "installpack",
		})
	}

	binding := downloadgrant.Binding{
		Account:    account,
		Actor:      actor,
		Namespace:  cfg.normalizedNamespace(),
		Route:      InstallerGrantBoundRoute,
		Client:     in.Client,
		Profile:    string(profile),
		PackID:     packInput.RenderRequest.PackID,
		PackDigest: packInput.RenderRequest.PackDigest,
	}
	issued, err := grantIssuer.Issue(ctx, downloadgrant.IssueInput{Binding: binding})
	if err != nil {
		return toolErrorResult("download_grant_issue_failed", "Ba failed to mint the one-time download grant", http.StatusInternalServerError, map[string]any{
			"source": "download_grant",
		})
	}
	if issued == nil || issued.GrantID == "" || strings.TrimSpace(issued.Token) == "" {
		return toolErrorResult("download_grant_issue_failed", "Ba minted an incomplete one-time download grant", http.StatusInternalServerError, map[string]any{
			"source": "download_grant",
		})
	}
	downloadURL, err := DownloadURLForGrant(cfg.instanceEndpoint, issued)
	if err != nil {
		return toolErrorResult("download_grant_issue_failed", "Ba failed to build the grant download URL", http.StatusInternalServerError, map[string]any{
			"source": "download_grant",
		})
	}

	slog.InfoContext(ctx, "ba agent_local_install_plan grant minted",
		"tool", ToolAgentLocalInstallPlan,
		"account", account,
		"actor", actor,
		"client", in.Client,
		"profile", string(profile),
		"grant_id", issued.GrantID,
		"pack_id", packInput.RenderRequest.PackID,
		"pack_digest", packInput.RenderRequest.PackDigest,
		"pack_checksum", pack.PackChecksum,
	)

	return installPlanSuccessResult(account, in.AgentID, actor, in.Client, string(profile), issued, downloadURL, pack)
}

func (cfg config) content() (AgentContentStore, error) {
	if cfg.contentStore != nil {
		return cfg.contentStore, nil
	}
	if cfg.contentStoreFactory == nil {
		return nil, fmt.Errorf("agent content store is not configured")
	}
	return cfg.contentStoreFactory()
}

func (cfg config) grantIssuerStore() (DownloadGrantIssuer, error) {
	if cfg.grantIssuer != nil {
		return cfg.grantIssuer, nil
	}
	if cfg.grantIssuerFactory == nil {
		return nil, fmt.Errorf("download grant issuer is not configured")
	}
	return cfg.grantIssuerFactory()
}

func (cfg config) render(ctx context.Context, req installpack.Request) (*installpack.Pack, error) {
	renderer := cfg.renderer
	if renderer == nil {
		renderer = installpack.NewRenderer()
	}
	return renderer.Render(ctx, req)
}

func (cfg config) normalizedNamespace() string {
	if namespace := strings.ToLower(strings.TrimSpace(cfg.namespace)); namespace != "" {
		return namespace
	}
	return DefaultNamespace
}

func (cfg config) rateLimit(account string) RateLimitDecision {
	limiter := cfg.rateLimiter
	if limiter == nil {
		return RateLimitDecision{Allowed: true}
	}
	now := time.Now().UTC()
	if cfg.now != nil {
		now = cfg.now().UTC()
	}
	return limiter.Allow(account, now)
}

func parseAgentLocalInstallPlanInput(args json.RawMessage, account string) (agentLocalInstallPlanInput, *mcpruntime.ToolResult, error) {
	raw := strings.TrimSpace(string(args))
	if raw == "" || raw == "null" {
		raw = "{}"
	}

	var in agentLocalInstallPlanInput
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("invalid_request", "invalid args: "+err.Error(), http.StatusBadRequest, nil), nil
	}
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.Client = normalizeClient(in.Client)
	in.Profile = normalizeClient(in.Profile)
	in.ActorUsername = normalizeActorUsername(in.ActorUsername)

	if in.AgentID == "" {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("invalid_request", "agent_id is required", http.StatusBadRequest, nil), nil
	}
	if in.Client == "" {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("invalid_request", "client is required", http.StatusBadRequest, nil), nil
	}
	if !supportedClient(in.Client) {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("invalid_request", "client must be claude_code or codex", http.StatusBadRequest, nil), nil
	}
	if in.Profile != "" && in.Profile != in.Client {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("invalid_request", "profile must match client when supplied", http.StatusBadRequest, nil), nil
	}
	if in.ActorUsername != "" && in.ActorUsername != account {
		return agentLocalInstallPlanInput{}, mustToolErrorResult("forbidden", "actor_username must match authenticated principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ba",
		}), nil
	}
	return in, nil, nil
}

func normalizeClient(client string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(client), "-", "_"))
}

func supportedClient(client string) bool {
	switch client {
	case string(installpack.ProfileClaudeCode), string(installpack.ProfileCodex):
		return true
	default:
		return false
	}
}

func authenticatedAccountHolderActor(principal *auth.Principal, toolName string) (string, *mcpruntime.ToolResult, error) {
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil || principal.Claims.IsAgent {
		return "", mustToolErrorResult("forbidden", toolName+" requires an account-holder OAuth principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ba",
		}), nil
	}
	actorUsername := normalizeActorUsername(firstNonEmpty(principal.Claims.GetUsername(), principal.Identity))
	if actorUsername == "" {
		return "", mustToolErrorResult("forbidden", toolName+" requires an authenticated actor username", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ba",
		}), nil
	}
	return actorUsername, nil, nil
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

func additiveMutationToolAnnotations() *mcpruntime.ToolAnnotations {
	return &mcpruntime.ToolAnnotations{
		ReadOnlyHint:    boolHint(false),
		DestructiveHint: boolHint(false),
		IdempotentHint:  boolHint(false),
	}
}

func boolHint(value bool) *bool { return &value }

// PackInputRequest describes a render request assembled from a plan or a
// consumed download-grant binding.
type PackInputRequest struct {
	ContentStore     AgentContentStore
	InstanceEndpoint string
	Namespace        string
	Account          string
	AgentID          string
	Actor            string
	Client           string
	Profile          installpack.Profile
	PackID           string
	PackDigest       string
}

// PackInput is the resolved deterministic install-pack render request.
type PackInput struct {
	RenderRequest installpack.Request
	SoulRecord    *agentcontent.Record
	Instructions  *agentcontent.Record
}

// BuildPackInput reads current account-scoped content and produces an explicit
// installpack.Request. It never reads host headers or process global state.
func BuildPackInput(ctx context.Context, in PackInputRequest) (*PackInput, error) {
	if in.ContentStore == nil {
		return nil, fmt.Errorf("agent content store is required")
	}
	account := normalizeActorUsername(in.Account)
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	actor := strings.ToLower(strings.TrimSpace(in.Actor))
	if actor == "" {
		var err error
		actor, err = actorFromAgentID(agentID)
		if err != nil {
			return nil, err
		}
	}
	client := normalizeClient(in.Client)
	if client == "" {
		client = normalizeClient(string(in.Profile))
	}
	if !supportedClient(client) {
		return nil, fmt.Errorf("client must be claude_code or codex")
	}
	profile := in.Profile
	if profile == "" {
		profile = installpack.Profile(client)
	}
	if normalizeClient(string(profile)) != client {
		return nil, fmt.Errorf("profile must match client")
	}
	stageDomain, err := StageDomainFromInstanceEndpoint(in.InstanceEndpoint)
	if err != nil {
		return nil, err
	}
	namespace := strings.ToLower(strings.TrimSpace(in.Namespace))
	if namespace == "" {
		namespace = DefaultNamespace
	}
	packID := strings.TrimSpace(in.PackID)
	if packID == "" {
		packID, err = PackIDForAgent(agentID, client)
		if err != nil {
			return nil, err
		}
	}

	soul, err := in.ContentStore.Get(ctx, account, agentID, agentcontent.ContentTypeAgentSoul)
	if err != nil {
		if errors.Is(err, agentcontent.ErrContentNotFound) {
			return nil, &AgentContentMissingError{
				AgentID:     agentID,
				ContentType: agentcontent.ContentTypeAgentSoul,
				FixTool:     "agent_soul_upsert",
				NextTool:    "agent_soul_publish",
			}
		}
		return nil, fmt.Errorf("read agent_soul: %w", err)
	}
	if soul == nil {
		return nil, &AgentContentMissingError{
			AgentID:     agentID,
			ContentType: agentcontent.ContentTypeAgentSoul,
			FixTool:     "agent_soul_upsert",
			NextTool:    "agent_soul_publish",
		}
	}
	if soul.LifecycleState != agentcontent.LifecycleStatePublished {
		return nil, &AgentSoulPublicationRequiredError{
			AgentID:        agentID,
			LifecycleState: string(soul.LifecycleState),
			PublishTool:    "agent_soul_publish",
		}
	}
	renderedSoul := soul.Content
	if soul.Document != nil {
		renderedSoul, err = agentcontent.RenderSoulDocument(soul.Document)
		if err != nil {
			return nil, fmt.Errorf("render published agent_soul: %w", err)
		}
	}
	instructions, err := in.ContentStore.Get(ctx, account, agentID, agentcontent.ContentTypeAgentInstructions)
	if err != nil {
		if errors.Is(err, agentcontent.ErrContentNotFound) {
			return nil, &AgentContentMissingError{
				AgentID:     agentID,
				ContentType: agentcontent.ContentTypeAgentInstructions,
				FixTool:     "agent_instructions_upsert",
			}
		}
		return nil, fmt.Errorf("read agent_instructions: %w", err)
	}
	if instructions == nil {
		return nil, &AgentContentMissingError{
			AgentID:     agentID,
			ContentType: agentcontent.ContentTypeAgentInstructions,
			FixTool:     "agent_instructions_upsert",
		}
	}
	packDigest := strings.TrimSpace(in.PackDigest)
	if packDigest == "" {
		packDigest = packInputDigest(account, actor, namespace, agentID, client, renderedSoul, soul, instructions)
	}

	return &PackInput{
		RenderRequest: installpack.Request{
			Profile:           profile,
			StageDomain:       stageDomain,
			Actor:             actor,
			Namespace:         namespace,
			Account:           account,
			PackID:            packID,
			PackDigest:        packDigest,
			AgentSoul:         renderedSoul,
			AgentInstructions: instructions.Content,
		},
		SoulRecord:   soul,
		Instructions: instructions,
	}, nil
}

func packBuildToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}
	details := map[string]any{"source": "ba_install_plan"}
	var publicationErr *AgentSoulPublicationRequiredError
	var missingErr *AgentContentMissingError
	switch {
	case errors.As(err, &missingErr):
		details["agent_id"] = missingErr.AgentID
		details["content_type"] = string(missingErr.ContentType)
		details["fix_tool"] = missingErr.FixTool
		details["next_tool"] = missingErr.NextTool
		return toolErrorResult("not_found", missingErr.Error(), http.StatusNotFound, details)
	case errors.As(err, &publicationErr):
		details["agent_id"] = publicationErr.AgentID
		details["lifecycle_state"] = publicationErr.LifecycleState
		details["publish_tool"] = publicationErr.PublishTool
		return toolErrorResult("agent_soul_publish_required", publicationErr.Error(), http.StatusConflict, details)
	case errors.Is(err, agentcontent.ErrContentNotFound):
		return toolErrorResult("not_found", "required account-scoped agent_soul or agent_instructions content was not found", http.StatusNotFound, details)
	default:
		msg := err.Error()
		if strings.Contains(msg, "stage domain") || strings.Contains(msg, "endpoint") || strings.Contains(msg, EnvInstanceMCPEndpoint) {
			return toolErrorResult("not_configured", "Ba instance endpoint configuration is invalid", http.StatusInternalServerError, details)
		}
		return toolErrorResult("invalid_request", msg, http.StatusBadRequest, details)
	}
}

func packInputDigest(account, actor, namespace, agentID, client, renderedSoul string, soul *agentcontent.Record, instructions *agentcontent.Record) string {
	payload := struct {
		Schema                     string `json:"schema"`
		Account                    string `json:"account"`
		Actor                      string `json:"actor"`
		Namespace                  string `json:"namespace"`
		AgentID                    string `json:"agent_id"`
		Client                     string `json:"client"`
		AgentSoulVersion           int64  `json:"agent_soul_version"`
		AgentSoulLifecycle         string `json:"agent_soul_lifecycle"`
		AgentSoulContentHash       string `json:"agent_soul_content_hash"`
		AgentInstructionsVersion   int64  `json:"agent_instructions_version"`
		AgentInstructionsLifecycle string `json:"agent_instructions_lifecycle"`
		AgentInstructionsHash      string `json:"agent_instructions_content_hash"`
	}{
		Schema:                     planSchema,
		Account:                    account,
		Actor:                      actor,
		Namespace:                  namespace,
		AgentID:                    agentID,
		Client:                     client,
		AgentSoulVersion:           recordVersion(soul),
		AgentSoulLifecycle:         recordLifecycle(soul),
		AgentSoulContentHash:       contentHash(renderedSoul),
		AgentInstructionsVersion:   recordVersion(instructions),
		AgentInstructionsLifecycle: recordLifecycle(instructions),
		AgentInstructionsHash:      contentHash(recordContent(instructions)),
	}
	b, _ := json.Marshal(payload)
	return contentHash(string(b))
}

func recordVersion(record *agentcontent.Record) int64 {
	if record == nil {
		return 0
	}
	return record.Version
}

func recordLifecycle(record *agentcontent.Record) string {
	if record == nil {
		return ""
	}
	return string(record.LifecycleState)
}

func recordContent(record *agentcontent.Record) string {
	if record == nil {
		return ""
	}
	return record.Content
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PackIDForAgent returns a deterministic pack id that carries the source
// agent_id needed by the later header-free download route provider.
func PackIDForAgent(agentID string, client string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	client = normalizeClient(client)
	if !supportedClient(client) {
		return "", fmt.Errorf("client must be claude_code or codex")
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(agentID))
	return packIDPrefix + client + "/" + encoded, nil
}

// AgentIDFromPackID reverses PackIDForAgent for the download route provider.
func AgentIDFromPackID(packID string) (string, error) {
	packID = strings.TrimSpace(packID)
	if !strings.HasPrefix(packID, packIDPrefix) {
		return "", fmt.Errorf("pack_id is not a Ba local-install pack id")
	}
	rest := strings.TrimPrefix(packID, packIDPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || !supportedClient(parts[0]) || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("pack_id is not a valid Ba local-install pack id")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || strings.TrimSpace(string(decoded)) == "" {
		return "", fmt.Errorf("pack_id is not a valid Ba local-install pack id")
	}
	return strings.TrimSpace(string(decoded)), nil
}

func actorFromAgentID(agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	candidate := agentID
	if u, err := url.Parse(agentID); err == nil && u.Scheme != "" && u.Host != "" {
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(parts) > 0 {
			candidate, _ = url.PathUnescape(parts[len(parts)-1])
		}
	}
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" || strings.ContainsAny(candidate, "/\\?#") || strings.Contains(candidate, "..") {
		return "", fmt.Errorf("agent_id must identify a single safe actor path segment or URL with a safe final path segment")
	}
	for _, r := range candidate {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("agent_id must identify a single safe actor path segment")
		}
	}
	if url.PathEscape(candidate) != candidate {
		return "", fmt.Errorf("agent_id must identify a single safe actor path segment")
	}
	return candidate, nil
}

// StageDomainFromInstanceEndpoint derives the stage domain from the CDK-injected
// instance endpoint template. It intentionally does not inspect request Host or
// forwarded headers.
func StageDomainFromInstanceEndpoint(endpoint string) (string, error) {
	u, err := parseInstanceEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("%s host must start with api.", EnvInstanceMCPEndpoint)
	}
	stageDomain := strings.TrimPrefix(host, "api.")
	if stageDomain == "" || !strings.Contains(stageDomain, ".") {
		return "", fmt.Errorf("%s host must include a stage domain", EnvInstanceMCPEndpoint)
	}
	return stageDomain, nil
}

func parseInstanceEndpoint(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceMCPEndpoint)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("%s must be an https URL", EnvInstanceMCPEndpoint)
	}
	return u, nil
}

// DownloadURLForGrant builds the header-free one-time download URL. The raw
// grant token is included only in this returned URL and is never persisted by
// internal/downloadgrant or logged by this package.
func DownloadURLForGrant(instanceEndpoint string, issued *downloadgrant.IssuedGrant) (string, error) {
	if issued == nil {
		return "", fmt.Errorf("issued grant is required")
	}
	u, err := parseInstanceEndpoint(instanceEndpoint)
	if err != nil {
		return "", err
	}
	grantID := strings.TrimSpace(issued.GrantID)
	token := strings.TrimSpace(issued.Token)
	if grantID == "" || token == "" {
		return "", fmt.Errorf("issued grant id and token are required")
	}
	u.Path = "/instance/downloads/installer-grants/" + url.PathEscape(grantID)
	u.RawQuery = ""
	u.Fragment = ""
	q := u.Query()
	q.Set("token", token)
	q.Set("account", issued.Binding.Account)
	q.Set("actor", issued.Binding.Actor)
	q.Set("namespace", issued.Binding.Namespace)
	q.Set("client", issued.Binding.Client)
	q.Set("profile", issued.Binding.Profile)
	q.Set("pack_id", issued.Binding.PackID)
	q.Set("pack_digest", issued.Binding.PackDigest)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func installPlanSuccessResult(account string, agentID string, actor string, client string, profile string, issued *downloadgrant.IssuedGrant, downloadURL string, pack *installpack.Pack) (*mcpruntime.ToolResult, error) {
	expiresAt := formatTime(issued.ExpiresAt)
	manifestEntries, err := jsonMapSlice(pack.Manifest.ManifestEntries)
	if err != nil {
		return nil, err
	}
	mergeInstructions, err := jsonMapSlice(pack.Manifest.MergeInstructions)
	if err != nil {
		return nil, err
	}
	installMarker, err := jsonMap(pack.Manifest.InstallMarker)
	if err != nil {
		return nil, err
	}
	manifest, err := jsonMap(pack.Manifest)
	if err != nil {
		return nil, err
	}

	resource := map[string]any{
		"uri":                           downloadURL,
		"download_url":                  downloadURL,
		"method":                        "GET",
		"media_type":                    "application/zip",
		"mime_type":                     "application/zip",
		"resource_kind":                 "installer_grant_download",
		"grant_id":                      issued.GrantID,
		"expires_at":                    expiresAt,
		"requires_authorization_header": false,
		"query_binding": map[string]any{
			"account":     issued.Binding.Account,
			"actor":       issued.Binding.Actor,
			"namespace":   issued.Binding.Namespace,
			"route":       issued.Binding.Route,
			"client":      issued.Binding.Client,
			"profile":     issued.Binding.Profile,
			"pack_id":     issued.Binding.PackID,
			"pack_digest": issued.Binding.PackDigest,
		},
	}
	verificationSteps := []string{
		"Download the ZIP from install_pack_resource.uri before expires_at; do not add Authorization headers to the grant URL.",
		"Compute SHA-256 over the downloaded ZIP bytes and compare it to pack_checksum.",
		"Read MANIFEST.json from the ZIP and confirm schema, pack_id, pack_digest, mcp_server_name, and mcp_endpoint_url match this plan.",
		"For every manifest_entries item, compute SHA-256 over that ZIP entry and compare it to checksum before writing local files.",
		"Review merge_instructions and update_guidance locally; the server never writes to the caller filesystem.",
	}
	updateGuidance := []map[string]any{
		{"action": "detect_existing_marker", "path": pack.Manifest.InstallMarker.Path, "description": "If an existing marker is present, compare mcp_server_name, pack_id, and pack_digest before replacing local files."},
		{"action": "merge_not_overwrite", "path": ".mcp.json", "description": "Merge the server entry keyed by mcp_server_name rather than replacing unrelated MCP client configuration."},
	}

	data := map[string]any{
		"schema":                planSchema,
		"account":               account,
		"agent_id":              agentID,
		"actor":                 actor,
		"client":                client,
		"profile":               profile,
		"grant_id":              issued.GrantID,
		"expires_at":            expiresAt,
		"download_url":          downloadURL,
		"install_pack_resource": resource,
		"resource_metadata":     resource,
		"pack_id":               pack.Manifest.PackID,
		"pack_digest":           pack.Manifest.PackDigest,
		"pack_checksum":         pack.PackChecksum,
		"mcp_server_name":       pack.MCPServerName,
		"mcp_endpoint_url":      pack.MCPEndpointURL,
		"manifest":              manifest,
		"manifest_entries":      manifestEntries,
		"marker_metadata":       installMarker,
		"install_marker":        installMarker,
		"merge_instructions":    mergeInstructions,
		"update_guidance":       updateGuidance,
		"verification_steps":    verificationSteps,
	}
	text := map[string]any{
		"summary":           "Ba local-install plan prepared with a one-time installer grant",
		"grant_id":          issued.GrantID,
		"expires_at":        expiresAt,
		"pack_checksum":     pack.PackChecksum,
		"pack_digest":       pack.Manifest.PackDigest,
		"mcp_server_name":   pack.MCPServerName,
		"download_location": "structuredContent.data.install_pack_resource.uri",
		"data":              map[string]any{"location": "structuredContent.data"},
	}
	return toolJSONTextResult(text, map[string]any{"data": data})
}

func jsonMap(value any) (map[string]any, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func jsonMapSlice(value any) ([]map[string]any, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
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

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RateLimitDecision describes a process-local grant minting rate decision.
type RateLimitDecision struct {
	Allowed   bool
	Limit     int
	Remaining int
	Window    time.Duration
	ResetAt   time.Time
}

// GrantMintLimiter guards account-scoped one-time grant minting.
type GrantMintLimiter interface {
	Allow(account string, now time.Time) RateLimitDecision
}

// InMemoryGrantMintLimiter is a bounded, process-local limiter for this
// foundation slice. It does not coordinate across Lambda execution environments
// and is therefore a safety backstop, not a durable quota system.
type InMemoryGrantMintLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

// NewInMemoryGrantMintLimiter creates a process-local per-account limiter.
func NewInMemoryGrantMintLimiter(limit int, window time.Duration) *InMemoryGrantMintLimiter {
	if limit <= 0 {
		limit = defaultRateLimit
	}
	if window <= 0 {
		window = defaultRateWindow
	}
	return &InMemoryGrantMintLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (l *InMemoryGrantMintLimiter) Allow(account string, now time.Time) RateLimitDecision {
	if l == nil {
		return RateLimitDecision{Allowed: true}
	}
	account = normalizeActorUsername(account)
	if account == "" {
		return RateLimitDecision{Allowed: false, Limit: l.limit, Window: l.window, ResetAt: now.Add(l.window)}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := l.hits[account][:0]
	for _, hit := range l.hits[account] {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	l.hits[account] = kept
	if len(kept) >= l.limit {
		reset := kept[0].Add(l.window)
		return RateLimitDecision{Allowed: false, Limit: l.limit, Remaining: 0, Window: l.window, ResetAt: reset}
	}
	l.hits[account] = append(kept, now)
	return RateLimitDecision{Allowed: true, Limit: l.limit, Remaining: l.limit - len(l.hits[account]), Window: l.window, ResetAt: now.Add(l.window)}
}
