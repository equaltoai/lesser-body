package ptahserver

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
)

const hostedGenesisStableBodySHA256 = "sha256:dcf25283fef4910719a378e6b6835a394293d8406710919680eda879a4b4c78f"

func TestFinalizedDeclarationTransformIsByteStableAndProvenanceComplete(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		wantModel string
	}{
		{
			name:      "declaration ready uses hash-authenticated minting model",
			fixture:   "testdata/host-contract/pr-978/hosted-genesis.conversation.completed-declaration-ready.example.json",
			wantModel: "openai:gpt-5",
		},
		{
			name:      "published replay uses canonical minting model",
			fixture:   "testdata/host-contract/pr-978/hosted-genesis.conversation.published.example.json",
			wantModel: "openai:gpt-5",
		},
	}

	var firstBody string
	var firstDocumentJSON string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := readGenesisFixture(t, tc.fixture)
			documentOne, sourceOne, err := transformFinalizedHostedGenesisDeclaration(raw, "reg_01jzhostedgenesis", "conv_01jzhostedgenesis")
			if err != nil {
				t.Fatalf("transformFinalizedHostedGenesisDeclaration(first) error = %v", err)
			}
			documentTwo, sourceTwo, err := transformFinalizedHostedGenesisDeclaration(raw, "reg_01jzhostedgenesis", "conv_01jzhostedgenesis")
			if err != nil {
				t.Fatalf("transformFinalizedHostedGenesisDeclaration(second) error = %v", err)
			}
			if documentOne.Body != documentTwo.Body || sha256Identifier([]byte(documentOne.Body)) != sha256Identifier([]byte(documentTwo.Body)) {
				t.Fatal("identical declaration produced non-identical body bytes")
			}
			if got := sha256Identifier([]byte(documentOne.Body)); got != hostedGenesisStableBodySHA256 {
				t.Fatalf("body sha = %q, want stable %q\nbody:\n%s", got, hostedGenesisStableBodySHA256, documentOne.Body)
			}
			if firstBody == "" {
				firstBody = documentOne.Body
			} else if documentOne.Body != firstBody {
				t.Fatal("declaration_ready and published projections rendered different canonical body bytes")
			}
			documentJSON, err := json.Marshal(documentOne)
			if err != nil {
				t.Fatalf("Marshal materialized document: %v", err)
			}
			if firstDocumentJSON == "" {
				firstDocumentJSON = string(documentJSON)
			} else if string(documentJSON) != firstDocumentJSON {
				t.Fatalf("declaration_ready and published projections materialized different v2 bytes:\nfirst=%s\ncurrent=%s", firstDocumentJSON, documentJSON)
			}
			if documentOne.SchemaVersion != agentcontent.SoulDocumentSchemaVersion ||
				documentOne.AgentID != "0x2222222222222222222222222222222222222222222222222222222222222222" ||
				documentOne.Structure == nil ||
				documentOne.Structure.FiveBodies == nil ||
				documentOne.Provenance == nil {
				t.Fatalf("materialized document is incomplete: %+v", documentOne)
			}
			provenance := documentOne.Provenance
			if provenance.DeclarationSchemaVersion != hostedGenesisDeclarationSchemaV2 ||
				provenance.DeclarationCandidateHash != "sha256:3e5139a34c53b0365fb245109661dced8596c20d2eb2c083e8f54f23e1a76138" ||
				provenance.RegistrationID != "reg_01jzhostedgenesis" ||
				provenance.ConversationID != "conv_01jzhostedgenesis" ||
				provenance.Model != tc.wantModel ||
				provenance.Source != "ptah_seed" {
				t.Fatalf("provenance = %+v, want complete source evidence", provenance)
			}
			if sourceOne.CandidateHash != sourceTwo.CandidateHash ||
				string(sourceOne.CanonicalJSON) != string(sourceTwo.CanonicalJSON) {
				t.Fatal("identical declaration produced unstable source extraction")
			}
			if got := documentOne.Structure.FiveBodies.Soul.Refusals[0].ClosestSafePath; got != "submit a matching structural affirmation" {
				t.Fatalf("five-body values were not preserved verbatim: %q", got)
			}
			for _, excerpt := range []string{
				"# Agent soul\n",
				"## Identity\n\nI am the tenant-bound Hosted Genesis conversation actor.",
				"### Refusals\n",
				"1. **Bypass:** skip the candidate hash check",
				"   **Closest safe path:** publish the already affirmed candidate bytes",
			} {
				if !strings.Contains(documentOne.Body, excerpt) {
					t.Fatalf("body missing deterministic template excerpt %q:\n%s", excerpt, documentOne.Body)
				}
			}
		})
	}
}

func TestFinalizedDeclarationTransformFailsClosedOnBindingDrift(t *testing.T) {
	base := readGenesisFixture(t, "testdata/host-contract/pr-978/hosted-genesis.conversation.completed-declaration-ready.example.json")
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "candidate hash",
			mutate: func(raw map[string]any) {
				nestedMap(nestedMap(raw, "conversation"), "declaration_candidate")["candidate_hash"] = "sha256:" + strings.Repeat("0", 64)
			},
		},
		{
			name: "review text",
			mutate: func(raw map[string]any) {
				nestedMap(nestedMap(nestedMap(raw, "conversation"), "declaration_candidate"), "review")["review_text"] = "tampered"
			},
		},
		{
			name: "registration id",
			mutate: func(raw map[string]any) {
				nestedMap(raw, "conversation")["registration_id"] = "other-registration"
			},
		},
		{
			name: "not finalized",
			mutate: func(raw map[string]any) {
				nestedMap(nestedMap(raw, "conversation"), "declaration_candidate")["phase"] = "review"
			},
		},
		{
			name: "review renderer",
			mutate: func(raw map[string]any) {
				nestedMap(nestedMap(nestedMap(raw, "conversation"), "declaration_candidate"), "review")["renderer_version"] = "unknown-reviewer"
			},
		},
		{
			name: "review revision",
			mutate: func(raw map[string]any) {
				nestedMap(nestedMap(nestedMap(raw, "conversation"), "declaration_candidate"), "review")["candidate_revision"] = float64(999)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := cloneJSONMap(t, base)
			tc.mutate(raw)
			document, _, err := transformFinalizedHostedGenesisDeclaration(raw, "reg_01jzhostedgenesis", "conv_01jzhostedgenesis")
			if document != nil || !errors.Is(err, errFinalizedDeclarationInvalid) {
				t.Fatalf("transform drift document=%+v error=%v, want typed fail-closed error", document, err)
			}
		})
	}
}

func readGenesisFixture(t *testing.T, path string) map[string]any {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
	return raw
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal clone: %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatalf("Unmarshal clone: %v", err)
	}
	return clone
}
