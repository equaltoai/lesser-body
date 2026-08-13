package mcpapp

import (
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

// WithErrorBoundary logs any unhandled error or panic that is about to become
// an `app.internal` HTTP 500 at the AppTheory error boundary. It must be the
// outermost actor-route middleware so it observes every downstream gate and the
// MCP runtime itself, including the grantee-only share-caller path.
//
// The log record carries the wrapped error message, the request_id, the route
// and resolved actor, and a best-effort caller class. It deliberately never
// reads or logs the Authorization header, the request body, bearer tokens,
// x402 grant material, or any other secret-bearing input.
func WithErrorBoundary(next apptheory.Handler, logger *slog.Logger) apptheory.Handler {
	if next == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	return func(ctx *apptheory.Context) (resp *apptheory.Response, err error) {
		defer func() {
			if r := recover(); r != nil {
				logBoundaryFailure(logger, ctx, "panic", strings.TrimSpace(string(debug.Stack())))
				resp = nil
				err = &apptheory.AppError{Code: "app.internal", Message: "internal error"}
			}
		}()

		resp, err = next(ctx)
		if err != nil && shouldLogBoundaryError(err) {
			logBoundaryFailure(logger, ctx, "error", strings.TrimSpace(err.Error()))
		}
		return resp, err
	}
}

// shouldLogBoundaryError reports whether err will surface as an app.internal
// 500 rather than an intentional, structured 4xx. Plain errors and panics are
// the cases the boundary previously emitted silently; structured AppErrors and
// AppTheoryErrors keep their existing audit paths.
func shouldLogBoundaryError(err error) bool {
	if err == nil {
		return false
	}
	var appErr *apptheory.AppError
	if errors.As(err, &appErr) {
		return appErr.Code == "" || appErr.Code == "app.internal"
	}
	var portableErr *apptheory.AppTheoryError
	if errors.As(err, &portableErr) {
		return portableErr.Code == "" || portableErr.Code == "app.internal"
	}
	return true
}

func logBoundaryFailure(logger *slog.Logger, ctx *apptheory.Context, kind string, detail string) {
	if logger == nil {
		return
	}

	requestID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
	}

	attrs := []any{
		"event", "mcp_error_boundary",
		"kind", kind,
		"error", detail,
		"request_id", requestID,
	}
	if ctx != nil {
		attrs = append(attrs,
			"route", strings.TrimSpace(ctx.Request.Path),
			"actor", actorFromRequestContext(ctx),
			"caller_class", boundaryCallerClass(ctx),
			"principal_type", boundaryPrincipalType(ctx),
			"identity", boundaryIdentity(ctx),
			"delegated_by", boundaryDelegatedBy(ctx),
			"share_caller", boundaryShareCaller(ctx),
		)
	}

	logger.Error("MCP request failed before a response was produced", attrs...)
}

func boundaryCallerClass(ctx *apptheory.Context) string {
	if ctx == nil {
		return "unknown"
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil {
		return "unknown"
	}
	switch principal.Type {
	case auth.PrincipalTypeX402Grant:
		return "public_paid"
	case auth.PrincipalTypeInstanceKey:
		return "instance_key"
	case auth.PrincipalTypeOAuthToken:
		if _, shared := mcpserver.ShareCallerFromContext(ctx.Context()); shared {
			return "bound_body"
		}
		if boundaryDelegatedBy(ctx) != "" {
			return "principal_operator"
		}
		return "bound_body"
	default:
		return "unknown"
	}
}

func boundaryPrincipalType(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil {
		return ""
	}
	return strings.TrimSpace(string(principal.Type))
}

func boundaryIdentity(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil {
		return ""
	}
	return strings.TrimSpace(principal.Identity)
}

func boundaryDelegatedBy(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil || principal.Claims == nil {
		return ""
	}
	return strings.TrimSpace(principal.Claims.DelegatedBy)
}

func boundaryShareCaller(ctx *apptheory.Context) bool {
	if ctx == nil {
		return false
	}
	_, shared := mcpserver.ShareCallerFromContext(ctx.Context())
	return shared
}
