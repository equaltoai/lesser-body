package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestLBM3_InboxToolsFilterNotifications(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var dismissedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/notifications":
			_, _ = w.Write([]byte(`[
				{
					"id":"n1",
					"type":"communication:inbound",
					"channel":"email",
					"messageId":"comm-msg-001",
					"from":{"address":"alice@example.com"},
					"subject":"Hi",
					"body":"Hello",
					"receivedAt":"2026-03-04T12:00:00Z"
				},
				{
					"id":"n2",
					"type":"communication:inbound",
					"channel":"sms",
					"messageId":"comm-msg-002",
					"from":{"address":"+15550142"},
					"body":"sms body",
					"receivedAt":"2026-03-04T12:05:00Z"
				}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/notifications/") && strings.HasSuffix(r.URL.Path, "/dismiss") && r.Method == http.MethodPost:
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 5 {
				dismissedID = parts[4]
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"write"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	// email_read should return only email messages.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name":      "email_read",
			"arguments": map[string]any{"folder": "inbox", "limit": 10},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("email_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("email_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		messages, _ := data["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("expected 1 email message, got %+v", messages)
		}
		msg, _ := messages[0].(map[string]any)
		if msg["messageId"] != "comm-msg-001" {
			t.Fatalf("unexpected email message: %+v", msg)
		}
	}

	// sms_read should return only sms messages.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name":      "sms_read",
			"arguments": map[string]any{"limit": 10},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("sms_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("sms_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		messages, _ := data["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("expected 1 sms message, got %+v", messages)
		}
		msg, _ := messages[0].(map[string]any)
		if msg["messageId"] != "comm-msg-002" {
			t.Fatalf("unexpected sms message: %+v", msg)
		}
	}

	// email_search should match on subject/body/from.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name":      "email_search",
			"arguments": map[string]any{"query": "alice", "limit": 5},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("email_search: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("email_search rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["count"] != 1.0 && data["count"] != 1 {
			t.Fatalf("expected 1 search hit, got %+v", data)
		}
	}

	// email_delete should dismiss the underlying notification.
	{
		dismissedID = ""
		callParams, _ := json.Marshal(map[string]any{
			"name": "email_delete",
			"arguments": map[string]any{
				"messageId": "comm-msg-001",
				"action":    "archive",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("email_delete: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if dismissedID != "n1" {
			t.Fatalf("expected dismissed notification n1, got %q", dismissedID)
		}
	}

	// agent://email/inbox should be readable.
	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://email/inbox"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 6, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read agent://email/inbox: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}
}
