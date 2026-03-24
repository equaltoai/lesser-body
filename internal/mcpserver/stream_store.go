package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
	tablecore "github.com/theory-cloud/tabletheory/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

const (
	streamMetaPrefix  = "STREAM#"
	streamEventPrefix = "#EVENT#"
	streamEventUpper  = "~"

	defaultStreamPollInitial = 100 * time.Millisecond
	defaultStreamPollMax     = time.Second
)

type DynamoStreamStore struct {
	db          tablecore.DB
	idGen       apptheory.IDGenerator
	entropyMu   sync.Mutex
	entropy     io.Reader
	pollInitial time.Duration
	pollMax     time.Duration
}

var _ mcpruntime.StreamStore = (*DynamoStreamStore)(nil)

func NewDynamoStreamStore(db tablecore.DB) mcpruntime.StreamStore {
	return &DynamoStreamStore{
		db:          db,
		idGen:       apptheory.RandomIDGenerator{},
		entropy:     ulid.Monotonic(rand.Reader, 0),
		pollInitial: defaultStreamPollInitial,
		pollMax:     defaultStreamPollMax,
	}
}

func (d *DynamoStreamStore) Create(ctx context.Context, sessionID string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("missing session id")
	}
	if d == nil || d.db == nil {
		return "", errors.New("stream store not initialized")
	}

	now := time.Now().UTC()
	streamID := d.idGen.NewID()
	record := &mcpStreamRecord{
		PK:        sessionID,
		SK:        streamMetaSortKey(streamID),
		StreamID:  streamID,
		Closed:    false,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(streamSessionTTL()).Unix(),
	}

	if err := d.db.Model(record).WithContext(ctx).Create(); err != nil {
		return "", fmt.Errorf("create stream: %w", err)
	}
	return streamID, nil
}

func (d *DynamoStreamStore) Append(ctx context.Context, sessionID, streamID string, data json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("missing session id")
	}
	if strings.TrimSpace(streamID) == "" {
		return "", errors.New("missing stream id")
	}
	if d == nil || d.db == nil {
		return "", errors.New("stream store not initialized")
	}

	stream, err := d.getStream(ctx, sessionID, streamID)
	if err != nil {
		return "", err
	}
	if stream.Closed {
		return "", mcpruntime.ErrStreamNotFound
	}

	eventKey, err := d.nextEventKey(time.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("generate stream event id: %w", err)
	}
	eventID := composeStreamEventID(streamID, eventKey)
	record := &mcpStreamEventRecord{
		PK:        sessionID,
		SK:        streamEventSortKey(streamID, eventKey),
		StreamID:  streamID,
		EventID:   eventID,
		EventKey:  eventKey,
		Data:      string(data),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(streamSessionTTL()).Unix(),
	}

	if err := d.db.Model(record).WithContext(ctx).Create(); err != nil {
		return "", fmt.Errorf("append stream event: %w", err)
	}
	return eventID, nil
}

func (d *DynamoStreamStore) Close(ctx context.Context, sessionID, streamID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("missing session id")
	}
	if strings.TrimSpace(streamID) == "" {
		return errors.New("missing stream id")
	}
	if d == nil || d.db == nil {
		return errors.New("stream store not initialized")
	}

	stream, err := d.getStream(ctx, sessionID, streamID)
	if err != nil {
		return err
	}
	stream.Closed = true
	stream.UpdatedAt = time.Now().UTC()

	if err := d.db.Model(stream).WithContext(ctx).Update("Closed", "UpdatedAt"); err != nil {
		return fmt.Errorf("close stream: %w", err)
	}
	return nil
}

func (d *DynamoStreamStore) Subscribe(ctx context.Context, sessionID, streamID, afterEventID string) (<-chan mcpruntime.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("missing session id")
	}
	if strings.TrimSpace(streamID) == "" {
		return nil, errors.New("missing stream id")
	}
	if d == nil || d.db == nil {
		return nil, errors.New("stream store not initialized")
	}

	if _, err := d.getStream(ctx, sessionID, streamID); err != nil {
		return nil, err
	}

	if _, err := validateAfterEventID(streamID, afterEventID); err != nil {
		return nil, err
	}

	out := make(chan mcpruntime.StreamEvent)
	go d.pollSubscription(ctx, sessionID, streamID, strings.TrimSpace(afterEventID), out)
	return out, nil
}

func (d *DynamoStreamStore) StreamForEvent(ctx context.Context, sessionID, eventID string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("missing session id")
	}
	if d == nil || d.db == nil {
		return "", errors.New("stream store not initialized")
	}

	streamID, eventKey, err := parseStreamEventID(eventID)
	if err != nil {
		return "", err
	}

	var record mcpStreamEventRecord
	err = d.db.Model(&mcpStreamEventRecord{}).
		WithContext(ctx).
		ConsistentRead().
		Where("PK", "=", sessionID).
		Where("SK", "=", streamEventSortKey(streamID, eventKey)).
		First(&record)
	switch {
	case err == nil:
		return streamID, nil
	case tableerrors.IsNotFound(err):
		return "", mcpruntime.ErrEventNotFound
	default:
		return "", fmt.Errorf("lookup event stream: %w", err)
	}
}

func (d *DynamoStreamStore) DeleteSession(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("missing session id")
	}
	if d == nil || d.db == nil {
		return errors.New("stream store not initialized")
	}

	var keys []mcpStreamDeleteKey
	if err := d.db.Model(&mcpStreamDeleteKey{}).
		WithContext(ctx).
		ConsistentRead().
		Select("PK", "SK").
		Where("PK", "=", sessionID).
		All(&keys); err != nil {
		if tableerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("list session stream records: %w", err)
	}

	for _, key := range keys {
		if err := d.db.Model(&mcpStreamDeleteKey{}).
			WithContext(ctx).
			Where("PK", "=", key.PK).
			Where("SK", "=", key.SK).
			Delete(); err != nil && !tableerrors.IsNotFound(err) {
			return fmt.Errorf("delete session stream record %s/%s: %w", key.PK, key.SK, err)
		}
	}

	return nil
}

func (d *DynamoStreamStore) pollSubscription(ctx context.Context, sessionID, streamID, lastSeen string, out chan<- mcpruntime.StreamEvent) {
	defer close(out)

	delay := d.pollInitial
	if delay <= 0 {
		delay = defaultStreamPollInitial
	}
	maxDelay := d.pollMax
	if maxDelay <= 0 {
		maxDelay = defaultStreamPollMax
	}

	for {
		events, closed, err := d.readSubscriptionPage(ctx, sessionID, streamID, lastSeen)
		if err != nil {
			return
		}
		if len(events) > 0 {
			for _, event := range events {
				lastSeen = event.ID
				select {
				case <-ctx.Done():
					return
				case out <- event:
				}
			}
			delay = d.pollInitial
			if delay <= 0 {
				delay = defaultStreamPollInitial
			}
			continue
		}
		if closed {
			return
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (d *DynamoStreamStore) readSubscriptionPage(ctx context.Context, sessionID, streamID, afterEventID string) ([]mcpruntime.StreamEvent, bool, error) {
	afterKey, err := validateAfterEventID(streamID, afterEventID)
	if err != nil {
		return nil, false, err
	}

	stream, err := d.getStream(ctx, sessionID, streamID)
	if err != nil {
		return nil, false, err
	}

	var records []mcpStreamEventRecord
	if err := d.db.Model(&mcpStreamEventRecord{}).
		WithContext(ctx).
		ConsistentRead().
		Where("PK", "=", sessionID).
		Where("SK", "BETWEEN", []any{
			streamEventRangeStart(streamID, afterKey),
			streamEventRangeEnd(streamID),
		}).
		OrderBy("SK", "asc").
		All(&records); err != nil && !tableerrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("list stream events: %w", err)
	}

	events := make([]mcpruntime.StreamEvent, 0, len(records))
	for _, record := range records {
		events = append(events, mcpruntime.StreamEvent{
			ID:   record.EventID,
			Data: json.RawMessage(record.Data),
		})
	}
	return events, stream.Closed, nil
}

func (d *DynamoStreamStore) getStream(ctx context.Context, sessionID, streamID string) (*mcpStreamRecord, error) {
	var record mcpStreamRecord
	err := d.db.Model(&mcpStreamRecord{}).
		WithContext(ctx).
		ConsistentRead().
		Where("PK", "=", sessionID).
		Where("SK", "=", streamMetaSortKey(streamID)).
		First(&record)
	switch {
	case err == nil:
		return &record, nil
	case tableerrors.IsNotFound(err):
		return nil, mcpruntime.ErrStreamNotFound
	default:
		return nil, fmt.Errorf("read stream: %w", err)
	}
}

func composeStreamEventID(streamID, eventKey string) string {
	return streamID + ":" + eventKey
}

func parseStreamEventID(eventID string) (string, string, error) {
	streamID, eventKey, ok := strings.Cut(strings.TrimSpace(eventID), ":")
	if !ok || strings.TrimSpace(streamID) == "" || strings.TrimSpace(eventKey) == "" {
		return "", "", fmt.Errorf("invalid last-event-id")
	}
	return streamID, eventKey, nil
}

func validateAfterEventID(streamID, afterEventID string) (string, error) {
	afterEventID = strings.TrimSpace(afterEventID)
	if afterEventID == "" {
		return "", nil
	}
	afterStreamID, eventKey, err := parseStreamEventID(afterEventID)
	if err != nil {
		return "", err
	}
	if afterStreamID != streamID {
		return "", fmt.Errorf("invalid last-event-id")
	}
	return eventKey, nil
}

func streamMetaSortKey(streamID string) string {
	return streamMetaPrefix + streamID
}

func streamEventSortKey(streamID, eventKey string) string {
	return streamMetaSortKey(streamID) + streamEventPrefix + eventKey
}

func streamEventRangeStart(streamID, afterKey string) string {
	if afterKey == "" {
		return streamEventSortKey(streamID, "")
	}
	return streamEventSortKey(streamID, afterKey+"\x00")
}

func streamEventRangeEnd(streamID string) string {
	return streamEventSortKey(streamID, streamEventUpper)
}

func (d *DynamoStreamStore) nextEventKey(now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if d == nil || d.entropy == nil {
		return "", errors.New("stream store entropy not initialized")
	}

	d.entropyMu.Lock()
	defer d.entropyMu.Unlock()

	id, err := ulid.New(ulid.Timestamp(now.UTC()), d.entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func streamSessionTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MCP_SESSION_TTL_MINUTES"))
	if raw != "" {
		if minutes, err := strconv.Atoi(raw); err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return 60 * time.Minute
}

type mcpStreamDeleteKey struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"pk"`
	SK string `theorydb:"sk,attr:SK" json:"sk"`
}

func (mcpStreamDeleteKey) TableName() string {
	return strings.TrimSpace(os.Getenv(envMcpStreamTable))
}

type mcpStreamRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"pk"`
	SK string `theorydb:"sk,attr:SK" json:"sk"`

	StreamID  string    `theorydb:"attr:streamId" json:"streamId"`
	Closed    bool      `theorydb:"attr:closed" json:"closed"`
	CreatedAt time.Time `theorydb:"attr:createdAt" json:"createdAt"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updatedAt"`
	ExpiresAt int64     `theorydb:"ttl,attr:expiresAt" json:"expiresAt"`
}

func (mcpStreamRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envMcpStreamTable))
}

type mcpStreamEventRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"pk"`
	SK string `theorydb:"sk,attr:SK" json:"sk"`

	StreamID  string    `theorydb:"attr:streamId" json:"streamId"`
	EventID   string    `theorydb:"attr:eventId" json:"eventId"`
	EventKey  string    `theorydb:"attr:eventKey" json:"eventKey"`
	Data      string    `theorydb:"attr:data" json:"data"`
	CreatedAt time.Time `theorydb:"attr:createdAt" json:"createdAt"`
	ExpiresAt int64     `theorydb:"ttl,attr:expiresAt" json:"expiresAt"`
}

func (mcpStreamEventRecord) TableName() string {
	return strings.TrimSpace(os.Getenv(envMcpStreamTable))
}
