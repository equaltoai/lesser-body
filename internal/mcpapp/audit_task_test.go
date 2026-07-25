package mcpapp

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

func TestTaskMethodsRequireReadScope(t *testing.T) {
	for _, method := range []string{"tasks/list", "tasks/get", "tasks/result", "tasks/cancel"} {
		t.Run(method, func(t *testing.T) {
			req := &mcpruntime.Request{JSONRPC: "2.0", ID: method, Method: method}
			if method != "tasks/list" {
				req.Params = mustRawForAuditTest(t, map[string]any{"taskId": "task-1"})
			}
			got := requiredScopesForMCPRequest(req)
			if len(got) != 1 || got[0] != "read" {
				t.Fatalf("expected read scope for %s, got %#v", method, got)
			}
		})
	}
}

func TestAuditMcpRequestLogsTaskMetadataWithoutArguments(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	auditMcpRequest(logger, "req-1", "agent1", nil, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "tool-task",
		Method:  "tools/call",
		Params: mustRawForAuditTest(t, map[string]any{
			"name":      "skill_bundle_get",
			"arguments": map[string]any{"skill_id": "secret-skill"},
			"task":      map[string]any{"ttl": 30_000},
		}),
	})
	logged := buf.String()
	for _, want := range []string{`"msg":"mcp tool call"`, `"tool":"skill_bundle_get"`, `"task_requested":true`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected audit log to contain %s, got %s", want, logged)
		}
	}
	if strings.Contains(logged, "secret-skill") || strings.Contains(logged, "30000") {
		t.Fatalf("audit log must not include tool arguments or task metadata body, got %s", logged)
	}

	buf.Reset()
	auditMcpRequest(logger, "req-2", "agent1", nil, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "task-get",
		Method:  "tasks/get",
		Params:  mustRawForAuditTest(t, map[string]any{"taskId": "task-1"}),
	})
	logged = buf.String()
	for _, want := range []string{`"msg":"mcp task method"`, `"method":"tasks/get"`, `"task_id":"task-1"`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected task audit log to contain %s, got %s", want, logged)
		}
	}
}

func mustRawForAuditTest(t testing.TB, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
