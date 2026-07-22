package ptahserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func TestFiveBodyGuidanceRegistersResourcesAndPrompts(t *testing.T) {
	srv := mcpruntime.NewServer("ptah-test", "dev", mcpruntime.WithCapabilityConfig(mcpruntime.CapabilityConfig{Tools: true, Resources: true, Prompts: true}))
	if err := RegisterResources(srv); err != nil {
		t.Fatalf("RegisterResources: %v", err)
	}
	if err := RegisterPrompts(srv); err != nil {
		t.Fatalf("RegisterPrompts: %v", err)
	}

	resources := srv.Resources().List()
	if got, want := resourceNames(resources), []string{resourceSoulSchemaV2, resourceGenesisInterviewGuide, resourceAgentSideGenesisPlaybook, resourceGenesisRubric, resourceGenesisOperatorSkill}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resource names = %v, want %v", got, want)
	}
	for _, res := range resources {
		if !strings.HasPrefix(res.URI, ptahGenesisGuidanceResourcePrefix) || res.MimeType != "application/json" {
			t.Fatalf("resource def = %+v, want ptah:// JSON resource", res)
		}
		contents, err := srv.Resources().Read(context.Background(), res.URI)
		if err != nil {
			t.Fatalf("Read(%s): %v", res.URI, err)
		}
		if len(contents) != 1 || contents[0].URI != res.URI || contents[0].MimeType != "application/json" || !json.Valid([]byte(contents[0].Text)) {
			t.Fatalf("resource contents for %s = %+v", res.URI, contents)
		}
	}
	templates := srv.Resources().ListTemplates()
	if len(templates) != 1 || templates[0].URITemplate != ptahGenesisGuidanceResourcePrefix+"{resource}" {
		t.Fatalf("resource templates = %+v", templates)
	}

	schemaPayload := resourcePayload(t, srv, resourceSoulSchemaV2)
	contract := schemaPayload["contract"].(map[string]any)
	if contract["schema_version"] != fiveBodySchemaVersion || contract["guidance_version"] != fiveBodyGuidanceVersion || contract["source_head_sha"] != fiveBodyHostHeadSHA {
		t.Fatalf("contract descriptor = %+v", contract)
	}
	schema := schemaPayload["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if props["schemaVersion"].(map[string]any)["const"] != fiveBodySchemaVersion || props["guidanceVersion"].(map[string]any)["const"] != fiveBodyGuidanceVersion {
		t.Fatalf("schema version consts not mirrored: %+v", props)
	}

	interview := resourcePayload(t, srv, resourceGenesisInterviewGuide)["interview"].(map[string]any)
	if interview["canonical_affirmation"] != canonicalGenesisAffirmation() {
		t.Fatalf("canonical affirmation = %q", interview["canonical_affirmation"])
	}
	stages := interview["stages"].([]any)
	if got, want := len(stages), 5; got != want {
		t.Fatalf("interview stages = %d, want %d", got, want)
	}
	for i, want := range []string{"identity", "philosophy", "discipline", "boundaries", "soul"} {
		stage := stages[i].(map[string]any)
		if stage["body"] != want || stage["read_back"] != true {
			t.Fatalf("stage %d = %+v, want %s/read_back", i, stage, want)
		}
	}

	rubric := resourcePayload(t, srv, resourceGenesisRubric)["rubric"].(map[string]any)
	refusalFloor := rubric["refusal_floor"].(map[string]any)
	if refusalFloor["min_items"] != float64(3) || !containsAnyString(refusalFloor["required_fields"], "closestSafePath") {
		t.Fatalf("refusal floor = %+v", refusalFloor)
	}

	playbook := resourcePayload(t, srv, resourceAgentSideGenesisPlaybook)["playbook"].(map[string]any)
	waitOnly := playbook["wait_only_processing_states"].(map[string]any)
	if waitOnly["next_tool"] != toolAgentGenesisRead || waitOnly["forbidden_next_tool"] != toolAgentGenesisAdvance || waitOnly["wait"] != true {
		t.Fatalf("wait-only processing guidance = %+v", waitOnly)
	}
	if !containsAnyString(waitOnly["states"], "in_progress") || !containsAnyString(waitOnly["states"], "declaration_extraction_pending") {
		t.Fatalf("wait-only processing states = %+v", waitOnly["states"])
	}
	if instruction := waitOnly["instruction"].(string); !strings.Contains(instruction, "Do not call "+toolAgentGenesisAdvance+" again") || !strings.Contains(instruction, "do not nudge") || !strings.Contains(instruction, "poll_after_seconds") {
		t.Fatalf("wait-only processing instruction = %q", instruction)
	}

	prompts := srv.Prompts().List()
	if got, want := promptNames(prompts), []string{promptDraftGenesisTurn, promptReviewSoulDraft}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt names = %v, want %v", got, want)
	}
	draftPrompt, err := srv.Prompts().Get(context.Background(), promptDraftGenesisTurn, json.RawMessage(`{"phase":"soul","current_status":"awaiting_owner","owner_intent":"finish refusal floor"}`))
	if err != nil {
		t.Fatalf("Prompts.Get draft: %v", err)
	}
	combined := promptText(draftPrompt)
	for _, want := range []string{fiveBodySchemaVersion, fiveBodyGuidanceVersion, "identity, philosophy, discipline, boundaries, and soul", canonicalGenesisAffirmation(), "ptah://genesis/genesis-interview-guide"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("draft prompt missing %q: %s", want, combined)
		}
	}

	reviewPrompt, err := srv.Prompts().Get(context.Background(), promptReviewSoulDraft, json.RawMessage(`{"draft":"identity/philosophy/discipline/boundaries/soul","focus":"refusal_floor"}`))
	if err != nil {
		t.Fatalf("Prompts.Get review: %v", err)
	}
	if text := promptText(reviewPrompt); !strings.Contains(text, "finding, refutation") || !strings.Contains(text, "closestSafePath") {
		t.Fatalf("review prompt missing rubric language: %s", text)
	}
}

func TestFiveBodyHostContractFixtureChecksumsAndVersions(t *testing.T) {
	meta := mustFiveBodyMetadata()
	if meta.SourceRepository != "equaltoai/lesser-host" || meta.SourcePullRequest != fiveBodyHostPR || meta.SourceHeadSHA != fiveBodyHostHeadSHA {
		t.Fatalf("metadata source = %+v", meta)
	}
	if meta.SchemaVersion != fiveBodySchemaVersion || meta.GuidanceVersion != fiveBodyGuidanceVersion {
		t.Fatalf("metadata versions = %+v", meta)
	}
	if got := sha256Hex(hostFiveBodyContractDoc); got != meta.ContractDocSHA256 {
		t.Fatalf("contract doc sha = %s, want %s", got, meta.ContractDocSHA256)
	}
	if got := sha256Hex(hostFiveBodySchemaJSON); got != meta.SchemaSHA256 {
		t.Fatalf("schema sha = %s, want %s", got, meta.SchemaSHA256)
	}
	if got := sha256Hex(hostFiveBodyExampleJSON); got != meta.ExampleSHA256 {
		t.Fatalf("example sha = %s, want %s", got, meta.ExampleSHA256)
	}

	var schema map[string]any
	if err := json.Unmarshal(hostFiveBodySchemaJSON, &schema); err != nil {
		t.Fatalf("schema fixture is invalid JSON: %v", err)
	}
	var example map[string]any
	if err := json.Unmarshal(hostFiveBodyExampleJSON, &example); err != nil {
		t.Fatalf("example fixture is invalid JSON: %v", err)
	}
	if example["schemaVersion"] != fiveBodySchemaVersion || example["guidanceVersion"] != fiveBodyGuidanceVersion {
		t.Fatalf("example versions = %s/%s", example["schemaVersion"], example["guidanceVersion"])
	}
	fiveBodies := example["fiveBodies"].(map[string]any)
	for _, body := range []string{"identity", "philosophy", "discipline", "boundaries", "soul"} {
		if _, ok := fiveBodies[body]; !ok {
			t.Fatalf("example missing fiveBodies.%s", body)
		}
	}

	// When the sibling lesser-host checkout is available in the delegated factory
	// workspace, compare the Host contract artifacts themselves. The checkout may
	// be on a later Host branch whose unrelated head moved while the PR #975
	// contract bytes stayed identical; only artifact drift should break Body's
	// mirror guard. Explicit env-provided Host dirs are stricter and must contain
	// every mirrored artifact.
	hostRoot, requireAllArtifacts, ok := findHostContractDir(t)
	if !ok {
		return
	}
	if err := verifyHostContractArtifacts(hostRoot, requireAllArtifacts, meta); err != nil {
		t.Fatal(err)
	}
}

func TestFiveBodyHostContractArtifactGuardAcceptsMatchingExplicitDir(t *testing.T) {
	dir := t.TempDir()
	writeHostContractArtifact(t, dir, "soul-five-body-schema.md", hostFiveBodyContractDoc)
	writeHostContractArtifact(t, dir, "soul-five-body.schema.v2.json", hostFiveBodySchemaJSON)
	writeHostContractArtifact(t, dir, "soul-five-body.example.v2.json", hostFiveBodyExampleJSON)

	if err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata()); err != nil {
		t.Fatalf("verifyHostContractArtifacts: %v", err)
	}
}

func TestFiveBodyHostContractArtifactGuardRejectsExplicitMissingArtifact(t *testing.T) {
	dir := t.TempDir()

	err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata())
	if err == nil || !strings.Contains(err.Error(), "missing required Host artifact soul-five-body-schema.md") {
		t.Fatalf("verifyHostContractArtifacts missing artifact error = %v", err)
	}
}

func TestFiveBodyHostContractArtifactGuardRejectsArtifactDrift(t *testing.T) {
	dir := t.TempDir()
	writeHostContractArtifact(t, dir, "soul-five-body-schema.md", []byte("changed host contract"))
	writeHostContractArtifact(t, dir, "soul-five-body.schema.v2.json", hostFiveBodySchemaJSON)
	writeHostContractArtifact(t, dir, "soul-five-body.example.v2.json", hostFiveBodyExampleJSON)

	err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata())
	if err == nil || !strings.Contains(err.Error(), "sync Host PR #975 before merge") {
		t.Fatalf("verifyHostContractArtifacts drift error = %v", err)
	}
}

func verifyHostContractArtifacts(contractsDir string, requireAllArtifacts bool, meta fiveBodyContractMetadata) error {
	for _, tc := range []struct {
		name string
		want string
		data []byte
	}{
		{name: "soul-five-body-schema.md", want: meta.ContractDocSHA256, data: hostFiveBodyContractDoc},
		{name: "soul-five-body.schema.v2.json", want: meta.SchemaSHA256, data: hostFiveBodySchemaJSON},
		{name: "soul-five-body.example.v2.json", want: meta.ExampleSHA256, data: hostFiveBodyExampleJSON},
	} {
		live, err := os.ReadFile(filepath.Join(contractsDir, tc.name))
		if os.IsNotExist(err) {
			if requireAllArtifacts {
				return fmt.Errorf("Host contracts dir %s missing required Host artifact %s; set LESSER_HOST_CONTRACTS_DIR/LESSER_HOST_ROOT to a checkout or directory containing Host PR #%d contract artifacts", contractsDir, tc.name, fiveBodyHostPR)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read Host artifact %s: %w", tc.name, err)
		}
		if got := sha256Hex(live); got != tc.want {
			return fmt.Errorf("Host artifact %s sha = %s, Body fixture/metadata want %s; sync Host PR #%d before merge", tc.name, got, tc.want, fiveBodyHostPR)
		}
		if string(live) != string(tc.data) {
			return fmt.Errorf("Host artifact %s differs byte-for-byte from Body mirror", tc.name)
		}
	}
	return nil
}

func writeHostContractArtifact(t *testing.T, dir string, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write host contract artifact %s: %v", name, err)
	}
}

func findHostContractDir(t *testing.T) (contractsDir string, requireAllArtifacts bool, ok bool) {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("LESSER_HOST_CONTRACTS_DIR")); dir != "" {
		contractsDir = filepath.Clean(dir)
		mustExistDir(t, "LESSER_HOST_CONTRACTS_DIR", contractsDir)
		return contractsDir, true, true
	}
	if root := strings.TrimSpace(os.Getenv("LESSER_HOST_ROOT")); root != "" {
		hostRepoRoot := filepath.Clean(root)
		contractsDir = filepath.Join(hostRepoRoot, "docs", "contracts")
		mustExistDir(t, "LESSER_HOST_ROOT docs/contracts", contractsDir)
		return contractsDir, true, true
	}

	bodyRoot, ok := findBodyRepoRoot(t)
	if !ok {
		return "", false, false
	}
	hostRepoRoot := filepath.Join(filepath.Dir(bodyRoot), "lesser-host")
	contractsDir = filepath.Join(hostRepoRoot, "docs", "contracts")
	if _, err := os.Stat(contractsDir); os.IsNotExist(err) {
		return "", false, false
	} else if err != nil {
		t.Fatalf("stat sibling Host contracts dir %s: %v", contractsDir, err)
	}
	return contractsDir, false, true
}

func mustExistDir(t *testing.T, label string, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s %s: %v", label, dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s %s is not a directory", label, dir)
	}
}

func findBodyRepoRoot(t *testing.T) (string, bool) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func TestAgentGenesisListReturnsHostBackedRecoveryIndex(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	const agentID = "0xabc"
	fake := &fakeGenesisClient{listResponse: map[string]any{
		"conversations": []any{
			map[string]any{
				"registration_id":       "reg-terminal",
				"conversation_id":       "conv-terminal",
				"status":                "published",
				"latest_turn_id":        "turn-terminal",
				"message_count":         float64(4),
				"created_at":            "2026-07-19T14:00:00Z",
				"updated_at":            "2026-07-19T14:06:00Z",
				"messages":              []any{map[string]any{"content": "private terminal transcript"}},
				"produced_declarations": map[string]any{"declaration": "private declaration"},
			},
			map[string]any{
				"registration_id":       "reg-failed",
				"conversation_id":       "conv-failed",
				"status":                "failed",
				"latest_turn_id":        "turn-failed",
				"message_count":         float64(3),
				"created_at":            "2026-07-19T14:00:00Z",
				"updated_at":            "2026-07-19T14:05:00Z",
				"messages":              []any{map[string]any{"content": "private failed transcript"}},
				"produced_declarations": map[string]any{"declaration": "private failed declaration"},
				"failure":               map[string]any{"recovery": map[string]any{"action": "restart_soul_bootstrap"}},
			},
			map[string]any{
				"registration_id": "reg-wait",
				"conversation_id": "conv-wait",
				"status":          "in_progress",
				"latest_turn_id":  "turn-wait",
				"message_count":   float64(2),
				"created_at":      "2026-07-19T14:00:00Z",
				"updated_at":      "2026-07-19T14:04:00Z",
			},
			map[string]any{
				"registration_id": "reg-owner",
				"conversation_id": "conv-owner",
				"status":          "assistant_turn_ready",
				"latest_turn_id":  "turn-owner",
				"message_count":   float64(2),
				"created_at":      "2026-07-19T14:00:00Z",
				"updated_at":      "2026-07-19T14:03:00Z",
			},
		},
	}}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	result, err := registry.Call(operatorToolContext("owner", []string{"read"}, "owner-oauth-bearer-test-only"), toolAgentGenesisList, json.RawMessage(`{"agent_id":"`+agentID+`","limit":5}`))
	if err != nil {
		t.Fatalf("agent_genesis_list: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("agent_genesis_list returned error: %+v", result)
	}
	if strings.Join(fake.calls, ",") != "list:"+agentID || fake.listAgentID != agentID || fake.listLimit != 5 || fake.bearer != "host-instance-key-test-only" {
		t.Fatalf("agent_genesis_list Host call = calls %v agent %q limit %d bearer %q", fake.calls, fake.listAgentID, fake.listLimit, fake.bearer)
	}
	data := structuredGenesisData(t, result)
	if data["operation"] != "list" || data["status"] != "ok" || data["agent_id"] != agentID {
		t.Fatalf("list data = %+v", data)
	}
	conversations := data["conversations"].([]map[string]any)
	if len(conversations) != 4 {
		t.Fatalf("conversations = %+v", conversations)
	}
	start := data["recommended_start"].(map[string]any)
	if start["registration_id"] != "reg-failed" || start["conversation_id"] != "conv-failed" || start["recommended_next_tool"] != toolAgentGenesisRead {
		t.Fatalf("recommended_start = %+v, want newest non-terminal failed lane directed to read", start)
	}
	args := start["recommended_arguments"].(map[string]any)
	if args["registration_id"] != "reg-failed" || args["conversation_id"] != "conv-failed" {
		t.Fatalf("recommended_start args = %+v", args)
	}
	failed := conversations[1]
	if failed["recommended_next_tool"] != toolAgentGenesisRead || failed["recoverable_hint"] != "unknown_until_read" || failed["restart_hint"] != "unknown_until_read" || !strings.Contains(failed["instruction"].(string), "do not guess") {
		t.Fatalf("failed guidance = %+v", failed)
	}
	waiting := conversations[2]
	if waiting["recommended_next_tool"] != toolAgentGenesisRead || waiting["wait"] != true || waiting["forbidden_next_tool"] != toolAgentGenesisAdvance || !strings.Contains(waiting["instruction"].(string), "does not include exact poll timing") {
		t.Fatalf("waiting guidance = %+v", waiting)
	}
	owner := conversations[3]
	if owner["recommended_next_tool"] != toolAgentGenesisRead || owner["alternate_next_tool"] != toolAgentGenesisAdvance || !strings.Contains(owner["instruction"].(string), "load the bounded latest prompt/context") {
		t.Fatalf("owner-input guidance = %+v", owner)
	}
	terminal := conversations[0]
	if terminal["terminal"] != true || terminal["recommended_next_tool"] != toolAgentGet || terminal["alternate_next_tool"] != toolAgentList {
		t.Fatalf("terminal guidance = %+v", terminal)
	}
	encoded := mustMarshalGenesisResult(t, result)
	for _, forbidden := range []string{"messages", "produced_declarations", "private terminal transcript", "private failed transcript", "private declaration"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("agent_genesis_list leaked %q in output: %s", forbidden, encoded)
		}
	}
	guidance := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisRead || !strings.Contains(guidance["instruction"].(string), "start with agent_genesis_list") || !strings.Contains(guidance["instruction"].(string), "recommended_start") {
		t.Fatalf("guidance = %+v", guidance)
	}
}

func TestAgentGenesisListRequiresOwnerOperatorReadAuthority(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(&fakeGenesisClient{})); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ordinary, err := registry.Call(toolContext("owner", []string{"read"}, "ordinary-oauth-bearer"), toolAgentGenesisList, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ordinary call: %v", err)
	}
	assertToolError(t, ordinary, "owner_operator_required", 403)
	noRead, err := registry.Call(operatorToolContext("owner", []string{"follow"}, "operator-oauth-bearer"), toolAgentGenesisList, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("no read call: %v", err)
	}
	assertToolError(t, noRead, "insufficient_scope", 403)
}

func TestGenesisOutputSchemasDeclareStatusAndFailureEnums(t *testing.T) {
	assertSchemaContainsEnum(t, genesisOutputSchema(), "not_available")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "declaration_extraction_pending")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "producer_contract_missing")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "microvm_unavailable")
	var schema map[string]any
	if err := json.Unmarshal(genesisOutputSchema(), &schema); err != nil {
		t.Fatalf("genesis output schema invalid JSON: %v", err)
	}
	dataSchema := schema["properties"].(map[string]any)["data"].(map[string]any)
	properties := dataSchema["properties"].(map[string]any)
	topLevelFailureSchema := properties["failure"].(map[string]any)
	conversationSchema := properties["conversation"].(map[string]any)
	nestedFailureSchema := conversationSchema["properties"].(map[string]any)["failure"].(map[string]any)
	for name, failureSchema := range map[string]map[string]any{
		"top_level":           topLevelFailureSchema,
		"nested_conversation": nestedFailureSchema,
	} {
		recoverySchema := failureSchema["properties"].(map[string]any)["recovery"].(map[string]any)
		actionSchema := recoverySchema["properties"].(map[string]any)["action"].(map[string]any)
		actionEnum := actionSchema["enum"].([]any)
		if got, want := actionEnum, []any{"refresh_state", "retry_same_step", "restart_soul_bootstrap", "operator_action"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s genesis recovery action enum = %v, want exact Host vocabulary %v", name, got, want)
		}
		for field, bounds := range map[string]map[string]any{
			"max_attempts":        {"minimum": float64(0), "maximum": float64(10)},
			"retry_after_seconds": {"minimum": float64(0), "maximum": float64(3600)},
		} {
			fieldSchema := recoverySchema["properties"].(map[string]any)[field].(map[string]any)
			for bound, want := range bounds {
				if fieldSchema[bound] != want {
					t.Fatalf("%s recovery schema %s.%s = %v, want %v", name, field, bound, fieldSchema[bound], want)
				}
			}
		}
	}
	for _, wantField := range []string{"forbidden_next_tool", "wait", "poll_after_seconds", "expected_wait_seconds"} {
		if !strings.Contains(string(genesisOutputSchema()), wantField) {
			t.Fatalf("genesis output schema missing guidance field %q: %s", wantField, string(genesisOutputSchema()))
		}
	}
	assertSchemaContainsEnum(t, agentGenesisListDef().OutputSchema, "ok")
	for _, wantField := range []string{"recommended_start", "recommended_next_tool", "recommended_arguments", "terminal", "recoverable_hint", "restart_hint"} {
		if !strings.Contains(string(agentGenesisListDef().OutputSchema), wantField) {
			t.Fatalf("agent_genesis_list output schema missing field %q: %s", wantField, string(agentGenesisListDef().OutputSchema))
		}
	}
}

func TestGenesisSanitizesFiveBodyContractEvidence(t *testing.T) {
	safe := sanitizeGenesisProducedDeclarations(map[string]any{
		"schemaVersion":   fiveBodySchemaVersion,
		"guidanceVersion": fiveBodyGuidanceVersion,
		"declaration":     "private declaration must not be returned",
		"fiveBodies":      map[string]any{"identity": map[string]any{"summary": "private body"}},
		"adversarialReview": map[string]any{
			"version":  "hosted-genesis-adversarial-review.v1",
			"reviewer": "hosted-genesis-deterministic-review.v1",
			"result":   "pass",
			"report":   "private review report must not be returned",
		},
		"evidence": map[string]any{
			"source":          "hosted_genesis",
			"model":           "openai:gpt-test",
			"schemaVersion":   fiveBodySchemaVersion,
			"guidanceVersion": fiveBodyGuidanceVersion,
		},
	})
	if safe["schema_version"] != fiveBodySchemaVersion || safe["guidance_version"] != fiveBodyGuidanceVersion {
		t.Fatalf("versions not surfaced safely: %+v", safe)
	}
	review := safe["adversarial_review"].(map[string]any)
	if review["result"] != "pass" || review["report"] != nil {
		t.Fatalf("review not sanitized: %+v", review)
	}
	evidence := safe["evidence"].(map[string]any)
	if evidence["schema_version"] != fiveBodySchemaVersion || evidence["guidance_version"] != fiveBodyGuidanceVersion || evidence["model"] != "openai:gpt-test" {
		t.Fatalf("evidence versions = %+v", evidence)
	}
	encoded, _ := json.Marshal(safe)
	for _, forbidden := range []string{"private declaration", "private body", "private review report", "fiveBodies"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("sanitized produced_declarations leaked %q: %s", forbidden, encoded)
		}
	}
}

func resourceNames(defs []mcpruntime.ResourceDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func promptNames(defs []mcpruntime.PromptDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func resourcePayload(t *testing.T, srv *mcpruntime.Server, name string) map[string]any {
	t.Helper()
	contents, err := srv.Resources().Read(context.Background(), fiveBodyResourceURI(name))
	if err != nil {
		t.Fatalf("read resource %s: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(contents[0].Text), &out); err != nil {
		t.Fatalf("decode resource %s: %v", name, err)
	}
	return out
}

func promptText(result *mcpruntime.PromptResult) string {
	if result == nil {
		return ""
	}
	var combined strings.Builder
	for _, msg := range result.Messages {
		combined.WriteString(msg.Content.Text)
		combined.WriteByte('\n')
	}
	return combined.String()
}

func containsAnyString(raw any, want string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertSchemaContainsEnum(t *testing.T, schema json.RawMessage, want string) {
	t.Helper()
	var raw any
	if err := json.Unmarshal(schema, &raw); err != nil {
		t.Fatalf("schema invalid JSON: %v", err)
	}
	if !jsonTreeContainsString(raw, want) {
		t.Fatalf("schema does not contain enum value %q: %s", want, string(schema))
	}
}

func jsonTreeContainsString(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if jsonTreeContainsString(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if jsonTreeContainsString(item, want) {
				return true
			}
		}
	}
	return false
}
