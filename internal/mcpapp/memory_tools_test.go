package mcpapp_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/memory"
)

func TestM6_MemoryAppendAndQuery(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"write"})
	authHeader := "Bearer " + token

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	mkID := func(t time.Time, entropyByte byte) string {
		entropy := bytes.Repeat([]byte{entropyByte}, 10)
		return ulid.MustNew(ulid.Timestamp(t), bytes.NewReader(entropy)).String()
	}

	id1 := mkID(time.Date(2026, 2, 21, 0, 0, 0, 0, time.UTC), 1)
	id2 := mkID(time.Date(2026, 2, 21, 1, 0, 0, 0, time.UTC), 2)
	idExpired := mkID(time.Date(2026, 2, 21, 2, 0, 0, 0, time.UTC), 3)

	appendCall := func(eventID string, content string, extra map[string]any) (created bool) {
		args := map[string]any{
			"content":  content,
			"event_id": eventID,
		}
		for k, v := range extra {
			args[k] = v
		}

		callParams, _ := json.Marshal(map[string]any{
			"name":      "memory_append",
			"arguments": args,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      2,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("memory_append: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal memory_append: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("memory_append error: %+v", rpc.Error)
		}

		var tool mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &tool)
		}
		if len(tool.Content) != 1 || tool.Content[0].Type != "text" || tool.Content[0].Text == "" {
			t.Fatalf("unexpected tool result: %+v", tool)
		}

		var out struct {
			Created bool `json:"created"`
			Event   struct {
				EventID string `json:"event_id"`
				Content string `json:"content"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(tool.Content[0].Text), &out); err != nil {
			t.Fatalf("unmarshal tool payload: %v", err)
		}
		if out.Event.EventID != eventID {
			t.Fatalf("expected event_id %q, got %q", eventID, out.Event.EventID)
		}
		return out.Created
	}

	// Append (created)
	if created := appendCall(id1, "hello", nil); !created {
		t.Fatalf("expected created=true for first append")
	}
	// Idempotent replay (not created)
	if created := appendCall(id1, "ignored", nil); created {
		t.Fatalf("expected created=false for idempotent replay")
	}
	// Append second event
	if created := appendCall(id2, "hello again", map[string]any{"tags": []string{"b", "a"}}); !created {
		t.Fatalf("expected created=true for second append")
	}
	// Append expired event
	appendCall(idExpired, "expired", map[string]any{"expires_at": "2000-01-01T00:00:00Z"})

	// Query should return only the two non-expired events, newest-first by default.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "memory_query",
			"arguments": map[string]any{
				"limit": 10,
				"order": "desc",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("memory_query: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal memory_query: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("memory_query error: %+v", rpc.Error)
		}

		var tool mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &tool)
		}

		var out struct {
			Events []struct {
				EventID string `json:"event_id"`
				Content string `json:"content"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(tool.Content[0].Text), &out); err != nil {
			t.Fatalf("unmarshal tool payload: %v", err)
		}
		if len(out.Events) != 2 {
			t.Fatalf("expected 2 events, got %d (%+v)", len(out.Events), out.Events)
		}
		if out.Events[0].EventID != id2 || out.Events[1].EventID != id1 {
			t.Fatalf("unexpected order: %+v", out.Events)
		}
	}

	// Text filter.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "memory_query",
			"arguments": map[string]any{
				"limit": 10,
				"query": "again",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      4,
			Method:  "tools/call",
			Params:  callParams,
		})

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("memory_query error: %+v", rpc.Error)
		}

		var tool mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &tool)
		}

		var out struct {
			Events []struct {
				EventID string `json:"event_id"`
			} `json:"events"`
		}
		_ = json.Unmarshal([]byte(tool.Content[0].Text), &out)
		if len(out.Events) != 1 || out.Events[0].EventID != id2 {
			t.Fatalf("unexpected filtered events: %+v", out.Events)
		}
	}

	// Time range (inclusive).
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "memory_query",
			"arguments": map[string]any{
				"start": "2026-02-21T00:00:00Z",
				"end":   "2026-02-21T00:00:00Z",
				"limit": 10,
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      5,
			Method:  "tools/call",
			Params:  callParams,
		})

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("memory_query error: %+v", rpc.Error)
		}

		var tool mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &tool)
		}

		var out struct {
			Events []struct {
				EventID string `json:"event_id"`
			} `json:"events"`
		}
		_ = json.Unmarshal([]byte(tool.Content[0].Text), &out)
		if len(out.Events) != 1 || out.Events[0].EventID != id1 {
			t.Fatalf("unexpected range events: %+v", out.Events)
		}
	}
}

func TestM6_MemoryAppend_InvalidEventID(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"write"})
	authHeader := "Bearer " + token

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "memory_append",
		"arguments": map[string]any{
			"content":  "hello",
			"event_id": "not-a-ulid",
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  callParams,
	})
	if resp.Status != 200 {
		t.Fatalf("tools/call: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if rpc.Error == nil || rpc.Error.Code != mcpruntime.CodeInvalidParams {
		t.Fatalf("expected invalid params, got: %+v", rpc.Error)
	}
}
