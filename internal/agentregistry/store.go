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

	// SourceHostGenesisFinalize identifies registry rows written by Body after
	// lesser-host has finalized and published a Host-owned genesis identity.
	// This is system-derived provenance, not caller-claimed agent creation.
	SourceHostGenesisFinalize = "host_genesis_finalize"

	// SourceAuthorityLesserHost records that the minted identity came from
	// lesser-host's HostedGenesisSession/finalization contract.
	SourceAuthorityLesserHost = "lesser_host"

	// SourceOperationAgentGenesisFinalize is the Ptah MCP operation that
	// observed the Host-finalized identity and wrote the registry row.
	SourceOperationAgentGenesisFinalize = "agent_genesis_finalize"
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
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	Account                string    `json:"account"`
	AgentID                string    `json:"agent_id"`
	Source                 string    `json:"source,omitempty"`
	SourceAuthority        string    `json:"source_authority,omitempty"`
	SourceOperation        string    `json:"source_operation,omitempty"`
	HostRegistrationID     string    `json:"host_registration_id,omitempty"`
	HostConversationID     string    `json:"host_conversation_id,omitempty"`
	Domain                 string    `json:"domain,omitempty"`
	LocalID                string    `json:"local_id,omitempty"`
	AuthorityModel         string    `json:"authority_model,omitempty"`
	AnchorState            string    `json:"anchor_state,omitempty"`
	OperationalBinding     string    `json:"operational_binding,omitempty"`
	LifecycleStatus        string    `json:"lifecycle_status,omitempty"`
	PublishedVersion       int64     `json:"published_version,omitempty"`
	SelfDescriptionVersion int64     `json:"self_description_version,omitempty"`
}

// CreateInput describes a new Ptah-created agent registry entry.
type CreateInput struct {
	Account string
	AgentID string
}

// FinalizedInput describes a Host-finalized minted agent registry projection.
// All fields must be derived from Host/Lesser responses or server-side
// invocation context. Callers never supply this struct directly.
type FinalizedInput struct {
	Account                string
	AgentID                string
	HostRegistrationID     string
	HostConversationID     string
	Domain                 string
	LocalID                string
	AuthorityModel         string
	AnchorState            string
	OperationalBinding     string
	LifecycleStatus        string
	PublishedVersion       int64
	SelfDescriptionVersion int64
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

// UpsertFinalized idempotently writes the account-scoped registry row for a
// Host-finalized minted agent. The identity and provenance are system-derived
// from lesser-host finalization output; this method is intentionally separate
// from Create so Ptah cannot blur Host-owned genesis with caller-claimed local
// creation. The returned boolean is true only when this call created the row.
func (s *Store) UpsertFinalized(ctx context.Context, in FinalizedInput) (*Agent, bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("agent registry store is nil")
	}
	validated, err := validateFinalizedInput(in)
	if err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	record := s.recordFor(validated.Account, validated.AgentID)
	record.RegistryCreatedAt = now
	record.RegistryUpdatedAt = now
	applyFinalizedFields(record, validated)

	err = s.db.Model(record).WithContext(ctx).IfNotExists().Create()
	switch {
	case err == nil:
		return record.toAgent(), true, nil
	case !tableerrors.IsConditionFailed(err):
		return nil, false, fmt.Errorf("create finalized agent registry record: %w", err)
	}

	updated := s.emptyRecord()
	builder := s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", accountPartitionKey(validated.Account)).
		Where("SK", "=", agentSortKey(validated.AgentID)).
		UpdateBuilder().
		Set("RegistryUpdatedAt", now).
		Set("Source", SourceHostGenesisFinalize).
		Set("SourceAuthority", SourceAuthorityLesserHost).
		Set("SourceOperation", SourceOperationAgentGenesisFinalize)
	if validated.HostRegistrationID != "" {
		builder = builder.Set("HostRegistrationID", validated.HostRegistrationID)
	}
	if validated.HostConversationID != "" {
		builder = builder.Set("HostConversationID", validated.HostConversationID)
	}
	if validated.Domain != "" {
		builder = builder.Set("Domain", validated.Domain)
	}
	if validated.LocalID != "" {
		builder = builder.Set("LocalID", validated.LocalID)
	}
	if validated.AuthorityModel != "" {
		builder = builder.Set("AuthorityModel", validated.AuthorityModel)
	}
	if validated.AnchorState != "" {
		builder = builder.Set("AnchorState", validated.AnchorState)
	}
	if validated.OperationalBinding != "" {
		builder = builder.Set("OperationalBinding", validated.OperationalBinding)
	}
	if validated.LifecycleStatus != "" {
		builder = builder.Set("LifecycleStatus", validated.LifecycleStatus)
	}
	if validated.PublishedVersion > 0 {
		builder = builder.Set("PublishedVersion", validated.PublishedVersion)
	}
	if validated.SelfDescriptionVersion > 0 {
		builder = builder.Set("SelfDescriptionVersion", validated.SelfDescriptionVersion)
	}
	if err := builder.ReturnValues("ALL_NEW").ExecuteWithResult(updated); err != nil {
		return nil, false, fmt.Errorf("update finalized agent registry record: %w", err)
	}
	return updated.toAgent(), false, nil
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

	Source                 string `theorydb:"attr:source" json:"source,omitempty"`
	SourceAuthority        string `theorydb:"attr:sourceAuthority" json:"source_authority,omitempty"`
	SourceOperation        string `theorydb:"attr:sourceOperation" json:"source_operation,omitempty"`
	HostRegistrationID     string `theorydb:"attr:hostRegistrationId" json:"host_registration_id,omitempty"`
	HostConversationID     string `theorydb:"attr:hostConversationId" json:"host_conversation_id,omitempty"`
	Domain                 string `theorydb:"attr:domain" json:"domain,omitempty"`
	LocalID                string `theorydb:"attr:localId" json:"local_id,omitempty"`
	AuthorityModel         string `theorydb:"attr:authorityModel" json:"authority_model,omitempty"`
	AnchorState            string `theorydb:"attr:anchorState" json:"anchor_state,omitempty"`
	OperationalBinding     string `theorydb:"attr:operationalBinding" json:"operational_binding,omitempty"`
	LifecycleStatus        string `theorydb:"attr:lifecycleStatus" json:"lifecycle_status,omitempty"`
	PublishedVersion       int64  `theorydb:"attr:publishedVersion" json:"published_version,omitempty"`
	SelfDescriptionVersion int64  `theorydb:"attr:selfDescriptionVersion" json:"self_description_version,omitempty"`
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
		Account:                normalizeAccount(r.Account),
		AgentID:                normalizeAgentID(r.AgentID),
		CreatedAt:              r.RegistryCreatedAt.UTC(),
		UpdatedAt:              r.RegistryUpdatedAt.UTC(),
		Source:                 strings.TrimSpace(r.Source),
		SourceAuthority:        strings.TrimSpace(r.SourceAuthority),
		SourceOperation:        strings.TrimSpace(r.SourceOperation),
		HostRegistrationID:     strings.TrimSpace(r.HostRegistrationID),
		HostConversationID:     strings.TrimSpace(r.HostConversationID),
		Domain:                 strings.TrimSpace(r.Domain),
		LocalID:                strings.TrimSpace(r.LocalID),
		AuthorityModel:         strings.TrimSpace(r.AuthorityModel),
		AnchorState:            strings.TrimSpace(r.AnchorState),
		OperationalBinding:     strings.TrimSpace(r.OperationalBinding),
		LifecycleStatus:        strings.TrimSpace(r.LifecycleStatus),
		PublishedVersion:       r.PublishedVersion,
		SelfDescriptionVersion: r.SelfDescriptionVersion,
	}
}

func validateFinalizedInput(in FinalizedInput) (FinalizedInput, error) {
	in.Account = normalizeAccount(in.Account)
	if in.Account == "" {
		return FinalizedInput{}, fmt.Errorf("account is required")
	}
	in.AgentID = normalizeAgentID(in.AgentID)
	if in.AgentID == "" {
		return FinalizedInput{}, fmt.Errorf("agent id is required")
	}
	in.HostRegistrationID = strings.TrimSpace(in.HostRegistrationID)
	in.HostConversationID = strings.TrimSpace(in.HostConversationID)
	in.Domain = strings.TrimSpace(in.Domain)
	in.LocalID = strings.TrimSpace(in.LocalID)
	in.AuthorityModel = strings.TrimSpace(in.AuthorityModel)
	in.AnchorState = strings.TrimSpace(in.AnchorState)
	in.OperationalBinding = strings.TrimSpace(in.OperationalBinding)
	in.LifecycleStatus = strings.TrimSpace(in.LifecycleStatus)
	if in.PublishedVersion < 0 {
		in.PublishedVersion = 0
	}
	if in.SelfDescriptionVersion < 0 {
		in.SelfDescriptionVersion = 0
	}
	return in, nil
}

func applyFinalizedFields(record *agentRecord, in FinalizedInput) {
	if record == nil {
		return
	}
	record.Source = SourceHostGenesisFinalize
	record.SourceAuthority = SourceAuthorityLesserHost
	record.SourceOperation = SourceOperationAgentGenesisFinalize
	record.HostRegistrationID = in.HostRegistrationID
	record.HostConversationID = in.HostConversationID
	record.Domain = in.Domain
	record.LocalID = in.LocalID
	record.AuthorityModel = in.AuthorityModel
	record.AnchorState = in.AnchorState
	record.OperationalBinding = in.OperationalBinding
	record.LifecycleStatus = in.LifecycleStatus
	record.PublishedVersion = in.PublishedVersion
	record.SelfDescriptionVersion = in.SelfDescriptionVersion
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
