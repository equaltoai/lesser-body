package ptahserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
)

const (
	hostedGenesisCanonicalBegin = "-----BEGIN HOSTED GENESIS CANONICAL JSON-----"
	hostedGenesisCanonicalEnd   = "-----END HOSTED GENESIS CANONICAL JSON-----"

	hostedGenesisDeclarationSchemaV2 = "soul-five-body-schema.v2"
	hostedGenesisOwnerReviewV1       = "hosted-genesis-owner-review.v1"
	hostedGenesisInstructionsSeedV1  = "ptah-hosted-genesis-agent-instructions.v1"
)

var (
	errFinalizedDeclarationInvalid = errors.New("finalized hosted-genesis declaration is invalid")
	hostedGenesisSHA256Pattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type finalizedHostedGenesisDeclaration struct {
	AgentID          string
	RegistrationID   string
	ConversationID   string
	Model            string
	SchemaVersion    string
	CandidateHash    string
	CanonicalJSON    []byte
	FiveBodies       *agentcontent.FiveBodies
	SourceStatus     string
	CandidateVersion string
}

// transformFinalizedHostedGenesisDeclaration is the only declaration
// application path. It performs bounded JSON/hash/template work in-process;
// it never invokes a MicroVM, model, provider, or sibling service.
func transformFinalizedHostedGenesisDeclaration(raw map[string]any, expectedRegistrationID, expectedConversationID string) (*agentcontent.SoulDocument, *finalizedHostedGenesisDeclaration, error) {
	source, err := parseFinalizedHostedGenesisDeclaration(raw, expectedRegistrationID, expectedConversationID)
	if err != nil {
		return nil, nil, err
	}
	body, err := renderHostedGenesisSoul(source.FiveBodies)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: render canonical five-body markdown", errFinalizedDeclarationInvalid)
	}
	document := &agentcontent.SoulDocument{
		SchemaVersion: agentcontent.SoulDocumentSchemaVersion,
		AgentID:       source.AgentID,
		Body:          body,
		Structure: &agentcontent.SoulStructure{
			FiveBodies: source.FiveBodies,
		},
		Provenance: &agentcontent.Provenance{
			DeclarationSchemaVersion: source.SchemaVersion,
			DeclarationCandidateHash: source.CandidateHash,
			RegistrationID:           source.RegistrationID,
			ConversationID:           source.ConversationID,
			Model:                    source.Model,
			Source:                   "ptah_seed",
		},
	}
	if err := agentcontent.ValidateSoulDocumentDraft(document, source.AgentID); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errFinalizedDeclarationInvalid, err)
	}
	return document, source, nil
}

func parseFinalizedHostedGenesisDeclaration(raw map[string]any, expectedRegistrationID, expectedConversationID string) (*finalizedHostedGenesisDeclaration, error) {
	conversation := nestedMap(raw, "conversation")
	if len(conversation) == 0 {
		return nil, declarationSeedError("conversation is missing")
	}
	status := strings.ToLower(strings.TrimSpace(stringValue(conversation, "status")))
	if status != "declaration_ready" && status != "published" {
		return nil, declarationSeedError("conversation is not declaration_ready or published")
	}
	registrationID := strings.TrimSpace(stringValue(conversation, "registration_id"))
	conversationID := strings.TrimSpace(stringValue(conversation, "conversation_id"))
	agentID := strings.TrimSpace(stringValue(conversation, "agent_id"))
	if registrationID == "" || conversationID == "" || agentID == "" {
		return nil, declarationSeedError("conversation identifiers are incomplete")
	}
	if expectedRegistrationID = strings.TrimSpace(expectedRegistrationID); expectedRegistrationID != "" && registrationID != expectedRegistrationID {
		return nil, declarationSeedError("registration_id does not match the finalize request")
	}
	if expectedConversationID = strings.TrimSpace(expectedConversationID); expectedConversationID != "" && conversationID != expectedConversationID {
		return nil, declarationSeedError("conversation_id does not match the finalize request")
	}

	candidate := nestedMap(conversation, "declaration_candidate")
	review := nestedMap(candidate, "review")
	if stringValue(candidate, "version") != "hosted-genesis-declaration-candidate.v1" ||
		stringValue(candidate, "phase") != "finalized" {
		return nil, declarationSeedError("declaration candidate is not finalized")
	}
	candidateHash := strings.TrimSpace(stringValue(candidate, "candidate_hash"))
	reviewCandidateHash := strings.TrimSpace(stringValue(review, "candidate_hash"))
	reviewHash := strings.TrimSpace(stringValue(review, "review_hash"))
	reviewText, reviewTextOK := review["review_text"].(string)
	candidateRevision, candidateRevisionOK := exactNonNegativeInt(candidate["revision"])
	reviewRevision, reviewRevisionOK := exactNonNegativeInt(review["candidate_revision"])
	if !hostedGenesisSHA256Pattern.MatchString(candidateHash) ||
		reviewCandidateHash != candidateHash ||
		!hostedGenesisSHA256Pattern.MatchString(reviewHash) ||
		stringValue(review, "renderer_version") != hostedGenesisOwnerReviewV1 ||
		!candidateRevisionOK || !reviewRevisionOK || reviewRevision != candidateRevision ||
		!reviewTextOK || reviewText == "" {
		return nil, declarationSeedError("declaration review bindings are incomplete")
	}
	if sha256Identifier([]byte(reviewText)) != reviewHash {
		return nil, declarationSeedError("declaration review hash does not match review_text")
	}

	canonical, err := canonicalJSONFromReview(reviewText)
	if err != nil {
		return nil, err
	}
	if sha256Identifier(canonical) != candidateHash {
		return nil, declarationSeedError("candidate hash does not match canonical declaration bytes")
	}
	declaration, err := decodeCanonicalHostedGenesisDeclaration(canonical)
	if err != nil {
		return nil, err
	}

	produced := nestedMap(conversation, "produced_declarations")
	evidence := nestedMap(produced, "evidence")
	if hash := strings.TrimSpace(stringValue(produced, "declaration_hash")); hash != "" && hash != candidateHash {
		return nil, declarationSeedError("produced declaration hash does not match the candidate")
	}
	if value := strings.TrimSpace(stringValue(evidence, "registration_id")); value != "" && value != registrationID {
		return nil, declarationSeedError("produced registration_id does not match the conversation")
	}
	if value := strings.TrimSpace(stringValue(evidence, "conversation_id")); value != "" && value != conversationID {
		return nil, declarationSeedError("produced conversation_id does not match the conversation")
	}
	if value := strings.TrimSpace(stringValue(evidence, "agent_id")); value != "" && value != agentID {
		return nil, declarationSeedError("produced agent_id does not match the conversation")
	}
	// The model carried inside the hash-authenticated canonical declaration is
	// stable across declaration_ready and published replays. Host may omit the
	// terminal produced_declarations projection after publication, so evidence
	// is only a fallback; choosing it first would make retries byte-unstable.
	model := strings.TrimSpace(declaration.MintingModel)
	if model == "" {
		model = strings.TrimSpace(stringValue(evidence, "model"))
	}
	if model == "" {
		return nil, declarationSeedError("declaration producer model is missing")
	}

	return &finalizedHostedGenesisDeclaration{
		AgentID:          agentID,
		RegistrationID:   registrationID,
		ConversationID:   conversationID,
		Model:            model,
		SchemaVersion:    declaration.SchemaVersion,
		CandidateHash:    candidateHash,
		CanonicalJSON:    append([]byte(nil), canonical...),
		FiveBodies:       declaration.FiveBodies,
		SourceStatus:     status,
		CandidateVersion: stringValue(candidate, "version"),
	}, nil
}

type canonicalHostedGenesisDeclaration struct {
	SchemaVersion string
	FiveBodies    *agentcontent.FiveBodies
	MintingModel  string
}

func decodeCanonicalHostedGenesisDeclaration(canonical []byte) (*canonicalHostedGenesisDeclaration, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &top); err != nil || top == nil {
		return nil, declarationSeedError("canonical declaration is not a JSON object")
	}
	var schemaVersion string
	if err := json.Unmarshal(top["schemaVersion"], &schemaVersion); err != nil || schemaVersion != hostedGenesisDeclarationSchemaV2 {
		return nil, declarationSeedError("canonical declaration schemaVersion is not soul-five-body-schema.v2")
	}
	var fiveBodies agentcontent.FiveBodies
	decoder := json.NewDecoder(bytes.NewReader(top["fiveBodies"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fiveBodies); err != nil {
		return nil, declarationSeedError("canonical fiveBodies is not the closed v2 shape")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, declarationSeedError("canonical fiveBodies contains trailing JSON")
	}
	var selfDescription struct {
		MintingModel string `json:"mintingModel"`
	}
	if raw := top["selfDescription"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &selfDescription); err != nil {
			return nil, declarationSeedError("canonical selfDescription is invalid")
		}
	}
	return &canonicalHostedGenesisDeclaration{
		SchemaVersion: schemaVersion,
		FiveBodies:    &fiveBodies,
		MintingModel:  selfDescription.MintingModel,
	}, nil
}

func canonicalJSONFromReview(reviewText string) ([]byte, error) {
	begin := hostedGenesisCanonicalBegin + "\n"
	end := "\n" + hostedGenesisCanonicalEnd
	beginIndex := strings.Index(reviewText, begin)
	if beginIndex < 0 || strings.LastIndex(reviewText, begin) != beginIndex {
		return nil, declarationSeedError("review_text has no unique canonical JSON begin marker")
	}
	remainder := reviewText[beginIndex+len(begin):]
	endIndex := strings.Index(remainder, end)
	if endIndex < 0 || strings.LastIndex(remainder, end) != endIndex {
		return nil, declarationSeedError("review_text has no unique canonical JSON end marker")
	}
	canonical := []byte(remainder[:endIndex])
	if len(canonical) == 0 || !json.Valid(canonical) {
		return nil, declarationSeedError("review_text canonical JSON is empty or invalid")
	}
	return canonical, nil
}

func renderHostedGenesisSoul(fiveBodies *agentcontent.FiveBodies) (string, error) {
	if fiveBodies == nil {
		return "", declarationSeedError("fiveBodies is required")
	}
	return agentcontent.RenderFiveBodiesMarkdown(fiveBodies)
}

func renderHostedGenesisInstructions(source *finalizedHostedGenesisDeclaration) (string, error) {
	if source == nil ||
		strings.TrimSpace(source.AgentID) == "" ||
		!hostedGenesisSHA256Pattern.MatchString(strings.TrimSpace(source.CandidateHash)) {
		return "", declarationSeedError("instructions source binding is incomplete")
	}
	return fmt.Sprintf(`# Agent operating instructions

Seed version: %s
Registry agent_id: %s
Declaration candidate: %s

This draft is the host-facing operating note for the materialized agent.

1. Read the published agent soul before acting. Treat its identity, philosophy, discipline, boundaries, refusals, and stated cadence as authoritative.
2. Honor its boundaries and refusals. Use each closest safe path instead of bypassing an invariant.
3. Follow the soul's cadence. At minimum, use Ground → Act → Record → Re-ground at meaningful work boundaries.
4. If this draft conflicts with the published soul, stop and follow the soul's stricter boundary. The owner may replace this draft through agent_instructions_upsert.
`, hostedGenesisInstructionsSeedV1, source.AgentID, source.CandidateHash), nil
}

func sha256Identifier(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func declarationSeedError(message string) error {
	return fmt.Errorf("%w: %s", errFinalizedDeclarationInvalid, message)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
