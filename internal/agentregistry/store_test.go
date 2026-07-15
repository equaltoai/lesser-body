package agentregistry

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestCreateAndGet(t *testing.T) {
	store, _ := newTestStore(t)
	created, err := store.Create(context.Background(), CreateInput{
		Account: " Account-A ",
		AgentID: "agent-123",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Account != "account-a" || created.AgentID != "agent-123" {
		t.Fatalf("created = %+v, want normalized account and agent", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps are zero: %+v", created)
	}

	got, err := store.Get(context.Background(), "account-a", "agent-123")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Account != created.Account || got.AgentID != created.AgentID {
		t.Fatalf("Get() = %+v, want %+v", got, created)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("got timestamps = %s/%s, want %s/%s", got.CreatedAt, got.UpdatedAt, created.CreatedAt, created.UpdatedAt)
	}
}

func TestDuplicateCreateMapsAlreadyExists(t *testing.T) {
	store, _ := newTestStore(t)
	in := CreateInput{Account: "account-a", AgentID: "agent-123"}
	if _, err := store.Create(context.Background(), in); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}

	_, err := store.Create(context.Background(), in)
	if !errors.Is(err, ErrAgentAlreadyExists) {
		t.Fatalf("second Create() error = %v, want ErrAgentAlreadyExists", err)
	}
}

func TestCrossAccountGetReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Create(context.Background(), CreateInput{Account: "account-a", AgentID: "agent-123"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(context.Background(), "account-b", "agent-123")
	if got != nil {
		t.Fatalf("cross-account Get() returned %+v, want nil", got)
	}
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("cross-account Get() error = %v, want ErrAgentNotFound", err)
	}
}

func TestCreateUsesInstanceRegistryTableNotLesserTable(t *testing.T) {
	store, fake := newTestStore(t)
	if _, err := store.Create(context.Background(), CreateInput{Account: "account-a", AgentID: "agent-123"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	registryItems := fake.Items(os.Getenv(EnvInstanceRegistryTable))
	if len(registryItems) != 1 {
		t.Fatalf("registry table items = %d, want 1", len(registryItems))
	}
	lesserItems := fake.Items(os.Getenv("LESSER_TABLE_NAME"))
	if len(lesserItems) != 0 {
		t.Fatalf("LESSER_TABLE_NAME items = %d, want 0", len(lesserItems))
	}
}

func newTestStore(t *testing.T) (*Store, *fakedb.Fake) {
	t.Helper()
	t.Setenv(EnvInstanceRegistryTable, "body-instance-registry-test")
	t.Setenv("LESSER_TABLE_NAME", "lesser-stage-table-test")

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	store, err := NewStore(db, os.Getenv(EnvInstanceRegistryTable))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := db.CreateTable(store.emptyRecord()); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	return store, fake
}
