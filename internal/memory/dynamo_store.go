package memory

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

const (
	pkPrefix = "LBMEMORY#"
	skPrefix = "EVENT#"
)

type DynamoStore struct {
	db tablecore.DB
}

func NewDynamoStore(db tablecore.DB) *DynamoStore {
	return &DynamoStore{db: db}
}

func (s *DynamoStore) Append(ctx context.Context, agentID string, in AppendInput) (*AppendResult, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, invalid("missing agent id")
	}

	eventID, occurredAt, err := resolveEventID(in.EventID, in.OccurredAt)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(in.Content)
	if content == "" {
		return nil, invalid("missing content")
	}

	now := time.Now().UTC()
	record := &memoryEventRecord{
		PK:           pkPrefix + agentID,
		SK:           skPrefix + eventID,
		EventID:      eventID,
		AgentID:      agentID,
		Content:      content,
		ContentLower: strings.ToLower(content),
		Tags:         normalizeTags(in.Tags),
		Timestamp:    occurredAt,
		CreatedAt:    now,
	}

	if in.HasExpiry {
		record.TTL = in.ExpiresAt.UTC().Unix()
	}

	err = s.db.Model(record).WithContext(ctx).IfNotExists().Create()
	switch {
	case err == nil:
		return &AppendResult{Event: record.toEvent(), Created: true}, nil
	case tableerrors.IsConditionFailed(err):
		var existing memoryEventRecord
		getErr := s.db.Model(&memoryEventRecord{}).
			WithContext(ctx).
			Where("PK", "=", record.PK).
			Where("SK", "=", record.SK).
			First(&existing)
		if getErr != nil {
			return nil, fmt.Errorf("fetch existing memory event: %w", getErr)
		}
		return &AppendResult{Event: existing.toEvent(), Created: false}, nil
	default:
		return nil, fmt.Errorf("append memory event: %w", err)
	}
}

func (s *DynamoStore) Query(ctx context.Context, agentID string, in QueryInput) (*QueryResult, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, invalid("missing agent id")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	order := strings.ToLower(strings.TrimSpace(in.Order))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return nil, invalid("invalid order (expected asc or desc)")
	}

	q := s.db.Model(&memoryEventRecord{}).
		WithContext(ctx).
		Where("PK", "=", pkPrefix+agentID).
		OrderBy("SK", order).
		Limit(limit)

	if strings.TrimSpace(in.Cursor) != "" {
		if err := q.SetCursor(strings.TrimSpace(in.Cursor)); err != nil {
			return nil, invalid("invalid cursor")
		}
	}

	if !in.Start.IsZero() && in.HasEnd && !in.End.IsZero() {
		q = q.Where("SK", "BETWEEN", []any{skLowerBound(in.Start), skUpperBound(in.End)})
	} else if !in.Start.IsZero() {
		q = q.Where("SK", ">=", skLowerBound(in.Start))
	} else if in.HasEnd && !in.End.IsZero() {
		q = q.Where("SK", "<=", skUpperBound(in.End))
	}

	nowUnix := time.Now().UTC().Unix()
	q = q.OrFilterGroup(func(q tablecore.Query) {
		q.Filter("TTL", "NOT_EXISTS", nil)
		q.OrFilter("TTL", ">", nowUnix)
	})

	queryText := strings.TrimSpace(in.Query)
	if queryText != "" {
		q = q.Filter("ContentLower", "CONTAINS", strings.ToLower(queryText))
	}

	var records []memoryEventRecord
	page, err := q.AllPaginated(&records)
	if err != nil {
		return nil, fmt.Errorf("query memory events: %w", err)
	}

	out := make([]Event, 0, len(records))
	for _, r := range records {
		out = append(out, r.toEvent())
	}

	// Normalize sort for in-memory store parity in edge cases.
	sort.SliceStable(out, func(i, j int) bool {
		if order == "asc" {
			return out[i].EventID < out[j].EventID
		}
		return out[i].EventID > out[j].EventID
	})

	res := &QueryResult{Events: out}
	if page != nil {
		res.NextCursor = strings.TrimSpace(page.NextCursor)
	}
	return res, nil
}

type memoryEventRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"pk"`
	SK string `theorydb:"sk,attr:SK" json:"sk"`

	EventID      string    `theorydb:"attr:eventID" json:"event_id"`
	AgentID      string    `theorydb:"attr:agentID" json:"agent_id"`
	Content      string    `theorydb:"attr:content" json:"content"`
	ContentLower string    `theorydb:"attr:contentLower,omitempty" json:"-"`
	Tags         []string  `theorydb:"attr:tags,omitempty" json:"tags,omitempty"`
	Timestamp    time.Time `theorydb:"attr:timestamp" json:"timestamp"`
	CreatedAt    time.Time `theorydb:"attr:createdAt" json:"created_at"`
	TTL          int64     `theorydb:"ttl,attr:ttl,omitempty" json:"ttl,omitempty"`
}

func (memoryEventRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envTableName))
}

func (r memoryEventRecord) toEvent() Event {
	out := Event{
		EventID:    r.EventID,
		OccurredAt: r.Timestamp.UTC(),
		Content:    r.Content,
		Tags:       normalizeTags(r.Tags),
	}
	if r.TTL > 0 {
		out.ExpiresAt = time.Unix(r.TTL, 0).UTC()
	}
	return out
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func resolveEventID(raw string, occurredAt time.Time) (string, time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		id, err := ulid.ParseStrict(raw)
		if err != nil {
			return "", time.Time{}, invalid("invalid event_id")
		}
		ts := id.Timestamp().UTC()
		if !occurredAt.IsZero() {
			want := occurredAt.UTC().Truncate(time.Millisecond)
			have := ts.Truncate(time.Millisecond)
			if want.UnixMilli() != have.UnixMilli() {
				return "", time.Time{}, invalid("event_id does not match occurred_at")
			}
		}
		return id.String(), ts, nil
	}

	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	id, err := ulid.New(ulid.Timestamp(occurredAt.UTC()), rand.Reader)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate event_id: %w", err)
	}
	return id.String(), id.Timestamp().UTC(), nil
}

func skLowerBound(t time.Time) string {
	var id ulid.ULID
	_ = id.SetTime(ulid.Timestamp(t.UTC()))
	_ = id.SetEntropy(make([]byte, 10))
	return skPrefix + id.String()
}

func skUpperBound(t time.Time) string {
	var id ulid.ULID
	_ = id.SetTime(ulid.Timestamp(t.UTC()))
	entropy := make([]byte, 10)
	for i := range entropy {
		entropy[i] = 0xFF
	}
	_ = id.SetEntropy(entropy)
	return skPrefix + id.String()
}
