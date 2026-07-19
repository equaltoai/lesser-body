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

func TestUpsertFinalizedCreatesAndReplaysHostDerivedRow(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	in := FinalizedInput{
		Account:                " Drone-Ada ",
		AgentID:                " agent-0xabc ",
		HostRegistrationID:     "reg-123",
		HostConversationID:     "conv-456",
		Domain:                 "example.com",
		LocalID:                "ada",
		AuthorityModel:         "instance_trust",
		AnchorState:            "hosted_offchain",
		OperationalBinding:     "hosted_bound_soul",
		LifecycleStatus:        "active",
		PublishedVersion:       7,
		SelfDescriptionVersion: 8,
	}

	created, didCreate, err := store.UpsertFinalized(ctx, in)
	if err != nil {
		t.Fatalf("UpsertFinalized(create) error = %v", err)
	}
	if !didCreate {
		t.Fatal("UpsertFinalized(create) didCreate = false, want true")
	}
	assertHostFinalizedAgent(t, created)

	replayed, didCreate, err := store.UpsertFinalized(ctx, in)
	if err != nil {
		t.Fatalf("UpsertFinalized(replay) error = %v", err)
	}
	if didCreate {
		t.Fatal("UpsertFinalized(replay) didCreate = true, want false")
	}
	assertHostFinalizedAgent(t, replayed)
	if replayed.CreatedAt.IsZero() || replayed.UpdatedAt.Before(replayed.CreatedAt) {
		t.Fatalf("replayed timestamps = created %s updated %s", replayed.CreatedAt, replayed.UpdatedAt)
	}

	items := fake.Items(os.Getenv(EnvInstanceRegistryTable))
	if len(items) != 1 {
		t.Fatalf("registry table items = %d, want exactly one idempotent row", len(items))
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

func assertHostFinalizedAgent(t testing.TB, got *Agent) {
	t.Helper()
	if got == nil {
		t.Fatal("agent is nil")
	}
	if got.Account != "drone-ada" || got.AgentID != "agent-0xabc" {
		t.Fatalf("agent scope = %+v, want normalized account/agent", got)
	}
	if got.Source != SourceHostGenesisFinalize || got.SourceAuthority != SourceAuthorityLesserHost || got.SourceOperation != SourceOperationAgentGenesisFinalize {
		t.Fatalf("provenance = source %q authority %q operation %q", got.Source, got.SourceAuthority, got.SourceOperation)
	}
	if got.HostRegistrationID != "reg-123" || got.HostConversationID != "conv-456" {
		t.Fatalf("host ids = %q/%q", got.HostRegistrationID, got.HostConversationID)
	}
	if got.Domain != "example.com" || got.LocalID != "ada" || got.AuthorityModel != "instance_trust" || got.AnchorState != "hosted_offchain" || got.OperationalBinding != "hosted_bound_soul" || got.LifecycleStatus != "active" || got.PublishedVersion != 7 || got.SelfDescriptionVersion != 8 {
		t.Fatalf("host identity fields = %+v", got)
	}
}
