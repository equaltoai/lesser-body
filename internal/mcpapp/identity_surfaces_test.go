package mcpapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
	"github.com/theory-cloud/apptheory/v3/testkit"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"

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
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
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
	var emailResolveQueries []string
	var channelResolveQueries []string
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
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case r.URL.Path == "/api/v1/soul/search":
			q := r.URL.Query().Get("q")
			domain := r.URL.Query().Get("domain")
			searchQueries = append(searchQueries, q)
			searchDomains = append(searchDomains, domain)
			if domain == "test.example.com" && (q == "agent-alice" || q == "ops.v2") {
				_, _ = w.Write([]byte(`{"version":"1","results":[{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"}],"count":1,"has_more":false}`))
				return
			}
			if domain == "" && q == "remote.example/steward" {
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
					"email":{"address":"agent-alice.simulacrum@lessersoul.ai","capabilities":["receive","send"],"verified":true},
					"phone":{"number":"+15550142","capabilities":["sms-receive"],"verified":false}
				},
				"contactPreferences":{
					"preferred":"email",
					"availability":{"schedule":"always"},
					"responseExpectation":{"target":"1h","guarantee":"best-effort"},
					"languages":["en"]
				},
				` + boundBodyPolicyJSON("identity.self.read", "communication.channels.read", "communication.email.read", "communication.sms.read") + `
			}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/channels":
			channelResolveQueries = append(channelResolveQueries, agentID)
			_, _ = w.Write([]byte(`{
				"agentId":"` + agentID + `",
				"channels":{
					"ens":{"name":"agent-alice.lessersoul.eth"},
					"email":{"address":"agent-alice.simulacrum@lessersoul.ai","capabilities":["receive","send"],"verified":true},
					"phone":{"number":"+15550142","capabilities":["sms-receive"],"verified":false}
				},
				"contactPreferences":{"preferred":"email"},
				"updatedAt":"2026-03-09T12:00:00Z"
			}`))
		case r.URL.Path == "/api/v1/soul/resolve/email/agent-alice.simulacrum@lessersoul.ai":
			emailResolveQueries = append(emailResolveQueries, "agent-alice.simulacrum@lessersoul.ai")
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/resolve/email/steward@remote.example":
			emailResolveQueries = append(emailResolveQueries, "steward@remote.example")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		case r.URL.Path == "/api/v1/soul/resolve/ens/agent-alice.lessersoul.eth":
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/resolve/ens/ops.eth":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email":
			if got := r.Header.Get("Authorization"); got != "Bearer instance-key-123" {
				t.Fatalf("expected host instance bearer for mailbox lookup, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-email",
					"deliveryId":"comm-delivery-email",
					"channelType":"email",
					"direction":"inbound",
					"from":{"address":"agent-alice.simulacrum@lessersoul.ai","soulAgentId":"` + agentID + `"},
					"to":[{"address":"agent1@test.example.com"}],
					"subject":"Host email",
					"preview":"Hello from host",
					"createdAt":"2026-05-10T12:00:00Z"
				}
			}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-phone":
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-phone",
					"deliveryId":"comm-delivery-phone",
					"channelType":"sms",
					"direction":"inbound",
					"from":{"number":"+15550142","soulAgentId":"` + agentID + `"},
					"to":{"number":"+15550199"},
					"preview":"SMS from host",
					"createdAt":"2026-05-10T12:01:00Z"
				}
			}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-ens":
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-ens",
					"deliveryId":"comm-delivery-ens",
					"channelType":"email",
					"direction":"inbound",
					"from":{"address":"agent-alice.simulacrum@lessersoul.ai","soulAgentId":"` + agentID + `"},
					"subject":"ENS host email",
					"preview":"ENS verified",
					"createdAt":"2026-05-10T12:02:00Z"
				}
			}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-spoof":
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-spoof",
					"deliveryId":"comm-delivery-spoof",
					"channelType":"email",
					"direction":"inbound",
					"from":{"name":"agent-alice.simulacrum@lessersoul.ai","address":"agent-alice.simulacrum@lessersoul.ai"},
					"subject":"Spoof",
					"preview":"No authoritative sender provenance",
					"createdAt":"2026-05-10T12:03:00Z"
				}
			}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-wrong-agent":
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-wrong-agent",
					"deliveryId":"comm-delivery-wrong-agent",
					"channelType":"email",
					"direction":"inbound",
					"from":{"address":"agent-alice.simulacrum@lessersoul.ai","soulAgentId":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
					"subject":"Wrong agent",
					"preview":"Wrong provenance",
					"createdAt":"2026-05-10T12:04:00Z"
				}
			}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/soul/comm/mailbox/"+agentID+"/messages/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"message not found"}}`))
		case r.URL.Path == "/api/v1/notifications":
			_, _ = w.Write([]byte(`[
				{
					"id":"n1",
					"type":"communication:inbound",
					"channel":"email",
					"messageId":"comm-msg-001",
					"from":{"address":"agent-alice.simulacrum@lessersoul.ai","soulAgentId":"` + agentID + `"},
					"subject":"Hi",
					"body":"Hello there",
					"receivedAt":"2026-03-09T12:00:00Z"
				},
				{
					"id":"n-spoof",
					"type":"communication:inbound",
					"channel":"email",
					"messageId":"comm-msg-spoof",
					"from":{"name":"agent-alice.simulacrum@lessersoul.ai","address":"attacker@example.net"},
					"subject":"Spoof",
					"body":"Pretending",
					"receivedAt":"2026-03-09T12:01:00Z"
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
	assertPublicLookupMatch := func(label string, match map[string]any) {
		t.Helper()
		if _, ok := match["channels"]; ok {
			t.Fatalf("%s: identity_lookup must not expose channels in public matches: %+v", label, match)
		}
		if _, ok := match["contactPreferences"]; ok {
			t.Fatalf("%s: identity_lookup must not expose contactPreferences in public matches: %+v", label, match)
		}
	}
	// CSR-007: identity_lookup no longer exposes managed soul email
	// addresses. Per-channel contact details remain behind the self-service
	// whoami / agent://channels resource with bound-operation policy enforcement.
	assertNoPublicEmail := func(label string, match map[string]any) {
		t.Helper()
		if email, ok := match["email"]; ok {
			t.Fatalf("%s: CSR-007: identity_lookup must not expose email in public matches, got %+v", label, email)
		}
	}
	assertToolErrorPayload := func(label string, body []byte, wantCode string) map[string]any {
		t.Helper()
		var rpc mcpruntime.Response
		_ = json.Unmarshal(body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("%s rpc error: %+v", label, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if !out.IsError {
			t.Fatalf("%s expected isError tool result, got %+v", label, out)
		}
		errPayload, _ := out.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != wantCode {
			t.Fatalf("%s expected error code %q, got %+v", label, wantCode, errPayload)
		}
		return errPayload
	}

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
		if email["address"] != "agent-alice.simulacrum@lessersoul.ai" {
			t.Fatalf("unexpected email channel: %+v", email)
		}
		prefs, _ := data["contactPreferences"].(map[string]any)
		if prefs["preferred"] != "email" {
			t.Fatalf("unexpected contactPreferences: %+v", prefs)
		}
	}

	// identity_lookup should resolve public ENS directly without stripping it to a bare localId.
	for _, q := range []string{"agent-alice.lessersoul.eth"} {
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
		assertPublicLookupMatch(q, match)
		assertNoPublicEmail(q, match)
	}
	if len(searchQueries) != 0 {
		t.Fatalf("identity_lookup for ENS should resolve directly, got search queries %+v", searchQueries)
	}
	if len(searchDomains) != 0 {
		t.Fatalf("identity_lookup for ENS should not set search domains, got %+v", searchDomains)
	}
	if len(emailResolveQueries) != 0 {
		t.Fatalf("identity_lookup for ENS should not call managed email resolution, got %+v", emailResolveQueries)
	}

	searchQueries = nil
	searchDomains = nil
	emailResolveQueries = nil

	// identity_lookup should fail closed for private reachability identifiers until
	// lesser-host exposes a body-facing instance-authenticated resolver.
	for _, q := range []string{"agent-alice.simulacrum@lessersoul.ai", "+15550142"} {
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": q},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 30, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(%q): status=%d body=%s", q, resp.Status, string(resp.Body))
		}
		errPayload := assertToolErrorPayload("identity_lookup("+q+")", resp.Body, "private_reachability_unavailable")
		if errPayload["status"] != float64(501) {
			t.Fatalf("identity_lookup(%q): expected status 501, got %+v", q, errPayload)
		}
	}
	if len(searchQueries) != 0 || len(searchDomains) != 0 || len(emailResolveQueries) != 0 {
		t.Fatalf("identity_lookup private reachability should fail before host/search calls: search=%+v domains=%+v email=%+v", searchQueries, searchDomains, emailResolveQueries)
	}

	searchQueries = nil
	searchDomains = nil
	emailResolveQueries = nil

	// identity_lookup should normalize current-instance local IDs and carry the authenticated instance domain explicitly.
	for _, q := range []string{"agent-alice", "@agent-alice", "ops.v2"} {
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
		assertPublicLookupMatch(q, match)
		assertNoPublicEmail(q, match)
	}
	if !reflect.DeepEqual(searchQueries, []string{"agent-alice", "agent-alice", "ops.v2"}) {
		t.Fatalf("identity_lookup should normalize local ID queries before search, got %+v", searchQueries)
	}
	if !reflect.DeepEqual(searchDomains, []string{"test.example.com", "test.example.com", "test.example.com"}) {
		t.Fatalf("identity_lookup should provide current-instance domain context for local queries, got %+v", searchDomains)
	}
	if len(emailResolveQueries) != 0 {
		t.Fatalf("identity_lookup should not attempt managed-email resolution for current-instance local IDs, got %+v", emailResolveQueries)
	}

	// identity_lookup should normalize exact remote ActivityPub identifiers into the exact qualified soul search contract.
	for _, tc := range []struct {
		name             string
		query            string
		wantSearchQ      string
		wantSearchDomain string
		wantEmailResolve []string
	}{
		{
			name:             "acct_form",
			query:            "@steward@remote.example",
			wantSearchQ:      "remote.example/steward",
			wantSearchDomain: "",
		},
		{
			name:             "canonical_actor_url",
			query:            "https://remote.example/users/steward",
			wantSearchQ:      "remote.example/steward",
			wantSearchDomain: "",
		},
	} {
		searchQueries = nil
		searchDomains = nil
		emailResolveQueries = nil

		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": tc.query},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 32, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("%s identity_lookup(%q): status=%d body=%s", tc.name, tc.query, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("%s identity_lookup(%q) rpc error: %+v", tc.name, tc.query, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		matches, _ := data["matches"].([]any)
		if len(matches) != 1 {
			t.Fatalf("%s identity_lookup(%q): expected 1 match, got %+v", tc.name, tc.query, matches)
		}
		match, _ := matches[0].(map[string]any)
		if match["agentId"] != agentID {
			t.Fatalf("%s identity_lookup(%q): agentId want %s got %v", tc.name, tc.query, agentID, match["agentId"])
		}
		assertPublicLookupMatch(tc.name, match)
		assertNoPublicEmail(tc.name, match)
		if !reflect.DeepEqual(searchQueries, []string{tc.wantSearchQ}) {
			t.Fatalf("%s identity_lookup(%q): search q want [%s] got %+v", tc.name, tc.query, tc.wantSearchQ, searchQueries)
		}
		if !reflect.DeepEqual(searchDomains, []string{tc.wantSearchDomain}) {
			t.Fatalf("%s identity_lookup(%q): search domain want [%s] got %+v", tc.name, tc.query, tc.wantSearchDomain, searchDomains)
		}
		if !reflect.DeepEqual(emailResolveQueries, tc.wantEmailResolve) {
			t.Fatalf("%s identity_lookup(%q): email resolves want %+v got %+v", tc.name, tc.query, tc.wantEmailResolve, emailResolveQueries)
		}
	}

	// Bare user@domain input is ambiguous with private managed email reachability.
	// It now fails closed instead of probing host email resolution anonymously; callers
	// should use @user@domain for public ActivityPub handles.
	{
		searchQueries = nil
		searchDomains = nil
		emailResolveQueries = nil
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": "steward@remote.example"},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 321, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(bare user@domain): status=%d body=%s", resp.Status, string(resp.Body))
		}
		_ = assertToolErrorPayload("identity_lookup(bare user@domain)", resp.Body, "private_reachability_unavailable")
		if len(searchQueries) != 0 || len(searchDomains) != 0 || len(emailResolveQueries) != 0 {
			t.Fatalf("identity_lookup bare user@domain should fail before host/search calls: search=%+v domains=%+v email=%+v", searchQueries, searchDomains, emailResolveQueries)
		}
	}

	// ENS-like names that miss the authoritative ENS resolver must not fall back
	// to current-instance local-id search.
	{
		searchQueries = nil
		searchDomains = nil
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": "ops.eth"},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 33, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(ops.eth): status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_lookup(ops.eth) rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if !out.IsError {
			t.Fatalf("expected not_found tool result for unresolved ENS name, got %+v", out)
		}
		errPayload, _ := out.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != "not_found" {
			t.Fatalf("expected not_found for unresolved ENS name, got %+v", errPayload)
		}
		if len(searchQueries) != 0 || len(searchDomains) != 0 {
			t.Fatalf("unresolved ENS name should not use local search, got q=%+v domains=%+v", searchQueries, searchDomains)
		}
	}

	// identity_lookup should reject internal-looking remote handle domains instead
	// of forwarding them as generic soul search queries.
	{
		searchQueries = nil
		searchDomains = nil
		emailResolveQueries = nil
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_lookup",
			"arguments": map[string]any{"query": "steward@localhost"},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 34, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_lookup(steward@localhost): status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_lookup(steward@localhost) rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		if !out.IsError {
			t.Fatalf("expected invalid_request tool result for internal remote handle, got %+v", out)
		}
		errPayload, _ := out.StructuredContent["error"].(map[string]any)
		if errPayload["code"] != "invalid_request" {
			t.Fatalf("expected invalid_request for internal remote handle, got %+v", errPayload)
		}
		if len(searchQueries) != 0 || len(searchDomains) != 0 {
			t.Fatalf("internal remote handle should not use soul search, got q=%+v domains=%+v", searchQueries, searchDomains)
		}
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

	// identity_verify should fail closed for private email/phone reachability until
	// lesser-host exposes a body-facing instance-authenticated resolver.
	for _, tc := range []struct {
		channel    string
		identifier string
	}{
		{channel: "email", identifier: "agent-alice.simulacrum@lessersoul.ai"},
		{channel: "phone", identifier: "+15550142"},
	} {
		channelResolveQueries = nil
		callParams, _ := json.Marshal(map[string]any{
			"name": "identity_verify",
			"arguments": map[string]any{
				"channel":    tc.channel,
				"identifier": tc.identifier,
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 6, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_verify %s: status=%d body=%s", tc.channel, resp.Status, string(resp.Body))
		}
		_ = assertToolErrorPayload("identity_verify "+tc.channel, resp.Body, "private_reachability_unavailable")
		if len(channelResolveQueries) != 0 {
			t.Fatalf("identity_verify %s should not fetch private /channels, got %+v", tc.channel, channelResolveQueries)
		}
	}

	callIdentityVerify := func(id int, channel string, identifier string, messageID string) map[string]any {
		t.Helper()
		args := map[string]any{
			"channel":    channel,
			"identifier": identifier,
		}
		if messageID != "" {
			args["messageId"] = messageID
		}
		callParams, _ := json.Marshal(map[string]any{
			"name":      "identity_verify",
			"arguments": args,
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_verify(%s,%s,%s): status=%d body=%s", channel, identifier, messageID, resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_verify(%s,%s,%s) rpc error: %+v", channel, identifier, messageID, rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data == nil {
			t.Fatalf("identity_verify(%s,%s,%s): expected structured data, got %+v", channel, identifier, messageID, out.StructuredContent)
		}
		return data
	}

	// Host mailbox messageRefs are the canonical provenance source for message-scoped identity_verify.
	{
		emailResolveQueries = nil
		data := callIdentityVerify(60, "email", "agent-alice.simulacrum@lessersoul.ai", "comm-delivery-email")
		if data["verified"] != false || data["reason"] != "sender_identifier_not_authoritatively_bound" {
			t.Fatalf("expected host email message verification to fail closed without authoritative identifier binding, got %+v", data)
		}
		if data["identityResolved"] != false {
			t.Fatalf("email message-scoped verify must not treat sender agent provenance as identifier resolution, got %+v", data)
		}
		message, _ := data["message"].(map[string]any)
		if message["source"] != "lesser-host-mailbox" || message["messageRef"] != "comm-delivery-email" {
			t.Fatalf("expected host mailbox message summary, got %+v", message)
		}
		if len(emailResolveQueries) != 0 {
			t.Fatalf("message-scoped email verify must not call private reachability resolver, got %+v", emailResolveQueries)
		}

		data = callIdentityVerify(61, "phone", "+1 (555) 0142", "comm-delivery-phone")
		if data["verified"] != false || data["reason"] != "sender_identifier_not_authoritatively_bound" {
			t.Fatalf("expected host phone message verification to fail closed without authoritative identifier binding, got %+v", data)
		}

		data = callIdentityVerify(62, "ens", "agent-alice.lessersoul.eth", "comm-delivery-ens")
		if data["verified"] != true || data["matchedBy"] != "sender.agentId" {
			t.Fatalf("expected host ENS message verification to succeed, got %+v", data)
		}

		data = callIdentityVerify(63, "email", "agent-alice.simulacrum@lessersoul.ai", "comm-delivery-spoof")
		if data["verified"] != false || data["reason"] != "message_lacks_authoritative_sender_provenance" {
			t.Fatalf("expected spoofed host sender to fail missing provenance, got %+v", data)
		}

		data = callIdentityVerify(64, "ens", "agent-alice.lessersoul.eth", "comm-delivery-wrong-agent")
		if data["verified"] != false || data["reason"] != "sender_does_not_match_resolved_identity" {
			t.Fatalf("expected wrong host sender agent to fail mismatch, got %+v", data)
		}

		data = callIdentityVerify(65, "ens", "agent-alice.lessersoul.eth", "comm-delivery-missing")
		if data["verified"] != false || data["messageFound"] != false || data["reason"] != "message_not_found" {
			t.Fatalf("expected missing host message to be explicit, got %+v", data)
		}
	}

	// identity_verify must not trust spoofable sender display/name/address fields
	// without authoritative soul agent provenance on the message.
	{
		channelResolveQueries = nil
		callParams, _ := json.Marshal(map[string]any{
			"name": "identity_verify",
			"arguments": map[string]any{
				"channel":    "ens",
				"identifier": "agent-alice.lessersoul.eth",
				"messageId":  "comm-msg-spoof",
			},
		})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: 61, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("identity_verify spoofed sender: status=%d body=%s", resp.Status, string(resp.Body))
		}

		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("identity_verify spoofed sender rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		{
			b, _ := json.Marshal(rpc.Result)
			_ = json.Unmarshal(b, &out)
		}
		data, _ := out.StructuredContent["data"].(map[string]any)
		if data["verified"] != false {
			t.Fatalf("expected spoofed sender to remain unverified, got %+v", data)
		}
		if data["reason"] != "message_lacks_authoritative_sender_provenance" {
			t.Fatalf("expected missing provenance reason, got %+v", data)
		}
		if len(channelResolveQueries) != 0 {
			t.Fatalf("identity_verify spoofed sender should not fetch private /channels, got %+v", channelResolveQueries)
		}
	}

	// identity_verify should also work when resolving the sender by ENS and using the same message provenance.
	{
		channelResolveQueries = nil
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
		if data["matchedBy"] != "sender.agentId" {
			t.Fatalf("expected sender provenance match, got %+v", data)
		}
		if len(channelResolveQueries) != 0 {
			t.Fatalf("identity_verify ENS should not fetch private /channels, got %+v", channelResolveQueries)
		}
	}
}

func TestIdentityVerifyHostMessageRequiresMailboxReadPolicy(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var hostMailboxCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"email":{"address":"agent-alice.simulacrum@lessersoul.ai"}},"contactPreferences":{},` + boundBodyPolicyJSON("identity.self.read", "communication.channels.read") + `}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-email":
			hostMailboxCalled = true
			_, _ = w.Write([]byte(`{"message":{"messageRef":"comm-delivery-email","channelType":"email"}}`))
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

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "identity_verify",
		"arguments": map[string]any{
			"channel":    "email",
			"identifier": "agent-alice.simulacrum@lessersoul.ai",
			"messageId":  "comm-delivery-email",
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("identity_verify: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if hostMailboxCalled {
		t.Fatalf("identity_verify must not call Host mailbox when only channels.read policy is allowed")
	}

	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("identity_verify rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	if !out.IsError {
		t.Fatalf("expected operation_not_allowed tool error, got %+v", out)
	}
	errPayload, _ := out.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "operation_not_allowed" {
		t.Fatalf("expected operation_not_allowed, got %+v", errPayload)
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["operation"] != "communication.email.read" {
		t.Fatalf("expected mailbox read operation denial, got %+v", details)
	}
}

func TestIdentityVerifyHostMessageRejectsCrossChannelSummary(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "instance-key-123")
	installSoulBindingLookup(t, "agent1", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)

	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var hostMailboxCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		case r.URL.Path == "/api/v1/soul/agents/"+agentID+"/registration":
			_, _ = w.Write([]byte(`{"version":"3","channels":{"email":{"address":"agent-alice.simulacrum@lessersoul.ai"},"phone":{"number":"+15550142"}},"contactPreferences":{},` + boundBodyPolicyJSON("communication.email.read") + `}`))
		case r.URL.Path == "/api/v1/soul/comm/mailbox/"+agentID+"/messages/comm-delivery-sms":
			hostMailboxCalled = true
			if got := r.Header.Get("Authorization"); got != "Bearer instance-key-123" {
				t.Fatalf("expected host instance bearer for mailbox lookup, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"message":{
					"messageRef":"comm-delivery-sms",
					"channelType":"sms",
					"direction":"inbound",
					"from":{"number":"+15550142","soulAgentId":"` + agentID + `"},
					"to":{"number":"+15550199"},
					"preview":"private sms preview",
					"createdAt":"2026-05-10T12:01:00Z"
				}
			}`))
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

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	authHeader := "Bearer " + newTestToken(t, "test", "agent1", []string{"read"})
	initResp := invokeJSON(t, env, app, map[string][]string{
		"authorization": {authHeader},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "identity_verify",
		"arguments": map[string]any{
			"channel":    "email",
			"identifier": "agent-alice.simulacrum@lessersoul.ai",
			"messageId":  "comm-delivery-sms",
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("identity_verify: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if !hostMailboxCalled {
		t.Fatalf("expected initial email-read policy to allow Host fetch before actual channel re-check")
	}

	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("identity_verify rpc error: %+v", rpc.Error)
	}
	var out mcpruntime.ToolResult
	b, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(b, &out)
	if !out.IsError {
		t.Fatalf("expected cross-channel operation_not_allowed tool error, got %+v", out)
	}
	if _, ok := out.StructuredContent["data"]; ok {
		t.Fatalf("cross-channel denial must not return message-bearing data: %+v", out.StructuredContent)
	}
	errPayload, _ := out.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "operation_not_allowed" {
		t.Fatalf("expected operation_not_allowed, got %+v", errPayload)
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["operation"] != "communication.sms.read" {
		t.Fatalf("expected actual sms mailbox read denial, got %+v", details)
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
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "", "agent-alice")))
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
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
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

func TestLBM1_IdentityLookupUnsupportedRemoteActorURLReturnsBadRequest(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulbinding.ResetForTests()
	soulapi.ResetForTests()
	defer soulbinding.ResetForTests()

	const agentID = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

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
		case r.URL.Path == "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case r.URL.Path == "/api/v1/soul/search":
			searchCalled = true
			_, _ = w.Write([]byte(`{"version":"1","results":[],"count":0,"has_more":false}`))
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
		"arguments": map[string]any{"query": "https://remote.example/actors/steward"},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("identity_lookup(unsupported remote actor URL): status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("identity_lookup(unsupported remote actor URL) rpc error: %+v", rpc.Error)
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
	msg, _ := errPayload["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "unsupported") || !strings.Contains(strings.ToLower(msg), "url") {
		t.Fatalf("expected actionable unsupported URL message, got %+v", errPayload)
	}
	if searchCalled {
		t.Fatalf("identity_lookup should reject unsupported remote actor URLs before search")
	}
}

func TestLBM1_SoulReadPublicMVPUsesPublicEndpoints(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	const agentID = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	var publicAuthHeaders []string
	privatePathCalled := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/resolve/email/") || strings.Contains(r.URL.Path, "/resolve/phone/") || strings.Contains(r.URL.Path, "/channels") || strings.Contains(r.URL.Path, "/comm/") {
			privatePathCalled = r.URL.Path
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"private endpoint called"}}`))
			return
		}
		publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{
				"agent":{
					"agent_id":"` + agentID + `",
					"domain":"test.example.com",
					"local_id":"agent-c",
					"ens_name":"agent-c.lessersoul.eth",
					"wallet":"0x1234",
					"token_id":"42",
					"meta_uri":"ipfs://meta",
					"status":"active",
					"lifecycle_status":"active",
					"self_description_version":"3",
					"updated_at":"2026-05-11T15:00:00Z",
					"avatar":{
						"token_uri":"ipfs://avatar",
						"image":"https://cdn.example/avatar.png",
						"current_style_id":"friendly",
						"current_style_name":"Friendly",
						"styles":[{"style_id":"friendly","style_name":"Friendly","selected":true}]
					}
				}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{
				"version":"3",
				"selfDescription":{"summary":"public self description"},
				"transparency":{"ai_generated":true},
				"channels":{"ens":{"name":"agent-c.lessersoul.eth"}}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities":
			_, _ = w.Write([]byte(`{"capabilities":[{"capability":"social.post","scope":"public","claim_level":"declared","degrades_to":"read-only"}]}`))
		case "/api/v1/soul/agents/" + agentID + "/boundaries":
			_, _ = w.Write([]byte(`{"boundaries":[{"boundary_id":"b1","category":"communication_policy","statement":"No private channels in public read","added_at":"2026-05-11T15:00:00Z","added_in_version":"3"}]}`))
		case "/api/v1/soul/agents/" + agentID + "/transparency":
			_, _ = w.Write([]byte(`{"transparency":{"ai_generated":true,"operator_disclosed":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

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

	listResp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if listResp.Status != 200 {
		t.Fatalf("tools/list: status=%d body=%s", listResp.Status, string(listResp.Body))
	}
	var listRPC mcpruntime.Response
	_ = json.Unmarshal(listResp.Body, &listRPC)
	if listRPC.Error != nil {
		t.Fatalf("tools/list error: %+v", listRPC.Error)
	}
	var listOut struct {
		Tools []mcpruntime.ToolDef `json:"tools"`
	}
	{
		b, _ := json.Marshal(listRPC.Result)
		_ = json.Unmarshal(b, &listOut)
	}
	haveSoulRead := false
	for _, tool := range listOut.Tools {
		if tool.Name == "soul_read" {
			haveSoulRead = true
			break
		}
	}
	if !haveSoulRead {
		t.Fatalf("expected soul_read in drone tools/list, got %+v", listOut.Tools)
	}

	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"agentId": agentID,
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if privatePathCalled != "" {
		t.Fatalf("soul_read called private endpoint %q", privatePathCalled)
	}
	for _, got := range publicAuthHeaders {
		if strings.TrimSpace(got) != "" {
			t.Fatalf("public soul_read endpoint should be unauthenticated, got Authorization=%q", got)
		}
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal soul_read response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("soul_read rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("expected count=1, got %+v", data)
	}
	access, _ := data["access"].(map[string]any)
	if access["mode"] != "public" || access["callerRelation"] != "public" || access["publicOnly"] != true || access["privateExpansion"] != false {
		t.Fatalf("expected explicit public-only access metadata, got %+v", access)
	}
	souls, _ := data["souls"].([]any)
	if len(souls) != 1 {
		t.Fatalf("expected one soul, got %+v", data)
	}
	soul, _ := souls[0].(map[string]any)
	identity, _ := soul["identity"].(map[string]any)
	if identity["agentId"] != agentID || identity["localId"] != "agent-c" || identity["ensName"] != "agent-c.lessersoul.eth" {
		t.Fatalf("unexpected identity block: %+v", identity)
	}
	capabilities, _ := soul["capabilities"].([]any)
	if len(capabilities) != 1 || capabilities[0].(map[string]any)["name"] != "social.post" {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
	boundaries, _ := soul["boundaries"].([]any)
	if len(boundaries) != 1 {
		t.Fatalf("unexpected boundaries: %+v", boundaries)
	}
	boundary, _ := boundaries[0].(map[string]any)
	if boundary["id"] != "b1" || boundary["issuedAt"] != "2026-05-11T15:00:00Z" || boundary["version"] != "3" || boundary["addedInVersion"] != "3" {
		t.Fatalf("unexpected normalized boundary: %+v", boundary)
	}
	channels, _ := soul["channels"].(map[string]any)
	emailChannel, _ := channels["email"].(map[string]any)
	if emailChannel["status"] != "unavailable" || emailChannel["reason"] != "deferred_private_reachability" {
		t.Fatalf("expected private email channel to be unavailable, got %+v", channels)
	}
	avatar, _ := soul["avatar"].(map[string]any)
	if avatar["status"] != "available" || avatar["currentStyleId"] != "friendly" {
		t.Fatalf("unexpected avatar block: %+v", avatar)
	}
	if _, ok := soul["_raw"]; ok {
		t.Fatalf("did not expect raw payload by default: %+v", soul)
	}
}

func TestM2_SoulReadIncludeRawRedactsPrivateReachability(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xcacacacacacacacacacacacacacacacacacacacacacacacacacacacacacacaca"
	const privateEmail = "private-reachability@example.test"
	const privatePhone = "+15550197000"
	const privatePreference = "prefer-private-email"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{
				"agent":{
					"agent_id":"` + agentID + `",
					"domain":"test.example.com",
					"local_id":"agent-raw",
					"ens_name":"agent-raw.lessersoul.eth",
					"email":"` + privateEmail + `",
					"phone":"` + privatePhone + `",
					"status":"active"
				}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{
				"version":"3",
				"channels":{
					"ens":{"name":"agent-raw.lessersoul.eth"},
					"email":{"address":"` + privateEmail + `"},
					"phone":{"number":"` + privatePhone + `"}
				},
				"contactPreferences":{"preferred":"` + privatePreference + `"},
				"nested":{"emailAddress":"` + privateEmail + `","phoneNumber":"` + privatePhone + `"}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

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

	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"agentId":     agentID,
			"include_raw": true,
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read raw: status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal soul_read raw response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("soul_read raw rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	souls, _ := data["souls"].([]any)
	if len(souls) != 1 {
		t.Fatalf("expected one soul, got %+v", data)
	}
	soul, _ := souls[0].(map[string]any)
	channels, _ := soul["channels"].(map[string]any)
	ens, _ := channels["ens"].(map[string]any)
	if ens["name"] != "agent-raw.lessersoul.eth" {
		t.Fatalf("normalized public ENS channel should remain available, got %+v", channels)
	}
	raw, _ := soul["_raw"].(map[string]any)
	if raw == nil {
		t.Fatalf("expected sanitized _raw payload")
	}
	rawJSON, _ := json.Marshal(raw)
	for _, forbidden := range []string{privateEmail, privatePhone, privatePreference} {
		if strings.Contains(string(rawJSON), forbidden) {
			t.Fatalf("sanitized _raw leaked private reachability value %q: %s", forbidden, string(rawJSON))
		}
	}
	registrationRaw, _ := raw["registration"].(map[string]any)
	channelsRedaction, _ := registrationRaw["channels"].(map[string]any)
	if channelsRedaction["redacted"] != true || channelsRedaction["reason"] != "private_reachability" {
		t.Fatalf("expected raw registration channels redaction, got %+v", registrationRaw["channels"])
	}
	prefsRedaction, _ := registrationRaw["contactPreferences"].(map[string]any)
	if prefsRedaction["redacted"] != true || prefsRedaction["reason"] != "private_reachability" {
		t.Fatalf("expected raw registration preferences redaction, got %+v", registrationRaw["contactPreferences"])
	}
}

func TestLBM1_SoulReadSelfModeVerifiesBoundCaller(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	installSoulBindingLookup(t, "Agent1", agentID)

	var publicAuthHeaders []string
	var lesserAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			lesserAuthHeader = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-self")))
		case "/api/v1/soul/agents/" + agentID:
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-self","ens_name":"agent-self.lessersoul.eth","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"version":"1","channels":{"ens":{"name":"agent-self.lessersoul.eth"}}}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{}`))
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
		"name":      "soul_read",
		"arguments": map[string]any{"self": true},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read(self): status=%d body=%s", resp.Status, string(resp.Body))
	}
	if lesserAuthHeader != authHeader {
		t.Fatalf("expected self verification to pass caller bearer to lesser, got %q", lesserAuthHeader)
	}
	for _, got := range publicAuthHeaders {
		if strings.TrimSpace(got) != "" {
			t.Fatalf("self soul_read public endpoint should be unauthenticated, got Authorization=%q", got)
		}
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal soul_read self response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("soul_read self rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	if data["query"] != "self" || data["count"] != float64(1) {
		t.Fatalf("unexpected self result header: %+v", data)
	}
	access, _ := data["access"].(map[string]any)
	if access["mode"] != "self" || access["callerRelation"] != "self" || access["publicOnly"] != true || access["privateExpansion"] != false || access["resolution"] != "bound_caller" {
		t.Fatalf("expected explicit self access metadata, got %+v", access)
	}
	souls, _ := data["souls"].([]any)
	if len(souls) != 1 {
		t.Fatalf("expected one self soul, got %+v", data)
	}
	identity, _ := souls[0].(map[string]any)["identity"].(map[string]any)
	if identity["agentId"] != agentID || identity["localId"] != "agent-self" {
		t.Fatalf("unexpected self identity: %+v", identity)
	}
}

func TestP21_SoulReadSummaryStandardFullViews(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xfafafafafafafafafafafafafafafafafafafafafafafafafafafafafafafafa"
	const privateEmail = "summary-private@example.test"
	const privatePhone = "+15550199999"
	const capabilityConstraint = "SUMMARY_CAPABILITY_CONSTRAINT_SHOULD_NOT_APPEAR"
	const boundaryStatement = "SUMMARY_BOUNDARY_STATEMENT_SHOULD_NOT_APPEAR"
	const transparencyPayload = "SUMMARY_TRANSPARENCY_PAYLOAD_SHOULD_NOT_APPEAR"
	const selfDescription = "SUMMARY_SELF_DESCRIPTION_SHOULD_NOT_APPEAR"
	installSoulBindingLookup(t, "agent1", agentID)

	var privatePathCalled string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-summary")))
		case "/api/v1/souls/bound/me/mint-conversations":
			privatePathCalled = r.URL.Path
			_, _ = w.Write([]byte(`{"conversations":[{"conversation_id":"mint-1","messages":"PRIVATE_MINT_BODY_SHOULD_NOT_APPEAR"}]}`))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{
				"agent":{
					"agent_id":"` + agentID + `",
					"domain":"test.example.com",
					"local_id":"agent-summary",
					"ens_name":"agent-summary.lessersoul.eth",
					"wallet":"0xabc",
					"token_id":"42",
					"status":"active",
					"lifecycle_status":"active",
					"lifecycle_reason":"operational",
					"email":"` + privateEmail + `",
					"phone":"` + privatePhone + `",
					"updated_at":"2026-05-17T20:00:00Z"
				}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{
				"version":"7",
				"selfDescription":"` + selfDescription + ` ` + strings.Repeat("self description ", 120) + `",
				"channels":{"ens":{"name":"agent-summary.lessersoul.eth"},"email":{"address":"` + privateEmail + `"},"phone":{"number":"` + privatePhone + `"}},
				"avatar":{"current_style_id":"friendly","image":"https://example.com/avatar.png"},
				"capabilities":[{"name":"social.post","constraints":"` + capabilityConstraint + `"},{"name":"memory.query","constraints":"` + capabilityConstraint + `"}]
			}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities":
			_, _ = w.Write([]byte(`{"capabilities":[
				{"name":"social.post","scope":"write","constraints":"` + capabilityConstraint + `","validation_ref":"cap-ref-1"},
				{"name":"memory.query","scope":"read","constraints":"` + capabilityConstraint + `","validation_ref":"cap-ref-2"}
			]}`))
		case "/api/v1/soul/agents/" + agentID + "/boundaries":
			_, _ = w.Write([]byte(`{"boundaries":[{"id":"b1","statement":"` + boundaryStatement + `","category":"safety","issued_at":"2026-05-17T20:00:00Z"}]}`))
		case "/api/v1/soul/agents/" + agentID + "/transparency":
			_, _ = w.Write([]byte(`{"transparency":{"audit":"` + transparencyPayload + `","source":"public"}}`))
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

	call := func(id int, args map[string]any) (mcpruntime.ToolResult, []byte) {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": "soul_read", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{
			"authorization":  {authHeader},
			"mcp-session-id": {sessionID},
		}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("soul_read: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal soul_read response: %v", err)
		}
		if rpc.Error != nil {
			t.Fatalf("soul_read rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		return out, resp.Body
	}

	defaultOut, defaultBody := call(2, map[string]any{"self": true})
	defaultData, _ := defaultOut.StructuredContent["data"].(map[string]any)
	defaultSouls, _ := defaultData["souls"].([]any)
	defaultSoul, _ := defaultSouls[0].(map[string]any)
	if _, ok := defaultData["view"]; ok {
		t.Fatalf("omitted/default soul_read should preserve existing top-level shape, got %+v", defaultData)
	}
	defaultCapabilities, _ := defaultSoul["capabilities"].([]any)
	if len(defaultCapabilities) != 2 || defaultCapabilities[0].(map[string]any)["constraints"] != capabilityConstraint {
		t.Fatalf("default soul_read should preserve full normalized capability bodies, got %+v", defaultCapabilities)
	}

	standardOut, standardBody := call(3, map[string]any{"self": true, "view": "standard"})
	standardData, _ := standardOut.StructuredContent["data"].(map[string]any)
	standardSouls, _ := standardData["souls"].([]any)
	standardSoul, _ := standardSouls[0].(map[string]any)
	if _, ok := standardSoul["private"]; ok {
		t.Fatalf("standard self read without include_private should not include private blocks: %+v", standardSoul)
	}
	standardCapabilities, _ := standardSoul["capabilities"].([]any)
	if len(standardCapabilities) != 2 || standardCapabilities[0].(map[string]any)["constraints"] != capabilityConstraint {
		t.Fatalf("view=standard should preserve existing capability bodies, got %+v", standardCapabilities)
	}
	if !reflect.DeepEqual(defaultOut.StructuredContent, standardOut.StructuredContent) ||
		defaultOut.Content[0].Text != standardOut.Content[0].Text {
		t.Fatalf("soul_read omitted view must remain equivalent to view=standard")
	}

	summaryOut, summaryBody := call(4, map[string]any{"self": true, "view": "summary"})
	if summaryOut.IsError {
		t.Fatalf("summary returned tool error: %+v", summaryOut.StructuredContent)
	}
	assertMCPPayloadBudget(t, "soul_read self summary large fixture", len(summaryBody), 8000)
	for _, forbidden := range []string{capabilityConstraint, boundaryStatement, transparencyPayload, selfDescription, privateEmail, privatePhone, "PRIVATE_MINT_BODY_SHOULD_NOT_APPEAR", "\"_raw\"", "\"private\""} {
		if strings.Contains(string(summaryBody), forbidden) {
			t.Fatalf("summary leaked forbidden payload %q: %s", forbidden, string(summaryBody))
		}
	}
	summaryData, _ := summaryOut.StructuredContent["data"].(map[string]any)
	if summaryData["view"] != "summary" || summaryData["count"] != float64(1) {
		t.Fatalf("unexpected summary metadata: %+v", summaryData)
	}
	summarySouls, _ := summaryData["souls"].([]any)
	summarySoul, _ := summarySouls[0].(map[string]any)
	identity, _ := summarySoul["identity"].(map[string]any)
	if identity["agentId"] != agentID || identity["localId"] != "agent-summary" || identity["ensName"] != "agent-summary.lessersoul.eth" {
		t.Fatalf("unexpected summary identity: %+v", identity)
	}
	caps, _ := summarySoul["capabilities"].(map[string]any)
	names, _ := caps["names"].([]any)
	if !reflect.DeepEqual(names, []any{"social.post", "memory.query"}) {
		t.Fatalf("summary should expose capability names only, got %+v", caps)
	}
	channels, _ := summarySoul["channels"].(map[string]any)
	email, _ := channels["email"].(map[string]any)
	if email["status"] != "unavailable" || email["reason"] != "deferred_private_reachability" {
		t.Fatalf("summary should preserve public channel availability without reachability data, got %+v", channels)
	}
	provenance, _ := summarySoul["provenance"].(map[string]any)
	if provenance["registrationVersion"] != "7" {
		t.Fatalf("expected summary provenance markers, got %+v", provenance)
	}
	expand, _ := summarySoul["expand"].(map[string]any)
	standardExpand, _ := expand["standard"].(map[string]any)
	standardArgs, _ := standardExpand["arguments"].(map[string]any)
	fullExpand, _ := expand["full"].(map[string]any)
	fullArgs, _ := fullExpand["arguments"].(map[string]any)
	if standardExpand["tool"] != "soul_read" || standardArgs["self"] != true || standardArgs["view"] != "standard" {
		t.Fatalf("unexpected standard expansion: %+v", standardExpand)
	}
	if fullExpand["tool"] != "soul_read" || fullArgs["self"] != true || fullArgs["view"] != "full" {
		t.Fatalf("unexpected full expansion: %+v", fullExpand)
	}
	var summaryText map[string]any
	if err := json.Unmarshal([]byte(summaryOut.Content[0].Text), &summaryText); err != nil {
		t.Fatalf("unmarshal summary text: %v", err)
	}
	if _, ok := summaryText["souls"]; ok {
		t.Fatalf("summary text should use structured-first locator, got %+v", summaryText)
	}
	if locator, _ := summaryText["data"].(map[string]any); locator["location"] != "structuredContent.data" {
		t.Fatalf("expected structured data locator in summary text, got %+v", summaryText)
	}

	fullOut, fullBody := call(5, map[string]any{"self": true, "view": "full"})
	fullData, _ := fullOut.StructuredContent["data"].(map[string]any)
	fullSouls, _ := fullData["souls"].([]any)
	fullSoul, _ := fullSouls[0].(map[string]any)
	if _, ok := fullSoul["_raw"].(map[string]any); !ok {
		t.Fatalf("view=full should expose sanitized _raw public payloads, got %+v", fullSoul)
	}
	if !strings.Contains(string(fullBody), capabilityConstraint) || !strings.Contains(string(fullBody), boundaryStatement) || !strings.Contains(string(fullBody), transparencyPayload) {
		t.Fatalf("view=full should preserve full public debug payloads")
	}
	if strings.Contains(string(fullBody), privateEmail) || strings.Contains(string(fullBody), privatePhone) {
		t.Fatalf("view=full leaked private reachability data: %s", string(fullBody))
	}
	if len(summaryBody) >= len(defaultBody) {
		t.Fatalf("expected summary payload to be smaller than default: summary=%d default=%d", len(summaryBody), len(defaultBody))
	}

	privateSummary, privateSummaryBody := call(6, map[string]any{"self": true, "view": "summary", "include_private": []string{"mintConversations"}})
	if !privateSummary.IsError {
		t.Fatalf("summary with include_private should fail explicitly")
	}
	errPayload, _ := privateSummary.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "invalid_request" || errPayload["status"] != float64(400) {
		t.Fatalf("unexpected summary private error: %+v", errPayload)
	}
	if privatePathCalled != "" {
		t.Fatalf("summary private rejection should not call private endpoint, got %q", privatePathCalled)
	}

	invalidString, _ := call(7, map[string]any{"self": true, "view": "tiny"})
	if !invalidString.IsError {
		t.Fatalf("invalid view string should be a tool error")
	}
	invalidNumber, _ := call(8, map[string]any{"self": true, "view": 17})
	if !invalidNumber.IsError {
		t.Fatalf("non-string view should be a tool error")
	}
	t.Logf("soul_read payload bytes default=%d standard=%d summary=%d full=%d private_summary_error=%d",
		len(defaultBody), len(standardBody), len(summaryBody), len(fullBody), len(privateSummaryBody))
}

func TestLBM1_SoulReadComposesRegistrationFallbackBlocks(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{
				"agent":{
					"agent_id":"` + agentID + `",
					"domain":"test.example.com",
					"local_id":"agent-e",
					"ens_name":"agent-e.lessersoul.eth",
					"status":"active"
				}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			_, _ = w.Write([]byte(`{
				"registration":{
					"version":"7",
					"url":"https://host.example/soul/agents/` + agentID + `/registration",
					"self_description":{"summary":"public fallback"},
					"channels":{"ens":{"name":"agent-e.lessersoul.eth"}},
					"avatar":{
						"token_uri":"ipfs://avatar-e",
						"image":"https://cdn.example/avatar-e.png",
						"current_style_id":"minimal",
						"current_style_name":"Minimal",
						"styles":[{"style_id":"minimal","style_name":"Minimal","selected":true}]
					},
					"capabilities":["social.post"],
					"boundaries":{"results":[{"boundaryId":"b-reg","category":"safety","statement":"Use public data only","addedAt":"2026-05-11T16:00:00Z","addedInVersion":"7"}]},
					"transparency":{"ai_generated":true,"operator_disclosed":true}
				}
			}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	soulapi.ResetForTests()

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

	callParams, _ := json.Marshal(map[string]any{
		"name":      "soul_read",
		"arguments": map[string]any{"agentId": agentID},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read(fallback): status=%d body=%s", resp.Status, string(resp.Body))
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal soul_read fallback response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("soul_read fallback rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	souls, _ := data["souls"].([]any)
	if len(souls) != 1 {
		t.Fatalf("expected one composed soul, got %+v", data)
	}
	soul, _ := souls[0].(map[string]any)
	registration, _ := soul["registration"].(map[string]any)
	if registration["version"] != "7" || registration["rawAvailable"] != true {
		t.Fatalf("expected registration envelope to normalize, got %+v", registration)
	}
	capabilities, _ := soul["capabilities"].([]any)
	if len(capabilities) != 1 {
		t.Fatalf("expected capabilities fallback from registration, got %+v", capabilities)
	}
	capability, _ := capabilities[0].(map[string]any)
	if capability["name"] != "social.post" || capability["claimLevel"] != "self-declared" {
		t.Fatalf("expected v1 flat-string capability promotion, got %+v", capability)
	}
	boundaries, _ := soul["boundaries"].([]any)
	if len(boundaries) != 1 {
		t.Fatalf("expected boundaries fallback from registration, got %+v", boundaries)
	}
	boundary, _ := boundaries[0].(map[string]any)
	if boundary["id"] != "b-reg" || boundary["issuedAt"] != "2026-05-11T16:00:00Z" || boundary["addedInVersion"] != "7" {
		t.Fatalf("expected normalized fallback boundary metadata, got %+v", boundary)
	}
	transparency, _ := soul["transparency"].(map[string]any)
	if transparency["aiGenerated"] != true || transparency["operatorDisclosed"] != true {
		t.Fatalf("expected transparency fallback from registration, got %+v", transparency)
	}
	avatar, _ := soul["avatar"].(map[string]any)
	if avatar["status"] != "available" || avatar["currentStyleId"] != "minimal" {
		t.Fatalf("expected avatar fallback from registration, got %+v", avatar)
	}
	sourceEndpoints, _ := soul["sourceEndpoints"].(map[string]any)
	capSource, _ := sourceEndpoints["capabilities"].(map[string]any)
	if capSource["status"] != "unavailable" || capSource["reason"] != "not_found" {
		t.Fatalf("expected sourceEndpoints to preserve capabilities endpoint status, got %+v", sourceEndpoints)
	}
	if _, ok := sourceEndpoints["identity"].(map[string]any); !ok {
		t.Fatalf("expected identity source endpoint, got %+v", sourceEndpoints)
	}
}

func TestLBM2_SoulReadPrivateMintConversationsUsesLesserSelfRoute(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	installSoulBindingLookup(t, "Agent1", agentID)

	var lesserSelfAuth string
	var privateListAuth string
	var privateListLimit string
	var directHostPath string
	var publicAuthHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/soul/instance/") {
			directHostPath = r.URL.Path
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"body must not call host directly"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			lesserSelfAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-private")))
		case "/api/v1/soul/agents/" + agentID:
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-private","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration":
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"version":"1"}`))
		case "/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			publicAuthHeaders = append(publicAuthHeaders, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/souls/bound/me/mint-conversations":
			privateListAuth = r.Header.Get("Authorization")
			privateListLimit = r.URL.Query().Get("limit")
			_, _ = w.Write([]byte(`{
				"version":"1",
				"conversations":[{
					"agent_id":"` + agentID + `",
					"conversation_id":"conv-1",
					"model":"gpt-test",
					"messages":"private list content must not appear",
					"produced_declarations":"private declarations must not appear",
					"status":"completed",
					"usage":{"input_tokens":1},
					"charged_credits":3,
					"created_at":"2026-05-11T21:00:00Z",
					"completed_at":"2026-05-11T21:01:00Z"
				}],
				"count":1,
				"limit":2
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "must-not-be-used-by-soul-read")
	lesserapi.ResetForTests()
	soulapi.ResetForTests()

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"self":                  true,
			"include_private":       []string{"mintConversations"},
			"mintConversationLimit": 2,
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read private list: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if lesserSelfAuth != authHeader || privateListAuth != authHeader {
		t.Fatalf("expected caller bearer only to Lesser self routes, bound=%q private=%q", lesserSelfAuth, privateListAuth)
	}
	if privateListLimit != "2" {
		t.Fatalf("expected private list limit=2, got %q", privateListLimit)
	}
	if directHostPath != "" {
		t.Fatalf("soul_read must not call Host directly, hit %q", directHostPath)
	}
	for _, got := range publicAuthHeaders {
		if strings.TrimSpace(got) != "" {
			t.Fatalf("public soul_read endpoint should be unauthenticated, got Authorization=%q", got)
		}
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	access, _ := data["access"].(map[string]any)
	if access["mode"] != "self" || access["publicOnly"] != false || access["privateExpansion"] != true || access["authorization"] != "lesser_self_scope_instance_trust" {
		t.Fatalf("unexpected private access metadata: %+v", access)
	}
	souls, _ := data["souls"].([]any)
	soul, _ := souls[0].(map[string]any)
	privateBlocks, _ := soul["private"].(map[string]any)
	mint, _ := privateBlocks["mintConversations"].(map[string]any)
	if mint["mode"] != "list" || mint["limit"] != float64(2) || mint["count"] != float64(1) {
		t.Fatalf("unexpected private mint list block: %+v", mint)
	}
	conversations, _ := mint["conversations"].([]any)
	if len(conversations) != 1 {
		t.Fatalf("expected one conversation summary, got %+v", mint)
	}
	summary, _ := conversations[0].(map[string]any)
	if summary["conversationId"] != "conv-1" || summary["chargedCredits"] != float64(3) {
		t.Fatalf("unexpected conversation summary: %+v", summary)
	}
	if _, ok := summary["messages"]; ok {
		t.Fatalf("list summaries must not expose messages: %+v", summary)
	}
	if _, ok := summary["producedDeclarations"]; ok {
		t.Fatalf("list summaries must not expose produced declarations: %+v", summary)
	}
}

func TestLBM2_SoulReadPrivateMintConversationSingleIncludesExplicitContent(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0x9999999999999999999999999999999999999999999999999999999999999999"
	const conversationID = "conv:single.1"
	largePrivateMessage := "private-lab-fixture " + strings.Repeat("private ", 9000)
	installSoulBindingLookup(t, "Agent1", agentID)

	var privateGetAuth string
	var privateGetQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-private")))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-private","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration",
			"/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/souls/bound/me/mint-conversations/" + conversationID:
			privateGetAuth = r.Header.Get("Authorization")
			privateGetQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": "1",
				"conversation": map[string]any{
					"agent_id":              agentID,
					"conversation_id":       conversationID,
					"model":                 "gpt-test",
					"messages":              largePrivateMessage,
					"produced_declarations": map[string]any{"capabilities": []any{}},
					"status":                "completed",
					"usage":                 map[string]any{"output_tokens": 2},
					"charged_credits":       7,
					"created_at":            "2026-05-11T21:00:00Z",
					"completed_at":          "2026-05-11T21:01:00Z",
				},
			})
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

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]
	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"self":               true,
			"include_private":    []string{"mintConversations"},
			"mintConversationId": conversationID,
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read private single: status=%d body=%s", resp.Status, string(resp.Body))
	}
	if privateGetAuth != authHeader {
		t.Fatalf("expected caller bearer to Lesser private get route, got %q", privateGetAuth)
	}
	if privateGetQuery != "" {
		t.Fatalf("single conversation read must not send list query, got %q", privateGetQuery)
	}
	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	if len(toolOut.Content) != 1 {
		t.Fatalf("expected one text content block, got %+v", toolOut.Content)
	}
	if strings.Contains(toolOut.Content[0].Text, "private-lab-fixture") {
		t.Fatalf("text content must not duplicate explicit private single-read content")
	}
	if !strings.Contains(toolOut.Content[0].Text, "available_in_structured_content") {
		t.Fatalf("text content should point clients to structuredContent for private fields, got %s", toolOut.Content[0].Text)
	}
	data, _ := toolOut.StructuredContent["data"].(map[string]any)
	souls, _ := data["souls"].([]any)
	soul, _ := souls[0].(map[string]any)
	privateBlocks, _ := soul["private"].(map[string]any)
	mint, _ := privateBlocks["mintConversations"].(map[string]any)
	if mint["mode"] != "single" {
		t.Fatalf("expected single private mode, got %+v", mint)
	}
	conversation, _ := mint["conversation"].(map[string]any)
	if conversation["conversationId"] != conversationID || !strings.Contains(conversation["messages"].(string), "private-lab-fixture") {
		t.Fatalf("unexpected single private conversation: %+v", conversation)
	}
	if _, ok := conversation["producedDeclarations"]; !ok {
		t.Fatalf("expected produced declarations in explicit single read: %+v", conversation)
	}
}

func TestLBM2_SoulReadPrivateMintConversationsFailsClosed(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	installMissingSoulBindingLookup(t)
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0x8888888888888888888888888888888888888888888888888888888888888888"
	var privateRouteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-public","status":"active"}}`))
		case "/api/v1/souls/bound/me/mint-conversations":
			privateRouteCalled = true
			_, _ = w.Write([]byte(`{"version":"1","conversations":[],"count":0,"limit":20}`))
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

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"agentId":         agentID,
			"include_private": []string{"mintConversations"},
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read non-self private: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	if !toolOut.IsError {
		t.Fatalf("expected private non-self request to fail closed, got %+v", toolOut)
	}
	errPayload, _ := toolOut.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", errPayload)
	}
	if privateRouteCalled {
		t.Fatalf("private route should not be called for non-self request")
	}
}

func TestLBM2_SoulReadPrivateMintConversationsMapsLesserErrors(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()
	lesserapi.ResetForTests()
	soulapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	t.Cleanup(soulapi.ResetForTests)

	const agentID = "0x7777777777777777777777777777777777777777777777777777777777777777"
	installSoulBindingLookup(t, "Agent1", agentID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-private")))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-private","status":"active"}}`))
		case "/api/v1/soul/agents/" + agentID + "/registration",
			"/api/v1/soul/agents/" + agentID + "/capabilities",
			"/api/v1/soul/agents/" + agentID + "/boundaries",
			"/api/v1/soul/agents/" + agentID + "/transparency":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/souls/bound/me/mint-conversations":
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"private soul conversation read rate limited","error_description":"slow down","error_code":"SOUL_PRIVATE_RATE_LIMITED"}`))
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

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newTestToken(t, "test", "Agent1", []string{"read"})
	authHeader := "Bearer " + token
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]
	callParams, _ := json.Marshal(map[string]any{
		"name": "soul_read",
		"arguments": map[string]any{
			"self":            true,
			"include_private": []string{"mintConversations"},
		},
	})
	resp := invokeJSON(t, env, app, map[string][]string{
		"authorization":  {authHeader},
		"mcp-session-id": {sessionID},
	}, &mcpruntime.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: callParams})
	if resp.Status != 200 {
		t.Fatalf("soul_read private rate limited: status=%d body=%s", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	_ = json.Unmarshal(resp.Body, &rpc)
	if rpc.Error != nil {
		t.Fatalf("rpc error: %+v", rpc.Error)
	}
	var toolOut mcpruntime.ToolResult
	{
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &toolOut)
	}
	if !toolOut.IsError {
		t.Fatalf("expected tool error, got %+v", toolOut)
	}
	errPayload, _ := toolOut.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "rate_limited" || errPayload["status"] != float64(http.StatusTooManyRequests) {
		t.Fatalf("unexpected private rate-limit error: %+v", errPayload)
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["upstreamErrorCode"] != "SOUL_PRIVATE_RATE_LIMITED" || details["retryAfter"] != "3" {
		t.Fatalf("expected upstream error details, got %+v", details)
	}
}
