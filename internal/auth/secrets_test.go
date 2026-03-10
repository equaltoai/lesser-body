package auth

import (
	"context"
	"testing"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

func TestLesserHostInstanceKeySecretID_UsesManagedTrustConfigWhenPresent(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("LESSER_TABLE_NAME", "lesser-main")
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{
			Present:              true,
			InstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123:secret:instance-key",
		}, nil
	}
	defer func() {
		loadEffectiveTrustConfig = trustconfig.Default
	}()

	got, err := lesserHostInstanceKeySecretID(context.Background())
	if err != nil {
		t.Fatalf("lesserHostInstanceKeySecretID: %v", err)
	}
	if got != "arn:aws:secretsmanager:us-east-1:123:secret:instance-key" {
		t.Fatalf("unexpected secret arn %q", got)
	}
}

func TestLesserHostInstanceKeySecretID_FailsClosedWhenManagedConfigMissing(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("LESSER_TABLE_NAME", "lesser-main")
	loadEffectiveTrustConfig = func(context.Context) (*trustconfig.Effective, error) {
		return &trustconfig.Effective{Present: true}, nil
	}
	defer func() {
		loadEffectiveTrustConfig = trustconfig.Default
	}()

	if _, err := lesserHostInstanceKeySecretID(context.Background()); err == nil {
		t.Fatalf("expected managed trust config error")
	}
}
