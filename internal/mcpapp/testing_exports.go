package mcpapp

import (
	"context"

	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

// SetLoadEffectiveTrustConfigForTests lets sibling-package tests stub trust config loading.
func SetLoadEffectiveTrustConfigForTests(fn func(context.Context) (*trustconfig.Effective, error)) func() {
	previous := loadEffectiveTrustConfig
	if fn == nil {
		loadEffectiveTrustConfig = trustconfig.Default
	} else {
		loadEffectiveTrustConfig = fn
	}
	return func() {
		loadEffectiveTrustConfig = previous
	}
}
