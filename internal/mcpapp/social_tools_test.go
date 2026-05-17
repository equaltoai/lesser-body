package mcpapp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/memory"
)

func TestM5_ToolsListContainsCoreTools(t *testing.T) {
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
	have := map[string]bool{}
	for _, tool := range result.Tools {
		have[tool.Name] = true
		toolsByName[tool.Name] = tool
	}

	for _, name := range []string{
		"profile_read",
		"timeline_read",
		"post_search",
		"post_get",
		"followers_list",
		"following_list",
		"conversations_read",
		"notifications_read",
		"notification_dismiss",
		"post_create",
		"post_boost",
		"post_favorite",
		"follow",
		"unfollow",
		"profile_update",
	} {
		if !have[name] {
			t.Fatalf("expected tool %q in tools/list", name)
		}
	}

	var notificationSchema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(toolsByName["notifications_read"].InputSchema, &notificationSchema); err != nil {
		t.Fatalf("unmarshal notifications_read schema: %v", err)
	}
	if propType, _ := notificationSchema.Properties["include_raw"]["type"].(string); propType != "boolean" {
		t.Fatalf("notifications_read include_raw should be boolean, got %+v", notificationSchema.Properties["include_raw"])
	}
	if propType, _ := notificationSchema.Properties["include_diagnostics"]["type"].(string); propType != "boolean" {
		t.Fatalf("notifications_read include_diagnostics should be boolean, got %+v", notificationSchema.Properties["include_diagnostics"])
	}
	typesProp := notificationSchema.Properties["types"]
	items, _ := typesProp["items"].(map[string]any)
	enum, _ := items["enum"].([]any)
	hasCommunicationInbound := false
	for _, value := range enum {
		if value == "communication:inbound" {
			hasCommunicationInbound = true
			break
		}
	}
	if !hasCommunicationInbound {
		t.Fatalf("notifications_read types enum should include communication:inbound, got %+v", typesProp)
	}
	if maxItems, _ := typesProp["maxItems"].(float64); maxItems != 8 {
		t.Fatalf("notifications_read types should advertise maxItems=8, got %+v", typesProp)
	}
	var postGetSchema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(toolsByName["post_get"].InputSchema, &postGetSchema); err != nil {
		t.Fatalf("unmarshal post_get schema: %v", err)
	}
	if propType, _ := postGetSchema.Properties["id"]["type"].(string); propType != "string" {
		t.Fatalf("post_get id should be string, got %+v", postGetSchema.Properties["id"])
	}
	viewProp := postGetSchema.Properties["view"]
	enum, _ = viewProp["enum"].([]any)
	if !reflect.DeepEqual(enum, []any{"standard", "full"}) {
		t.Fatalf("post_get view enum = %+v", viewProp)
	}
	if !containsString(postGetSchema.Required, "id") {
		t.Fatalf("post_get should require id, got %+v", postGetSchema.Required)
	}
	for _, toolName := range []string{"timeline_read", "post_search"} {
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(toolsByName[toolName].InputSchema, &schema); err != nil {
			t.Fatalf("unmarshal %s schema: %v", toolName, err)
		}
		viewProp := schema.Properties["view"]
		enum, _ := viewProp["enum"].([]any)
		if !reflect.DeepEqual(enum, []any{"compact", "standard", "full"}) {
			t.Fatalf("%s view enum = %+v", toolName, viewProp)
		}
		if propType, _ := schema.Properties["max_output_bytes"]["type"].(string); propType != "integer" {
			t.Fatalf("%s max_output_bytes should be integer, got %+v", toolName, schema.Properties["max_output_bytes"])
		}
		if propType, _ := schema.Properties["preview_chars"]["type"].(string); propType != "integer" {
			t.Fatalf("%s preview_chars should be integer, got %+v", toolName, schema.Properties["preview_chars"])
		}
	}
	var conversationSchema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(toolsByName["conversations_read"].InputSchema, &conversationSchema); err != nil {
		t.Fatalf("unmarshal conversations_read schema: %v", err)
	}
	if propType, _ := conversationSchema.Properties["include_raw"]["type"].(string); propType != "boolean" {
		t.Fatalf("conversations_read include_raw should be boolean, got %+v", conversationSchema.Properties["include_raw"])
	}
}

func TestM5_ToolsProxyToLesserAPI(t *testing.T) {
	type recorded struct {
		Method string
		Path   string
		Query  string
		Auth   string
	}

	cases := []struct {
		name         string
		tool         string
		scope        string // "read" or "write"
		args         any
		invalidArgs  any
		failureCode  int
		failureCalls bool
		wantRequests []recorded
	}{
		{
			name:         "profile_read",
			tool:         "profile_read",
			scope:        "read",
			args:         map[string]any{},
			invalidArgs:  map[string]any{},
			failureCode:  mcpruntime.CodeServerError,
			failureCalls: true,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/accounts/verify_credentials"},
			},
		},
		{
			name:        "timeline_read_home",
			tool:        "timeline_read",
			scope:       "read",
			args:        map[string]any{"timeline": "home", "limit": 5},
			invalidArgs: map[string]any{"timeline": ""},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/timelines/home", Query: "limit=5"},
			},
		},
		{
			name:        "post_search",
			tool:        "post_search",
			scope:       "read",
			args:        map[string]any{"query": "hello", "limit": 2},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v2/search"},
			},
		},
		{
			name:        "post_get",
			tool:        "post_get",
			scope:       "read",
			args:        map[string]any{"id": "s1"},
			invalidArgs: map[string]any{"id": ""},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/statuses/s1"},
			},
		},
		{
			name:        "followers_list",
			tool:        "followers_list",
			scope:       "read",
			args:        map[string]any{"limit": 2, "cursor": "c1"},
			invalidArgs: map[string]any{"limit": "nope"},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/accounts/verify_credentials"},
				{Method: "GET", Path: "/api/v1/accounts/acct1/followers", Query: "limit=2&max_id=c1"},
			},
		},
		{
			name:        "following_list",
			tool:        "following_list",
			scope:       "read",
			args:        map[string]any{"limit": 2},
			invalidArgs: map[string]any{"limit": "nope"},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/accounts/verify_credentials"},
				{Method: "GET", Path: "/api/v1/accounts/acct1/following", Query: "limit=2"},
			},
		},
		{
			name:        "conversations_read",
			tool:        "conversations_read",
			scope:       "read",
			args:        map[string]any{"limit": 2},
			invalidArgs: map[string]any{"limit": "nope"},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/conversations", Query: "limit=2"},
			},
		},
		{
			name:        "notifications_read",
			tool:        "notifications_read",
			scope:       "read",
			args:        map[string]any{"limit": 2, "types": []string{"mention"}},
			invalidArgs: map[string]any{"limit": "nope"},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/notifications", Query: "limit=2&types%5B%5D=mention"},
			},
		},
		{
			name:        "notification_dismiss",
			tool:        "notification_dismiss",
			scope:       "write",
			args:        map[string]any{"id": "n1"},
			invalidArgs: map[string]any{"id": 123},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/notifications/n1/dismiss"},
			},
		},
		{
			name:        "post_create",
			tool:        "post_create",
			scope:       "write",
			args:        map[string]any{"content": "hi", "visibility": "public"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/statuses"},
			},
		},
		{
			name:        "post_boost",
			tool:        "post_boost",
			scope:       "write",
			args:        map[string]any{"post_id": "s1"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/statuses/s1/reblog"},
			},
		},
		{
			name:        "post_favorite",
			tool:        "post_favorite",
			scope:       "write",
			args:        map[string]any{"post_id": "s1"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/statuses/s1/favourite"},
			},
		},
		{
			name:        "follow",
			tool:        "follow",
			scope:       "write",
			args:        map[string]any{"account_id": "a1"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/accounts/a1/follow"},
			},
		},
		{
			name:        "unfollow",
			tool:        "unfollow",
			scope:       "write",
			args:        map[string]any{"account_id": "a1"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "POST", Path: "/api/v1/accounts/a1/unfollow"},
			},
		},
		{
			name:        "profile_update",
			tool:        "profile_update",
			scope:       "write",
			args:        map[string]any{"display_name": "Alice"},
			invalidArgs: map[string]any{},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "PATCH", Path: "/api/v1/accounts/update_credentials"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_SESSION_TABLE", "")
			t.Setenv("JWT_SECRET", "test")
			auth.ResetForTests()

			var got []recorded
			wantAuth := ""

			forceError := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, recorded{
					Method: r.Method,
					Path:   r.URL.Path,
					Query:  r.URL.RawQuery,
					Auth:   r.Header.Get("Authorization"),
				})
				if wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
					return
				}

				if forceError {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"forced"}`))
					return
				}

				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/accounts/verify_credentials":
					_, _ = w.Write([]byte(`{"id":"acct1","username":"agent1"}`))
				case "/api/v1/accounts/acct1/followers":
					_, _ = w.Write([]byte(`[{"id":"f1"}]`))
				case "/api/v1/accounts/acct1/following":
					_, _ = w.Write([]byte(`[{"id":"g1"}]`))
				case "/api/v1/conversations":
					_, _ = w.Write([]byte(`[{"id":"conv1"}]`))
				case "/api/v1/timelines/home":
					_, _ = w.Write([]byte(`[{"id":"t1"}]`))
				case "/api/v1/timelines/public":
					_, _ = w.Write([]byte(`[{"id":"t2"}]`))
				case "/api/v2/search":
					_, _ = w.Write([]byte(`{"statuses":[{"id":"s1"}],"accounts":[],"hashtags":[]}`))
				case "/api/v1/statuses/s1":
					_, _ = w.Write([]byte(`{"id":"s1","url":"https://example.com/@agent/s1","content":"hello","account":{"id":"acct1","acct":"agent@example.com"},"visibility":"public"}`))
				case "/api/v1/notifications":
					_, _ = w.Write([]byte(`[{"id":"n1"}]`))
				case "/api/v1/notifications/n1/dismiss":
					_, _ = w.Write([]byte(`{}`))
				case "/api/v1/statuses":
					_, _ = w.Write([]byte(`{"id":"new1"}`))
				case "/api/v1/statuses/s1/reblog":
					_, _ = w.Write([]byte(`{"id":"s1","reblogged":true}`))
				case "/api/v1/statuses/s1/favourite":
					_, _ = w.Write([]byte(`{"id":"s1","favourited":true}`))
				case "/api/v1/accounts/a1/follow":
					_, _ = w.Write([]byte(`{"id":"a1","following":true}`))
				case "/api/v1/accounts/a1/unfollow":
					_, _ = w.Write([]byte(`{"id":"a1","following":false}`))
				case "/api/v1/accounts/update_credentials":
					_, _ = w.Write([]byte(`{"ok":true}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"not found"}`))
				}
			}))
			defer server.Close()

			t.Setenv("LESSER_API_BASE_URL", server.URL)
			lesserapi.ResetForTests()

			tokenScopes := []string{tc.scope}
			token := newTestToken(t, "test", "agent1", tokenScopes)
			wantAuth = "Bearer " + token

			app, err := mcpapp.New("test", "dev")
			if err != nil {
				t.Fatalf("new app: %v", err)
			}
			env := testkit.New()

			initResp := invokeJSON(t, env, app, map[string][]string{
				"authorization": {wantAuth},
			}, &mcpruntime.Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			})
			if initResp.Status != 200 {
				t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
			}
			sessionID := initResp.Headers["mcp-session-id"][0]

			// Happy path
			{
				got = nil

				callParams, _ := json.Marshal(map[string]any{
					"name":      tc.tool,
					"arguments": tc.args,
				})
				callResp := invokeJSON(t, env, app, map[string][]string{
					"authorization":  {wantAuth},
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
				var rpcCall mcpruntime.Response
				if err := json.Unmarshal(callResp.Body, &rpcCall); err != nil {
					t.Fatalf("unmarshal tools/call: %v", err)
				}
				if rpcCall.Error != nil {
					t.Fatalf("tools/call error: %+v", rpcCall.Error)
				}
				var toolResult mcpruntime.ToolResult
				{
					b, _ := json.Marshal(rpcCall.Result)
					_ = json.Unmarshal(b, &toolResult)
				}
				if toolResult.StructuredContent == nil {
					t.Fatalf("expected structuredContent")
				}

				if len(got) != len(tc.wantRequests) {
					t.Fatalf("unexpected request count: got=%d want=%d (%+v)", len(got), len(tc.wantRequests), got)
				}
				for i := range tc.wantRequests {
					if got[i].Method != tc.wantRequests[i].Method || got[i].Path != tc.wantRequests[i].Path {
						t.Fatalf("request[%d] got=%s %s want=%s %s", i, got[i].Method, got[i].Path, tc.wantRequests[i].Method, tc.wantRequests[i].Path)
					}
					if strings.TrimSpace(tc.wantRequests[i].Query) != "" && got[i].Query != tc.wantRequests[i].Query {
						t.Fatalf("request[%d] query got=%q want=%q", i, got[i].Query, tc.wantRequests[i].Query)
					}
				}
			}

			// Failure path: tools return a JSON-RPC server error for handler failures (no upstream
			// calls for validation failures), and tools without input requirements use an upstream
			// failure instead.
			{
				got = nil

				forceError = tc.failureCalls

				callParams, _ := json.Marshal(map[string]any{
					"name":      tc.tool,
					"arguments": tc.invalidArgs,
				})
				callResp := invokeJSON(t, env, app, map[string][]string{
					"authorization":  {wantAuth},
					"mcp-session-id": {sessionID},
				}, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      3,
					Method:  "tools/call",
					Params:  callParams,
				})
				if callResp.Status != 200 {
					t.Fatalf("tools/call failure: status=%d body=%s", callResp.Status, string(callResp.Body))
				}
				var rpcCall mcpruntime.Response
				if err := json.Unmarshal(callResp.Body, &rpcCall); err != nil {
					t.Fatalf("unmarshal tools/call failure: %v", err)
				}
				if rpcCall.Error == nil || rpcCall.Error.Code != tc.failureCode {
					t.Fatalf("expected JSON-RPC error code %d, got: %+v", tc.failureCode, rpcCall.Error)
				}
				if tc.failureCalls {
					if len(got) == 0 {
						t.Fatalf("expected upstream request for server error")
					}
				} else if len(got) != 0 {
					t.Fatalf("expected no upstream requests for invalid params, got %+v", got)
				}
			}

			// Write tools should fail closed when scope is only read.
			if tc.scope == "write" {
				got = nil

				readToken := newTestToken(t, "test", "agent1", []string{"read"})
				readAuth := "Bearer " + readToken

				callParams, _ := json.Marshal(map[string]any{
					"name":      tc.tool,
					"arguments": tc.args,
				})
				callResp := invokeJSON(t, env, app, map[string][]string{
					"authorization":  {readAuth},
					"mcp-session-id": {sessionID},
				}, &mcpruntime.Request{
					JSONRPC: "2.0",
					ID:      4,
					Method:  "tools/call",
					Params:  callParams,
				})
				if callResp.Status != 403 {
					t.Fatalf("expected 403 for write tool with read token, got %d (%s)", callResp.Status, string(callResp.Body))
				}
				if len(got) != 0 {
					t.Fatalf("expected no upstream requests for forbidden call, got %+v", got)
				}
			}
		})
	}
}

func TestM5_PostGetExpandsStatusRefViaLesserRoute(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	fullContent := strings.TrimSpace("full post content " + strings.Repeat("abcdefghijklmnopqrstuvwxyz ", 40))

	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/statuses/post-1" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"post-1",
			"url":"https://example.com/@alice/post-1",
			"created_at":"2026-05-17T17:00:00Z",
			"visibility":"public",
			"content":"` + fullContent + `",
			"account":{
				"id":"acct-1",
				"username":"alice",
				"acct":"alice@example.com",
				"display_name":"Alice",
				"url":"https://example.com/@alice",
				"note":"` + strings.Repeat("raw profile note ", 40) + `"
			},
			"debugPayload":{"large":"` + strings.Repeat("debug ", 200) + `"}
		}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	call := func(id int, args map[string]any) map[string]any {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": "post_get", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("post_get: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal post_get: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("post_get rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data == nil {
			t.Fatalf("expected structured data, got %+v", out.StructuredContent)
		}
		return data
	}

	standard := call(2, map[string]any{"id": "post-1", "view": "standard"})
	if standard["id"] != "post-1" || standard["view"] != "standard" || standard["source"] != "lesser-api" {
		t.Fatalf("unexpected standard post_get metadata: %+v", standard)
	}
	status, _ := standard["status"].(map[string]any)
	if status["content"] != fullContent || status["visibility"] != "public" || status["createdAt"] != "2026-05-17T17:00:00Z" {
		t.Fatalf("standard post_get should return normalized full status fields, got %+v", status)
	}
	authorRef, _ := status["authorRef"].(map[string]any)
	if authorRef["id"] != "acct-1" || authorRef["acct"] != "alice@example.com" || authorRef["displayName"] != "Alice" || authorRef["url"] != "https://example.com/@alice" {
		t.Fatalf("standard post_get author ref = %+v", authorRef)
	}
	statusRef, _ := standard["statusRef"].(map[string]any)
	if statusRef["id"] != "post-1" || statusRef["contentTruncated"] != true {
		t.Fatalf("expected compact statusRef with truncation marker, got %+v", statusRef)
	}
	if preview, _ := statusRef["contentPreview"].(string); preview == "" || len([]rune(preview)) > 500 {
		t.Fatalf("expected bounded contentPreview, got %d runes", len([]rune(preview)))
	}
	expand, _ := statusRef["expand"].(map[string]any)
	expandArgs, _ := expand["arguments"].(map[string]any)
	if expand["tool"] != "post_get" || expandArgs["id"] != "post-1" || expandArgs["view"] != "standard" || expand["resultPath"] != "structuredContent.data.status" {
		t.Fatalf("unexpected statusRef expansion metadata: %+v", expand)
	}
	omitted, _ := statusRef["omitted"].([]any)
	if len(omitted) != 1 {
		t.Fatalf("expected compact statusRef omitted metadata, got %+v", statusRef["omitted"])
	}
	omittedRecord, _ := omitted[0].(map[string]any)
	omittedExpand, _ := omittedRecord["expand"].(map[string]any)
	if omittedRecord["path"] != "content" || omittedExpand["tool"] != "post_get" || omittedExpand["resultPath"] != "structuredContent.data.status.content" {
		t.Fatalf("unexpected omitted expansion metadata: %+v", omittedRecord)
	}

	full := call(3, map[string]any{"id": "post-1", "view": "full"})
	if full["view"] != "full" {
		t.Fatalf("expected full view metadata, got %+v", full)
	}
	rawStatus, _ := full["status"].(map[string]any)
	if rawStatus["content"] != fullContent {
		t.Fatalf("full post_get should expose upstream content, got %+v", rawStatus["content"])
	}
	if _, ok := rawStatus["debugPayload"].(map[string]any); !ok {
		t.Fatalf("full post_get should expose upstream payload for audit/debug expansion, got %+v", rawStatus)
	}
	rawAccount, _ := rawStatus["account"].(map[string]any)
	if _, ok := rawAccount["note"].(string); !ok {
		t.Fatalf("full post_get should preserve upstream account payload, got %+v", rawAccount)
	}

	if !reflect.DeepEqual(gotPaths, []string{"GET /api/v1/statuses/post-1", "GET /api/v1/statuses/post-1"}) {
		t.Fatalf("unexpected Lesser status routes: %+v", gotPaths)
	}
}

func TestM5_TimelineReadCompactUsesStatusRefsAndPayloadBudget(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	const contentTail = "TIMELINE_FULL_CONTENT_TAIL_SHOULD_NOT_APPEAR"
	const accountNote = "TIMELINE_ACCOUNT_NOTE_SHOULD_NOT_APPEAR"
	const debugPayload = "TIMELINE_DEBUG_PAYLOAD_SHOULD_NOT_APPEAR"

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/timelines/home" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(socialStatusesFixtureJSON(5, "timeline", contentTail, accountNote, debugPayload)))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, env, sessionID, authHeader := newSocialToolTestSession(t)

	compact := callSocialTool(t, env, app, authHeader, sessionID, 2, "timeline_read", map[string]any{
		"timeline": "home",
		"limit":    5,
		"view":     "compact",
	})
	if gotQueries[0] != "limit=5" {
		t.Fatalf("expected compact timeline query limit=5, got %q", gotQueries[0])
	}
	assertMCPPayloadBudget(t, "timeline_read compact limit=5 large fixture", len(compact.ResponseBody), 6000)
	for _, forbidden := range []string{contentTail, accountNote, debugPayload, "account note ", "debug payload "} {
		if strings.Contains(string(compact.ResponseBody), forbidden) {
			t.Fatalf("compact timeline response leaked %q: %s", forbidden, string(compact.ResponseBody))
		}
	}
	data, _ := compact.Result.StructuredContent["data"].(map[string]any)
	if data["view"] != "compact" || data["timeline"] != "home" || data["count"] != float64(5) {
		t.Fatalf("unexpected compact timeline metadata: %+v", data)
	}
	statuses, _ := data["statuses"].([]any)
	if len(statuses) != 5 {
		t.Fatalf("expected 5 compact statuses, got %+v", data["statuses"])
	}
	firstStatus, _ := statuses[0].(map[string]any)
	if firstStatus["id"] != "timeline-1" || firstStatus["url"] == "" || firstStatus["visibility"] != "public" {
		t.Fatalf("unexpected compact status ref: %+v", firstStatus)
	}
	if preview, _ := firstStatus["contentPreview"].(string); preview == "" || len([]rune(preview)) > 160 || strings.Contains(preview, contentTail) {
		t.Fatalf("expected bounded compact preview without tail, got %q (%d runes)", preview, len([]rune(preview)))
	}
	if firstStatus["contentTruncated"] != true {
		t.Fatalf("expected contentTruncated marker, got %+v", firstStatus)
	}
	authorRef, _ := firstStatus["authorRef"].(map[string]any)
	if authorRef["id"] != "acct-timeline-1" || authorRef["acct"] != "timeline1@example.com" || authorRef["displayName"] != "Timeline 1" {
		t.Fatalf("unexpected authorRef: %+v", authorRef)
	}
	if _, ok := authorRef["note"]; ok {
		t.Fatalf("authorRef must not inline upstream account note: %+v", authorRef)
	}
	expand, _ := firstStatus["expand"].(map[string]any)
	expandArgs, _ := expand["arguments"].(map[string]any)
	if expand["tool"] != "post_get" || expandArgs["id"] != "timeline-1" || expandArgs["view"] != "standard" {
		t.Fatalf("unexpected compact expansion metadata: %+v", expand)
	}
	omitted, _ := firstStatus["omitted"].([]any)
	if len(omitted) != 1 {
		t.Fatalf("expected per-status omitted metadata, got %+v", firstStatus["omitted"])
	}
	omittedRecord, _ := omitted[0].(map[string]any)
	omittedExpand, _ := omittedRecord["expand"].(map[string]any)
	if omittedRecord["path"] != "content" || omittedExpand["tool"] != "post_get" || omittedExpand["resultPath"] != "structuredContent.data.status.content" {
		t.Fatalf("unexpected omitted metadata: %+v", omittedRecord)
	}
	topOmitted, _ := data["omitted"].([]any)
	if len(topOmitted) == 0 {
		t.Fatalf("compact timeline should name list-level omitted fields: %+v", data)
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(compact.Result.Content[0].Text), &text); err != nil {
		t.Fatalf("unmarshal compact text: %v", err)
	}
	if _, ok := text["statuses"]; ok {
		t.Fatalf("compact text should use structured-first locator instead of duplicating statuses: %+v", text)
	}
	if locator, _ := text["data"].(map[string]any); locator["location"] != "structuredContent.data" {
		t.Fatalf("expected structured data locator in compact text, got %+v", text)
	}

	standard := callSocialTool(t, env, app, authHeader, sessionID, 3, "timeline_read", map[string]any{
		"timeline": "home",
		"limit":    5,
		"view":     "standard",
	})
	assertMCPPayloadIncrease(t,
		"timeline_read compact limit=5 large fixture",
		len(compact.ResponseBody),
		"timeline_read standard large fixture",
		len(standard.ResponseBody),
	)
	if !strings.Contains(string(standard.ResponseBody), contentTail) || !strings.Contains(string(standard.ResponseBody), accountNote) {
		t.Fatalf("standard timeline response should preserve upstream shape/content")
	}

	defaultResult := callSocialTool(t, env, app, authHeader, sessionID, 4, "timeline_read", map[string]any{
		"timeline": "home",
		"limit":    5,
	})
	if _, ok := defaultResult.Result.StructuredContent["data"].([]any); !ok {
		t.Fatalf("default timeline response must remain upstream array-shaped, got %+v", defaultResult.Result.StructuredContent["data"])
	}
}

func TestM5_PostSearchCompactUsesStatusRefsAndPayloadBudget(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	const contentTail = "SEARCH_FULL_CONTENT_TAIL_SHOULD_NOT_APPEAR"
	const accountNote = "SEARCH_ACCOUNT_NOTE_SHOULD_NOT_APPEAR"
	const debugPayload = "SEARCH_DEBUG_PAYLOAD_SHOULD_NOT_APPEAR"

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"statuses":` + socialStatusesFixtureJSON(10, "search", contentTail, accountNote, debugPayload) + `,
			"accounts":[{"id":"acct-search-root","acct":"root@example.com","display_name":"Root","url":"https://example.com/@root","note":"` + accountNote + ` ` + strings.Repeat("search account note ", 100) + `"}],
			"hashtags":[{"name":"mcp","url":"https://example.com/tags/mcp","history":[{"day":"1","uses":"100"}]}],
			"debugPayload":{"large":"` + debugPayload + ` ` + strings.Repeat("debug payload ", 200) + `"}
		}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, env, sessionID, authHeader := newSocialToolTestSession(t)

	compact := callSocialTool(t, env, app, authHeader, sessionID, 2, "post_search", map[string]any{
		"query": "mcp",
		"limit": 10,
		"view":  "compact",
	})
	if compact.Result.IsError {
		t.Fatalf("post_search compact returned tool error: %+v body=%s", compact.Result.StructuredContent, string(compact.ResponseBody))
	}
	if gotQueries[0] != "limit=10&q=mcp&type=statuses" {
		t.Fatalf("expected compact search query, got %q", gotQueries[0])
	}
	assertMCPPayloadBudget(t, "post_search compact limit=10 large fixture", len(compact.ResponseBody), 8000)
	for _, forbidden := range []string{contentTail, accountNote, debugPayload, "search account note ", "debug payload "} {
		if strings.Contains(string(compact.ResponseBody), forbidden) {
			t.Fatalf("compact search response leaked %q: %s", forbidden, string(compact.ResponseBody))
		}
	}

	data, _ := compact.Result.StructuredContent["data"].(map[string]any)
	if data["view"] != "compact" || data["query"] != "mcp" || data["count"] != float64(10) {
		t.Fatalf("unexpected compact search metadata: %+v", data)
	}
	statuses, _ := data["statuses"].([]any)
	if len(statuses) != 10 {
		t.Fatalf("expected 10 compact statuses, got %+v", data["statuses"])
	}
	firstStatus, _ := statuses[0].(map[string]any)
	if firstStatus["id"] != "search-1" || firstStatus["contentTruncated"] != true {
		t.Fatalf("unexpected compact search status: %+v", firstStatus)
	}
	expand, _ := firstStatus["expand"].(map[string]any)
	expandArgs, _ := expand["arguments"].(map[string]any)
	if expand["tool"] != "post_get" || expandArgs["id"] != "search-1" || expandArgs["view"] != "standard" {
		t.Fatalf("unexpected compact search expansion: %+v", expand)
	}
	accounts, _ := data["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("expected compact account refs for search accounts, got %+v", data["accounts"])
	}
	accountRef, _ := accounts[0].(map[string]any)
	if accountRef["id"] != "acct-search-root" || accountRef["acct"] != "root@example.com" || accountRef["displayName"] != "Root" {
		t.Fatalf("unexpected compact search account ref: %+v", accountRef)
	}
	if _, ok := accountRef["note"]; ok {
		t.Fatalf("search account ref must not inline upstream note: %+v", accountRef)
	}
	hashtags, _ := data["hashtags"].([]any)
	if len(hashtags) != 1 {
		t.Fatalf("expected compact hashtag summary, got %+v", data["hashtags"])
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(compact.Result.Content[0].Text), &text); err != nil {
		t.Fatalf("unmarshal compact search text: %v", err)
	}
	if _, ok := text["statuses"]; ok {
		t.Fatalf("compact search text should not duplicate statuses: %+v", text)
	}

	standard := callSocialTool(t, env, app, authHeader, sessionID, 3, "post_search", map[string]any{
		"query": "mcp",
		"limit": 10,
		"view":  "standard",
	})
	assertMCPPayloadIncrease(t,
		"post_search compact limit=10 large fixture",
		len(compact.ResponseBody),
		"post_search standard large fixture",
		len(standard.ResponseBody),
	)
	if !strings.Contains(string(standard.ResponseBody), contentTail) || !strings.Contains(string(standard.ResponseBody), accountNote) {
		t.Fatalf("standard search response should preserve upstream payload")
	}
}

func TestM5_SocialCompactHonorsMaxOutputBytesTooLarge(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/timelines/home" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(socialStatusesFixtureJSON(5, "tiny-budget", "tail", "note", "debug")))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, env, sessionID, authHeader := newSocialToolTestSession(t)

	result := callSocialTool(t, env, app, authHeader, sessionID, 2, "timeline_read", map[string]any{
		"timeline":         "home",
		"limit":            5,
		"view":             "compact",
		"max_output_bytes": 1000,
	})
	if !result.Result.IsError {
		t.Fatalf("expected response_too_large tool error, got %+v", result.Result)
	}
	errorPayload, _ := result.Result.StructuredContent["error"].(map[string]any)
	if errorPayload["code"] != "response_too_large" || errorPayload["status"] != float64(413) {
		t.Fatalf("unexpected too-large error payload: %+v", errorPayload)
	}
	details, _ := errorPayload["details"].(map[string]any)
	if details["tool"] != "timeline_read" || details["maxOutputBytes"] != float64(1000) || details["measuredBytes"] == float64(0) {
		t.Fatalf("unexpected too-large details: %+v", details)
	}
}

func TestM5_ConversationsReadUsesCompactBoundedDefaults(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/conversations" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id":"conv-1",
				"unread":true,
				"updated_at":"2026-05-10T13:00:00Z",
				"accounts":[{"id":"acct-1","username":"alice","acct":"alice@example.com","display_name":"Alice","note":"` + strings.Repeat("bio ", 200) + `"}],
				"last_status":{"id":"post-1","content":"` + strings.Repeat("conversation-content ", 80) + `","created_at":"2026-05-10T12:59:00Z","visibility":"direct"},
				"debugPayload":{"large":"` + strings.Repeat("x", 4096) + `"}
			}
		]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	responseBytes := map[int]int{}
	call := func(id int, args map[string]any) map[string]any {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": "conversations_read", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("conversations_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		responseBytes[id] = len(resp.Body)
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal conversations_read: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("conversations_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data == nil {
			t.Fatalf("expected structured data, got %+v", out.StructuredContent)
		}
		return data
	}

	defaultData := call(2, map[string]any{})
	if gotQueries[0] != "limit=20" {
		t.Fatalf("expected bounded default limit query, got %q", gotQueries[0])
	}
	conversations, _ := defaultData["conversations"].([]any)
	if len(conversations) != 1 {
		t.Fatalf("expected one conversation, got %+v", defaultData)
	}
	conversation, _ := conversations[0].(map[string]any)
	if _, ok := conversation["raw"]; ok {
		t.Fatalf("raw field should be absent by default: %+v", conversation)
	}
	if _, ok := conversation["_raw"]; ok {
		t.Fatalf("_raw field should be absent by default: %+v", conversation)
	}
	participants, _ := conversation["participants"].([]any)
	if len(participants) != 1 {
		t.Fatalf("expected compact participants, got %+v", conversation)
	}
	lastPost, _ := conversation["lastPost"].(map[string]any)
	if content, _ := lastPost["content"].(string); len([]rune(content)) > 500 {
		t.Fatalf("expected bounded lastPost content, got %d runes", len([]rune(content)))
	}
	assertMCPPayloadBudget(t, "conversations_read default compact large fixture", responseBytes[2], 7000)

	rawData := call(3, map[string]any{"limit": 200, "include_raw": true})
	if gotQueries[1] != "limit=80" {
		t.Fatalf("expected max bounded limit query, got %q", gotQueries[1])
	}
	rawConversations, _ := rawData["conversations"].([]any)
	rawConversation, _ := rawConversations[0].(map[string]any)
	if _, ok := rawConversation["_raw"].(map[string]any); !ok {
		t.Fatalf("expected include_raw=true to expose _raw, got %+v", rawConversation)
	}
	assertMCPPayloadIncrease(t,
		"conversations_read default compact large fixture",
		responseBytes[2],
		"conversations_read include_raw expanded fixture",
		responseBytes[3],
	)
}

func TestM5_NotificationsReadReturnsStructuredNotifications(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RawQuery {
		case "limit=5&max_id=n0&types%5B%5D=reply":
			// Current Lesser instances may still emit mention here; lesser-body should not relabel it.
			_, _ = w.Write([]byte(`[
				{
					"id":"n-reply-like-mention",
					"type":"mention",
					"created_at":"2026-03-16T09:00:00Z",
					"account":{"id":"acct-reply","username":"alice","acct":"alice@example.com","display_name":"Alice"},
					"status":{
						"id":"post-1",
						"content":"@agent1 thanks!",
						"created_at":"2026-03-16T08:59:00Z",
						"in_reply_to_id":"root-1",
						"visibility":"public"
					}
				}
			]`))
		case "limit=5&max_id=n0&types%5B%5D=favourite":
			_, _ = w.Write([]byte(`[
				{
					"id":"n-fav",
					"type":"favourite",
					"created_at":"2026-03-16T08:00:00Z",
					"account":{"id":"acct-fav","username":"carol","acct":"carol@example.com"},
					"status":{"id":"post-2","content":"Great post","visibility":"public"}
				}
			]`))
		default:
			t.Fatalf("unexpected notifications query: %q", r.URL.RawQuery)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

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
		"name": "notifications_read",
		"arguments": map[string]any{
			"limit": 5,
			"since": "n0",
			"types": []string{"reply", "favorite"},
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
		t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 notifications queries, got %v", gotQueries)
	}
	if gotQueries[0] != "limit=5&max_id=n0&types%5B%5D=reply" || gotQueries[1] != "limit=5&max_id=n0&types%5B%5D=favourite" {
		t.Fatalf("unexpected notifications queries: %v", gotQueries)
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal notifications_read: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
	}

	var out mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
	}
	data, _ := out.StructuredContent["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("expected filtered count=1, got %+v", data)
	}
	if data["nextSince"] != "n-fav" {
		t.Fatalf("expected nextSince=n-fav, got %+v", data)
	}

	notifications, _ := data["notifications"].([]any)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %+v", notifications)
	}

	favourite, _ := notifications[0].(map[string]any)
	if favourite["type"] != "favourite" {
		t.Fatalf("expected favourite notification type, got %+v", favourite)
	}
}

func TestM5_NotificationsReadOmitsRawByDefaultAndExposesDiagnosticsWhenRequested(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x1111111111111111111111111111111111111111111111111111111111111111")
	auth.ResetForTests()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id":"n-mail",
				"type":"communication:inbound",
				"created_at":"2026-05-10T12:00:00Z",
				"channel":"email",
				"messageId":"comm-delivery-email",
				"from":{"name":"Sender","address":"sender@example.com","soulAgentId":"0xabc"},
				"to":[{"address":"agent@example.com"}],
				"subject":"Hello",
				"body":"` + strings.Repeat("long-body ", 80) + `",
				"status":{"id":"post-1","content":"` + strings.Repeat("long-post ", 120) + `","visibility":"public"},
				"debugPayload":{"large":"` + strings.Repeat("x", 4096) + `"}
			}
		]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	responseBytes := map[int]int{}
	call := func(id int, args map[string]any) map[string]any {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": "notifications_read", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		responseBytes[id] = len(resp.Body)

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal notifications_read: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data == nil {
			t.Fatalf("expected structured data, got %+v", out.StructuredContent)
		}
		return data
	}

	defaultData := call(2, map[string]any{"limit": 20})
	notifications, _ := defaultData["notifications"].([]any)
	if len(notifications) != 1 {
		t.Fatalf("expected one notification, got %+v", defaultData)
	}
	notification, _ := notifications[0].(map[string]any)
	if _, ok := notification["raw"]; ok {
		t.Fatalf("raw field should be absent by default: %+v", notification)
	}
	if _, ok := notification["_raw"]; ok {
		t.Fatalf("_raw field should be absent by default: %+v", notification)
	}
	comm, _ := notification["communication"].(map[string]any)
	if comm["messageId"] != "comm-delivery-email" || comm["channel"] != "email" {
		t.Fatalf("expected compact communication summary, got %+v", comm)
	}
	if preview, _ := comm["preview"].(string); len([]rune(preview)) > 240 {
		t.Fatalf("expected bounded communication preview, got %d runes", len([]rune(preview)))
	}
	post, _ := notification["targetPost"].(map[string]any)
	if content, _ := post["content"].(string); len([]rune(content)) > 500 {
		t.Fatalf("expected bounded targetPost content, got %d runes", len([]rune(content)))
	}
	if _, ok := defaultData["diagnostics"]; ok {
		t.Fatalf("diagnostics should be opt-in by default, got %+v", defaultData["diagnostics"])
	}
	assertMCPPayloadBudget(t, "notifications_read default compact large fixture", responseBytes[2], 7000)

	diagnosticData := call(3, map[string]any{"limit": 20, "include_diagnostics": true})
	diagnostics, _ := diagnosticData["diagnostics"].(map[string]any)
	if diagnostics["upstreamCount"] != float64(1) || diagnostics["normalizedCount"] != float64(1) {
		t.Fatalf("expected notification diagnostics counts, got %+v", diagnostics)
	}
	if diagnostics["responseBytes"] == float64(0) || diagnostics["mcpPayloadBytes"] == float64(0) {
		t.Fatalf("expected response size diagnostics, got %+v", diagnostics)
	}

	rawData := call(4, map[string]any{"limit": 20, "include_raw": true})
	rawNotifications, _ := rawData["notifications"].([]any)
	rawNotification, _ := rawNotifications[0].(map[string]any)
	if _, ok := rawNotification["_raw"].(map[string]any); !ok {
		t.Fatalf("expected include_raw=true to expose _raw, got %+v", rawNotification)
	}
	if _, ok := rawNotification["raw"]; ok {
		t.Fatalf("include_raw=true should use _raw, not raw: %+v", rawNotification)
	}
	assertMCPPayloadIncrease(t,
		"notifications_read default compact large fixture",
		responseBytes[2],
		"notifications_read include_raw expanded fixture",
		responseBytes[4],
	)
}

func TestM5_NotificationsReadSupportsCommunicationInboundFilter(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installSoulBindingLookup(t, "agent1", "0x2222222222222222222222222222222222222222222222222222222222222222")
	auth.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.RawQuery != "limit=5&types%5B%5D=communication%3Ainbound" {
			t.Fatalf("expected communication:inbound to be accepted as a notification type filter, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[
			{
				"id":"n-mail",
				"type":"communication:inbound",
				"created_at":"2026-05-10T12:00:00Z",
				"channel":"email",
				"messageId":"comm-delivery-email",
				"from":{"name":"Sender","address":"sender@example.com"},
				"subject":"Hello",
				"preview":"email preview"
			},
			{
				"id":"n-mention",
				"type":"mention",
				"created_at":"2026-05-10T11:00:00Z",
				"account":{"id":"acct-1","acct":"alice@example.com"},
				"status":{"id":"post-1","content":"hello","visibility":"public"}
			}
		]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "notifications_read",
		"arguments": map[string]any{
			"limit": 5,
			"types": []string{"communication:inbound"},
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if len(gotQueries) != 1 {
		t.Fatalf("expected one upstream request, got %v", gotQueries)
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal notifications_read: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	data, _ := out.StructuredContent["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("expected one communication notification, got %+v", data)
	}
	types, _ := data["types"].([]any)
	if len(types) != 1 || types[0] != "communication:inbound" {
		t.Fatalf("expected requested type to echo communication:inbound, got %+v", data["types"])
	}
	notifications, _ := data["notifications"].([]any)
	if len(notifications) != 1 {
		t.Fatalf("expected one filtered notification, got %+v", notifications)
	}
	notification, _ := notifications[0].(map[string]any)
	if notification["type"] != "communication:inbound" {
		t.Fatalf("expected communication:inbound notification, got %+v", notification)
	}
	comm, _ := notification["communication"].(map[string]any)
	if comm["messageId"] != "comm-delivery-email" || comm["channel"] != "email" {
		t.Fatalf("expected compact communication summary, got %+v", comm)
	}
}

func TestM2_DroneNotificationsReadBlocksCommunicationNotifications(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()

	const privateSentinel = "private-drone-comm-sentinel@example.test"
	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id":"n-mail",
				"type":"communication:inbound",
				"created_at":"2026-05-10T12:00:00Z",
				"channel":"email",
				"messageId":"comm-delivery-email",
				"from":{"address":"` + privateSentinel + `"},
				"subject":"Private",
				"preview":"private preview"
			},
			{
				"id":"n-mention",
				"type":"mention",
				"created_at":"2026-05-10T11:00:00Z",
				"account":{"id":"acct-1","acct":"alice@example.com"},
				"status":{"id":"post-1","content":"hello","visibility":"public"}
			}
		]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	call := func(id int, args map[string]any) mcpruntime.ToolResult {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": "notifications_read", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal notifications_read: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
		return out
	}

	blocked := call(2, map[string]any{"limit": 5, "types": []string{"communication:inbound"}})
	if !blocked.IsError {
		t.Fatalf("expected communication notification filter to be rejected for drone runtime")
	}
	errPayload, _ := blocked.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "runtime_boundary" || errPayload["status"] != float64(403) {
		t.Fatalf("unexpected runtime-boundary error: %+v", errPayload)
	}
	if len(gotQueries) != 0 {
		t.Fatalf("explicit communication filter should be rejected before Lesser call, got queries %v", gotQueries)
	}

	filtered := call(3, map[string]any{"limit": 5, "include_raw": true})
	if filtered.IsError {
		t.Fatalf("unexpected tool error filtering untyped notifications: %+v", filtered.StructuredContent)
	}
	data, _ := filtered.StructuredContent["data"].(map[string]any)
	notifications, _ := data["notifications"].([]any)
	if len(notifications) != 1 {
		t.Fatalf("expected only social notification for drone runtime, got %+v", data)
	}
	notification, _ := notifications[0].(map[string]any)
	if notification["type"] != "mention" {
		t.Fatalf("expected communication notification to be filtered, got %+v", notification)
	}
	b, _ := json.Marshal(filtered)
	if strings.Contains(string(b), privateSentinel) || strings.Contains(string(b), "private preview") {
		t.Fatalf("drone notification response leaked communication metadata: %s", string(b))
	}
}

func TestM5_NotificationsReadPreservesReadState(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"n-unread","type":"mention","read":false,"created_at":"2026-05-11T02:00:00Z","account":{"id":"acct-1","acct":"alice@example.com"},"status":{"id":"post-1","content":"hello","visibility":"public"}},
			{"id":"n-read","type":"reply","read":true,"read_at":"2026-05-11T02:05:00Z","created_at":"2026-05-11T02:04:00Z","account":{"id":"acct-2","acct":"bob@example.com"},"status":{"id":"post-2","content":"read reply","visibility":"public"}},
			{"id":"n-unread-shape","type":"favourite","unread":true,"created_at":"2026-05-11T02:03:00Z","account":{"id":"acct-3","acct":"carol@example.com"},"status":{"id":"post-3","content":"fav","visibility":"public"}},
			{"id":"n-read-shape","type":"reblog","unread":false,"created_at":"2026-05-11T02:02:00Z","account":{"id":"acct-4","acct":"drew@example.com"},"status":{"id":"post-4","content":"boost","visibility":"public"}}
		]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{"name": "notifications_read", "arguments": map[string]any{"limit": 20}})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal notifications_read: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	data, _ := out.StructuredContent["data"].(map[string]any)
	notifications, _ := data["notifications"].([]any)
	byID := map[string]map[string]any{}
	for _, item := range notifications {
		n, _ := item.(map[string]any)
		id, _ := n["id"].(string)
		byID[id] = n
		if _, ok := n["raw"]; ok {
			t.Fatalf("raw field should be absent by default: %+v", n)
		}
		if _, ok := n["_raw"]; ok {
			t.Fatalf("_raw field should be absent by default: %+v", n)
		}
	}

	if byID["n-unread"]["read"] != false {
		t.Fatalf("expected read:false to be preserved, got %+v", byID["n-unread"])
	}
	if byID["n-read"]["read"] != true || byID["n-read"]["readAt"] != "2026-05-11T02:05:00Z" {
		t.Fatalf("expected read:true and readAt to be preserved, got %+v", byID["n-read"])
	}
	if byID["n-unread-shape"]["read"] != false || byID["n-unread-shape"]["unread"] != true {
		t.Fatalf("expected unread:true to infer read:false and preserve unread, got %+v", byID["n-unread-shape"])
	}
	if byID["n-read-shape"]["read"] != true || byID["n-read-shape"]["unread"] != false {
		t.Fatalf("expected unread:false to infer read:true and preserve unread, got %+v", byID["n-read-shape"])
	}
}

func TestM5_ProfileReadRejectsNonObjectArguments(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

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
		"name":      "profile_read",
		"arguments": []any{},
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
	if rpc.Error == nil || rpc.Error.Code != mcpruntime.CodeServerError {
		t.Fatalf("expected server error, got: %+v", rpc.Error)
	}
	if requests != 0 {
		t.Fatalf("expected no upstream requests, got %d", requests)
	}
}

func TestM5_NotificationsReadTimestampSinceFiltersRecentRemoteCreates(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RawQuery {
		case "limit=30":
			_, _ = w.Write([]byte(`[
				{"id":"old-mention","type":"mention","created_at":"2026-04-04T12:00:00Z","account":{"id":"acct-old","acct":"old@example.com"},"status":{"id":"old-post","content":"old mention","visibility":"public"}},
				{"id":"remote-create-mention-9addcb94","type":"mention","created_at":"2026-04-24T20:07:34.02332832Z","account":{"id":"https://dev.theory.greater.website/users/steward","acct":"steward@dev.theory.greater.website","display_name":"Steward"},"status":{"id":"remote-status-mention","content":"PROJECT20-1248-DEDUPE","visibility":"public"}},
				{"id":"remote-create-reply-a33f4d5","type":"reply","created_at":"2026-04-24T20:07:40.451151151Z","account":{"id":"https://dev.theory.greater.website/users/steward","acct":"steward@dev.theory.greater.website","display_name":"Steward"},"status":{"id":"remote-status-reply","content":"PROJECT20-1248-REPLY","in_reply_to_id":"parent-1","visibility":"public"}}
			]`))
		case "limit=30&types%5B%5D=mention":
			_, _ = w.Write([]byte(`[
				{"id":"old-typed-mention","type":"mention","created_at":"2026-04-04T12:00:00Z","account":{"id":"acct-old","acct":"old@example.com"},"status":{"id":"old-post","content":"old mention","visibility":"public"}},
				{"id":"remote-create-mention-9addcb94","type":"mention","created_at":"2026-04-24T20:07:34.02332832Z","account":{"id":"https://dev.theory.greater.website/users/steward","acct":"steward@dev.theory.greater.website","display_name":"Steward"},"status":{"id":"remote-status-mention","content":"PROJECT20-1248-DEDUPE","visibility":"public"}}
			]`))
		case "limit=30&types%5B%5D=reply":
			_, _ = w.Write([]byte(`[
				{"id":"remote-create-reply-a33f4d5","type":"reply","created_at":"2026-04-24T20:07:40.451151151Z","account":{"id":"https://dev.theory.greater.website/users/steward","acct":"steward@dev.theory.greater.website","display_name":"Steward"},"status":{"id":"remote-status-reply","content":"PROJECT20-1248-REPLY","in_reply_to_id":"parent-1","visibility":"public"}}
			]`))
		default:
			t.Fatalf("unexpected notifications query: %q", r.URL.RawQuery)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

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

	callNotifications := func(id int, arguments map[string]any) map[string]any {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "notifications_read",
			"arguments": arguments,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal notifications_read: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("notifications_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		data, _ := out.StructuredContent["data"].(map[string]any)
		return data
	}

	data := callNotifications(2, map[string]any{
		"limit": 30,
		"since": "2026-04-24T20:06:00Z",
	})
	if data["count"] != float64(2) {
		t.Fatalf("expected two fresh notifications after timestamp since, got %+v", data)
	}
	if data["nextSince"] != "2026-04-24T20:07:40.451151151Z" {
		t.Fatalf("expected timestamp nextSince from newest notification, got %+v", data)
	}
	notifications, _ := data["notifications"].([]any)
	seen := map[string]bool{}
	for _, item := range notifications {
		n, _ := item.(map[string]any)
		seen[n["id"].(string)] = true
	}
	if !seen["remote-create-mention-9addcb94"] || !seen["remote-create-reply-a33f4d5"] || seen["old-mention"] {
		t.Fatalf("unexpected timestamp-filtered notifications: %+v", notifications)
	}

	typed := callNotifications(3, map[string]any{
		"limit": 30,
		"since": "2026-04-24T20:06:00Z",
		"types": []string{"mention", "reply"},
	})
	if typed["count"] != float64(2) {
		t.Fatalf("expected fresh mention and reply after typed timestamp since, got %+v", typed)
	}

	wantQueries := []string{
		"limit=30",
		"limit=30&types%5B%5D=mention",
		"limit=30&types%5B%5D=reply",
	}
	if len(gotQueries) != len(wantQueries) {
		t.Fatalf("expected %d notifications queries, got %v", len(wantQueries), gotQueries)
	}
	for i, want := range wantQueries {
		if gotQueries[i] != want {
			t.Fatalf("query %d: want %q, got %q", i, want, gotQueries[i])
		}
		if strings.Contains(gotQueries[i], "max_id=") {
			t.Fatalf("timestamp since must not be forwarded as max_id: %v", gotQueries)
		}
	}
}

func TestM5_NotificationsReadBoundsLimitAndRejectsUnknownTypes(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.RawQuery != "limit=80" {
			t.Fatalf("expected capped limit query, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	call := func(id int, arguments map[string]any) *mcpruntime.Response {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "notifications_read",
			"arguments": arguments,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("notifications_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal notifications_read: %v", err)
		}
		return &rpc
	}

	if rpc := call(2, map[string]any{"limit": 500}); rpc.Error != nil {
		t.Fatalf("expected capped limit request to succeed, got %+v", rpc.Error)
	}
	if len(gotQueries) != 1 {
		t.Fatalf("expected one upstream request, got %v", gotQueries)
	}

	rpc := call(3, map[string]any{"limit": 5, "types": []string{"mention", "not-a-real-type"}})
	if rpc.Error == nil {
		t.Fatalf("expected unknown notification type to fail before upstream fanout")
	}
	if !strings.Contains(rpc.Error.Message, "supported values") || !strings.Contains(rpc.Error.Message, "communication:inbound") {
		t.Fatalf("unsupported type error should enumerate supported values, got %+v", rpc.Error)
	}
	if len(gotQueries) != 1 {
		t.Fatalf("invalid type should not fan out upstream, got %v", gotQueries)
	}

	tooManyTypes := []string{"mention", "mention", "mention", "mention", "mention", "mention", "mention", "mention", "mention"}
	rpc = call(4, map[string]any{"limit": 5, "types": tooManyTypes})
	if rpc.Error == nil {
		t.Fatalf("expected excess notification type entries to fail before upstream fanout")
	}
	if !strings.Contains(rpc.Error.Message, "maximum 8") {
		t.Fatalf("excess type error should include maximum budget, got %+v", rpc.Error)
	}
	if len(gotQueries) != 1 {
		t.Fatalf("excess type entries should not fan out upstream, got %v", gotQueries)
	}
}

func TestM5_NotificationsReadSeparatesTimestampSinceAndCursor(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RawQuery {
		case "limit=5":
			_, _ = w.Write([]byte(`[
				{"id":"n3","type":"mention","created_at":"2026-03-01T03:00:00Z","account":{"id":"acct-3","acct":"agent3","display_name":"Agent 3"},"status":{"id":"post-3","content":"Newest","visibility":"public"}},
				{"id":"n2","type":"mention","created_at":"2026-03-01T02:00:00Z","account":{"id":"acct-2","acct":"agent2","display_name":"Agent 2"},"status":{"id":"post-2","content":"Older","visibility":"public"}}
			]`))
		case "limit=5&max_id=notif%2320260301030000%23n3":
			w.Header().Set("Link", `<https://example.test/api/v1/notifications?max_id=notif%2320260301020000%23n2&limit=5>; rel="next"`)
			_, _ = w.Write([]byte(`[
				{"id":"n2","type":"mention","created_at":"2026-03-01T02:00:00Z","account":{"id":"acct-2","acct":"agent2","display_name":"Agent 2"},"status":{"id":"post-2","content":"Cursor page","visibility":"public"}}
			]`))
		case "limit=5&max_id=n1":
			_, _ = w.Write([]byte(`[
				{"id":"n1-old","type":"mention","created_at":"2026-03-01T01:00:00Z","account":{"id":"acct-1","acct":"agent1","display_name":"Agent 1"},"status":{"id":"post-1","content":"Legacy cursor","visibility":"public"}}
			]`))
		default:
			t.Fatalf("unexpected notifications query: %q", r.URL.RawQuery)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read", "write"})
	authHeader := "Bearer " + token

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

	callTool := func(id int, name string, arguments map[string]any) map[string]any {
		callParams, _ := json.Marshal(map[string]any{
			"name":      name,
			"arguments": arguments,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}
		if rpc.Error != nil {
			t.Fatalf("%s error: %+v", name, rpc.Error)
		}

		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if name == "memory_append" || name == "memory_query" {
			return out.StructuredContent
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		return data
	}

	first := callTool(2, "notifications_read", map[string]any{"limit": 5})
	if first["since"] != "" || first["cursor"] != "" {
		t.Fatalf("expected empty since/cursor on first call, got %+v", first)
	}
	if first["nextSince"] != "2026-03-01T03:00:00Z" {
		t.Fatalf("expected timestamp nextSince on first call, got %+v", first)
	}

	second := callTool(3, "notifications_read", map[string]any{"limit": 5})
	if second["since"] != "" || second["cursor"] != "" {
		t.Fatalf("expected omitted since to avoid implicit cursor, got %+v", second)
	}

	cursor := callTool(4, "notifications_read", map[string]any{"limit": 5, "cursor": "notif#20260301030000#n3"})
	if cursor["cursor"] != "notif#20260301030000#n3" || cursor["nextCursor"] != "notif#20260301020000#n2" {
		t.Fatalf("expected explicit cursor and nextCursor, got %+v", cursor)
	}

	legacy := callTool(5, "notifications_read", map[string]any{"limit": 5, "since": "n1"})
	if legacy["since"] != "n1" || legacy["cursor"] != "n1" {
		t.Fatalf("expected legacy non-timestamp since to act as cursor alias, got %+v", legacy)
	}

	reset := callTool(6, "notifications_read", map[string]any{"limit": 5, "since": ""})
	if reset["since"] != "" || reset["cursor"] != "" {
		t.Fatalf("expected empty since on reset call, got %+v", reset)
	}
	if reset["count"] != float64(2) {
		t.Fatalf("expected reset call to return full list, got %+v", reset)
	}

	wantQueries := []string{
		"limit=5",
		"limit=5",
		"limit=5&max_id=notif%2320260301030000%23n3",
		"limit=5&max_id=n1",
		"limit=5",
	}
	if len(gotQueries) != len(wantQueries) {
		t.Fatalf("expected %d notifications queries, got %v", len(wantQueries), gotQueries)
	}
	for i, want := range wantQueries {
		if gotQueries[i] != want {
			t.Fatalf("query %d: want %q, got %q", i, want, gotQueries[i])
		}
	}
}

func TestM5_NotificationDismissClearsCursorOnDismissAll(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	var gotRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests = append(gotRequests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications":
			_, _ = w.Write([]byte(`[
				{"id":"n7","type":"mention","created_at":"2026-03-01T07:00:00Z","account":{"id":"acct-7","acct":"agent7","display_name":"Agent 7"},"status":{"id":"post-7","content":"Seed cursor","visibility":"public"}}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/notifications/clear":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read", "write"})
	authHeader := "Bearer " + token

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

	callTool := func(id int, name string, arguments map[string]any) map[string]any {
		callParams, _ := json.Marshal(map[string]any{
			"name":      name,
			"arguments": arguments,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}
		if rpc.Error != nil {
			t.Fatalf("%s error: %+v", name, rpc.Error)
		}

		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if name == "memory_append" || name == "memory_query" {
			return out.StructuredContent
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		return data
	}

	readCursor := func(id int) string {
		data := callTool(id, "memory_query", map[string]any{
			"query": "notification_cursor:",
			"limit": 1,
			"order": "desc",
		})
		events, _ := data["events"].([]any)
		if len(events) == 0 {
			return ""
		}
		event, _ := events[0].(map[string]any)
		content, _ := event["content"].(string)
		return strings.TrimSpace(strings.TrimPrefix(content, "notification_cursor:"))
	}

	_ = callTool(2, "memory_append", map[string]any{
		"content":  "notification_cursor:n7",
		"event_id": "01JMY4Y6A00000000000000007",
	})

	out := callTool(3, "notification_dismiss", map[string]any{})
	if out["ok"] != true || out["dismiss"] != "all" {
		t.Fatalf("unexpected dismiss-all response: %+v", out)
	}
	if readCursor(4) != "" {
		t.Fatalf("expected dismiss-all to clear cursor")
	}

	wantRequests := []string{"POST /api/v1/notifications/clear"}
	if len(gotRequests) != len(wantRequests) {
		t.Fatalf("expected requests %v, got %v", wantRequests, gotRequests)
	}
	for i, want := range wantRequests {
		if gotRequests[i] != want {
			t.Fatalf("request %d: want %q, got %q", i, want, gotRequests[i])
		}
	}
}

func TestM5_NotificationDismissSingleKeepsCursorAndHandlesNotFound(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_BODY_MEMORY_STORE", "memory")
	auth.ResetForTests()
	memory.ResetForTests()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications":
			_, _ = w.Write([]byte(`[
				{"id":"n9","type":"mention","created_at":"2026-03-01T09:00:00Z","account":{"id":"acct-9","acct":"agent9","display_name":"Agent 9"},"status":{"id":"post-9","content":"Seed cursor","visibility":"public"}}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/notifications/n9/dismiss":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/notifications/missing/dismiss":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read", "write"})
	authHeader := "Bearer " + token

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

	callTool := func(id int, name string, arguments map[string]any) (*mcpruntime.Response, *mcpruntime.ToolResult) {
		callParams, _ := json.Marshal(map[string]any{
			"name":      name,
			"arguments": arguments,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params:  callParams,
		})
		if resp.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}

		var out mcpruntime.ToolResult
		if rpc.Error == nil {
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		return &rpc, &out
	}

	readCursor := func(id int) string {
		rpc, out := callTool(id, "memory_query", map[string]any{
			"query": "notification_cursor:",
			"limit": 1,
			"order": "desc",
		})
		if rpc.Error != nil {
			t.Fatalf("memory_query error: %+v", rpc.Error)
		}
		events, _ := out.StructuredContent["events"].([]any)
		if len(events) == 0 {
			return ""
		}
		event, _ := events[0].(map[string]any)
		content, _ := event["content"].(string)
		return strings.TrimSpace(strings.TrimPrefix(content, "notification_cursor:"))
	}

	rpc, _ := callTool(2, "memory_append", map[string]any{
		"content":  "notification_cursor:n9",
		"event_id": "01JMY4Y6A00000000000000009",
	})
	if rpc.Error != nil {
		t.Fatalf("memory_append error: %+v", rpc.Error)
	}
	if readCursor(3) != "n9" {
		t.Fatalf("expected cursor n9 after seed append")
	}

	rpc, out := callTool(4, "notification_dismiss", map[string]any{"id": "n9"})
	if rpc.Error != nil {
		t.Fatalf("notification_dismiss error: %+v", rpc.Error)
	}
	data, _ := out.StructuredContent["data"].(map[string]any)
	if data["ok"] != true || data["id"] != "n9" || data["dismiss"] != "single" {
		t.Fatalf("unexpected single dismiss response: %+v", data)
	}
	if readCursor(5) != "n9" {
		t.Fatalf("expected single dismiss to keep cursor")
	}

	rpc, _ = callTool(6, "notification_dismiss", map[string]any{"id": "missing"})
	if rpc.Error == nil {
		t.Fatalf("expected not-found error")
	}
	if !strings.Contains(strings.ToLower(rpc.Error.Message), "not found") {
		t.Fatalf("expected not-found error message, got %+v", rpc.Error)
	}
}

type socialToolCallResult struct {
	ResponseBody []byte
	RPC          mcpruntime.Response
	Result       mcpruntime.ToolResult
}

func newSocialToolTestSession(t testing.TB) (*apptheory.App, *testkit.Env, string, string) {
	t.Helper()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	return app, env, initResp.Headers["mcp-session-id"][0], authHeader
}

func callSocialTool(t testing.TB, env *testkit.Env, app *apptheory.App, authHeader string, sessionID string, id int, name string, args map[string]any) socialToolCallResult {
	t.Helper()

	callParams, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s rpc error: %+v", name, rpc.Error)
	}

	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	return socialToolCallResult{
		ResponseBody: resp.Body,
		RPC:          rpc,
		Result:       out,
	}
}

func socialStatusesFixtureJSON(count int, prefix string, contentTail string, accountNote string, debugPayload string) string {
	var b strings.Builder
	b.WriteString("[")
	titlePrefix := strings.ToUpper(prefix[:1]) + prefix[1:]
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		content := strings.Repeat(fmt.Sprintf("%s content %02d ", prefix, i), 18) + contentTail
		b.WriteString(fmt.Sprintf(`{
			"id":"%s-%d",
			"url":"https://example.com/@%s%d/%s-%d",
			"created_at":"2026-05-17T17:%02d:00Z",
			"visibility":"public",
			"content":"%s",
			"account":{
				"id":"acct-%s-%d",
				"username":"%s%d",
				"acct":"%s%d@example.com",
				"display_name":"%s %d",
				"url":"https://example.com/@%s%d",
				"note":"%s %s"
			},
			"debugPayload":{"large":"%s %s"}
		}`,
			prefix, i,
			prefix, i, prefix, i,
			i,
			content,
			prefix, i,
			prefix, i,
			prefix, i,
			titlePrefix, i,
			prefix, i,
			accountNote, strings.Repeat("account note ", 80),
			debugPayload, strings.Repeat("debug payload ", 80),
		))
	}
	b.WriteString("]")
	return b.String()
}
