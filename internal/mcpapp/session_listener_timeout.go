package mcpapp

import (
	"context"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const (
	envMcpSessionListenerTimeout = "MCP_SESSION_LISTENER_TIMEOUT"

	sessionListenerLambdaBuffer = 5 * time.Second
	sessionListenerLambdaCap    = 25 * time.Second
)

func WithSessionListenerTimeoutBudget(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if timeout, ok := sessionListenerTimeout(ctx); ok {
			// The response body outlives handler return, so we intentionally do not
			// call cancel here. The timeout will close the GET listener cleanly.
			boundedCtx, _ := context.WithTimeout(ctx.Context(), timeout)
			setRequestContext(ctx, boundedCtx)
		}
		return next(ctx)
	}
}

func sessionListenerTimeout(ctx *apptheory.Context) (time.Duration, bool) {
	if !isInitialSessionListenerRequest(ctx) {
		return 0, false
	}

	if raw := strings.TrimSpace(os.Getenv(envMcpSessionListenerTimeout)); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err == nil && timeout > 0 {
			return timeout, true
		}
	}

	if ctx == nil || ctx.RemainingMS <= 0 {
		return 0, false
	}

	remaining := time.Duration(ctx.RemainingMS) * time.Millisecond
	if remaining <= sessionListenerLambdaBuffer {
		return 0, false
	}

	timeout := remaining - sessionListenerLambdaBuffer
	if timeout > sessionListenerLambdaCap {
		timeout = sessionListenerLambdaCap
	}
	if timeout <= 0 {
		return 0, false
	}
	return timeout, true
}

func isInitialSessionListenerRequest(ctx *apptheory.Context) bool {
	if ctx == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(ctx.Request.Method), "GET") {
		return false
	}
	return firstHeaderValue(ctx.Request.Headers, "last-event-id") == ""
}
