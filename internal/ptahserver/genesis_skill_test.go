package ptahserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func TestAgentGenesisSkillGetReturnsDeterministicHostBackedBundle(t *testing.T) {
	fake := &fakeGenesisClient{}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	first := callGenesisTool(t, registry, operatorToolContext("owner", []string{"read"}, "owner-oauth-bearer-test-only"), toolAgentGenesisSkillGet, `{}`)
	second := callGenesisTool(t, registry, operatorToolContext("owner", []string{"read"}, "owner-oauth-bearer-test-only"), toolAgentGenesisSkillGet, `null`)
	if len(fake.calls) != 0 {
		t.Fatalf("agent_genesis_skill_get must not call Host: %v", fake.calls)
	}
	firstData := structuredGenesisData(t, first)
	secondData := structuredGenesisData(t, second)
	if !reflect.DeepEqual(firstData, secondData) {
		t.Fatalf("skill bundle is not deterministic:\nfirst = %#v\nsecond = %#v", firstData, secondData)
	}

	if firstData["operation"] != "skill_get" || firstData["status"] != "skill_ready" || firstData["state_authority"] != "Host HostedGenesisSession" {
		t.Fatalf("skill data envelope = %+v", firstData)
	}
	skill := firstData["skill"].(map[string]any)
	if skill["id"] != genesisSkillID || skill["name"] != genesisSkillName {
		t.Fatalf("skill identity = %+v", skill)
	}
	version := skill["version"].(string)
	if !strings.Contains(version, fiveBodyGuidanceVersion) || !strings.Contains(version, fiveBodyHostHeadSHA[:12]) {
		t.Fatalf("skill version %q must derive from %s and the pinned Host head", version, fiveBodyGuidanceVersion)
	}
	if firstData["bundle_id"] != genesisSkillBundleID(genesisSkillFiles()) || !strings.HasPrefix(firstData["bundle_id"].(string), "sha256:") {
		t.Fatalf("bundle_id = %v", firstData["bundle_id"])
	}

	content := firstData["content"].(map[string]any)
	files := firstData["files"].([]map[string]any)
	if content["mode"] != "inline_files" || content["file_count"] != len(files) || len(files) == 0 {
		t.Fatalf("content summary = %+v with %d files", content, len(files))
	}
	var skillMD string
	for _, file := range files {
		body := file["content"].(string)
		if file["sha256"] != sha256Hex([]byte(body)) || file["bytes"] != len(body) {
			t.Fatalf("file entry checksum mismatch: %+v", file)
		}
		if file["path"] == "SKILL.md" {
			skillMD = body
		}
	}
	if skillMD == "" {
		t.Fatal("bundle is missing a SKILL.md entry")
	}
	for _, want := range []string{
		toolAgentGenesisSkillGet,
		toolAgentGenesisBegin,
		"identity, then philosophy, then discipline, then boundaries, then soul",
		"Persist the Host conversation_id immediately",
		"structuredContent.data.guidance.next_tool",
		toolAgentGenesisComplete,
		"you never submit declarations as the source of truth",
		toolAgentGenesisFinalizePreflight,
		toolAgentGenesisFinalize,
		toolAgentGet,
		"restart_soul_bootstrap",
		"Do not call `" + toolAgentGenesisRecover + "`",
		"Host is the source of truth",
		"Never fabricate",
		canonicalGenesisAffirmation(),
	} {
		if !strings.Contains(skillMD, want) {
			t.Fatalf("SKILL.md missing operating directive %q", want)
		}
	}

	provenance := firstData["provenance"].(map[string]any)
	if provenance["host_pr"] != fiveBodyHostPR || provenance["host_head_sha"] != fiveBodyHostHeadSHA {
		t.Fatalf("provenance = %+v", provenance)
	}
	meta := mustFiveBodyMetadata()
	hostContract := provenance["host_contract"].(map[string]any)
	checksums := hostContract["checksums"].(map[string]string)
	if hostContract["schema_version"] != meta.SchemaVersion || checksums["schema_sha256"] != meta.SchemaSHA256 || checksums["contract_doc_sha256"] != meta.ContractDocSHA256 {
		t.Fatalf("host contract provenance = %+v", hostContract)
	}

	semantics := firstData["semantics"].(map[string]any)
	if semantics["read_only"] != true || semantics["install"] != "none" || semantics["writes"] != "none" || semantics["materialization"] != "client_decides" {
		t.Fatalf("semantics = %+v", semantics)
	}
	guidance := firstData["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisBegin {
		t.Fatalf("guidance = %+v", guidance)
	}

	encoded := mustMarshalGenesisResult(t, first, second)
	if strings.Contains(encoded, "owner-oauth-bearer-test-only") {
		t.Fatalf("skill result leaked the caller bearer token: %s", encoded)
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(first.Content[0].Text), &text); err != nil {
		t.Fatalf("skill text summary is not JSON: %v", err)
	}
	if text["bundle_id"] != firstData["bundle_id"] || text["summary"] == nil {
		t.Fatalf("skill text summary = %+v", text)
	}
}

func TestAgentGenesisSkillGetRequiresOwnerOperatorReadAuthority(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(&fakeGenesisClient{})); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ordinary, err := registry.Call(toolContext("owner", []string{"read"}, "ordinary-oauth-bearer"), toolAgentGenesisSkillGet, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ordinary call: %v", err)
	}
	assertToolError(t, ordinary, "owner_operator_required", 403)
	noRead, err := registry.Call(operatorToolContext("owner", []string{"follow"}, "operator-oauth-bearer"), toolAgentGenesisSkillGet, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("no read call: %v", err)
	}
	assertToolError(t, noRead, "insufficient_scope", 403)
	agent, err := registry.Call(toolContextWithAgent("owner", []string{"read"}, "agent-delegated-bearer", true), toolAgentGenesisSkillGet, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("agent call: %v", err)
	}
	assertToolError(t, agent, "forbidden", 403)
	badArgs, err := registry.Call(operatorToolContext("owner", []string{"read"}, "operator-oauth-bearer"), toolAgentGenesisSkillGet, json.RawMessage(`{"install":true}`))
	if err != nil {
		t.Fatalf("bad args call: %v", err)
	}
	assertToolError(t, badArgs, "invalid_request", 400)
}

func TestGenesisOperatorSkillResourceMatchesToolBundle(t *testing.T) {
	srv := mcpruntime.NewServer("ptah-test", "dev", mcpruntime.WithCapabilityConfig(mcpruntime.CapabilityConfig{Tools: true, Resources: true, Prompts: true}))
	if err := RegisterResources(srv); err != nil {
		t.Fatalf("RegisterResources: %v", err)
	}

	payload := resourcePayload(t, srv, resourceGenesisOperatorSkill)
	if payload["kind"] != resourceGenesisOperatorSkill || payload["tool"] != toolAgentGenesisSkillGet {
		t.Fatalf("skill resource payload = %+v", payload)
	}
	bundle := payload["skill_bundle"].(map[string]any)
	if bundle["bundle_id"] != genesisSkillBundleID(genesisSkillFiles()) {
		t.Fatalf("resource bundle_id = %v, want the deterministic tool bundle id", bundle["bundle_id"])
	}
	files := bundle["files"].([]any)
	if len(files) != len(genesisSkillFiles()) {
		t.Fatalf("resource bundle files = %d, want %d", len(files), len(genesisSkillFiles()))
	}

	playbook := resourcePayload(t, srv, resourceAgentSideGenesisPlaybook)["playbook"].(map[string]any)
	skillLink := playbook["skill"].(map[string]any)
	if skillLink["tool"] != toolAgentGenesisSkillGet || skillLink["resource"] != fiveBodyResourceURI(resourceGenesisOperatorSkill) {
		t.Fatalf("playbook skill link = %+v", skillLink)
	}
	sequence := playbook["tool_sequence"].([]any)
	firstStep := sequence[0].(map[string]any)
	if firstStep["step"] != "skill" || firstStep["tool"] != toolAgentGenesisSkillGet {
		t.Fatalf("playbook first step = %+v, want the skill fetch step", firstStep)
	}

	guidanceMap := ""
	for _, file := range genesisSkillFiles() {
		if file.path == "references/genesis-guidance-map.md" {
			guidanceMap = file.content
		}
	}
	for _, want := range []string{
		fiveBodyResourceURI(resourceSoulSchemaV2),
		fiveBodyResourceURI(resourceGenesisOperatorSkill),
		promptDraftGenesisTurn,
		promptReviewSoulDraft,
		fiveBodyHostHeadSHA,
	} {
		if !strings.Contains(guidanceMap, want) {
			t.Fatalf("guidance map missing %q", want)
		}
	}
}
