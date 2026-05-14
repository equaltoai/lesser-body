package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillBundleGetSchemaAvoidsTopLevelComposition(t *testing.T) {
	def := skillBundleGetDef()
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal skill_bundle_get schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("skill_bundle_get schema type: want object got %#v", schema["type"])
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf", "enum", "not"} {
		if _, ok := schema[keyword]; ok {
			t.Fatalf("skill_bundle_get schema must not use top-level %s for MCP client compatibility: %+v", keyword, schema)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties["skill_id"] == nil || properties["revision_number"] == nil || properties["bundle_id"] == nil {
		t.Fatalf("skill_bundle_get schema should advertise both selector forms in properties, got %+v", properties)
	}
}

func TestSkillBundleGetStillRequiresSelectorAtRuntime(t *testing.T) {
	_, err := handleSkillBundleGet(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected missing selector to fail at runtime")
	}
	if !strings.Contains(err.Error(), "skill_id is required unless bundle_id is provided") {
		t.Fatalf("unexpected missing selector error: %v", err)
	}
}

func TestSkillBundleVerificationStates(t *testing.T) {
	entryDigest := skillSHA256Digest([]byte("# Skill\n"))
	bundle := map[string]any{
		"digests": map[string]any{
			"bundle_digest":      "sha256:bundle",
			"publication_digest": "sha256:publication",
		},
		"files": []any{
			map[string]any{
				"path":             "SKILL.md",
				"install_path":     "example/SKILL.md",
				"digest":           entryDigest,
				"content_included": false,
			},
		},
	}

	unknown := verifySkillBundleLocalState(bundle, nil, false)
	if unknown.State != skillInstallStateUnknownLocal || unknown.CheckedFiles != 0 {
		t.Fatalf("omitted local_files should be unknown, got %+v", unknown)
	}

	notInstalled := verifySkillBundleLocalState(bundle, []skillLocalFileObservation{}, true)
	if notInstalled.State != skillInstallStateNotInstalled || len(notInstalled.Files) != 1 || notInstalled.Files[0].State != skillInstallStateNotInstalled {
		t.Fatalf("empty local_files should report not_installed, got %+v", notInstalled)
	}

	verified := verifySkillBundleLocalState(bundle, []skillLocalFileObservation{{Path: "SKILL.md", Content: "# Skill\n", HasContent: true}}, true)
	if verified.State != skillInstallStateVerifiedMatch || verified.CheckedFiles != 1 || len(verified.Files) != 1 || !verified.Files[0].ContentCompared {
		t.Fatalf("matching local bytes should verify, got %+v", verified)
	}
	if verified.Files[0].ComputedDigest != entryDigest {
		t.Fatalf("computed digest got %q want %q", verified.Files[0].ComputedDigest, entryDigest)
	}

	modified := verifySkillBundleLocalState(bundle, []skillLocalFileObservation{{InstallPath: "example/SKILL.md", Content: "changed", HasContent: true}}, true)
	if modified.State != skillInstallStateModifiedCopy || len(modified.Files) != 1 || modified.Files[0].State != skillInstallStateModifiedCopy {
		t.Fatalf("mismatched local bytes should report modified copy, got %+v", modified)
	}
}

func TestSkillBundleContentSummaryDistinguishesInlineAndMetadataOnly(t *testing.T) {
	metadataOnly := summarizeSkillBundleContent(map[string]any{"files": []any{
		map[string]any{"path": "SKILL.md", "content_included": false},
	}})
	if metadataOnly.Mode != "metadata_only" || metadataOnly.MetadataOnlyFiles != 1 {
		t.Fatalf("metadata-only summary got %+v", metadataOnly)
	}

	mixed := summarizeSkillBundleContent(map[string]any{"files": []any{
		map[string]any{"path": "SKILL.md", "content_included": true},
		map[string]any{"path": "extra.md", "content_included": false},
	}})
	if mixed.Mode != "mixed" || mixed.InlineFiles != 1 || mixed.MetadataOnlyFiles != 1 {
		t.Fatalf("mixed summary got %+v", mixed)
	}
}

func TestParseSkillBundleID(t *testing.T) {
	skillID, revision, err := parseSkillBundleID("skill:skill-a:revision:00000042")
	if err != nil {
		t.Fatalf("parse bundle id: %v", err)
	}
	if skillID != "skill-a" || revision != 42 {
		t.Fatalf("parsed got skillID=%q revision=%d", skillID, revision)
	}

	if _, _, err := parseSkillBundleID("bad"); err == nil {
		t.Fatalf("expected invalid bundle id error")
	}
}
