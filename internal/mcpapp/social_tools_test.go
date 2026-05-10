package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	rawData := call(3, map[string]any{"limit": 200, "include_raw": true})
	if gotQueries[1] != "limit=80" {
		t.Fatalf("expected max bounded limit query, got %q", gotQueries[1])
	}
	rawConversations, _ := rawData["conversations"].([]any)
	rawConversation, _ := rawConversations[0].(map[string]any)
	if _, ok := rawConversation["_raw"].(map[string]any); !ok {
		t.Fatalf("expected include_raw=true to expose _raw, got %+v", rawConversation)
	}
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

func TestM5_NotificationsReadOmitsRawByDefaultAndExposesDiagnostics(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
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
	diagnostics, _ := defaultData["diagnostics"].(map[string]any)
	if diagnostics["upstreamCount"] != float64(1) || diagnostics["normalizedCount"] != float64(1) {
		t.Fatalf("expected notification diagnostics counts, got %+v", diagnostics)
	}
	if diagnostics["responseBytes"] == float64(0) || diagnostics["mcpPayloadBytes"] == float64(0) {
		t.Fatalf("expected response size diagnostics, got %+v", diagnostics)
	}

	rawData := call(3, map[string]any{"limit": 20, "include_raw": true})
	rawNotifications, _ := rawData["notifications"].([]any)
	rawNotification, _ := rawNotifications[0].(map[string]any)
	if _, ok := rawNotification["_raw"].(map[string]any); !ok {
		t.Fatalf("expected include_raw=true to expose _raw, got %+v", rawNotification)
	}
	if _, ok := rawNotification["raw"]; ok {
		t.Fatalf("include_raw=true should use _raw, not raw: %+v", rawNotification)
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
	if len(gotQueries) != 1 {
		t.Fatalf("invalid type should not fan out upstream, got %v", gotQueries)
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
