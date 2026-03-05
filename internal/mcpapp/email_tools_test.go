package mcpapp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestLBM2_EmailSendAndReply_TalkToCommAPI(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const tokenUser = "agent1"

	var gotAuth string
	var gotBody map[string]any
	var statusCode int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/soul/agents/mine":
			_, _ = w.Write([]byte(`{
				"agents":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-bob","status":"active"}}],
				"count":1
			}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-bob","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{},"contactPreferences":{},"boundaries":[{"id":"b1","category":"communication_policy","channel":"email","statement":"no unsolicited"}]}`))
		case r.URL.Path == "/api/v1/soul/comm/send" && r.Method == http.MethodPost:
			gotAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(statusCode)
			if statusCode == 403 {
				_, _ = w.Write([]byte(`{"error":{"message":"blocked by boundary"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"messageId":"comm-msg-001","status":"sent"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", tokenUser, []string{"write"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	// email_send should POST to /comm/send with bearer auth and include core fields.
	{
		gotAuth = ""
		gotBody = nil
		statusCode = 200

		callParams, _ := json.Marshal(map[string]any{
			"name": "email_send",
			"arguments": map[string]any{
				"to":      "alice@example.com",
				"subject": "Hello",
				"body":    "Hi there",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("email_send: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotAuth != authHeader {
			t.Fatalf("expected comm api Authorization=%q, got %q", authHeader, gotAuth)
		}
		if gotBody["channel"] != "email" || gotBody["agentId"] != agentID {
			t.Fatalf("unexpected comm api body: %+v", gotBody)
		}
		if gotBody["to"] != "alice@example.com" || gotBody["subject"] != "Hello" || gotBody["body"] != "Hi there" {
			t.Fatalf("unexpected comm api payload fields: %+v", gotBody)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("email_send rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["messageId"] != "comm-msg-001" || data["status"] != "sent" {
			t.Fatalf("unexpected tool data: %+v", data)
		}
	}

	// email_reply should surface boundary violations as tool errors (isError + structured error code).
	{
		gotAuth = ""
		gotBody = nil
		statusCode = 403

		callParams, _ := json.Marshal(map[string]any{
			"name": "email_reply",
			"arguments": map[string]any{
				"messageId": "comm-msg-000",
				"body":      "reply body",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("email_reply: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotAuth != authHeader {
			t.Fatalf("expected comm api Authorization=%q, got %q", authHeader, gotAuth)
		}
		if gotBody["inReplyTo"] != "comm-msg-000" {
			t.Fatalf("expected inReplyTo=comm-msg-000, got %+v", gotBody)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("email_reply rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if !out.IsError {
			t.Fatalf("expected isError tool result, got %+v", out)
		}
		errPayload, _ := out.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != "boundary_violation" {
			t.Fatalf("expected boundary_violation, got %+v", errPayload)
		}
		if !strings.Contains(strings.ToLower(errPayload["message"].(string)), "blocked") {
			t.Fatalf("expected error message to mention blocked, got %+v", errPayload)
		}
	}
}
