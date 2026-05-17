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

// X402GrantConsumeRequestForTests is the accepted lesser-host consume request shape sent by lesser-body.
type X402GrantConsumeRequestForTests = x402GrantConsumeRequest

// X402GrantConsumeResponseForTests is the accepted lesser-host consume response shape consumed by lesser-body.
type X402GrantConsumeResponseForTests = x402GrantConsumeResponse

// X402InvocationGrantViewForTests exposes the nested Host grant view for sibling-package tests.
type X402InvocationGrantViewForTests = x402InvocationGrantView

// X402GrantPaymentBindingForTests exposes the nested Host payment binding for sibling-package tests.
type X402GrantPaymentBindingForTests = x402GrantPaymentBinding

// X402InvocationGrantUsageForTests exposes the nested Host usage view for sibling-package tests.
type X402InvocationGrantUsageForTests = x402InvocationGrantUsage

// SetX402GrantConsumerForTests lets sibling-package tests stub lesser-host grant consumption.
func SetX402GrantConsumerForTests(fn func(context.Context, X402GrantConsumeRequestForTests) (X402GrantConsumeResponseForTests, error)) func() {
	previous := consumeX402GrantWithHost
	if fn == nil {
		consumeX402GrantWithHost = defaultConsumeX402GrantWithHost
	} else {
		consumeX402GrantWithHost = func(ctx context.Context, req x402GrantConsumeRequest) (x402GrantConsumeResponse, error) {
			return fn(ctx, req)
		}
	}
	return func() {
		consumeX402GrantWithHost = previous
	}
}
