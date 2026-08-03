package ptahserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
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
	var guidanceMap string
	for _, file := range files {
		body := file["content"].(string)
		if file["sha256"] != sha256Hex([]byte(body)) || file["bytes"] != len(body) {
			t.Fatalf("file entry checksum mismatch: %+v", file)
		}
		switch file["path"] {
		case "SKILL.md":
			skillMD = body
		case "references/genesis-guidance-map.md":
			guidanceMap = body
		}
	}
	if skillMD == "" || guidanceMap == "" {
		t.Fatalf("bundle is missing expected skill files: SKILL.md=%t guidance_map=%t", skillMD != "", guidanceMap != "")
	}
	for _, want := range []string{
		toolAgentGenesisSkillGet,
		toolAgentGenesisBegin,
		"candidate phase is",
		"Persist conversation_id after every call",
		"structuredContent.data.guidance.next_tool",
		"`in_progress` is wait/read-only",
		"Never call advance to nudge",
		"poll_after_seconds",
		"exact, lossless",
		"candidate_action",
		"`affirm` forbids section",
		"`edit` requires one exact section",
		"`declaration_ready`",
		toolAgentGenesisFinalizePreflight,
		toolAgentGenesisFinalize,
		"Verify with `" + toolAgentGet + "` / `" + toolAgentList + "`",
		"`restart_soul_bootstrap`",
		"fresh `" + toolAgentGenesisBegin + "`, not `" + toolAgentGenesisRecover + "`",
		"`retry_same_step`",
		"call `" + toolAgentGenesisRecover + "` exactly once",
		"`refresh_state`",
		"call `" + toolAgentGenesisRead + "` exactly once",
		"`operator_action`",
		"contact the instance operator",
		"sole candidate/state authority",
		"Free-form or canonical affirmation phrases have zero authority",
		"AppTheory MicroVM",
	} {
		if !strings.Contains(skillMD, want) {
			t.Fatalf("SKILL.md missing operating directive %q", want)
		}
	}

	if len(first.Content) != 1 {
		t.Fatalf("skill visible content blocks = %d, want 1", len(first.Content))
	}
	visible := first.Content[0].Text
	if first.Content[0].Type != "text" {
		t.Fatalf("skill visible content type = %q", first.Content[0].Type)
	}
	if !strings.Contains(visible, "## File: `SKILL.md`") || !strings.Contains(visible, "## File: `references/genesis-guidance-map.md`") {
		t.Fatalf("visible skill content does not expose both files: %s", visible)
	}
	if !strings.Contains(visible, skillMD) || !strings.Contains(visible, guidanceMap) {
		t.Fatalf("visible skill content must include complete SKILL.md and guidance map")
	}
	assertVisibleGenesisSkillDirectives(t, visible)

	provenance := firstData["provenance"].(map[string]any)
	if provenance["host_pr"] != fiveBodyHostPR || provenance["host_head_sha"] != fiveBodyHostHeadSHA {
		t.Fatalf("provenance = %+v", provenance)
	}
	meta := mustFiveBodyMetadata()
	hostContract := provenance["host_contract"].(map[string]any)
	checksums := hostContract["checksums"].(map[string]string)
	if hostContract["schema_version"] != meta.SchemaVersion || len(checksums) != len(meta.Artifacts) || checksums["hosted-genesis.conversation.response.schema.json"] == "" || checksums["openapi.yaml"] == "" {
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
	for _, forbidden := range []string{"owner-oauth-bearer-test-only", "LESSER_HOST_INSTANCE_KEY"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("skill result leaked forbidden token/secret marker %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(visible, firstData["bundle_id"].(string)) {
		t.Fatalf("visible skill content missing bundle id %v", firstData["bundle_id"])
	}
}

func assertVisibleGenesisSkillDirectives(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{
		toolAgentGenesisBegin,
		"Candidate section phase",
		"Persist `conversation_id` immediately and after every call",
		"Follow `structuredContent.data.guidance.next_tool`",
		"`in_progress` is wait/read-only",
		"Do not call `" + toolAgentGenesisAdvance + "` again and do not nudge",
		"Candidate review requires inspecting the exact lossless `review_text`",
		"structural `candidate_action`",
		"`declaration_ready` goes directly",
		toolAgentGenesisFinalizePreflight,
		toolAgentGenesisFinalize,
		"Verify with `" + toolAgentGet + "` / `" + toolAgentList + "`",
		"`restart_soul_bootstrap` means fresh `" + toolAgentGenesisBegin + "`, not `" + toolAgentGenesisRecover + "`",
		"`retry_same_step` means wait",
		"call `" + toolAgentGenesisRecover + "` exactly once",
		"`refresh_state` means call `" + toolAgentGenesisRead + "` exactly once",
		"`operator_action` means stop automatic Genesis tool calls",
		"sole candidate/state authority",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("visible skill content missing operating directive %q", want)
		}
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

	contents, err := srv.Resources().Read(context.Background(), fiveBodyResourceURI(resourceGenesisOperatorSkill))
	if err != nil {
		t.Fatalf("read visible skill resource: %v", err)
	}
	resourceText := contents[0].Text
	for _, want := range []string{
		"SKILL.md",
		"references/genesis-guidance-map.md",
		toolAgentGenesisBegin,
		"candidate phase is",
		"Persist conversation_id after every call",
		"structuredContent.data.guidance.next_tool",
		"`in_progress` is wait/read-only",
		"Never call advance to nudge",
		"candidate_action",
		"exact, lossless",
		toolAgentGenesisFinalizePreflight,
		toolAgentGenesisFinalize,
		"Verify with `" + toolAgentGet + "` / `" + toolAgentList + "`",
		"fresh `" + toolAgentGenesisBegin + "`, not `" + toolAgentGenesisRecover + "`",
		"`retry_same_step`",
		"call `" + toolAgentGenesisRecover + "` exactly once",
		"`refresh_state`",
		"`operator_action`",
		"sole candidate/state authority",
	} {
		if !strings.Contains(resourceText, want) {
			t.Fatalf("skill resource visible JSON missing %q", want)
		}
	}
	if strings.Contains(resourceText, "LESSER_HOST_INSTANCE_KEY") {
		t.Fatalf("skill resource leaked secret marker: %s", resourceText)
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
	recoveryActions := playbook["recovery_actions"].(map[string]any)
	for action, wantTool := range map[string]string{
		"retry_same_step":        toolAgentGenesisRecover,
		"restart_soul_bootstrap": toolAgentGenesisBegin,
		"refresh_state":          toolAgentGenesisRead,
	} {
		actionGuidance := recoveryActions[action].(map[string]any)
		if actionGuidance["next_tool"] != wantTool {
			t.Fatalf("playbook recovery action %s = %+v, want next_tool %s", action, actionGuidance, wantTool)
		}
	}
	operatorGuidance := recoveryActions["operator_action"].(map[string]any)
	if _, ok := operatorGuidance["next_tool"]; ok {
		t.Fatalf("playbook operator_action must not choose an automatic tool: %+v", operatorGuidance)
	}
	restartGuidance := recoveryActions["restart_soul_bootstrap"].(map[string]any)
	if restartGuidance["fresh_lane"] != true || restartGuidance["forbidden_next_tool"] != toolAgentGenesisRecover {
		t.Fatalf("playbook restart_soul_bootstrap guidance = %+v", restartGuidance)
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
		promptReviewGenesisCandidate,
		fiveBodyHostHeadSHA,
		"in_progress",
		"wait-only; never " + toolAgentGenesisAdvance,
		"candidate phase review",
		"failure.recovery.action=retry_same_step",
		"failure.recovery.action=refresh_state",
		"failure.recovery.action=operator_action",
	} {
		if !strings.Contains(guidanceMap, want) {
			t.Fatalf("guidance map missing %q", want)
		}
	}
}
