package baserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	"github.com/equaltoai/lesser-body/internal/installpack"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/golang-jwt/jwt/v5"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
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
		WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "prototype-11")),
		WithActorBindingReader(&fakeActorBindingReader{actorUsername: "prototype-11"}),
		WithSoulBindingIntegrationBearer("binding-secret"),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
		WithNamespace("equaltoai"),
		WithRateLimiter(NewInMemoryGrantMintLimiter(10, time.Minute)),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	result, err := registry.Call(operatorToolContext("Owner", []string{"write"}), ToolAgentLocalInstallPlan, json.RawMessage(`{
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
	if binding.Account != "owner" || binding.Actor != "prototype-11" || binding.Namespace != "equaltoai" || binding.Route != InstallerGrantBoundRoute || binding.Client != "codex" || binding.Profile != "codex" {
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
	if data["mcp_endpoint_url"] != "https://api.dev.example.com/mcp/prototype-11" {
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
	for _, forbidden := range []string{"raw-plan-token", "published soul body", "follow the operator", "tokenHash", "sha256:token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("text result leaked forbidden material %q: %s", forbidden, text)
		}
	}
}

func TestAgentLocalInstallPlanValidatesRegistryActorAgainstAuthoritativeLesserBinding(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		registryLocalID       string
		authoritativeUsername string
		wantError             bool
	}{
		{
			name:                  "agreement renders",
			registryLocalID:       "sentinelsentinel",
			authoritativeUsername: "sentinelsentinel",
		},
		{
			name:                  "disagreement fails closed",
			registryLocalID:       "sentinel",
			authoritativeUsername: "sentinelsentinel",
			wantError:             true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
			binding := &fakeActorBindingReader{actorUsername: tc.authoritativeUsername}
			renderer := &countingRenderer{delegate: installpack.NewRenderer()}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry,
				WithAgentContentStore(newFakeContentStore("owner", "agent-one")),
				WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", tc.registryLocalID)),
				WithActorBindingReader(binding),
				WithSoulBindingIntegrationBearer("binding-secret"),
				WithDownloadGrantIssuer(issuer),
				WithRenderer(renderer),
				WithInstanceEndpoint(testInstanceEndpoint),
			); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			result, err := registry.Call(
				operatorToolContext("owner", []string{"write"}),
				ToolAgentLocalInstallPlan,
				json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`),
			)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if binding.calls != 1 || binding.agentID != "agent-one" || binding.bearer != "binding-secret" {
				t.Fatalf("binding read = calls:%d agent:%q bearer:%q", binding.calls, binding.agentID, binding.bearer)
			}
			if !tc.wantError {
				if result == nil || result.IsError || renderer.calls != 1 || issuer.calls != 1 {
					t.Fatalf("agreement result=%+v render_calls=%d issuer_calls=%d", result, renderer.calls, issuer.calls)
				}
				return
			}

			payload := toolError(t, result)
			if payload["code"] != "actor_endpoint_divergence" || statusValue(payload["status"]) != http.StatusConflict {
				t.Fatalf("divergence payload = %+v", payload)
			}
			if renderer.calls != 0 || issuer.calls != 0 {
				t.Fatalf("divergence side effects = render:%d grant:%d, want none", renderer.calls, issuer.calls)
			}
		})
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
				WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
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
		WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
		WithActorBindingReader(&fakeActorBindingReader{actorUsername: "agent-one"}),
		WithSoulBindingIntegrationBearer("binding-secret"),
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
		WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
		WithActorBindingReader(&fakeActorBindingReader{actorUsername: "agent-one"}),
		WithSoulBindingIntegrationBearer("binding-secret"),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
		WithRateLimiter(NewInMemoryGrantMintLimiter(1, time.Minute)),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("owner", []string{"write"})
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

func TestBuildPackInputRequiresPublishedSoulAndNamesPublishStep(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
	}{
		{name: "published passes", state: string(agentcontent.LifecycleStatePublished)},
		{name: "draft rejected", state: string(agentcontent.LifecycleStateDraft)},
		{name: "archived rejected", state: string(agentcontent.LifecycleStateArchived)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeContentStore("owner", "agent-one")
			key := key("owner", "agent-one", agentcontent.ContentTypeAgentSoul)
			store.records[key].LifecycleState = agentcontent.LifecycleState(tc.state)
			if store.records[key].Document != nil {
				store.records[key].Document.LifecycleState = agentcontent.LifecycleState(tc.state)
			}
			pack, err := BuildPackInput(context.Background(), PackInputRequest{
				ContentStore:     store,
				InstanceEndpoint: testInstanceEndpoint,
				Namespace:        "equaltoai",
				Account:          "owner",
				AgentID:          "agent-one",
				Actor:            "agent-one",
				Client:           "codex",
			})
			if tc.state == string(agentcontent.LifecycleStatePublished) {
				if err != nil || pack == nil {
					t.Fatalf("BuildPackInput(published) = %+v/%v, want pass", pack, err)
				}
				return
			}
			if pack != nil || !errors.Is(err, ErrAgentSoulPublicationRequired) {
				t.Fatalf("BuildPackInput(%s) = %+v/%v, want typed publication error", tc.state, pack, err)
			}
			var publicationErr *AgentSoulPublicationRequiredError
			if !errors.As(err, &publicationErr) ||
				publicationErr.LifecycleState != tc.state ||
				publicationErr.PublishTool != "agent_soul_publish" ||
				!strings.Contains(err.Error(), "call agent_soul_publish") {
				t.Fatalf("publication error = %#v / %v", publicationErr, err)
			}
		})
	}
}

func TestBuildPackInputNamesMissingRecordAndExactFixTool(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType agentcontent.ContentType
		fixTool     string
		nextTool    string
	}{
		{
			name:        "agent_soul",
			contentType: agentcontent.ContentTypeAgentSoul,
			fixTool:     "agent_soul_upsert",
			nextTool:    "agent_soul_publish",
		},
		{
			name:        "agent_instructions",
			contentType: agentcontent.ContentTypeAgentInstructions,
			fixTool:     "agent_instructions_upsert",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeContentStore("owner", "agent-one")
			delete(store.records, key("owner", "agent-one", tc.contentType))
			pack, err := BuildPackInput(context.Background(), PackInputRequest{
				ContentStore:     store,
				InstanceEndpoint: testInstanceEndpoint,
				Namespace:        "equaltoai",
				Account:          "owner",
				AgentID:          "agent-one",
				Actor:            "prototype-11",
				Client:           "codex",
			})
			if pack != nil || !errors.Is(err, ErrAgentContentMissing) {
				t.Fatalf("BuildPackInput(%s missing) = %+v/%v, want typed missing error", tc.contentType, pack, err)
			}
			var missingErr *AgentContentMissingError
			if !errors.As(err, &missingErr) ||
				missingErr.ContentType != tc.contentType ||
				missingErr.FixTool != tc.fixTool ||
				missingErr.NextTool != tc.nextTool {
				t.Fatalf("missing error = %#v / %v", missingErr, err)
			}
			for _, required := range []string{string(tc.contentType), tc.fixTool, tc.nextTool} {
				if required != "" && !strings.Contains(err.Error(), required) {
					t.Fatalf("missing error does not name %q: %v", required, err)
				}
			}
		})
	}
}

func TestBuildPackInputNeverDerivesOAuthActorFromRegistryAgentID(t *testing.T) {
	const registryAgentID = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	store := newFakeContentStore("owner", registryAgentID)
	pack, err := BuildPackInput(context.Background(), PackInputRequest{
		ContentStore:     store,
		InstanceEndpoint: testInstanceEndpoint,
		Namespace:        "equaltoai",
		Account:          "owner",
		AgentID:          registryAgentID,
		Client:           "codex",
	})
	if pack != nil || err == nil || !strings.Contains(err.Error(), "actor local_id is required") {
		t.Fatalf("BuildPackInput without local_id actor = %+v/%v, want fail-closed identifier distinction", pack, err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("BuildPackInput read content before local_id validation: %v", store.calls)
	}
}

func TestAgentLocalInstallPlanMapsUnpublishedSoulToTypedPublishError(t *testing.T) {
	for _, state := range []string{
		string(agentcontent.LifecycleStateDraft),
		string(agentcontent.LifecycleStateArchived),
	} {
		t.Run(state, func(t *testing.T) {
			store := newFakeContentStore("owner", "agent-one")
			soulKey := key("owner", "agent-one", agentcontent.ContentTypeAgentSoul)
			store.records[soulKey].LifecycleState = agentcontent.LifecycleState(state)
			issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry,
				WithAgentContentStore(store),
				WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
				WithDownloadGrantIssuer(issuer),
				WithInstanceEndpoint(testInstanceEndpoint),
				WithRateLimiter(NewInMemoryGrantMintLimiter(10, time.Minute)),
			); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			result, err := registry.Call(operatorToolContext("owner", []string{"write"}), ToolAgentLocalInstallPlan, json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			payload := toolError(t, result)
			if payload["code"] != "agent_soul_publish_required" || statusValue(payload["status"]) != http.StatusConflict {
				t.Fatalf("publish gate payload = %+v", payload)
			}
			details, _ := payload["details"].(map[string]any)
			if details["lifecycle_state"] != state || details["publish_tool"] != "agent_soul_publish" {
				t.Fatalf("publish gate details = %+v", details)
			}
			if issuer.calls != 0 {
				t.Fatalf("grant issuer calls = %d, want 0 before publication", issuer.calls)
			}
		})
	}
}

func TestAgentLocalInstallPlanMapsMissingContentToExact404Repair(t *testing.T) {
	for _, tc := range []struct {
		contentType agentcontent.ContentType
		fixTool     string
		nextTool    string
	}{
		{
			contentType: agentcontent.ContentTypeAgentSoul,
			fixTool:     "agent_soul_upsert",
			nextTool:    "agent_soul_publish",
		},
		{
			contentType: agentcontent.ContentTypeAgentInstructions,
			fixTool:     "agent_instructions_upsert",
		},
	} {
		t.Run(string(tc.contentType), func(t *testing.T) {
			store := newFakeContentStore("owner", "agent-one")
			delete(store.records, key("owner", "agent-one", tc.contentType))
			issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
			registry := mcpruntime.NewToolRegistry()
			if err := RegisterTools(registry,
				WithAgentContentStore(store),
				WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
				WithDownloadGrantIssuer(issuer),
				WithInstanceEndpoint(testInstanceEndpoint),
			); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			result, err := registry.Call(
				operatorToolContext("owner", []string{"write"}),
				ToolAgentLocalInstallPlan,
				json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`),
			)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			payload := toolError(t, result)
			if payload["code"] != "not_found" || statusValue(payload["status"]) != http.StatusNotFound {
				t.Fatalf("missing content payload = %+v", payload)
			}
			details, _ := payload["details"].(map[string]any)
			if details["content_type"] != string(tc.contentType) ||
				details["fix_tool"] != tc.fixTool ||
				details["next_tool"] != tc.nextTool {
				t.Fatalf("missing content details = %+v", details)
			}
			message, _ := payload["message"].(string)
			for _, required := range []string{string(tc.contentType), tc.fixTool, tc.nextTool} {
				if required != "" && !strings.Contains(message, required) {
					t.Fatalf("missing content message does not name %q: %q", required, message)
				}
			}
			if issuer.calls != 0 {
				t.Fatalf("missing content minted %d grants, want 0", issuer.calls)
			}
		})
	}
}

func TestUnpublishedSoulDoesNotConsumeGrantMintRateLimit(t *testing.T) {
	store := newFakeContentStore("owner", "agent-one")
	store.records[key("owner", "agent-one", agentcontent.ContentTypeAgentSoul)].LifecycleState = agentcontent.LifecycleStateDraft
	limiter := NewInMemoryGrantMintLimiter(1, time.Minute)
	issuer := &fakeGrantIssuer{expiresAt: time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry,
		WithAgentContentStore(store),
		WithAgentRegistryStore(newFakeAgentRegistryStore("owner", "agent-one", "agent-one")),
		WithDownloadGrantIssuer(issuer),
		WithInstanceEndpoint(testInstanceEndpoint),
		WithRateLimiter(limiter),
	); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		result, err := registry.Call(
			operatorToolContext("owner", []string{"write"}),
			ToolAgentLocalInstallPlan,
			json.RawMessage(`{"agent_id":"agent-one","client":"codex"}`),
		)
		if err != nil {
			t.Fatalf("Call(attempt %d): %v", attempt, err)
		}
		payload := toolError(t, result)
		if payload["code"] != "agent_soul_publish_required" {
			t.Fatalf("attempt %d payload = %+v, want typed publication gate", attempt, payload)
		}
	}
	if got := len(limiter.hits["owner"]); got != 0 {
		t.Fatalf("unpublished calls consumed %d grant-mint rate slots, want 0", got)
	}
	if issuer.calls != 0 {
		t.Fatalf("grant issuer calls = %d, want 0", issuer.calls)
	}
}

func TestBuildPackInputRendersTypedFiveBodiesElseCanonicalBody(t *testing.T) {
	store := newFakeContentStore("owner", "agent-one")
	soulKey := key("owner", "agent-one", agentcontent.ContentTypeAgentSoul)
	soul := store.records[soulKey]
	soul.Document.Body = "body fallback must not replace typed structure"
	soul.Content = soul.Document.Body
	soul.Document.Structure = &agentcontent.SoulStructure{FiveBodies: &agentcontent.FiveBodies{
		Identity:   &agentcontent.DeclarationSection{Summary: "typed identity"},
		Philosophy: &agentcontent.DeclarationSection{Summary: "typed philosophy"},
		Discipline: &agentcontent.DeclarationSection{Summary: "typed discipline"},
		Boundaries: &agentcontent.DeclarationSection{Summary: "typed boundaries"},
		Soul: &agentcontent.SoulDeclarationSection{
			Summary: "typed soul",
			Refusals: []agentcontent.Refusal{{
				Bypass:          "render the body over typed truth",
				Invariant:       "typed structure is selected when present",
				ClosestSafePath: "render the five-body overlay",
			}},
		},
	}}
	pack, err := BuildPackInput(context.Background(), PackInputRequest{
		ContentStore:     store,
		InstanceEndpoint: testInstanceEndpoint,
		Namespace:        "equaltoai",
		Account:          "owner",
		AgentID:          "agent-one",
		Actor:            "agent-one",
		Client:           "codex",
	})
	if err != nil {
		t.Fatalf("BuildPackInput(structured) error = %v", err)
	}
	if !strings.Contains(pack.RenderRequest.AgentSoul, "## Identity\n\ntyped identity") ||
		strings.Contains(pack.RenderRequest.AgentSoul, "body fallback must not replace typed structure") {
		t.Fatalf("structured AgentSoul render = %q", pack.RenderRequest.AgentSoul)
	}

	soul.Document.Structure = nil
	pack, err = BuildPackInput(context.Background(), PackInputRequest{
		ContentStore: store, InstanceEndpoint: testInstanceEndpoint, Namespace: "equaltoai",
		Account: "owner", AgentID: "agent-one", Actor: "agent-one", Client: "codex",
	})
	if err != nil {
		t.Fatalf("BuildPackInput(body-only) error = %v", err)
	}
	if pack.RenderRequest.AgentSoul != soul.Document.Body {
		t.Fatalf("body-only AgentSoul = %q, want exact body %q", pack.RenderRequest.AgentSoul, soul.Document.Body)
	}
}

func TestPackIDEndpointAndLocalActorValidation(t *testing.T) {
	packID, err := PackIDForAgent("https://lesser.example/users/Ptah_Agent", "claude-code")
	if err != nil {
		t.Fatalf("PackIDForAgent: %v", err)
	}
	agentID, err := AgentIDFromPackID(packID)
	if err != nil || agentID != "https://lesser.example/users/Ptah_Agent" {
		t.Fatalf("AgentIDFromPackID = %q/%v", agentID, err)
	}
	actor, err := actorFromLocalID("Ptah_Agent")
	if err != nil || actor != "Ptah_Agent" {
		t.Fatalf("actorFromLocalID = %q/%v, want exact registry local_id", actor, err)
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

type fakeAgentRegistryStore struct {
	records map[string]*agentregistry.Agent
}

type fakeActorBindingReader struct {
	calls         int
	bearer        string
	agentID       string
	actorUsername string
	err           error
}

func (f *fakeActorBindingReader) GetSoulBinding(_ context.Context, bearer string, agentID string, _ string) (*lesserapi.SoulBindingResponse, error) {
	f.calls++
	f.bearer = bearer
	f.agentID = agentID
	if f.err != nil {
		return nil, f.err
	}
	return &lesserapi.SoulBindingResponse{
		Status:       "bound",
		BindingState: "bound",
		Agent: lesserapi.SoulBindingAgent{
			AgentID: agentID,
		},
		Binding: lesserapi.SoulAgentBinding{
			AgentUsername: f.actorUsername,
		},
	}, nil
}

type countingRenderer struct {
	calls    int
	delegate Renderer
}

func (r *countingRenderer) Render(ctx context.Context, req installpack.Request) (*installpack.Pack, error) {
	r.calls++
	return r.delegate.Render(ctx, req)
}

func newFakeAgentRegistryStore(account, agentID, localID string) *fakeAgentRegistryStore {
	return &fakeAgentRegistryStore{records: map[string]*agentregistry.Agent{
		strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.TrimSpace(agentID): {
			Account: strings.ToLower(strings.TrimSpace(account)),
			AgentID: strings.TrimSpace(agentID),
			LocalID: strings.TrimSpace(localID),
		},
	}}
}

func (f *fakeAgentRegistryStore) Get(_ context.Context, account string, agentID string) (*agentregistry.Agent, error) {
	if f == nil {
		return nil, agentregistry.ErrAgentNotFound
	}
	record := f.records[strings.ToLower(strings.TrimSpace(account))+"\x00"+strings.TrimSpace(agentID)]
	if record == nil {
		return nil, agentregistry.ErrAgentNotFound
	}
	clone := *record
	return &clone, nil
}

func newFakeContentStore(account, agentID string) *fakeContentStore {
	createdAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	return &fakeContentStore{records: map[string]*agentcontent.Record{
		key(account, agentID, agentcontent.ContentTypeAgentSoul): {
			Account:            account,
			AgentID:            agentID,
			Type:               agentcontent.ContentTypeAgentSoul,
			Content:            "published soul body",
			Version:            3,
			SoulVersion:        2,
			LifecycleState:     agentcontent.LifecycleStatePublished,
			CreatedAt:          createdAt,
			UpdatedAt:          updatedAt,
			UpdatedBySubjectID: "subject-owner",
			Document: &agentcontent.SoulDocument{
				SchemaVersion:      agentcontent.SoulDocumentSchemaVersion,
				AgentID:            agentID,
				Body:               "published soul body",
				SoulVersion:        2,
				LifecycleState:     agentcontent.LifecycleStatePublished,
				UpdatedBySubjectID: "subject-owner",
				CreatedAt:          createdAt.Format(time.RFC3339Nano),
				UpdatedAt:          updatedAt.Format(time.RFC3339Nano),
				Version:            3,
			},
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

func operatorToolContext(username string, scopes []string) context.Context {
	ctx := toolContext(username, scopes)
	principal := auth.PrincipalFromToolContext(ctx)
	if principal != nil && principal.Claims != nil {
		principal.Claims.ClientClass = "operator"
	}
	return ctx
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
