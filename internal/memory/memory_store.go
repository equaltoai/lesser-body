package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type InMemoryStore struct {
	mu     sync.RWMutex
	events map[string]map[string]Event // agentID -> eventID -> event
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		events: map[string]map[string]Event{},
	}
}

func (s *InMemoryStore) Append(_ context.Context, agentID string, in AppendInput) (*AppendResult, error) {
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

	ev := Event{
		EventID:    eventID,
		OccurredAt: occurredAt.UTC(),
		Content:    content,
		Tags:       normalizeTags(in.Tags),
	}
	if in.HasExpiry {
		ev.ExpiresAt = in.ExpiresAt.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.events == nil {
		s.events = map[string]map[string]Event{}
	}
	if s.events[agentID] == nil {
		s.events[agentID] = map[string]Event{}
	}

	if existing, ok := s.events[agentID][eventID]; ok {
		return &AppendResult{Event: existing, Created: false}, nil
	}

	s.events[agentID][eventID] = ev
	return &AppendResult{Event: ev, Created: true}, nil
}

func (s *InMemoryStore) Query(_ context.Context, agentID string, in QueryInput) (*QueryResult, error) {
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

	queryText := strings.ToLower(strings.TrimSpace(in.Query))

	s.mu.RLock()
	agentEvents := s.events[agentID]
	s.mu.RUnlock()

	now := time.Now().UTC()
	out := make([]Event, 0, len(agentEvents))
	for _, ev := range agentEvents {
		if !ev.ExpiresAt.IsZero() && !ev.ExpiresAt.After(now) {
			continue
		}
		if !in.Start.IsZero() && ev.OccurredAt.Before(in.Start.UTC()) {
			continue
		}
		if in.HasEnd && !in.End.IsZero() && ev.OccurredAt.After(in.End.UTC()) {
			continue
		}
		if queryText != "" && !strings.Contains(strings.ToLower(ev.Content), queryText) {
			continue
		}
		out = append(out, ev)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if order == "asc" {
			return out[i].EventID < out[j].EventID
		}
		return out[i].EventID > out[j].EventID
	})

	if len(out) > limit {
		out = out[:limit]
	}

	return &QueryResult{Events: out}, nil
}
