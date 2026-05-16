package mcpapp_test

import (
	"context"
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestReadToolOutputSchemasAdvertisedInToolsListAndDiscovery(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_ENDPOINT", "https://api.example.com/mcp")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	toolsByName := toolsListByNameForOutputSchemaTest(t, env, app)

	assertSchemaPropertyType(t, outputSchemaObject(t, toolsByName["memory_query"]), "events", "array")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["skills_catalog"], "data"), "authority", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["skill_bundle_get"], "data"), "verification", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["soul_read"], "data"), "souls", "array")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["identity_whoami"], "data"), "channels", "object")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["identity_lookup"], "data"), "matches", "array")
	assertSchemaPropertyType(t, nestedOutputSchemaObject(t, toolsByName["identity_verify"], "data"), "verified", "boolean")

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/.well-known/mcp.json",
	})
	if resp.Status != 200 {
		t.Fatalf("well-known mcp.json: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var out struct {
		Tools []struct {
			Name         string          `json:"name"`
			OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal well-known mcp.json: %v", err)
	}
	for _, tool := range out.Tools {
		if tool.Name == "memory_query" {
			assertSchemaPropertyType(t, parseOutputSchemaRaw(t, "well-known memory_query", tool.OutputSchema), "events", "array")
			return
		}
	}
	t.Fatalf("memory_query missing from well-known discovery tools: %+v", out.Tools)
}

func TestCommunicationAndWriteToolOutputSchemasAdvertised(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	toolsByName := toolsListByNameForOutputSchemaTest(t, env, app)

	for _, name := range []string{"email_send", "email_reply", "sms_send"} {
		dataSchema := nestedOutputSchemaObject(t, toolsByName[name], "data")
		assertSchemaPropertyType(t, dataSchema, "messageId", "string")
		assertSchemaPropertyType(t, dataSchema, "status", "string")
		assertSchemaPropertyType(t, dataSchema, "idempotencyKey", "string")
	}
	for _, name := range []string{"email_read", "email_search", "sms_read", "voicemail_read"} {
		schema := outputSchemaObject(t, toolsByName[name])
		assertSchemaPropertyType(t, schema, "messages", "array")
		assertSchemaPropertyType(t, schema, "count", "integer")
		assertSchemaPropertyType(t, schema, "nextCursor", "string")
	}
	assertSchemaPropertyType(t, outputSchemaObject(t, toolsByName["email_get"]), "message", "object")
	assertSchemaPropertyType(t, outputSchemaObject(t, toolsByName["email_get_content"]), "body", "string")
	for _, name := range []string{"email_delete", "email_mark_read", "email_mark_unread"} {
		schema := outputSchemaObject(t, toolsByName[name])
		assertSchemaPropertyType(t, schema, "messageId", "string")
		assertSchemaPropertyType(t, schema, "action", "string")
		assertSchemaPropertyType(t, schema, "state", "object")
	}
	assertSchemaPropertyType(t, outputSchemaObject(t, toolsByName["memory_append"]), "event", "object")

	for _, name := range []string{"post_create", "post_boost", "post_favorite", "follow", "unfollow", "profile_update", "notification_dismiss"} {
		assertSchemaPropertyType(t, outputSchemaObject(t, toolsByName[name]), "data", "object")
	}
}

func toolsListByNameForOutputSchemaTest(t testing.TB, env *testkit.Env, app *apptheory.App) map[string]mcpruntime.ToolDef {
	t.Helper()

	token := newTestTokenWithAudience(t, "test", "agent1", []string{"read", "write"}, []string{"https://api.example.com/mcp"})
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
	b, _ := json.Marshal(rpc.Result)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}

	toolsByName := map[string]mcpruntime.ToolDef{}
	for _, tool := range result.Tools {
		toolsByName[tool.Name] = tool
	}
	return toolsByName
}

func outputSchemaObject(t testing.TB, def mcpruntime.ToolDef) map[string]any {
	t.Helper()
	return parseOutputSchemaRaw(t, def.Name, def.OutputSchema)
}

func parseOutputSchemaRaw(t testing.TB, name string, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("%s missing outputSchema", name)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal %s outputSchema: %v", name, err)
	}
	return schema
}

func nestedOutputSchemaObject(t testing.TB, def mcpruntime.ToolDef, prop string) map[string]any {
	t.Helper()
	schema := outputSchemaObject(t, def)
	props, _ := schema["properties"].(map[string]any)
	raw, ok := props[prop]
	if !ok {
		t.Fatalf("%s outputSchema missing property %q: %+v", def.Name, prop, schema)
	}
	nested, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s outputSchema property %q is %T, want object", def.Name, prop, raw)
	}
	return nested
}

func assertSchemaPropertyType(t testing.TB, schema map[string]any, prop string, want string) {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	raw, ok := props[prop]
	if !ok {
		t.Fatalf("schema missing property %q: %+v", prop, schema)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("schema property %q is %T, want object", prop, raw)
	}
	got, _ := m["type"].(string)
	if got != want {
		t.Fatalf("schema property %q type: got %q want %q", prop, got, want)
	}
}
