package agentcontent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateSoulDocumentDraftBoundsAndClosedStructuredShape(t *testing.T) {
	summary := "A bounded summary"
	valid := &SoulDocument{
		SchemaVersion: SoulDocumentSchemaVersion,
		AgentID:       "agent-123",
		Body:          strings.Repeat("a", MaxAgentSoulBytes),
		Summary:       &summary,
		Structure: &SoulStructure{FiveBodies: &FiveBodies{
			Identity:   &DeclarationSection{Summary: "identity"},
			Philosophy: &DeclarationSection{Summary: "philosophy"},
			Discipline: &DeclarationSection{Summary: "discipline"},
			Boundaries: &DeclarationSection{Summary: "boundaries"},
			Soul: &SoulDeclarationSection{
				Summary: "soul",
				Refusals: []Refusal{{
					Bypass:          "skip validation",
					Invariant:       "closed shapes remain closed",
					ClosestSafePath: "submit the v2 shape",
				}},
			},
		}},
		Provenance: &Provenance{
			DeclarationSchemaVersion: "soul-five-body-schema.v2",
			DeclarationCandidateHash: "sha256:" + strings.Repeat("a", 64),
			RegistrationID:           "registration-1",
			ConversationID:           "conversation-1",
			Model:                    "provider:model",
			Source:                   "owner",
		},
	}
	if err := ValidateSoulDocumentDraft(valid, "agent-123"); err != nil {
		t.Fatalf("ValidateSoulDocumentDraft(valid) error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*SoulDocument)
	}{
		{
			name: "body byte overflow",
			mutate: func(document *SoulDocument) {
				document.Body = strings.Repeat("b", MaxAgentSoulBytes+1)
			},
		},
		{
			name: "untrimmed summary",
			mutate: func(document *SoulDocument) {
				value := " not trimmed "
				document.Summary = &value
			},
		},
		{
			name: "forbidden selector delimiter",
			mutate: func(document *SoulDocument) {
				document.AgentID = "local/id"
			},
		},
		{
			name: "missing five body section",
			mutate: func(document *SoulDocument) {
				document.Structure.FiveBodies.Discipline = nil
			},
		},
		{
			name: "empty refusal floor",
			mutate: func(document *SoulDocument) {
				document.Structure.FiveBodies.Soul.Refusals = nil
			},
		},
		{
			name: "incomplete provenance",
			mutate: func(document *SoulDocument) {
				document.Provenance.Model = ""
			},
		},
		{
			name: "invalid provenance hash",
			mutate: func(document *SoulDocument) {
				document.Provenance.DeclarationCandidateHash = "SHA256:ABC"
			},
		},
		{
			name: "client lifecycle field",
			mutate: func(document *SoulDocument) {
				document.LifecycleState = LifecycleStateDraft
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := cloneSoulDocument(valid)
			tc.mutate(document)
			if err := ValidateSoulDocumentDraft(document, "agent-123"); !errors.Is(err, ErrInvalidSoulDocument) &&
				!errors.Is(err, ErrContentTooLarge) {
				t.Fatalf("ValidateSoulDocumentDraft() error = %v, want typed schema/size error", err)
			}
		})
	}
}

func TestDecodeSoulDocumentRejectsUnknownProperties(t *testing.T) {
	_, err := decodeSoulDocument(`{
		"schema_version":"lessersoul.panonomous.soul-document.v2",
		"agent_id":"agent-123",
		"body":"body",
		"unknown":true
	}`)
	if !errors.Is(err, ErrInvalidSoulDocument) {
		t.Fatalf("decodeSoulDocument(unknown) error = %v, want ErrInvalidSoulDocument", err)
	}

	_, err = decodeSoulDocument(`{
		"schema_version":"lessersoul.panonomous.soul-document.v2",
		"agent_id":"agent-123",
		"body":"body",
		"structure":{"five_bodies":{
			"identity":{"summary":"identity","unknown":true},
			"philosophy":{"summary":"philosophy"},
			"discipline":{"summary":"discipline"},
			"boundaries":{"summary":"boundaries"},
			"soul":{"summary":"soul","refusals":[{"bypass":"b","invariant":"i","closestSafePath":"c"}]}
		}}
	}`)
	if !errors.Is(err, ErrInvalidSoulDocument) {
		t.Fatalf("decodeSoulDocument(nested unknown) error = %v, want ErrInvalidSoulDocument", err)
	}
}

func TestSoulDocumentJSONRejectsSchemaNullsAndMissingRequiredSectionFields(t *testing.T) {
	cases := []string{
		`{"agent_id":"agent-123","body":"body","structure":null}`,
		`{"agent_id":"agent-123","body":"body","summary":null}`,
		`{"agent_id":"agent-123","body":"body","structure":{"five_bodies":{
			"identity":{},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[{"bypass":"x","invariant":"i","closestSafePath":"c"}]}
		}}}`,
		`{"agent_id":"agent-123","body":"body","structure":{"five_bodies":{
			"identity":{"summary":null},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[{"bypass":"x","invariant":"i","closestSafePath":"c"}]}
		}}}`,
		`{"agent_id":"agent-123","body":"body","structure":{"five_bodies":{
			"identity":{"summary":"","notes":null},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[{"bypass":"x","invariant":"i","closestSafePath":"c"}]}
		}}}`,
		`{"agent_id":"agent-123","body":"body","structure":{"five_bodies":{
			"identity":{"summary":"i"},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[{"bypass":"x","invariant":"i"}]}
		}}}`,
		`{"agent_id":"agent-123","body":"body","structure":{"five_bodies":{
			"identity":{"summary":"i"},"philosophy":{"summary":"p"},"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},"soul":{"summary":"s","refusals":[null]}
		}}}`,
	}
	for index, input := range cases {
		if _, err := decodeSoulDocument(input); !errors.Is(err, ErrInvalidSoulDocument) {
			t.Fatalf("case %d decode error = %v, want ErrInvalidSoulDocument", index, err)
		}
	}
}

func TestSoulDocumentJSONPreservesExplicitEmptyNotes(t *testing.T) {
	document, err := decodeSoulDocument(`{
		"agent_id":"agent-123",
		"body":"body",
		"structure":{"five_bodies":{
			"identity":{"summary":"i","notes":[]},
			"philosophy":{"summary":"p"},
			"discipline":{"summary":"d"},
			"boundaries":{"summary":"b"},
			"soul":{"summary":"s","notes":[],"refusals":[{"bypass":"x","invariant":"i","closestSafePath":"c"}]}
		}}
	}`)
	if err != nil {
		t.Fatalf("decodeSoulDocument(empty notes) error = %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal(empty notes) error = %v", err)
	}
	if got := strings.Count(string(encoded), `"notes":[]`); got != 2 {
		t.Fatalf("encoded empty notes count = %d, want 2: %s", got, encoded)
	}
}

func TestValidateSoulDocumentDraftUsesNormativeUTF8ByteBounds(t *testing.T) {
	summaryAtLimit := strings.Repeat("é", MaxAgentSoulSummaryBytes/2)
	document := &SoulDocument{
		AgentID: "agent-utf8",
		Body:    strings.Repeat("é", MaxAgentSoulBytes/2),
		Summary: &summaryAtLimit,
	}
	if err := ValidateSoulDocumentDraft(document, "agent-utf8"); err != nil {
		t.Fatalf("ValidateSoulDocumentDraft(multibyte at byte limits) error = %v", err)
	}

	document.Body += "é"
	var sizeErr *SizeError
	if err := ValidateSoulDocumentDraft(document, "agent-utf8"); !errors.As(err, &sizeErr) ||
		sizeErr.Actual != MaxAgentSoulBytes+2 {
		t.Fatalf("multibyte body overflow error = %#v, want %d bytes", err, MaxAgentSoulBytes+2)
	}

	document.Body = "body"
	summaryOverLimit := summaryAtLimit + "é"
	document.Summary = &summaryOverLimit
	var validationErr *ValidationError
	if err := ValidateSoulDocumentDraft(document, "agent-utf8"); !errors.As(err, &validationErr) ||
		validationErr.Field != "summary" {
		t.Fatalf("multibyte summary overflow error = %#v, want summary ValidationError", err)
	}
}
