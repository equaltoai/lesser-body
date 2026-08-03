package agentcontent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/theory-cloud/tabletheory/v3"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	"github.com/theory-cloud/tabletheory/v3/pkg/session"
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
	LifecycleStateDraft     LifecycleState = "draft"
	LifecycleStatePublished LifecycleState = "published"
	LifecycleStateArchived  LifecycleState = "archived"
)

var (
	// ErrContentNotFound is returned when the requested account/agent/content
	// record is absent. Cross-account reads intentionally map here.
	ErrContentNotFound = errors.New("agent content not found")

	// ErrInvalidContentType is returned for any content type outside AgentSoul
	// and AgentInstructions.
	ErrInvalidContentType = errors.New("invalid agent content type")

	// ErrInvalidLifecycleState is returned when persisted state is not one of
	// draft, published, or archived.
	ErrInvalidLifecycleState = errors.New("invalid agent content lifecycle state")

	// ErrInvalidLifecycleTransition is returned when a caller attempts a
	// transition outside the soul-document v2 state machine.
	ErrInvalidLifecycleTransition = errors.New("invalid agent content lifecycle transition")

	// ErrContentTooLarge is returned when draft content exceeds the configured
	// per-type byte limit.
	ErrContentTooLarge = errors.New("agent content too large")

	// ErrMissingUpdatedBySubjectID is returned when a write does not carry the
	// source-backed subject id supplied by the service/tool layer.
	ErrMissingUpdatedBySubjectID = errors.New("updated by subject id is required")

	// ErrContentConflict is returned when a conditional write loses an optimistic
	// concurrency race after bounded retries.
	ErrContentConflict = errors.New("agent content write conflict")

	// ErrSoulRewriteRequired is returned when a lifecycle action encounters a
	// pre-v2 opaque agent_soul row. The owner must rewrite it through the v2
	// authoring path before it can be published or archived.
	ErrSoulRewriteRequired = errors.New("agent soul rewrite required")
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

// TransitionError preserves a typed lifecycle rejection while naming the
// explicit action the caller should take.
type TransitionError struct {
	Action ContentAction
	From   LifecycleState
	To     LifecycleState
}

func (e *TransitionError) Error() string {
	if e == nil {
		return ErrInvalidLifecycleTransition.Error()
	}
	return fmt.Sprintf("%s: action %s cannot transition %s to %s", ErrInvalidLifecycleTransition, e.Action, e.From, e.To)
}

func (e *TransitionError) Unwrap() error { return ErrInvalidLifecycleTransition }

// ContentAction names lifecycle writes for transition errors and immutable
// history rows.
type ContentAction string

const (
	ContentActionUpsert  ContentAction = "upsert"
	ContentActionPublish ContentAction = "publish"
	ContentActionArchive ContentAction = "archive"
)

// SoulRewriteRequiredError preserves the lifecycle action rejected for a
// pre-v2 opaque row and names the exact Ptah repair sequence.
type SoulRewriteRequiredError struct {
	Action ContentAction
}

func (e *SoulRewriteRequiredError) Error() string {
	if e == nil {
		return ErrSoulRewriteRequired.Error()
	}
	return fmt.Sprintf(
		"%s: cannot %s a pre-v2 opaque agent_soul; rewrite via agent_soul_upsert, then publish via agent_soul_publish",
		ErrSoulRewriteRequired,
		e.Action,
	)
}

func (e *SoulRewriteRequiredError) Unwrap() error { return ErrSoulRewriteRequired }

// Record is the current account-scoped content projection for one account,
// agent, and content type. AgentSoul records include the closed v2 Document;
// Content remains a compatibility alias for document.body.
type Record struct {
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Account            string         `json:"account"`
	AgentID            string         `json:"agent_id"`
	Type               ContentType    `json:"type"`
	Content            string         `json:"content"`
	Version            int64          `json:"version"`
	SoulVersion        int64          `json:"soul_version,omitempty"`
	LifecycleState     LifecycleState `json:"lifecycle_state"`
	UpdatedBySubjectID string         `json:"updated_by_subject_id"`
	Document           *SoulDocument  `json:"document,omitempty"`
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
	SoulDocument       *SoulDocument
	UpdatedBySubjectID string
}

// PublishInput describes an explicit owner-authorized draft-to-published
// transition for an agent_soul document.
type PublishInput struct {
	Account            string
	AgentID            string
	UpdatedBySubjectID string
}

// SeedPublishedInput is the deterministic Ptah genesis application path. It
// creates the exact finalized declaration only when no current soul exists,
// then publishes it; replays of the identical source repair a partial draft or
// return the already-published snapshot without creating another version.
type SeedPublishedInput struct {
	Account            string
	AgentID            string
	SoulDocument       *SoulDocument
	UpdatedBySubjectID string
}

// SeedInstructionsInput is the deterministic Ptah genesis application path for
// the initial agent_instructions draft. It is create-only: any existing draft,
// including an owner-authored replacement, wins unchanged.
type SeedInstructionsInput struct {
	Account            string
	AgentID            string
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
	db        tablecore.ExtendedDB
	tableName string
}

// NewStore constructs a Store over an injected TableTheory DB. tableName should
// be the configured INSTANCE_CONTENT_TABLE value.
func NewStore(db tablecore.ExtendedDB, tableName string) (*Store, error) {
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

	db, err := tabletheory.New(session.Config{Region: os.Getenv(envAWSRegion)})
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
	if validated.contentType == ContentTypeAgentSoul {
		return s.upsertSoul(ctx, validated, in)
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

func (s *Store) upsertSoul(ctx context.Context, validated validatedWriteInput, in UpsertInput) (*Record, error) {
	now := time.Now().UTC()
	document := validated.soulDocument

	for attempt := 0; attempt < updateRetryLimit; attempt++ {
		current, err := s.loadRecord(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
		switch {
		case errors.Is(err, ErrContentNotFound):
			stamped := stampSoulDocument(document, LifecycleStateDraft, 1, 1, validated.updatedBySubjectID, now, now)
			if err := ValidateSoulDocumentRecord(stamped, validated.agentID); err != nil {
				return nil, err
			}
			encoded, err := encodeSoulDocument(stamped)
			if err != nil {
				return nil, err
			}
			created := s.recordFor(validated.account, validated.agentID, ContentTypeAgentSoul)
			created.Content = stamped.Body
			created.DocumentJSON = encoded
			created.SoulVersion = 1
			created.Version = 1
			created.LifecycleState = string(LifecycleStateDraft)
			created.ContentCreatedAt = now
			created.ContentUpdatedAt = now
			created.UpdatedBySubjectID = validated.updatedBySubjectID
			history := s.historyFor(created, ContentActionUpsert, now, validated.updatedBySubjectID)
			err = s.db.TransactWrite(ctx, func(tx tablecore.TransactionBuilder) error {
				tx.Create(created).Create(history)
				return nil
			})
			switch {
			case err == nil:
				return s.Get(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
			case tableerrors.IsConditionFailed(err):
				continue
			default:
				return nil, fmt.Errorf("create agent soul document: %w", err)
			}
		case err != nil:
			return nil, err
		default:
			currentSoulVersion := current.SoulVersion
			if currentSoulVersion < 1 {
				currentSoulVersion = current.Version
			}
			nextSoulVersion := currentSoulVersion + 1
			nextRecordVersion := current.Version + 1
			stamped := stampSoulDocument(document, LifecycleStateDraft, nextSoulVersion, nextRecordVersion, validated.updatedBySubjectID, now, now)
			if err := ValidateSoulDocumentRecord(stamped, validated.agentID); err != nil {
				return nil, err
			}
			encoded, err := encodeSoulDocument(stamped)
			if err != nil {
				return nil, err
			}
			historySource := *current
			historySource.Content = stamped.Body
			historySource.DocumentJSON = encoded
			historySource.SoulVersion = nextSoulVersion
			historySource.Version = nextRecordVersion
			historySource.LifecycleState = string(LifecycleStateDraft)
			historySource.ContentCreatedAt = now
			historySource.ContentUpdatedAt = now
			historySource.UpdatedBySubjectID = validated.updatedBySubjectID
			history := s.historyFor(&historySource, ContentActionUpsert, now, validated.updatedBySubjectID)
			err = s.db.TransactWrite(ctx, func(tx tablecore.TransactionBuilder) error {
				tx.UpdateWithBuilder(current, func(update tablecore.UpdateBuilder) error {
					update.
						Set("Content", stamped.Body).
						Set("DocumentJSON", encoded).
						Set("SoulVersion", nextSoulVersion).
						Set("LifecycleState", string(LifecycleStateDraft)).
						Set("ContentCreatedAt", now).
						Set("ContentUpdatedAt", now).
						Set("UpdatedBySubjectID", validated.updatedBySubjectID).
						Add("Version", int64(1)).
						ConditionVersion(current.Version)
					return nil
				}).Create(history)
				return nil
			})
			switch {
			case err == nil:
				return s.Get(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
			case tableerrors.IsConditionFailed(err):
				continue
			default:
				return nil, fmt.Errorf("update agent soul document: %w", err)
			}
		}
	}
	return nil, fmt.Errorf("%w: upsert agent soul document", ErrContentConflict)
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
	if typ == ContentTypeAgentSoul {
		if err := ValidateSoulAgentID(agentID); err != nil {
			return nil, err
		}
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

// Publish explicitly transitions the current agent_soul draft to a published
// immutable snapshot. Replaying publication of the same current version is
// idempotent.
func (s *Store) Publish(ctx context.Context, in PublishInput) (*Record, error) {
	if s == nil {
		return nil, fmt.Errorf("agent content store is nil")
	}
	validated, err := validateWriteScope(in.Account, in.AgentID, ContentTypeAgentSoul, in.UpdatedBySubjectID)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.transitionSoul(ctx, validated, ContentActionPublish, LifecycleStateDraft, LifecycleStatePublished, 0)
}

// SeedPublished idempotently applies one finalized Hosted Genesis declaration
// without ever overwriting owner-authored or differently-provenanced content.
// A process interruption between the draft and publish transactions is repaired
// by the next replay.
func (s *Store) SeedPublished(ctx context.Context, in SeedPublishedInput) (*Record, bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("agent content store is nil")
	}
	validated, err := validateUpsertInput(UpsertInput{
		Account:            in.Account,
		AgentID:            in.AgentID,
		Type:               ContentTypeAgentSoul,
		SoulDocument:       in.SoulDocument,
		UpdatedBySubjectID: in.UpdatedBySubjectID,
	})
	if err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	created := false
	for attempt := 0; attempt < updateRetryLimit; attempt++ {
		var current *Record
		stored, err := s.loadRecord(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
		switch {
		case errors.Is(err, ErrContentNotFound):
			draft, createErr := s.createInitialSoulDraft(ctx, validated)
			switch {
			case createErr == nil:
				current = draft
				created = true
			case errors.Is(createErr, ErrContentConflict):
				continue
			default:
				return nil, false, createErr
			}
		case err != nil:
			return nil, false, err
		case stored.isLegacyOpaqueSoul():
			return nil, false, &SoulRewriteRequiredError{Action: ContentActionPublish}
		default:
			current, err = stored.toRecord()
			if err != nil {
				return nil, false, err
			}
		}

		if !sameSoulDocumentAuthoringContent(current.Document, validated.soulDocument) {
			return nil, false, fmt.Errorf("%w: finalized declaration differs from current agent_soul", ErrContentConflict)
		}
		switch current.LifecycleState {
		case LifecycleStatePublished:
			return current, created, nil
		case LifecycleStateDraft:
			// Bind the seed publication to the exact draft that matched the
			// finalized declaration above. A concurrent owner upsert must never
			// cause this path to publish a different draft.
			published, err := s.transitionSoul(
				ctx,
				validated,
				ContentActionPublish,
				LifecycleStateDraft,
				LifecycleStatePublished,
				current.Version,
			)
			if errors.Is(err, ErrContentConflict) {
				continue
			}
			return published, created, err
		default:
			return nil, false, &TransitionError{
				Action: ContentActionPublish,
				From:   current.LifecycleState,
				To:     LifecycleStatePublished,
			}
		}
	}
	return nil, false, fmt.Errorf("%w: seed finalized agent soul", ErrContentConflict)
}

// SeedInstructions creates the deterministic Hosted Genesis operating note
// only when the account-scoped agent has no instructions record. Matching
// replays and later retries after an owner upsert return the exact current
// version without modifying content, audit fields, lifecycle, or version.
func (s *Store) SeedInstructions(ctx context.Context, in SeedInstructionsInput) (*Record, bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("agent content store is nil")
	}
	validated, err := validateUpsertInput(UpsertInput{
		Account:            in.Account,
		AgentID:            in.AgentID,
		Type:               ContentTypeAgentInstructions,
		Content:            in.Content,
		UpdatedBySubjectID: in.UpdatedBySubjectID,
	})
	if err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	created := s.recordFor(validated.account, validated.agentID, ContentTypeAgentInstructions)
	created.Content = in.Content
	created.Version = 1
	created.LifecycleState = string(LifecycleStateDraft)
	created.ContentCreatedAt = now
	created.ContentUpdatedAt = now
	created.UpdatedBySubjectID = validated.updatedBySubjectID

	err = s.db.Model(created).WithContext(ctx).IfNotExists().Create()
	switch {
	case err == nil:
		record, recordErr := created.toRecord()
		return record, true, recordErr
	case tableerrors.IsConditionFailed(err):
		record, getErr := s.Get(ctx, validated.account, validated.agentID, ContentTypeAgentInstructions)
		return record, false, getErr
	default:
		return nil, false, fmt.Errorf("create genesis agent instructions seed: %w", err)
	}
}

func (s *Store) createInitialSoulDraft(ctx context.Context, validated validatedWriteInput) (*Record, error) {
	now := time.Now().UTC()
	stamped := stampSoulDocument(validated.soulDocument, LifecycleStateDraft, 1, 1, validated.updatedBySubjectID, now, now)
	if err := ValidateSoulDocumentRecord(stamped, validated.agentID); err != nil {
		return nil, err
	}
	encoded, err := encodeSoulDocument(stamped)
	if err != nil {
		return nil, err
	}
	created := s.recordFor(validated.account, validated.agentID, ContentTypeAgentSoul)
	created.Content = stamped.Body
	created.DocumentJSON = encoded
	created.SoulVersion = 1
	created.Version = 1
	created.LifecycleState = string(LifecycleStateDraft)
	created.ContentCreatedAt = now
	created.ContentUpdatedAt = now
	created.UpdatedBySubjectID = validated.updatedBySubjectID
	history := s.historyFor(created, ContentActionUpsert, now, validated.updatedBySubjectID)
	err = s.db.TransactWrite(ctx, func(tx tablecore.TransactionBuilder) error {
		tx.Create(created).Create(history)
		return nil
	})
	switch {
	case err == nil:
		return s.Get(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
	case tableerrors.IsConditionFailed(err):
		return nil, fmt.Errorf("%w: create initial agent soul draft", ErrContentConflict)
	default:
		return nil, fmt.Errorf("create initial agent soul draft: %w", err)
	}
}

// Archive idempotently retires the current content record. For agent_soul,
// only a published snapshot can be archived; draft publication is always an
// explicit owner act. Agent instructions preserve their existing draft archive
// behavior.
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
	if validated.contentType == ContentTypeAgentSoul {
		return s.transitionSoul(ctx, validated, ContentActionArchive, LifecycleStatePublished, LifecycleStateArchived, 0)
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

func (s *Store) transitionSoul(
	ctx context.Context,
	validated validatedWriteInput,
	action ContentAction,
	from, to LifecycleState,
	expectedRecordVersion int64,
) (*Record, error) {
	for attempt := 0; attempt < updateRetryLimit; attempt++ {
		current, err := s.loadRecord(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
		if err != nil {
			return nil, err
		}
		if current.isLegacyOpaqueSoul() {
			return nil, &SoulRewriteRequiredError{Action: action}
		}
		if expectedRecordVersion > 0 && current.Version != expectedRecordVersion {
			return nil, fmt.Errorf("%w: %s agent soul source draft changed", ErrContentConflict, action)
		}
		currentState, err := normalizeLifecycleState(LifecycleState(current.LifecycleState))
		if err != nil {
			return nil, err
		}
		if currentState == to {
			return current.toRecord()
		}
		if currentState != from {
			return nil, &TransitionError{Action: action, From: currentState, To: to}
		}

		currentRecord, err := current.toRecord()
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		nextRecordVersion := current.Version + 1
		stamped := stampSoulDocument(
			currentRecord.Document,
			to,
			currentRecord.SoulVersion,
			nextRecordVersion,
			currentRecord.UpdatedBySubjectID,
			current.ContentCreatedAt,
			current.ContentUpdatedAt,
		)
		if err := ValidateSoulDocumentRecord(stamped, validated.agentID); err != nil {
			return nil, err
		}
		encoded, err := encodeSoulDocument(stamped)
		if err != nil {
			return nil, err
		}

		historySource := *current
		historySource.DocumentJSON = encoded
		historySource.Version = nextRecordVersion
		historySource.LifecycleState = string(to)
		history := s.historyFor(&historySource, action, now, validated.updatedBySubjectID)
		err = s.db.TransactWrite(ctx, func(tx tablecore.TransactionBuilder) error {
			tx.UpdateWithBuilder(current, func(update tablecore.UpdateBuilder) error {
				update.
					Set("DocumentJSON", encoded).
					Set("LifecycleState", string(to)).
					Add("Version", int64(1)).
					ConditionVersion(current.Version)
				return nil
			}).Create(history)
			return nil
		})
		switch {
		case err == nil:
			return s.Get(ctx, validated.account, validated.agentID, ContentTypeAgentSoul)
		case tableerrors.IsConditionFailed(err):
			continue
		default:
			return nil, fmt.Errorf("%s agent soul document: %w", action, err)
		}
	}
	return nil, fmt.Errorf("%w: %s agent soul document", ErrContentConflict, action)
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
	DocumentJSON       string    `theorydb:"attr:documentJson" json:"document_json,omitempty"`
	SoulVersion        int64     `theorydb:"attr:soulVersion" json:"soul_version,omitempty"`
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

func (r *contentRecord) isLegacyOpaqueSoul() bool {
	return r != nil &&
		ContentType(r.ContentType) == ContentTypeAgentSoul &&
		strings.TrimSpace(r.DocumentJSON) == ""
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
	record := &Record{
		Account:            normalizeAccount(r.Account),
		AgentID:            normalizeAgentID(r.AgentID),
		Type:               typ,
		Content:            r.Content,
		Version:            r.Version,
		SoulVersion:        r.SoulVersion,
		LifecycleState:     state,
		CreatedAt:          r.ContentCreatedAt.UTC(),
		UpdatedAt:          r.ContentUpdatedAt.UTC(),
		UpdatedBySubjectID: strings.TrimSpace(r.UpdatedBySubjectID),
	}
	if typ != ContentTypeAgentSoul {
		if state == LifecycleStatePublished {
			return nil, fmt.Errorf("%w: published is only valid for agent_soul", ErrInvalidLifecycleState)
		}
		return record, nil
	}

	var document *SoulDocument
	if strings.TrimSpace(r.DocumentJSON) == "" {
		// Compatibility projection for pre-v2 Body rows. The old opaque
		// content becomes the v2 canonical body; it is never interpreted as
		// JSON or as a local_id/soul_agent_id.
		document = &SoulDocument{
			SchemaVersion: SoulDocumentSchemaVersion,
			AgentID:       record.AgentID,
			Body:          r.Content,
		}
		soulVersion := r.SoulVersion
		if soulVersion < 1 {
			soulVersion = r.Version
		}
		document = stampSoulDocument(document, state, soulVersion, r.Version, record.UpdatedBySubjectID, record.CreatedAt, record.UpdatedAt)
	} else {
		document, err = decodeSoulDocument(r.DocumentJSON)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateSoulDocumentRecord(document, record.AgentID); err != nil {
		return nil, err
	}
	expectedSoulVersion := r.SoulVersion
	if expectedSoulVersion < 1 {
		expectedSoulVersion = r.Version
	}
	if document.Body != r.Content ||
		document.SoulVersion != expectedSoulVersion ||
		document.Version != record.Version ||
		document.LifecycleState != record.LifecycleState ||
		document.UpdatedBySubjectID != record.UpdatedBySubjectID ||
		document.CreatedAt != record.CreatedAt.Format(time.RFC3339Nano) ||
		document.UpdatedAt != record.UpdatedAt.Format(time.RFC3339Nano) {
		return nil, validationError("$", "persisted document metadata does not match its storage projection")
	}
	record.SoulVersion = document.SoulVersion
	record.Document = document
	return record, nil
}

type historyRecord struct {
	tableName string

	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	Account            string    `theorydb:"attr:account" json:"account"`
	AgentID            string    `theorydb:"attr:agentId" json:"agent_id"`
	ContentType        string    `theorydb:"attr:contentType" json:"content_type"`
	Content            string    `theorydb:"attr:content" json:"content"`
	DocumentJSON       string    `theorydb:"attr:documentJson" json:"document_json"`
	SoulVersion        int64     `theorydb:"attr:soulVersion" json:"soul_version"`
	RecordVersion      int64     `theorydb:"attr:recordVersion" json:"record_version"`
	LifecycleState     string    `theorydb:"attr:lifecycleState" json:"lifecycle_state"`
	Action             string    `theorydb:"attr:action" json:"action"`
	ActionAt           time.Time `theorydb:"attr:actionAt" json:"action_at"`
	ActionBySubjectID  string    `theorydb:"attr:actionBySubjectId" json:"action_by_subject_id"`
	ContentCreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ContentUpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	UpdatedBySubjectID string    `theorydb:"attr:updatedBySubjectId" json:"updated_by_subject_id"`
}

func (r historyRecord) TableName() string {
	if tableName := strings.TrimSpace(r.tableName); tableName != "" {
		return tableName
	}
	return strings.TrimSpace(os.Getenv(EnvInstanceContentTable))
}

func (s *Store) historyFor(record *contentRecord, action ContentAction, actionAt time.Time, actionBySubjectID string) *historyRecord {
	return &historyRecord{
		tableName:          s.tableName,
		PK:                 record.PK,
		SK:                 historySortKey(ContentTypeAgentSoul, record.SoulVersion, LifecycleState(record.LifecycleState)),
		Account:            record.Account,
		AgentID:            record.AgentID,
		ContentType:        record.ContentType,
		Content:            record.Content,
		DocumentJSON:       record.DocumentJSON,
		SoulVersion:        record.SoulVersion,
		RecordVersion:      record.Version,
		LifecycleState:     record.LifecycleState,
		Action:             string(action),
		ActionAt:           actionAt.UTC(),
		ActionBySubjectID:  strings.TrimSpace(actionBySubjectID),
		ContentCreatedAt:   record.ContentCreatedAt,
		ContentUpdatedAt:   record.ContentUpdatedAt,
		UpdatedBySubjectID: record.UpdatedBySubjectID,
	}
}

type validatedWriteInput struct {
	account            string
	agentID            string
	contentType        ContentType
	updatedBySubjectID string
	soulDocument       *SoulDocument
}

func validateUpsertInput(in UpsertInput) (validatedWriteInput, error) {
	validated, err := validateWriteScope(in.Account, in.AgentID, in.Type, in.UpdatedBySubjectID)
	if err != nil {
		return validatedWriteInput{}, err
	}
	switch validated.contentType {
	case ContentTypeAgentSoul:
		document := cloneSoulDocument(in.SoulDocument)
		if document == nil {
			document = &SoulDocument{
				AgentID: validated.agentID,
				Body:    in.Content,
			}
		} else if in.Content != "" && in.Content != document.Body {
			return validatedWriteInput{}, validationError("body", "must not conflict with the legacy content alias")
		}
		if err := ValidateSoulDocumentDraft(document, validated.agentID); err != nil {
			return validatedWriteInput{}, err
		}
		validated.soulDocument = document
	case ContentTypeAgentInstructions:
		if in.SoulDocument != nil {
			return validatedWriteInput{}, validationError("$", "soul_document is only valid for agent_soul")
		}
		if err := validateContentSize(validated.contentType, in.Content); err != nil {
			return validatedWriteInput{}, err
		}
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
	if typ == ContentTypeAgentSoul {
		if err := ValidateSoulAgentID(agentID); err != nil {
			return validatedWriteInput{}, err
		}
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

func historySortKey(contentType ContentType, soulVersion int64, state LifecycleState) string {
	typ, err := normalizeContentType(contentType)
	if err != nil || soulVersion < 1 {
		return ""
	}
	return fmt.Sprintf("%s%s#HISTORY#%020d#%s", contentSKPrefix, typ, soulVersion, state)
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
	case string(LifecycleStatePublished):
		return LifecycleStatePublished, nil
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
