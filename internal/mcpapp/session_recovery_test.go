package mcpapp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/memory"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"
)

func TestActorMCPOAuthDeadSessionTransparentlyRebindsReadCall(t *testing.T) {
	previousLogger := slog.Default()
	var logOutput strings.Builder
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logOutput, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(
		t,
		"test-secret",
		"agent1",
		[]string{"read"},
		[]string{"https://api.example.com/mcp/agent1"},
	)

	const deadSessionID = "dead-session"
	callParams, err := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"message": "rebound"},
	})
	if err != nil {
		t.Fatalf("marshal echo params: %v", err)
	}
	reboundResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {deadSessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "rebound-read",
		Method:  "tools/call",
		Params:  callParams,
	})
	if reboundResp.Status != 200 {
		t.Fatalf("rebound read status = %d, want 200; body = %s", reboundResp.Status, string(reboundResp.Body))
	}
	freshSessionID := firstHeader(reboundResp.Headers, "mcp-session-id")
	if freshSessionID == "" || freshSessionID == deadSessionID {
		t.Fatalf("rebound response session id = %q, want a fresh id", freshSessionID)
	}
	if got := firstHeader(reboundResp.Headers, "www-authenticate"); got != "" {
		t.Fatalf("successful rebind gained WWW-Authenticate: %q", got)
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(reboundResp.Body, &rpc); err != nil {
		t.Fatalf("decode rebound read response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("rebound read returned JSON-RPC error: %+v", rpc.Error)
	}
	var tool mcpruntime.ToolResult
	resultBody, err := json.Marshal(rpc.Result)
	if err != nil {
		t.Fatalf("marshal rebound tool result: %v", err)
	}
	if err := json.Unmarshal(resultBody, &tool); err != nil {
		t.Fatalf("decode rebound tool result: %v", err)
	}
	if len(tool.Content) != 1 || tool.Content[0].Text != "rebound" {
		t.Fatalf("rebound echo result = %+v", tool)
	}

	nextResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {freshSessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "fresh-session-next-call",
		Method:  "tools/list",
	})
	if nextResp.Status != 200 {
		t.Fatalf("fresh session next call status = %d, want 200; body = %s", nextResp.Status, string(nextResp.Body))
	}

	assertSanitizedSessionRebindAudit(t, logOutput.String(), "agent1", deadSessionID, freshSessionID)
}

func TestActorMCPOAuthDeadSessionWriteCallExecutesExactlyOnce(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	t.Cleanup(memory.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestTokenWithAudience(
		t,
		"test-secret",
		"agent1",
		[]string{"write"},
		[]string{"https://api.example.com/mcp/agent1"},
	)

	const sentinel = "dead-session write must execute exactly once"
	callParams, err := json.Marshal(map[string]any{
		"name":      "memory_append",
		"arguments": map[string]any{"content": sentinel},
	})
	if err != nil {
		t.Fatalf("marshal memory_append params: %v", err)
	}
	resp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {"dead-write-session"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "rebound-write",
		Method:  "tools/call",
		Params:  callParams,
	})
	if resp.Status != 200 {
		t.Fatalf("rebound write status = %d, want 200; body = %s", resp.Status, string(resp.Body))
	}
	if freshSessionID := firstHeader(resp.Headers, "mcp-session-id"); freshSessionID == "" || freshSessionID == "dead-write-session" {
		t.Fatalf("rebound write response session id = %q, want a fresh id", freshSessionID)
	}

	store, err := memory.Default()
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	got, err := store.Query(context.Background(), "agent1", memory.QueryInput{
		Query: sentinel,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("query memory after rebound write: %v", err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("rebound write executed %d times, want exactly once; events = %+v", len(got.Events), got.Events)
	}
}

func TestActorMCPOAuthDeadSessionSSEStreamIsNotRebound(t *testing.T) {
	previousLogger := slog.Default()
	var logOutput strings.Builder
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logOutput, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	token := newTestTokenWithAudience(
		t,
		"test-secret",
		"agent1",
		[]string{"read"},
		[]string{"https://api.example.com/mcp/agent1"},
	)

	resp := testkit.New().Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":        {"Bearer " + token},
			"accept":               {"text/event-stream"},
			"mcp-protocol-version": {"2025-11-25"},
			"mcp-session-id":       {"dead-stream-session"},
			"last-event-id":        {"event-from-dead-session"},
		},
	})
	assertSessionNotFound404(t, resp)
	if got := firstHeader(resp.Headers, "cache-control"); got != "no-store" {
		t.Fatalf("dead OAuth SSE cache-control = %q, want no-store", got)
	}
	if got := firstHeader(resp.Headers, "mcp-session-id"); got != "" {
		t.Fatalf("dead SSE resume was rebound to session %q", got)
	}
	assertSanitizedSessionNotFoundAudit(t, logOutput.String(), "agent1", "dead-stream-session", token)
}

func TestOAuthSessionRebindLeavesUnauthenticatedDeadSessionAs404(t *testing.T) {
	server := mcpruntime.NewServer("unauthenticated-session-test", "dev")
	app := apptheory.New()
	app.Post("/mcp", mcpapp.WithOAuthSessionRecovery(server.Handler()))

	body, err := json.Marshal(&mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "unauthenticated-dead-session",
		Method:  "tools/list",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp := testkit.New().Invoke(context.Background(), app, apptheory.Request{
		Method: "POST",
		Path:   "/mcp",
		Headers: map[string][]string{
			"accept":               {"application/json, text/event-stream"},
			"content-type":         {"application/json"},
			"mcp-protocol-version": {"2025-11-25"},
			"mcp-session-id":       {"dead-session"},
		},
		Body: body,
	})

	assertSessionNotFound404(t, resp)
}

func TestActorMCPSessionNotFoundRemains404ForNonOAuthPrincipal(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	resp := invokeJSONAtPath(t, testkit.New(), app, "/mcp/instance", map[string][]string{
		"authorization":        {"Bearer legacy-instance-key"},
		"mcp-protocol-version": {"2025-11-25"},
		"mcp-session-id":       {"dead-session"},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "legacy-dead-session",
		Method:  "tools/list",
	})

	assertSessionNotFound404(t, resp)
}

func TestActorMCP20260728StatelessRequestIsNotRebound(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp/{actor}")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	app, err := mcpapp.New("lesser-body", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	token := newTestTokenWithAudience(
		t,
		"test-secret",
		"agent1",
		[]string{"read"},
		[]string{"https://api.example.com/mcp/agent1"},
	)

	resp := invokeJSONAtPath(t, testkit.New(), app, "/mcp/agent1", map[string][]string{
		"authorization":        {"Bearer " + token},
		"mcp-protocol-version": {"2026-07-28"},
		"mcp-method":           {"tools/list"},
		"mcp-session-id":       {"dead-session-is-ignored"},
	}, bodyModernRequest("modern-stateless", "tools/list", map[string]any{}))
	if resp.Status != 200 {
		t.Fatalf("modern stateless status = %d, want 200; body = %s", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != "" {
		t.Fatalf("modern stateless response gained WWW-Authenticate: %q", got)
	}
	if got := firstHeader(resp.Headers, "mcp-session-id"); got != "" {
		t.Fatalf("modern stateless response gained mcp-session-id: %q", got)
	}
}

func assertSessionNotFound404(t testing.TB, resp apptheory.Response) {
	t.Helper()

	if resp.Status != 404 {
		t.Fatalf("session-not-found status = %d, want 404; body = %s", resp.Status, string(resp.Body))
	}
	if strings.TrimSpace(string(resp.Body)) != `{"error":"session not found"}` {
		t.Fatalf("session-not-found body changed: %s", string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "www-authenticate"); got != "" {
		t.Fatalf("session-not-found 404 gained WWW-Authenticate: %q", got)
	}
	if got := firstHeader(resp.Headers, "mcp-www-authenticate"); got != "" {
		t.Fatalf("session-not-found 404 gained MCP-WWW-Authenticate: %q", got)
	}
	if got := firstHeader(resp.Headers, "mcp-session-id"); got != "" {
		t.Fatalf("non-OAuth 404 gained mcp-session-id: %q", got)
	}
}

func assertSanitizedSessionRebindAudit(t testing.TB, logs string, forbiddenValues ...string) {
	t.Helper()

	count := 0
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode structured log event: %v; line = %s", err, scanner.Text())
		}
		if event["msg"] != "mcp session rebound" {
			continue
		}
		count++
		if event["principal_type"] != string(auth.PrincipalTypeOAuthToken) ||
			event["reason"] != "mcp_session_rebound" {
			t.Fatalf("rebind audit fields = %+v", event)
		}
		for _, forbiddenKey := range []string{
			"actor",
			"body",
			"identity",
			"new_session_id",
			"old_session_id",
			"request_body",
			"session_id",
		} {
			if _, ok := event[forbiddenKey]; ok {
				t.Fatalf("rebind audit exposed %q: %+v", forbiddenKey, event)
			}
		}
		serialized, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal rebind audit event: %v", err)
		}
		for _, forbiddenValue := range forbiddenValues {
			if forbiddenValue != "" && strings.Contains(string(serialized), forbiddenValue) {
				t.Fatalf("rebind audit exposed sensitive value %q: %s", forbiddenValue, serialized)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan structured logs: %v", err)
	}
	if count != 1 {
		t.Fatalf("rebind audit event count = %d, want 1; logs = %s", count, logs)
	}
}

func assertSanitizedSessionNotFoundAudit(t testing.TB, logs string, forbiddenValues ...string) {
	t.Helper()

	count := 0
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode structured log event: %v; line = %s", err, scanner.Text())
		}
		if event["msg"] != "mcp session not found" {
			continue
		}
		count++
		if event["principal_type"] != string(auth.PrincipalTypeOAuthToken) ||
			event["reason"] != "mcp_session_not_found" {
			t.Fatalf("session-not-found audit fields = %+v", event)
		}
		serialized, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal session-not-found audit event: %v", err)
		}
		for _, forbiddenValue := range forbiddenValues {
			if forbiddenValue != "" && strings.Contains(string(serialized), forbiddenValue) {
				t.Fatalf("session-not-found audit exposed sensitive value %q: %s", forbiddenValue, serialized)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan structured logs: %v", err)
	}
	if count != 1 {
		t.Fatalf("session-not-found audit event count = %d, want 1; logs = %s", count, logs)
	}
}
