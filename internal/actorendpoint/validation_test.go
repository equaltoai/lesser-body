package actorendpoint

import (
	"errors"
	"testing"
)

func TestValidateReturnsTypedDivergence(t *testing.T) {
	if err := Validate(" sentinelsentinel ", "sentinelsentinel"); err != nil {
		t.Fatalf("trimmed agreement: %v", err)
	}

	err := Validate("sentinel", "sentinelsentinel")
	if !errors.Is(err, ErrDivergence) {
		t.Fatalf("errors.Is(ErrDivergence) = false: %v", err)
	}
	var divergence *DivergenceError
	if !errors.As(err, &divergence) {
		t.Fatalf("errors.As(DivergenceError) = false: %T %v", err, err)
	}
	if divergence.ProjectedLocalID != "sentinel" || divergence.AuthoritativeActorUsername != "sentinelsentinel" {
		t.Fatalf("divergence = %+v", divergence)
	}
	if err := Validate("SentinelSentinel", "sentinelsentinel"); !errors.Is(err, ErrDivergence) {
		t.Fatalf("case-changing projection must fail closed: %v", err)
	}
}
