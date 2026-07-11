package soulbinding

import (
	"context"
	"reflect"
	"testing"
	"time"

	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
)

func TestResolveAgentID_NormalizesUsernameBeforeLookup(t *testing.T) {
	t.Setenv(envTableName, "test-main-table")

	var gotPK string
	var gotSK string
	SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeBindingDB{
			firstFn: func(dest any, where map[string]any) error {
				gotPK, _ = where["PK"].(string)
				gotSK, _ = where["SK"].(string)
				return setStringFields(dest, map[string]string{
					"AgentID":  " Agent-ABC ",
					"Username": "medic",
				})
			},
		}, nil
	})
	t.Cleanup(ResetForTests)

	agentID, err := ResolveAgentID(context.Background(), "  Medic ")
	if err != nil {
		t.Fatalf("resolve agent id: %v", err)
	}
	if gotPK != soulBodyBindingUserPKPre+"medic" {
		t.Fatalf("expected lowercase binding PK, got %q", gotPK)
	}
	if gotSK != skSoulBodyBinding {
		t.Fatalf("expected binding SK %q, got %q", skSoulBodyBinding, gotSK)
	}
	if agentID != "agent-abc" {
		t.Fatalf("expected normalized agent id, got %q", agentID)
	}
}

func TestSoulBodyBindingUsernamePartitionKey_NormalizesUsername(t *testing.T) {
	if got := soulBodyBindingUsernamePartitionKey("  Medic "); got != soulBodyBindingUserPKPre+"medic" {
		t.Fatalf("expected lowercase partition key, got %q", got)
	}
	if got := soulBodyBindingUsernamePartitionKey("   "); got != "" {
		t.Fatalf("expected empty partition key for blank username, got %q", got)
	}
}

type fakeBindingDB struct {
	firstFn func(dest any, where map[string]any) error
}

func (f *fakeBindingDB) Model(any) tablecore.Query {
	return &fakeBindingQuery{
		where:   map[string]any{},
		firstFn: f.firstFn,
	}
}

func (f *fakeBindingDB) Migrate() error                           { return nil }
func (f *fakeBindingDB) AutoMigrate(...any) error                 { return nil }
func (f *fakeBindingDB) Close() error                             { return nil }
func (f *fakeBindingDB) WithContext(context.Context) tablecore.DB { return f }

type fakeBindingQuery struct {
	where   map[string]any
	firstFn func(dest any, where map[string]any) error
}

func (q *fakeBindingQuery) Where(field string, _ string, value any) tablecore.Query {
	q.where[field] = value
	return q
}

func (q *fakeBindingQuery) Index(string) tablecore.Query                        { return q }
func (q *fakeBindingQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *fakeBindingQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *fakeBindingQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *fakeBindingQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *fakeBindingQuery) IfNotExists() tablecore.Query                        { return q }
func (q *fakeBindingQuery) IfExists() tablecore.Query                           { return q }
func (q *fakeBindingQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *fakeBindingQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *fakeBindingQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *fakeBindingQuery) Limit(int) tablecore.Query                    { return q }
func (q *fakeBindingQuery) Offset(int) tablecore.Query                   { return q }
func (q *fakeBindingQuery) Select(...string) tablecore.Query             { return q }
func (q *fakeBindingQuery) ConsistentRead() tablecore.Query              { return q }
func (q *fakeBindingQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *fakeBindingQuery) All(any) error                                { return nil }
func (q *fakeBindingQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	return nil, nil
}
func (q *fakeBindingQuery) Count() (int64, error)                     { return 0, nil }
func (q *fakeBindingQuery) Create() error                             { return nil }
func (q *fakeBindingQuery) CreateOrUpdate() error                     { return nil }
func (q *fakeBindingQuery) Update(...string) error                    { return nil }
func (q *fakeBindingQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *fakeBindingQuery) Delete() error                             { return nil }
func (q *fakeBindingQuery) Scan(any) error                            { return nil }
func (q *fakeBindingQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *fakeBindingQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *fakeBindingQuery) BatchGet([]any, any) error                 { return nil }
func (q *fakeBindingQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *fakeBindingQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *fakeBindingQuery) BatchCreate(any) error                      { return nil }
func (q *fakeBindingQuery) BatchDelete([]any) error                    { return nil }
func (q *fakeBindingQuery) BatchWrite([]any, []any) error              { return nil }
func (q *fakeBindingQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	return nil
}
func (q *fakeBindingQuery) Cursor(string) tablecore.Query               { return q }
func (q *fakeBindingQuery) SetCursor(string) error                      { return nil }
func (q *fakeBindingQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *fakeBindingQuery) First(dest any) error {
	if q.firstFn == nil {
		return nil
	}
	return q.firstFn(dest, q.where)
}

func setStringFields(dest any, values map[string]string) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}
	for field, value := range values {
		fv := elem.FieldByName(field)
		if !fv.IsValid() || !fv.CanSet() || fv.Kind() != reflect.String {
			continue
		}
		fv.SetString(value)
	}
	return nil
}
