package baserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	"github.com/golang-jwt/jwt/v5"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const testInstanceEndpoint = "https://api.dev.example.com/instance/{surface}/mcp"

func TestRegisterToolsRegistersAgentLocalInstallPlan(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	tools := registry.List()
	if got, want := len(tools), 1; got != want {
		t.Fatalf("tool count = %d, want %d", got, want)
	}
	def := tools[0]
	if def.Name != ToolAgentLocalInstallPlan {
		t.Fatalf("tool name = %q, want %q", def.Name, ToolAgentLocalInstallPlan)
	}
	if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint {
		t.Fatalf("agent_local_install_plan must be write/additive: %+v", def.Annotations)
	}
	if def.Annotations.DestructiveHint == nil || *def.Annotations.DestructiveHint {
		t.Fatalf("agent_local_install_plan must be non-destructive grant minting: %+v", def.Annotations)
	}
	if def.Annotations.IdempotentHint == nil || *def.Annotations.IdempotentHint {
		t.Fatalf("agent_local_install_plan is not idempotent because it mints grants: %+v", def.Annotations)
	}

	var schema struct {
		Required []string                   `json:"required"`
		Props    map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("input schema invalid json: %v", err)
	}
	for _, required := range []string{"agent_id", "client"} {
		if !contains(schema.Required, required) {
			t.Fatalf("input schema required = %v, missing %s", schema.Required, required)
		}
	}
	for _, prop := range []string{"actor_username", "profile"} {
		if _, ok := schema.Props[prop]; !ok {
			t.Fatalf("input schema missing %s", prop)
		}
	}
	if !strings.Contains(string(schema.Props["client"]), "claude_code") || !strings.Contains(string(schema.Props["client"]), "codex") {
		t.Fatalf("client schema does not advertise supported clients: %s", string(schema.Props["client"]))
	}
}

func TestAgentLocalInstallPlanMintsGrantEnvelopeWithoutTextTokenLeak(t *testing.T) {
	content := newFakeContentStore("owner", "agent-one")
	issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry,
		WithAgentContentStore(content),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
		WithNamespace("equaltoai"),
		WithRateLimiter(NewInMemoryGrantMintLimiter(10, time.Minute)),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("Owner", []string{"write"}), ToolAgentLocalInstallPlan, json.RawMessage(`{
		"agent_id":" agent-one ",
		"client":"codex",
		"profile":"codex",
		"actor_username":"owner"
	}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if issuer.calls != 1 {
		t.Fatalf("grant issuer calls = %d, want 1", issuer.calls)
	}
	binding := issuer.inputs[0].Binding
	if binding.Account != "owner" || binding.Actor != "agent-one" || binding.Namespace != "equaltoai" || binding.Route != InstallerGrantBoundRoute || binding.Client != "codex" || binding.Profile != "codex" {
		t.Fatalf("grant binding = %+v", binding)
	}
	if !strings.HasPrefix(binding.PackID, packIDPrefix+"codex/") {
		t.Fatalf("pack id = %q, want Ba codex prefix", binding.PackID)
	}
	if gotAgentID, err := AgentIDFromPackID(binding.PackID); err != nil || gotAgentID != "agent-one" {
		t.Fatalf("AgentIDFromPackID(%q) = %q/%v, want agent-one", binding.PackID, gotAgentID, err)
	}
	if !strings.HasPrefix(binding.PackDigest, "sha256:") {
		t.Fatalf("pack digest = %q, want sha256", binding.PackDigest)
	}

	data := toolData(t, result)
	if data["schema"] != planSchema || data["grant_id"] != "dg_test" || data["mcp_server_name"] == "" {
		t.Fatalf("plan identity fields = %+v", data)
	}
	if data["pack_digest"] != binding.PackDigest {
		t.Fatalf("pack_digest = %q, want binding digest %q", data["pack_digest"], binding.PackDigest)
	}
	packChecksum, _ := data["pack_checksum"].(string)
	if !strings.HasPrefix(packChecksum, "sha256:") {
		t.Fatalf("pack_checksum = %q, want sha256", packChecksum)
	}
	if data["mcp_endpoint_url"] != "https://api.dev.example.com/mcp/agent-one" {
		t.Fatalf("mcp_endpoint_url = %q", data["mcp_endpoint_url"])
	}
	entries, _ := data["manifest_entries"].([]map[string]any)
	if len(entries) == 0 {
		t.Fatalf("manifest_entries empty: %+v", data["manifest_entries"])
	}
	merge, _ := data["merge_instructions"].([]map[string]any)
	if len(merge) == 0 {
		t.Fatalf("merge_instructions empty")
	}
	if steps, ok := data["verification_steps"].([]string); !ok || len(steps) == 0 {
		t.Fatalf("verification_steps = %#v", data["verification_steps"])
	}

	downloadURL, _ := data["download_url"].(string)
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		t.Fatalf("download_url parse: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "api.dev.example.com" || parsed.Path != "/instance/downloads/installer-grants/dg_test" {
		t.Fatalf("download_url = %s", downloadURL)
	}
	q := parsed.Query()
	if q.Get("token") != "raw-plan-token" {
		t.Fatalf("download token query = %q, want raw token", q.Get("token"))
	}
	for key, want := range map[string]string{
		"account":     binding.Account,
		"actor":       binding.Actor,
		"namespace":   binding.Namespace,
		"client":      binding.Client,
		"profile":     binding.Profile,
		"pack_id":     binding.PackID,
		"pack_digest": binding.PackDigest,
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("download query %s = %q, want %q", key, got, want)
		}
	}
	resource := data["install_pack_resource"].(map[string]any)
	if resource["uri"] != downloadURL || resource["requires_authorization_header"] != false || resource["media_type"] != "application/zip" {
		t.Fatalf("install_pack_resource = %+v", resource)
	}

	text := result.Content[0].Text
	for _, forbidden := range []string{"raw-plan-token", "draft soul content", "follow the operator", "tokenHash", "sha256:token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("text result leaked forbidden material %q: %s", forbidden, text)
		}
	}
}

func TestAgentLocalInstallPlanRejectsUnauthorizedPrincipalsBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		code string
	}{
		{name: "read only", ctx: toolContext("owner", []string{"read"}), code: "insufficient_scope"},
		{name: "agent principal", ctx: toolContextWithAgent("owner", []string{"write"}, true), code: "forbidden"},
		{name: "legacy instance key", ctx: auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeInstanceKey, Identity: "instance"}, "legacy-key"), code: "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry,
				WithAgentContentStore(newFakeContentStore("owner", "agent-one")),
				WithDownloadGrantIssuer(issuer),
				WithInstanceEndpoint(testInstanceEndpoint),
			); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(tc.ctx, ToolAgentLocalInstallPlan, json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			payload := toolError(t, result)
			if payload["code"] != tc.code {
				t.Fatalf("error code = %q, want %q payload=%+v", payload["code"], tc.code, payload)
			}
			if issuer.calls != 0 {
				t.Fatalf("grant issuer calls = %d, want 0", issuer.calls)
			}
		})
	}
}

func TestAgentLocalInstallPlanRejectsActorMismatchBeforeGrant(t *testing.T) {
	issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry,
		WithAgentContentStore(newFakeContentStore("owner", "agent-one")),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(toolContext("owner", []string{"write"}), ToolAgentLocalInstallPlan, json.RawMessage(`{"agent_id":"agent-one","client":"codex","actor_username":"other"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	payload := toolError(t, result)
	if payload["code"] != "forbidden" {
		t.Fatalf("error payload = %+v", payload)
	}
	if issuer.calls != 0 {
		t.Fatalf("grant issuer calls = %d, want 0", issuer.calls)
	}
}

func TestAgentLocalInstallPlanEnforcesPerAccountInProcessRateCap(t *testing.T) {
	issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry,
		WithAgentContentStore(newFakeContentStore("owner", "agent-one")),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
		WithRateLimiter(NewInMemoryGrantMintLimiter(1, time.Minute)),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := toolContext("owner", []string{"write"})
	args := json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`)
	first, err := registry.Call(ctx, ToolAgentLocalInstallPlan, args)
	if err != nil || first == nil || first.IsError {
		t.Fatalf("first call result=%+v err=%v", first, err)
	}
	second, err := registry.Call(ctx, ToolAgentLocalInstallPlan, args)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	payload := toolError(t, second)
	if payload["code"] != "rate_limited" || statusValue(payload["status"]) != 429 {
		t.Fatalf("rate limit payload = %+v", payload)
	}
	if issuer.calls != 1 {
		t.Fatalf("grant issuer calls = %d, want 1", issuer.calls)
	}
}

func TestPackIDEndpointAndActorDerivation(t *testing.T) {
	packID, err := PackIDForAgent("https://lesser.example/users/Ptah_Agent", "claude-code")
	if err != nil {
		t.Fatalf("PackIDForAgent: %v", err)
	}
	agentID, err := AgentIDFromPackID(packID)
	if err != nil || agentID != "https://lesser.example/users/Ptah_Agent" {
		t.Fatalf("AgentIDFromPackID = %q/%v", agentID, err)
	}
	actor, err := actorFromAgentID(agentID)
	if err != nil || actor != "ptah_agent" {
		t.Fatalf("actorFromAgentID = %q/%v, want ptah_agent", actor, err)
	}
	stageDomain, err := StageDomainFromInstanceEndpoint(testInstanceEndpoint)
	if err != nil || stageDomain != "dev.example.com" {
		t.Fatalf("StageDomainFromInstanceEndpoint = %q/%v", stageDomain, err)
	}
}

type fakeGrantIssuer struct {
	calls     int
	inputs    []downloadgrant.IssueInput
	expiresAt time.Time
}

func (f *fakeGrantIssuer) Issue(_ context.Context, in downloadgrant.IssueInput) (*downloadgrant.IssuedGrant, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	return &downloadgrant.IssuedGrant{
		Binding:        in.Binding,
		GrantID:        "dg_test",
		Token:          "raw-plan-token",
		ExpiresAt:      f.expiresAt,
		ExpiresAtEpoch: f.expiresAt.Unix(),
	}, nil
}

type fakeContentStore struct {
	records map[string]*agentcontent.Record
	calls   []string
}

func newFakeContentStore(account, agentID string) *fakeContentStore {
	return &fakeContentStore{records: map[string]*agentcontent.Record{
		key(account, agentID, agentcontent.ContentTypeAgentSoul): {
			Account:            account,
			AgentID:            agentID,
			Type:               agentcontent.ContentTypeAgentSoul,
			Content:            "draft soul content",
			Version:            3,
			LifecycleState:     agentcontent.LifecycleStateDraft,
			CreatedAt:          time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
			UpdatedAt:          time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC),
			UpdatedBySubjectID: "subject-owner",
		},
		key(account, agentID, agentcontent.ContentTypeAgentInstructions): {
			Account:            account,
			AgentID:            agentID,
			Type:               agentcontent.ContentTypeAgentInstructions,
			Content:            "follow the operator",
			Version:            4,
			LifecycleState:     agentcontent.LifecycleStateDraft,
			CreatedAt:          time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC),
			UpdatedAt:          time.Date(2026, 7, 15, 12, 31, 0, 0, time.UTC),
			UpdatedBySubjectID: "subject-owner",
		},
	}}
}

func (f *fakeContentStore) Get(_ context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error) {
	f.calls = append(f.calls, key(account, agentID, contentType))
	record := f.records[key(account, agentID, contentType)]
	if record == nil {
		return nil, agentcontent.ErrContentNotFound
	}
	clone := *record
	return &clone, nil
}

func key(account, agentID string, contentType agentcontent.ContentType) string {
	return strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.TrimSpace(agentID) + "\x00" + string(contentType)
}

func toolContext(username string, scopes []string) context.Context {
	return toolContextWithAgent(username, scopes, false)
}

func toolContextWithAgent(username string, scopes []string, isAgent bool) context.Context {
	subject := "subject-" + strings.ToLower(strings.TrimSpace(username))
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: username,
		Claims: &auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
			Username:         username,
			Scopes:           scopes,
			IsAgent:          isAgent,
		},
	}, "owner-oauth-token")
}

func toolData(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structured content: %+v", result)
	}
	data, ok := result.StructuredContent["data"].(map[string]any)
	if !ok {
		t.Fatalf("structured data = %#v", result.StructuredContent["data"])
	}
	return data
}

func toolError(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result IsError=false: %+v", result)
	}
	payload, ok := result.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload = %#v", result.StructuredContent["error"])
	}
	return payload
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func statusValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
