package instancex402

import "context"

// ConsumeRequestForTests exposes the Host consume request shape sent by Body.
type ConsumeRequestForTests = consumeRequest

// ConsumeResponseForTests exposes the Host consume response shape consumed by Body.
type ConsumeResponseForTests = consumeResponse

// GrantViewForTests exposes the nested Host grant view for integration tests.
type GrantViewForTests = grantView

// PaymentBindingForTests exposes the nested Host payment binding for tests.
type PaymentBindingForTests = paymentBinding

// GrantUsageForTests exposes the nested Host usage view for tests.
type GrantUsageForTests = grantUsage

// SetConsumerForTests lets sibling-package tests stub lesser-host grant consumption.
func SetConsumerForTests(fn func(context.Context, ConsumeRequestForTests) (ConsumeResponseForTests, error)) func() {
	consumerMu.Lock()
	previous := consumerOverride
	if fn == nil {
		consumerOverride = nil
	} else {
		consumerOverride = func(ctx context.Context, req consumeRequest) (consumeResponse, error) {
			return fn(ctx, req)
		}
	}
	consumerMu.Unlock()
	return func() {
		consumerMu.Lock()
		consumerOverride = previous
		consumerMu.Unlock()
	}
}

// SetAgentIDResolverForTests lets tests avoid Lesser-table reads when exercising
// the instance x402 gate.
func SetAgentIDResolverForTests(fn func(context.Context, string) (string, error)) func() {
	resolverMu.Lock()
	previous := resolverOverride
	resolverOverride = fn
	resolverMu.Unlock()
	return func() {
		resolverMu.Lock()
		resolverOverride = previous
		resolverMu.Unlock()
	}
}
