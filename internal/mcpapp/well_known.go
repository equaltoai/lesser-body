package mcpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	oauthruntime "github.com/theory-cloud/apptheory/runtime/oauth"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
	"github.com/equaltoai/lesser-body/internal/trustconfig"
)

type mcpWellKnownDoc struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Endpoint     string                 `json:"endpoint,omitempty"`
	Capabilities map[string]bool        `json:"capabilities"`
	Auth         map[string]any         `json:"auth"`
	Tools        []mcpWellKnownToolHint `json:"tools,omitempty"`
}

type mcpWellKnownToolHint struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

var loadEffectiveTrustConfig = trustconfig.Default

var publicOAuthDiscoveryScopes = []string{"read", "write", "follow", "push"}

func WellKnownMcpHandler(srv *mcpserver.Server, name string, version string) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		endpoint, err := validatedMcpEndpointForRequest(ctx)
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
		}

		if srv != nil && srv.Registry() != nil {
			for _, tool := range srv.Registry().List() {
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

func WellKnownOAuthProtectedResourceHandler() apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		endpoint, err := validatedMcpEndpointForRequest(ctx)
		if err != nil {
			return invalidDiscoveryConfigResponse(err), nil
		}
		if endpoint == "" {
			return apptheory.MustJSON(404, map[string]string{"error": "not_found"}), nil
		}

		cfg, err := loadEffectiveTrustConfig(trustConfigContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("load trust config: %w", err)
		}

		issuer := ""
		if cfg != nil {
			issuer = strings.TrimSpace(cfg.TrustBaseURL)
		}
		if issuer == "" {
			return invalidDiscoveryConfigResponse(fmt.Errorf("TRUST_CONFIG.TrustBaseURL is required for OAuth discovery")), nil
		}

		md, err := oauthruntime.NewProtectedResourceMetadata(endpoint, []string{issuer})
		if err != nil {
			return nil, fmt.Errorf("build protected resource metadata: %w", err)
		}
		md.ScopesSupported = append([]string(nil), publicOAuthDiscoveryScopes...)
		md.BearerMethodsSupported = []string{"header"}

		body, err := md.MarshalJSONBytes()
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

func mcpEndpointForRequest(ctx *apptheory.Context) string {
	endpoint, err := validatedMcpEndpointForRequest(ctx)
	if err != nil {
		return ""
	}
	return endpoint
}

func protectedResourceMetadataURLForRequest(ctx *apptheory.Context) string {
	endpoint := mcpEndpointForRequest(ctx)
	if url, ok := oauthruntime.ResourceMetadataURLFromMcpEndpoint(endpoint); ok {
		return url
	}
	if ctx == nil {
		return ""
	}
	url, ok := oauthruntime.ProtectedResourceMetadataURLForRequest(ctx.Request.Headers)
	if !ok {
		return ""
	}
	return url
}

func trustConfigContext(ctx *apptheory.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx.Context()
}

func inferMcpEndpointFromRequest(ctx *apptheory.Context) string {
	if ctx == nil {
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

	return fmt.Sprintf("%s://%s/mcp", proto, strings.TrimSpace(host))
}

func invalidDiscoveryConfigResponse(err error) *apptheory.Response {
	message := "invalid discovery configuration"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}

	return apptheory.MustJSON(500, map[string]any{
		"error": map[string]any{
			"code":    "app.config_invalid",
			"message": message,
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
