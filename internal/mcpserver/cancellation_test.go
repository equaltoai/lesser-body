package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func TestSmsSendStreamingHonorsCanceledContext(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	installSoulBindingLookup(t, "agent1", agentID)

	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var closeStarted sync.Once
	var closeCanceled sync.Once
	t.Cleanup(func() { close(release) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-eve")))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-eve","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"phone":{"number":"+15550142","capabilities":["sms-send"],"verified":true,"entitlement":{"state":"provisioned"}}},"contactPreferences":{},` + boundBodyPolicyJSONForTest("communication.sms.send") + `}`))
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

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	baseCtx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", Scopes: []string{"write"}},
	}, "test-token")
	ctx, cancel := context.WithCancel(baseCtx)

	args, err := json.Marshal(map[string]any{
		"to":        "+1 (555) 0143",
		"body":      "Cancel this send.",
		"messageId": "comm-msg-cancel",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	type callResult struct {
		res *mcpruntime.ToolResult
		err error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		res, err := handleSmsSendStreaming(ctx, args, func(mcpruntime.SSEEvent) {})
		resultCh <- callResult{res: res, err: err}
	}()

	waitForMCPServerCancelSignal(t, started, "sms_send host request to start")
	cancel()
	waitForMCPServerCancelSignal(t, canceled, "sms_send host request context to cancel")

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("sms_send returned unexpected error: %v", result.err)
		}
		if result.res == nil || !result.res.IsError {
			t.Fatalf("expected canceled sms_send to return tool error result, got %+v", result.res)
		}
	case <-time.After(time.Second):
		t.Fatal("sms_send did not return promptly after context cancellation")
	}
}

func waitForMCPServerCancelSignal(t testing.TB, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
