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
	app.Get("/.well-known/oauth-protected-resource/mcp/{actor}", WithBrowserCORS(WellKnownOAuthProtectedResourceHandler(cachedAuthorizationServerIssuer())))

	rootHandler := WithBrowserCORS(SharedMcpRetiredHandler())
	actorHandler := WithBrowserCORS(WithClientCompatibilityHeaders(WithMCPAuthorization(WithActorBinding(WithAudit(WithToolContext(srv.Handler()), logger)))))

	app.Post("/mcp/{actor}", actorHandler)
	app.Get("/mcp/{actor}", actorHandler)
	app.Delete("/mcp/{actor}", actorHandler)
	app.Post("/mcp", rootHandler)
	app.Get("/mcp", rootHandler)
	app.Delete("/mcp", rootHandler)

	return app, nil
}
