package mcpapp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestLBM6_SMSAndVoiceOutboundTools(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	const tokenUser = "agent1"

	var gotAuth string
	var gotBody map[string]any
	voiceStatusCode := http.StatusOK
	voiceResponseBody := `{"messageId":"comm-msg-call-001","status":"accepted","provider":"telnyx"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/souls/mine":
			_, _ = w.Write([]byte(`{
				"souls":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-carol","status":"active"}}],
				"count":1
			}`))
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
			if gotBody["channel"] == "voice" {
				w.WriteHeader(voiceStatusCode)
				_, _ = w.Write([]byte(voiceResponseBody))
				return
			}
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
		if gotBody["to"] != "+15550143" || gotBody["inReplyTo"] != "comm-msg-prev" || gotBody["body"] != "On it." {
			t.Fatalf("unexpected sms payload: %+v", gotBody)
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
	})

	t.Run("phone call succeeds when host supports voice", func(t *testing.T) {
		gotAuth = ""
		gotBody = nil
		voiceStatusCode = http.StatusOK
		voiceResponseBody = `{"messageId":"comm-msg-call-001","status":"accepted","provider":"telnyx"}`

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
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("phone_call: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotAuth != "Bearer instance-key-123" {
			t.Fatalf("expected comm api Authorization=%q, got %q", "Bearer instance-key-123", gotAuth)
		}
		if gotBody["channel"] != "voice" || gotBody["inReplyTo"] != "comm-msg-prev" {
			t.Fatalf("unexpected voice payload: %+v", gotBody)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("phone_call rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if out.IsError {
			t.Fatalf("expected success tool result, got %+v", out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["messageId"] != "comm-msg-call-001" || data["status"] != "accepted" {
			t.Fatalf("unexpected phone_call result: %+v", data)
		}
	})

	t.Run("phone call preserves host gap fallback for older hosts", func(t *testing.T) {
		gotAuth = ""
		gotBody = nil
		voiceStatusCode = http.StatusServiceUnavailable
		voiceResponseBody = `{"error":{"message":"channel not supported"}}`

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
			t.Fatalf("phone_call legacy gap: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("phone_call legacy gap rpc error: %+v", rpc.Error)
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
		if errPayload["code"] != "host_gap" {
			t.Fatalf("expected host_gap, got %+v", errPayload)
		}
		details, _ := errPayload["details"].(map[string]any)
		if details["gap"] != "outbound_voice_not_supported" {
			t.Fatalf("expected outbound voice gap details, got %+v", details)
		}
	})
}
