package mcpapp

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
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
