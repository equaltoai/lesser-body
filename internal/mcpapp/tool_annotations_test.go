package mcpapp_test

import (
	"encoding/json"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestMCPToolAnnotationsForMailboxAndMemory(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read", "write"})

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
		if err := json.Unmarshal(b, &result); err != nil {
			t.Fatalf("unmarshal tools/list result: %v", err)
		}
	}

	toolsByName := map[string]mcpruntime.ToolDef{}
	for _, tool := range result.Tools {
		toolsByName[tool.Name] = tool
	}

	assertHint := func(name string, readOnly *bool, destructive *bool, idempotent *bool) {
		t.Helper()
		tool, ok := toolsByName[name]
		if !ok {
			t.Fatalf("expected tool %q in tools/list", name)
		}
		if tool.Annotations == nil {
			t.Fatalf("expected annotations for %s", name)
		}
		if readOnly != nil {
			if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != *readOnly {
				t.Fatalf("%s readOnlyHint: got %+v want %v", name, tool.Annotations.ReadOnlyHint, *readOnly)
			}
		}
		if destructive != nil {
			if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != *destructive {
				t.Fatalf("%s destructiveHint: got %+v want %v", name, tool.Annotations.DestructiveHint, *destructive)
			}
		}
		if idempotent != nil {
			if tool.Annotations.IdempotentHint == nil || *tool.Annotations.IdempotentHint != *idempotent {
				t.Fatalf("%s idempotentHint: got %+v want %v", name, tool.Annotations.IdempotentHint, *idempotent)
			}
		}
	}

	truth := true
	falsehood := false

	for _, name := range []string{"email_read", "email_get", "email_get_content", "email_search", "sms_read", "voicemail_read", "memory_query", "soul_read", "skills_catalog", "skill_bundle_get"} {
		assertHint(name, &truth, nil, nil)
	}
	for _, name := range []string{"email_send", "email_reply", "email_delete", "sms_send"} {
		assertHint(name, &falsehood, &truth, nil)
	}
	for _, name := range []string{"email_mark_read", "email_mark_unread"} {
		assertHint(name, &falsehood, &falsehood, &truth)
	}
	assertHint("memory_append", &falsehood, &falsehood, &falsehood)
}
