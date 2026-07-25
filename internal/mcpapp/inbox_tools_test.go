package mcpapp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
	"github.com/theory-cloud/apptheory/v2/testkit"

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
	const canonicalEmail = "agent-alice.simulacrum@lessersoul.ai"
	var gotAuth []string
	var listQueries []string
	var statePaths []string
	var contentPathCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"email":{"address":"` + canonicalEmail + `","capabilities":["email-read","email-manage"]},"phone":{"capabilities":["sms-read","voice-receive"],"entitlement":{"state":"provisioned"}}},"contactPreferences":{},"boundaries":[],` + boundBodyPolicyJSON("communication.email.read", "communication.email.manage", "communication.sms.read", "communication.voice.read") + `}`))
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
				_, _ = w.Write([]byte(mailboxEmailListFixture(agentID, canonicalEmail, 10)))
			case "sms":
				_, _ = w.Write([]byte(`{"messages":[{"messageRef":"comm-delivery-sms","deliveryId":"comm-delivery-sms","messageId":"comm-msg-sms","threadId":"comm-thread-sms","direction":"inbound","channelType":"sms","status":"delivered","from":{"number":"+15550142"},"preview":"sms preview","content":{"available":true},"state":{"read":false,"archived":false,"deleted":false},"createdAt":"2026-03-04T12:05:00Z"}],"count":1,"hasMore":false}`))
			case "voice":
				_, _ = w.Write([]byte(`{"messages":[{"messageRef":"comm-delivery-voice","deliveryId":"comm-delivery-voice","messageId":"comm-msg-voice","threadId":"comm-thread-voice","direction":"inbound","channelType":"voice","status":"delivered","from":{"number":"+15550143"},"preview":"voice preview","content":{"available":true},"state":{"read":false,"archived":false,"deleted":false},"createdAt":"2026-03-04T12:10:00Z"}],"count":1,"hasMore":false}`))
			default:
				_, _ = w.Write([]byte(`{"messages":[],"count":0,"hasMore":false}`))
			}
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email" && r.Method == http.MethodGet:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"message":{"messageRef":"comm-delivery-email","deliveryId":"comm-delivery-email","messageId":"comm-msg-email","threadId":"comm-thread-email","direction":"inbound","channelType":"email","status":"delivered","from":{"address":"alice@example.com"},"to":{"address":"` + canonicalEmail + `"},"subject":"Hi","preview":"Hello preview","content":{"available":true},"state":{"read":false,"archived":false,"deleted":false},"createdAt":"2026-03-04T12:00:00Z"}}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email/content" && r.Method == http.MethodGet:
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			contentPathCalls++
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

	responseBytes := map[int]int{}
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
		responseBytes[id] = len(resp.Body)
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("%s rpc error: %+v", name, rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		if _, ok := out.StructuredContent["data"]; ok {
			t.Fatalf("%s should expose flat structuredContent, got %+v", name, out.StructuredContent)
		}
		return out.StructuredContent
	}

	emailRead := callTool(2, "email_read", map[string]any{"folder": "inbox", "limit": 10, "unreadOnly": true})
	messages, _ := emailRead["messages"].([]any)
	if len(messages) != 10 {
		t.Fatalf("expected 10 email messages, got %+v", messages)
	}
	msg, _ := messages[0].(map[string]any)
	if msg["messageId"] != "comm-delivery-email-00" || msg["hostMessageId"] != "comm-msg-email-00" || msg["body"] != "Hello preview 00" || msg["bodyIsPreview"] != true {
		t.Fatalf("unexpected email message: %+v", msg)
	}
	if to, _ := msg["to"].(map[string]any); to["address"] != canonicalEmail {
		t.Fatalf("mailbox list should preserve canonical instance-scoped recipient as opaque email string, got %+v", to)
	}
	for _, alias := range []string{"messageId", "messageRef", "deliveryId", "hostMessageId"} {
		if value, _ := msg[alias].(string); value == "" {
			t.Fatalf("expected mailbox compatibility alias %q to remain populated, got %+v", alias, msg)
		}
	}
	if _, ok := msg["raw"]; ok {
		t.Fatalf("raw field should be absent by default: %+v", msg)
	}
	if _, ok := msg["_raw"]; ok {
		t.Fatalf("_raw field should be absent by default: %+v", msg)
	}
	if emailRead["nextCursor"] != "cursor-2" || emailRead["nextSince"] != "cursor-2" {
		t.Fatalf("expected cursor aliases, got %+v", emailRead)
	}
	if strings.Contains(mustJSON(t, emailRead), "FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR") {
		t.Fatalf("default email_read must not inline full mailbox body: %+v", emailRead)
	}

	emailReadRaw := callTool(20, "email_read", map[string]any{"folder": "inbox", "limit": 10, "include_raw": true})
	rawMessages, _ := emailReadRaw["messages"].([]any)
	rawMsg, _ := rawMessages[0].(map[string]any)
	if _, ok := rawMsg["_raw"].(map[string]any); !ok {
		t.Fatalf("expected include_raw=true to expose _raw, got %+v", rawMsg)
	}
	if _, ok := rawMsg["raw"]; ok {
		t.Fatalf("include_raw=true should use _raw, not raw: %+v", rawMsg)
	}
	if strings.Contains(mustJSON(t, emailReadRaw), "FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR") {
		t.Fatalf("include_raw list response must sanitize full mailbox body: %+v", emailReadRaw)
	}
	assertMCPPayloadIncrease(t,
		"email_read default mailbox preview large fixture",
		responseBytes[2],
		"email_read include_raw expanded fixture",
		responseBytes[20],
	)

	emailReadStandard := callTool(24, "email_read", map[string]any{"folder": "inbox", "limit": 10, "view": "standard", "unreadOnly": true})
	standardMessages, _ := emailReadStandard["messages"].([]any)
	standardMsg, _ := standardMessages[0].(map[string]any)
	if standardMsg["messageId"] != "comm-delivery-email-00" || standardMsg["messageRef"] != "comm-delivery-email-00" || standardMsg["deliveryId"] != "comm-delivery-email-00" || standardMsg["hostMessageId"] != "comm-msg-email-00" {
		t.Fatalf("view=standard should preserve mailbox aliases, got %+v", standardMsg)
	}
	if standardMsg["channel"] != "email" || standardMsg["channelType"] != "email" || standardMsg["body"] != "Hello preview 00" || standardMsg["bodyIsPreview"] != true {
		t.Fatalf("view=standard should preserve channel/body preview compatibility, got %+v", standardMsg)
	}
	if emailReadStandard["nextCursor"] != "cursor-2" || emailReadStandard["nextSince"] != "cursor-2" || emailReadStandard["unreadOnly"] != true {
		t.Fatalf("view=standard should preserve cursor/filter echo fields, got %+v", emailReadStandard)
	}
	if !reflect.DeepEqual(emailRead, emailReadStandard) {
		t.Fatalf("email_read omitted view must remain equivalent to view=standard: default=%+v standard=%+v", emailRead, emailReadStandard)
	}

	emailReadCompact := callTool(25, "email_read", map[string]any{"folder": "inbox", "limit": 10, "view": "compact"})
	assertMCPPayloadBudget(t, "email_read compact mailbox fixture", responseBytes[25], 8000)
	compactMessages, _ := emailReadCompact["messages"].([]any)
	compactMsg, _ := compactMessages[0].(map[string]any)
	if emailReadCompact["view"] != "compact" || len(compactMessages) != 10 {
		t.Fatalf("unexpected compact list metadata: %+v", emailReadCompact)
	}
	if compactMsg["messageRef"] != "comm-delivery-email-00" || compactMsg["channelType"] != "email" || compactMsg["preview"] != "Hello preview 00" {
		t.Fatalf("unexpected compact message ref/preview: %+v", compactMsg)
	}
	if _, ok := compactMsg["body"]; ok {
		t.Fatalf("compact message must not duplicate preview into body: %+v", compactMsg)
	}
	for _, forbidden := range []string{"messageId", "deliveryId", "hostMessageId", "channel", "_raw"} {
		if _, ok := compactMsg[forbidden]; ok {
			t.Fatalf("compact message should omit %s, got %+v", forbidden, compactMsg)
		}
	}
	expand, _ := compactMsg["expand"].(map[string]any)
	metadataExpand, _ := expand["metadata"].(map[string]any)
	metadataArgs, _ := metadataExpand["arguments"].(map[string]any)
	contentExpand, _ := expand["content"].(map[string]any)
	contentArgs, _ := contentExpand["arguments"].(map[string]any)
	if metadataExpand["tool"] != "email_get" || metadataArgs["messageId"] != "comm-delivery-email-00" {
		t.Fatalf("unexpected compact metadata expansion: %+v", metadataExpand)
	}
	if contentExpand["tool"] != "email_get_content" || contentArgs["messageId"] != "comm-delivery-email-00" {
		t.Fatalf("unexpected compact content expansion: %+v", contentExpand)
	}
	if strings.Contains(mustJSON(t, emailReadCompact), "FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR") || strings.Contains(mustJSON(t, emailReadCompact), "mailbox-debug") {
		t.Fatalf("compact email_read leaked full/raw fixture data: %+v", emailReadCompact)
	}

	emailReadFull := callTool(26, "email_read", map[string]any{"folder": "inbox", "limit": 10, "view": "full"})
	fullMessages, _ := emailReadFull["messages"].([]any)
	fullMsg, _ := fullMessages[0].(map[string]any)
	if emailReadFull["view"] != "full" {
		t.Fatalf("view=full should be explicit, got %+v", emailReadFull)
	}
	if _, ok := fullMsg["_raw"].(map[string]any); !ok {
		t.Fatalf("view=full should expose sanitized _raw metadata, got %+v", fullMsg)
	}
	if !strings.Contains(mustJSON(t, emailReadFull), "mailbox-debug") {
		t.Fatalf("view=full should preserve verbose audit metadata")
	}
	if strings.Contains(mustJSON(t, emailReadFull), "FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR") {
		t.Fatalf("view=full list response must not inline full mailbox body: %+v", emailReadFull)
	}

	listCallsBeforeInvalidView := len(listQueries)
	invalidView := callToolAllowError(t, env, app, authHeader, sessionID, 27, "email_read", map[string]any{"folder": "inbox", "view": "summary"})
	if !invalidView.IsError {
		t.Fatalf("unsupported view should return a tool error")
	}
	invalidViewPayload, _ := invalidView.StructuredContent["error"].(map[string]any)
	if invalidViewPayload["code"] != "invalid_request" || invalidViewPayload["status"] != float64(400) {
		t.Fatalf("unexpected unsupported view error: %+v", invalidViewPayload)
	}
	invalidNumber := callToolAllowError(t, env, app, authHeader, sessionID, 28, "email_read", map[string]any{"folder": "inbox", "view": 7})
	if !invalidNumber.IsError {
		t.Fatalf("non-string view should return a tool error")
	}
	if len(listQueries) != listCallsBeforeInvalidView {
		t.Fatalf("invalid view calls should fail before lesser-host mailbox list, before=%d after=%d", listCallsBeforeInvalidView, len(listQueries))
	}
	t.Logf("email_read payload bytes default=%d standard=%d compact=%d full=%d include_raw=%d",
		responseBytes[2], responseBytes[24], responseBytes[25], responseBytes[26], responseBytes[20])

	smsRead := callTool(3, "sms_read", map[string]any{"limit": 10})
	smsMessages, _ := smsRead["messages"].([]any)
	if len(smsMessages) != 1 {
		t.Fatalf("expected 1 sms message, got %+v", smsMessages)
	}
	smsReadRaw := callTool(21, "sms_read", map[string]any{"limit": 10, "include_raw": true})
	smsRawMessages, _ := smsReadRaw["messages"].([]any)
	smsRawMsg, _ := smsRawMessages[0].(map[string]any)
	if _, ok := smsRawMsg["_raw"].(map[string]any); !ok {
		t.Fatalf("expected sms include_raw=true to expose _raw, got %+v", smsRawMsg)
	}

	voicemailReadRaw := callTool(22, "voicemail_read", map[string]any{"limit": 10, "include_raw": true})
	voicemailRawMessages, _ := voicemailReadRaw["messages"].([]any)
	voicemailRawMsg, _ := voicemailRawMessages[0].(map[string]any)
	if _, ok := voicemailRawMsg["_raw"].(map[string]any); !ok {
		t.Fatalf("expected voicemail include_raw=true to expose _raw, got %+v", voicemailRawMsg)
	}

	emailSearch := callTool(4, "email_search", map[string]any{"query": "alice", "limit": 5})
	if emailSearch["strategy"] != "host bounded metadata/preview query" {
		t.Fatalf("expected host search strategy, got %+v", emailSearch)
	}

	emailGet := callTool(5, "email_get", map[string]any{"messageId": "comm-delivery-email"})
	if _, ok := emailGet["message"].(map[string]any); !ok {
		t.Fatalf("expected email_get message, got %+v", emailGet)
	}
	emailGetMessage, _ := emailGet["message"].(map[string]any)
	if to, _ := emailGetMessage["to"].(map[string]any); to["address"] != canonicalEmail {
		t.Fatalf("email_get should preserve canonical instance-scoped recipient as opaque email string, got %+v", to)
	}
	emailGetRaw := callTool(23, "email_get", map[string]any{"messageId": "comm-delivery-email", "include_raw": true})
	emailGetRawMessage, _ := emailGetRaw["message"].(map[string]any)
	if _, ok := emailGetRawMessage["_raw"].(map[string]any); !ok {
		t.Fatalf("expected email_get include_raw=true to expose _raw, got %+v", emailGetRawMessage)
	}

	if contentPathCalls != 0 {
		t.Fatalf("email_read/email_get metadata paths must not fetch full content, content calls=%d", contentPathCalls)
	}
	content := callTool(6, "email_get_content", map[string]any{"messageId": "comm-delivery-email"})
	if content["body"] != "Full body" || content["messageId"] != "comm-delivery-email" {
		t.Fatalf("unexpected content response: %+v", content)
	}
	if contentPathCalls != 1 {
		t.Fatalf("email_get_content should be the only full-body path, content calls=%d", contentPathCalls)
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

func callToolAllowError(t *testing.T, env *testkit.Env, app *apptheory.App, authHeader string, sessionID string, id int, name string, arguments map[string]any) mcpruntime.ToolResult {
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
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal %s response: %v", name, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s rpc error: %+v", name, rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	return out
}

func mailboxEmailListFixture(agentID string, toAddress string, count int) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, `{"instanceSlug":"inst1","agentId":%q,"messages":[`, agentID)
	for i := range count {
		if i > 0 {
			b.WriteByte(',')
		}
		suffix := fmt.Sprintf("%02d", i)
		_, _ = fmt.Fprintf(&b, `{
			"messageRef":"comm-delivery-email-%s",
			"deliveryId":"comm-delivery-email-%s",
			"messageId":"comm-msg-email-%s",
			"threadId":"comm-thread-email",
			"direction":"inbound",
			"channelType":"email",
			"status":"delivered",
			"from":{"address":"alice-%s@example.com","displayName":"Alice %s"},
			"to":{"address":%q},
			"subject":"Hi %s",
			"preview":"Hello preview %s",
			"body":"FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR %s %s",
			"content":{"available":true,"bytes":2048,"mimeType":"text/plain","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","contentHref":"/content","body":"FULL_MAILBOX_BODY_SHOULD_NOT_APPEAR content %s"},
			"state":{"read":false,"archived":false,"deleted":false},
			"createdAt":"2026-03-04T12:00:00Z",
			"debugPayload":{"large":"%s"}
		}`, suffix, suffix, suffix, suffix, suffix, toAddress, suffix, suffix, suffix, strings.Repeat("full mailbox body ", 120), suffix, strings.Repeat("mailbox-debug ", 80))
	}
	_, _ = fmt.Fprintf(&b, `],"count":%d,"hasMore":true,"nextCursor":"cursor-2"}`, count)
	return b.String()
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}
	return string(b)
}
