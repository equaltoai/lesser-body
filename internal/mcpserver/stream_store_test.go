package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

func TestDynamoStreamStore_CreateAppendSubscribeResumeAndDelete(t *testing.T) {
	t.Setenv(envMcpStreamTable, "test-mcp-streams")

	db := newFakeStreamDB()
	store := NewDynamoStreamStore(db).(*DynamoStreamStore)
	store.pollInitial = time.Millisecond
	store.pollMax = 2 * time.Millisecond

	ctx := context.Background()
	streamID, err := store.Create(ctx, "sess-1")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	firstPayload := json.RawMessage(`{"seq":1}`)
	firstEventID, err := store.Append(ctx, "sess-1", streamID, firstPayload)
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}

	secondPayload := json.RawMessage(`{"seq":2}`)
	secondEventID, err := store.Append(ctx, "sess-1", streamID, secondPayload)
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}

	if gotStreamID, err := store.StreamForEvent(ctx, "sess-1", firstEventID); err != nil {
		t.Fatalf("stream for first event: %v", err)
	} else if gotStreamID != streamID {
		t.Fatalf("expected stream id %q, got %q", streamID, gotStreamID)
	}

	if err := store.Close(ctx, "sess-1", streamID); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	allCh, err := store.Subscribe(ctx, "sess-1", streamID, "")
	if err != nil {
		t.Fatalf("subscribe from start: %v", err)
	}
	allEvents := readAllStreamEvents(t, allCh)
	if len(allEvents) != 2 {
		t.Fatalf("expected 2 events, got %d", len(allEvents))
	}
	if allEvents[0].ID != firstEventID || string(allEvents[0].Data) != string(firstPayload) {
		t.Fatalf("unexpected first event: %+v", allEvents[0])
	}
	if allEvents[1].ID != secondEventID || string(allEvents[1].Data) != string(secondPayload) {
		t.Fatalf("unexpected second event: %+v", allEvents[1])
	}

	resumeCh, err := store.Subscribe(ctx, "sess-1", streamID, firstEventID)
	if err != nil {
		t.Fatalf("resume subscribe: %v", err)
	}
	resumed := readAllStreamEvents(t, resumeCh)
	if len(resumed) != 1 || resumed[0].ID != secondEventID {
		t.Fatalf("expected only second event after resume, got %+v", resumed)
	}

	if err := store.DeleteSession(ctx, "sess-1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := store.StreamForEvent(ctx, "sess-1", secondEventID); !errors.Is(err, mcpruntime.ErrEventNotFound) {
		t.Fatalf("expected event lookup failure after delete, got %v", err)
	}
}

func TestDynamoStreamStore_SubscribeRejectsMismatchedLastEventID(t *testing.T) {
	t.Setenv(envMcpStreamTable, "test-mcp-streams")

	db := newFakeStreamDB()
	store := NewDynamoStreamStore(db).(*DynamoStreamStore)

	streamID, err := store.Create(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}

	if _, err := store.Subscribe(context.Background(), "sess-1", streamID, "other-stream:01HZZZZZZZZZZZZZZZZZZZZZZZ"); err == nil {
		t.Fatalf("expected mismatched last-event-id error")
	}
}

func TestBuildServerOptionsFromEnv_UsesSharedDBFactoryForSessionAndStreamStores(t *testing.T) {
	t.Setenv(envMcpSessionTable, "test-sessions")
	t.Setenv(envMcpStreamTable, "test-streams")

	prev := newMCPDB
	t.Cleanup(func() {
		newMCPDB = prev
	})

	calls := 0
	newMCPDB = func() (tablecore.DB, error) {
		calls++
		return newFakeStreamDB(), nil
	}

	opts, err := buildServerOptionsFromEnv()
	if err != nil {
		t.Fatalf("build server options: %v", err)
	}
	if len(opts) < 2 {
		t.Fatalf("expected session and stream server options, got %d", len(opts))
	}
	if calls != 1 {
		t.Fatalf("expected shared db factory call count 1, got %d", calls)
	}
}

func readAllStreamEvents(t testing.TB, ch <-chan mcpruntime.StreamEvent) []mcpruntime.StreamEvent {
	t.Helper()

	done := make(chan []mcpruntime.StreamEvent, 1)
	go func() {
		var out []mcpruntime.StreamEvent
		for event := range ch {
			out = append(out, event)
		}
		done <- out
	}()

	select {
	case out := <-done:
		return out
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for stream events")
		return nil
	}
}

type fakeStreamDB struct {
	mu      sync.Mutex
	streams map[string]mcpStreamRecord
	events  map[string]mcpStreamEventRecord
}

func newFakeStreamDB() *fakeStreamDB {
	return &fakeStreamDB{
		streams: map[string]mcpStreamRecord{},
		events:  map[string]mcpStreamEventRecord{},
	}
}

func (f *fakeStreamDB) Model(model any) tablecore.Query {
	return &fakeStreamQuery{
		db:    f,
		model: model,
		where: map[string]fakeCondition{},
	}
}

func (f *fakeStreamDB) Transaction(func(tx *tablecore.Tx) error) error { return nil }
func (f *fakeStreamDB) Migrate() error                                 { return nil }
func (f *fakeStreamDB) AutoMigrate(...any) error                       { return nil }
func (f *fakeStreamDB) Close() error                                   { return nil }
func (f *fakeStreamDB) WithContext(context.Context) tablecore.DB       { return f }

type fakeCondition struct {
	op    string
	value any
}

type fakeStreamQuery struct {
	db      *fakeStreamDB
	model   any
	where   map[string]fakeCondition
	orderBy string
	limit   int
}

func (q *fakeStreamQuery) Where(field string, op string, value any) tablecore.Query {
	q.where[field] = fakeCondition{op: op, value: value}
	return q
}

func (q *fakeStreamQuery) Index(string) tablecore.Query                        { return q }
func (q *fakeStreamQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *fakeStreamQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *fakeStreamQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *fakeStreamQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *fakeStreamQuery) IfNotExists() tablecore.Query                        { return q }
func (q *fakeStreamQuery) IfExists() tablecore.Query                           { return q }
func (q *fakeStreamQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *fakeStreamQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *fakeStreamQuery) OrderBy(field string, order string) tablecore.Query {
	q.orderBy = strings.ToLower(strings.TrimSpace(order))
	return q
}
func (q *fakeStreamQuery) Limit(limit int) tablecore.Query              { q.limit = limit; return q }
func (q *fakeStreamQuery) Offset(int) tablecore.Query                   { return q }
func (q *fakeStreamQuery) Select(...string) tablecore.Query             { return q }
func (q *fakeStreamQuery) ConsistentRead() tablecore.Query              { return q }
func (q *fakeStreamQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *fakeStreamQuery) AllPaginated(dest any) (*tablecore.PaginatedResult, error) {
	return nil, q.All(dest)
}
func (q *fakeStreamQuery) Count() (int64, error)                     { return 0, nil }
func (q *fakeStreamQuery) CreateOrUpdate() error                     { return nil }
func (q *fakeStreamQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *fakeStreamQuery) Scan(any) error                            { return nil }
func (q *fakeStreamQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *fakeStreamQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *fakeStreamQuery) BatchGet([]any, any) error                 { return nil }
func (q *fakeStreamQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *fakeStreamQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *fakeStreamQuery) BatchCreate(any) error                      { return nil }
func (q *fakeStreamQuery) BatchDelete([]any) error                    { return nil }
func (q *fakeStreamQuery) BatchWrite([]any, []any) error              { return nil }
func (q *fakeStreamQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	return nil
}
func (q *fakeStreamQuery) Cursor(string) tablecore.Query               { return q }
func (q *fakeStreamQuery) SetCursor(string) error                      { return nil }
func (q *fakeStreamQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *fakeStreamQuery) First(dest any) error {
	records, err := q.matchedRecords()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return tableerrors.ErrItemNotFound
	}
	return assignFirst(dest, records[0])
}

func (q *fakeStreamQuery) All(dest any) error {
	records, err := q.matchedRecords()
	if err != nil {
		return err
	}
	return assignAll(dest, records)
}

func (q *fakeStreamQuery) Create() error {
	q.db.mu.Lock()
	defer q.db.mu.Unlock()

	switch record := q.model.(type) {
	case *mcpStreamRecord:
		q.db.streams[fakeKey(record.PK, record.SK)] = *record
	case *mcpStreamEventRecord:
		q.db.events[fakeKey(record.PK, record.SK)] = *record
	default:
		return fmt.Errorf("unsupported create model %T", q.model)
	}
	return nil
}

func (q *fakeStreamQuery) Update(fields ...string) error {
	q.db.mu.Lock()
	defer q.db.mu.Unlock()

	switch record := q.model.(type) {
	case *mcpStreamRecord:
		key := fakeKey(record.PK, record.SK)
		existing, ok := q.db.streams[key]
		if !ok {
			return tableerrors.ErrItemNotFound
		}
		for _, field := range fields {
			switch field {
			case "Closed":
				existing.Closed = record.Closed
			case "UpdatedAt":
				existing.UpdatedAt = record.UpdatedAt
			}
		}
		q.db.streams[key] = existing
	default:
		return fmt.Errorf("unsupported update model %T", q.model)
	}
	return nil
}

func (q *fakeStreamQuery) Delete() error {
	q.db.mu.Lock()
	defer q.db.mu.Unlock()

	pk, okPK := q.lookupString("PK")
	sk, okSK := q.lookupString("SK")
	if !okPK || !okSK {
		return tableerrors.ErrItemNotFound
	}
	key := fakeKey(pk, sk)
	delete(q.db.streams, key)
	delete(q.db.events, key)
	return nil
}

func (q *fakeStreamQuery) matchedRecords() ([]any, error) {
	q.db.mu.Lock()
	defer q.db.mu.Unlock()

	var records []any
	switch q.model.(type) {
	case *mcpStreamRecord:
		for _, record := range q.db.streams {
			if q.matchesStream(record) {
				records = append(records, record)
			}
		}
	case *mcpStreamEventRecord:
		for _, record := range q.db.events {
			if q.matchesEvent(record) {
				records = append(records, record)
			}
		}
	case *mcpStreamDeleteKey:
		for _, record := range q.db.streams {
			if q.matchesStream(record) {
				records = append(records, mcpStreamDeleteKey{PK: record.PK, SK: record.SK})
			}
		}
		for _, record := range q.db.events {
			if q.matchesEvent(record) {
				records = append(records, mcpStreamDeleteKey{PK: record.PK, SK: record.SK})
			}
		}
	default:
		return nil, fmt.Errorf("unsupported query model %T", q.model)
	}

	sort.Slice(records, func(i, j int) bool {
		left := recordSortKey(records[i])
		right := recordSortKey(records[j])
		if q.orderBy == "desc" {
			return left > right
		}
		return left < right
	})

	if q.limit > 0 && len(records) > q.limit {
		records = records[:q.limit]
	}
	return records, nil
}

func (q *fakeStreamQuery) matchesStream(record mcpStreamRecord) bool {
	return q.matchesKeyFields(record.PK, record.SK)
}

func (q *fakeStreamQuery) matchesEvent(record mcpStreamEventRecord) bool {
	return q.matchesKeyFields(record.PK, record.SK)
}

func (q *fakeStreamQuery) matchesKeyFields(pk, sk string) bool {
	if want, ok := q.lookupString("PK"); ok && want != pk {
		return false
	}
	if cond, ok := q.where["SK"]; ok {
		switch strings.ToUpper(cond.op) {
		case "=":
			want, _ := cond.value.(string)
			return want == sk
		case "BETWEEN":
			bounds, _ := cond.value.([]any)
			if len(bounds) != 2 {
				return false
			}
			lower, _ := bounds[0].(string)
			upper, _ := bounds[1].(string)
			return sk >= lower && sk <= upper
		default:
			return false
		}
	}
	return true
}

func (q *fakeStreamQuery) lookupString(field string) (string, bool) {
	cond, ok := q.where[field]
	if !ok {
		return "", false
	}
	if strings.TrimSpace(cond.op) != "=" {
		return "", false
	}
	value, ok := cond.value.(string)
	return value, ok
}

func assignFirst(dest any, record any) error {
	return assignStruct(dest, reflect.ValueOf(record))
}

func assignAll(dest any, records []any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("dest must be a non-nil pointer")
	}
	slice := rv.Elem()
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("dest must point to a slice")
	}

	result := reflect.MakeSlice(slice.Type(), 0, len(records))
	for _, record := range records {
		value := reflect.ValueOf(record)
		if value.Type().AssignableTo(slice.Type().Elem()) {
			result = reflect.Append(result, value)
			continue
		}
		if value.Type().ConvertibleTo(slice.Type().Elem()) {
			result = reflect.Append(result, value.Convert(slice.Type().Elem()))
			continue
		}
		return fmt.Errorf("cannot assign %T into %s", record, slice.Type())
	}
	slice.Set(result)
	return nil
}

func assignStruct(dest any, value reflect.Value) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("dest must be a non-nil pointer")
	}
	elem := rv.Elem()
	if !value.Type().AssignableTo(elem.Type()) {
		if value.Type().ConvertibleTo(elem.Type()) {
			value = value.Convert(elem.Type())
		} else {
			return fmt.Errorf("cannot assign %s to %s", value.Type(), elem.Type())
		}
	}
	elem.Set(value)
	return nil
}

func fakeKey(pk, sk string) string {
	return pk + "|" + sk
}

func recordSortKey(record any) string {
	switch typed := record.(type) {
	case mcpStreamRecord:
		return typed.SK
	case mcpStreamEventRecord:
		return typed.SK
	case mcpStreamDeleteKey:
		return typed.SK
	default:
		return ""
	}
}
