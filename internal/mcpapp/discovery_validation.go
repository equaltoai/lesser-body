package mcpapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const oauthAuthorizationServerMetadataPath = "/.well-known/oauth-authorization-server"

var probeAuthorizationServerMetadata = defaultProbeAuthorizationServerMetadata

func validateDiscoveryStartupConfig(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	validatedEndpoint := ""
	if raw := strings.TrimSpace(os.Getenv("MCP_ENDPOINT")); raw != "" {
		var err error
		validatedEndpoint, err = validatedMcpEndpoint(raw)
		if err != nil {
			return fmt.Errorf("validate MCP_ENDPOINT: %w", err)
		}
	}

	cfg, err := loadEffectiveTrustConfig(ctx)
	if err != nil {
		return fmt.Errorf("load trust config: %w", err)
	}
	if cfg == nil || !cfg.Present {
		return nil
	}

	trustBaseURL := strings.TrimSpace(cfg.TrustBaseURL)
	if trustBaseURL == "" {
		return fmt.Errorf("TRUST_CONFIG.TrustBaseURL is required for OAuth discovery")
	}
	if _, err := validatedPublicBaseURL(trustBaseURL); err != nil {
		return fmt.Errorf("validate TRUST_CONFIG.TrustBaseURL: %w", err)
	}

	if validatedEndpoint == "" {
		return nil
	}

	metadataURL, err := oauthAuthorizationServerMetadataURLForMcpEndpoint(validatedEndpoint)
	if err != nil {
		return fmt.Errorf("validate MCP_ENDPOINT: %w", err)
	}
	if err := probeAuthorizationServerMetadata(ctx, metadataURL); err != nil {
		return fmt.Errorf("validate MCP_ENDPOINT reachability via %s: %w", metadataURL, err)
	}

	return nil
}

func validatedMcpEndpointForRequest(ctx *apptheory.Context) (string, error) {
	configured := strings.TrimSpace(os.Getenv("MCP_ENDPOINT"))
	if configured == "" {
		inferred := inferMcpEndpointFromRequest(ctx)
		if inferred == "" {
			return "", nil
		}
		return validatedMcpEndpoint(inferred)
	}

	validatedConfigured, err := validatedMcpEndpoint(configured)
	if err != nil {
		return "", fmt.Errorf("validate MCP_ENDPOINT: %w", err)
	}

	inferred := inferMcpEndpointFromRequest(ctx)
	if inferred == "" {
		return validatedConfigured, nil
	}

	validatedInferred, err := validatedMcpEndpoint(inferred)
	if err != nil {
		return "", fmt.Errorf("infer MCP endpoint from request: %w", err)
	}
	if validatedInferred != validatedConfigured {
		return "", fmt.Errorf("configured MCP_ENDPOINT %q does not match request URL %q", validatedConfigured, validatedInferred)
	}

	return validatedConfigured, nil
}

func validatedMcpEndpoint(raw string) (string, error) {
	u, err := validatedPublicBaseURL(raw)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/mcp") {
		return "", fmt.Errorf("path must end with /mcp")
	}
	return u.String(), nil
}

func validatedPublicBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported url scheme: %s", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("url host is empty")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func oauthAuthorizationServerMetadataURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return baseURL + oauthAuthorizationServerMetadataPath
}

func authorizationServerURLForMcpEndpoint(mcpEndpoint string) (string, error) {
	u, err := validatedMcpEndpoint(mcpEndpoint)
	if err != nil {
		return "", err
	}

	base, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/mcp")
	return base.String(), nil
}

func oauthAuthorizationServerMetadataURLForMcpEndpoint(mcpEndpoint string) (string, error) {
	baseURL, err := authorizationServerURLForMcpEndpoint(mcpEndpoint)
	if err != nil {
		return "", err
	}
	return oauthAuthorizationServerMetadataURL(baseURL), nil
}

func defaultProbeAuthorizationServerMetadata(ctx context.Context, metadataURL string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request metadata: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
