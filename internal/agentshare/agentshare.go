// Package agentshare reads Lesser-owned per-agent share grants for actor MCP admission.
package agentshare

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory/v3"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	"github.com/theory-cloud/tabletheory/v3/pkg/session"
)

const (
	envAWSRegion            = "AWS_REGION"
	envTableName            = "LESSER_TABLE_NAME"
	userPartitionKeyPrefix  = "USER#"
	agentMetadataSortKey    = "METADATA"
	shareGrantSortKeyPrefix = "AGENT_SHARE#GRANTEE#"
)

var localUsernamePattern = regexp.MustCompile(`^[a-z0-9_-]{1,30}$`)

type grantRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentUsername   string     `theorydb:"attr:agentUsername" json:"agent_username"`
	GranteeUsername string     `theorydb:"attr:granteeUsername" json:"grantee_username"`
	GrantedBy       string     `theorydb:"attr:grantedBy" json:"granted_by"`
	GrantedAt       time.Time  `theorydb:"attr:grantedAt" json:"granted_at"`
	RevokedAt       *time.Time `theorydb:"attr:revokedAt,omitempty" json:"revoked_at,omitempty"`
}

func (grantRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envTableName))
}

// agentUserRecord is the minimal projection of lesser's storage.User agent
// record that carries the owner identity. Only the fields needed for owner
// resolution are read; nothing else from the user record is projected.
type agentUserRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username   string `theorydb:"attr:username" json:"username"`
	IsAgent    bool   `theorydb:"attr:isAgent" json:"is_agent"`
	AgentOwner string `theorydb:"attr:agentOwner" json:"agent_owner,omitempty"`
}

func (agentUserRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envTableName))
}

var newDB = func() (tablecore.DB, error) {
	return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
}

// SetDBFactoryForTests replaces the TableTheory client factory for a test.
func SetDBFactoryForTests(fn func() (tablecore.DB, error)) {
	if fn == nil {
		ResetForTests()
		return
	}
	newDB = fn
}

// ResetForTests restores the production TableTheory client factory.
func ResetForTests() {
	newDB = func() (tablecore.DB, error) {
		return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	}
}

// IsActive performs the authoritative, uncached active-grant lookup for one request.
func IsActive(ctx context.Context, agentUsername, granteeUsername string) (bool, error) {
	agentUsername, agentOK := normalizeLocalUsername(agentUsername)
	granteeUsername, granteeOK := normalizeLocalUsername(granteeUsername)
	if !agentOK || !granteeOK {
		return false, nil
	}
	if strings.TrimSpace(os.Getenv(envTableName)) == "" {
		return false, fmt.Errorf("LESSER_TABLE_NAME is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := newDB()
	if err != nil {
		return false, fmt.Errorf("create tabletheory client: %w", err)
	}

	expectedPK := userPartitionKeyPrefix + agentUsername
	expectedSK := shareGrantSortKeyPrefix + granteeUsername
	record := &grantRecord{}
	err = db.Model(&grantRecord{}).
		WithContext(ctx).
		Where("PK", "=", expectedPK).
		Where("SK", "=", expectedSK).
		ConsistentRead().
		First(record)
	switch {
	case err == nil:
		return validateActiveRecord(record, expectedPK, expectedSK, agentUsername, granteeUsername)
	case tableerrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("read agent share grant: %w", err)
	}
}

func validateActiveRecord(record *grantRecord, expectedPK, expectedSK, agentUsername, granteeUsername string) (bool, error) {
	if record == nil {
		return false, fmt.Errorf("malformed agent share grant")
	}
	if record.PK != expectedPK || record.SK != expectedSK ||
		record.AgentUsername != agentUsername || record.GranteeUsername != granteeUsername ||
		strings.TrimSpace(record.GrantedBy) == "" || record.GrantedAt.IsZero() {
		return false, fmt.Errorf("malformed agent share grant")
	}
	if record.RevokedAt != nil {
		return false, nil
	}
	return true, nil
}

// NormalizePrincipalUsername canonicalizes a lesser principal or DelegatedBy
// value to its bare local-username form: a leading "@" is stripped, the value
// is lowercased and trimmed. The owner path and the grantee path must compare
// DelegatedBy against the agent owner in this canonical form so that
// "@owner" and "owner" are treated identically.
func NormalizePrincipalUsername(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "@")
	return strings.ToLower(strings.TrimSpace(value))
}

// AgentOwner resolves the agent's owner (lesser storage.User.AgentOwner) via a
// strongly-consistent uncached read of USER#<agentUsername>/METADATA. It fails
// closed: an absent, malformed, or unreadable owner returns an error so callers
// never admit a request by guessing an owner.
func AgentOwner(ctx context.Context, agentUsername string) (string, error) {
	agentUsername, ok := normalizeLocalUsername(agentUsername)
	if !ok {
		return "", fmt.Errorf("invalid agent username %q", agentUsername)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := newDB()
	if err != nil {
		return "", fmt.Errorf("create tabletheory client: %w", err)
	}

	expectedPK := userPartitionKeyPrefix + agentUsername
	expectedSK := agentMetadataSortKey
	record := &agentUserRecord{}
	err = db.Model(&agentUserRecord{}).
		WithContext(ctx).
		Where("PK", "=", expectedPK).
		Where("SK", "=", expectedSK).
		ConsistentRead().
		First(record)
	switch {
	case err == nil:
		return validateAgentOwner(record, expectedPK, expectedSK, agentUsername)
	case tableerrors.IsNotFound(err):
		return "", fmt.Errorf("agent user record not found for %q", agentUsername)
	default:
		return "", fmt.Errorf("read agent owner: %w", err)
	}
}

func validateAgentOwner(record *agentUserRecord, expectedPK, expectedSK, agentUsername string) (string, error) {
	if record == nil {
		return "", fmt.Errorf("malformed agent user record")
	}
	if record.PK != expectedPK || record.SK != expectedSK || !record.IsAgent {
		return "", fmt.Errorf("malformed agent user record")
	}
	if normalized, ok := normalizeLocalUsername(record.Username); !ok || normalized != agentUsername {
		return "", fmt.Errorf("malformed agent user record")
	}
	owner := NormalizePrincipalUsername(record.AgentOwner)
	if owner == "" {
		return "", fmt.Errorf("agent owner is empty")
	}
	return owner, nil
}

func normalizeLocalUsername(username string) (string, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	return username, localUsernamePattern.MatchString(username)
}
