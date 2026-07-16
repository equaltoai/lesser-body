package agentregistry

import (
	"context"
	"errors"
	"os"
	"reflect"
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

func TestListReturnsAccountScopedPaginatedAgents(t *testing.T) {
	store, _ := newTestStore(t)
	for _, in := range []CreateInput{
		{Account: "account-a", AgentID: "agent-001"},
		{Account: "account-a", AgentID: "agent-002"},
		{Account: "account-a", AgentID: "agent-003"},
		{Account: "account-b", AgentID: "agent-000"},
	} {
		if _, err := store.Create(context.Background(), in); err != nil {
			t.Fatalf("Create(%+v) error = %v", in, err)
		}
	}

	first, err := store.List(context.Background(), ListInput{Account: " ACCOUNT-A ", Limit: 2})
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	if got, want := agentIDs(first.Agents), []string{"agent-001", "agent-002"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page agent ids = %v, want %v", got, want)
	}
	if first.Count != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page metadata = %+v, want count=2 has_more cursor", first)
	}

	second, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if got, want := agentIDs(second.Agents), []string{"agent-003"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page agent ids = %v, want %v", got, want)
	}
	if second.Count != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page metadata = %+v, want final page count=1", second)
	}
}

func TestListEmptyAccountReturnsEmptyPage(t *testing.T) {
	store, _ := newTestStore(t)
	page, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Agents) != 0 || page.Count != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty page = %+v, want empty terminal page", page)
	}
}

func TestListRejectsInvalidCursorAndLimit(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: -1}); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("negative limit error = %v, want ErrInvalidLimit", err)
	}
	if _, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: MaxListLimit + 1}); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("too-large limit error = %v, want ErrInvalidLimit", err)
	}
	if _, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: 10, Cursor: "not-a-valid-cursor"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor error = %v, want ErrInvalidCursor", err)
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

func TestListUsesInstanceRegistryTableNotLesserTable(t *testing.T) {
	store, fake := newTestStore(t)
	for _, in := range []CreateInput{
		{Account: "account-a", AgentID: "agent-001"},
		{Account: "account-a", AgentID: "agent-002"},
	} {
		if _, err := store.Create(context.Background(), in); err != nil {
			t.Fatalf("Create(%+v) error = %v", in, err)
		}
	}

	page, err := store.List(context.Background(), ListInput{Account: "account-a", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got, want := agentIDs(page.Agents), []string{"agent-001", "agent-002"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agent ids = %v, want %v", got, want)
	}
	if len(fake.Items(os.Getenv("LESSER_TABLE_NAME"))) != 0 {
		t.Fatalf("List/Create used LESSER_TABLE_NAME items: %+v", fake.Items(os.Getenv("LESSER_TABLE_NAME")))
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

func agentIDs(agents []*Agent) []string {
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, agent.AgentID)
	}
	return out
}
