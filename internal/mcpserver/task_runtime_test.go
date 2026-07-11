package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
)

func TestTaskRuntimeEnvEnablesSkillBundleGetTaskExecution(t *testing.T) {
	store := installMemoryTaskRuntimeForTest(t)
	t.Setenv(envMcpSessionTable, "")
	t.Setenv(envMcpStreamTable, "")
	t.Setenv(envMcpTaskTable, "theory-dev-mcp-tasks")
	t.Setenv("MCP_TASK_TTL_MINUTES", "10")

	authSeen := make(chan string, 1)
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authSeen <- r.Header.Get("Authorization"):
		default:
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/skills/skill-a/revisions/1/bundle" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bundle":{"schema_version":"lesser.skill.bundle.v1","bundle_id":"skill:skill-a:revision:00000001","digests":{"bundle_digest":"sha256:bundle","publication_digest":"sha256:publication"},"files":[{"path":"SKILL.md","digest":"sha256:file","content_included":false}]}}`))
	}))
	defer lesser.Close()
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	lesserapi.ResetForTests()

	srv, err := New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if !TasksPublicDiscoveryEnabled(srv) {
		t.Fatalf("expected task public discovery to be enabled when task runtime and task-capable tool are configured")
	}

	env := testkit.New()
	app := env.App()
	app.Post("/mcp", srv.Handler())
	ctx := taskToolContext()

	initResp, sessionID := invokeTaskRPC(t, ctx, env, app, "", &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "init-task-runtime",
		Method:  "initialize",
		Params:  mustJSONRaw(t, map[string]any{"protocolVersion": "2025-11-25"}),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}
	if sessionID == "" {
		t.Fatalf("expected initialize to issue a session id")
	}
	assertInitializeAdvertisesTasks(t, initResp)

	toolsResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, &mcpruntime.Request{JSONRPC: "2.0", ID: "tools-list", Method: "tools/list"})
	if toolsResp.Error != nil {
		t.Fatalf("tools/list error: %+v", toolsResp.Error)
	}
	assertSkillBundleGetTaskSupport(t, toolsResp)

	createParams := mustJSONRaw(t, map[string]any{
		"name": "skill_bundle_get",
		"arguments": map[string]any{
			"skill_id":        "skill-a",
			"revision_number": 1,
			"include_content": false,
			"local_files":     []any{},
		},
		"task": map[string]any{"ttl": int64(30_000)},
	})
	createResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, &mcpruntime.Request{JSONRPC: "2.0", ID: "create-task", Method: "tools/call", Params: createParams})
	if createResp.Error != nil {
		t.Fatalf("tools/call task create error: %+v", createResp.Error)
	}
	created := decodeCreateTaskResult(t, createResp.Result)
	if created.Task.TaskID == "" || created.Task.Status != mcpruntime.TaskStatusWorking {
		t.Fatalf("unexpected created task: %+v", created.Task)
	}
	record := waitForTaskStatus(t, store, sessionID, created.Task.TaskID, mcpruntime.TaskStatusCompleted)
	if record.ToolName != "skill_bundle_get" || record.Method != "tools/call" || len(record.Result) == 0 {
		t.Fatalf("unexpected stored task record: %+v", record)
	}
	select {
	case gotAuth := <-authSeen:
		if gotAuth != "Bearer task-test-token" {
			t.Fatalf("task-backed skill_bundle_get should preserve caller bearer, got %q", gotAuth)
		}
	default:
		t.Fatalf("task-backed skill_bundle_get did not call Lesser")
	}

	getResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, taskRequest("get-task", "tasks/get", created.Task.TaskID))
	if getResp.Error != nil {
		t.Fatalf("tasks/get error: %+v", getResp.Error)
	}
	gotTask := decodeTask(t, getResp.Result)
	if gotTask.TaskID != created.Task.TaskID || gotTask.Status != mcpruntime.TaskStatusCompleted {
		t.Fatalf("unexpected tasks/get result: %+v", gotTask)
	}

	resultResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, taskRequest("result-task", "tasks/result", created.Task.TaskID))
	if resultResp.Error != nil {
		t.Fatalf("tasks/result error: %+v", resultResp.Error)
	}
	resultBytes, _ := json.Marshal(resultResp.Result)
	if !json.Valid(resultBytes) || !containsJSON(resultBytes, "skill:skill-a:revision:00000001") || !containsJSON(resultBytes, "io.modelcontextprotocol/related-task") {
		t.Fatalf("expected skill bundle task result with related-task metadata, got %s", string(resultBytes))
	}

	listResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, &mcpruntime.Request{JSONRPC: "2.0", ID: "list-tasks", Method: "tasks/list"})
	if listResp.Error != nil {
		t.Fatalf("tasks/list error: %+v", listResp.Error)
	}
	list := decodeTaskList(t, listResp.Result)
	if len(list.Tasks) != 1 || list.Tasks[0].TaskID != created.Task.TaskID {
		t.Fatalf("unexpected task list: %+v", list)
	}

	_, otherSessionID := invokeTaskRPC(t, ctx, env, app, "", &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "init-other-session",
		Method:  "initialize",
		Params:  mustJSONRaw(t, map[string]any{"protocolVersion": "2025-11-25"}),
	})
	otherGet, _ := invokeTaskRPC(t, ctx, env, app, otherSessionID, taskRequest("get-other-session", "tasks/get", created.Task.TaskID))
	if otherGet.Error == nil || otherGet.Error.Code != mcpruntime.CodeInvalidParams {
		t.Fatalf("expected task lookup from another session to fail closed, got %+v", otherGet.Error)
	}

	twoHours := int64((2 * time.Hour) / time.Millisecond)
	excessiveTTLParams := mustJSONRaw(t, map[string]any{
		"name":      "skill_bundle_get",
		"arguments": map[string]any{"skill_id": "skill-a", "revision_number": 1},
		"task":      map[string]any{"ttl": twoHours},
	})
	excessiveTTLResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, &mcpruntime.Request{JSONRPC: "2.0", ID: "excessive-ttl", Method: "tools/call", Params: excessiveTTLParams})
	if excessiveTTLResp.Error == nil || excessiveTTLResp.Error.Code != mcpruntime.CodeInvalidParams {
		t.Fatalf("expected excessive task ttl to fail closed, got %+v", excessiveTTLResp.Error)
	}
}

func TestTaskRuntimeCancelsInFlightSkillBundleGet(t *testing.T) {
	store := installMemoryTaskRuntimeForTest(t)
	t.Setenv(envMcpSessionTable, "")
	t.Setenv(envMcpStreamTable, "")
	t.Setenv(envMcpTaskTable, "theory-dev-mcp-tasks")
	t.Setenv("MCP_TASK_TTL_MINUTES", "10")

	started := make(chan struct{})
	canceled := make(chan struct{})
	lesser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer lesser.Close()
	t.Setenv("LESSER_API_BASE_URL", lesser.URL)
	lesserapi.ResetForTests()

	srv, err := New("test-server", "dev")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	env := testkit.New()
	app := env.App()
	app.Post("/mcp", srv.Handler())
	ctx := taskToolContext()

	_, sessionID := invokeTaskRPC(t, ctx, env, app, "", &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "init-cancel",
		Method:  "initialize",
		Params:  mustJSONRaw(t, map[string]any{"protocolVersion": "2025-11-25"}),
	})
	createResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      "create-cancel-task",
		Method:  "tools/call",
		Params: mustJSONRaw(t, map[string]any{
			"name":      "skill_bundle_get",
			"arguments": map[string]any{"skill_id": "slow", "revision_number": 1},
			"task":      map[string]any{},
		}),
	})
	if createResp.Error != nil {
		t.Fatalf("tools/call task create error: %+v", createResp.Error)
	}
	created := decodeCreateTaskResult(t, createResp.Result)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("skill_bundle_get task did not start Lesser request")
	}

	cancelResp, _ := invokeTaskRPC(t, ctx, env, app, sessionID, taskRequest("cancel-task", "tasks/cancel", created.Task.TaskID))
	if cancelResp.Error != nil {
		t.Fatalf("tasks/cancel error: %+v", cancelResp.Error)
	}
	canceledTask := decodeTask(t, cancelResp.Result)
	if canceledTask.Status != mcpruntime.TaskStatusCanceled {
		t.Fatalf("expected canceled task status, got %+v", canceledTask)
	}

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("skill_bundle_get request context was not canceled")
	}
	stored := waitForTaskStatus(t, store, sessionID, created.Task.TaskID, mcpruntime.TaskStatusCanceled)
	if stored.Task.Status != mcpruntime.TaskStatusCanceled {
		t.Fatalf("expected stored task to remain canceled, got %+v", stored.Task)
	}
}

func installMemoryTaskRuntimeForTest(t testing.TB) mcpruntime.TaskStore {
	t.Helper()
	store := mcpruntime.NewMemoryTaskStore()
	oldDB := newMCPDB
	oldTaskStore := newMCPTaskStore
	newMCPDB = func() (tablecore.DB, error) { return nil, nil }
	newMCPTaskStore = func(tablecore.DB) mcpruntime.TaskStore { return store }
	t.Cleanup(func() {
		newMCPDB = oldDB
		newMCPTaskStore = oldTaskStore
	})
	return store
}

func taskToolContext() context.Context {
	principal := &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", Scopes: []string{"read"}},
	}
	return auth.InjectToolContext(context.Background(), principal, "task-test-token")
}

func invokeTaskRPC(t testing.TB, ctx context.Context, env *testkit.Env, app *apptheory.App, sessionID string, req *mcpruntime.Request) (*mcpruntime.Response, string) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	headers := map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"application/json, text/event-stream"},
	}
	if sessionID != "" {
		headers["mcp-session-id"] = []string{sessionID}
	}
	resp := env.Invoke(ctx, app, apptheory.Request{Method: "POST", Path: "/mcp", Headers: headers, Body: body})
	nextSessionID := sessionID
	if ids := resp.Headers["mcp-session-id"]; len(ids) > 0 && ids[0] != "" {
		nextSessionID = ids[0]
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal rpc response: %v (status=%d body=%s)", err, resp.Status, string(resp.Body))
	}
	return &rpc, nextSessionID
}

func mustJSONRaw(t testing.TB, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

func taskRequest(id string, method string, taskID string) *mcpruntime.Request {
	return &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  mustJSONRawNoTest(map[string]any{"taskId": taskID}),
	}
}

func mustJSONRawNoTest(value any) json.RawMessage {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return b
}

func assertInitializeAdvertisesTasks(t testing.TB, resp *mcpruntime.Response) {
	t.Helper()
	b, _ := json.Marshal(resp.Result)
	var out struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	tasks, ok := out.Capabilities["tasks"].(map[string]any)
	if !ok {
		t.Fatalf("expected initialize capabilities.tasks object, got %+v", out.Capabilities)
	}
	if _, ok := tasks["list"].(map[string]any); !ok {
		t.Fatalf("expected tasks.list capability, got %+v", tasks)
	}
	if _, ok := tasks["cancel"].(map[string]any); !ok {
		t.Fatalf("expected tasks.cancel capability, got %+v", tasks)
	}
	requests, _ := tasks["requests"].(map[string]any)
	tools, _ := requests["tools"].(map[string]any)
	if _, ok := tools["call"].(map[string]any); !ok {
		t.Fatalf("expected tasks.requests.tools.call capability, got %+v", tasks)
	}
}

func assertSkillBundleGetTaskSupport(t testing.TB, resp *mcpruntime.Response) {
	t.Helper()
	b, _ := json.Marshal(resp.Result)
	var out struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	for _, tool := range out.Tools {
		if tool.Name != "skill_bundle_get" {
			continue
		}
		if tool.Execution == nil || tool.Execution.TaskSupport != mcpruntime.TaskSupportOptional {
			t.Fatalf("skill_bundle_get should declare optional task support, got %+v", tool.Execution)
		}
		return
	}
	t.Fatalf("skill_bundle_get missing from tools/list")
}

func decodeCreateTaskResult(t testing.TB, raw any) mcpruntime.CreateTaskResult {
	t.Helper()
	b, _ := json.Marshal(raw)
	var out mcpruntime.CreateTaskResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal create task result: %v (%s)", err, string(b))
	}
	return out
}

func decodeTask(t testing.TB, raw any) mcpruntime.Task {
	t.Helper()
	b, _ := json.Marshal(raw)
	var out mcpruntime.Task
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal task: %v (%s)", err, string(b))
	}
	return out
}

func decodeTaskList(t testing.TB, raw any) mcpruntime.TaskListResult {
	t.Helper()
	b, _ := json.Marshal(raw)
	var out mcpruntime.TaskListResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal task list: %v (%s)", err, string(b))
	}
	return out
}

func waitForTaskStatus(t testing.TB, store mcpruntime.TaskStore, sessionID string, taskID string, status mcpruntime.TaskStatus) *mcpruntime.TaskRecord {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := store.Get(context.Background(), mcpruntime.TaskLookup{SessionID: sessionID, TaskID: taskID})
		if err == nil && record.Task.Status == status {
			return record
		}
		select {
		case <-deadline:
			if err != nil {
				t.Fatalf("task %s did not reach %s: %v", taskID, status, err)
			}
			t.Fatalf("task %s did not reach %s; latest status=%s", taskID, status, record.Task.Status)
		case <-ticker.C:
		}
	}
}

func containsJSON(b []byte, needle string) bool {
	return json.Valid(b) && len(needle) > 0 && strings.Contains(string(b), needle)
}
