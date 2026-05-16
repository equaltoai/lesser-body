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

// X402GrantValidationRequestForTests is the sanitized host validation request shape sent by lesser-body.
type X402GrantValidationRequestForTests = x402GrantValidationRequest

// X402GrantValidationResponseForTests is the sanitized host validation response shape consumed by lesser-body.
type X402GrantValidationResponseForTests = x402GrantValidationResponse

// SetX402GrantValidatorForTests lets sibling-package tests stub lesser-host grant validation.
func SetX402GrantValidatorForTests(fn func(context.Context, X402GrantValidationRequestForTests) (X402GrantValidationResponseForTests, error)) func() {
	previous := validateX402GrantWithHost
	if fn == nil {
		validateX402GrantWithHost = defaultValidateX402GrantWithHost
	} else {
		validateX402GrantWithHost = func(ctx context.Context, req x402GrantValidationRequest) (x402GrantValidationResponse, error) {
			return fn(ctx, req)
		}
	}
	return func() {
		validateX402GrantWithHost = previous
	}
}
