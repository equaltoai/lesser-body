package instanceapp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/equaltoai/lesser-body/internal/instancex402"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/ptahserver"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
	"github.com/theory-cloud/tabletheory/v2"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestInstancePlaneMCP_InitializeAndToolsList(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name       string
		path       string
		serverName string
		wantTools  []string
	}{
		{name: "ptah", path: "/instance/ptah/mcp", serverName: "lesser-body-instance-ptah", wantTools: []string{"agent_bind_soul", "agent_get", "agent_list", "agent_soul_get", "agent_soul_upsert", "agent_soul_archive", "agent_instructions_get", "agent_instructions_upsert", "agent_instructions_archive", "agent_genesis_skill_get", "agent_genesis_begin", "agent_genesis_list", "agent_genesis_read", "agent_genesis_advance", "agent_genesis_recover", "agent_genesis_complete", "agent_genesis_finalize_preflight", "agent_genesis_finalize"}},
		{name: "ba", path: "/instance/ba/mcp", serverName: "lesser-body-instance-ba", wantTools: []string{baserver.ToolAgentLocalInstallPlan}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath(tc.path))

			initResp := invokeMCP(t, env, app, tc.path, bearerHeaders(token), &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if initResp.Status != 200 {
				t.Fatalf("initialize status = %d, body = %s", initResp.Status, string(initResp.Body))
			}

			var initBody struct {
				Result struct {
					ServerInfo struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"serverInfo"`
					Capabilities map[string]any `json:"capabilities"`
				} `json:"result"`
			}
			if err := json.Unmarshal(initResp.Body, &initBody); err != nil {
				t.Fatalf("decode initialize response: %v", err)
			}
			if initBody.Result.ServerInfo.Name != tc.serverName {
				t.Fatalf("server name = %q, want %q", initBody.Result.ServerInfo.Name, tc.serverName)
			}
			if initBody.Result.ServerInfo.Version != "dev" {
				t.Fatalf("server version = %q, want dev", initBody.Result.ServerInfo.Version)
			}
			if tc.name == "ptah" {
				for _, capability := range []string{"tools", "resources", "prompts"} {
					if _, ok := initBody.Result.Capabilities[capability]; !ok {
						t.Fatalf("ptah initialize missing %s capability: %+v", capability, initBody.Result.Capabilities)
					}
				}
			} else if _, ok := initBody.Result.Capabilities["resources"]; ok {
				t.Fatalf("%s should not advertise resources: %+v", tc.name, initBody.Result.Capabilities)
			}

			sessionID := firstHeader(initResp.Headers, "mcp-session-id")
			if sessionID == "" {
				t.Fatalf("initialize response did not include mcp-session-id")
			}

			headers := bearerHeaders(token)
			headers["mcp-session-id"] = []string{sessionID}
			headers["mcp-protocol-version"] = []string{"2025-11-25"}
			listResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      2,
				Method:  "tools/list",
			})
			if listResp.Status != 200 {
				t.Fatalf("tools/list status = %d, body = %s", listResp.Status, string(listResp.Body))
			}

			var listBody struct {
				Result struct {
					Tools []mcpruntime.ToolDef `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal(listResp.Body, &listBody); err != nil {
				t.Fatalf("decode tools/list response: %v", err)
			}
			if got := toolNames(listBody.Result.Tools); !reflect.DeepEqual(got, tc.wantTools) {
				t.Fatalf("tools/list names = %v, want %v", got, tc.wantTools)
			}
			if tc.name == "ba" {
				def := listBody.Result.Tools[0]
				if def.Name != baserver.ToolAgentLocalInstallPlan || def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint || def.Annotations.DestructiveHint == nil || *def.Annotations.DestructiveHint || def.Annotations.IdempotentHint == nil || *def.Annotations.IdempotentHint {
					t.Fatalf("agent_local_install_plan annotations not write/additive: %+v", def)
				}
			}
			if tc.name == "ptah" {
				for _, tool := range listBody.Result.Tools {
					if tool.Name == "agent_create" {
						t.Fatalf("removed agent_create tool is still advertised: %+v", tool)
					}
				}
				def := listBody.Result.Tools[0]
				if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || *def.Annotations.ReadOnlyHint {
					t.Fatalf("agent_bind_soul annotations not write/additive: %+v", def.Annotations)
				}
				getDef := listBody.Result.Tools[1]
				if getDef.Name != "agent_get" || getDef.Annotations == nil || getDef.Annotations.ReadOnlyHint == nil || !*getDef.Annotations.ReadOnlyHint {
					t.Fatalf("agent_get annotations not read-only: %+v", getDef)
				}
				listDef := listBody.Result.Tools[2]
				if listDef.Name != "agent_list" || listDef.Annotations == nil || listDef.Annotations.ReadOnlyHint == nil || !*listDef.Annotations.ReadOnlyHint {
					t.Fatalf("agent_list annotations not read-only: %+v", listDef)
				}
				soulGetDef := listBody.Result.Tools[3]
				if soulGetDef.Name != "agent_soul_get" || soulGetDef.Annotations == nil || soulGetDef.Annotations.ReadOnlyHint == nil || !*soulGetDef.Annotations.ReadOnlyHint || !strings.Contains(soulGetDef.Description, "provisional_agent_soul_schema_pending_lesser_soul_s1") {
					t.Fatalf("agent_soul_get annotations/description invalid: %+v", soulGetDef)
				}
				soulUpsertDef := listBody.Result.Tools[4]
				if soulUpsertDef.Name != "agent_soul_upsert" || soulUpsertDef.Annotations == nil || soulUpsertDef.Annotations.ReadOnlyHint == nil || *soulUpsertDef.Annotations.ReadOnlyHint || soulUpsertDef.Annotations.IdempotentHint == nil || *soulUpsertDef.Annotations.IdempotentHint {
					t.Fatalf("agent_soul_upsert annotations invalid: %+v", soulUpsertDef)
				}
				soulArchiveDef := listBody.Result.Tools[5]
				if soulArchiveDef.Name != "agent_soul_archive" || soulArchiveDef.Annotations == nil || soulArchiveDef.Annotations.ReadOnlyHint == nil || *soulArchiveDef.Annotations.ReadOnlyHint || soulArchiveDef.Annotations.IdempotentHint == nil || !*soulArchiveDef.Annotations.IdempotentHint {
					t.Fatalf("agent_soul_archive annotations invalid: %+v", soulArchiveDef)
				}
				instructionsGetDef := listBody.Result.Tools[6]
				if instructionsGetDef.Name != "agent_instructions_get" || instructionsGetDef.Annotations == nil || instructionsGetDef.Annotations.ReadOnlyHint == nil || !*instructionsGetDef.Annotations.ReadOnlyHint || strings.Contains(instructionsGetDef.Description, "provisional_agent_soul_schema_pending_lesser_soul_s1") {
					t.Fatalf("agent_instructions_get annotations/description invalid: %+v", instructionsGetDef)
				}
				instructionsUpsertDef := listBody.Result.Tools[7]
				if instructionsUpsertDef.Name != "agent_instructions_upsert" || instructionsUpsertDef.Annotations == nil || instructionsUpsertDef.Annotations.ReadOnlyHint == nil || *instructionsUpsertDef.Annotations.ReadOnlyHint || instructionsUpsertDef.Annotations.IdempotentHint == nil || *instructionsUpsertDef.Annotations.IdempotentHint {
					t.Fatalf("agent_instructions_upsert annotations invalid: %+v", instructionsUpsertDef)
				}
				instructionsArchiveDef := listBody.Result.Tools[8]
				if instructionsArchiveDef.Name != "agent_instructions_archive" || instructionsArchiveDef.Annotations == nil || instructionsArchiveDef.Annotations.ReadOnlyHint == nil || *instructionsArchiveDef.Annotations.ReadOnlyHint || instructionsArchiveDef.Annotations.IdempotentHint == nil || !*instructionsArchiveDef.Annotations.IdempotentHint {
					t.Fatalf("agent_instructions_archive annotations invalid: %+v", instructionsArchiveDef)
				}
				genesisSkillDef := listBody.Result.Tools[9]
				if genesisSkillDef.Name != "agent_genesis_skill_get" || genesisSkillDef.Annotations == nil || genesisSkillDef.Annotations.ReadOnlyHint == nil || !*genesisSkillDef.Annotations.ReadOnlyHint || !strings.Contains(genesisSkillDef.Description, "no local installation") || !strings.Contains(string(genesisSkillDef.OutputSchema), "bundle_id") {
					t.Fatalf("agent_genesis_skill_get definition invalid: %+v", genesisSkillDef)
				}
				genesisListDef := listBody.Result.Tools[11]
				if genesisListDef.Name != "agent_genesis_list" || genesisListDef.Annotations == nil || genesisListDef.Annotations.ReadOnlyHint == nil || !*genesisListDef.Annotations.ReadOnlyHint || !strings.Contains(genesisListDef.Description, "producer_contract_missing") || !strings.Contains(string(genesisListDef.OutputSchema), "not_available") {
					t.Fatalf("agent_genesis_list definition invalid: %+v", genesisListDef)
				}

				resourcesResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      3,
					Method:  "resources/list",
				})
				if resourcesResp.Status != 200 {
					t.Fatalf("resources/list status = %d, body = %s", resourcesResp.Status, string(resourcesResp.Body))
				}
				var resourcesRPC mcpruntime.Response
				if err := json.Unmarshal(resourcesResp.Body, &resourcesRPC); err != nil {
					t.Fatalf("decode resources/list response: %v", err)
				}
				if resourcesRPC.Error != nil {
					t.Fatalf("resources/list error: %+v", resourcesRPC.Error)
				}
				var resourcesOut struct {
					Resources []mcpruntime.ResourceDef `json:"resources"`
				}
				{
					b, _ := json.Marshal(resourcesRPC.Result)
					_ = json.Unmarshal(b, &resourcesOut)
				}
				haveResource := map[string]bool{}
				for _, res := range resourcesOut.Resources {
					haveResource[res.Name] = true
				}
				for _, name := range []string{"soul-schema-v2", "genesis-interview-guide", "agent-side-genesis-playbook", "genesis-rubric", "genesis-operator-skill"} {
					if !haveResource[name] {
						t.Fatalf("resources/list missing %s: %+v", name, resourcesOut.Resources)
					}
				}

				readParams, _ := json.Marshal(map[string]any{"uri": "ptah://genesis/soul-schema-v2"})
				readResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      4,
					Method:  "resources/read",
					Params:  readParams,
				})
				if readResp.Status != 200 {
					t.Fatalf("resources/read status = %d, body = %s", readResp.Status, string(readResp.Body))
				}
				var readRPC mcpruntime.Response
				if err := json.Unmarshal(readResp.Body, &readRPC); err != nil {
					t.Fatalf("decode resources/read response: %v", err)
				}
				if readRPC.Error != nil {
					t.Fatalf("resources/read error: %+v", readRPC.Error)
				}
				readEncoded, _ := json.Marshal(readRPC.Result)
				if !strings.Contains(string(readEncoded), "soul-five-body-schema.v2") || !strings.Contains(string(readEncoded), "e70b1835624724056a099bc96f4f931d0d348cd2") {
					t.Fatalf("resources/read did not return Host contract metadata: %s", string(readEncoded))
				}

				promptsResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      5,
					Method:  "prompts/list",
				})
				if promptsResp.Status != 200 {
					t.Fatalf("prompts/list status = %d, body = %s", promptsResp.Status, string(promptsResp.Body))
				}
				var promptsRPC mcpruntime.Response
				if err := json.Unmarshal(promptsResp.Body, &promptsRPC); err != nil {
					t.Fatalf("decode prompts/list response: %v", err)
				}
				if promptsRPC.Error != nil {
					t.Fatalf("prompts/list error: %+v", promptsRPC.Error)
				}
				var promptsOut struct {
					Prompts []mcpruntime.PromptDef `json:"prompts"`
				}
				{
					b, _ := json.Marshal(promptsRPC.Result)
					_ = json.Unmarshal(b, &promptsOut)
				}
				if got := promptNames(promptsOut.Prompts); !reflect.DeepEqual(got, []string{"draft-genesis-turn", "review-soul-draft"}) {
					t.Fatalf("prompts/list names = %v", got)
				}

				getParams, _ := json.Marshal(map[string]any{
					"name":      "draft-genesis-turn",
					"arguments": map[string]any{"phase": "soul", "owner_intent": "finish refusals"},
				})
				getResp := invokeMCP(t, env, app, tc.path, headers, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      6,
					Method:  "prompts/get",
					Params:  getParams,
				})
				if getResp.Status != 200 {
					t.Fatalf("prompts/get status = %d, body = %s", getResp.Status, string(getResp.Body))
				}
				var getRPC mcpruntime.Response
				if err := json.Unmarshal(getResp.Body, &getRPC); err != nil {
					t.Fatalf("decode prompts/get response: %v", err)
				}
				if getRPC.Error != nil {
					t.Fatalf("prompts/get error: %+v", getRPC.Error)
				}
				getEncoded, _ := json.Marshal(getRPC.Result)
				if !strings.Contains(string(getEncoded), "identity, philosophy, discipline, boundaries, and soul") || !strings.Contains(string(getEncoded), "Do you affirm this declaration") {
					t.Fatalf("prompts/get missing five-body guidance: %s", string(getEncoded))
				}
			}
		})
	}
}

func TestInstancePlaneMCP_AgentBindSoulUsesDedicatedBearerAndReplays(t *testing.T) {
	requests := 0
	var captured []lesserapi.SoulBindingRequest
	var capturedAuth []string
	var capturedIdempotency []string
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/souls/bindings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		capturedAuth = append(capturedAuth, r.Header.Get("Authorization"))
		capturedIdempotency = append(capturedIdempotency, r.Header.Get("Idempotency-Key"))
		var body lesserapi.SoulBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Lesser request: %v", err)
		}
		captured = append(captured, body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceSoulBindingResponse(requests > 1)))
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	userToken := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", userToken)

	args := map[string]any{
		"soul_agent_id":        "agent-0xabc",
		"idempotency_key":      "bind-key-1",
		"host_registration_id": "hreg_123",
		"host_conversation_id": "hconv_456",
		"principal_address":    "0x2222222222222222222222222222222222222222",
		"evidence": map[string]any{
			"host_request_id":  "hreq_789",
			"declaration_hash": "sha256:abc",
			"issued_at":        "2026-07-14T16:20:00Z",
		},
	}
	first := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", args)
	if first.Result == nil || first.Result.IsError {
		t.Fatalf("first call result = %+v error = %+v", first.Result, first.Error)
	}
	second := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", args)
	if second.Result == nil || second.Result.IsError {
		t.Fatalf("second call result = %+v error = %+v", second.Result, second.Error)
	}

	if requests != 2 {
		t.Fatalf("Lesser requests = %d, want 2", requests)
	}
	for i, authz := range capturedAuth {
		if authz != "Bearer integration-secret" {
			t.Fatalf("request %d Authorization = %q, want dedicated integration bearer", i+1, authz)
		}
		if strings.Contains(authz, userToken) {
			t.Fatalf("request %d forwarded user OAuth token", i+1)
		}
	}
	for i, key := range capturedIdempotency {
		if key != "bind-key-1" {
			t.Fatalf("request %d Idempotency-Key = %q", i+1, key)
		}
	}
	for i, body := range captured {
		if body.ActorUsername != "agent1" || body.SoulAgentID != "agent-0xabc" || body.BodyActorID != "body://ptah/agent1" {
			t.Fatalf("request %d required/body fields = %+v", i+1, body)
		}
		if body.AuthorityModel != lesserapi.SoulAuthorityModelInstanceTrust || body.AnchorState != lesserapi.SoulAnchorStateHostedOffchain || body.OperationalBinding != lesserapi.SoulOperationalBindingHostedBound {
			t.Fatalf("request %d hints = %+v", i+1, body)
		}
		if body.Evidence.Source != "ptah" || body.Evidence.HostRequestID != "hreq_789" {
			t.Fatalf("request %d evidence = %+v", i+1, body.Evidence)
		}
	}
	if replayed := toolResultData(t, second.Result)["replayed"]; replayed != true {
		t.Fatalf("second replayed = %#v, want true", replayed)
	}
}

func TestInstancePlaneMCP_AgentBindSoulRequiresWriteScope(t *testing.T) {
	requests := 0
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)

	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", map[string]any{
		"soul_agent_id":   "agent-0xabc",
		"idempotency_key": "bind-key-1",
	})
	if out.Result == nil || !out.Result.IsError {
		t.Fatalf("result IsError = false: result=%+v error=%+v", out.Result, out.Error)
	}
	errPayload := toolResultError(t, out.Result)
	if errPayload["code"] != "insufficient_scope" || errPayload["status"] != float64(403) {
		t.Fatalf("error payload = %+v", errPayload)
	}
	if requests != 0 {
		t.Fatalf("Lesser requests = %d, want 0", requests)
	}
}

func TestInstancePlaneMCP_AgentGetAndListReadRegistry(t *testing.T) {
	registryStore := newInstanceAgentRegistryStore(t)
	for _, in := range []agentregistry.CreateInput{
		{Account: "agent1", AgentID: "agent-001"},
		{Account: "agent1", AgentID: "agent-002"},
		{Account: "agent2", AgentID: "agent-000"},
	} {
		if _, err := registryStore.Create(context.Background(), in); err != nil {
			t.Fatalf("Create(%+v): %v", in, err)
		}
	}
	resetRegistry := ptahserver.SetAgentRegistryFactoryForTests(func() (ptahserver.AgentRegistry, error) {
		return registryStore, nil
	})
	t.Cleanup(resetRegistry)
	resetLive := ptahserver.SetAgentLiveClientFactoryForTests(func() (ptahserver.AgentLiveClient, error) {
		return &instanceAgentLiveClient{}, nil
	})
	t.Cleanup(resetLive)

	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	userToken := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", userToken)

	getOut := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_get", map[string]any{
		"agent_id": "agent-001",
	})
	if getOut.Result == nil || getOut.Result.IsError {
		t.Fatalf("agent_get result = %+v error = %+v", getOut.Result, getOut.Error)
	}
	getData := toolResultData(t, getOut.Result)
	registrySummary, _ := getData["registry"].(map[string]any)
	if registrySummary["account"] != "agent1" || registrySummary["agent_id"] != "agent-001" {
		t.Fatalf("agent_get registry summary = %+v", registrySummary)
	}
	contentVersion, _ := getData["content_version"].(map[string]any)
	if contentVersion["status"] != "not_available" {
		t.Fatalf("content_version = %+v, want not_available", contentVersion)
	}

	missingOut := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_get", map[string]any{
		"agent_id": "agent-000",
	})
	if missingOut.Result == nil || !missingOut.Result.IsError {
		t.Fatalf("cross-account agent_get result = %+v error = %+v", missingOut.Result, missingOut.Error)
	}
	missingErr := toolResultError(t, missingOut.Result)
	if missingErr["code"] != "not_found" || missingErr["status"] != float64(404) {
		t.Fatalf("cross-account error = %+v, want not_found", missingErr)
	}
	if encoded, _ := json.Marshal(missingErr); strings.Contains(string(encoded), "agent-000") || strings.Contains(string(encoded), "agent2") {
		t.Fatalf("cross-account not_found leaked registry detail: %s", string(encoded))
	}

	firstPage := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_list", map[string]any{
		"limit": 1,
	})
	if firstPage.Result == nil || firstPage.Result.IsError {
		t.Fatalf("first agent_list result = %+v error = %+v", firstPage.Result, firstPage.Error)
	}
	firstData := toolResultData(t, firstPage.Result)
	firstAgents, _ := firstData["agents"].([]any)
	if len(firstAgents) != 1 {
		t.Fatalf("first agents = %+v, want one", firstData["agents"])
	}
	firstItem, _ := firstAgents[0].(map[string]any)
	firstRegistry, _ := firstItem["registry"].(map[string]any)
	if firstRegistry["agent_id"] != "agent-001" || firstRegistry["account"] != "agent1" {
		t.Fatalf("first registry = %+v, want agent-001/agent1", firstRegistry)
	}
	firstPagination, _ := firstData["pagination"].(map[string]any)
	nextCursor, _ := firstPagination["next_cursor"].(string)
	if nextCursor == "" || firstPagination["has_more"] != true || firstPagination["count"] != float64(1) {
		t.Fatalf("first pagination = %+v, want cursor/has_more/count", firstPagination)
	}

	secondPage := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_list", map[string]any{
		"limit":  10,
		"cursor": nextCursor,
	})
	if secondPage.Result == nil || secondPage.Result.IsError {
		t.Fatalf("second agent_list result = %+v error = %+v", secondPage.Result, secondPage.Error)
	}
	secondData := toolResultData(t, secondPage.Result)
	secondAgents, _ := secondData["agents"].([]any)
	if len(secondAgents) != 1 {
		t.Fatalf("second agents = %+v, want one", secondData["agents"])
	}
	secondItem, _ := secondAgents[0].(map[string]any)
	secondRegistry, _ := secondItem["registry"].(map[string]any)
	if secondRegistry["agent_id"] != "agent-002" || secondRegistry["account"] != "agent1" {
		t.Fatalf("second registry = %+v, want agent-002/agent1", secondRegistry)
	}
	secondPagination, _ := secondData["pagination"].(map[string]any)
	if secondPagination["has_more"] != false || secondPagination["next_cursor"] != "" || secondPagination["count"] != float64(1) {
		t.Fatalf("second pagination = %+v, want terminal page", secondPagination)
	}
}

func TestInstancePlaneMCP_AgentListFallsBackToLesserLiveAgents(t *testing.T) {
	registryStore := newInstanceAgentRegistryStore(t)
	resetRegistry := ptahserver.SetAgentRegistryFactoryForTests(func() (ptahserver.AgentRegistry, error) {
		return registryStore, nil
	})
	t.Cleanup(resetRegistry)
	live := &instanceAgentLiveClient{agents: []lesserapi.AgentDirectoryEntry{{
		Username:     "scout",
		DisplayName:  "Scout",
		AgentType:    "CUSTOM",
		AgentVersion: "1",
	}}}
	resetLive := ptahserver.SetAgentLiveClientFactoryForTests(func() (ptahserver.AgentLiveClient, error) {
		return live, nil
	})
	t.Cleanup(resetLive)

	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)
	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_list", map[string]any{})
	if out.Result == nil || out.Result.IsError {
		t.Fatalf("agent_list result = %+v error = %+v", out.Result, out.Error)
	}
	if live.calls != 1 {
		t.Fatalf("live agent calls = %d, want one", live.calls)
	}
	data := toolResultData(t, out.Result)
	agents, _ := data["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("agents = %+v, want one live agent", data["agents"])
	}
	item, _ := agents[0].(map[string]any)
	if item["source"] != "lesser_live" {
		t.Fatalf("source = %v, want lesser_live", item["source"])
	}
	if _, ok := item["registry"]; ok {
		t.Fatalf("live-only item claimed Body registry ownership: %+v", item)
	}
	liveSummary, _ := item["live_agent"].(map[string]any)
	if liveSummary["username"] != "scout" {
		t.Fatalf("live_agent = %+v, want scout", liveSummary)
	}
}

func TestInstancePlaneMCP_AgentBindSoulDoesNotMutateLocalBindingAndKaResolvesSouledFromFixture(t *testing.T) {
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceSoulBindingResponse(false)))
	}))
	defer lesser.Close()

	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	t.Setenv(ptahserver.EnvSoulBindingIntegrationBearer, "integration-secret")
	t.Setenv("LESSER_TABLE_NAME", "lesser-test")
	auth.ResetForTests()
	lesserapi.ResetForTests()

	localBindingDB := &instanceBindingDB{
		agentID:  "agent-0xabc",
		username: "agent1",
	}
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return localBindingDB, nil
	})
	t.Cleanup(soulbinding.ResetForTests)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ptah/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ptah/mcp", token)
	out := callMCPTool(t, env, app, "/instance/ptah/mcp", headers, "agent_bind_soul", map[string]any{
		"soul_agent_id":   "agent-0xabc",
		"idempotency_key": "bind-key-1",
	})
	if out.Result == nil || out.Result.IsError {
		t.Fatalf("tool result = %+v error = %+v", out.Result, out.Error)
	}

	resolved, err := soulbinding.ResolveAgentID(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("ResolveAgentID: %v", err)
	}
	if resolved != "agent-0xabc" {
		t.Fatalf("resolved soul agent = %q, want agent-0xabc", resolved)
	}
	profile := runtimepolicy.ResolveForActor(context.Background(), "agent1")
	if profile.Profile != runtimepolicy.ProfileSouled || !profile.BoundSoul || profile.SoulAgentID != "agent-0xabc" {
		t.Fatalf("runtime profile = %+v, want souled bound profile from soulbinding fixture", profile)
	}
	if localBindingDB.mutations != 0 {
		t.Fatalf("local SOUL_BODY_BINDING mutations = %d, want 0", localBindingDB.mutations)
	}
}

func TestInstancePlaneMCP_RejectsDisallowedPrincipals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		setup   func(t testing.TB) map[string][]string
		want    int
		wantErr string
	}{
		{
			name: "agent delegated OAuth token",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "test-secret")
				t.Setenv("JWT_SECRET_ARN", "")
				auth.ResetForTests()
				token := newTestTokenWithAudienceAndAgent(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"), true)
				return bearerHeaders(token)
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "agent delegated OAuth token on ba",
			path: "/instance/ba/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "test-secret")
				t.Setenv("JWT_SECRET_ARN", "")
				auth.ResetForTests()
				token := newTestTokenWithAudienceAndAgent(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ba/mcp"), true)
				return bearerHeaders(token)
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "legacy managed instance key despite compatibility flag",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "")
				t.Setenv("JWT_SECRET_ARN", "")
				t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
				t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
				t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
				auth.ResetForTests()
				return bearerHeaders("legacy-instance-key")
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "legacy managed instance key on ba despite compatibility flag",
			path: "/instance/ba/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "")
				t.Setenv("JWT_SECRET_ARN", "")
				t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
				t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
				t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
				auth.ResetForTests()
				return bearerHeaders("legacy-instance-key")
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
		{
			name: "instance audience mismatch",
			path: "/instance/ptah/mcp",
			setup: func(t testing.TB) map[string][]string {
				t.Helper()
				t.Setenv("JWT_SECRET", "test-secret")
				t.Setenv("JWT_SECRET_ARN", "")
				auth.ResetForTests()
				token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ba/mcp"))
				return bearerHeaders(token)
			},
			want:    403,
			wantErr: "instance_principal_not_allowed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := instanceapp.New("lesser-body-instance", "dev")
			if err != nil {
				t.Fatalf("new app: %v", err)
			}
			env := testkit.New()

			resp := invokeMCP(t, env, app, tc.path, tc.setup(t), &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if resp.Status != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", resp.Status, tc.want, string(resp.Body))
			}
			if !strings.Contains(string(resp.Body), tc.wantErr) {
				t.Fatalf("response did not include %q: %s", tc.wantErr, string(resp.Body))
			}
		})
	}
}

func TestInstancePlaneMCP_RejectsMissingInstanceAudience(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test-secret", "agent1", []string{"read"})

	resp := invokeMCP(t, env, app, "/instance/ptah/mcp", bearerHeaders(token), &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 403 {
		t.Fatalf("status = %d, want 403; body = %s", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "instance_principal_not_allowed") {
		t.Fatalf("response did not include instance_principal_not_allowed: %s", string(resp.Body))
	}
}

func TestInstancePlaneMCP_RejectsMissingHostForAudienceCheck(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"read"}, audienceForPath("/instance/ptah/mcp"))

	resp := invokeMCP(t, env, app, "/instance/ptah/mcp", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 403 {
		t.Fatalf("status = %d, want 403; body = %s", resp.Status, string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), "instance_principal_not_allowed") {
		t.Fatalf("response did not include instance_principal_not_allowed: %s", string(resp.Body))
	}
}

func TestInstancePlaneMCP_RejectsActorScopedX402GrantForInstanceTools(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	resetConsumer := instancex402.SetConsumerForTests(func(context.Context, instancex402.ConsumeRequestForTests) (instancex402.ConsumeResponseForTests, error) {
		t.Fatalf("actor-scoped x402 grant must be rejected before Host consume")
		return instancex402.ConsumeResponseForTests{}, nil
	})
	t.Cleanup(resetConsumer)

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	t.Run("agent_local_install_plan", func(t *testing.T) {
		token := newTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ba/mcp"))
		headers := initializedMCPHeaders(t, env, app, "/instance/ba/mcp", token)
		addInstanceX402Headers(headers, "scoped-grant-install", "raw-scoped-install-token", "tools.invoke", "raw-scoped-install-payment")

		out := callMCPTool(t, env, app, "/instance/ba/mcp", headers, baserver.ToolAgentLocalInstallPlan, map[string]any{
			"agent_id":       "agent-one",
			"client":         "codex",
			"actor_username": "agent1",
		})
		if reason := toolResultErrorReason(t, out.Result); reason != "x402_grant_capability_mismatch" {
			t.Fatalf("agent_local_install_plan x402 reason = %q, want capability mismatch", reason)
		}
		assertNoRawX402Leak(t, out.Result, "raw-scoped-install-token", "raw-scoped-install-payment", "host-instance-key-secret")
	})
}

func TestInstancePlaneMCP_RejectsUnauthenticatedRequests(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name    string
		headers map[string][]string
	}{
		{name: "missing bearer", headers: nil},
		{name: "x402 headers are not auth", headers: map[string][]string{
			"lesser-x402-grant-id":   {"grant-123"},
			"lesser-x402-grant":      {"grant-token"},
			"lesser-x402-capability": {"ptah.instance"},
			"payment-signature":      {"payment"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := invokeMCP(t, env, app, "/instance/ptah/mcp", tc.headers, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if resp.Status != 401 {
				t.Fatalf("status = %d, want 401; body = %s", resp.Status, string(resp.Body))
			}
		})
	}
}

func TestInstancePlaneMCP_UnknownPlaneIsNotMounted(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	auth.ResetForTests()

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test-secret", "agent1", []string{"read"})

	resp := invokeMCP(t, env, app, "/instance/ka/mcp", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != 404 {
		t.Fatalf("status = %d, want 404; body = %s", resp.Status, string(resp.Body))
	}
}

func TestInstancePlaneWellKnownProtectedResourceMetadata(t *testing.T) {
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "https://api.example.com/instance/{surface}/mcp")
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://claude.ai")

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	for _, tc := range []struct {
		name     string
		surface  string
		path     string
		resource string
	}{
		{
			name:     "ptah",
			surface:  "ptah",
			path:     "/.well-known/oauth-protected-resource/instance/ptah/mcp",
			resource: "https://api.example.com/instance/ptah/mcp",
		},
		{
			name:     "ba",
			surface:  "ba",
			path:     "/.well-known/oauth-protected-resource/instance/ba/mcp",
			resource: "https://api.example.com/instance/ba/mcp",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.Invoke(context.Background(), app, apptheory.Request{
				Method: "GET",
				Path:   tc.path,
				Headers: map[string][]string{
					"host":              {testHost},
					"x-forwarded-proto": {"https"},
					"origin":            {"https://claude.ai"},
				},
			})
			if resp.Status != 200 {
				t.Fatalf("status = %d, want 200; body = %s", resp.Status, string(resp.Body))
			}
			if got := firstHeader(resp.Headers, "content-type"); got != "application/json" {
				t.Fatalf("content-type = %q, want application/json", got)
			}
			if got := firstHeader(resp.Headers, "cache-control"); got != "public, max-age=60" {
				t.Fatalf("cache-control = %q, want public, max-age=60", got)
			}
			if got := firstHeader(resp.Headers, "access-control-allow-origin"); got != "https://claude.ai" {
				t.Fatalf("access-control-allow-origin = %q, want https://claude.ai", got)
			}

			var out struct {
				Resource               string   `json:"resource"`
				AuthorizationServers   []string `json:"authorization_servers"`
				ScopesSupported        []string `json:"scopes_supported"`
				BearerMethodsSupported []string `json:"bearer_methods_supported"`
			}
			if err := json.Unmarshal(resp.Body, &out); err != nil {
				t.Fatalf("unmarshal metadata: %v", err)
			}
			if out.Resource != tc.resource {
				t.Fatalf("resource = %q, want %q", out.Resource, tc.resource)
			}
			if want := []string{"https://api.example.com"}; !reflect.DeepEqual(out.AuthorizationServers, want) {
				t.Fatalf("authorization_servers = %#v, want %#v", out.AuthorizationServers, want)
			}
			if want := []string{"read", "write", "follow", "push"}; !reflect.DeepEqual(out.ScopesSupported, want) {
				t.Fatalf("scopes_supported = %#v, want %#v", out.ScopesSupported, want)
			}
			if strings.Contains(strings.ToLower(string(resp.Body)), "admin") || strings.Contains(string(resp.Body), "instance:") {
				t.Fatalf("instance metadata advertised non-issuable/internal scopes: %s", string(resp.Body))
			}
			if want := []string{"header"}; !reflect.DeepEqual(out.BearerMethodsSupported, want) {
				t.Fatalf("bearer_methods_supported = %#v, want %#v", out.BearerMethodsSupported, want)
			}
		})
	}
}

func TestInstancePlaneWellKnownProtectedResource_RejectsMissingConfiguredEndpoint(t *testing.T) {
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "")

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource/instance/ptah/mcp",
		Headers: map[string][]string{
			"host":              {"evil.example"},
			"x-forwarded-proto": {"https"},
		},
	})
	if resp.Status != 500 {
		t.Fatalf("status = %d, want 500; body = %s", resp.Status, string(resp.Body))
	}
	body := string(resp.Body)
	if !strings.Contains(body, baserver.EnvInstanceMCPEndpoint+" is required") {
		t.Fatalf("expected missing endpoint error, got %s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("missing endpoint response must not infer from untrusted host headers: %s", body)
	}
}

func TestInstancePlaneWellKnownProtectedResource_RejectsConfiguredEndpointMismatch(t *testing.T) {
	t.Setenv(baserver.EnvInstanceMCPEndpoint, "https://api.example.com/instance/{surface}/mcp")

	app, err := instanceapp.New("lesser-body-instance", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/oauth-protected-resource/instance/ba/mcp",
		Headers: map[string][]string{
			"x-forwarded-host":  {"other.example.com"},
			"x-forwarded-proto": {"https"},
		},
	})
	if resp.Status != 400 {
		t.Fatalf("status = %d, want 400; body = %s", resp.Status, string(resp.Body))
	}
	body := string(resp.Body)
	if !strings.Contains(body, "app.invalid_public_url") {
		t.Fatalf("expected invalid public url error, got %s", body)
	}
	if !strings.Contains(body, "https://api.example.com/.well-known/oauth-protected-resource/instance/ba/mcp") {
		t.Fatalf("expected canonical metadata URL in mismatch response, got %s", body)
	}
}

func invokeMCP(t testing.TB, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, payload any) apptheory.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	reqHeaders := map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"application/json, text/event-stream"},
	}
	for k, values := range headers {
		reqHeaders[k] = append([]string(nil), values...)
	}

	return env.Invoke(context.Background(), app, apptheory.Request{
		Method:  "POST",
		Path:    path,
		Headers: reqHeaders,
		Body:    body,
	})
}

func initializedMCPHeaders(t testing.TB, env *testkit.Env, app *apptheory.App, path string, token string) map[string][]string {
	t.Helper()

	initResp := invokeMCP(t, env, app, path, bearerHeaders(token), &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize status = %d, body = %s", initResp.Status, string(initResp.Body))
	}
	sessionID := firstHeader(initResp.Headers, "mcp-session-id")
	if sessionID == "" {
		t.Fatalf("initialize response missing mcp-session-id")
	}

	headers := bearerHeaders(token)
	headers["mcp-session-id"] = []string{sessionID}
	headers["mcp-protocol-version"] = []string{"2025-11-25"}
	return headers
}

type toolCallOutput struct {
	Result *mcpruntime.ToolResult `json:"result,omitempty"`
	Error  map[string]any         `json:"error,omitempty"`
}

func callMCPTool(t testing.TB, env *testkit.Env, app *apptheory.App, path string, headers map[string][]string, name string, args map[string]any) toolCallOutput {
	t.Helper()

	resp := invokeMCP(t, env, app, path, headers, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if resp.Status != 200 {
		t.Fatalf("tools/call status = %d, body = %s", resp.Status, string(resp.Body))
	}
	var out toolCallOutput
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode tools/call response: %v; body=%s", err, string(resp.Body))
	}
	return out
}

func toolNames(defs []mcpruntime.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func promptNames(defs []mcpruntime.PromptDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func toolResultData(t testing.TB, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structuredContent: %+v", result)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("structuredContent.data missing: %+v", result.StructuredContent)
	}
	return data
}

func toolResultError(t testing.TB, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatalf("missing structuredContent: %+v", result)
	}
	payload, _ := result.StructuredContent["error"].(map[string]any)
	if payload == nil {
		t.Fatalf("structuredContent.error missing: %+v", result.StructuredContent)
	}
	return payload
}

func newInstanceAgentRegistryStore(t testing.TB) *agentregistry.Store {
	t.Helper()
	t.Setenv(agentregistry.EnvInstanceRegistryTable, "body-instance-registry-test")

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	store, err := agentregistry.NewStore(db, "body-instance-registry-test")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := db.CreateTable(instanceAgentRegistryRecord{}); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	return store
}

type instanceAgentLiveClient struct {
	calls  int
	agents []lesserapi.AgentDirectoryEntry
}

func (f *instanceAgentLiveClient) ListAgents(_ context.Context) ([]lesserapi.AgentDirectoryEntry, error) {
	f.calls++
	return f.agents, nil
}

type instanceAgentRegistryRecord struct {
	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	Account           string    `theorydb:"attr:account" json:"account"`
	AgentID           string    `theorydb:"attr:agentId" json:"agent_id"`
	RegistryCreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	RegistryUpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

func (instanceAgentRegistryRecord) TableName() string {
	return "body-instance-registry-test"
}

func instanceSoulBindingResponse(replayed bool) string {
	return fmt.Sprintf(`{
		"version":"1",
		"status":"bound",
		"binding_state":"bound",
		"agent":{
			"agent_id":"agent-0xabc",
			"domain":"example.com",
			"local_id":"agent1",
			"authority_model":"%s",
			"anchor_state":"%s",
			"operational_binding":"%s",
			"lifecycle_status":"active",
			"published_version":3
		},
		"binding":{
			"agent_username":"agent1",
			"principal_address":"0x1111111111111111111111111111111111111111",
			"bound_at":"2026-07-14T16:20:02Z",
			"updated_at":"2026-07-14T16:20:02Z"
		},
		"idempotency":{
			"key":"bind-key-1",
			"replayed":%t,
			"payload_hash":"sha256:handler-payload"
		},
		"links":{"status":"/api/v1/souls/bindings/agent-0xabc"}
	}`, lesserapi.SoulAuthorityModelInstanceTrust, lesserapi.SoulAnchorStateHostedOffchain, lesserapi.SoulOperationalBindingHostedBound, replayed)
}

func firstHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func bearerHeaders(token string) map[string][]string {
	return map[string][]string{
		"authorization":     {"Bearer " + token},
		"host":              {testHost},
		"x-forwarded-proto": {"https"},
	}
}

func audienceForPath(path string) []string {
	return []string{"https://" + testHost + path}
}

const testHost = "api.example.com"

func newTestToken(t testing.TB, secret string, username string, scopes []string) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithClaims(t, secret, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti_test",
		},
		Username: username,
		Scopes:   scopes,
		ClientID: "test-client",
	})
}

func newTestTokenWithAudience(t testing.TB, secret string, username string, scopes []string, audience []string) string {
	t.Helper()

	return newTestTokenWithAudienceAndAgent(t, secret, username, scopes, audience, false)
}

func newTestTokenWithAudienceAndAgent(t testing.TB, secret string, username string, scopes []string, audience []string, isAgent bool) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithClaims(t, secret, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti_test",
			Audience:  jwt.ClaimStrings(audience),
		},
		Username: username,
		Scopes:   scopes,
		ClientID: "test-client",
		IsAgent:  isAgent,
	})
}

func newOperatorTestTokenWithAudience(t testing.TB, secret string, username string, scopes []string, audience []string) string {
	t.Helper()

	now := time.Now().UTC()
	return newTestTokenWithClaims(t, secret, &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti_operator_test",
			Audience:  jwt.ClaimStrings(audience),
		},
		Username:    username,
		Scopes:      scopes,
		ClientID:    "operator-test-client",
		ClientClass: "operator",
	})
}

func newTestTokenWithClaims(t testing.TB, secret string, claims *auth.Claims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

type instanceBindingDB struct {
	agentID   string
	username  string
	mutations int
}

func (f *instanceBindingDB) Model(any) tablecore.Query {
	return &instanceBindingQuery{db: f, where: map[string]any{}}
}

func (f *instanceBindingDB) Migrate() error                           { return nil }
func (f *instanceBindingDB) AutoMigrate(...any) error                 { return nil }
func (f *instanceBindingDB) Close() error                             { return nil }
func (f *instanceBindingDB) WithContext(context.Context) tablecore.DB { return f }

type instanceBindingQuery struct {
	db    *instanceBindingDB
	where map[string]any
}

func (q *instanceBindingQuery) Where(field string, _ string, value any) tablecore.Query {
	q.where[field] = value
	return q
}

func (q *instanceBindingQuery) Index(string) tablecore.Query                        { return q }
func (q *instanceBindingQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *instanceBindingQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *instanceBindingQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *instanceBindingQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *instanceBindingQuery) IfNotExists() tablecore.Query                        { return q }
func (q *instanceBindingQuery) IfExists() tablecore.Query                           { return q }
func (q *instanceBindingQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *instanceBindingQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *instanceBindingQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *instanceBindingQuery) Limit(int) tablecore.Query                    { return q }
func (q *instanceBindingQuery) Offset(int) tablecore.Query                   { return q }
func (q *instanceBindingQuery) Select(...string) tablecore.Query             { return q }
func (q *instanceBindingQuery) ConsistentRead() tablecore.Query              { return q }
func (q *instanceBindingQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *instanceBindingQuery) All(any) error                                { return nil }
func (q *instanceBindingQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	return nil, nil
}
func (q *instanceBindingQuery) Count() (int64, error)                     { return 0, nil }
func (q *instanceBindingQuery) Create() error                             { q.db.mutations++; return nil }
func (q *instanceBindingQuery) CreateOrUpdate() error                     { q.db.mutations++; return nil }
func (q *instanceBindingQuery) Update(...string) error                    { q.db.mutations++; return nil }
func (q *instanceBindingQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *instanceBindingQuery) Delete() error                             { q.db.mutations++; return nil }
func (q *instanceBindingQuery) Scan(any) error                            { return nil }
func (q *instanceBindingQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *instanceBindingQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *instanceBindingQuery) BatchGet([]any, any) error                 { return nil }
func (q *instanceBindingQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *instanceBindingQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *instanceBindingQuery) BatchCreate(any) error                      { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchDelete([]any) error                    { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchWrite([]any, []any) error              { q.db.mutations++; return nil }
func (q *instanceBindingQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	q.db.mutations++
	return nil
}
func (q *instanceBindingQuery) Cursor(string) tablecore.Query               { return q }
func (q *instanceBindingQuery) SetCursor(string) error                      { return nil }
func (q *instanceBindingQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *instanceBindingQuery) First(dest any) error {
	if q.db == nil {
		return nil
	}
	return setStringFields(dest, map[string]string{
		"AgentID":  q.db.agentID,
		"Username": q.db.username,
	})
}

func setStringFields(dest any, values map[string]string) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}
	for field, value := range values {
		fv := elem.FieldByName(field)
		if !fv.IsValid() || !fv.CanSet() || fv.Kind() != reflect.String {
			continue
		}
		fv.SetString(value)
	}
	return nil
}
