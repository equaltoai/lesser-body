package mcpapp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestM7_WellKnownMcpJSON(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "theory-dev-mcp-tasks")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/mcp.json",
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &raw); err != nil {
		t.Fatalf("unmarshal raw mcp.json: %v", err)
	}
	wantTopLevel := map[string]bool{
		"name":              true,
		"version":           true,
		"endpoint":          true,
		"capabilities":      true,
		"auth":              true,
		"runtime_profiles":  true,
		"instance_surfaces": true,
		"tools":             true,
	}
	for key := range raw {
		if !wantTopLevel[key] {
			t.Fatalf("unexpected top-level discovery key %q in %s", key, string(resp.Body))
		}
	}
	for key := range wantTopLevel {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing top-level discovery key %q in %s", key, string(resp.Body))
		}
	}

	var out struct {
		Name             string           `json:"name"`
		Version          string           `json:"version"`
		Endpoint         string           `json:"endpoint"`
		Capabilities     map[string]bool  `json:"capabilities"`
		Tools            []map[string]any `json:"tools"`
		InstanceSurfaces map[string]struct {
			Endpoint                     string `json:"endpoint"`
			ProtectedResourceMetadataURL string `json:"protected_resource_metadata_url"`
			ToolsDiscovery               string `json:"tools_discovery"`
		} `json:"instance_surfaces"`
		Auth struct {
			Type   string   `json:"type"`
			Scopes []string `json:"scopes"`
			Notes  string   `json:"notes"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Endpoint != "https://api.example.com/mcp" {
		t.Fatalf("unexpected endpoint: %q", out.Endpoint)
	}
	if !out.Capabilities["tools"] {
		t.Fatalf("expected capabilities.tools=true")
	}
	if !out.Capabilities["completions"] {
		t.Fatalf("expected capabilities.completions=true")
	}
	if !out.Capabilities["tasks"] {
		t.Fatalf("expected task pilot runtime to advertise capabilities.tasks")
	}
	if out.Auth.Type != "bearer" {
		t.Fatalf("expected bearer auth hints, got %q", out.Auth.Type)
	}
	if want := []string{"read", "write", "follow", "push"}; !reflect.DeepEqual(out.Auth.Scopes, want) {
		t.Fatalf("unexpected auth scopes: got %#v want %#v", out.Auth.Scopes, want)
	}
	if !strings.Contains(out.Auth.Notes, "OAuth access token") {
		t.Fatalf("expected OAuth-first auth note, got %q", out.Auth.Notes)
	}
	if strings.Contains(strings.ToLower(out.Auth.Notes), "or managed instance key") {
		t.Fatalf("discovery should not recommend managed instance key, got %q", out.Auth.Notes)
	}
	for _, scope := range out.Auth.Scopes {
		if scope == "admin" || strings.Contains(scope, "instance") {
			t.Fatalf("discovery advertised non-public scope %q", scope)
		}
	}
	if got := out.InstanceSurfaces["ptah"].Endpoint; got != "https://api.example.com/instance/ptah/mcp" {
		t.Fatalf("ptah endpoint = %q", got)
	}
	if got := out.InstanceSurfaces["ptah"].ProtectedResourceMetadataURL; got != "https://api.example.com/.well-known/oauth-protected-resource/instance/ptah/mcp" {
		t.Fatalf("ptah protected_resource_metadata_url = %q", got)
	}
	if got := out.InstanceSurfaces["ba"].Endpoint; got != "https://api.example.com/instance/ba/mcp" {
		t.Fatalf("ba endpoint = %q", got)
	}
	if got := out.InstanceSurfaces["ba"].ProtectedResourceMetadataURL; got != "https://api.example.com/.well-known/oauth-protected-resource/instance/ba/mcp" {
		t.Fatalf("ba protected_resource_metadata_url = %q", got)
	}
	for surface, hint := range out.InstanceSurfaces {
		if hint.ToolsDiscovery != "authenticated tools/list" {
			t.Fatalf("%s tools_discovery = %q", surface, hint.ToolsDiscovery)
		}
	}

	foundEcho := false
	foundSkillsCatalog := false
	foundSkillBundleGet := false
	foundArticleDraftCreate := false
	foundArticleDraftPublish := false
	foundPhoneCall := false
	foundAgentCreate := false
	foundAgentLocalInstallPlan := false
	for _, tool := range out.Tools {
		if tool["name"] == "echo" {
			foundEcho = true
		}
		if tool["name"] == "skills_catalog" {
			foundSkillsCatalog = true
		}
		if tool["name"] == "skill_bundle_get" {
			foundSkillBundleGet = true
		}
		if tool["name"] == "article_draft_create" {
			foundArticleDraftCreate = true
		}
		if tool["name"] == "article_draft_publish" {
			foundArticleDraftPublish = true
		}
		if tool["name"] == "phone_call" {
			foundPhoneCall = true
		}
		if tool["name"] == "agent_create" {
			foundAgentCreate = true
		}
		if tool["name"] == "agent_local_install_plan" {
			foundAgentLocalInstallPlan = true
		}
	}
	if !foundEcho {
		t.Fatalf("expected echo tool in well-known doc")
	}
	if !foundSkillsCatalog || !foundSkillBundleGet {
		t.Fatalf("expected skills tools in well-known doc")
	}
	if !foundArticleDraftCreate {
		t.Fatalf("expected article_draft_create in well-known doc")
	}
	if !foundArticleDraftPublish {
		t.Fatalf("expected article_draft_publish in well-known doc")
	}
	if foundPhoneCall {
		t.Fatalf("phone_call should not appear in well-known doc")
	}
	if foundAgentCreate || foundAgentLocalInstallPlan {
		t.Fatalf("instance-plane tools must not appear in Ka public tools list")
	}
}

func TestM7_WellKnownMcpJSON_AdvertisesActorTemplateEndpoint(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/mcp.json",
	})
	if resp.Status != 200 {
		t.Fatalf("unexpected status: %d (%s)", resp.Status, string(resp.Body))
	}

	var out struct {
		Endpoint         string `json:"endpoint"`
		InstanceSurfaces map[string]struct {
			Endpoint                     string `json:"endpoint"`
			ProtectedResourceMetadataURL string `json:"protected_resource_metadata_url"`
		} `json:"instance_surfaces"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Endpoint != "https://api.example.com/mcp/{actor}" {
		t.Fatalf("unexpected endpoint: %q", out.Endpoint)
	}
	if got := out.InstanceSurfaces["ptah"].Endpoint; got != "https://api.example.com/instance/ptah/mcp" {
		t.Fatalf("unexpected ptah instance endpoint: %q", got)
	}
	if got := out.InstanceSurfaces["ba"].Endpoint; got != "https://api.example.com/instance/ba/mcp" {
		t.Fatalf("unexpected ba instance endpoint: %q", got)
	}
	if got := out.InstanceSurfaces["ptah"].ProtectedResourceMetadataURL; got != "https://api.example.com/.well-known/oauth-protected-resource/instance/ptah/mcp" {
		t.Fatalf("unexpected ptah metadata url: %q", got)
	}
}
