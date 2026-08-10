package recoverymaterial

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/hostapi"
)

const instructionsSeedVersion = "ptah-host-recovery-instructions.v1"

func Soul(agent *hostapi.RecoveryAgent) (*agentcontent.SoulDocument, error) {
	if agent == nil || len(agent.DeclarationsJSON) == 0 {
		return nil, fmt.Errorf("recovery agent declarations are required")
	}
	schemaVersion := declarationSchemaVersion(agent.DeclarationsJSON)
	historical := false
	body := fmt.Sprintf("# Recovered Host soul declaration\n\nRecovery classification: `%s`\nMigration-read digest: `%s`\n\nThe JSON below is the exact Host-retained declaration object verified during recovery.\n\n```json\n%s\n```\n",
		agent.Classification, agent.MigrationReadSHA256, string(agent.DeclarationsJSON))
	summary := "Host-retained soul declaration recovered into Ptah"
	document := &agentcontent.SoulDocument{
		SchemaVersion: agentcontent.SoulDocumentSchemaVersion,
		AgentID:       agent.AgentID,
		Body:          body,
		Summary:       &summary,
		Provenance: &agentcontent.Provenance{
			DeclarationSchemaVersion: schemaVersion,
			RegistrationID:           agent.Source.RegistrationID,
			ConversationID:           agent.Source.ConversationID,
			Source:                   "host_recovery",
			RecoveryClassification:   agent.Classification,
			MigrationReadSHA256:      agent.MigrationReadSHA256,
			ProducedAt:               agent.Source.ProducedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			HistoricalPublicationSHA: &historical,
		},
	}
	if err := agentcontent.ValidateSoulDocumentDraft(document, agent.AgentID); err != nil {
		return nil, fmt.Errorf("materialize recovery soul: %w", err)
	}
	return document, nil
}

func Instructions(agent *hostapi.RecoveryAgent) (string, error) {
	if agent == nil || strings.TrimSpace(agent.AgentID) == "" || strings.TrimSpace(agent.MigrationReadSHA256) == "" {
		return "", fmt.Errorf("recovery instructions source is incomplete")
	}
	return fmt.Sprintf(`# Agent operating instructions

Seed version: %s
Registry agent_id: %s
Recovery declaration: %s

Read the recovered agent soul before acting. Treat its retained self-description, capabilities, boundaries, transparency, and stricter constraints as authoritative. If this draft conflicts with the recovered soul, follow the soul. The account owner may revise these instructions through Ptah.
`, instructionsSeedVersion, agent.AgentID, agent.MigrationReadSHA256), nil
}

func declarationSchemaVersion(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		if value := bytes.TrimSpace(fields["schemaVersion"]); len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			var text string
			if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
			var number json.Number
			if json.Unmarshal(value, &number) == nil && strings.TrimSpace(number.String()) != "" {
				return number.String()
			}
		}
	}
	return "host-recovery-declarations.v1"
}
