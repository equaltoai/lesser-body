package agentcontent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// SoulDocumentSchemaVersion is the stable Panonomous soul-document v2
	// marker published by lesser-soul.
	SoulDocumentSchemaVersion = "lessersoul.panonomous.soul-document.v2"

	// SoulDocumentSchemaURL is the canonical public v2 contract.
	SoulDocumentSchemaURL = "https://spec.lessersoul.ai/contracts/panonomous/soul-document/v2/schema.json"

	// MaxAgentSoulBytes is the v2 normative UTF-8 byte bound for body.
	MaxAgentSoulBytes = 49_152

	// MaxAgentSoulSummaryBytes is the v2 normative UTF-8 byte bound for
	// summary.
	MaxAgentSoulSummaryBytes = 2_048

	// MaxAgentSoulDocumentBytes leaves headroom below DynamoDB's item limit for
	// storage keys and audit metadata. The public schema deliberately leaves
	// declaration note sizes open, so the persistence boundary needs a total
	// item guard in addition to field validation.
	MaxAgentSoulDocumentBytes = 300 * 1024
)

var (
	ErrInvalidSoulDocument = errors.New("invalid agent soul document")

	soulDocumentHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// SoulDocument is the closed Panonomous soul-document v2 shape. Lifecycle and
// audit fields are stamped by Store; authoring callers must leave them empty.
type SoulDocument struct {
	SchemaVersion      string         `json:"schema_version,omitempty"`
	AgentID            string         `json:"agent_id"`
	Body               string         `json:"body"`
	Summary            *string        `json:"summary,omitempty"`
	SoulVersion        int64          `json:"soul_version,omitempty"`
	LifecycleState     LifecycleState `json:"lifecycle_state,omitempty"`
	UpdatedBySubjectID string         `json:"updated_by_subject_id,omitempty"`
	CreatedAt          string         `json:"created_at,omitempty"`
	UpdatedAt          string         `json:"updated_at,omitempty"`
	Version            int64          `json:"version,omitempty"`
	Structure          *SoulStructure `json:"structure,omitempty"`
	Provenance         *Provenance    `json:"provenance,omitempty"`
}

type SoulStructure struct {
	FiveBodies *FiveBodies `json:"five_bodies"`
}

type FiveBodies struct {
	Identity   *DeclarationSection     `json:"identity"`
	Philosophy *DeclarationSection     `json:"philosophy"`
	Discipline *DeclarationSection     `json:"discipline"`
	Boundaries *DeclarationSection     `json:"boundaries"`
	Soul       *SoulDeclarationSection `json:"soul"`
}

type DeclarationSection struct {
	Summary string   `json:"summary"`
	Notes   []string `json:"notes,omitempty"`
}

type SoulDeclarationSection struct {
	Summary  string    `json:"summary"`
	Notes    []string  `json:"notes,omitempty"`
	Refusals []Refusal `json:"refusals"`
}

type Refusal struct {
	Bypass          string `json:"bypass"`
	Invariant       string `json:"invariant"`
	ClosestSafePath string `json:"closestSafePath"`
}

type Provenance struct {
	DeclarationSchemaVersion string `json:"declaration_schema_version"`
	DeclarationCandidateHash string `json:"declaration_candidate_hash,omitempty"`
	RegistrationID           string `json:"registration_id"`
	ConversationID           string `json:"conversation_id"`
	Model                    string `json:"model,omitempty"`
	Source                   string `json:"source"`
	RecoveryClassification   string `json:"recovery_classification,omitempty"`
	MigrationReadSHA256      string `json:"migration_read_sha256,omitempty"`
	ProducedAt               string `json:"produced_at,omitempty"`
	HistoricalPublicationSHA *bool  `json:"historical_publication_sha,omitempty"`
}

// UnmarshalJSON keeps persisted and externally supplied v2 documents closed
// even for JSON nulls, which encoding/json otherwise silently coerces into Go
// zero values.
func (document *SoulDocument) UnmarshalJSON(data []byte) error {
	if document == nil {
		return errors.New("agent soul document target is nil")
	}
	if _, err := requireClosedObjectFields(
		data,
		[]string{"agent_id", "body"},
		[]string{
			"schema_version", "agent_id", "body", "summary", "soul_version",
			"lifecycle_state", "updated_by_subject_id", "created_at", "updated_at",
			"version", "structure", "provenance",
		},
	); err != nil {
		return err
	}
	type soulDocumentWire SoulDocument
	var decoded soulDocumentWire
	if err := decodeClosedJSONValue(data, &decoded); err != nil {
		return err
	}
	*document = SoulDocument(decoded)
	return nil
}

func (section *DeclarationSection) UnmarshalJSON(data []byte) error {
	if section == nil {
		return errors.New("declaration section target is nil")
	}
	fields, err := requireClosedObjectFields(data, []string{"summary"}, []string{"summary", "notes"})
	if err != nil {
		return err
	}
	if raw, ok := fields["notes"]; ok {
		if err := validateJSONStringArray(raw); err != nil {
			return fmt.Errorf("notes: %w", err)
		}
	}
	type declarationSectionWire DeclarationSection
	var decoded declarationSectionWire
	if err := decodeClosedJSONValue(data, &decoded); err != nil {
		return err
	}
	*section = DeclarationSection(decoded)
	return nil
}

// MarshalJSON preserves an explicitly present empty notes array rather than
// collapsing it into omission. This keeps declaration structure values
// byte-stable across the deterministic Host-to-v2 transform.
func (section DeclarationSection) MarshalJSON() ([]byte, error) {
	var notes *[]string
	if section.Notes != nil {
		value := make([]string, len(section.Notes))
		copy(value, section.Notes)
		notes = &value
	}
	return json.Marshal(struct {
		Summary string    `json:"summary"`
		Notes   *[]string `json:"notes,omitempty"`
	}{
		Summary: section.Summary,
		Notes:   notes,
	})
}

func (section *SoulDeclarationSection) UnmarshalJSON(data []byte) error {
	if section == nil {
		return errors.New("soul declaration section target is nil")
	}
	fields, err := requireClosedObjectFields(
		data,
		[]string{"summary", "refusals"},
		[]string{"summary", "notes", "refusals"},
	)
	if err != nil {
		return err
	}
	if raw, ok := fields["notes"]; ok {
		if err := validateJSONStringArray(raw); err != nil {
			return fmt.Errorf("notes: %w", err)
		}
	}
	type soulDeclarationSectionWire SoulDeclarationSection
	var decoded soulDeclarationSectionWire
	if err := decodeClosedJSONValue(data, &decoded); err != nil {
		return err
	}
	*section = SoulDeclarationSection(decoded)
	return nil
}

func (section SoulDeclarationSection) MarshalJSON() ([]byte, error) {
	var notes *[]string
	if section.Notes != nil {
		value := make([]string, len(section.Notes))
		copy(value, section.Notes)
		notes = &value
	}
	return json.Marshal(struct {
		Summary  string    `json:"summary"`
		Notes    *[]string `json:"notes,omitempty"`
		Refusals []Refusal `json:"refusals"`
	}{
		Summary:  section.Summary,
		Notes:    notes,
		Refusals: section.Refusals,
	})
}

func (refusal *Refusal) UnmarshalJSON(data []byte) error {
	if refusal == nil {
		return errors.New("refusal target is nil")
	}
	if _, err := requireClosedObjectFields(
		data,
		[]string{"bypass", "invariant", "closestSafePath"},
		[]string{"bypass", "invariant", "closestSafePath"},
	); err != nil {
		return err
	}
	type refusalWire Refusal
	var decoded refusalWire
	if err := decodeClosedJSONValue(data, &decoded); err != nil {
		return err
	}
	*refusal = Refusal(decoded)
	return nil
}

// ValidationError identifies the exact v2 field that failed validation while
// preserving errors.Is(err, ErrInvalidSoulDocument).
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ErrInvalidSoulDocument.Error()
	}
	return fmt.Sprintf("%s: %s %s", ErrInvalidSoulDocument, e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrInvalidSoulDocument }

func validationError(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

func requireClosedObjectFields(data []byte, required, allowed []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, errors.New("must be a JSON object")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	for field, raw := range fields {
		if _, ok := allowedSet[field]; !ok {
			return nil, fmt.Errorf("unknown field %q", field)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("%s must not be null", field)
		}
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return nil, fmt.Errorf("%s is required", field)
		}
	}
	return fields, nil
}

func validateJSONStringArray(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return errors.New("must be a JSON array")
	}
	for index, raw := range values {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("item %d must not be null", index)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("item %d must be a string", index)
		}
	}
	return nil
}

func decodeClosedJSONValue(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("must contain one JSON value")
	}
	return nil
}

// ValidateSoulDocumentDraft validates the author-controlled v2 shape. The
// expected selector is the account-scoped registry agent_id, which is distinct
// from Host local_id/agent_username and Lesser Soul soul_agent_id.
func ValidateSoulDocumentDraft(document *SoulDocument, expectedAgentID string) error {
	if document == nil {
		return validationError("$", "is required")
	}
	if document.SoulVersion != 0 || document.LifecycleState != "" || document.UpdatedBySubjectID != "" ||
		document.CreatedAt != "" || document.UpdatedAt != "" || document.Version != 0 {
		return validationError("$", "must not contain server-managed lifecycle fields")
	}
	return validateSoulDocument(document, strings.TrimSpace(expectedAgentID), false)
}

// ValidateSoulDocumentRecord validates a fully stamped persisted/output
// record, including lifecycle and audit metadata.
func ValidateSoulDocumentRecord(document *SoulDocument, expectedAgentID string) error {
	return validateSoulDocument(document, strings.TrimSpace(expectedAgentID), true)
}

func validateSoulDocument(document *SoulDocument, expectedAgentID string, requireServerFields bool) error {
	if document == nil {
		return validationError("$", "is required")
	}
	if document.SchemaVersion != "" && document.SchemaVersion != SoulDocumentSchemaVersion {
		return validationError("schema_version", "must equal "+SoulDocumentSchemaVersion)
	}
	if err := validateSoulAgentID(document.AgentID); err != nil {
		return err
	}
	if expectedAgentID != "" && document.AgentID != expectedAgentID {
		return validationError("agent_id", "must match the account-scoped record selector")
	}
	if !utf8.ValidString(document.Body) {
		return validationError("body", "must be valid UTF-8")
	}
	if len(document.Body) == 0 {
		return validationError("body", "must not be empty")
	}
	if actual := len([]byte(document.Body)); actual > MaxAgentSoulBytes {
		return &SizeError{Type: ContentTypeAgentSoul, Limit: MaxAgentSoulBytes, Actual: actual}
	}
	if document.Summary != nil {
		summary := *document.Summary
		if !utf8.ValidString(summary) {
			return validationError("summary", "must be valid UTF-8")
		}
		if summary == "" || strings.TrimSpace(summary) == "" {
			return validationError("summary", "must not be blank")
		}
		if summary != strings.TrimSpace(summary) {
			return validationError("summary", "must be trimmed")
		}
		if actual := len([]byte(summary)); actual > MaxAgentSoulSummaryBytes {
			return validationError("summary", fmt.Sprintf("has %d UTF-8 bytes, limit %d", actual, MaxAgentSoulSummaryBytes))
		}
	}
	if document.Structure != nil {
		if err := validateFiveBodies(document.Structure.FiveBodies); err != nil {
			return err
		}
	}
	if document.Provenance != nil {
		if err := validateProvenance(document.Provenance); err != nil {
			return err
		}
	}

	if requireServerFields {
		if document.SchemaVersion == "" {
			return validationError("schema_version", "is required on stored records")
		}
		if document.SoulVersion < 1 {
			return validationError("soul_version", "must be at least 1")
		}
		if _, err := normalizeLifecycleState(document.LifecycleState); err != nil {
			return err
		}
		if strings.TrimSpace(document.UpdatedBySubjectID) == "" {
			return validationError("updated_by_subject_id", "is required")
		}
		if document.Version < 1 {
			return validationError("version", "must be at least 1")
		}
		if _, err := time.Parse(time.RFC3339Nano, document.CreatedAt); err != nil {
			return validationError("created_at", "must be an RFC3339 date-time")
		}
		if _, err := time.Parse(time.RFC3339Nano, document.UpdatedAt); err != nil {
			return validationError("updated_at", "must be an RFC3339 date-time")
		}
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return validationError("$", "cannot be encoded")
	}
	if len(encoded) > MaxAgentSoulDocumentBytes {
		return validationError("$", fmt.Sprintf("encoded size has %d bytes, storage limit %d", len(encoded), MaxAgentSoulDocumentBytes))
	}
	return nil
}

// ValidateSoulAgentID validates the route-local v2 selector without conflating
// it with Host local_id/agent_username or Lesser Soul soul_agent_id.
func ValidateSoulAgentID(agentID string) error {
	return validateSoulAgentID(agentID)
}

func validateSoulAgentID(agentID string) error {
	if !utf8.ValidString(agentID) {
		return validationError("agent_id", "must be valid UTF-8")
	}
	if agentID == "" {
		return validationError("agent_id", "must not be empty")
	}
	if utf8.RuneCountInString(agentID) > 128 {
		return validationError("agent_id", "must contain at most 128 characters")
	}
	if strings.ContainsAny(agentID, "|=/") {
		return validationError("agent_id", "must not contain |, =, or /")
	}
	return nil
}

func validateFiveBodies(five *FiveBodies) error {
	if five == nil {
		return validationError("structure.five_bodies", "is required")
	}
	sections := []struct {
		name    string
		section *DeclarationSection
	}{
		{"identity", five.Identity},
		{"philosophy", five.Philosophy},
		{"discipline", five.Discipline},
		{"boundaries", five.Boundaries},
	}
	for _, item := range sections {
		if item.section == nil {
			return validationError("structure.five_bodies."+item.name, "is required")
		}
		if err := validateUTF8Strings("structure.five_bodies."+item.name, item.section.Summary, item.section.Notes); err != nil {
			return err
		}
	}
	if five.Soul == nil {
		return validationError("structure.five_bodies.soul", "is required")
	}
	if err := validateUTF8Strings("structure.five_bodies.soul", five.Soul.Summary, five.Soul.Notes); err != nil {
		return err
	}
	if len(five.Soul.Refusals) == 0 {
		return validationError("structure.five_bodies.soul.refusals", "must contain at least one item")
	}
	for i, refusal := range five.Soul.Refusals {
		values := []struct {
			name  string
			value string
		}{
			{"bypass", refusal.Bypass},
			{"invariant", refusal.Invariant},
			{"closestSafePath", refusal.ClosestSafePath},
		}
		for _, item := range values {
			if !utf8.ValidString(item.value) {
				return validationError(fmt.Sprintf("structure.five_bodies.soul.refusals[%d].%s", i, item.name), "must be valid UTF-8")
			}
		}
	}
	return nil
}

func validateUTF8Strings(field, summary string, notes []string) error {
	if !utf8.ValidString(summary) {
		return validationError(field+".summary", "must be valid UTF-8")
	}
	for i, note := range notes {
		if !utf8.ValidString(note) {
			return validationError(fmt.Sprintf("%s.notes[%d]", field, i), "must be valid UTF-8")
		}
	}
	return nil
}

func validateProvenance(provenance *Provenance) error {
	if provenance == nil {
		return nil
	}
	required := []struct {
		field string
		value string
	}{
		{"declaration_schema_version", provenance.DeclarationSchemaVersion},
		{"registration_id", provenance.RegistrationID},
		{"conversation_id", provenance.ConversationID},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return validationError("provenance."+item.field, "must not be empty")
		}
		if !utf8.ValidString(item.value) {
			return validationError("provenance."+item.field, "must be valid UTF-8")
		}
	}
	switch provenance.Source {
	case "host_recovery":
		if provenance.RecoveryClassification != "published_artifact_verified" && provenance.RecoveryClassification != "legacy_declarations_only" {
			return validationError("provenance.recovery_classification", "must be a supported Host recovery classification")
		}
		if !soulDocumentHashPattern.MatchString(provenance.MigrationReadSHA256) {
			return validationError("provenance.migration_read_sha256", "must be a lowercase sha256 identifier")
		}
		if _, err := time.Parse(time.RFC3339Nano, provenance.ProducedAt); err != nil {
			return validationError("provenance.produced_at", "must be an RFC3339 date-time")
		}
		if provenance.HistoricalPublicationSHA == nil || *provenance.HistoricalPublicationSHA {
			return validationError("provenance.historical_publication_sha", "must be explicitly false")
		}
		if provenance.DeclarationCandidateHash != "" || provenance.Model != "" {
			return validationError("provenance", "host recovery must not claim candidate hash or model")
		}
	case "host_genesis_finalize", "ptah_seed", "owner":
		if strings.TrimSpace(provenance.Model) == "" {
			return validationError("provenance.model", "must not be empty")
		}
		if !soulDocumentHashPattern.MatchString(provenance.DeclarationCandidateHash) {
			return validationError("provenance.declaration_candidate_hash", "must be a lowercase sha256 identifier")
		}
		if provenance.RecoveryClassification != "" || provenance.MigrationReadSHA256 != "" || provenance.ProducedAt != "" || provenance.HistoricalPublicationSHA != nil {
			return validationError("provenance", "non-recovery sources must not carry recovery fields")
		}
	default:
		return validationError("provenance.source", "must be host_genesis_finalize, host_recovery, ptah_seed, or owner")
	}
	return nil
}

func encodeSoulDocument(document *SoulDocument) (string, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode agent soul document: %w", err)
	}
	if len(encoded) > MaxAgentSoulDocumentBytes {
		return "", validationError("$", fmt.Sprintf("encoded size has %d bytes, storage limit %d", len(encoded), MaxAgentSoulDocumentBytes))
	}
	return string(encoded), nil
}

func decodeSoulDocument(encoded string) (*SoulDocument, error) {
	var document SoulDocument
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, validationError("$", "persisted document is not a closed v2 object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, validationError("$", "persisted document contains trailing or malformed JSON")
	}
	return &document, nil
}

func cloneSoulDocument(document *SoulDocument) *SoulDocument {
	if document == nil {
		return nil
	}
	clone := *document
	if document.Summary != nil {
		summary := *document.Summary
		clone.Summary = &summary
	}
	if document.Provenance != nil {
		provenance := *document.Provenance
		clone.Provenance = &provenance
	}
	if document.Structure != nil {
		structure := *document.Structure
		clone.Structure = &structure
		if document.Structure.FiveBodies != nil {
			fiveBodies := *document.Structure.FiveBodies
			structure.FiveBodies = &fiveBodies
			fiveBodies.Identity = cloneDeclarationSection(document.Structure.FiveBodies.Identity)
			fiveBodies.Philosophy = cloneDeclarationSection(document.Structure.FiveBodies.Philosophy)
			fiveBodies.Discipline = cloneDeclarationSection(document.Structure.FiveBodies.Discipline)
			fiveBodies.Boundaries = cloneDeclarationSection(document.Structure.FiveBodies.Boundaries)
			fiveBodies.Soul = cloneSoulDeclarationSection(document.Structure.FiveBodies.Soul)
		}
	}
	return &clone
}

func cloneDeclarationSection(section *DeclarationSection) *DeclarationSection {
	if section == nil {
		return nil
	}
	clone := *section
	if section.Notes != nil {
		clone.Notes = make([]string, len(section.Notes))
		copy(clone.Notes, section.Notes)
	}
	return &clone
}

func cloneSoulDeclarationSection(section *SoulDeclarationSection) *SoulDeclarationSection {
	if section == nil {
		return nil
	}
	clone := *section
	if section.Notes != nil {
		clone.Notes = make([]string, len(section.Notes))
		copy(clone.Notes, section.Notes)
	}
	if section.Refusals != nil {
		clone.Refusals = make([]Refusal, len(section.Refusals))
		copy(clone.Refusals, section.Refusals)
	}
	return &clone
}

func stampSoulDocument(document *SoulDocument, state LifecycleState, soulVersion, recordVersion int64, subject string, createdAt, updatedAt time.Time) *SoulDocument {
	stamped := cloneSoulDocument(document)
	if stamped == nil {
		return nil
	}
	stamped.SchemaVersion = SoulDocumentSchemaVersion
	stamped.SoulVersion = soulVersion
	stamped.LifecycleState = state
	stamped.UpdatedBySubjectID = strings.TrimSpace(subject)
	stamped.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	stamped.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	stamped.Version = recordVersion
	return stamped
}

func sameSoulDocumentAuthoringContent(left, right *SoulDocument) bool {
	authoringBytes := func(document *SoulDocument) []byte {
		clone := cloneSoulDocument(document)
		if clone == nil {
			return nil
		}
		if clone.SchemaVersion == "" {
			clone.SchemaVersion = SoulDocumentSchemaVersion
		}
		clone.SoulVersion = 0
		clone.LifecycleState = ""
		clone.UpdatedBySubjectID = ""
		clone.CreatedAt = ""
		clone.UpdatedAt = ""
		clone.Version = 0
		encoded, _ := json.Marshal(clone)
		return encoded
	}
	return bytes.Equal(authoringBytes(left), authoringBytes(right))
}
