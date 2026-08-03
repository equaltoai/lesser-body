package actorendpoint

import (
	"errors"
	"testing"
)

func TestValidateReturnsTypedDivergence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		projected     string
		authoritative string
		wantError     bool
	}{
		{name: "trimmed agreement", projected: " sentinelsentinel ", authoritative: "sentinelsentinel"},
		{name: "same mixed-case value", projected: "SentinelSentinel", authoritative: "SentinelSentinel"},
		{name: "ASCII case variant", projected: "SENTINEL", authoritative: "sentinel"},
		{name: "different suffix", projected: "SentinelSentinel", authoritative: "SentinelSentinelX", wantError: true},
		{name: "Unicode fold orbit", projected: "ſentinel", authoritative: "sentinel", wantError: true},
		{name: "empty projected", projected: " ", authoritative: "sentinel", wantError: true},
		{name: "empty authoritative", projected: "sentinel", authoritative: "\t", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.projected, tc.authoritative)
			if tc.wantError && !errors.Is(err, ErrDivergence) {
				t.Fatalf("Validate(%q, %q) error = %v, want ErrDivergence", tc.projected, tc.authoritative, err)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("Validate(%q, %q) error = %v, want nil", tc.projected, tc.authoritative, err)
			}
		})
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
}
