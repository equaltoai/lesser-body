package mcpapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	oauthruntime "github.com/theory-cloud/apptheory/runtime/oauth"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

type mcpWellKnownDoc struct {
	Name            string                            `json:"name"`
	Version         string                            `json:"version"`
	Endpoint        string                            `json:"endpoint,omitempty"`
	Capabilities    map[string]bool                   `json:"capabilities"`
	Auth            map[string]any                    `json:"auth"`
	RuntimeProfiles map[string]runtimepolicy.Contract `json:"runtime_profiles,omitempty"`
	Tools           []mcpWellKnownToolHint            `json:"tools,omitempty"`
}

type mcpWellKnownToolHint struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type protectedResourceMetadataDocument struct {
	*oauthruntime.ProtectedResourceMetadata
	ResourceName string `json:"resource_name,omitempty"`
}

var loadEffectiveTrustConfig = trustconfig.Default

var publicOAuthDiscoveryScopes = []string{"read", "write", "follow", "push"}

func WellKnownMcpHandler(srv *mcpserver.Server, name string, version string) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		endpoint, err := publicDiscoveryMcpEndpointForRequest(ctx)
		if err != nil {
			return invalidDiscoveryConfigResponse(err), nil
		}

		doc := mcpWellKnownDoc{
			Name:     strings.TrimSpace(name),
			Version:  strings.TrimSpace(version),
			Endpoint: endpoint,
			Capabilities: map[string]bool{
				"tools":     true,
				"resources": srv != nil && srv.Resources() != nil && srv.Resources().Len() > 0,
				"prompts":   srv != nil && srv.Prompts() != nil && srv.Prompts().Len() > 0,
			},
			Auth: map[string]any{
				"type":   "bearer",
				"scopes": append([]string(nil), publicOAuthDiscoveryScopes...),
				"notes":  "Use a Lesser OAuth access token minted via the connector flow. Managed instance key and hardcoded bearer-token flows are deprecated inbound compatibility paths.",
			},
			RuntimeProfiles: runtimepolicy.Contracts(),
		}

		if srv != nil && srv.Registry() != nil {
			for _, tool := range srv.Registry().List() {
				if !runtimepolicy.ToolAllowed(runtimepolicy.ProfileSouled, tool.Name) {
					continue
				}
				doc.Tools = append(doc.Tools, mcpWellKnownToolHint{
					Name:        strings.TrimSpace(tool.Name),
					Description: strings.TrimSpace(tool.Description),
				})
			}
		}

		b, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal mcp.json: %w", err)
		}

		headers := map[string][]string{
			"content-type":  {"application/json"},
			"cache-control": {"public, max-age=60"},
		}
		return &apptheory.Response{Status: 200, Headers: headers, Body: b}, nil
	}
}

func WellKnownOAuthProtectedResourceHandler(probedAuthorizationServerIssuer string) apptheory.Handler {
	probedAuthorizationServerIssuer = strings.TrimSpace(probedAuthorizationServerIssuer)

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		endpoint, err := publicDiscoveryMcpEndpointForRequest(ctx)
		if err != nil {
			return invalidDiscoveryConfigResponse(err), nil
		}
		if endpoint == "" {
			return apptheory.MustJSON(404, map[string]string{"error": "not_found"}), nil
		}

		authorizationServerURL := probedAuthorizationServerIssuer
		if authorizationServerURL == "" {
			authorizationServerURL, err = authorizationServerURLForMcpEndpoint(endpoint)
			if err != nil {
				return invalidDiscoveryConfigResponse(fmt.Errorf("derive authorization server URL from MCP endpoint: %w", err)), nil
			}
		}

		md, err := oauthruntime.NewProtectedResourceMetadata(endpoint, []string{authorizationServerURL})
		if err != nil {
			return nil, fmt.Errorf("build protected resource metadata: %w", err)
		}
		md.ScopesSupported = append([]string(nil), publicOAuthDiscoveryScopes...)
		md.BearerMethodsSupported = []string{"header"}

		body, err := json.Marshal(protectedResourceMetadataDocument{
			ProtectedResourceMetadata: md,
			ResourceName:              protectedResourceNameForRequest(ctx),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal protected resource metadata: %w", err)
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

func protectedResourceNameForRequest(ctx *apptheory.Context) string {
	if actor := strings.TrimSpace(actorFromRequestContext(ctx)); actor != "" {
		return actor
	}
	return "MCP"
}

func mcpEndpointForRequest(ctx *apptheory.Context) string {
	endpoint, err := validatedMcpEndpointForRequest(ctx)
	if err != nil {
		return ""
	}
	return endpoint
}

func protectedResourceMetadataURLForRequest(ctx *apptheory.Context) string {
	endpoint, err := publicDiscoveryMcpEndpointForRequest(ctx)
	if err != nil || endpoint == "" {
		return ""
	}
	if url, ok := oauthruntime.ResourceMetadataURLFromMcpEndpoint(endpoint); ok {
		return url
	}
	return ""
}

func inferMcpEndpointFromRequest(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}

	requestPath := strings.TrimSpace(ctx.Request.Path)
	mcpPath := mcpEndpointPathFromRequest(requestPath)
	if mcpPath == "" {
		return ""
	}

	host := firstHeaderValue(ctx.Request.Headers, "x-forwarded-host")
	if host == "" {
		host = firstHeaderValue(ctx.Request.Headers, "host")
	}
	if strings.TrimSpace(host) == "" {
		return ""
	}

	proto := firstHeaderValue(ctx.Request.Headers, "x-forwarded-proto")
	if proto == "" {
		proto = "https"
	}
	proto = strings.ToLower(strings.TrimSpace(proto))
	if proto != "http" && proto != "https" {
		proto = "https"
	}

	return fmt.Sprintf("%s://%s%s", proto, strings.TrimSpace(host), mcpPath)
}

func mcpEndpointPathFromRequest(requestPath string) string {
	requestPath = strings.TrimSpace(requestPath)
	if requestPath == "" {
		return ""
	}

	requestPath = strings.TrimRight(requestPath, "/")
	switch {
	case requestPath == "/mcp":
		return requestPath
	case strings.HasPrefix(requestPath, "/mcp/"):
		if strings.Contains(strings.TrimPrefix(requestPath, "/mcp/"), "/") {
			return ""
		}
		return requestPath
	case strings.HasPrefix(requestPath, "/.well-known/oauth-protected-resource/mcp/"):
		actor := strings.TrimPrefix(requestPath, "/.well-known/oauth-protected-resource/mcp/")
		if actor == "" || strings.Contains(actor, "/") {
			return ""
		}
		return "/mcp/" + actor
	default:
		return ""
	}
}

func invalidDiscoveryConfigResponse(err error) *apptheory.Response {
	status := 500
	code := "app.config_invalid"
	message := "invalid discovery configuration"
	details := map[string]any{}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}
	var mismatch *mcpEndpointMismatchError
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

func firstHeaderValue(headers map[string][]string, key string) string {
	if len(headers) == 0 {
		return ""
	}
	for k, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(k), key) {
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
