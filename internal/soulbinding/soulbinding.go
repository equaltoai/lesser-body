package soulbinding

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/theory-cloud/tabletheory"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/pkg/errors"
	"github.com/theory-cloud/tabletheory/pkg/session"
)

const (
	envAWSRegion             = "AWS_REGION"
	envTableName             = "LESSER_TABLE_NAME"
	skSoulBodyBinding        = "SOUL_BODY_BINDING"
	soulBodyBindingUserPKPre = "SOUL_BODY_BINDING_USERNAME#"
)

type usernameBindingRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username string `theorydb:"attr:username" json:"username"`
	AgentID  string `theorydb:"attr:agentId" json:"agent_id"`
}

func (usernameBindingRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envTableName))
}

var newDB = func() (tablecore.DB, error) {
	return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
}

func SetDBFactoryForTests(fn func() (tablecore.DB, error)) {
	if fn == nil {
		ResetForTests()
		return
	}
	newDB = fn
}

func ResetForTests() {
	newDB = func() (tablecore.DB, error) {
		return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	}
}

func ResolveAgentID(ctx context.Context, username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return "", nil
	}
	if strings.TrimSpace(os.Getenv(envTableName)) == "" {
		return "", fmt.Errorf("LESSER_TABLE_NAME is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := newDB()
	if err != nil {
		return "", fmt.Errorf("create tabletheory client: %w", err)
	}

	record := &usernameBindingRecord{}
	err = db.Model(&usernameBindingRecord{}).
		WithContext(ctx).
		Where("PK", "=", soulBodyBindingUsernamePartitionKey(username)).
		Where("SK", "=", skSoulBodyBinding).
		First(record)
	switch {
	case err == nil:
		return normalizeAgentID(record.AgentID), nil
	case tableerrors.IsNotFound(err):
		return "", nil
	default:
		return "", fmt.Errorf("read soul binding: %w", err)
	}
}

func soulBodyBindingUsernamePartitionKey(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return ""
	}
	return soulBodyBindingUserPKPre + username
}

func normalizeAgentID(agentID string) string {
	return strings.ToLower(strings.TrimSpace(agentID))
}
