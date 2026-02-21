package mcpapp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-body/internal/mcpserver"
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

func WellKnownMcpHandler(srv *mcpserver.Server, name string, version string) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		endpoint := strings.TrimSpace(os.Getenv("MCP_ENDPOINT"))
		if endpoint == "" {
			endpoint = inferMcpEndpointFromRequest(ctx)
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
				"scopes": []string{"read", "write", "admin"},
				"notes":  "Use Lesser OAuth access token (HS256 JWT) or managed instance key.",
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
