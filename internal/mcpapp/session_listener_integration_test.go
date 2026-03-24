package mcpapp_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

const testMCPProtocolVersion = "2025-11-25"

func TestInitialSessionListenerGETClosesOnConfiguredBudget(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("MCP_SESSION_LISTENER_TIMEOUT", "50ms")
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

	start := time.Now()
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":        {authHeader},
			"mcp-session-id":       {sessionID},
			"mcp-protocol-version": {testMCPProtocolVersion},
		},
	})
	if resp.Status != 200 {
		t.Fatalf("listener GET: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if resp.BodyReader == nil {
		t.Fatalf("expected session listener BodyReader")
	}

	body, err := io.ReadAll(resp.BodyReader)
	if err != nil {
		t.Fatalf("read listener body: %v", err)
	}

	elapsed := time.Since(start)
	if !strings.Contains(string(body), ": keepalive") {
		t.Fatalf("expected keepalive frame, got %q", string(body))
	}
	if elapsed < 35*time.Millisecond {
		t.Fatalf("listener closed too quickly, elapsed=%v body=%q", elapsed, string(body))
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("listener did not close on budget, elapsed=%v body=%q", elapsed, string(body))
	}
}
