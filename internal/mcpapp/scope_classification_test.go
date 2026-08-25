package mcpapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

// A read-scoped token must not be able to invoke a side-effecting tool, and the
// rejection must happen before any downstream lesser / lesser-host call is made.
func TestReadScopedTokenCannotInvokeWriteTools(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	const agentID = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	// Counts every downstream request. A 403 must leave this at zero.
	var upstreamCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-carol")))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
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

	writeTools := map[string]map[string]any{
		"sms_send":                {"to": "+1 (555) 0143", "body": "On it."},
		"email_send":              {"to": "someone@example.com", "subject": "hi", "body": "hello"},
		"post_create":             {"content": "hello world"},
		"memory_append":           {"content": "remember this"},
		"follow":                  {"accountId": "acct-1"},
		"message_request_accept":  {"conversationId": "conv-1"},
		"message_request_decline": {"conversationId": "conv-1"},
	}

	for tool, args := range writeTools {
		t.Run(tool+" is rejected for a read-scoped token", func(t *testing.T) {
			upstreamCalls.Store(0)

			callParams, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
			resp := invokeJSON(t, env, app, map[string][]string{
				"authorization":  {authHeader},
				"mcp-session-id": {sessionID},
			}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})

			if resp.Status != 403 {
				t.Fatalf("%s with read scope: expected 403, got status=%d body=%s", tool, resp.Status, string(resp.Body))
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
				t.Fatalf("%s: decode error body: %v", tool, err)
			}
			if body.Error.Details.Reason != "insufficient_scope" {
				t.Fatalf("%s: expected reason insufficient_scope, got %q", tool, body.Error.Details.Reason)
			}
			if len(body.Error.Details.RequiredScopes) != 1 || body.Error.Details.RequiredScopes[0] != "write" {
				t.Fatalf("%s: expected requiredScopes [write], got %v", tool, body.Error.Details.RequiredScopes)
			}

			if got := upstreamCalls.Load(); got != 0 {
				t.Fatalf("%s: rejected call must make zero downstream calls, got %d", tool, got)
			}
		})
	}

	// The read surface must still work: tightening must not over-restrict.
	t.Run("read-scoped token can still invoke a read tool", func(t *testing.T) {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "echo",
			"arguments": map[string]any{"message": "still readable"},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("echo with read scope: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("echo with read scope returned rpc error: %+v", rpc.Error)
		}
	})
}
