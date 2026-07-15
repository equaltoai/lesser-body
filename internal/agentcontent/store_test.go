package agentcontent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestUpsertCreateUpdateGetFlow(t *testing.T) {
	store, _ := newTestStore(t)

	created, err := store.Upsert(context.Background(), UpsertInput{
		Account:            " Account-A ",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "first soul draft",
		UpdatedBySubjectID: " subject-create ",
	})
	if err != nil {
		t.Fatalf("Upsert(create) error = %v", err)
	}
	if created.Account != "account-a" || created.AgentID != "agent-123" || created.Type != ContentTypeAgentSoul {
		t.Fatalf("created scope/type = %+v, want normalized account, agent, type", created)
	}
	if created.Content != "first soul draft" || created.Version != 1 || created.LifecycleState != LifecycleStateDraft {
		t.Fatalf("created content/version/state = %+v, want draft version 1", created)
	}
	if created.UpdatedBySubjectID != "subject-create" {
		t.Fatalf("created UpdatedBySubjectID = %q", created.UpdatedBySubjectID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps are zero: %+v", created)
	}

	updated, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               "AgentSoul",
		Content:            "second soul draft",
		UpdatedBySubjectID: "subject-update",
	})
	if err != nil {
		t.Fatalf("Upsert(update) error = %v", err)
	}
	if updated.Content != "second soul draft" || updated.Version != 2 || updated.LifecycleState != LifecycleStateDraft {
		t.Fatalf("updated content/version/state = %+v, want draft version 2", updated)
	}
	if updated.UpdatedBySubjectID != "subject-update" {
		t.Fatalf("updated UpdatedBySubjectID = %q", updated.UpdatedBySubjectID)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("updated CreatedAt = %s, want original %s", updated.CreatedAt, created.CreatedAt)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("updated UpdatedAt = %s before created UpdatedAt %s", updated.UpdatedAt, created.UpdatedAt)
	}

	got, err := store.Get(context.Background(), "account-a", "agent-123", ContentTypeAgentSoul)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Content != updated.Content || got.Version != updated.Version || got.UpdatedBySubjectID != updated.UpdatedBySubjectID {
		t.Fatalf("Get() = %+v, want updated %+v", got, updated)
	}
}

func TestIndependentVersionCounters(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	soul, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul v1",
		UpdatedBySubjectID: "subject-1",
	})
	if err != nil {
		t.Fatalf("Upsert(soul) error = %v", err)
	}
	instructions, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions v1",
		UpdatedBySubjectID: "subject-2",
	})
	if err != nil {
		t.Fatalf("Upsert(instructions) error = %v", err)
	}
	if soul.Version != 1 || instructions.Version != 1 {
		t.Fatalf("initial versions soul=%d instructions=%d, want both 1", soul.Version, instructions.Version)
	}

	soul, err = store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul v2",
		UpdatedBySubjectID: "subject-3",
	})
	if err != nil {
		t.Fatalf("Upsert(soul v2) error = %v", err)
	}
	instructions, err = store.Get(ctx, "account-a", "agent-123", ContentTypeAgentInstructions)
	if err != nil {
		t.Fatalf("Get(instructions) error = %v", err)
	}
	if soul.Version != 2 {
		t.Fatalf("soul version = %d, want 2", soul.Version)
	}
	if instructions.Version != 1 || instructions.Content != "instructions v1" {
		t.Fatalf("instructions after soul update = %+v, want unchanged version 1", instructions)
	}
}

func TestCrossAccountGetReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul",
		UpdatedBySubjectID: "subject-1",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, err := store.Get(context.Background(), "account-b", "agent-123", ContentTypeAgentSoul)
	if got != nil {
		t.Fatalf("cross-account Get() returned %+v, want nil", got)
	}
	if !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("cross-account Get() error = %v, want ErrContentNotFound", err)
	}
}

func TestSizeBounds(t *testing.T) {
	store, _ := newTestStore(t)
	cases := []struct {
		name  string
		typ   ContentType
		limit int
	}{
		{name: "agent soul", typ: ContentTypeAgentSoul, limit: MaxAgentSoulBytes},
		{name: "agent instructions", typ: ContentTypeAgentInstructions, limit: MaxAgentInstructionsBytes},
	}

	for _, tc := range cases {
		t.Run(tc.name+" accepts limit", func(t *testing.T) {
			_, err := store.Upsert(context.Background(), UpsertInput{
				Account:            "account-a",
				AgentID:            "agent-" + strings.ReplaceAll(tc.name, " ", "-"),
				Type:               tc.typ,
				Content:            strings.Repeat("a", tc.limit),
				UpdatedBySubjectID: "subject-limit",
			})
			if err != nil {
				t.Fatalf("Upsert(at limit) error = %v", err)
			}
		})

		t.Run(tc.name+" rejects over limit", func(t *testing.T) {
			_, err := store.Upsert(context.Background(), UpsertInput{
				Account:            "account-a",
				AgentID:            "agent-over-" + strings.ReplaceAll(tc.name, " ", "-"),
				Type:               tc.typ,
				Content:            strings.Repeat("b", tc.limit+1),
				UpdatedBySubjectID: "subject-over",
			})
			if !errors.Is(err, ErrContentTooLarge) {
				t.Fatalf("Upsert(over limit) error = %v, want ErrContentTooLarge", err)
			}
			var sizeErr *SizeError
			if !errors.As(err, &sizeErr) || sizeErr.Type != tc.typ || sizeErr.Limit != tc.limit || sizeErr.Actual != tc.limit+1 {
				t.Fatalf("size error = %#v, want typed details for %s", err, tc.typ)
			}
		})
	}
}

func TestArchiveIsIdempotentAndCapturesAudit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	created, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions draft",
		UpdatedBySubjectID: "subject-create",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	archived, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		UpdatedBySubjectID: "subject-archive",
	})
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if archived.LifecycleState != LifecycleStateArchived || archived.Version != created.Version || archived.Content != created.Content {
		t.Fatalf("archived = %+v, want archived with same content/version", archived)
	}
	if archived.UpdatedBySubjectID != "subject-archive" {
		t.Fatalf("archived UpdatedBySubjectID = %q", archived.UpdatedBySubjectID)
	}

	again, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		UpdatedBySubjectID: "subject-archive-again",
	})
	if err != nil {
		t.Fatalf("Archive(again) error = %v", err)
	}
	if again.LifecycleState != LifecycleStateArchived || again.Version != archived.Version || again.Content != archived.Content {
		t.Fatalf("Archive(again) = %+v, want idempotent archived content/version", again)
	}
	if again.UpdatedBySubjectID != "subject-archive-again" {
		t.Fatalf("Archive(again) UpdatedBySubjectID = %q", again.UpdatedBySubjectID)
	}

	draft, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions draft after archive",
		UpdatedBySubjectID: "subject-reopen",
	})
	if err != nil {
		t.Fatalf("Upsert(after archive) error = %v", err)
	}
	if draft.LifecycleState != LifecycleStateDraft || draft.Version != archived.Version+1 {
		t.Fatalf("Upsert(after archive) = %+v, want draft version increment", draft)
	}
}

func TestArchiveMissingRecordReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.Archive(context.Background(), ArchiveInput{
		Account:            "account-a",
		AgentID:            "missing-agent",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive",
	})
	if !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("Archive(missing) error = %v, want ErrContentNotFound", err)
	}
}

func TestInvalidTypeStateAndAuditErrors(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Get(context.Background(), "account-a", "agent-123", "biography"); !errors.Is(err, ErrInvalidContentType) {
		t.Fatalf("Get(invalid type) error = %v, want ErrInvalidContentType", err)
	}
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account: "account-a",
		AgentID: "agent-123",
		Type:    ContentTypeAgentSoul,
		Content: "soul",
	}); !errors.Is(err, ErrMissingUpdatedBySubjectID) {
		t.Fatalf("Upsert(missing subject) error = %v, want ErrMissingUpdatedBySubjectID", err)
	}

	record := store.recordFor("account-a", "agent-bad-state", ContentTypeAgentSoul)
	record.Content = "soul"
	record.Version = 1
	record.LifecycleState = "published"
	record.ContentCreatedAt = time.Now().UTC()
	record.ContentUpdatedAt = record.ContentCreatedAt
	record.UpdatedBySubjectID = "subject-seed"
	if err := store.db.Model(record).Create(); err != nil {
		t.Fatalf("seed invalid-state record error = %v", err)
	}

	got, err := store.Get(context.Background(), "account-a", "agent-bad-state", ContentTypeAgentSoul)
	if got != nil {
		t.Fatalf("Get(invalid state) returned %+v, want nil", got)
	}
	if !errors.Is(err, ErrInvalidLifecycleState) {
		t.Fatalf("Get(invalid state) error = %v, want ErrInvalidLifecycleState", err)
	}
}

func TestWritesUseInstanceContentTableNotLesserTable(t *testing.T) {
	store, fake := newTestStore(t)
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul",
		UpdatedBySubjectID: "subject-create",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if _, err := store.Archive(context.Background(), ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive",
	}); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	contentItems := fake.Items(os.Getenv(EnvInstanceContentTable))
	if len(contentItems) != 1 {
		t.Fatalf("content table items = %d, want 1", len(contentItems))
	}
	lesserItems := fake.Items(os.Getenv("LESSER_TABLE_NAME"))
	if len(lesserItems) != 0 {
		t.Fatalf("LESSER_TABLE_NAME items = %d, want 0", len(lesserItems))
	}
}

func newTestStore(t *testing.T) (*Store, *fakedb.Fake) {
	t.Helper()
	t.Setenv(EnvInstanceContentTable, "body-instance-content-test")
	t.Setenv("LESSER_TABLE_NAME", "lesser-stage-table-test")

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	store, err := NewStore(db, os.Getenv(EnvInstanceContentTable))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := db.CreateTable(store.emptyRecord()); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	return store, fake
}
