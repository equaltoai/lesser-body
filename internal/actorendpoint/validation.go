// Package actorendpoint validates the actor path segment projected into an
// actor-scoped MCP endpoint against Lesser's authoritative actor username.
package actorendpoint

import (
	"errors"
	"fmt"
	"strings"
)

var ErrDivergence = errors.New("actor endpoint projection diverges from authoritative Lesser actor")

// DivergenceError is returned when a projected registry local_id would render
// a different actor-scoped MCP endpoint than Lesser's authoritative actor.
type DivergenceError struct {
	ProjectedLocalID           string
	AuthoritativeActorUsername string
}

func (e *DivergenceError) Error() string {
	if e == nil {
		return ErrDivergence.Error()
	}
	return fmt.Sprintf(
		"%s: projected local_id %q does not match actor username %q",
		ErrDivergence,
		strings.TrimSpace(e.ProjectedLocalID),
		strings.TrimSpace(e.AuthoritativeActorUsername),
	)
}

func (e *DivergenceError) Unwrap() error { return ErrDivergence }

// Validate compares trimmed identifiers using Lesser's ASCII-lowercase
// username contract while preserving the source values in a typed error.
// Deliberately do not use strings.ToLower or strings.EqualFold: only ASCII A-Z
// maps to a-z, so non-ASCII never folds into ASCII by construction.
func Validate(projectedLocalID string, authoritativeActorUsername string) error {
	projected := strings.TrimSpace(projectedLocalID)
	authoritative := strings.TrimSpace(authoritativeActorUsername)
	if projected == "" || authoritative == "" || asciiLower(projected) != asciiLower(authoritative) {
		return &DivergenceError{
			ProjectedLocalID:           projected,
			AuthoritativeActorUsername: authoritative,
		}
	}
	return nil
}

func asciiLower(value string) string {
	lowered := []byte(value)
	for i, b := range lowered {
		if b >= 'A' && b <= 'Z' {
			lowered[i] = b + ('a' - 'A')
		}
	}
	return string(lowered)
}
