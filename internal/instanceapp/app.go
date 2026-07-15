package instanceapp

import (
	"log/slog"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	SurfacePtah = "ptah"
	SurfaceBa   = "ba"
)

// New builds the instance-plane Lambda app. Ptah and Ba are intentionally
// separate AppTheory MCP server instances so each plane can grow its own static
// registry without affecting Ka's actor-scoped /mcp/{actor} runtime.
func New(name, version string) (*apptheory.App, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "lesser-body-instance"
	}

	ptah := newPlaneServer(name, version, SurfacePtah)
	ba := newPlaneServer(name, version, SurfaceBa)

	app := apptheory.New(
		apptheory.WithAuthHook(auth.Hook(slog.Default())),
	)

	app.Post("/instance/ptah/mcp", rejectX402Headers(ptah.Handler()), apptheory.RequireAuth())
	app.Post("/instance/ba/mcp", rejectX402Headers(ba.Handler()), apptheory.RequireAuth())
	app.Get("/.well-known/oauth-protected-resource/instance/ptah/mcp", wellKnownStubHandler(SurfacePtah))
	app.Get("/.well-known/oauth-protected-resource/instance/ba/mcp", wellKnownStubHandler(SurfaceBa))

	return app, nil
}

func newPlaneServer(appName, version, surface string) *mcpruntime.Server {
	return mcpruntime.NewServer(
		appName+"-"+surface,
		strings.TrimSpace(version),
		mcpruntime.WithCapabilityConfig(mcpruntime.CapabilityConfig{
			Tools: true,
		}),
	)
}

func wellKnownStubHandler(surface string) apptheory.Handler {
	surface = strings.TrimSpace(surface)
	return func(_ *apptheory.Context) (*apptheory.Response, error) {
		return apptheory.MustJSON(501, map[string]any{
			"error": map[string]any{
				"code":    "instance_metadata_not_implemented",
				"message": "instance-plane OAuth metadata is not implemented in this foundation slice",
				"details": map[string]any{
					"surface": surface,
				},
			},
		}), nil
	}
}

func rejectX402Headers(next apptheory.Handler) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if hasAnyHeader(ctx, x402HeaderNames...) {
			return apptheory.MustJSON(400, map[string]any{
				"error": map[string]any{
					"code":    "x402_not_supported",
					"message": "x402 instance-plane invocation is not supported in this foundation slice",
				},
			}), nil
		}
		return next(ctx)
	}
}

var x402HeaderNames = []string{
	"lesser-x402-grant-id",
	"x-lesser-x402-grant-id",
	"lesser-x402-grant",
	"x-lesser-x402-grant",
	"lesser-x402-capability",
	"x-lesser-x402-capability",
	"payment-signature",
	"x-payment",
}

func hasAnyHeader(ctx *apptheory.Context, names ...string) bool {
	if ctx == nil {
		return false
	}
	for key, values := range ctx.Request.Headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		for _, name := range names {
			if !strings.EqualFold(key, name) {
				continue
			}
			for _, value := range values {
				if strings.TrimSpace(value) != "" {
					return true
				}
			}
		}
	}
	return false
}
