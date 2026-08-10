package recoverymaterial

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/hostapi"
)

func TestSoulPreservesExactDeclarationsAndRecoveryProvenance(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":"2", "selfDescription":{"purpose":"recover"},"capabilities":[],"boundaries":[],"transparency":{}}`)
	agent := &hostapi.RecoveryAgent{
		AgentID:             "0x57d10000000000000000000000000000000000000000000000000000000065c3",
		Classification:      hostapi.RecoveryPublishedArtifactVerified,
		MigrationReadSHA256: "sha256:" + strings.Repeat("a", 64),
		DeclarationsJSON:    raw,
		Source:              hostapi.RecoverySource{RegistrationID: "reg", ConversationID: "conv", ProducedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	doc, err := Soul(agent)
	if err != nil {
		t.Fatalf("Soul: %v", err)
	}
	if !strings.Contains(doc.Body, string(raw)) {
		t.Fatalf("body did not preserve declaration bytes: %q", doc.Body)
	}
	if doc.Provenance == nil || doc.Provenance.Source != "host_recovery" || doc.Provenance.DeclarationSchemaVersion != "2" || doc.Provenance.HistoricalPublicationSHA == nil || *doc.Provenance.HistoricalPublicationSHA {
		t.Fatalf("provenance = %+v", doc.Provenance)
	}
}

func TestSoulRejectsDeclarationTooLargeForPtahSoul(t *testing.T) {
	agent := &hostapi.RecoveryAgent{
		AgentID:             "0x57d10000000000000000000000000000000000000000000000000000000065c3",
		Classification:      hostapi.RecoveryLegacyDeclarationsOnly,
		MigrationReadSHA256: "sha256:" + strings.Repeat("a", 64),
		DeclarationsJSON:    json.RawMessage(`{"selfDescription":{"purpose":"` + strings.Repeat("x", 50000) + `"},"capabilities":[],"boundaries":[],"transparency":{}}`),
		Source:              hostapi.RecoverySource{RegistrationID: "reg", ConversationID: "conv", ProducedAt: time.Now().UTC()},
	}
	if _, err := Soul(agent); err == nil {
		t.Fatal("expected bounded soul rejection")
	}
}
