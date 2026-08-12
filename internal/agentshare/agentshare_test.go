package agentshare

import (
	"context"
	"errors"
	"testing"
	"time"

	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
)

func TestIsActiveUsesOneStronglyConsistentBaseTableGet(t *testing.T) {
	t.Setenv(envTableName, "lesser-test")

	fake := &fakeShareDB{
		records: []grantRecord{activeGrant("arch", "alice")},
	}
	SetDBFactoryForTests(func() (tablecore.DB, error) { return fake, nil })
	t.Cleanup(ResetForTests)

	active, err := IsActive(context.Background(), "  ArCh ", " ALICE ")
	if err != nil {
		t.Fatalf("check active grant: %v", err)
	}
	if !active {
		t.Fatal("expected active grant")
	}
	if fake.modelCalls != 1 || fake.firstCalls != 1 {
		t.Fatalf("expected one direct model read, model=%d first=%d", fake.modelCalls, fake.firstCalls)
	}
	if fake.where["PK"] != "USER#arch" || fake.where["SK"] != "AGENT_SHARE#GRANTEE#alice" {
		t.Fatalf("unexpected direct-read keys: %#v", fake.where)
	}
	if !fake.consistentRead {
		t.Fatal("authorization read must be strongly consistent")
	}
	if fake.indexCalled {
		t.Fatal("authorization read must never consult a GSI")
	}
	if fake.allCalled {
		t.Fatal("authorization read must use one direct GetItem-shaped First call, not a query/list")
	}
}

func TestIsActiveTreatsMissingRevokedAndNonLocalAsNotShared(t *testing.T) {
	t.Setenv(envTableName, "lesser-test")
	now := time.Now().UTC()

	tests := []struct {
		name    string
		agent   string
		grantee string
		record  *grantRecord
		readErr error
	}{
		{name: "missing", agent: "arch", grantee: "alice", readErr: tableerrors.ErrItemNotFound},
		{name: "revoked", agent: "arch", grantee: "alice", record: grantPtr(func() grantRecord {
			record := activeGrant("arch", "alice")
			record.RevokedAt = &now
			return record
		}())},
		{name: "remote actor", agent: "@arch@remote.example", grantee: "alice"},
		{name: "remote grantee", agent: "arch", grantee: "alice@remote.example"},
		{name: "actor URL", agent: "https://remote.example/users/arch", grantee: "alice"},
		{name: "invalid local username", agent: "arch", grantee: "alice smith"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeShareDB{readErrs: []error{tc.readErr}}
			if tc.record != nil {
				fake.records = []grantRecord{*tc.record}
			}
			SetDBFactoryForTests(func() (tablecore.DB, error) { return fake, nil })
			t.Cleanup(ResetForTests)

			active, err := IsActive(context.Background(), tc.agent, tc.grantee)
			if err != nil {
				t.Fatalf("not-shared case returned error: %v", err)
			}
			if active {
				t.Fatal("expected not shared")
			}
			if tc.name != "missing" && tc.record == nil && fake.firstCalls != 0 {
				t.Fatalf("non-local identity must be rejected before table access, reads=%d", fake.firstCalls)
			}
		})
	}
}

func TestIsActiveRejectsMalformedRowsAndReadFailures(t *testing.T) {
	t.Setenv(envTableName, "lesser-test")

	tests := []struct {
		name    string
		record  grantRecord
		readErr error
	}{
		{name: "wrong partition key", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.PK = "USER#other" })},
		{name: "wrong sort key", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.SK = "AGENT_SHARE#GRANTEE#other" })},
		{name: "wrong agent", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.AgentUsername = "other" })},
		{name: "wrong grantee", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.GranteeUsername = "other" })},
		{name: "blank granted by", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.GrantedBy = "  " })},
		{name: "zero granted at", record: mutateGrant(activeGrant("arch", "alice"), func(g *grantRecord) { g.GrantedAt = time.Time{} })},
		{name: "read error", readErr: errors.New("dynamodb permission denied")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeShareDB{records: []grantRecord{tc.record}, readErrs: []error{tc.readErr}}
			SetDBFactoryForTests(func() (tablecore.DB, error) { return fake, nil })
			t.Cleanup(ResetForTests)

			active, err := IsActive(context.Background(), "arch", "alice")
			if err == nil {
				t.Fatal("malformed/read failure must surface an operational error")
			}
			if active {
				t.Fatal("malformed/read failure must fail closed")
			}
		})
	}
}

func TestIsActiveReadsAgainAfterRevocation(t *testing.T) {
	t.Setenv(envTableName, "lesser-test")
	now := time.Now().UTC()
	revoked := activeGrant("arch", "alice")
	revoked.RevokedAt = &now

	fake := &fakeShareDB{records: []grantRecord{
		activeGrant("arch", "alice"),
		revoked,
	}}
	factoryCalls := 0
	SetDBFactoryForTests(func() (tablecore.DB, error) {
		factoryCalls++
		return fake, nil
	})
	t.Cleanup(ResetForTests)

	first, err := IsActive(context.Background(), "arch", "alice")
	if err != nil || !first {
		t.Fatalf("first active check = %v, %v", first, err)
	}
	second, err := IsActive(context.Background(), "arch", "alice")
	if err != nil {
		t.Fatalf("second revoked check: %v", err)
	}
	if second {
		t.Fatal("revocation must take effect on the next request")
	}
	if factoryCalls != 2 || fake.firstCalls != 2 {
		t.Fatalf("positive grants must not be cached, factory=%d reads=%d", factoryCalls, fake.firstCalls)
	}
}

func TestIsActiveFailsClosedWhenStorageIsUnavailable(t *testing.T) {
	t.Run("table name missing", func(t *testing.T) {
		t.Setenv(envTableName, "")
		active, err := IsActive(context.Background(), "arch", "alice")
		if err == nil || active {
			t.Fatalf("missing table must fail closed, active=%v err=%v", active, err)
		}
	})

	t.Run("client creation fails", func(t *testing.T) {
		t.Setenv(envTableName, "lesser-test")
		SetDBFactoryForTests(func() (tablecore.DB, error) { return nil, errors.New("client timeout") })
		t.Cleanup(ResetForTests)
		active, err := IsActive(context.Background(), "arch", "alice")
		if err == nil || active {
			t.Fatalf("client failure must fail closed, active=%v err=%v", active, err)
		}
	})
}

func activeGrant(agent, grantee string) grantRecord {
	return grantRecord{
		PK:              "USER#" + agent,
		SK:              "AGENT_SHARE#GRANTEE#" + grantee,
		AgentUsername:   agent,
		GranteeUsername: grantee,
		GrantedBy:       agent,
		GrantedAt:       time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
}

func grantPtr(record grantRecord) *grantRecord { return &record }

func mutateGrant(record grantRecord, mutate func(*grantRecord)) grantRecord {
	mutate(&record)
	return record
}

type fakeShareDB struct {
	records        []grantRecord
	readErrs       []error
	where          map[string]any
	modelCalls     int
	firstCalls     int
	consistentRead bool
	indexCalled    bool
	allCalled      bool
}

func (f *fakeShareDB) Model(any) tablecore.Query {
	f.modelCalls++
	f.where = map[string]any{}
	return &fakeShareQuery{db: f}
}

func (f *fakeShareDB) Migrate() error                           { return nil }
func (f *fakeShareDB) AutoMigrate(...any) error                 { return nil }
func (f *fakeShareDB) Close() error                             { return nil }
func (f *fakeShareDB) WithContext(context.Context) tablecore.DB { return f }

type fakeShareQuery struct{ db *fakeShareDB }

func (q *fakeShareQuery) Where(field, operator string, value any) tablecore.Query {
	if operator != "=" {
		panic("authorization lookup must use exact equality keys")
	}
	q.db.where[field] = value
	return q
}
func (q *fakeShareQuery) Index(string) tablecore.Query {
	q.db.indexCalled = true
	return q
}
func (q *fakeShareQuery) ConsistentRead() tablecore.Query {
	q.db.consistentRead = true
	return q
}
func (q *fakeShareQuery) First(dest any) error {
	q.db.firstCalls++
	index := q.db.firstCalls - 1
	if index < len(q.db.readErrs) && q.db.readErrs[index] != nil {
		return q.db.readErrs[index]
	}
	if index >= len(q.db.records) {
		return tableerrors.ErrItemNotFound
	}
	record, ok := dest.(*grantRecord)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*record = q.db.records[index]
	return nil
}
func (q *fakeShareQuery) All(any) error {
	q.db.allCalled = true
	return errors.New("authorization lookup must not query")
}

func (q *fakeShareQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *fakeShareQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *fakeShareQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *fakeShareQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *fakeShareQuery) IfNotExists() tablecore.Query                        { return q }
func (q *fakeShareQuery) IfExists() tablecore.Query                           { return q }
func (q *fakeShareQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *fakeShareQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *fakeShareQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *fakeShareQuery) Limit(int) tablecore.Query                    { return q }
func (q *fakeShareQuery) Offset(int) tablecore.Query                   { return q }
func (q *fakeShareQuery) Select(...string) tablecore.Query             { return q }
func (q *fakeShareQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *fakeShareQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	q.db.allCalled = true
	return nil, errors.New("authorization lookup must not paginate")
}
func (q *fakeShareQuery) Count() (int64, error)                     { return 0, nil }
func (q *fakeShareQuery) Create() error                             { return nil }
func (q *fakeShareQuery) CreateOrUpdate() error                     { return nil }
func (q *fakeShareQuery) Update(...string) error                    { return nil }
func (q *fakeShareQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *fakeShareQuery) Delete() error                             { return nil }
func (q *fakeShareQuery) Scan(any) error                            { return nil }
func (q *fakeShareQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *fakeShareQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *fakeShareQuery) BatchGet([]any, any) error                 { return nil }
func (q *fakeShareQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *fakeShareQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *fakeShareQuery) BatchCreate(any) error                      { return nil }
func (q *fakeShareQuery) BatchDelete([]any) error                    { return nil }
func (q *fakeShareQuery) BatchWrite([]any, []any) error              { return nil }
func (q *fakeShareQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	return nil
}
func (q *fakeShareQuery) Cursor(string) tablecore.Query               { return q }
func (q *fakeShareQuery) SetCursor(string) error                      { return nil }
func (q *fakeShareQuery) WithContext(context.Context) tablecore.Query { return q }
