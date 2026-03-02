package mcpapp

import (
	"log/slog"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

func New(name, version string) (*apptheory.App, error) {
	srv, err := mcpserver.New(name, version)
	if err != nil {
		return nil, err
	}

	logger := slog.Default()
	app := apptheory.New(
		apptheory.WithAuthHook(auth.Hook(logger)),
	)

	app.Get("/.well-known/mcp.json", WellKnownMcpHandler(srv, name, version))

	handler := WithAudit(WithToolContext(srv.Handler()), logger)
	app.Post("/mcp", handler, apptheory.RequireAuth())
	app.Get("/mcp", handler, apptheory.RequireAuth())
	app.Delete("/mcp", handler, apptheory.RequireAuth())

	return app, nil
}
