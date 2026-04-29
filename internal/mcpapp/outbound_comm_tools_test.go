package mcpapp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestLBM6_SMSAndVoiceOutboundTools(t *testing.T) {
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
	const tokenUser = "agent1"
	const expectedSMSBody = "[bot] On it."

	var gotAuth string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/souls/mine":
			_, _ = w.Write([]byte(`{"souls":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-carol"},"binding_state":"bound","binding":{"agent_username":"agent1"}}]}`))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-carol","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{
				"version":"3",
				"channels":{
					"phone":{"number":"+15550142","capabilities":["sms-send","voice-receive"],"verified":true}
				},
				"contactPreferences":{},
				"boundaries":[{"id":"b1","category":"communication_policy","channel":"sms","statement":"reply in thread"}]
			}`))
		case "/api/v1/soul/comm/send":
			gotAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_, _ = w.Write([]byte(`{"messageId":"comm-msg-sms-001","status":"queued"}`))
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
	token := newTestToken(t, "test", tokenUser, []string{"write"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	t.Run("sms send uses instance key and reply reference", func(t *testing.T) {
		gotAuth = ""
		gotBody = nil

		callParams, _ := json.Marshal(map[string]any{
			"name": "sms_send",
			"arguments": map[string]any{
				"to":        "+1 (555) 0143",
				"body":      "On it.",
				"messageId": "comm-msg-prev",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("sms_send: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotAuth != "Bearer instance-key-123" {
			t.Fatalf("expected comm api Authorization=%q, got %q", "Bearer instance-key-123", gotAuth)
		}
		if gotBody["channel"] != "sms" || gotBody["agentId"] != agentID {
			t.Fatalf("unexpected comm api body: %+v", gotBody)
		}
		if gotBody["to"] != "+15550143" || gotBody["inReplyTo"] != "comm-msg-prev" || gotBody["body"] != expectedSMSBody {
			t.Fatalf("unexpected sms payload: %+v", gotBody)
		}
		if gotBody["idempotencyKey"] != "comm-msg-prev" {
			t.Fatalf("expected sms idempotencyKey=comm-msg-prev, got %+v", gotBody)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("sms_send rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["messageId"] != "comm-msg-sms-001" || data["status"] != "queued" {
			t.Fatalf("unexpected sms result: %+v", data)
		}
		if data["idempotencyKey"] != "comm-msg-prev" {
			t.Fatalf("expected sms result idempotencyKey, got %+v", data)
		}
	})

	t.Run("sms send generates fallback idempotency key", func(t *testing.T) {
		gotAuth = ""
		gotBody = nil

		callParams, _ := json.Marshal(map[string]any{
			"name": "sms_send",
			"arguments": map[string]any{
				"to":   "+1 (555) 0143",
				"body": "On it.",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
			"x-request-id":   {"req-sms-send-123"},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 22, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("sms_send fallback idempotency: status=%d body=%s", resp.Status, string(resp.Body))
		}
		idempotencyKey, _ := gotBody["idempotencyKey"].(string)
		if idempotencyKey == "" || idempotencyKey == "req-sms-send-123" {
			t.Fatalf("expected generated sms fallback idempotencyKey distinct from request id, got %+v", gotBody)
		}
		if _, ok := gotBody["inReplyTo"]; ok {
			t.Fatalf("expected no inReplyTo for non-reply sms, got %+v", gotBody)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("sms_send fallback rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["idempotencyKey"] != idempotencyKey {
			t.Fatalf("expected sms fallback result idempotencyKey, got %+v", data)
		}
	})

	t.Run("phone call is no longer registered", func(t *testing.T) {
		listResp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/list"})
		if listResp.Status != 200 {
			t.Fatalf("tools/list: status=%d body=%s", listResp.Status, string(listResp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(listResp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("tools/list rpc error: %+v", rpc.Error)
		}

		var out struct {
			Tools []mcpruntime.ToolDef `json:"tools"`
		}
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		for _, tool := range out.Tools {
			if tool.Name == "phone_call" {
				t.Fatalf("phone_call should not appear in tools/list: %+v", out.Tools)
			}
		}

		callParams, _ := json.Marshal(map[string]any{
			"name": "phone_call",
			"arguments": map[string]any{
				"to":                 "+1 (555) 0143",
				"purpose":            "Call back to confirm details",
				"maxDurationMinutes": 15,
				"messageId":          "comm-msg-prev",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("phone_call: status=%d body=%s", resp.Status, string(resp.Body))
		}

		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error == nil {
			t.Fatalf("expected rpc error for unregistered phone_call, got %+v", rpc)
		}
		if !strings.Contains(rpc.Error.Message, "tool not found: phone_call") {
			t.Fatalf("expected unknown tool error, got %+v", rpc.Error)
		}
		if gotBody != nil && gotBody["channel"] == "voice" {
			t.Fatalf("phone_call should not reach comm api: %+v", gotBody)
		}
	})
}
