package mcpapp

import (
	"context"
	"reflect"
	"strings"
	"unsafe"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
)

func WithToolContext(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(c *apptheory.Context) (*apptheory.Response, error) {
		if c != nil {
			principal := auth.PrincipalFromContext(c)
			token := bearerTokenFromHeaders(c.Request.Headers)

			if principal != nil || token != "" {
				ctx := auth.InjectToolContext(c.Context(), principal, token)
				setRequestContext(c, ctx)
			}
		}

		return next(c)
	}
}

func bearerTokenFromHeaders(headers map[string][]string) string {
	for k, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(k), "authorization") {
			continue
		}
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			parts := strings.Fields(v)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				return ""
			}
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

// setRequestContext overrides apptheory.Context's internal request context so
// downstream handlers (including AppTheory's MCP server) pass it through to
// tool/resource/prompt handlers.
func setRequestContext(c *apptheory.Context, ctx context.Context) {
	if c == nil {
		return
	}

	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return
	}

	elem := v.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}

	field := elem.FieldByName("ctx")
	if !field.IsValid() || !field.CanAddr() {
		return
	}

	ptr := unsafe.Pointer(field.UnsafeAddr())
	reflect.NewAt(field.Type(), ptr).Elem().Set(reflect.ValueOf(ctx))
}
