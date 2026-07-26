package instanceapp

import (
	"context"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"unsafe"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/ptahserver"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	SurfacePtah = "ptah"
	SurfaceBa   = "ba"
)

// New builds the instance-plane Lambda app. Ptah and Ba are intentionally
// separate AppTheory MCP server instances so each plane can grow its own static
// registry without affecting Ka's actor-scoped /mcp/{actor} runtime.
func New(name, version string, custom ...Option) (*apptheory.App, error) {
	opts := applyOptions(custom)

	name = strings.TrimSpace(name)
	if name == "" {
		name = "lesser-body-instance"
	}

	ptah := newPlaneServer(name, version, SurfacePtah)
	if err := ptahserver.RegisterTools(ptah.Registry()); err != nil {
		return nil, err
	}
	if err := ptahserver.RegisterResources(ptah); err != nil {
		return nil, err
	}
	if err := ptahserver.RegisterPrompts(ptah); err != nil {
		return nil, err
	}
	ba := newPlaneServer(name, version, SurfaceBa)
	if err := baserver.RegisterTools(ba.Registry(), opts.baToolOptions...); err != nil {
		return nil, err
	}

	app := apptheory.New(
		apptheory.WithAuthHook(auth.Hook(slog.Default())),
	)

	app.Post("/instance/ptah/mcp", instanceMCPHandler(ptah, opts.baInstanceEndpoint, SurfacePtah), apptheory.RequireAuth())
	app.Post("/instance/ba/mcp", instanceMCPHandler(ba, opts.baInstanceEndpoint, SurfaceBa), apptheory.RequireAuth())
	app.Get(installerGrantPathPattern, installerGrantHandler(opts))
	app.Get(instanceProtectedResourceMetadataPath(SurfacePtah), mcpapp.WithBrowserCORS(wellKnownProtectedResourceHandler(opts.baInstanceEndpoint, SurfacePtah)))
	app.Get(instanceProtectedResourceMetadataPath(SurfaceBa), mcpapp.WithBrowserCORS(wellKnownProtectedResourceHandler(opts.baInstanceEndpoint, SurfaceBa)))

	return app, nil
}

func instanceMCPHandler(server *mcpruntime.Server, endpointTemplate string, surface string) apptheory.Handler {
	runtimeHandler := mcpapp.WithOAuthSessionRecovery(
		server.Handler(),
		func(ctx *apptheory.Context) string {
			return instanceProtectedResourceMetadataURLForRequest(ctx, endpointTemplate, surface)
		},
		mcpapp.MCPAuthorizationScopes,
	)
	return requireInstancePrincipal(withToolContext(runtimeHandler))
}

func newPlaneServer(appName, version, surface string) *mcpruntime.Server {
	capabilities := mcpruntime.CapabilityConfig{
		Tools: true,
	}
	if surface == SurfacePtah {
		capabilities.Resources = true
		capabilities.Prompts = true
	}
	return mcpruntime.NewServer(
		appName+"-"+surface,
		strings.TrimSpace(version),
		mcpruntime.WithCapabilityConfig(capabilities),
	)
}

func withToolContext(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if ctx != nil {
			principal := auth.PrincipalFromContext(ctx)
			token := bearerTokenFromHeaders(ctx.Request.Headers)
			requestID := strings.TrimSpace(ctx.RequestID)
			if principal != nil || token != "" || requestID != "" {
				toolCtx := auth.InjectToolContext(ctx.Context(), principal, token)
				toolCtx = auth.WithToolRequestID(toolCtx, requestID)
				toolCtx = auth.InjectToolRequestSnapshot(toolCtx, auth.ToolRequestSnapshot{
					Headers: ctx.Request.Headers,
					Body:    ctx.Request.Body,
					Path:    ctx.Request.Path,
				})
				setRequestContext(ctx, toolCtx)
			}
		}
		return next(ctx)
	}
}

func bearerTokenFromHeaders(headers map[string][]string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "authorization") {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			parts := strings.Fields(value)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				return ""
			}
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

// setRequestContext overrides apptheory.Context's internal request context so
// AppTheory's MCP server passes the authenticated principal into tool handlers.
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

func requireInstancePrincipal(next apptheory.Handler) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		if !instancePrincipalAllowed(ctx) {
			return instancePrincipalForbiddenResponse(), nil
		}
		return next(ctx)
	}
}

func instancePrincipalAllowed(ctx *apptheory.Context) bool {
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil {
		return false
	}
	if principal.Claims.IsAgent {
		return false
	}
	if strings.TrimSpace(principal.Claims.GetUsername()) == "" {
		return false
	}

	expectedAudience := instanceAudienceForRequest(ctx)
	if expectedAudience == "" {
		return false
	}
	return auth.ValidateTokenAudience(principal.Claims, expectedAudience) == nil
}

func instancePrincipalForbiddenResponse() *apptheory.Response {
	return apptheory.MustJSON(403, map[string]any{
		"error": map[string]any{
			"code":    "instance_principal_not_allowed",
			"message": "instance-plane MCP requires an account-holder OAuth token for this instance resource",
		},
	})
}

func instanceAudienceForRequest(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}

	host := strings.ToLower(strings.TrimSpace(firstHeaderValue(ctx, "host")))
	if host == "" {
		return ""
	}

	proto := strings.ToLower(strings.TrimSpace(firstCommaSeparatedHeaderValue(ctx, "x-forwarded-proto")))
	if proto == "" {
		proto = "https"
	}
	if proto != "https" {
		return ""
	}

	path := strings.TrimSpace(ctx.Request.Path)
	if path == "" || !strings.HasPrefix(path, "/") {
		return ""
	}

	return (&url.URL{
		Scheme: proto,
		Host:   host,
		Path:   path,
	}).String()
}

func firstHeaderValue(ctx *apptheory.Context, name string) string {
	if ctx == nil {
		return ""
	}
	name = strings.TrimSpace(name)
	for key, values := range ctx.Request.Headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstCommaSeparatedHeaderValue(ctx *apptheory.Context, name string) string {
	value := firstHeaderValue(ctx, name)
	if before, _, ok := strings.Cut(value, ","); ok {
		return strings.TrimSpace(before)
	}
	return value
}
