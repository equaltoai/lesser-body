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

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
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
	reviewProtocol := interview["review_protocol"].(map[string]any)
	if !strings.Contains(reviewProtocol["authority"].(string), "zero authority") || !strings.Contains(reviewProtocol["affirm"].(string), "no section") || !strings.Contains(reviewProtocol["edit"].(string), "exact section") {
		t.Fatalf("review protocol = %+v", reviewProtocol)
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
	if !containsAnyString(waitOnly["states"], "in_progress") {
		t.Fatalf("wait-only processing states = %+v", waitOnly["states"])
	}
	if instruction := waitOnly["instruction"].(string); !strings.Contains(instruction, "Do not call "+toolAgentGenesisAdvance+" again") || !strings.Contains(instruction, "do not nudge") || !strings.Contains(instruction, "poll_after_seconds") {
		t.Fatalf("wait-only processing instruction = %q", instruction)
	}

	prompts := srv.Prompts().List()
	if got, want := promptNames(prompts), []string{promptDraftGenesisTurn, promptReviewGenesisCandidate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt names = %v, want %v", got, want)
	}
	draftPrompt, err := srv.Prompts().Get(context.Background(), promptDraftGenesisTurn, json.RawMessage(`{"phase":"soul","current_status":"assistant_turn_ready","owner_intent":"finish refusal floor"}`))
	if err != nil {
		t.Fatalf("Prompts.Get draft: %v", err)
	}
	combined := promptText(draftPrompt)
	for _, want := range []string{fiveBodySchemaVersion, fiveBodyGuidanceVersion, "identity, philosophy, discipline, boundaries, and soul", "structural candidate_action", "ptah://genesis/genesis-interview-guide"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("draft prompt missing %q: %s", want, combined)
		}
	}

	reviewPrompt, err := srv.Prompts().Get(context.Background(), promptReviewGenesisCandidate, json.RawMessage(`{"review_text":"identity/philosophy/discipline/boundaries/soul","candidate_revision":"7","candidate_hash":"sha256:aaa","review_hash":"sha256:bbb","owner_intent":"edit boundaries"}`))
	if err != nil {
		t.Fatalf("Prompts.Get review: %v", err)
	}
	if text := promptText(reviewPrompt); !strings.Contains(text, "action=affirm") || !strings.Contains(text, "action=edit") || !strings.Contains(text, "zero authority") || !strings.Contains(text, "identity/philosophy") {
		t.Fatalf("review prompt missing structural protocol: %s", text)
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
	if len(meta.Artifacts) != 11 {
		t.Fatalf("mirrored artifact count = %d, want 11", len(meta.Artifacts))
	}
	for _, artifact := range meta.Artifacts {
		mirrored, err := hostContractMirrorFS.ReadFile("testdata/host-contract/pr-978/" + artifact.MirrorFile)
		if err != nil {
			t.Fatalf("read mirrored %s: %v", artifact.MirrorFile, err)
		}
		if got := sha256Hex(mirrored); got != artifact.SHA256 {
			t.Fatalf("mirror %s sha = %s, want %s", artifact.MirrorFile, got, artifact.SHA256)
		}
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
	// be on a later Host branch whose unrelated head moved while the PR #980
	// contract bytes stayed identical; only artifact drift should break Body's
	// mirror guard. Explicit env-provided Host dirs are stricter and must contain
	// every mirrored artifact.
	hostRoot, requireAllArtifacts, ok := findHostContractRoot(t)
	if !ok {
		return
	}
	if err := verifyHostContractArtifacts(hostRoot, requireAllArtifacts, meta); err != nil {
		t.Fatal(err)
	}
}

func TestFiveBodyHostContractArtifactGuardAcceptsMatchingExplicitDir(t *testing.T) {
	dir := t.TempDir()
	writeAllHostContractArtifacts(t, dir, mustFiveBodyMetadata())

	if err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata()); err != nil {
		t.Fatalf("verifyHostContractArtifacts: %v", err)
	}
}

func TestFiveBodyHostContractArtifactGuardRejectsExplicitMissingArtifact(t *testing.T) {
	dir := t.TempDir()

	err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata())
	if err == nil || !strings.Contains(err.Error(), "missing required Host artifact docs/contracts/soul-five-body-schema.md") {
		t.Fatalf("verifyHostContractArtifacts missing artifact error = %v", err)
	}
}

func TestFiveBodyHostContractArtifactGuardRejectsArtifactDrift(t *testing.T) {
	dir := t.TempDir()
	writeAllHostContractArtifacts(t, dir, mustFiveBodyMetadata())
	writeHostContractArtifact(t, dir, "docs/contracts/soul-five-body-schema.md", []byte("changed host contract"))

	err := verifyHostContractArtifacts(dir, true, mustFiveBodyMetadata())
	if err == nil || !strings.Contains(err.Error(), "sync Host PR #980 before merge") {
		t.Fatalf("verifyHostContractArtifacts drift error = %v", err)
	}
}

func verifyHostContractArtifacts(hostRoot string, requireAllArtifacts bool, meta fiveBodyContractMetadata) error {
	for _, artifact := range meta.Artifacts {
		mirror, err := hostContractMirrorFS.ReadFile("testdata/host-contract/pr-978/" + artifact.MirrorFile)
		if err != nil {
			return fmt.Errorf("read Body mirror %s: %w", artifact.MirrorFile, err)
		}
		live, err := os.ReadFile(filepath.Join(hostRoot, filepath.FromSlash(artifact.SourcePath)))
		if os.IsNotExist(err) {
			if requireAllArtifacts {
				return fmt.Errorf("Host root %s missing required Host artifact %s; set LESSER_HOST_ROOT to a checkout containing Host PR #%d artifacts", hostRoot, artifact.SourcePath, fiveBodyHostPR)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read Host artifact %s: %w", artifact.SourcePath, err)
		}
		if got := sha256Hex(live); got != artifact.SHA256 {
			return fmt.Errorf("Host artifact %s sha = %s, Body fixture/metadata want %s; sync Host PR #%d before merge", artifact.SourcePath, got, artifact.SHA256, fiveBodyHostPR)
		}
		if string(live) != string(mirror) {
			return fmt.Errorf("Host artifact %s differs byte-for-byte from Body mirror", artifact.SourcePath)
		}
	}
	return nil
}

func writeHostContractArtifact(t *testing.T, dir string, name string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir host contract artifact %s: %v", name, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write host contract artifact %s: %v", name, err)
	}
}

func writeAllHostContractArtifacts(t *testing.T, dir string, meta fiveBodyContractMetadata) {
	t.Helper()
	for _, artifact := range meta.Artifacts {
		data, err := hostContractMirrorFS.ReadFile("testdata/host-contract/pr-978/" + artifact.MirrorFile)
		if err != nil {
			t.Fatalf("read mirror %s: %v", artifact.MirrorFile, err)
		}
		writeHostContractArtifact(t, dir, artifact.SourcePath, data)
	}
}

func findHostContractRoot(t *testing.T) (hostRoot string, requireAllArtifacts bool, ok bool) {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("LESSER_HOST_CONTRACTS_DIR")); dir != "" {
		contractsDir := filepath.Clean(dir)
		mustExistDir(t, "LESSER_HOST_CONTRACTS_DIR", contractsDir)
		return filepath.Dir(filepath.Dir(contractsDir)), true, true
	}
	if root := strings.TrimSpace(os.Getenv("LESSER_HOST_ROOT")); root != "" {
		hostRoot = filepath.Clean(root)
		mustExistDir(t, "LESSER_HOST_ROOT", hostRoot)
		return hostRoot, true, true
	}

	bodyRoot, ok := findBodyRepoRoot(t)
	if !ok {
		return "", false, false
	}
	hostRepoRoot := filepath.Join(filepath.Dir(bodyRoot), "lesser-host")
	if _, err := os.Stat(hostRepoRoot); os.IsNotExist(err) {
		return "", false, false
	} else if err != nil {
		t.Fatalf("stat sibling Host root %s: %v", hostRepoRoot, err)
	}
	return hostRepoRoot, true, true
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
	if owner["recommended_next_tool"] != toolAgentGenesisRead || owner["alternate_next_tool"] != toolAgentGenesisAdvance || !strings.Contains(owner["instruction"].(string), "load candidate phase and exact review bindings") {
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
	assertSchemaContainsEnum(t, genesisOutputSchema(), "created")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "assistant_turn_ready")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "declaration_ready")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "published")
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
	for _, wantField := range []string{"forbidden_next_tool", "wait", "poll_after_seconds", "expected_wait_seconds", "declaration_candidate", "review_text", "candidate_revision", "candidate_hash", "review_hash"} {
		if !strings.Contains(string(genesisOutputSchema()), wantField) {
			t.Fatalf("genesis output schema missing guidance field %q: %s", wantField, string(genesisOutputSchema()))
		}
	}
	guidanceSchema := properties["guidance"].(map[string]any)
	guidanceProperties := guidanceSchema["properties"].(map[string]any)
	advertisedActionsSchema, ok := guidanceProperties["candidate_actions"].(map[string]any)
	if !ok || advertisedActionsSchema["minItems"] != float64(6) || advertisedActionsSchema["maxItems"] != float64(6) {
		t.Fatalf("genesis candidate_actions output schema = %#v, want exactly six directly callable actions", advertisedActionsSchema)
	}
	advertisedActionSchema := advertisedActionsSchema["items"].(map[string]any)
	advertisedActionProperties := advertisedActionSchema["properties"].(map[string]any)
	candidateActionSchema := advertisedActionProperties["candidate_action"].(map[string]any)
	if candidateActionSchema["additionalProperties"] != false {
		t.Fatalf("advertised candidate_action schema must fail closed on unknown keys: %#v", candidateActionSchema)
	}
	if got, want := candidateActionSchema["required"], []any{"action", "candidate_revision", "candidate_hash", "review_hash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised candidate_action required fields = %#v, want %#v", got, want)
	}
	if _, ok := candidateActionSchema["allOf"].([]any); !ok {
		t.Fatalf("advertised candidate_action schema does not describe affirm/edit section conditionality: %#v", candidateActionSchema)
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

func TestGenesisCurrentProtocolSurfacesContainNoRemovedCompatibilityLane(t *testing.T) {
	tools := mcpruntime.NewToolRegistry()
	if err := RegisterTools(tools); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	var active strings.Builder
	for _, def := range tools.List() {
		active.WriteString(def.Name)
		active.WriteString(def.Description)
		active.Write(def.InputSchema)
		active.Write(def.OutputSchema)
	}

	srv := mcpruntime.NewServer("ptah-test", "dev", mcpruntime.WithCapabilityConfig(mcpruntime.CapabilityConfig{Tools: true, Resources: true, Prompts: true}))
	if err := RegisterResources(srv); err != nil {
		t.Fatalf("RegisterResources: %v", err)
	}
	if err := RegisterPrompts(srv); err != nil {
		t.Fatalf("RegisterPrompts: %v", err)
	}
	for _, def := range srv.Resources().List() {
		contents, err := srv.Resources().Read(context.Background(), def.URI)
		if err != nil {
			t.Fatalf("Read(%s): %v", def.URI, err)
		}
		for _, content := range contents {
			active.WriteString(content.Text)
		}
	}
	for _, file := range genesisSkillFiles() {
		active.WriteString(file.content)
	}
	for name, args := range map[string]json.RawMessage{
		promptDraftGenesisTurn:       json.RawMessage(`{"phase":"review","current_status":"assistant_turn_ready"}`),
		promptReviewGenesisCandidate: json.RawMessage(`{"review_text":"exact review","candidate_revision":"1","candidate_hash":"sha256:a","review_hash":"sha256:b"}`),
	} {
		result, err := srv.Prompts().Get(context.Background(), name, args)
		if err != nil {
			t.Fatalf("Prompt(%s): %v", name, err)
		}
		active.WriteString(promptText(result))
	}
	bodyRoot, ok := findBodyRepoRoot(t)
	if !ok {
		t.Fatal("Body repo root unavailable")
	}
	doc, err := os.ReadFile(filepath.Join(bodyRoot, "docs", "mcp.md"))
	if err != nil {
		t.Fatalf("read docs/mcp.md: %v", err)
	}
	active.Write(doc)

	for _, forbidden := range []string{
		"agent_genesis_" + "complete",
		"declaration_" + "extraction_pending",
		"declaration_" + "extraction_failed",
		"awaiting_" + "owner",
		"needs_" + "owner_turn",
		"ready_for_" + "completion",
		"review-" + "soul-draft",
		"Do you affirm this " + "declaration",
	} {
		if strings.Contains(active.String(), forbidden) {
			t.Fatalf("removed compatibility surface %q is still active", forbidden)
		}
	}
	registered := strings.Join(toolDefNames(tools.List()), ",")
	for _, privateTool := range []string{"declaration_identity_put", "declaration_philosophy_put", "declaration_discipline_put", "declaration_boundaries_put", "declaration_soul_put"} {
		if strings.Contains(registered, privateTool) {
			t.Fatalf("Host-private provider tool %q was registered on Ptah: %s", privateTool, registered)
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
