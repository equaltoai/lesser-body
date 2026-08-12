package lesserapi

import (
	"context"
	"strings"
)

// ActAsHeader is Lesser's share-grant act-as indicator header
// (docs/contracts/agent-share-act-as.md in Lesser). When present on an
// act-as-enabled endpoint and an active (agent, caller) share grant exists,
// Lesser performs the request agent-scoped with the authenticated caller
// recorded as actedBy attribution. It is honored only on the enabled surfaces
// enumerated by that contract and ignored everywhere else.
const ActAsHeader = "X-Lesser-Act-As"

type actAsContextKey struct{}

// WithActAs returns a context that directs this client's outbound requests to
// carry Lesser's share-grant act-as indicator naming the shared agent's plain
// local username. The caller's own bearer token still authenticates the
// request; the header only selects the agent scope under Lesser's per-request
// grant check. An empty username leaves the context unchanged, so owner-path
// requests never carry the header.
func WithActAs(ctx context.Context, agentUsername string) context.Context {
	agentUsername = strings.TrimSpace(agentUsername)
	if ctx == nil || agentUsername == "" {
		return ctx
	}
	return context.WithValue(ctx, actAsContextKey{}, agentUsername)
}

// ActAsFromContext returns the act-as agent username attached via WithActAs,
// or "" when the context carries no indicator.
func ActAsFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(actAsContextKey{}).(string)
	return strings.TrimSpace(value)
}
