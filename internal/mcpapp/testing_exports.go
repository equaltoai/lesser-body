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

// SetProbeAuthorizationServerMetadataForTests lets sibling-package tests stub discovery validation probes.
func SetProbeAuthorizationServerMetadataForTests(fn func(context.Context, string) (string, error)) func() {
	previous := probeAuthorizationServerMetadata
	if fn == nil {
		probeAuthorizationServerMetadata = defaultProbeAuthorizationServerMetadata
	} else {
		probeAuthorizationServerMetadata = fn
	}
	return func() {
		probeAuthorizationServerMetadata = previous
	}
}
