package agentregistry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
)

const (
	// EnvInstanceRegistryTable is the body-owned instance-plane registry table.
	// It is provisioned by this repo's CDK stack as INSTANCE_REGISTRY_TABLE and
	// must not be confused with Lesser's LESSER_TABLE_NAME actor data table.
	EnvInstanceRegistryTable = "INSTANCE_REGISTRY_TABLE"

	envAWSRegion = "AWS_REGION"

	accountPKPrefix = "ACCOUNT#"
	agentSKPrefix   = "AGENT#"

	// DefaultListLimit is the page size used when callers do not request one.
	DefaultListLimit = 25

	// MaxListLimit is the largest page size accepted by the registry list API.
	MaxListLimit = 100
)

var (
	// ErrAgentAlreadyExists is returned when a create attempts to write an
	// existing (account, agentID) registry key.
	ErrAgentAlreadyExists = errors.New("agent already exists")

	// ErrAgentNotFound is returned when a registry entry is absent for the
	// supplied account scope and agent id. Cross-account reads intentionally map
	// here rather than disclosing that the agent id exists under another account.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrInvalidCursor is returned when a pagination cursor cannot be decoded by
	// TableTheory for this account-scoped registry query.
	ErrInvalidCursor = errors.New("invalid cursor")

	// ErrInvalidLimit is returned when callers request a nonsensical or
	// unsupported registry list page size.
	ErrInvalidLimit = errors.New("invalid limit")
)

// Agent is the account-scoped registry projection for a Ptah-created agent.
type Agent struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Account   string    `json:"account"`
	AgentID   string    `json:"agent_id"`
}

// CreateInput describes a new Ptah-created agent registry entry.
type CreateInput struct {
	Account string
	AgentID string
}

// ListInput describes an account-scoped registry list request.
type ListInput struct {
	Account string
	Cursor  string
	Limit   int
}

// ListResult is a single account-scoped registry page.
type ListResult struct {
	Agents     []*Agent `json:"agents"`
	NextCursor string   `json:"next_cursor"`
	HasMore    bool     `json:"has_more"`
	Count      int      `json:"count"`
}

// Store persists Ptah-created agent registry entries in a body-owned table.
type Store struct {
	db        tablecore.DB
	tableName string
}

// NewStore constructs a Store over an injected TableTheory DB. tableName should
// be the configured INSTANCE_REGISTRY_TABLE value.
func NewStore(db tablecore.DB, tableName string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("agent registry db is required")
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceRegistryTable)
	}
	return &Store{db: db, tableName: tableName}, nil
}

// Default creates the production TableTheory-backed registry store from
// process configuration.
func Default() (*Store, error) {
	tableName := strings.TrimSpace(os.Getenv(EnvInstanceRegistryTable))
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceRegistryTable)
	}

	db, err := tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}
	return NewStore(db, tableName)
}

// Create inserts a new registry record guarded by TableTheory's conditional
// create semantics. A duplicate account/agent key is mapped to
// ErrAgentAlreadyExists.
func (s *Store) Create(ctx context.Context, in CreateInput) (*Agent, error) {
	if s == nil {
		return nil, fmt.Errorf("agent registry store is nil")
	}
	account := normalizeAccount(in.Account)
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	agentID := normalizeAgentID(in.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	record := s.recordFor(account, agentID)
	record.RegistryCreatedAt = now
	record.RegistryUpdatedAt = now

	err := s.db.Model(record).WithContext(ctx).IfNotExists().Create()
	switch {
	case err == nil:
		return record.toAgent(), nil
	case tableerrors.IsConditionFailed(err):
		return nil, fmt.Errorf("%w: account %q agent %q", ErrAgentAlreadyExists, account, agentID)
	default:
		return nil, fmt.Errorf("create agent registry record: %w", err)
	}
}

// Get returns the registry record for account and agentID. Looking up an agent
// id under the wrong account returns ErrAgentNotFound.
func (s *Store) Get(ctx context.Context, account string, agentID string) (*Agent, error) {
	if s == nil {
		return nil, fmt.Errorf("agent registry store is nil")
	}
	account = normalizeAccount(account)
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	record := s.emptyRecord()
	err := s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", accountPartitionKey(account)).
		Where("SK", "=", agentSortKey(agentID)).
		First(record)
	switch {
	case err == nil:
		return record.toAgent(), nil
	case tableerrors.IsNotFound(err):
		return nil, fmt.Errorf("%w: account %q agent %q", ErrAgentNotFound, account, agentID)
	default:
		return nil, fmt.Errorf("get agent registry record: %w", err)
	}
}

// List returns a paginated page of registry records for one account partition.
// It uses TableTheory query pagination over ACCOUNT#<account> and never scans or
// accepts a caller-supplied account override outside this store API.
func (s *Store) List(ctx context.Context, in ListInput) (*ListResult, error) {
	if s == nil {
		return nil, fmt.Errorf("agent registry store is nil")
	}
	account := normalizeAccount(in.Account)
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	limit, err := normalizeListLimit(in.Limit)
	if err != nil {
		return nil, err
	}
	cursor := strings.TrimSpace(in.Cursor)
	if ctx == nil {
		ctx = context.Background()
	}

	var records []agentRecord
	query := s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", accountPartitionKey(account)).
		Where("SK", "begins_with", agentSKPrefix).
		Limit(limit)
	if cursor != "" {
		if err := query.SetCursor(cursor); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
	}

	page, err := query.AllPaginated(&records)
	if err != nil {
		if cursor != "" && strings.Contains(strings.ToLower(err.Error()), "invalid cursor") {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		return nil, fmt.Errorf("list agent registry records: %w", err)
	}

	agents := make([]*Agent, 0, len(records))
	for i := range records {
		agents = append(agents, records[i].toAgent())
	}

	result := &ListResult{
		Agents: agents,
		Count:  len(agents),
	}
	if page != nil {
		result.NextCursor = page.NextCursor
		result.HasMore = page.HasMore
		if page.Count > 0 || len(agents) == 0 {
			result.Count = page.Count
		}
	}
	return result, nil
}

func (s *Store) emptyRecord() *agentRecord {
	return &agentRecord{tableName: s.tableName}
}

func (s *Store) recordFor(account string, agentID string) *agentRecord {
	return &agentRecord{
		tableName: s.tableName,
		PK:        accountPartitionKey(account),
		SK:        agentSortKey(agentID),
		Account:   account,
		AgentID:   agentID,
	}
}

type agentRecord struct {
	tableName string

	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	Account           string    `theorydb:"attr:account" json:"account"`
	AgentID           string    `theorydb:"attr:agentId" json:"agent_id"`
	RegistryCreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	RegistryUpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

func (r agentRecord) TableName() string {
	if tableName := strings.TrimSpace(r.tableName); tableName != "" {
		return tableName
	}
	return strings.TrimSpace(os.Getenv(EnvInstanceRegistryTable))
}

func (r *agentRecord) toAgent() *Agent {
	if r == nil {
		return nil
	}
	return &Agent{
		Account:   normalizeAccount(r.Account),
		AgentID:   normalizeAgentID(r.AgentID),
		CreatedAt: r.RegistryCreatedAt.UTC(),
		UpdatedAt: r.RegistryUpdatedAt.UTC(),
	}
}

func accountPartitionKey(account string) string {
	account = normalizeAccount(account)
	if account == "" {
		return ""
	}
	return accountPKPrefix + account
}

func agentSortKey(agentID string) string {
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return ""
	}
	return agentSKPrefix + agentID
}

func normalizeAccount(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

func normalizeAgentID(agentID string) string {
	return strings.TrimSpace(agentID)
}

func normalizeListLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultListLimit, nil
	case limit < 0:
		return 0, fmt.Errorf("%w: limit must be positive", ErrInvalidLimit)
	case limit > MaxListLimit:
		return 0, fmt.Errorf("%w: limit must be <= %d", ErrInvalidLimit, MaxListLimit)
	default:
		return limit, nil
	}
}
