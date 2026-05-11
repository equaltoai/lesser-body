package mcpapp_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestLBM0_CommunicationToolSchemasMatchSpec(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	listResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {"Bearer " + token},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})
	if listResp.Status != 200 {
		t.Fatalf("tools/list: status=%d body=%s", listResp.Status, string(listResp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(listResp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("tools/list error: %+v", rpc.Error)
	}

	var result struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &result)
	}

	toolsByName := map[string]mcpruntime.ToolDef{}
	for _, tool := range result.Tools {
		toolsByName[tool.Name] = tool
	}

	for _, name := range []string{
		"email_send",
		"email_read",
		"email_get",
		"email_get_content",
		"email_search",
		"email_reply",
		"email_delete",
		"email_mark_read",
		"email_mark_unread",
		"sms_send",
		"sms_read",
		"voicemail_read",
		"identity_whoami",
		"soul_read",
		"identity_lookup",
		"identity_verify",
	} {
		if _, ok := toolsByName[name]; !ok {
			t.Fatalf("expected tool %q in tools/list", name)
		}
	}

	type schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required,omitempty"`
	}

	mustSchema := func(t *testing.T, def mcpruntime.ToolDef) schema {
		t.Helper()
		var s schema
		if err := json.Unmarshal(def.InputSchema, &s); err != nil {
			t.Fatalf("unmarshal %s input schema: %v", def.Name, err)
		}
		return s
	}

	expectPropType := func(t *testing.T, s schema, prop string, want string) map[string]any {
		t.Helper()
		raw, ok := s.Properties[prop]
		if !ok {
			t.Fatalf("missing property %q", prop)
		}
		m, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("property %q not an object", prop)
		}
		typ, _ := m["type"].(string)
		if typ != want {
			t.Fatalf("property %q type: want %q got %q", prop, want, typ)
		}
		return m
	}

	{
		s := mustSchema(t, toolsByName["email_send"])
		if s.Type != "object" {
			t.Fatalf("email_send schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"to", "subject", "body"}) {
			t.Fatalf("email_send required: want [to subject body], got %#v", s.Required)
		}
		expectPropType(t, s, "to", "string")
		expectPropType(t, s, "subject", "string")
		expectPropType(t, s, "body", "string")
		cc := expectPropType(t, s, "cc", "array")
		ccItems, ok := cc["items"].(map[string]any)
		if !ok {
			t.Fatalf("email_send cc.items missing or invalid")
		}
		if ccItemsType, _ := ccItems["type"].(string); ccItemsType != "string" {
			t.Fatalf("email_send cc.items.type: want string got %q", ccItemsType)
		}
		bcc := expectPropType(t, s, "bcc", "array")
		bccItems, ok := bcc["items"].(map[string]any)
		if !ok {
			t.Fatalf("email_send bcc.items missing or invalid")
		}
		if bccItemsType, _ := bccItems["type"].(string); bccItemsType != "string" {
			t.Fatalf("email_send bcc.items.type: want string got %q", bccItemsType)
		}
		expectPropType(t, s, "replyTo", "string")
		expectPropType(t, s, "messageId", "string")
		expectPropType(t, s, "inReplyTo", "string")
	}

	{
		s := mustSchema(t, toolsByName["soul_read"])
		if s.Type != "object" {
			t.Fatalf("soul_read schema type: want object got %q", s.Type)
		}
		expectPropType(t, s, "query", "string")
		expectPropType(t, s, "agentId", "string")
		expectPropType(t, s, "ensName", "string")
		expectPropType(t, s, "self", "boolean")
		expectPropType(t, s, "include_private", "array")
		expectPropType(t, s, "mintConversationId", "string")
		expectPropType(t, s, "mintConversationLimit", "integer")
		expectPropType(t, s, "limit", "integer")
		expectPropType(t, s, "include_raw", "boolean")
	}

	{
		s := mustSchema(t, toolsByName["email_read"])
		if s.Type != "object" {
			t.Fatalf("email_read schema type: want object got %q", s.Type)
		}
		expectPropType(t, s, "folder", "string")
		expectPropType(t, s, "unreadOnly", "boolean")
		expectPropType(t, s, "include_raw", "boolean")
		expectPropType(t, s, "read", "boolean")
		expectPropType(t, s, "includeArchived", "boolean")
		expectPropType(t, s, "archived", "boolean")
		expectPropType(t, s, "includeDeleted", "boolean")
		expectPropType(t, s, "deleted", "boolean")
		limit := expectPropType(t, s, "limit", "integer")
		if limit["maximum"] != float64(100) {
			t.Fatalf("email_read limit.maximum: want 100 got %#v", limit["maximum"])
		}
		expectPropType(t, s, "cursor", "string")
		expectPropType(t, s, "since", "string")
		expectPropType(t, s, "threadId", "string")
	}

	{
		for _, name := range []string{"email_get", "email_get_content", "email_mark_read", "email_mark_unread"} {
			s := mustSchema(t, toolsByName[name])
			if s.Type != "object" {
				t.Fatalf("%s schema type: want object got %q", name, s.Type)
			}
			if !reflect.DeepEqual(s.Required, []string{"messageId"}) {
				t.Fatalf("%s required: want [messageId], got %#v", name, s.Required)
			}
			expectPropType(t, s, "messageId", "string")
			if name == "email_get" {
				expectPropType(t, s, "include_raw", "boolean")
			}
		}
	}

	{
		s := mustSchema(t, toolsByName["email_search"])
		if s.Type != "object" {
			t.Fatalf("email_search schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"query"}) {
			t.Fatalf("email_search required: want [query], got %#v", s.Required)
		}
		expectPropType(t, s, "query", "string")
		expectPropType(t, s, "folder", "string")
		expectPropType(t, s, "include_raw", "boolean")
		expectPropType(t, s, "read", "boolean")
		expectPropType(t, s, "unreadOnly", "boolean")
		expectPropType(t, s, "includeArchived", "boolean")
		expectPropType(t, s, "archived", "boolean")
		expectPropType(t, s, "includeDeleted", "boolean")
		expectPropType(t, s, "deleted", "boolean")
		limit := expectPropType(t, s, "limit", "integer")
		if limit["maximum"] != float64(100) {
			t.Fatalf("email_search limit.maximum: want 100 got %#v", limit["maximum"])
		}
		expectPropType(t, s, "cursor", "string")
		expectPropType(t, s, "threadId", "string")
	}

	{
		s := mustSchema(t, toolsByName["email_reply"])
		if s.Type != "object" {
			t.Fatalf("email_reply schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"messageId", "body"}) {
			t.Fatalf("email_reply required: want [messageId body], got %#v", s.Required)
		}
		expectPropType(t, s, "messageId", "string")
		expectPropType(t, s, "body", "string")
		expectPropType(t, s, "to", "string")
		expectPropType(t, s, "subject", "string")
		expectPropType(t, s, "cc", "array")
		expectPropType(t, s, "bcc", "array")
		expectPropType(t, s, "replyTo", "string")
		expectPropType(t, s, "replyAll", "boolean")
		expectPropType(t, s, "idempotencyKey", "string")
	}

	{
		s := mustSchema(t, toolsByName["email_delete"])
		if s.Type != "object" {
			t.Fatalf("email_delete schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"messageId", "action"}) {
			t.Fatalf("email_delete required: want [messageId action], got %#v", s.Required)
		}
		expectPropType(t, s, "messageId", "string")
		action := expectPropType(t, s, "action", "string")
		enum, _ := action["enum"].([]any)
		if !reflect.DeepEqual(enum, []any{"delete", "archive"}) {
			t.Fatalf("email_delete action.enum: want [delete archive], got %#v", action["enum"])
		}
	}

	{
		s := mustSchema(t, toolsByName["sms_send"])
		if s.Type != "object" {
			t.Fatalf("sms_send schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"to", "body"}) {
			t.Fatalf("sms_send required: want [to body], got %#v", s.Required)
		}
		expectPropType(t, s, "to", "string")
		expectPropType(t, s, "body", "string")
		expectPropType(t, s, "messageId", "string")
		expectPropType(t, s, "inReplyTo", "string")
	}

	{
		for _, name := range []string{"sms_read", "voicemail_read"} {
			s := mustSchema(t, toolsByName[name])
			if s.Type != "object" {
				t.Fatalf("%s schema type: want object got %q", name, s.Type)
			}
			expectPropType(t, s, "unreadOnly", "boolean")
			expectPropType(t, s, "include_raw", "boolean")
			expectPropType(t, s, "read", "boolean")
			expectPropType(t, s, "includeArchived", "boolean")
			expectPropType(t, s, "archived", "boolean")
			expectPropType(t, s, "includeDeleted", "boolean")
			expectPropType(t, s, "deleted", "boolean")
			limit := expectPropType(t, s, "limit", "integer")
			if limit["maximum"] != float64(100) {
				t.Fatalf("%s limit.maximum: want 100 got %#v", name, limit["maximum"])
			}
			expectPropType(t, s, "cursor", "string")
			expectPropType(t, s, "threadId", "string")
			if name == "sms_read" {
				expectPropType(t, s, "since", "string")
			}
		}
	}

	{
		s := mustSchema(t, toolsByName["identity_lookup"])
		if !reflect.DeepEqual(s.Required, []string{"query"}) {
			t.Fatalf("identity_lookup required: want [query], got %#v", s.Required)
		}
		if !strings.Contains(toolsByName["identity_lookup"].Description, "current-instance local ID") {
			t.Fatalf("identity_lookup description should mention current-instance local IDs, got %q", toolsByName["identity_lookup"].Description)
		}
		if !strings.Contains(toolsByName["identity_lookup"].Description, "remote ActivityPub handle") {
			t.Fatalf("identity_lookup description should mention remote ActivityPub handles, got %q", toolsByName["identity_lookup"].Description)
		}
		expectPropType(t, s, "query", "string")
	}

	{
		s := mustSchema(t, toolsByName["identity_verify"])
		if !reflect.DeepEqual(s.Required, []string{"channel", "identifier"}) {
			t.Fatalf("identity_verify required: want [channel identifier], got %#v", s.Required)
		}
		expectPropType(t, s, "channel", "string")
		expectPropType(t, s, "identifier", "string")
		expectPropType(t, s, "messageId", "string")
	}
}
