package lambdaentry

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestNewAPIGatewayHandler_McpRouteReturnsStreamingResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	handler := NewAPIGatewayHandler(app)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	out, err := handler(context.Background(), events.APIGatewayProxyRequest{
		HTTPMethod: "POST",
		Path:       "/mcp",
		Headers: map[string]string{
			"content-type": "application/json",
		},
		Body:            string(body),
		IsBase64Encoded: false,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	streaming, ok := out.(*events.APIGatewayProxyStreamingResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyStreamingResponse for /mcp, got %T", out)
	}
	if streaming.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", streaming.StatusCode)
	}
	if streaming.Body == nil {
		t.Fatalf("expected body reader")
	}
	b, readErr := io.ReadAll(streaming.Body)
	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}
	if len(b) == 0 {
		t.Fatalf("expected non-empty body")
	}
}

func TestNewAPIGatewayHandler_McpDeleteReturnsProxyResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	handler := NewAPIGatewayHandler(app)
	out, err := handler(context.Background(), events.APIGatewayProxyRequest{
		HTTPMethod: "DELETE",
		Path:       "/mcp",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if _, ok := out.(events.APIGatewayProxyResponse); !ok {
		t.Fatalf("expected APIGatewayProxyResponse for DELETE /mcp, got %T", out)
	}
}

func TestNewAPIGatewayHandler_WellKnownReturnsProxyResponseType(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	handler := NewAPIGatewayHandler(app)
	out, err := handler(context.Background(), events.APIGatewayProxyRequest{
		HTTPMethod: "GET",
		Path:       "/.well-known/mcp.json",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	proxy, ok := out.(events.APIGatewayProxyResponse)
	if !ok {
		t.Fatalf("expected APIGatewayProxyResponse for /.well-known/mcp.json, got %T", out)
	}
	if proxy.StatusCode != 200 {
		t.Fatalf("expected 200, got %d (%s)", proxy.StatusCode, proxy.Body)
	}
}
