package mcpapp_test

import (
	"encoding/json"
	"reflect"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestLBM0_CommunicationToolSchemasMatchSpec(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
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
		"email_search",
		"email_reply",
		"email_delete",
		"sms_send",
		"sms_read",
		"phone_call",
		"voicemail_read",
		"identity_whoami",
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
		s := mustSchema(t, toolsByName["phone_call"])
		if s.Type != "object" {
			t.Fatalf("phone_call schema type: want object got %q", s.Type)
		}
		if !reflect.DeepEqual(s.Required, []string{"to", "purpose"}) {
			t.Fatalf("phone_call required: want [to purpose], got %#v", s.Required)
		}
		expectPropType(t, s, "to", "string")
		expectPropType(t, s, "purpose", "string")
		expectPropType(t, s, "maxDurationMinutes", "integer")
	}

	{
		s := mustSchema(t, toolsByName["identity_lookup"])
		if !reflect.DeepEqual(s.Required, []string{"query"}) {
			t.Fatalf("identity_lookup required: want [query], got %#v", s.Required)
		}
		expectPropType(t, s, "query", "string")
	}
}
