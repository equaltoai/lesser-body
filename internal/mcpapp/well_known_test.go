package mcpapp_test

import (
	"context"
	"encoding/json"
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
		Auth         map[string]any   `json:"auth"`
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
	if _, ok := out.Auth["type"]; !ok {
		t.Fatalf("expected auth hints")
	}

	foundEcho := false
	for _, tool := range out.Tools {
		if tool["name"] == "echo" {
			foundEcho = true
			break
		}
	}
	if !foundEcho {
		t.Fatalf("expected echo tool in well-known doc")
	}
}
