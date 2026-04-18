package mcpapp_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

const testMCPProtocolVersion = "2025-11-25"

func TestInitialSessionListenerGETClosesOnLambdaBudget(t *testing.T) {
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

	event := events.APIGatewayProxyRequest{
		Resource:   "/mcp/{actor}",
		HTTPMethod: "GET",
		Path:       "/mcp/agent1",
		Headers: map[string]string{
			"authorization":        authHeader,
			"mcp-session-id":       sessionID,
			"mcp-protocol-version": testMCPProtocolVersion,
			"accept":               "text/event-stream",
		},
		StageVariables: streamingStageVariables("GET", "/mcp/{actor}"),
	}
	eventBody, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 5300*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := app.HandleLambda(reqCtx, eventBody)
	if err != nil {
		t.Fatalf("handle lambda: %v", err)
	}

	resp, ok := out.(*events.APIGatewayProxyStreamingResponse)
	if !ok {
		t.Fatalf("expected streaming response, got %T", out)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("listener GET: status=%d", resp.StatusCode)
	}
	if resp.Body == nil {
		t.Fatalf("expected session listener BodyReader")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read listener body: %v", err)
	}

	elapsed := time.Since(start)
	if reqCtx.Err() != nil {
		t.Fatalf("expected listener to close before parent deadline, got %v", reqCtx.Err())
	}
	if len(body) == 0 {
		t.Fatalf("expected non-empty listener body")
	}
	if !containsKeepaliveFrame(string(body)) {
		t.Fatalf("expected keepalive frame, got %q", string(body))
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("listener closed too quickly, elapsed=%v body=%q", elapsed, string(body))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("listener did not close on lambda budget, elapsed=%v body=%q", elapsed, string(body))
	}
}

func containsKeepaliveFrame(body string) bool {
	return len(body) > 0 && (body == ": keepalive\n\n" ||
		body == ":keepalive\n\n" ||
		body == ": keepalive\r\n\r\n" ||
		body == ":keepalive\r\n\r\n" ||
		strings.Contains(body, ": keepalive") ||
		strings.Contains(body, ":keepalive"))
}
