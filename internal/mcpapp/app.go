package mcpapp

import (
	"context"
	"log/slog"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func New(name, version string) (*apptheory.App, error) {
	if err := validateDiscoveryStartupConfig(context.Background()); err != nil {
		return nil, err
	}

	srv, err := mcpserver.New(name, version)
	if err != nil {
		return nil, err
	}

	logger := slog.Default()
	app := apptheory.New(
		apptheory.WithAuthHook(auth.Hook(logger)),
	)

	app.Get("/.well-known/mcp.json", WithBrowserCORS(WellKnownMcpHandler(srv, name, version)))
	app.Get("/.well-known/oauth-protected-resource", WithBrowserCORS(WellKnownOAuthProtectedResourceHandler(cachedAuthorizationServerIssuer())))

	handler := WithBrowserCORS(WithClientCompatibilityHeaders(WithMCPAuthorization(WithAudit(WithToolContext(srv.Handler()), logger))))
	app.Post("/mcp", handler)
	app.Get("/mcp", handler)
	app.Delete("/mcp", handler)

	return app, nil
}
