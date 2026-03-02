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

	have := map[string]bool{}
	for _, tool := range result.Tools {
		have[tool.Name] = true
	}

	for _, name := range []string{
		"profile_read",
		"timeline_read",
		"post_search",
		"followers_list",
		"following_list",
		"notifications_read",
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
			name:        "notifications_read",
			tool:        "notifications_read",
			scope:       "read",
			args:        map[string]any{"limit": 2, "types": []string{"mention"}},
			invalidArgs: map[string]any{"limit": "nope"},
			failureCode: mcpruntime.CodeServerError,
			wantRequests: []recorded{
				{Method: "GET", Path: "/api/v1/notifications"},
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
				case "/api/v1/timelines/home":
					_, _ = w.Write([]byte(`[{"id":"t1"}]`))
				case "/api/v1/timelines/public":
					_, _ = w.Write([]byte(`[{"id":"t2"}]`))
				case "/api/v2/search":
					_, _ = w.Write([]byte(`{"statuses":[{"id":"s1"}],"accounts":[],"hashtags":[]}`))
				case "/api/v1/notifications":
					_, _ = w.Write([]byte(`[{"id":"n1"}]`))
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
