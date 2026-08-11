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

func normalizeLocalUsername(username string) (string, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	return username, localUsernamePattern.MatchString(username)
}
