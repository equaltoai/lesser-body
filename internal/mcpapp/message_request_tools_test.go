package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestMessageRequestLifecycleAcrossActorScopedMCP(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("LESSER_TABLE_NAME", "")
	t.Setenv("JWT_SECRET", "test")
	installTrustConfigIsolation(t)
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	senderToken := newTestToken(t, "test", "sender", []string{"write"})
	recipientToken := newTestToken(t, "test", "recipient", []string{"write"})
	senderAuth := "Bearer " + senderToken
	recipientAuth := "Bearer " + recipientToken

	state := struct {
		sync.Mutex
		firstSent    bool
		accepted     bool
		secondSent   bool
		declinedConv bool
	}{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/statuses":
			if got := r.Header.Get("Authorization"); got != senderAuth {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"wrong sender"}`))
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode status body: %v", err)
			}
			if body["visibility"] != "direct" || !strings.Contains(body["status"].(string), "@recipient") {
				t.Fatalf("direct-message status body = %+v", body)
			}

			state.Lock()
			defer state.Unlock()
			if !state.firstSent {
				state.firstSent = true
				_, _ = w.Write([]byte(`{"id":"dm-1","visibility":"direct"}`))
				return
			}
			if !state.accepted {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Message request pending"}`))
				return
			}
			state.secondSent = true
			_, _ = w.Write([]byte(`{"id":"dm-2","visibility":"direct"}`))

		case r.Method == http.MethodPost && r.URL.Path == "/api/graphql":
			if got := r.Header.Get("Authorization"); got != recipientAuth {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"wrong recipient"}`))
				return
			}
			var op struct {
				OperationName string         `json:"operationName"`
				Variables     map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
				t.Fatalf("decode graphql operation: %v", err)
			}
			state.Lock()
			defer state.Unlock()
			switch op.OperationName {
			case "BodyMessageRequests":
				requests := []string{}
				if state.firstSent && !state.accepted {
					requests = append(requests, messageRequestFixture("conv-1", "sender", "PENDING", strings.Repeat("pending message preview ", 30)))
				}
				if !state.declinedConv {
					requests = append(requests, messageRequestFixture("conv-2", "reviewer", "PENDING", "please review"))
				}
				_, _ = w.Write([]byte(`{"data":{"conversations":[` + strings.Join(requests, ",") + `]}}`))
			case "BodyAcceptMessageRequest":
				if op.Variables["conversationId"] != "conv-1" {
					t.Fatalf("accept conversationId = %#v", op.Variables["conversationId"])
				}
				state.accepted = true
				_, _ = w.Write([]byte(`{"data":{"acceptMessageRequest":` + messageRequestFixture("conv-1", "sender", "ACCEPTED", "hello recipient") + `}}`))
			case "BodyDeclineMessageRequest":
				if op.Variables["conversationId"] != "conv-2" {
					t.Fatalf("decline conversationId = %#v", op.Variables["conversationId"])
				}
				state.declinedConv = true
				_, _ = w.Write([]byte(`{"data":{"declineMessageRequest":true}}`))
			default:
				t.Fatalf("unexpected GraphQL operation %q", op.OperationName)
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/conversations/lookup":
			if got := r.Header.Get("Authorization"); got != recipientAuth {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"wrong recipient"}`))
				return
			}
			if got := r.URL.Query().Get("counterpart"); got != "sender" {
				t.Fatalf("counterpart lookup = %q", got)
			}
			state.Lock()
			defer state.Unlock()
			if !state.accepted {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(socialConversationDetailFixtureJSON("conv-1", "", "", "")))

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/conversations":
			if got := r.Header.Get("Authorization"); got != senderAuth && got != recipientAuth {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"wrong actor"}`))
				return
			}
			state.Lock()
			defer state.Unlock()
			if !state.accepted {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"conv-1","unread":true,"accounts":[{"id":"acct-sender","acct":"sender@example.test"}],"last_status":{"id":"dm-1","content":"hello recipient","visibility":"direct"}}]`))

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	senderSession := initializeActorSession(t, env, app, "/mcp/sender", senderAuth)
	recipientSession := initializeActorSession(t, env, app, "/mcp/recipient", recipientAuth)

	first := callActorTool(t, env, app, "/mcp/sender", senderAuth, senderSession, 10, "post_create", map[string]any{
		"content":    "@recipient hello",
		"visibility": "direct",
	})
	if first.IsError {
		t.Fatalf("first direct message failed: %+v", first.StructuredContent)
	}

	blocked := callActorTool(t, env, app, "/mcp/sender", senderAuth, senderSession, 11, "post_create", map[string]any{
		"content":    "@recipient second before accept",
		"visibility": "direct",
	})
	if !blocked.IsError {
		t.Fatalf("second direct message should preserve Lesser's pending-request rejection: %+v", blocked.StructuredContent)
	}
	blockedError, _ := blocked.StructuredContent["error"].(map[string]any)
	blockedDetails, _ := blockedError["details"].(map[string]any)
	blockedAPIError, _ := blockedDetails["apiError"].(map[string]any)
	if blockedAPIError["error"] != "Message request pending" {
		t.Fatalf("pending-request rejection lost upstream reason: %+v", blocked.StructuredContent)
	}

	focusedPending := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 12, "direct_messages_read", map[string]any{
		"counterpart": "sender",
	})
	if !focusedPending.IsError {
		t.Fatalf("focused pending read must require the recipient's explicit decision: %+v", focusedPending.StructuredContent)
	}
	focusedError, _ := focusedPending.StructuredContent["error"].(map[string]any)
	focusedDetails, _ := focusedError["details"].(map[string]any)
	if focusedError["code"] != "message_request_pending" || focusedDetails["conversationId"] != "conv-1" {
		t.Fatalf("focused pending request = %+v", focusedPending.StructuredContent)
	}
	focusedAction, _ := focusedDetails["nextAction"].(map[string]any)
	focusedActionArgs, _ := focusedAction["arguments"].(map[string]any)
	if focusedAction["tool"] != "message_request_accept" || focusedActionArgs["conversationId"] != "conv-1" {
		t.Fatalf("focused pending action = %+v", focusedAction)
	}

	listed := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 13, "message_requests_list", map[string]any{"limit": 10})
	listedData := requireToolData(t, listed)
	if listedData["folder"] != "REQUESTS" || listedData["count"] != float64(2) {
		t.Fatalf("message request list = %+v", listedData)
	}
	requests, _ := listedData["requests"].([]any)
	firstRequest := requests[0].(map[string]any)
	if firstRequest["conversationId"] != "conv-1" || firstRequest["requestState"] != "PENDING" {
		t.Fatalf("first request = %+v", firstRequest)
	}
	lastMessage := firstRequest["lastMessageRef"].(map[string]any)
	if lastMessage["contentTruncated"] != true || strings.Contains(lastMessage["contentPreview"].(string), strings.Repeat("pending message preview ", 10)) {
		t.Fatalf("message request preview was not bounded: %+v", lastMessage)
	}

	accepted := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 14, "message_request_accept", map[string]any{"conversationId": "conv-1"})
	acceptedData := requireToolData(t, accepted)
	if acceptedData["requestState"] != "ACCEPTED" || acceptedData["decision"] != "accepted" {
		t.Fatalf("accept result = %+v", acceptedData)
	}

	for _, actor := range []struct {
		path, authHeader, sessionID string
	}{
		{path: "/mcp/sender", authHeader: senderAuth, sessionID: senderSession},
		{path: "/mcp/recipient", authHeader: recipientAuth, sessionID: recipientSession},
	} {
		visible := callActorTool(t, env, app, actor.path, actor.authHeader, actor.sessionID, 15, "conversations_read", map[string]any{"limit": 10})
		data := requireToolData(t, visible)
		if data["count"] != float64(1) {
			t.Fatalf("accepted conversation not visible to %s: %+v", actor.path, data)
		}
	}
	focusedAccepted := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 16, "direct_messages_read", map[string]any{
		"counterpart": "sender",
	})
	if focusedAccepted.IsError {
		t.Fatalf("focused direct read failed after explicit acceptance: %+v", focusedAccepted.StructuredContent)
	}
	if data := requireToolData(t, focusedAccepted); data["id"] != "conv-1" {
		t.Fatalf("focused accepted conversation = %+v", data)
	}

	flowing := callActorTool(t, env, app, "/mcp/sender", senderAuth, senderSession, 17, "post_create", map[string]any{
		"content":    "@recipient second after accept",
		"visibility": "direct",
	})
	if flowing.IsError {
		t.Fatalf("second direct message remained pending after accept: %+v", flowing.StructuredContent)
	}

	declined := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 18, "message_request_decline", map[string]any{"conversationId": "conv-2"})
	declinedData := requireToolData(t, declined)
	if declinedData["requestState"] != "DECLINED" || declinedData["success"] != true {
		t.Fatalf("decline result = %+v", declinedData)
	}

	empty := callActorTool(t, env, app, "/mcp/recipient", recipientAuth, recipientSession, 19, "message_requests_list", map[string]any{})
	if data := requireToolData(t, empty); data["count"] != float64(0) {
		t.Fatalf("resolved requests remained in request folder: %+v", data)
	}

	state.Lock()
	defer state.Unlock()
	if !state.secondSent || !state.accepted || !state.declinedConv {
		t.Fatalf("upstream lifecycle state: accepted=%t secondSent=%t declined=%t", state.accepted, state.secondSent, state.declinedConv)
	}
}

func initializeActorSession(t testing.TB, env *testkit.Env, app *apptheory.App, path string, authHeader string) string {
	t.Helper()
	resp := invokeJSONAtPath(t, env, app, path, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("initialize %s: status=%d body=%s", path, resp.Status, string(resp.Body))
	}
	values := resp.Headers["mcp-session-id"]
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		t.Fatalf("initialize %s missing session id: %+v", path, resp.Headers)
	}
	return values[0]
}

func callActorTool(t testing.TB, env *testkit.Env, app *apptheory.App, path string, authHeader string, sessionID string, id int, name string, args map[string]any) mcpruntime.ToolResult {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("marshal %s args: %v", name, err)
	}
	resp := invokeJSONAtPath(t, env, app, path, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params})
	if resp.Status != http.StatusOK {
		t.Fatalf("%s %s: status=%d body=%s", path, name, resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("decode %s rpc: %v", name, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s rpc error: %+v", name, rpc.Error)
	}
	var result mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return result
}

func requireToolData(t testing.TB, result mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result.StructuredContent)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("missing structuredContent.data: %+v", result.StructuredContent)
	}
	return data
}

func messageRequestFixture(conversationID string, username string, state string, content string) string {
	b, _ := json.Marshal(map[string]any{
		"id":     conversationID,
		"unread": true,
		"accounts": []any{map[string]any{
			"id":          "acct-" + username,
			"username":    username,
			"domain":      "example.test",
			"displayName": strings.ToUpper(username[:1]) + username[1:],
		}},
		"viewerMetadata": map[string]any{
			"requestState": state,
			"requestedAt":  "2026-07-26T12:00:00Z",
			"acceptedAt":   map[bool]any{true: "2026-07-26T12:01:00Z", false: nil}[state == "ACCEPTED"],
		},
		"lastStatus": map[string]any{
			"id":        "status-" + conversationID,
			"content":   content,
			"createdAt": "2026-07-26T12:00:00Z",
		},
		"createdAt": "2026-07-26T12:00:00Z",
		"updatedAt": "2026-07-26T12:01:00Z",
	})
	return string(b)
}
