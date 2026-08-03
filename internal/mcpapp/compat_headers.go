package mcpapp

import (
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

var mcpExposeHeaders = []string{
	"mcp-session-id",
	"www-authenticate",
}

func WithClientCompatibilityHeaders(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resp, err := next(ctx)
		if err != nil || resp == nil {
			return resp, err
		}
		mergeResponseHeader(resp, "access-control-expose-headers", mcpExposeHeaders...)
		return resp, nil
	}
}

func WithBrowserCORS(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resp, err := next(ctx)
		if err != nil || resp == nil {
			return resp, err
		}
		if origin := allowedRequestOrigin(ctx); origin != "" {
			resp.Headers["access-control-allow-origin"] = []string{origin}
			mergeResponseHeader(resp, "vary", "origin")
		}
		return resp, nil
	}
}

func allowedRequestOrigin(ctx *apptheory.Context) string {
	origin := strings.TrimSpace(firstHeaderValue(headersForContext(ctx), "origin"))
	if origin == "" {
		return ""
	}

	allowedOrigins := mcpserver.SplitCommaSeparatedEnv(os.Getenv("MCP_ALLOWED_ORIGINS"))
	if len(allowedOrigins) == 0 {
		return ""
	}
	for _, allowed := range allowedOrigins {
		if allowed == "*" {
			return "*"
		}
		if origin == allowed {
			return origin
		}
	}
	return ""
}

func headersForContext(ctx *apptheory.Context) map[string][]string {
	if ctx == nil {
		return nil
	}
	return ctx.Request.Headers
}

func mergeResponseHeader(resp *apptheory.Response, key string, values ...string) {
	if resp == nil || len(values) == 0 {
		return
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}

	existing := resp.Headers[key]
	seen := make(map[string]struct{}, len(existing)+len(values))
	merged := make([]string, 0, len(existing)+len(values))
	for _, value := range existing {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		merged = append(merged, trimmed)
	}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		merged = append(merged, trimmed)
	}
	if len(merged) == 0 {
		return
	}

	resp.Headers[key] = []string{strings.Join(merged, ", ")}
}
