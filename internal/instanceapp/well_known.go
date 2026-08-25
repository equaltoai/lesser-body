package instanceapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	oauthruntime "github.com/theory-cloud/apptheory/v4/runtime/oauth"
)

const instanceSurfacePlaceholder = "{surface}"

var instanceOAuthDiscoveryScopes = []string{"read", "write", "follow", "push"}

type instanceEndpointInfo struct {
	BasePath string
	Surface  string
}

type instanceEndpointMismatchError struct {
	Configured string
	Requested  string
}

func (e *instanceEndpointMismatchError) Error() string {
	if e == nil {
		return "configured INSTANCE_MCP_ENDPOINT does not match request URL"
	}
	return fmt.Sprintf("configured INSTANCE_MCP_ENDPOINT %q does not match request URL %q", e.Configured, e.Requested)
}

func wellKnownProtectedResourceHandler(instanceEndpointTemplate string, authorizationServerIssuer string, surface string) apptheory.Handler {
	instanceEndpointTemplate = strings.TrimSpace(instanceEndpointTemplate)
	authorizationServerIssuer = strings.TrimSpace(authorizationServerIssuer)
	surface = strings.TrimSpace(surface)

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resource, err := instanceEndpointForRequest(ctx, instanceEndpointTemplate, surface)
		if err != nil {
			return invalidInstanceDiscoveryConfigResponse(err), nil
		}

		if authorizationServerIssuer == "" {
			return invalidInstanceDiscoveryConfigResponse(fmt.Errorf("authorization server issuer is unavailable for instance protected-resource metadata")), nil
		}

		md, err := oauthruntime.NewProtectedResourceMetadata(resource, []string{authorizationServerIssuer})
		if err != nil {
			return nil, fmt.Errorf("build instance protected resource metadata: %w", err)
		}
		md.ScopesSupported = append([]string(nil), instanceOAuthDiscoveryScopes...)
		md.BearerMethodsSupported = []string{"header"}

		body, err := json.Marshal(md)
		if err != nil {
			return nil, fmt.Errorf("marshal instance protected resource metadata: %w", err)
		}

		return &apptheory.Response{
			Status: 200,
			Headers: map[string][]string{
				"content-type":  {"application/json"},
				"cache-control": {"public, max-age=60"},
			},
			Body: body,
		}, nil
	}
}

func resolveInstanceAuthorizationServerIssuer(ctx context.Context, instanceEndpointTemplate string) (string, error) {
	if strings.TrimSpace(instanceEndpointTemplate) == "" {
		return "", nil
	}

	resource, err := instanceEndpointForSurface(instanceEndpointTemplate, SurfacePtah)
	if err != nil {
		return "", err
	}
	authorizationServerURL, err := authorizationServerURLForInstanceEndpoint(resource, SurfacePtah)
	if err != nil {
		return "", fmt.Errorf("derive authorization server URL from instance MCP endpoint: %w", err)
	}
	issuer, err := mcpapp.ProbeAuthorizationServerIssuer(ctx, authorizationServerURL)
	if err != nil {
		return "", fmt.Errorf("discover authorization server issuer from %s: %w", authorizationServerURL, err)
	}
	return issuer, nil
}

func instanceEndpointForRequest(ctx *apptheory.Context, endpointTemplate string, surface string) (string, error) {
	configured, err := instanceEndpointForSurface(endpointTemplate, surface)
	if err != nil {
		return "", err
	}

	inferred := inferInstanceEndpointFromRequest(ctx, surface)
	if inferred == "" {
		return configured, nil
	}

	validatedInferred, err := validateInstanceEndpoint(inferred, surface)
	if err != nil {
		return "", fmt.Errorf("infer instance MCP endpoint from request: %w", err)
	}
	if validatedInferred != configured {
		return "", &instanceEndpointMismatchError{
			Configured: configured,
			Requested:  validatedInferred,
		}
	}

	return configured, nil
}

func instanceEndpointForSurface(endpointTemplate string, surface string) (string, error) {
	endpointTemplate = strings.TrimSpace(endpointTemplate)
	surface = strings.TrimSpace(surface)
	if endpointTemplate == "" {
		return "", fmt.Errorf("%s is required for instance protected-resource metadata", baserver.EnvInstanceMCPEndpoint)
	}
	if surface != SurfacePtah && surface != SurfaceBa {
		return "", fmt.Errorf("unsupported instance surface %q", surface)
	}

	endpoint := strings.ReplaceAll(endpointTemplate, instanceSurfacePlaceholder, surface)
	return validateInstanceEndpoint(endpoint, surface)
}

func validateInstanceEndpoint(raw string, surface string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", baserver.EnvInstanceMCPEndpoint, err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%s must be an https URL", baserver.EnvInstanceMCPEndpoint)
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("%s host is empty", baserver.EnvInstanceMCPEndpoint)
	}
	info, err := parseInstanceEndpointPath(u.Path, surface)
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	u.Fragment = ""
	return canonicalInstanceAbsoluteURL(u, buildInstanceEndpointPath(info)), nil
}

func inferInstanceEndpointFromRequest(ctx *apptheory.Context, surface string) string {
	if ctx == nil {
		return ""
	}
	resourcePath := instanceEndpointPathFromRequest(ctx.Request.Path, surface)
	if resourcePath == "" {
		return ""
	}

	host := firstRequestHeaderValue(ctx.Request.Headers, "x-forwarded-host")
	if host == "" {
		host = firstRequestHeaderValue(ctx.Request.Headers, "host")
	}
	if strings.TrimSpace(host) == "" {
		return ""
	}

	proto := firstRequestHeaderValue(ctx.Request.Headers, "x-forwarded-proto")
	if proto == "" {
		proto = "https"
	}
	proto = strings.ToLower(strings.TrimSpace(proto))
	if proto != "http" && proto != "https" {
		proto = "https"
	}

	return fmt.Sprintf("%s://%s%s", proto, strings.TrimSpace(host), resourcePath)
}

func instanceEndpointPathFromRequest(requestPath string, surface string) string {
	requestPath = strings.TrimRight(strings.TrimSpace(requestPath), "/")
	switch requestPath {
	case "/instance/" + surface + "/mcp":
		return requestPath
	case instanceProtectedResourceMetadataPath(surface):
		return "/instance/" + surface + "/mcp"
	default:
		return ""
	}
}

func instanceProtectedResourceMetadataPath(surface string) string {
	return "/.well-known/oauth-protected-resource/instance/" + strings.TrimSpace(surface) + "/mcp"
}

func instanceProtectedResourceMetadataURLForRequest(ctx *apptheory.Context, endpointTemplate string, surface string) string {
	resource, err := instanceEndpointForRequest(ctx, endpointTemplate, surface)
	if err != nil {
		return ""
	}
	metadataURL, ok := oauthruntime.ResourceMetadataURLFromMcpEndpoint(resource)
	if !ok {
		return ""
	}
	return metadataURL
}

func authorizationServerURLForInstanceEndpoint(resource string, surface string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(resource))
	if err != nil {
		return "", fmt.Errorf("parse instance endpoint: %w", err)
	}
	info, err := parseInstanceEndpointPath(u.Path, surface)
	if err != nil {
		return "", err
	}
	return canonicalInstanceAbsoluteURL(u, info.BasePath), nil
}

func parseInstanceEndpointPath(path string, surface string) (instanceEndpointInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return instanceEndpointInfo{}, fmt.Errorf("%s path must include /instance/%s/mcp", baserver.EnvInstanceMCPEndpoint, surface)
	}
	path = strings.TrimRight(path, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 3 {
		return instanceEndpointInfo{}, fmt.Errorf("%s path must include /instance/%s/mcp", baserver.EnvInstanceMCPEndpoint, surface)
	}
	for i := 0; i <= len(segments)-3; i++ {
		if segments[i] != "instance" || segments[i+1] != surface || segments[i+2] != "mcp" {
			continue
		}
		if i+3 != len(segments) {
			return instanceEndpointInfo{}, fmt.Errorf("%s path may not include segments after /instance/%s/mcp", baserver.EnvInstanceMCPEndpoint, surface)
		}
		basePath := ""
		if i > 0 {
			basePath = "/" + strings.Join(segments[:i], "/")
		}
		return instanceEndpointInfo{
			BasePath: basePath,
			Surface:  surface,
		}, nil
	}

	return instanceEndpointInfo{}, fmt.Errorf("%s path must include /instance/%s/mcp", baserver.EnvInstanceMCPEndpoint, surface)
}

func buildInstanceEndpointPath(info instanceEndpointInfo) string {
	path := strings.TrimRight(strings.TrimSpace(info.BasePath), "/")
	if path == "" {
		path = "/instance/" + info.Surface + "/mcp"
	} else {
		path += "/instance/" + info.Surface + "/mcp"
	}
	return path
}

func canonicalInstanceAbsoluteURL(u *url.URL, path string) string {
	if u == nil {
		return ""
	}
	scheme := strings.TrimSpace(u.Scheme)
	host := strings.TrimSpace(u.Host)
	if scheme == "" || host == "" {
		return ""
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path == "/" {
		path = ""
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}

func invalidInstanceDiscoveryConfigResponse(err error) *apptheory.Response {
	status := 500
	code := "app.config_invalid"
	message := "invalid instance discovery configuration"
	details := map[string]any{}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}

	var mismatch *instanceEndpointMismatchError
	if errors.As(err, &mismatch) {
		status = 400
		code = "app.invalid_public_url"
		details["reason"] = "configured_endpoint_mismatch"
		details["canonical_mcp_url"] = mismatch.Configured
		details["requested_mcp_url"] = mismatch.Requested
		if resourceMetadataURL, ok := oauthruntime.ResourceMetadataURLFromMcpEndpoint(mismatch.Configured); ok {
			details["canonical_resource_metadata_url"] = resourceMetadataURL
		}
	}

	return apptheory.MustJSON(status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

func firstRequestHeaderValue(headers map[string][]string, key string) string {
	for candidate, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(candidate), key) {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}
