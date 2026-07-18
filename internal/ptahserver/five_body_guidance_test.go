package ptahserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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
	if got, want := resourceNames(resources), []string{resourceSoulSchemaV2, resourceGenesisInterviewGuide, resourceAgentSideGenesisPlaybook, resourceGenesisRubric}; !reflect.DeepEqual(got, want) {
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
	// workspace, compare against the exact Host PR #928 artifacts. This makes a
	// Host PR update visibly break the Body mirror instead of silently forking it.
	hostRoot, hostRepoRoot, ok := findHostContractDir(t)
	if !ok {
		return
	}
	if hostRepoRoot != "" {
		assertHostCheckoutHead(t, hostRepoRoot, fiveBodyHostHeadSHA)
	}
	for _, tc := range []struct {
		name string
		want string
		data []byte
	}{
		{name: "soul-five-body-schema.md", want: meta.ContractDocSHA256, data: hostFiveBodyContractDoc},
		{name: "soul-five-body.schema.v2.json", want: meta.SchemaSHA256, data: hostFiveBodySchemaJSON},
		{name: "soul-five-body.example.v2.json", want: meta.ExampleSHA256, data: hostFiveBodyExampleJSON},
	} {
		live, err := os.ReadFile(filepath.Join(hostRoot, tc.name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read sibling Host artifact %s: %v", tc.name, err)
		}
		if got := sha256Hex(live); got != tc.want {
			t.Fatalf("sibling Host artifact %s sha = %s, Body fixture/metadata want %s; sync Host PR #928 before merge", tc.name, got, tc.want)
		}
		if string(live) != string(tc.data) {
			t.Fatalf("sibling Host artifact %s differs byte-for-byte from Body mirror", tc.name)
		}
	}
}

func findHostContractDir(t *testing.T) (contractsDir string, hostRepoRoot string, ok bool) {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("LESSER_HOST_CONTRACTS_DIR")); dir != "" {
		contractsDir = filepath.Clean(dir)
		mustExistDir(t, "LESSER_HOST_CONTRACTS_DIR", contractsDir)
		return contractsDir, hostRepoRootForContractsDir(contractsDir), true
	}
	if root := strings.TrimSpace(os.Getenv("LESSER_HOST_ROOT")); root != "" {
		hostRepoRoot = filepath.Clean(root)
		contractsDir = filepath.Join(hostRepoRoot, "docs", "contracts")
		mustExistDir(t, "LESSER_HOST_ROOT docs/contracts", contractsDir)
		return contractsDir, hostRepoRoot, true
	}

	bodyRoot, ok := findBodyRepoRoot(t)
	if !ok {
		return "", "", false
	}
	hostRepoRoot = filepath.Join(filepath.Dir(bodyRoot), "lesser-host")
	contractsDir = filepath.Join(hostRepoRoot, "docs", "contracts")
	if _, err := os.Stat(contractsDir); os.IsNotExist(err) {
		return "", "", false
	} else if err != nil {
		t.Fatalf("stat sibling Host contracts dir %s: %v", contractsDir, err)
	}
	return contractsDir, hostRepoRoot, true
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

func hostRepoRootForContractsDir(contractsDir string) string {
	contractsDir = filepath.Clean(contractsDir)
	if filepath.Base(contractsDir) == "contracts" && filepath.Base(filepath.Dir(contractsDir)) == "docs" {
		return filepath.Dir(filepath.Dir(contractsDir))
	}
	return ""
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

func assertHostCheckoutHead(t *testing.T, hostRepoRoot string, want string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(hostRepoRoot, ".git")); os.IsNotExist(err) {
		return
	} else if err != nil {
		t.Fatalf("stat sibling Host .git in %s: %v", hostRepoRoot, err)
	}
	out, err := exec.Command("git", "-C", hostRepoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read sibling Host checkout head in %s: %v", hostRepoRoot, err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("sibling Host checkout head = %s, want %s; sync Body fixtures against the requested Host PR #928 head", got, want)
	}
}

func TestAgentGenesisListReturnsSafeProducerContractMissing(t *testing.T) {
	fake := &fakeGenesisClient{}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	result, err := registry.Call(operatorToolContext("owner", []string{"read"}, "owner-oauth-bearer-test-only"), toolAgentGenesisList, json.RawMessage(`{"agent_id":"agent-0xabc","limit":5}`))
	if err != nil {
		t.Fatalf("agent_genesis_list: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("agent_genesis_list returned error: %+v", result)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("agent_genesis_list must not call Host without a checked list client surface: %v", fake.calls)
	}
	data := structuredGenesisData(t, result)
	if data["operation"] != "list" || data["status"] != "not_available" {
		t.Fatalf("list data = %+v", data)
	}
	failure := data["failure"].(map[string]any)
	if failure["code"] != "producer_contract_missing" || !strings.Contains(failure["reason"].(string), "will not fabricate") {
		t.Fatalf("failure = %+v", failure)
	}
	producer := data["producer_contract"].(map[string]any)
	if producer["host_pr"] != fiveBodyHostPR || producer["host_head_sha"] != fiveBodyHostHeadSHA || producer["schema_version"] != fiveBodySchemaVersion {
		t.Fatalf("producer contract = %+v", producer)
	}
	guidance := data["guidance"].(map[string]any)
	if guidance["next_tool"] != toolAgentGenesisRead || guidance["alternate_next_tool"] != toolAgentList {
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
	assertSchemaContainsEnum(t, genesisOutputSchema(), "producer_contract_missing")
	assertSchemaContainsEnum(t, genesisOutputSchema(), "restart_soul_bootstrap")
	assertSchemaContainsEnum(t, agentGenesisListDef().OutputSchema, "not_available")
	assertSchemaContainsEnum(t, agentGenesisListDef().OutputSchema, "producer_contract_missing")
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
