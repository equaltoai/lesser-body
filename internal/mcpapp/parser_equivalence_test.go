package mcpapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

func TestAuditParserEquivalence_DispatchableToolCallsAreAuthorized(t *testing.T) {
	h := newParserEquivalenceHarness(t)
	sessionID := h.initialize(t, []string{"read"}, nil)

	singleWriteTool := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_append","arguments":{"content":"single dispatch sentinel"}}}`)
	requireRuntimeDispatch(t, h, []string{"write"}, singleWriteTool, map[string][]string{"mcp-session-id": {sessionID}})
	requireInsufficientScopeBeforeRuntime(t, h, []string{"read"}, singleWriteTool, map[string][]string{"mcp-session-id": {sessionID}})

	batchWriteTool := []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}},
		{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_append","arguments":{"content":"batch dispatch sentinel"}}}
	]`)
	requireRuntimeDispatch(t, h, []string{"write"}, batchWriteTool, nil)
	requireInsufficientScopeBeforeRuntime(t, h, []string{"read"}, batchWriteTool, nil)

	notificationWriteTool := []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"memory_append","arguments":{"content":"notification sentinel"}}}`)
	req, err := mcpruntime.ParseRequest(notificationWriteTool)
	if err != nil {
		t.Fatalf("notification tools/call should parse with AppTheory parser: %v", err)
	}
	if req.ID != nil {
		t.Fatalf("expected notification-form request to omit id, got %#v", req.ID)
	}
	if got := requiredScopesForMCPRequest(req); !reflect.DeepEqual(got, []string{"write"}) {
		t.Fatalf("notification-form tools/call must be seen by Body authorization, got required scopes %v", got)
	}
	requireInsufficientScopeBeforeRuntime(t, h, []string{"read"}, notificationWriteTool, map[string][]string{"mcp-session-id": {sessionID}})

	h.resetCounts()
	resp, err := h.invoke([]string{"write"}, notificationWriteTool, map[string][]string{"mcp-session-id": {sessionID}})
	if err != nil {
		t.Fatalf("invoke notification with write scope: %v", err)
	}
	if resp.Status != 202 {
		t.Fatalf("notification tools/call should remain an AppTheory notification, got status=%d body=%s", resp.Status, string(resp.Body))
	}
	if got := h.toolCalls.Load(); got != 0 {
		t.Fatalf("notification tools/call must not dispatch a tool handler, got %d calls", got)
	}
}

func TestAuditParserEquivalence_ParseFailuresDoNotReachToolDispatch(t *testing.T) {
	h := newParserEquivalenceHarness(t)
	sessionID := h.initialize(t, []string{"write"}, nil)

	cases := []struct {
		name    string
		body    []byte
		headers map[string][]string
		batch   bool
	}{
		{
			name:    "malformed single tools call",
			body:    []byte(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"memory_append","arguments":`),
			headers: map[string][]string{"mcp-session-id": {sessionID}},
		},
		{
			name:  "malformed batch with leading dispatchable write tool",
			batch: true,
			body: []byte(`[
				{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}},
				{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_append","arguments":{"content":"must not dispatch"}}},
			]`),
		},
		{
			name:  "invalid batch element after dispatchable write tool",
			batch: true,
			body: []byte(`[
				{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}},
				{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_append","arguments":{"content":"must not dispatch"}}},
				{"jsonrpc":"2.0","id":3,"method":""}
			]`),
		},
		{
			name:    "missing method object",
			body:    []byte(`{"jsonrpc":"2.0","id":11,"params":{"name":"memory_append","arguments":{"content":"must not dispatch"}}}`),
			headers: map[string][]string{"mcp-session-id": {sessionID}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.resetCounts()
			if tc.batch {
				if _, err := mcpruntime.ParseBatchRequest(tc.body); err == nil {
					t.Fatalf("corpus body unexpectedly parses as an AppTheory batch request")
				}
			} else if strings.Contains(string(tc.body), `"method"`) {
				if _, err := mcpruntime.ParseRequest(tc.body); err == nil {
					t.Fatalf("corpus body unexpectedly parses as an AppTheory request")
				}
			}

			resp, err := h.invoke([]string{"write"}, tc.body, tc.headers)
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if resp.Status == 403 {
				t.Fatalf("parse-failure corpus should be handled by the AppTheory runtime, not Body scope gate: %s", string(resp.Body))
			}
			if got := h.nextCalls.Load(); got != 1 {
				t.Fatalf("parse-failure corpus should be passed to AppTheory runtime once, got next calls=%d", got)
			}
			if got := h.toolCalls.Load(); got != 0 {
				t.Fatalf("parse-failure corpus must not reach tool dispatch, got %d tool calls", got)
			}
		})
	}
}

func TestAuditParserEquivalence_EmptyOrMissingToolNameDoesNotDispatch(t *testing.T) {
	h := newParserEquivalenceHarness(t)
	sessionID := h.initialize(t, []string{"read"}, nil)

	cases := []struct {
		name string
		body []byte
	}{
		{
			name: "empty tool name",
			body: []byte(`{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"   ","arguments":{"content":"must not dispatch"}}}`),
		},
		{
			name: "missing tool name",
			body: []byte(`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"arguments":{"content":"must not dispatch"}}}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := mcpruntime.ParseRequest(tc.body)
			if err != nil {
				t.Fatalf("edge tools/call should parse with AppTheory parser: %v", err)
			}
			if got := requiredScopesForMCPRequest(req); got != nil {
				t.Fatalf("empty/missing tool name should defer to AppTheory invalid-params handling, got required scopes %v", got)
			}

			h.resetCounts()
			resp, err := h.invoke([]string{"read"}, tc.body, map[string][]string{"mcp-session-id": {sessionID}})
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if resp.Status != 200 {
				t.Fatalf("expected AppTheory JSON-RPC invalid-params response, got status=%d body=%s", resp.Status, string(resp.Body))
			}
			if got := h.nextCalls.Load(); got != 1 {
				t.Fatalf("empty/missing tool name should reach AppTheory validation once, got next calls=%d", got)
			}
			if got := h.toolCalls.Load(); got != 0 {
				t.Fatalf("empty/missing tool name must not reach tool dispatch, got %d tool calls", got)
			}
			var rpc mcpruntime.Response
			if err := json.Unmarshal(resp.Body, &rpc); err != nil {
				t.Fatalf("unmarshal JSON-RPC response: %v", err)
			}
			if rpc.Error == nil || rpc.Error.Code != mcpruntime.CodeInvalidParams {
				t.Fatalf("expected invalid params for empty/missing tool name, got %+v", rpc.Error)
			}
		})
	}
}

type parserEquivalenceHarness struct {
	handler   apptheory.Handler
	nextCalls atomic.Int64
	toolCalls atomic.Int64
}

func newParserEquivalenceHarness(t testing.TB) *parserEquivalenceHarness {
	t.Helper()

	srv := mcpruntime.NewServer("parser-equivalence", "test")
	h := &parserEquivalenceHarness{}
	err := srv.Registry().RegisterTool(mcpruntime.ToolDef{
		Name:        "memory_append",
		Description: "test write tool using Body's registered write-scope classification",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error) {
		h.toolCalls.Add(1)
		return &mcpruntime.ToolResult{Content: []mcpruntime.ContentBlock{{Type: "text", Text: "dispatched"}}}, nil
	})
	if err != nil {
		t.Fatalf("register test tool: %v", err)
	}

	runtimeHandler := srv.Handler()
	h.handler = WithAudit(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		h.nextCalls.Add(1)
		return runtimeHandler(ctx)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	return h
}

func (h *parserEquivalenceHarness) initialize(t testing.TB, scopes []string, headers map[string][]string) string {
	t.Helper()
	h.resetCounts()
	resp, err := h.invoke(scopes, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`), headers)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", resp.Status, string(resp.Body))
	}
	ids := resp.Headers["mcp-session-id"]
	if len(ids) == 0 || strings.TrimSpace(ids[0]) == "" {
		t.Fatalf("initialize did not return mcp-session-id: %+v", resp.Headers)
	}
	return ids[0]
}

func (h *parserEquivalenceHarness) invoke(scopes []string, body []byte, extraHeaders map[string][]string) (*apptheory.Response, error) {
	headers := map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"application/json, text/event-stream"},
	}
	for k, v := range extraHeaders {
		headers[k] = append([]string(nil), v...)
	}
	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Method:  "POST",
			Path:    "/mcp/agent1",
			Headers: headers,
			Body:    append([]byte(nil), body...),
		},
		AuthIdentity: "agent1",
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims: &auth.Claims{
			Username: "agent1",
			Scopes:   append([]string(nil), scopes...),
		},
	})
	return h.handler(ctx)
}

func (h *parserEquivalenceHarness) resetCounts() {
	h.nextCalls.Store(0)
	h.toolCalls.Store(0)
}

func requireRuntimeDispatch(t testing.TB, h *parserEquivalenceHarness, scopes []string, body []byte, headers map[string][]string) {
	t.Helper()
	h.resetCounts()
	resp, err := h.invoke(scopes, body, headers)
	if err != nil {
		t.Fatalf("invoke dispatchable request: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("dispatchable request: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if got := h.nextCalls.Load(); got != 1 {
		t.Fatalf("dispatchable request should reach AppTheory runtime once, got next calls=%d", got)
	}
	if got := h.toolCalls.Load(); got != 1 {
		t.Fatalf("dispatchable request should reach tool dispatch once with sufficient scope, got %d", got)
	}
}

func requireInsufficientScopeBeforeRuntime(t testing.TB, h *parserEquivalenceHarness, scopes []string, body []byte, headers map[string][]string) {
	t.Helper()
	h.resetCounts()
	resp, err := h.invoke(scopes, body, headers)
	if err != nil {
		t.Fatalf("invoke read-scoped write request: %v", err)
	}
	if resp.Status != 403 {
		t.Fatalf("read-scoped write request should be rejected by Body authorization, got status=%d body=%s", resp.Status, string(resp.Body))
	}
	if got := h.nextCalls.Load(); got != 0 {
		t.Fatalf("Body authorization must stop request before AppTheory runtime, got next calls=%d", got)
	}
	if got := h.toolCalls.Load(); got != 0 {
		t.Fatalf("Body authorization must stop request before tool dispatch, got %d tool calls", got)
	}
	var out struct {
		Error struct {
			Details struct {
				Reason         string   `json:"reason"`
				RequiredScopes []string `json:"requiredScopes"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal insufficient-scope response: %v", err)
	}
	if out.Error.Details.Reason != "insufficient_scope" || !reflect.DeepEqual(out.Error.Details.RequiredScopes, []string{"write"}) {
		t.Fatalf("unexpected insufficient-scope details: %+v", out.Error.Details)
	}
}
