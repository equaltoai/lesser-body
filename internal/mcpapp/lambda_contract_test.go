package mcpapp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

const apptheoryStreamingRouteStageVariablePrefix = "APPTHEORYSTREAMINGV1"

func invokeLambdaProxyRequest(t testing.TB, ctx context.Context, app handleLambdaApp, event events.APIGatewayProxyRequest) any {
	t.Helper()

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal lambda event: %v", err)
	}

	out, err := app.HandleLambda(ctx, body)
	if err != nil {
		t.Fatalf("handle lambda: %v", err)
	}

	return out
}

type handleLambdaApp interface {
	HandleLambda(context.Context, json.RawMessage) (any, error)
}

func streamingStageVariables(method, resource string) map[string]string {
	return map[string]string{
		streamingStageVariableName(method, resource): "1",
	}
}

func streamingStageVariableName(method, resource string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToUpper(method)) + " " + normalizeAPIGatewayRoutePath(resource)))
	return apptheoryStreamingRouteStageVariablePrefix + hex.EncodeToString(sum[:16])
}

func normalizeAPIGatewayRoutePath(path string) string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return "/"
	}

	parts := strings.Split(trimmed, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

func readStreamingBody(t testing.TB, body io.Reader) string {
	t.Helper()

	if body == nil {
		t.Fatal("expected body reader")
	}

	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestHandleLambda_McpRouteReturnsStreamingResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/mcp/{actor}",
		HTTPMethod: "POST",
		Path:       "/mcp/agent1",
		Headers: map[string]string{
			"content-type": "application/json",
			"accept":       "application/json, text/event-stream",
		},
		Body:           string(body),
		StageVariables: streamingStageVariables("POST", "/mcp/{actor}"),
	})

	streaming, ok := out.(*events.APIGatewayProxyStreamingResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyStreamingResponse for /mcp/{actor}, got %T", out)
	}
	if streaming.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", streaming.StatusCode)
	}
	if got := streaming.Headers["www-authenticate"]; got != `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/agent1", scope="read write"` {
		t.Fatalf("unexpected WWW-Authenticate header: %q", got)
	}
	if expose := streaming.Headers["access-control-expose-headers"]; !strings.Contains(strings.ToLower(expose), "www-authenticate") {
		t.Fatalf("expected access-control-expose-headers to include www-authenticate, got %q", expose)
	}
	if body := readStreamingBody(t, streaming.Body); strings.TrimSpace(body) == "" {
		t.Fatalf("expected non-empty body")
	}
}

func TestHandleLambda_McpTrailingSlashCanonicalizes(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/mcp/{actor}",
		HTTPMethod: "POST",
		Path:       "/mcp/agent1/",
		Headers: map[string]string{
			"content-type": "application/json",
			"accept":       "application/json, text/event-stream",
		},
		Body:           string(body),
		StageVariables: streamingStageVariables("POST", "/mcp/{actor}"),
	})

	streaming, ok := out.(*events.APIGatewayProxyStreamingResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyStreamingResponse for /mcp/{actor}/, got %T", out)
	}
	if streaming.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", streaming.StatusCode)
	}
}

func TestHandleLambda_McpRouteRejectsWrongPublicHost(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/mcp/{actor}",
		HTTPMethod: "POST",
		Path:       "/mcp/agent1",
		Headers: map[string]string{
			"content-type":      "application/json",
			"accept":            "application/json, text/event-stream",
			"x-forwarded-proto": "https",
			"x-forwarded-host":  "api.example.com",
		},
		Body:           string(body),
		StageVariables: streamingStageVariables("POST", "/mcp/{actor}"),
	})

	streaming, ok := out.(*events.APIGatewayProxyStreamingResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyStreamingResponse for /mcp/{actor}, got %T", out)
	}
	if streaming.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", streaming.StatusCode)
	}
	if body := readStreamingBody(t, streaming.Body); !strings.Contains(body, "app.invalid_public_url") {
		t.Fatalf("expected invalid public url body, got %s", body)
	}
}

func TestHandleLambda_McpDeleteReturnsProxyResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/mcp/{actor}",
		HTTPMethod: "DELETE",
		Path:       "/mcp/agent1",
	})

	if _, ok := out.(events.APIGatewayProxyResponse); !ok {
		t.Fatalf("expected APIGatewayProxyResponse for DELETE /mcp/{actor}, got %T", out)
	}
}

func TestHandleLambda_WellKnownReturnsProxyResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/.well-known/mcp.json",
		HTTPMethod: "GET",
		Path:       "/.well-known/mcp.json",
	})

	proxy, ok := out.(events.APIGatewayProxyResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyResponse for /.well-known/mcp.json, got %T", out)
	}
	if proxy.StatusCode != 200 {
		t.Fatalf("expected 200, got %d (%s)", proxy.StatusCode, proxy.Body)
	}
}

func TestHandleLambda_OAuthProtectedResourceTrailingSlashCanonicalizes(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	auth.ResetForTests()

	restore := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			TrustBaseURL: "https://lesser.example",
			Present:      true,
		}, nil
	})
	t.Cleanup(restore)
	restoreProbe := mcpapp.SetProbeAuthorizationServerMetadataForTests(func(context.Context, string) (string, error) {
		return "https://dev.example.com", nil
	})
	t.Cleanup(restoreProbe)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	out := invokeLambdaProxyRequest(t, context.Background(), app, events.APIGatewayProxyRequest{
		Resource:   "/.well-known/oauth-protected-resource/mcp/{actor}",
		HTTPMethod: "GET",
		Path:       "/.well-known/oauth-protected-resource/mcp/agent1/",
	})

	proxy, ok := out.(events.APIGatewayProxyResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyResponse for /.well-known/oauth-protected-resource/mcp/{actor}/, got %T", out)
	}
	if proxy.StatusCode != 200 {
		t.Fatalf("expected 200, got %d (%s)", proxy.StatusCode, proxy.Body)
	}
}
