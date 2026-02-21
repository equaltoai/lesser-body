package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/session"
)

const (
	envStoreMode = "LESSER_BODY_MEMORY_STORE" // "dynamo" (default) or "memory"
	envTableName = "LESSER_TABLE_NAME"
)

type AppendInput struct {
	EventID    string
	OccurredAt time.Time
	Content    string
	Tags       []string
	ExpiresAt  time.Time
	HasExpiry  bool
}

type Event struct {
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Content    string    `json:"content"`
	Tags       []string  `json:"tags,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

type AppendResult struct {
	Event   Event `json:"event"`
	Created bool  `json:"created"`
}

type QueryInput struct {
	Start  time.Time
	End    time.Time
	HasEnd bool

	Query  string
	Limit  int
	Cursor string
	Order  string // "asc" or "desc"
}

type QueryResult struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type Store interface {
	Append(ctx context.Context, agentID string, in AppendInput) (*AppendResult, error)
	Query(ctx context.Context, agentID string, in QueryInput) (*QueryResult, error)
}

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid input"
	}
	return strings.TrimSpace(e.Message)
}

func invalid(msg string) error {
	return &ValidationError{Message: msg}
}

func IsValidationError(err error) bool {
	var out *ValidationError
	return errors.As(err, &out)
}

var defaultStore struct {
	once sync.Once
	s    Store
	err  error
}

func Default() (Store, error) {
	defaultStore.once.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(envStoreMode))) {
		case "", "dynamo":
			defaultStore.s, defaultStore.err = newDefaultDynamoStore()
		case "memory":
			defaultStore.s = NewInMemoryStore()
		default:
			defaultStore.err = fmt.Errorf("%s must be \"dynamo\" or \"memory\"", envStoreMode)
		}
	})

	if defaultStore.s == nil {
		return nil, defaultStore.err
	}
	return defaultStore.s, nil
}

func ResetForTests() {
	defaultStore = struct {
		once sync.Once
		s    Store
		err  error
	}{}
}

func newDefaultDynamoStore() (Store, error) {
	if strings.TrimSpace(os.Getenv(envTableName)) == "" {
		return nil, fmt.Errorf("%s is required", envTableName)
	}

	db, err := tabletheory.NewBasic(session.Config{Region: os.Getenv("AWS_REGION")})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}
	return NewDynamoStore(db), nil
}
