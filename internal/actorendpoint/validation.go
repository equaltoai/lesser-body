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

// Validate compares trimmed identifiers using the lowercase-canonical contract
// Lesser applies to usernames while preserving the source values in a typed
// error. Deliberately do not use strings.EqualFold: Unicode simple-fold orbits
// can equate a non-ASCII identifier with a genuinely different ASCII actor.
func Validate(projectedLocalID string, authoritativeActorUsername string) error {
	projected := strings.TrimSpace(projectedLocalID)
	authoritative := strings.TrimSpace(authoritativeActorUsername)
	if projected == "" || authoritative == "" || strings.ToLower(projected) != strings.ToLower(authoritative) {
		return &DivergenceError{
			ProjectedLocalID:           projected,
			AuthoritativeActorUsername: authoritative,
		}
	}
	return nil
}
