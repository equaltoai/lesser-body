package mcpapp_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

type fakeTableTheoryDB struct {
	firstFn func(dest any, where map[string]any) error
}

func (f *fakeTableTheoryDB) Model(any) tablecore.Query {
	return &fakeTableTheoryQuery{
		where:   map[string]any{},
		firstFn: f.firstFn,
	}
}

func (f *fakeTableTheoryDB) Transaction(func(tx *tablecore.Tx) error) error { return nil }
func (f *fakeTableTheoryDB) Migrate() error                                 { return nil }
func (f *fakeTableTheoryDB) AutoMigrate(...any) error                       { return nil }
func (f *fakeTableTheoryDB) Close() error                                   { return nil }
func (f *fakeTableTheoryDB) WithContext(context.Context) tablecore.DB       { return f }

type fakeTableTheoryQuery struct {
	where   map[string]any
	firstFn func(dest any, where map[string]any) error
}

func (q *fakeTableTheoryQuery) Where(field string, _ string, value any) tablecore.Query {
	q.where[field] = value
	return q
}

func (q *fakeTableTheoryQuery) Index(string) tablecore.Query                        { return q }
func (q *fakeTableTheoryQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *fakeTableTheoryQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *fakeTableTheoryQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *fakeTableTheoryQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) IfNotExists() tablecore.Query                        { return q }
func (q *fakeTableTheoryQuery) IfExists() tablecore.Query                           { return q }
func (q *fakeTableTheoryQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *fakeTableTheoryQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *fakeTableTheoryQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *fakeTableTheoryQuery) Limit(int) tablecore.Query                    { return q }
func (q *fakeTableTheoryQuery) Offset(int) tablecore.Query                   { return q }
func (q *fakeTableTheoryQuery) Select(...string) tablecore.Query             { return q }
func (q *fakeTableTheoryQuery) ConsistentRead() tablecore.Query              { return q }
func (q *fakeTableTheoryQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) All(any) error                                { return nil }
func (q *fakeTableTheoryQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	return nil, nil
}
func (q *fakeTableTheoryQuery) Count() (int64, error)                     { return 0, nil }
func (q *fakeTableTheoryQuery) Create() error                             { return nil }
func (q *fakeTableTheoryQuery) CreateOrUpdate() error                     { return nil }
func (q *fakeTableTheoryQuery) Update(...string) error                    { return nil }
func (q *fakeTableTheoryQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *fakeTableTheoryQuery) Delete() error                             { return nil }
func (q *fakeTableTheoryQuery) Scan(any) error                            { return nil }
func (q *fakeTableTheoryQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *fakeTableTheoryQuery) BatchGet([]any, any) error                 { return nil }
func (q *fakeTableTheoryQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *fakeTableTheoryQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *fakeTableTheoryQuery) BatchCreate(any) error                      { return nil }
func (q *fakeTableTheoryQuery) BatchDelete([]any) error                    { return nil }
func (q *fakeTableTheoryQuery) BatchWrite([]any, []any) error              { return nil }
func (q *fakeTableTheoryQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	return nil
}
func (q *fakeTableTheoryQuery) Cursor(string) tablecore.Query               { return q }
func (q *fakeTableTheoryQuery) SetCursor(string) error                      { return nil }
func (q *fakeTableTheoryQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *fakeTableTheoryQuery) First(dest any) error {
	if q.firstFn == nil {
		return nil
	}
	return q.firstFn(dest, q.where)
}

func setStructFields(dest any, values map[string]string) error {
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

func installTrustConfigIsolation(t testing.TB) {
	t.Helper()
	trustconfig.ResetForTests()
	t.Cleanup(trustconfig.ResetForTests)

	restoreTrustConfig := mcpapp.SetLoadEffectiveTrustConfigForTests(func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{}, nil
	})
	t.Cleanup(restoreTrustConfig)
}

func installSoulBindingLookup(t testing.TB, username string, agentID string) {
	t.Helper()
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	installTrustConfigIsolation(t)
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": username,
				})
			},
		}, nil
	})
}

func installMissingSoulBindingLookup(t testing.TB) {
	t.Helper()
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	installTrustConfigIsolation(t)
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return tableerrors.ErrItemNotFound
			},
		}, nil
	})
}

func installErroringSoulBindingLookup(t testing.TB) {
	t.Helper()
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	installTrustConfigIsolation(t)
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return errors.New("soulbinding lookup failed")
			},
		}, nil
	})
}

func resetSoulBindingLookup(t testing.TB) {
	t.Helper()
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
}
