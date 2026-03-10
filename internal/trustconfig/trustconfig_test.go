package trustconfig

import "testing"

func TestEffectiveFromRecord_PrefersOverrideAndDefaultsAttestations(t *testing.T) {
	overrideBase := "https://override.example/"
	record := &trustConfigRecord{
		Managed: &trustConfigLayer{
			BaseURL:              "https://managed.example/",
			InstanceKeySecretARN: "arn:managed",
		},
		Override: &trustConfigPatch{
			BaseURL:              &overrideBase,
			InstanceKeySecretARN: nil,
		},
	}

	got := effectiveFromRecord(record)
	if !got.Present {
		t.Fatalf("expected Present=true")
	}
	if got.TrustBaseURL != "https://override.example" {
		t.Fatalf("unexpected trust base url %q", got.TrustBaseURL)
	}
	if got.AttestationsBaseURL != "https://override.example" {
		t.Fatalf("unexpected attestations base url %q", got.AttestationsBaseURL)
	}
	if got.InstanceKeySecretARN != "arn:managed" {
		t.Fatalf("unexpected instance key arn %q", got.InstanceKeySecretARN)
	}
}
