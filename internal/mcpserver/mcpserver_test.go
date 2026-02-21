package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/theory-cloud/apptheory/testkit"
	mcptest "github.com/theory-cloud/apptheory/testkit/mcp"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func TestEchoTool(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")

	srv, err := mcpserver.New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	env := testkit.New()
	client := mcptest.NewClient(srv, env)

	initResp, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if client.SessionID() == "" {
		t.Fatalf("expected non-empty session id")
	}
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}
	{
		b, marshalErr := json.Marshal(initResp.Result)
		if marshalErr != nil {
			t.Fatalf("marshal initialize result: %v", marshalErr)
		}
		var out struct {
			Capabilities map[string]any `json:"capabilities"`
		}
		if unmarshalErr := json.Unmarshal(b, &out); unmarshalErr != nil {
			t.Fatalf("unmarshal initialize result: %v", unmarshalErr)
		}
		if _, ok := out.Capabilities["tools"]; !ok {
			t.Fatalf("initialize result missing capabilities.tools: %+v", out.Capabilities)
		}
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	mcptest.AssertHasTools(t, tools, "echo")

	out, err := client.CallTool(context.Background(), "echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "text" || out.Content[0].Text != "hi" {
		t.Fatalf("unexpected tool result: %+v", out)
	}
}
