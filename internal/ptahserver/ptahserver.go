package ptahserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	// EnvSoulBindingIntegrationBearer is the dedicated server-to-server bearer
	// Body/Ptah uses for Lesser's hosted soul/body binding API. It must not be a
	// caller OAuth token.
	EnvSoulBindingIntegrationBearer = "LESSER_SOUL_BINDING_INTEGRATION_BEARER"

	toolAgentBindSoul = "agent_bind_soul"
)

type soulBindingClient interface {
	InitiateSoulBinding(ctx context.Context, integrationBearer string, idempotencyKey string, req lesserapi.SoulBindingRequest) (*lesserapi.SoulBindingResponse, error)
}

type config struct {
	client              soulBindingClient
	clientFactory       func() (soulBindingClient, error)
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
		cfg.client = client
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

	return r.RegisterTool(agentBindSoulDef(), cfg.handleAgentBindSoul)
}

func defaultConfig() config {
	return config{
		clientFactory: func() (soulBindingClient, error) {
			return lesserapi.Default()
		},
		integrationBearerFn: func(context.Context) (string, error) {
			return strings.TrimSpace(os.Getenv(EnvSoulBindingIntegrationBearer)), nil
		},
	}
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

func (cfg config) handleAgentBindSoul(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	principal := auth.PrincipalFromToolContext(ctx)
	actorUsername, errResult, err := authenticatedAccountHolderActor(principal)
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

func authenticatedAccountHolderActor(principal *auth.Principal) (string, *mcpruntime.ToolResult, error) {
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil || principal.Claims.IsAgent {
		return "", mustToolErrorResult("forbidden", "agent_bind_soul requires an account-holder OAuth principal", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	actorUsername := normalizeActorUsername(firstNonEmpty(principal.Claims.GetUsername(), principal.Identity))
	if actorUsername == "" {
		return "", mustToolErrorResult("forbidden", "agent_bind_soul requires an authenticated actor username", http.StatusForbidden, map[string]any{
			"source": "lesser_body_ptah",
		}), nil
	}
	return actorUsername, nil, nil
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

func (cfg config) soulBindingClient() (soulBindingClient, error) {
	if cfg.client != nil {
		return cfg.client, nil
	}
	if cfg.clientFactory == nil {
		return nil, fmt.Errorf("soul-binding client is not configured")
	}
	return cfg.clientFactory()
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

func additiveMutationToolAnnotations() *mcpruntime.ToolAnnotations {
	return &mcpruntime.ToolAnnotations{
		ReadOnlyHint:    boolHint(false),
		DestructiveHint: boolHint(false),
		IdempotentHint:  boolHint(false),
	}
}

func boolHint(value bool) *bool {
	return &value
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

func mapFromJSON(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal lesser soul-binding response: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal lesser soul-binding response: %w", err)
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
