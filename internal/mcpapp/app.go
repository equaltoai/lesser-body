package mcpapp

import (
	"context"
	"log/slog"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func New(name, version string) (*apptheory.App, error) {
	probeAuthorizationServerIssuer, err := validateDiscoveryStartupConfig(context.Background())
	if err != nil {
		return nil, err
	}

	srv, err := mcpserver.New(name, version)
	if err != nil {
		return nil, err
	}
	// Install the per-POST tool-context derivation (principal, bearer,
	// request-id, actor, runtime policy, share caller) on the Ka server. The
	// hook lives here because it needs mcpapp/mcpserver/runtimepolicy, and
	// mcpserver cannot import mcpapp.
	RegisterToolContextHook(srv)

	logger := slog.Default()
	app := apptheory.New(
		apptheory.WithAuthHook(auth.Hook(logger)),
	)

	app.Get("/.well-known/mcp.json", WithBrowserCORS(WellKnownMcpHandler(srv, name, version)))
	app.Get("/.well-known/oauth-protected-resource/mcp/{actor}", WithBrowserCORS(WellKnownOAuthProtectedResourceHandler(probeAuthorizationServerIssuer)))

	rootHandler := WithBrowserCORS(SharedMcpRetiredHandler())
	runtimeHandler := withActorOAuthSessionRecovery(srv.Handler())
	actorHandler := WithErrorBoundary(WithBrowserCORS(WithClientCompatibilityHeaders(WithMCPAuthorization(WithActorBinding(WithRuntimePolicy(WithAudit(runtimeHandler, logger)))))), logger)

	app.Post("/mcp/{actor}", actorHandler)
	app.Get("/mcp/{actor}", actorHandler)
	app.Delete("/mcp/{actor}", actorHandler)
	app.Post("/mcp", rootHandler)
	app.Get("/mcp", rootHandler)
	app.Delete("/mcp", rootHandler)

	return app, nil
}
