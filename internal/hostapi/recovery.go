package hostapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/equaltoai/lesser-body/internal/soulapi"
)

const recoveryAgentPath = "/api/v1/soul/instance/recovery/agents/"

const (
	RecoveryPublishedArtifactVerified = "published_artifact_verified"
	RecoveryLegacyDeclarationsOnly    = "legacy_declarations_only"
)

var (
	recoveryAgentIDPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	recoverySHA256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	recoveryDigestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type RawJSONDoer interface {
	DoJSONRaw(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) ([]byte, error)
}

type RecoveryClient interface {
	ReadRecoveryAgent(ctx context.Context, bearerToken, agentID string) (*RecoveryAgent, error)
}

type RecoveryAgent struct {
	Version                string
	AgentID                string
	Domain                 string
	LocalID                string
	Status                 string
	Classification         string
	SelfDescriptionVersion int64
	Source                 RecoverySource
	MigrationReadSHA256    string
	Provenance             RecoveryProvenance
	Versions               []RecoveryVersion
	DeclarationsJSON       json.RawMessage
	PublishedRegistration  *RecoveryPublishedRegistration
}

type RecoverySource struct {
	RegistrationID string    `json:"registration_id"`
	ConversationID string    `json:"conversation_id"`
	ProducedAt     time.Time `json:"produced_at"`
}

type RecoveryProvenance struct {
	Source                   string `json:"source"`
	DigestSemantics          string `json:"digest_semantics"`
	HistoricalPublicationSHA bool   `json:"historical_publication_sha"`
}

type RecoveryVersion struct {
	VersionNumber              int       `json:"version_number"`
	RegistrationURI            string    `json:"registration_uri"`
	RegistrationSHA256         string    `json:"registration_sha256"`
	PreviousRegistrationSHA256 string    `json:"previous_registration_sha256,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	ChecksumVerified           bool      `json:"checksum_verified"`
}

type RecoveryPublishedRegistration struct {
	CurrentRegistrationSHA256 string `json:"current_registration_sha256"`
	CurrentChecksumVerified   bool   `json:"current_checksum_verified"`
}

type recoveryAgentWire struct {
	Version                string                         `json:"version"`
	AgentID                string                         `json:"agent_id"`
	Domain                 string                         `json:"domain"`
	LocalID                string                         `json:"local_id"`
	Status                 string                         `json:"status"`
	Classification         string                         `json:"classification"`
	SelfDescriptionVersion int64                          `json:"self_description_version"`
	Source                 RecoverySource                 `json:"source"`
	MigrationReadSHA256    string                         `json:"migration_read_sha256"`
	Provenance             RecoveryProvenance             `json:"provenance"`
	Versions               []RecoveryVersion              `json:"versions"`
	Declarations           json.RawMessage                `json:"declarations"`
	PublishedRegistration  *RecoveryPublishedRegistration `json:"published_registration,omitempty"`
}

func NewRecovery(client RawJSONDoer) RecoveryClient { return &recoveryClient{client: client} }

func DefaultRecovery() (RecoveryClient, error) {
	client, err := soulapi.Default()
	if err != nil {
		return nil, err
	}
	return NewRecovery(client), nil
}

type recoveryClient struct{ client RawJSONDoer }

func (c *recoveryClient) ReadRecoveryAgent(ctx context.Context, bearerToken, agentID string) (*RecoveryAgent, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("lesser-host recovery client is not configured")
	}
	agentID = strings.TrimSpace(agentID)
	if !recoveryAgentIDPattern.MatchString(agentID) {
		return nil, errors.New("lesser-host recovery agent id is invalid")
	}
	if strings.TrimSpace(bearerToken) == "" {
		return nil, errors.New("lesser-host instance key is required for recovery")
	}
	raw, err := c.client.DoJSONRaw(ctx, http.MethodGet, recoveryAgentPath+url.PathEscape(agentID), nil, bearerToken, nil)
	if err != nil {
		var apiErr *soulapi.APIError
		if errors.As(err, &apiErr) && apiErr != nil {
			return nil, sanitizeError(err)
		}
		return nil, errors.New("lesser-host recovery request failed")
	}
	return decodeRecoveryAgent(raw)
}

func decodeRecoveryAgent(raw []byte) (*RecoveryAgent, error) {
	var wire recoveryAgentWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("lesser-host recovery response is invalid: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("lesser-host recovery response has trailing JSON")
	}
	if err := validateRecoveryAgent(&wire); err != nil {
		return nil, err
	}
	return &RecoveryAgent{
		Version: wire.Version, AgentID: wire.AgentID, Domain: wire.Domain, LocalID: wire.LocalID,
		Status: wire.Status, Classification: wire.Classification,
		SelfDescriptionVersion: wire.SelfDescriptionVersion, Source: wire.Source,
		MigrationReadSHA256: wire.MigrationReadSHA256, Provenance: wire.Provenance,
		Versions:              append([]RecoveryVersion(nil), wire.Versions...),
		DeclarationsJSON:      append(json.RawMessage(nil), wire.Declarations...),
		PublishedRegistration: wire.PublishedRegistration,
	}, nil
}

func validateRecoveryAgent(w *recoveryAgentWire) error {
	if w == nil || w.Version != "1" || !recoveryAgentIDPattern.MatchString(w.AgentID) {
		return errors.New("lesser-host recovery response identity is invalid")
	}
	if strings.TrimSpace(w.Domain) == "" || len(w.Domain) > 253 || strings.Contains(w.Domain, "://") ||
		strings.TrimSpace(w.LocalID) == "" || len(w.LocalID) > 128 || w.Status != "active" || w.SelfDescriptionVersion < 1 {
		return errors.New("lesser-host recovery response actor binding is invalid")
	}
	if strings.TrimSpace(w.Source.RegistrationID) == "" || len(w.Source.RegistrationID) > 256 ||
		strings.TrimSpace(w.Source.ConversationID) == "" || len(w.Source.ConversationID) > 128 || w.Source.ProducedAt.IsZero() {
		return errors.New("lesser-host recovery response source is invalid")
	}
	if w.Provenance.Source != "hosted_genesis_exact_declarations" ||
		w.Provenance.DigestSemantics != "migration_read_sha256" || w.Provenance.HistoricalPublicationSHA {
		return errors.New("lesser-host recovery response provenance is invalid")
	}
	if !recoveryDigestPattern.MatchString(w.MigrationReadSHA256) || !validRecoveryDeclarations(w.Declarations) {
		return errors.New("lesser-host recovery response declarations are invalid")
	}
	digest := sha256.Sum256(w.Declarations)
	if "sha256:"+hex.EncodeToString(digest[:]) != w.MigrationReadSHA256 {
		return errors.New("lesser-host recovery declaration digest mismatch")
	}
	if err := validateRecoveryVersions(w); err != nil {
		return err
	}
	return nil
}

func validRecoveryDeclarations(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, name := range []string{"selfDescription", "capabilities", "boundaries", "transparency"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	var object map[string]any
	var list []any
	return json.Unmarshal(fields["selfDescription"], &object) == nil && len(object) > 0 &&
		json.Unmarshal(fields["capabilities"], &list) == nil && list != nil &&
		json.Unmarshal(fields["boundaries"], &list) == nil && list != nil &&
		json.Unmarshal(fields["transparency"], &object) == nil && object != nil
}

func validateRecoveryVersions(w *recoveryAgentWire) error {
	switch w.Classification {
	case RecoveryLegacyDeclarationsOnly:
		if len(w.Versions) != 0 || w.PublishedRegistration != nil {
			return errors.New("lesser-host legacy recovery publication evidence is invalid")
		}
		return nil
	case RecoveryPublishedArtifactVerified:
		if len(w.Versions) == 0 || w.PublishedRegistration == nil || !w.PublishedRegistration.CurrentChecksumVerified {
			return errors.New("lesser-host published recovery evidence is incomplete")
		}
	default:
		return errors.New("lesser-host recovery classification is invalid")
	}
	previous := ""
	for i, version := range w.Versions {
		if version.VersionNumber != i+1 || version.CreatedAt.IsZero() || !version.ChecksumVerified ||
			!recoverySHA256Pattern.MatchString(version.RegistrationSHA256) ||
			(i == 0 && version.PreviousRegistrationSHA256 != "") ||
			(i > 0 && version.PreviousRegistrationSHA256 != previous) {
			return errors.New("lesser-host recovery version chain is invalid")
		}
		expectedSuffix := "/registry/v1/agents/" + w.AgentID + "/versions/" + strconv.Itoa(version.VersionNumber) + "/registration.json"
		if !strings.HasPrefix(version.RegistrationURI, "s3://") || !strings.HasSuffix(version.RegistrationURI, expectedSuffix) {
			return errors.New("lesser-host recovery version uri is invalid")
		}
		previous = version.RegistrationSHA256
	}
	if !recoverySHA256Pattern.MatchString(w.PublishedRegistration.CurrentRegistrationSHA256) ||
		w.PublishedRegistration.CurrentRegistrationSHA256 != previous {
		return errors.New("lesser-host current registration checksum is invalid")
	}
	return nil
}
