package trustconfig

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
)

const (
	envAWSRegion = "AWS_REGION"
	envTableName = "LESSER_TABLE_NAME"

	instanceConfigPK = "INSTANCE#CONFIG"
	skTrustConfig    = "TRUST_CONFIG"
)

type Effective struct {
	TrustBaseURL         string
	AttestationsBaseURL  string
	InstanceKeySecretARN string
	Present              bool
}

type trustConfigRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Managed  *trustConfigLayer `theorydb:"attr:managed" json:"managed"`
	Override *trustConfigPatch `theorydb:"attr:override,omitempty" json:"override,omitempty"`

	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

type trustConfigLayer struct {
	BaseURL              string `theorydb:"attr:baseURL,omitempty" json:"base_url,omitempty"`
	AttestationsURL      string `theorydb:"attr:attestationsURL,omitempty" json:"attestations_url,omitempty"`
	InstanceKeySecretARN string `theorydb:"attr:instanceKeySecretARN,omitempty" json:"instance_key_secret_arn,omitempty"`
}

type trustConfigPatch struct {
	BaseURL              *string `theorydb:"attr:baseURL,omitempty" json:"base_url,omitempty"`
	AttestationsURL      *string `theorydb:"attr:attestationsURL,omitempty" json:"attestations_url,omitempty"`
	InstanceKeySecretARN *string `theorydb:"attr:instanceKeySecretARN,omitempty" json:"instance_key_secret_arn,omitempty"`
}

func (trustConfigRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envTableName))
}

var defaultConfig struct {
	once sync.Once
	cfg  *Effective
	err  error
}

var newDB = func() (tablecore.DB, error) {
	return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
}

func Default(ctx context.Context) (*Effective, error) {
	defaultConfig.once.Do(func() {
		defaultConfig.cfg, defaultConfig.err = load(ctx)
	})
	return defaultConfig.cfg, defaultConfig.err
}

func ResetForTests() {
	defaultConfig = struct {
		once sync.Once
		cfg  *Effective
		err  error
	}{}
	newDB = func() (tablecore.DB, error) {
		return tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	}
}

func load(ctx context.Context) (*Effective, error) {
	if strings.TrimSpace(os.Getenv(envTableName)) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := newDB()
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}

	record := &trustConfigRecord{}
	err = db.Model(&trustConfigRecord{}).
		WithContext(ctx).
		Where("PK", "=", instanceConfigPK).
		Where("SK", "=", skTrustConfig).
		First(record)
	switch {
	case err == nil:
		return effectiveFromRecord(record), nil
	case tableerrors.IsNotFound(err):
		return &Effective{}, nil
	default:
		return nil, fmt.Errorf("read trust config: %w", err)
	}
}

func effectiveFromRecord(record *trustConfigRecord) *Effective {
	if record == nil {
		return &Effective{}
	}

	out := &Effective{Present: true}
	if record.Managed != nil {
		out.TrustBaseURL = normalizeURL(record.Managed.BaseURL)
		out.AttestationsBaseURL = normalizeURL(record.Managed.AttestationsURL)
		out.InstanceKeySecretARN = strings.TrimSpace(record.Managed.InstanceKeySecretARN)
	}
	if record.Override != nil {
		if record.Override.BaseURL != nil {
			out.TrustBaseURL = normalizeURL(*record.Override.BaseURL)
		}
		if record.Override.AttestationsURL != nil {
			out.AttestationsBaseURL = normalizeURL(*record.Override.AttestationsURL)
		}
		if record.Override.InstanceKeySecretARN != nil {
			out.InstanceKeySecretARN = strings.TrimSpace(*record.Override.InstanceKeySecretARN)
		}
	}
	if out.AttestationsBaseURL == "" {
		out.AttestationsBaseURL = out.TrustBaseURL
	}
	return out
}

func normalizeURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}
