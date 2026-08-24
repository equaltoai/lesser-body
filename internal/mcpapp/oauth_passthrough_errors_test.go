package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

func TestDownstreamOAuthFailure_ToolResultRequiresReauth(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/accounts/verify_credentials" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name":      "profile_read",
		"arguments": map[string]any{},
	})
	callResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  callParams,
	})
	if callResp.Status != 200 {
		t.Fatalf("tools/call: status=%d body=%s", callResp.Status, string(callResp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(callResp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("expected tool result error payload, got JSON-RPC error %+v", rpc.Error)
	}

	var toolResult mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolResult)
	}
	if !toolResult.IsError {
		t.Fatalf("expected tool result to be marked as error")
	}
	errPayload, _ := toolResult.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "unauthorized" {
		t.Fatalf("unexpected code: %+v", errPayload)
	}
	if status, _ := errPayload["status"].(float64); status != 401 {
		t.Fatalf("unexpected status: %+v", errPayload)
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["authAction"] != "refresh_or_reauthorize" {
		t.Fatalf("unexpected details: %+v", details)
	}
	if details["refreshRequired"] != true {
		t.Fatalf("expected refreshRequired=true, got %+v", details)
	}
}

func TestDownstreamOAuthFailure_ResourceReturnsStructuredErrorPayload(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/accounts/verify_credentials" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"scope denied"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	params, _ := json.Marshal(map[string]any{"uri": "agent://profile"})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "resources/read", Params: params})
	if resp.Status != 200 {
		t.Fatalf("resources/read: status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal resources/read: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("expected structured resource payload, got JSON-RPC error %+v", rpc.Error)
	}

	var out struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("expected one resource content, got %+v", out.Contents)
	}

	var payload struct {
		Error struct {
			Code    string         `json:"code"`
			Status  int            `json:"status"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out.Contents[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal resource payload: %v", err)
	}
	if payload.Error.Code != "forbidden" || payload.Error.Status != 403 {
		t.Fatalf("unexpected resource auth payload: %+v", payload)
	}
	if payload.Error.Details["authAction"] != "reauthorize" {
		t.Fatalf("unexpected details: %+v", payload.Error.Details)
	}
}
