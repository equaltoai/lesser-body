package mcpapp_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

const testMCPProtocolVersion = "2025-11-25"

func TestInitialSessionListenerGETClosesAfterKeepalive(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read"}, []string{"https://api.example.com/mcp/agent1"})
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {authHeader},
		"mcp-protocol-version": {testMCPProtocolVersion},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	startedAt := time.Now()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":        {authHeader},
			"mcp-session-id":       {sessionID},
			"mcp-protocol-version": {testMCPProtocolVersion},
			"accept":               {"text/event-stream"},
		},
	})
	if resp.Status != 200 {
		t.Fatalf("listener GET: status=%d, want 200; body=%s", resp.Status, string(resp.Body))
	}
	if resp.BodyReader == nil {
		t.Fatal("listener GET must return an SSE body")
	}
	body, err := io.ReadAll(resp.BodyReader)
	if err != nil {
		t.Fatalf("read listener body: %v", err)
	}
	if !strings.Contains(string(body), "keepalive") {
		t.Fatalf("listener body = %q, want keepalive", string(body))
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("idle listener retained Lambda execution for %v", elapsed)
	}
}
