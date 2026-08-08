package mcpapp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestCancellationNotificationCancelsLesserReadTool(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	started := make(chan struct{})
	canceled := make(chan struct{})
	var closeStarted sync.Once
	var closeCanceled sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/timelines/home":
			closeStarted.Do(func() { close(started) })
			<-r.Context().Done()
			closeCanceled.Do(func() { close(canceled) })
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
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	sessionID := initializeCancellationSession(t, env, app, authHeader)

	callParams, _ := json.Marshal(map[string]any{
		"name": "timeline_read",
		"arguments": map[string]any{
			"timeline": "home",
			"limit":    20,
		},
	})

	resultCh := make(chan apptheory.Response, 1)
	go func() {
		resultCh <- env.Invoke(context.Background(), app, apptheory.Request{
			Method: "POST",
			Path:   "/mcp/agent1",
			Headers: map[string][]string{
				"authorization":  {authHeader},
				"content-type":   {"application/json"},
				"accept":         {"application/json, text/event-stream"},
				"mcp-session-id": {sessionID},
			},
			Body: mustMarshalJSONForTest(t, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      "read-cancel",
				Method:  "tools/call",
				Params:  callParams,
			}),
		})
	}()

	waitForSignal(t, started, "timeline_read upstream request to start")
	sendCancellationNotification(t, env, app, authHeader, sessionID, "read-cancel")
	waitForSignal(t, canceled, "timeline_read upstream request context to cancel")

	select {
	case resp := <-resultCh:
		if resp.Status != http.StatusOK {
			t.Fatalf("timeline_read response status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal timeline_read response: %v body=%s", err, string(resp.Body))
		}
		_, payload := requireToolErrorResult(t, &rpc)
		if payload["code"] != "request_cancelled" {
			t.Fatalf("canceled timeline_read error = %+v, want request_cancelled", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeline_read did not return promptly after cancellation")
	}
}

func TestCancellationNotificationCancelsStreamingSmsSend(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var closeStarted sync.Once
	var closeCanceled sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-eve")))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-eve","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"phone":{"number":"+15550142","capabilities":["sms-send"],"verified":true,"entitlement":{"state":"provisioned"}}},"contactPreferences":{},` + boundBodyPolicyJSON("communication.sms.send") + `}`))
		case "/api/v1/soul/comm/send":
			_, _ = io.ReadAll(r.Body)
			closeStarted.Do(func() { close(started) })
			select {
			case <-r.Context().Done():
				closeCanceled.Do(func() { close(canceled) })
			case <-release:
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()
	defer close(release)

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"write"})
	sessionID := initializeCancellationSession(t, env, app, authHeader)

	callParams, _ := json.Marshal(map[string]any{
		"name": "sms_send",
		"arguments": map[string]any{
			"to":        "+1 (555) 0143",
			"body":      "Cancel this send.",
			"messageId": "comm-msg-cancel",
		},
		"_meta": map[string]any{"progressToken": "pt-cancel"},
	})

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "POST",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":  {authHeader},
			"content-type":   {"application/json"},
			"accept":         {"application/json, text/event-stream"},
			"mcp-session-id": {sessionID},
		},
		Body: mustMarshalJSONForTest(t, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      "sms-cancel",
			Method:  "tools/call",
			Params:  callParams,
		}),
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("sms_send response status=%d body=%s", resp.Status, string(resp.Body))
	}
	if resp.BodyReader == nil {
		t.Fatal("expected streaming response body")
	}

	streamDone := make(chan struct{})
	go func() {
		_, _ = io.ReadAll(resp.BodyReader)
		close(streamDone)
	}()

	waitForSignal(t, started, "sms_send host request to start")
	sendCancellationNotification(t, env, app, authHeader, sessionID, "sms-cancel")
	waitForSignal(t, canceled, "sms_send host request context to cancel")
	waitForSignal(t, streamDone, "sms_send stream to close after cancellation")
}

func initializeCancellationSession(t testing.TB, env *testkit.Env, app *apptheory.App, authHeader string) string {
	t.Helper()
	initResp := invokeJSONAtPath(t, env, app, "/mcp/agent1", map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != http.StatusOK {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	ids := initResp.Headers["mcp-session-id"]
	if len(ids) == 0 || ids[0] == "" {
		t.Fatalf("initialize did not return mcp-session-id")
	}
	return ids[0]
}

func sendCancellationNotification(t testing.TB, env *testkit.Env, app *apptheory.App, authHeader string, sessionID string, requestID string) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{
		"requestId": requestID,
		"reason":    "test cancellation",
	})
	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "POST",
		Path:   "/mcp/agent1",
		Headers: map[string][]string{
			"authorization":  {authHeader},
			"content-type":   {"application/json"},
			"accept":         {"application/json, text/event-stream"},
			"mcp-session-id": {sessionID},
		},
		Body: mustMarshalJSONForTest(t, &mcpruntime.Request{
			JSONRPC: "2.0",
			Method:  "notifications/cancelled",
			Params:  params,
		}),
	})
	if resp.Status != http.StatusAccepted {
		t.Fatalf("notifications/cancelled status=%d body=%s", resp.Status, string(resp.Body))
	}
}

func waitForSignal(t testing.TB, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
