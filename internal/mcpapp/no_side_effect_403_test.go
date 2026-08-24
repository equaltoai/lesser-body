package mcpapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/memory"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestReadScopedCommunicationWriteToolsReturn403BeforeHostSideEffects(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	var hostCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"read-scope 403 must not call host"}}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	cases := []struct {
		tool string
		args map[string]any
	}{
		{
			tool: "email_send",
			args: map[string]any{
				"to":             "alice@example.com",
				"subject":        "Denied before host",
				"body":           "This must never be delegated.",
				"idempotencyKey": "email-denied-001",
			},
		},
		{
			tool: "sms_send",
			args: map[string]any{
				"to":        "+1 (555) 0143",
				"body":      "This must never be delegated.",
				"messageId": "sms-denied-001",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			hostCalls.Store(0)
			callParams, _ := json.Marshal(map[string]any{
				"name":      tc.tool,
				"arguments": tc.args,
			})
			resp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {authHeader},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})

			assertInsufficientScope403(t, resp, "write")
			if got := hostCalls.Load(); got != 0 {
				t.Fatalf("read-scoped %s must make zero lesser-host delegated calls, got %d", tc.tool, got)
			}
		})
	}
}

func TestReadScopedMemoryAppendReturns403BeforeMemoryWrite(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()
	t.Cleanup(memory.ResetForTests)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	const sentinel = "read-scope memory_append must not write this sentinel"
	callParams, _ := json.Marshal(map[string]any{
		"name": "memory_append",
		"arguments": map[string]any{
			"content": sentinel,
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})

	assertInsufficientScope403(t, resp, "write")

	store, err := memory.Default()
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	got, err := store.Query(context.Background(), "agent1", memory.QueryInput{Query: sentinel, Limit: 10})
	if err != nil {
		t.Fatalf("query memory after forbidden append: %v", err)
	}
	if len(got.Events) != 0 {
		t.Fatalf("read-scoped memory_append must make zero memory writes, got events: %+v", got.Events)
	}
}

func assertInsufficientScope403(t testing.TB, resp apptheory.Response, requiredScope string) {
	t.Helper()
	if resp.Status != 403 {
		t.Fatalf("expected 403 insufficient_scope, got status=%d body=%s", resp.Status, string(resp.Body))
	}
	var body struct {
		Error struct {
			Details struct {
				Reason         string   `json:"reason"`
				RequiredScopes []string `json:"requiredScopes"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode 403 body: %v", err)
	}
	if body.Error.Details.Reason != "insufficient_scope" {
		t.Fatalf("expected insufficient_scope, got %+v", body.Error.Details)
	}
	if len(body.Error.Details.RequiredScopes) != 1 || body.Error.Details.RequiredScopes[0] != requiredScope {
		t.Fatalf("expected requiredScopes [%s], got %v", requiredScope, body.Error.Details.RequiredScopes)
	}
}
