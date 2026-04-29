package mcpapp_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
	mcptestkit "github.com/theory-cloud/apptheory/testkit/mcp"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestSmsSendStreamingEmitsProgressAndFinalResult(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/souls/mine":
			_, _ = w.Write([]byte(`{"souls":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-dana"},"binding_state":"bound","binding":{"agent_username":"agent1"}}]}`))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-dana","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"phone":{"number":"+15550142","capabilities":["sms-send"],"verified":true}},"contactPreferences":{}}`))
		case "/api/v1/soul/comm/send":
			time.Sleep(10 * time.Millisecond)
			_, _ = w.Write([]byte(`{"messageId":"comm-msg-sms-002","status":"queued"}`))
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
	token := newTestToken(t, "test", "agent1", []string{"write"})
	authHeader := "Bearer " + token

	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "sms_send",
		"arguments": map[string]any{
			"to":        "+1 (555) 0143",
			"body":      "On it.",
			"messageId": "comm-msg-prev",
		},
		"_meta": map[string]any{
			"progressToken": "pt-sms-1",
		},
	})

	resp := env.Invoke(t.Context(), app, apptheory.Request{
		Method: "POST",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":  {authHeader},
			"content-type":   {"application/json"},
			"accept":         {"text/event-stream"},
			"mcp-session-id": {sessionID},
		},
		Body: mustMarshalJSONForTest(t, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      2,
			Method:  "tools/call",
			Params:  callParams,
		}),
	})
	if resp.Status != 200 {
		t.Fatalf("tools/call stream: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if resp.BodyReader == nil {
		t.Fatalf("expected streaming body reader")
	}

	reader := bufio.NewReader(resp.BodyReader)
	firstMsg, err := mcptestkit.ReadSSEMessage(reader)
	if err != nil {
		t.Fatalf("read first SSE frame: %v", err)
	}
	firstFrame := string(firstMsg.Data)
	if !strings.Contains(firstFrame, `"method":"notifications/progress"`) {
		t.Fatalf("expected first frame progress notification, got:\n%s", firstFrame)
	}
	if !strings.Contains(firstFrame, `"progressToken":"pt-sms-1"`) {
		t.Fatalf("expected first frame progress token, got:\n%s", firstFrame)
	}
	if !strings.Contains(firstFrame, `"message":"sending sms"`) {
		t.Fatalf("expected send progress message, got:\n%s", firstFrame)
	}

	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read rest of SSE stream: %v", err)
	}
	all := firstFrame + string(rest)
	if !strings.Contains(all, `"message":"sms queued"`) {
		t.Fatalf("expected queued progress message, got:\n%s", all)
	}
	if !strings.Contains(all, `"messageId":"comm-msg-sms-002"`) {
		t.Fatalf("expected final result payload, got:\n%s", all)
	}
	if !strings.Contains(all, `"idempotencyKey":"comm-msg-prev"`) {
		t.Fatalf("expected idempotency key in final stream result, got:\n%s", all)
	}
}

func mustMarshalJSONForTest(t testing.TB, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}
