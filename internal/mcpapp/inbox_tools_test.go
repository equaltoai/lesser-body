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

func TestLBM3_InboxToolsUseHostMailbox(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0x1111111111111111111111111111111111111111111111111111111111111111"
	var gotAuth []string
	var listQueries []string
	var statePaths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/souls/mine":
			_, _ = w.Write([]byte(`{"souls":[{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"}}]}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{},"contactPreferences":{},"boundaries":[]}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages" && r.Method == http.MethodGet:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			listQueries = append(listQueries, r.URL.RawQuery)
			channel := r.URL.Query().Get("channelType")
			query := r.URL.Query().Get("query")
			switch channel {
			case "email":
				if query != "" && !strings.EqualFold(query, "alice") {
					_, _ = w.Write([]byte(`{"messages":[],"count":0,"hasMore":false}`))
					return
				}
				_, _ = w.Write([]byte(`{
					"instanceSlug":"inst1",
					"agentId":"` + agentID + `",
					"messages":[{
						"messageRef":"comm-delivery-email",
						"deliveryId":"comm-delivery-email",
						"messageId":"comm-msg-email",
						"threadId":"comm-thread-email",
						"direction":"inbound",
						"channelType":"email",
						"status":"delivered",
						"from":{"address":"alice@example.com"},
						"to":{"address":"agent@example.com"},
						"subject":"Hi",
						"preview":"Hello preview",
						"content":{"available":true,"bytes":11,"mimeType":"text/plain","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","contentHref":"/content"},
						"state":{"read":false,"archived":false,"deleted":false},
						"createdAt":"2026-03-04T12:00:00Z"
					}],
					"count":1,
					"hasMore":true,
					"nextCursor":"cursor-2"
				}`))
			case "sms":
				_, _ = w.Write([]byte(`{"messages":[{"messageRef":"comm-delivery-sms","deliveryId":"comm-delivery-sms","messageId":"comm-msg-sms","threadId":"comm-thread-sms","direction":"inbound","channelType":"sms","status":"delivered","from":{"number":"+15550142"},"preview":"sms preview","content":{"available":true},"state":{"read":false,"archived":false,"deleted":false},"createdAt":"2026-03-04T12:05:00Z"}],"count":1,"hasMore":false}`))
			default:
				_, _ = w.Write([]byte(`{"messages":[],"count":0,"hasMore":false}`))
			}
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email" && r.Method == http.MethodGet:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"message":{"messageRef":"comm-delivery-email","deliveryId":"comm-delivery-email","messageId":"comm-msg-email","threadId":"comm-thread-email","direction":"inbound","channelType":"email","status":"delivered","from":{"address":"alice@example.com"},"to":{"address":"agent@example.com"},"subject":"Hi","preview":"Hello preview","content":{"available":true},"state":{"read":false,"archived":false,"deleted":false},"createdAt":"2026-03-04T12:00:00Z"}}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email/content" && r.Method == http.MethodGet:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"instanceSlug":"inst1","agentId":"` + agentID + `","messageRef":"comm-delivery-email","deliveryId":"comm-delivery-email","messageId":"comm-msg-email","contentType":"text/plain","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":11,"body":"Full body"}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email/") && r.Method == http.MethodPost:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			statePaths = append(statePaths, r.URL.Path)
			state := `{"read":true,"archived":false,"deleted":false}`
			if strings.HasSuffix(r.URL.Path, "/archive") {
				state = `{"read":false,"archived":true,"deleted":false}`
			}
			_, _ = w.Write([]byte(`{"message":{"messageRef":"comm-delivery-email","deliveryId":"comm-delivery-email","messageId":"comm-msg-email","threadId":"comm-thread-email","direction":"inbound","channelType":"email","status":"delivered","content":{"available":true},"state":` + state + `,"createdAt":"2026-03-04T12:00:00Z"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found","path":"` + r.URL.Path + `","body":"` + string(body) + `"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

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

	callTool := func(id int, name string, arguments map[string]any) map[string]any {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": name, "arguments": arguments})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("%s rpc error: %+v", name, rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		data, _ := out.StructuredContent["data"].(map[string]any)
		return data
	}

	emailRead := callTool(2, "email_read", map[string]any{"folder": "inbox", "limit": 10, "unreadOnly": true})
	messages, _ := emailRead["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected 1 email message, got %+v", messages)
	}
	msg, _ := messages[0].(map[string]any)
	if msg["messageId"] != "comm-delivery-email" || msg["hostMessageId"] != "comm-msg-email" || msg["body"] != "Hello preview" || msg["bodyIsPreview"] != true {
		t.Fatalf("unexpected email message: %+v", msg)
	}
	if emailRead["nextCursor"] != "cursor-2" || emailRead["nextSince"] != "cursor-2" {
		t.Fatalf("expected cursor aliases, got %+v", emailRead)
	}

	smsRead := callTool(3, "sms_read", map[string]any{"limit": 10})
	smsMessages, _ := smsRead["messages"].([]any)
	if len(smsMessages) != 1 {
		t.Fatalf("expected 1 sms message, got %+v", smsMessages)
	}

	emailSearch := callTool(4, "email_search", map[string]any{"query": "alice", "limit": 5})
	if emailSearch["strategy"] != "host bounded metadata/preview query" {
		t.Fatalf("expected host search strategy, got %+v", emailSearch)
	}

	emailGet := callTool(5, "email_get", map[string]any{"messageId": "comm-delivery-email"})
	if _, ok := emailGet["message"].(map[string]any); !ok {
		t.Fatalf("expected email_get message, got %+v", emailGet)
	}

	content := callTool(6, "email_get_content", map[string]any{"messageId": "comm-delivery-email"})
	if content["body"] != "Full body" || content["messageId"] != "comm-delivery-email" {
		t.Fatalf("unexpected content response: %+v", content)
	}

	readState := callTool(7, "email_mark_read", map[string]any{"messageId": "comm-delivery-email"})
	state, _ := readState["state"].(map[string]any)
	if state["read"] != true {
		t.Fatalf("expected read state true, got %+v", readState)
	}

	archive := callTool(8, "email_delete", map[string]any{"messageId": "comm-delivery-email", "action": "archive"})
	if archive["action"] != "archive" {
		t.Fatalf("expected archive action, got %+v", archive)
	}

	params, _ := json.Marshal(map[string]any{"uri": "agent://email/inbox"})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 9, Method: "resources/read", Params: params})
	if resp.Status != 200 {
		t.Fatalf("resources/read agent://email/inbox: status=%d body=%s", resp.Status, string(resp.Body))
	}

	for _, authHeader := range gotAuth {
		if authHeader != "Bearer instance-key-123" {
			t.Fatalf("expected host mailbox Authorization bearer, got %q", authHeader)
		}
	}
	if len(statePaths) < 2 || !strings.HasSuffix(statePaths[0], "/read") || !strings.HasSuffix(statePaths[1], "/archive") {
		t.Fatalf("expected read and archive state paths, got %+v", statePaths)
	}
	combinedQueries := strings.Join(listQueries, "\n")
	for _, want := range []string{"channelType=email", "direction=inbound", "unreadOnly=true", "includeArchived=false", "query=alice", "channelType=sms"} {
		if !strings.Contains(combinedQueries, want) {
			t.Fatalf("expected mailbox query %q in %s", want, combinedQueries)
		}
	}
}
