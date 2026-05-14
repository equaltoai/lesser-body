package mcpapp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestM7_WellKnownMcpJSON(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
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

	var out struct {
		Name         string           `json:"name"`
		Version      string           `json:"version"`
		Endpoint     string           `json:"endpoint"`
		Capabilities map[string]bool  `json:"capabilities"`
		Tools        []map[string]any `json:"tools"`
		Auth         struct {
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

	foundEcho := false
	foundSkillsCatalog := false
	foundSkillBundleGet := false
	foundPhoneCall := false
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
		if tool["name"] == "phone_call" {
			foundPhoneCall = true
		}
	}
	if !foundEcho {
		t.Fatalf("expected echo tool in well-known doc")
	}
	if !foundSkillsCatalog || !foundSkillBundleGet {
		t.Fatalf("expected skills tools in well-known doc")
	}
	if foundPhoneCall {
		t.Fatalf("phone_call should not appear in well-known doc")
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
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Endpoint != "https://api.example.com/mcp/{actor}" {
		t.Fatalf("unexpected endpoint: %q", out.Endpoint)
	}
}
