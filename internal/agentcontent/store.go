package agentcontent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/theory-cloud/tabletheory/v2"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
)

const (
	// EnvInstanceContentTable is the body-owned instance-plane content table.
	// It is provisioned by this repo's CDK stack as INSTANCE_CONTENT_TABLE and
	// must not be confused with Lesser's LESSER_TABLE_NAME actor data table.
	EnvInstanceContentTable = "INSTANCE_CONTENT_TABLE"

	envAWSRegion = "AWS_REGION"

	accountPKPrefix = "ACCOUNT#"
	agentPKSegment  = "#AGENT#"
	contentSKPrefix = "CONTENT#"

	// MaxAgentSoulBytes bounds a single draft soul body. The default is kept
	// below DynamoDB's item limit so future metadata can fit beside the content.
	MaxAgentSoulBytes = 64 * 1024

	// MaxAgentInstructionsBytes bounds a single draft instructions body. The
	// default is kept below DynamoDB's item limit so future metadata can fit
	// beside the content.
	MaxAgentInstructionsBytes = 64 * 1024

	updateRetryLimit = 3
)

// ContentType identifies the exact Ptah-authored content families this store
// accepts. Unknown types fail closed with ErrInvalidContentType.
type ContentType string

const (
	ContentTypeAgentSoul         ContentType = "agent_soul"
	ContentTypeAgentInstructions ContentType = "agent_instructions"
)

// LifecycleState is the record lifecycle exposed to later Ptah authoring tools.
type LifecycleState string

const (
	LifecycleStateDraft    LifecycleState = "draft"
	LifecycleStateArchived LifecycleState = "archived"
)

var (
	// ErrContentNotFound is returned when the requested account/agent/content
	// record is absent. Cross-account reads intentionally map here.
	ErrContentNotFound = errors.New("agent content not found")

	// ErrInvalidContentType is returned for any content type outside AgentSoul
	// and AgentInstructions.
	ErrInvalidContentType = errors.New("invalid agent content type")

	// ErrInvalidLifecycleState is returned when persisted state is not one of
	// draft or archived.
	ErrInvalidLifecycleState = errors.New("invalid agent content lifecycle state")

	// ErrContentTooLarge is returned when draft content exceeds the configured
	// per-type byte limit.
	ErrContentTooLarge = errors.New("agent content too large")

	// ErrMissingUpdatedBySubjectID is returned when a write does not carry the
	// source-backed subject id supplied by the service/tool layer.
	ErrMissingUpdatedBySubjectID = errors.New("updated by subject id is required")

	// ErrContentConflict is returned when a conditional write loses an optimistic
	// concurrency race after bounded retries.
	ErrContentConflict = errors.New("agent content write conflict")
)

// SizeError describes a content-size validation failure while preserving
// errors.Is(err, ErrContentTooLarge).
type SizeError struct {
	Type   ContentType
	Limit  int
	Actual int
}

func (e *SizeError) Error() string {
	if e == nil {
		return ErrContentTooLarge.Error()
	}
	return fmt.Sprintf("%s: type %s has %d bytes, limit %d", ErrContentTooLarge, e.Type, e.Actual, e.Limit)
}

func (e *SizeError) Unwrap() error { return ErrContentTooLarge }

// Record is the current account-scoped draft/archived content record for one
// account, agent, and content type.
type Record struct {
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Account            string         `json:"account"`
	AgentID            string         `json:"agent_id"`
	Type               ContentType    `json:"type"`
	Content            string         `json:"content"`
	Version            int64          `json:"version"`
	LifecycleState     LifecycleState `json:"lifecycle_state"`
	UpdatedBySubjectID string         `json:"updated_by_subject_id"`
}

// UpsertInput describes a create-or-update draft write. UpdatedBySubjectID is
// intentionally a source-backed service input so later Ptah handlers can pass
// Claims.Subject (or another stable authenticated subject) without this store
// reaching into auth context itself.
type UpsertInput struct {
	Account            string
	AgentID            string
	Type               ContentType
	Content            string
	UpdatedBySubjectID string
}

// ArchiveInput describes an idempotent lifecycle transition to archived.
type ArchiveInput struct {
	Account            string
	AgentID            string
	Type               ContentType
	UpdatedBySubjectID string
}

// Store persists Ptah-authored versioned drafts in a body-owned instance table.
type Store struct {
	db        tablecore.DB
	tableName string
}

// NewStore constructs a Store over an injected TableTheory DB. tableName should
// be the configured INSTANCE_CONTENT_TABLE value.
func NewStore(db tablecore.DB, tableName string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("agent content db is required")
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceContentTable)
	}
	return &Store{db: db, tableName: tableName}, nil
}

// Default creates the production TableTheory-backed content store from process
// configuration.
func Default() (*Store, error) {
	tableName := strings.TrimSpace(os.Getenv(EnvInstanceContentTable))
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceContentTable)
	}

	db, err := tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}
	return NewStore(db, tableName)
}

// Upsert creates or updates draft content and increments this content type's
// version monotonically. Soul and instructions counters are independent because
// they are stored as separate sort-key records.
func (s *Store) Upsert(ctx context.Context, in UpsertInput) (*Record, error) {
	if s == nil {
		return nil, fmt.Errorf("agent content store is nil")
	}
	validated, err := validateUpsertInput(in)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	created := s.recordFor(validated.account, validated.agentID, validated.contentType)
	created.Content = in.Content
	created.Version = 1
	created.LifecycleState = string(LifecycleStateDraft)
	created.ContentCreatedAt = now
	created.ContentUpdatedAt = now
	created.UpdatedBySubjectID = validated.updatedBySubjectID

	err = s.db.Model(created).WithContext(ctx).IfNotExists().Create()
	switch {
	case err == nil:
		return created.toRecord()
	case !tableerrors.IsConditionFailed(err):
		return nil, fmt.Errorf("create agent content record: %w", err)
	}

	for attempt := 0; attempt < updateRetryLimit; attempt++ {
		current, err := s.loadRecord(ctx, validated.account, validated.agentID, validated.contentType)
		if err != nil {
			return nil, err
		}

		updated := s.emptyRecord()
		err = s.db.Model(s.emptyRecord()).
			WithContext(ctx).
			Where("PK", "=", contentPartitionKey(validated.account, validated.agentID)).
			Where("SK", "=", contentSortKey(validated.contentType)).
			UpdateBuilder().
			Set("Content", in.Content).
			Set("LifecycleState", string(LifecycleStateDraft)).
			Set("ContentUpdatedAt", now).
			Set("UpdatedBySubjectID", validated.updatedBySubjectID).
			Add("Version", 1).
			ConditionVersion(current.Version).
			ReturnValues("ALL_NEW").
			ExecuteWithResult(updated)
		switch {
		case err == nil:
			return updated.toRecord()
		case tableerrors.IsConditionFailed(err):
			continue
		default:
			return nil, fmt.Errorf("update agent content record: %w", err)
		}
	}

	return nil, fmt.Errorf("%w: upsert agent content", ErrContentConflict)
}

// Get returns the current record for an account, agent, and content type. A
// wrong account and an absent record both map to ErrContentNotFound.
func (s *Store) Get(ctx context.Context, account string, agentID string, contentType ContentType) (*Record, error) {
	if s == nil {
		return nil, fmt.Errorf("agent content store is nil")
	}
	account = normalizeAccount(account)
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	typ, err := normalizeContentType(contentType)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	record, err := s.loadRecord(ctx, account, agentID, typ)
	if err != nil {
		return nil, err
	}
	return record.toRecord()
}

// Archive idempotently marks the current content record archived. It preserves
// the content version while updating lifecycle and audit fields.
func (s *Store) Archive(ctx context.Context, in ArchiveInput) (*Record, error) {
	if s == nil {
		return nil, fmt.Errorf("agent content store is nil")
	}
	validated, err := validateArchiveInput(in)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	for attempt := 0; attempt < updateRetryLimit; attempt++ {
		current, err := s.loadRecord(ctx, validated.account, validated.agentID, validated.contentType)
		if err != nil {
			return nil, err
		}

		updated := s.emptyRecord()
		err = s.db.Model(s.emptyRecord()).
			WithContext(ctx).
			Where("PK", "=", contentPartitionKey(validated.account, validated.agentID)).
			Where("SK", "=", contentSortKey(validated.contentType)).
			UpdateBuilder().
			Set("LifecycleState", string(LifecycleStateArchived)).
			Set("ContentUpdatedAt", now).
			Set("UpdatedBySubjectID", validated.updatedBySubjectID).
			ConditionVersion(current.Version).
			ReturnValues("ALL_NEW").
			ExecuteWithResult(updated)
		switch {
		case err == nil:
			return updated.toRecord()
		case tableerrors.IsConditionFailed(err):
			continue
		default:
			return nil, fmt.Errorf("archive agent content record: %w", err)
		}
	}

	return nil, fmt.Errorf("%w: archive agent content", ErrContentConflict)
}

func (s *Store) loadRecord(ctx context.Context, account string, agentID string, contentType ContentType) (*contentRecord, error) {
	record := s.emptyRecord()
	err := s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", contentPartitionKey(account, agentID)).
		Where("SK", "=", contentSortKey(contentType)).
		First(record)
	switch {
	case err == nil:
		if _, err := record.toRecord(); err != nil {
			return nil, err
		}
		return record, nil
	case tableerrors.IsNotFound(err):
		return nil, ErrContentNotFound
	default:
		return nil, fmt.Errorf("get agent content record: %w", err)
	}
}

func (s *Store) emptyRecord() *contentRecord {
	return &contentRecord{tableName: s.tableName}
}

func (s *Store) recordFor(account string, agentID string, contentType ContentType) *contentRecord {
	return &contentRecord{
		tableName:   s.tableName,
		PK:          contentPartitionKey(account, agentID),
		SK:          contentSortKey(contentType),
		Account:     normalizeAccount(account),
		AgentID:     normalizeAgentID(agentID),
		ContentType: string(contentType),
	}
}

type contentRecord struct {
	tableName string

	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	ContentCreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ContentUpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	Account            string    `theorydb:"attr:account" json:"account"`
	AgentID            string    `theorydb:"attr:agentId" json:"agent_id"`
	ContentType        string    `theorydb:"attr:contentType" json:"content_type"`
	Content            string    `theorydb:"attr:content" json:"content"`
	Version            int64     `theorydb:"version,attr:version" json:"version"`
	LifecycleState     string    `theorydb:"attr:lifecycleState" json:"lifecycle_state"`
	UpdatedBySubjectID string    `theorydb:"attr:updatedBySubjectId" json:"updated_by_subject_id"`
}

func (r contentRecord) TableName() string {
	if tableName := strings.TrimSpace(r.tableName); tableName != "" {
		return tableName
	}
	return strings.TrimSpace(os.Getenv(EnvInstanceContentTable))
}

func (r *contentRecord) toRecord() (*Record, error) {
	if r == nil {
		return nil, nil
	}
	typ, err := normalizeContentType(ContentType(r.ContentType))
	if err != nil {
		return nil, err
	}
	state, err := normalizeLifecycleState(LifecycleState(r.LifecycleState))
	if err != nil {
		return nil, err
	}
	return &Record{
		Account:            normalizeAccount(r.Account),
		AgentID:            normalizeAgentID(r.AgentID),
		Type:               typ,
		Content:            r.Content,
		Version:            r.Version,
		LifecycleState:     state,
		CreatedAt:          r.ContentCreatedAt.UTC(),
		UpdatedAt:          r.ContentUpdatedAt.UTC(),
		UpdatedBySubjectID: strings.TrimSpace(r.UpdatedBySubjectID),
	}, nil
}

type validatedWriteInput struct {
	account            string
	agentID            string
	contentType        ContentType
	updatedBySubjectID string
}

func validateUpsertInput(in UpsertInput) (validatedWriteInput, error) {
	validated, err := validateWriteScope(in.Account, in.AgentID, in.Type, in.UpdatedBySubjectID)
	if err != nil {
		return validatedWriteInput{}, err
	}
	if err := validateContentSize(validated.contentType, in.Content); err != nil {
		return validatedWriteInput{}, err
	}
	return validated, nil
}

func validateArchiveInput(in ArchiveInput) (validatedWriteInput, error) {
	return validateWriteScope(in.Account, in.AgentID, in.Type, in.UpdatedBySubjectID)
}

func validateWriteScope(account string, agentID string, contentType ContentType, updatedBySubjectID string) (validatedWriteInput, error) {
	account = normalizeAccount(account)
	if account == "" {
		return validatedWriteInput{}, fmt.Errorf("account is required")
	}
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return validatedWriteInput{}, fmt.Errorf("agent id is required")
	}
	typ, err := normalizeContentType(contentType)
	if err != nil {
		return validatedWriteInput{}, err
	}
	updatedBySubjectID = strings.TrimSpace(updatedBySubjectID)
	if updatedBySubjectID == "" {
		return validatedWriteInput{}, ErrMissingUpdatedBySubjectID
	}
	return validatedWriteInput{
		account:            account,
		agentID:            agentID,
		contentType:        typ,
		updatedBySubjectID: updatedBySubjectID,
	}, nil
}

func validateContentSize(contentType ContentType, content string) error {
	limit := maxContentBytes(contentType)
	actual := len([]byte(content))
	if actual > limit {
		return &SizeError{Type: contentType, Limit: limit, Actual: actual}
	}
	return nil
}

func maxContentBytes(contentType ContentType) int {
	switch contentType {
	case ContentTypeAgentSoul:
		return MaxAgentSoulBytes
	case ContentTypeAgentInstructions:
		return MaxAgentInstructionsBytes
	default:
		return 0
	}
}

func contentPartitionKey(account string, agentID string) string {
	account = normalizeAccount(account)
	agentID = normalizeAgentID(agentID)
	if account == "" || agentID == "" {
		return ""
	}
	return accountPKPrefix + account + agentPKSegment + agentID
}

func contentSortKey(contentType ContentType) string {
	typ, err := normalizeContentType(contentType)
	if err != nil {
		return ""
	}
	return contentSKPrefix + string(typ)
}

func normalizeContentType(contentType ContentType) (ContentType, error) {
	token := normalizeEnumToken(string(contentType))
	switch token {
	case "agentsoul":
		return ContentTypeAgentSoul, nil
	case "agentinstructions":
		return ContentTypeAgentInstructions, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidContentType, strings.TrimSpace(string(contentType)))
	}
}

func normalizeLifecycleState(state LifecycleState) (LifecycleState, error) {
	switch strings.ToLower(strings.TrimSpace(string(state))) {
	case string(LifecycleStateDraft):
		return LifecycleStateDraft, nil
	case string(LifecycleStateArchived):
		return LifecycleStateArchived, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidLifecycleState, strings.TrimSpace(string(state)))
	}
}

func normalizeEnumToken(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func normalizeAccount(account string) string {
	return strings.ToLower(strings.TrimSpace(account))
}

func normalizeAgentID(agentID string) string {
	return strings.TrimSpace(agentID)
}
