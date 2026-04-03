package mcpapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	"github.com/theory-cloud/apptheory/testkit"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestLBM1_IdentityToolsAndChannelResources(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulbinding.ResetForTests()
	soulapi.ResetForTests()
	defer soulbinding.ResetForTests()

	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const tokenUser = "Agent1"

	var gotBindingPK string
	var gotBindingSK string
	var searchQueries []string
	var searchDomains []string
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				gotBindingPK, _ = where["PK"].(string)
				gotBindingSK, _ = where["SK"].(string)
				return setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": tokenUser,
				})
			},
		}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/soul/search":
			q := r.URL.Query().Get("q")
			domain := r.URL.Query().Get("domain")
			searchQueries = append(searchQueries, q)
			searchDomains = append(searchDomains, domain)
			if q == "agent-alice" && domain == "test.example.com" {
				_, _ = w.Write([]byte(`{"version":"1","results":[{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"}],"count":1,"has_more":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"version":"1","results":[],"count":0,"has_more":false}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{
				"version":"3",
				"channels":{
					"ens":{"name":"agent-alice.lessersoul.eth"},
					"email":{"address":"agent-alice@lessersoul.ai","capabilities":["receive","send"],"verified":true},
					"phone":{"number":"+15550142","capabilities":["sms-receive"],"verified":false}
				},
				"contactPreferences":{
					"preferred":"email",
					"availability":{"schedule":"always"},
					"responseExpectation":{"target":"1h","guarantee":"best-effort"},
					"languages":["en"]
				}
			}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/channels":
			_, _ = w.Write([]byte(`{
				"agentId":"` + agentID + `",
				"channels":{
					"ens":{"name":"agent-alice.lessersoul.eth"},
					"email":{"address":"agent-alice@lessersoul.ai","capabilities":["receive","send"],"verified":true},
					"phone":{"number":"+15550142","capabilities":["sms-receive"],"verified":false}
				},
				"contactPreferences":{"preferred":"email"},
				"updatedAt":"2026-03-09T12:00:00Z"
			}`))
		case r.URL.Path == "/api/v1/soul/resolve/email/agent-alice@lessersoul.ai":
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/resolve/ens/agent-alice.lessersoul.eth":
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/notifications":
			_, _ = w.Write([]byte(`[
				{
					"id":"n1",
					"type":"communication:inbound",
					"channel":"email",
					"messageId":"comm-msg-001",
					"from":{"address":"agent-alice@lessersoul.ai","soulAgentId":"` + agentID + `"},
					"subject":"Hi",
					"body":"Hello there",
					"receivedAt":"2026-03-09T12:00:00Z"
				}
			]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", tokenUser, []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	// identity_whoami should resolve the authenticated local agent via the soul binding and return channels + preferences.
	{
		gotBindingPK = ""
		gotBindingSK = ""
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_whoami",
			"arguments": map[string]any{},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_whoami: status=%d body=%s", resp.Status, string(resp.Body))
		}
		if gotBindingPK != "SOUL_BODY_BINDING_USERNAME#agent1" {
			t.Fatalf("expected binding lookup PK, got %q", gotBindingPK)
		}
		if gotBindingSK != "SOUL_BODY_BINDING" {
			t.Fatalf("expected binding lookup SK, got %q", gotBindingSK)
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_whoami rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["agentId"] != agentID {
			t.Fatalf("agentId: want %s got %v", agentID, data["agentId"])
		}
		ch, _ := data["channels"].(map[string]any)
		if ch == nil {
			t.Fatalf("expected channels object")
		}
		email, _ := ch["email"].(map[string]any)
		if email["address"] != "agent-alice@lessersoul.ai" {
			t.Fatalf("unexpected email channel: %+v", email)
		}
		prefs, _ := data["contactPreferences"].(map[string]any)
		if prefs["preferred"] != "email" {
			t.Fatalf("unexpected contactPreferences: %+v", prefs)
		}
	}

	// identity_lookup should resolve managed email/ENS directly without stripping them to bare localIds.
	for _, q := range []string{"agent-alice@lessersoul.ai", "agent-alice.lessersoul.eth"} {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": q},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(%q): status=%d body=%s", q, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_lookup(%q) rpc error: %+v", q, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		matches, _ := data["matches"].([]any)
		if len(matches) != 1 {
			t.Fatalf("identity_lookup(%q): expected 1 match, got %+v", q, matches)
		}
		match, _ := matches[0].(map[string]any)
		if match["agentId"] != agentID {
			t.Fatalf("identity_lookup(%q): agentId want %s got %v", q, agentID, match["agentId"])
		}
	}
	if len(searchQueries) != 0 {
		t.Fatalf("identity_lookup for managed identifiers should resolve directly, got search queries %+v", searchQueries)
	}
	if len(searchDomains) != 0 {
		t.Fatalf("identity_lookup for managed identifiers should not set search domains, got %+v", searchDomains)
	}

	searchQueries = nil
	searchDomains = nil

	// identity_lookup should normalize current-instance local IDs and carry the authenticated instance domain explicitly.
	for _, q := range []string{"agent-alice", "@agent-alice"} {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": q},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 31, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(%q): status=%d body=%s", q, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_lookup(%q) rpc error: %+v", q, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		matches, _ := data["matches"].([]any)
		if len(matches) != 1 {
			t.Fatalf("identity_lookup(%q): expected 1 match, got %+v", q, matches)
		}
		match, _ := matches[0].(map[string]any)
		if match["agentId"] != agentID {
			t.Fatalf("identity_lookup(%q): agentId want %s got %v", q, agentID, match["agentId"])
		}
		if match["localId"] != "agent-alice" {
			t.Fatalf("identity_lookup(%q): localId want agent-alice got %v", q, match["localId"])
		}
	}
	if !reflect.DeepEqual(searchQueries, []string{"agent-alice", "agent-alice"}) {
		t.Fatalf("identity_lookup should normalize local ID queries before search, got %+v", searchQueries)
	}
	if !reflect.DeepEqual(searchDomains, []string{"test.example.com", "test.example.com"}) {
		t.Fatalf("identity_lookup should provide current-instance domain context for local queries, got %+v", searchDomains)
	}

	// agent://channels + agent://channels/preferences should read successfully.
	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://channels"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 4, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read channels: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}

	{
		params, _ := json.Marshal(map[string]any{"uri": "agent://channels/preferences"})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 5, Method: "resources/read", Params: params})
		if resp.Status != 200 {
			t.Fatalf("resources/read channel preferences: status=%d body=%s", resp.Status, string(resp.Body))
		}
	}

	// identity_verify should resolve the identity and verify a concrete inbound message.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "identity_verify",
			"arguments": map[string]any{
				"channel":    "email",
				"identifier": "agent-alice@lessersoul.ai",
				"messageId":  "comm-msg-001",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 6, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_verify email: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_verify email rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["verified"] != true {
			t.Fatalf("expected verified identity match, got %+v", data)
		}
		if data["matchedBy"] != "sender.agentId" && data["matchedBy"] != "sender.email" {
			t.Fatalf("expected sender provenance match, got %+v", data)
		}
	}

	// identity_verify should also work when resolving the sender by ENS and using the same message provenance.
	{
		callParams, _ := json.Marshal(map[string]any{
			"name": "identity_verify",
			"arguments": map[string]any{
				"channel":    "ens",
				"identifier": "agent-alice.lessersoul.eth",
				"messageId":  "comm-msg-001",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 7, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_verify ens: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_verify ens rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["verified"] != true {
			t.Fatalf("expected ENS verification to succeed, got %+v", data)
		}
	}
}

func TestLBM1_IdentityLookupBareLocalFailsWithoutTrustworthyCurrentInstanceDomain(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulbinding.ResetForTests()
	soulapi.ResetForTests()
	defer soulbinding.ResetForTests()

	const agentID = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": "Agent1",
				})
			},
		}, nil
	})

	searchCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/search":
			searchCalled = true
			_, _ = w.Write([]byte(`{"version":"1","results":[],"count":0,"has_more":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name":      "identity_lookup",
		"arguments": map[string]any{"query": "agent-alice"},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("identity_lookup(agent-alice): status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("identity_lookup(agent-alice) rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
	}
	if !out.IsError {
		t.Fatalf("expected isError tool result, got %+v", out)
	}
	errPayload, _ := out.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", errPayload)
	}
	if !strings.Contains(strings.ToLower(errPayload["message"].(string)), "trustworthy instance domain") {
		t.Fatalf("expected error message to mention trustworthy instance domain, got %+v", errPayload)
	}
	if searchCalled {
		t.Fatalf("identity_lookup should fail before search when current-instance domain context is unavailable")
	}
}

func TestLBM1_IdentityLookupMalformedLocalIdentifierReturnsBadRequest(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulbinding.ResetForTests()
	soulapi.ResetForTests()
	defer soulbinding.ResetForTests()

	const agentID = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": "Agent1",
				})
			},
		}, nil
	})

	var gotSearchQuery string
	var gotSearchDomain string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/soul/search":
			gotSearchQuery = r.URL.Query().Get("q")
			gotSearchDomain = r.URL.Query().Get("domain")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"local_id must not contain /, :, or @"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name":      "identity_lookup",
		"arguments": map[string]any{"query": "bad/local"},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("identity_lookup(bad/local): status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("identity_lookup(bad/local) rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
	}
	if !out.IsError {
		t.Fatalf("expected isError tool result, got %+v", out)
	}
	errPayload, _ := out.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", errPayload)
	}
	if !strings.Contains(strings.ToLower(errPayload["message"].(string)), "local_id") {
		t.Fatalf("expected error message to mention local_id, got %+v", errPayload)
	}
	if gotSearchQuery != "bad/local" || gotSearchDomain != "" {
		t.Fatalf("unexpected downstream malformed search shape: q=%q domain=%q", gotSearchQuery, gotSearchDomain)
	}
}
