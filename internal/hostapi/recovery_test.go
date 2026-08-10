package hostapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

type rawRecoveryDoer struct {
	raw   []byte
	err   error
	path  string
	token string
}

func (d *rawRecoveryDoer) DoJSONRaw(_ context.Context, _ string, path string, _ url.Values, bearer string, _ any) ([]byte, error) {
	d.path, d.token = path, bearer
	return d.raw, d.err
}

func TestRecoveryClientVerifiesExactDeclarationDigest(t *testing.T) {
	raw := recoveryFixture(t, RecoveryPublishedArtifactVerified)
	doer := &rawRecoveryDoer{raw: raw}
	agent, err := NewRecovery(doer).ReadRecoveryAgent(context.Background(), "managed-key", testRecoveryAgentID)
	if err != nil {
		t.Fatalf("ReadRecoveryAgent: %v", err)
	}
	if agent.Classification != RecoveryPublishedArtifactVerified || len(agent.Versions) != 2 {
		t.Fatalf("agent = %+v", agent)
	}
	if string(agent.DeclarationsJSON) != string(testRecoveryDeclarations) {
		t.Fatalf("declarations bytes changed: %q", agent.DeclarationsJSON)
	}
	if doer.path != recoveryAgentPath+testRecoveryAgentID || doer.token != "managed-key" {
		t.Fatalf("request = path %q token %q", doer.path, doer.token)
	}
}

func TestRecoveryClientAcceptsHonestLegacyDraftEvidence(t *testing.T) {
	agent, err := NewRecovery(&rawRecoveryDoer{raw: recoveryFixture(t, RecoveryLegacyDeclarationsOnly)}).
		ReadRecoveryAgent(context.Background(), "managed-key", testRecoveryAgentID)
	if err != nil {
		t.Fatalf("ReadRecoveryAgent: %v", err)
	}
	if agent.PublishedRegistration != nil || len(agent.Versions) != 0 {
		t.Fatalf("legacy publication evidence = %+v / %+v", agent.PublishedRegistration, agent.Versions)
	}
}

func TestRecoveryClientFailsClosedOnContractDriftAndIntegrityMismatch(t *testing.T) {
	base := recoveryFixtureMap(t, RecoveryPublishedArtifactVerified)
	tests := map[string]func(map[string]any){
		"unknown field": func(m map[string]any) { m["future"] = true },
		"digest":        func(m map[string]any) { m["migration_read_sha256"] = "sha256:" + strings.Repeat("0", 64) },
		"provenance":    func(m map[string]any) { m["provenance"].(map[string]any)["historical_publication_sha"] = true },
		"chain": func(m map[string]any) {
			m["versions"].([]any)[1].(map[string]any)["previous_registration_sha256"] = strings.Repeat("9", 64)
		},
		"published missing": func(m map[string]any) { delete(m, "published_registration") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			clone := cloneRecoveryFixture(t, base)
			mutate(clone)
			raw, _ := json.Marshal(clone)
			if _, err := NewRecovery(&rawRecoveryDoer{raw: raw}).ReadRecoveryAgent(context.Background(), "key", testRecoveryAgentID); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestRecoveryClientSanitizesUpstreamErrors(t *testing.T) {
	doer := &rawRecoveryDoer{err: errors.New("secret declaration body")}
	_, err := NewRecovery(doer).ReadRecoveryAgent(context.Background(), "key", testRecoveryAgentID)
	if err == nil || strings.Contains(err.Error(), "secret declaration body") {
		t.Fatalf("error was not sanitized: %v", err)
	}
}

const testRecoveryAgentID = "0x57d10000000000000000000000000000000000000000000000000000000065c3"

var testRecoveryDeclarations = []byte(`{"schemaVersion":"2","selfDescription":{"purpose":"recover"},"capabilities":[],"boundaries":[],"transparency":{}}`)

func recoveryFixture(t *testing.T, classification string) []byte {
	t.Helper()
	raw, err := json.Marshal(recoveryFixtureMap(t, classification))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func recoveryFixtureMap(t *testing.T, classification string) map[string]any {
	t.Helper()
	digest := sha256.Sum256(testRecoveryDeclarations)
	one := strings.Repeat("1", 64)
	two := strings.Repeat("2", 64)
	fixture := map[string]any{
		"version": "1", "agent_id": testRecoveryAgentID, "domain": "theory.greater.website", "local_id": "della-marlowe", "status": "active",
		"classification": classification, "self_description_version": 2,
		"source":                map[string]any{"registration_id": "reg_della", "conversation_id": "conv_della", "produced_at": "2026-01-16T12:00:00Z"},
		"migration_read_sha256": "sha256:" + hex.EncodeToString(digest[:]),
		"provenance":            map[string]any{"source": "hosted_genesis_exact_declarations", "digest_semantics": "migration_read_sha256", "historical_publication_sha": false},
		"declarations":          json.RawMessage(testRecoveryDeclarations),
	}
	if classification == RecoveryPublishedArtifactVerified {
		fixture["versions"] = []any{
			map[string]any{"version_number": 1, "registration_uri": "s3://bucket/registry/v1/agents/" + testRecoveryAgentID + "/versions/1/registration.json", "registration_sha256": one, "created_at": "2026-01-16T12:01:00Z", "checksum_verified": true},
			map[string]any{"version_number": 2, "registration_uri": "s3://bucket/registry/v1/agents/" + testRecoveryAgentID + "/versions/2/registration.json", "registration_sha256": two, "previous_registration_sha256": one, "created_at": "2026-02-16T12:01:00Z", "checksum_verified": true},
		}
		fixture["published_registration"] = map[string]any{"current_registration_sha256": two, "current_checksum_verified": true}
	} else {
		fixture["versions"] = []any{}
	}
	return fixture
}

func cloneRecoveryFixture(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(in)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
