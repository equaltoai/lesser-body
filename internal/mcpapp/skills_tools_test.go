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

func TestProject21M4SkillsCatalogAndBundleTools(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	type recorded struct {
		Method string
		Path   string
		Query  string
		Auth   string
	}
	var got []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/skills/catalog":
			if r.Method != http.MethodGet {
				t.Fatalf("catalog method got %s", r.Method)
			}
			_, _ = w.Write([]byte(`{
				"entries":[{
					"skill":{"id":"skill-a","slug":"skill-a","name":"Skill A","default_exposure":"public","status":"approved"},
					"revision":{"id":"skill-a-r1","skill_id":"skill-a","revision_number":1,"status":"approved","bundle_digest":"sha256:bundle","approval_digest":"sha256:approval","principal_id":"principal-1"},
					"bundle":{"schema_version":"lesser.skill.bundle.v1","bundle_id":"skill:skill-a:revision:00000001","source":"canonical_skill_revision","published":true,"skill_id":"skill-a","revision_id":"skill-a-r1","revision_number":1,"default_exposure":"public","digests":{"bundle_digest":"sha256:bundle","publication_digest":"sha256:publication","manifest_digest":"sha256:manifest","content_digest":"sha256:content","approval_digest":"sha256:approval"},"files":[{"path":"SKILL.md","digest":"sha256:file","install_path":"skill-a/SKILL.md","content_included":false}],"install_hints":{"layout":"codex-skill","directory_name":"skill-a","entrypoint":"SKILL.md"},"provenance":[{"source_type":"proposal","digest":"sha256:proposal"}],"approval_id":"approval-1","principal_id":"principal-1"}
				}],
				"count":1,
				"next_cursor":"next-catalog"
			}`))
		case "/api/v1/skills/skill-a/revisions/1/bundle":
			if r.Method != http.MethodGet {
				t.Fatalf("bundle method got %s", r.Method)
			}
			contentIncluded := r.URL.Query().Get("include_content") == "true"
			content := ""
			if contentIncluded {
				content = `,"content":"# Skill A\n","encoding":"utf-8"`
			}
			_, _ = w.Write([]byte(`{
				"bundle":{"schema_version":"lesser.skill.bundle.v1","bundle_id":"skill:skill-a:revision:00000001","source":"canonical_skill_revision","published":true,"skill_id":"skill-a","revision_id":"skill-a-r1","revision_number":1,"default_exposure":"public","digests":{"bundle_digest":"sha256:bundle","publication_digest":"sha256:publication","manifest_digest":"sha256:manifest","content_digest":"sha256:content","approval_digest":"sha256:approval"},"files":[{"path":"SKILL.md","digest":"sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935","install_path":"skill-a/SKILL.md","content_included":` + boolJSON(contentIncluded) + content + `}],"install_hints":{"layout":"codex-skill","directory_name":"skill-a","entrypoint":"SKILL.md","required_files":["SKILL.md"]},"provenance":[{"source_type":"proposal","digest":"sha256:proposal"}],"approval_id":"approval-1","approval_authority_type":"pilot","approval_authority_id":"pilot.simulacrum@lessersoul.ai","approved_by":"Pilot","principal_id":"principal-1","principal_approval_id":"pa-1"}
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
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

	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	callTool := func(id int, name string, args map[string]any) mcpruntime.ToolResult {
		t.Helper()
		callParams, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}, "mcp-session-id": {sessionID}}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: callParams})
		if resp.Status != 200 {
			t.Fatalf("%s: status=%d body=%s", name, resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpc); err != nil {
			t.Fatalf("unmarshal %s rpc: %v", name, err)
		}
		if rpc.Error != nil {
			t.Fatalf("%s rpc error: %+v", name, rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal %s tool result: %v", name, err)
		}
		return out
	}

	catalog := callTool(2, "skills_catalog", map[string]any{"limit": 1, "cursor": "c1", "exposure": "public"})
	catalogData, _ := catalog.StructuredContent["data"].(map[string]any)
	if catalogData == nil {
		t.Fatalf("expected catalog structured data, got %+v", catalog.StructuredContent)
	}
	if got[0].Path != "/api/v1/skills/catalog" || got[0].Query != "cursor=c1&exposure=public&limit=1" || got[0].Auth != authHeader {
		t.Fatalf("unexpected catalog request: %+v", got[0])
	}
	entries, _ := catalogData["entries"].([]any)
	if len(entries) != 1 || catalogData["next_cursor"] != "next-catalog" {
		t.Fatalf("catalog response did not preserve Lesser shape: %+v", catalogData)
	}
	entry := entries[0].(map[string]any)
	bundle := entry["bundle"].(map[string]any)
	digests := bundle["digests"].(map[string]any)
	if digests["publication_digest"] != "sha256:publication" || bundle["install_hints"] == nil || bundle["provenance"] == nil {
		t.Fatalf("catalog did not preserve trust metadata: %+v", bundle)
	}

	_ = callTool(3, "skills_catalog", map[string]any{"limit": 1000})
	if got[1].Path != "/api/v1/skills/catalog" || got[1].Query != "limit=100" || got[1].Auth != authHeader {
		t.Fatalf("skills_catalog should cap oversized limits before Lesser, got %+v", got[1])
	}

	bundleResult := callTool(4, "skill_bundle_get", map[string]any{
		"skill_id":        "skill-a",
		"revision_number": 1,
		"include_content": true,
		"local_files": []map[string]any{{
			"path":     "SKILL.md",
			"content":  "# Skill A\n",
			"encoding": "utf-8",
		}},
	})
	bundleData, _ := bundleResult.StructuredContent["data"].(map[string]any)
	if bundleData == nil {
		t.Fatalf("expected bundle structured data, got %+v", bundleResult.StructuredContent)
	}
	if got[2].Path != "/api/v1/skills/skill-a/revisions/1/bundle" || got[2].Query != "include_content=true" || got[2].Auth != authHeader {
		t.Fatalf("unexpected bundle request: %+v", got[2])
	}
	content, _ := bundleData["content"].(map[string]any)
	if content["mode"] != "inline" {
		t.Fatalf("expected inline content summary, got %+v", content)
	}
	verification, _ := bundleData["verification"].(map[string]any)
	if verification["state"] != "verified_match" || verification["checked_files"] != float64(1) {
		t.Fatalf("expected verified local bundle, got %+v", verification)
	}
	authority, _ := bundleData["authority"].(map[string]any)
	if authority["workspace_mutated"] != false || authority["source"] != "lesser" {
		t.Fatalf("unexpected authority metadata: %+v", authority)
	}
	bundleObj, _ := bundleData["bundle"].(map[string]any)
	if bundleObj["approval_id"] != "approval-1" || bundleObj["principal_id"] != "principal-1" {
		t.Fatalf("bundle did not preserve approval/principal metadata: %+v", bundleObj)
	}

	metadataOnly := callTool(5, "skill_bundle_get", map[string]any{"bundle_id": "skill:skill-a:revision:00000001"})
	metadataData, _ := metadataOnly.StructuredContent["data"].(map[string]any)
	metadataContent, _ := metadataData["content"].(map[string]any)
	metadataVerification, _ := metadataData["verification"].(map[string]any)
	if metadataContent["mode"] != "metadata_only" || metadataVerification["state"] != "unknown_local_state" {
		t.Fatalf("metadata-only bundle should report unknown local state, content=%+v verification=%+v", metadataContent, metadataVerification)
	}
}

func TestProject21M4SkillBundleReportsModifiedAndUpstreamFailure(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("JWT_SECRET", "test")
	auth.ResetForTests()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fail/"):
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
		default:
			_, _ = w.Write([]byte(`{"bundle":{"digests":{"bundle_digest":"sha256:bundle","publication_digest":"sha256:publication"},"files":[{"path":"SKILL.md","digest":"sha256:3fc349d92cca36c2326da54076558a5d94b76d815cfe2bd1a0ce0ec284d53935","install_path":"skill-a/SKILL.md","content_included":false}]}}`))
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
	initResp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}}, &mcpruntime.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Status != 200 {
		t.Fatalf("initialize: status=%d body=%s", initResp.Status, string(initResp.Body))
	}
	sessionID := initResp.Headers["mcp-session-id"][0]

	call := func(id int, args map[string]any) mcpruntime.ToolResult {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"name": "skill_bundle_get", "arguments": args})
		resp := invokeJSON(t, env, app, map[string][]string{"authorization": {authHeader}, "mcp-session-id": {sessionID}}, &mcpruntime.Request{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params})
		if resp.Status != 200 {
			t.Fatalf("skill_bundle_get: status=%d body=%s", resp.Status, string(resp.Body))
		}
		var rpc mcpruntime.Response
		_ = json.Unmarshal(resp.Body, &rpc)
		if rpc.Error != nil {
			t.Fatalf("skill_bundle_get rpc error: %+v", rpc.Error)
		}
		var out mcpruntime.ToolResult
		b, _ := json.Marshal(rpc.Result)
		_ = json.Unmarshal(b, &out)
		return out
	}

	modified := call(2, map[string]any{"skill_id": "skill-a", "revision_number": 1, "local_files": []map[string]any{{"path": "SKILL.md", "content": "changed"}}})
	modifiedData, _ := modified.StructuredContent["data"].(map[string]any)
	verification, _ := modifiedData["verification"].(map[string]any)
	if verification["state"] != "modified_local_copy" {
		t.Fatalf("expected modified_local_copy, got %+v", verification)
	}

	failure := call(3, map[string]any{"skill_id": "fail", "revision_number": 1})
	if !failure.IsError {
		t.Fatalf("expected tool-level error for Lesser failure, got %+v", failure)
	}
	errPayload, _ := failure.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "upstream_error" || errPayload["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("unexpected error payload: %+v", errPayload)
	}
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
