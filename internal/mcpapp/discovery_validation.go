package mcpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

const oauthAuthorizationServerMetadataPath = "/.well-known/oauth-authorization-server"
const mcpActorPlaceholder = "{actor}"

type authorizationServerMetadata struct {
	Issuer string `json:"issuer"`
}

type mcpEndpointInfo struct {
	BasePath string
	Actor    string
}

type mcpEndpointMismatchError struct {
	Configured string
	Requested  string
}

func (e *mcpEndpointMismatchError) Error() string {
	if e == nil {
		return "configured MCP_ENDPOINT does not match request URL"
	}
	return fmt.Sprintf("configured MCP_ENDPOINT %q does not match request URL %q", e.Configured, e.Requested)
}

var probeAuthorizationServerMetadata = defaultProbeAuthorizationServerMetadata

var authorizationServerIssuerCache struct {
	mu     sync.RWMutex
	issuer string
}

func validateDiscoveryStartupConfig(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	setAuthorizationServerIssuer("")

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
	issuer, err := probeAuthorizationServerMetadata(ctx, metadataURL)
	if err != nil {
		return fmt.Errorf("validate MCP_ENDPOINT reachability via %s: %w", metadataURL, err)
	}
	if _, err := validatedPublicBaseURL(issuer); err != nil {
		return fmt.Errorf("validate authorization server issuer %q: %w", issuer, err)
	}
	setAuthorizationServerIssuer(issuer)

	return nil
}

func validatedMcpEndpointForRequest(ctx *apptheory.Context) (string, error) {
	resolvedConfigured, err := configuredMcpEndpointForRequest(ctx)
	if err != nil {
		return "", err
	}

	inferred := inferMcpEndpointFromRequest(ctx)
	if inferred == "" {
		return resolvedConfigured, nil
	}

	validatedInferred, err := validatedMcpEndpoint(inferred)
	if err != nil {
		return "", fmt.Errorf("infer MCP endpoint from request: %w", err)
	}
	if validatedInferred != resolvedConfigured {
		return "", &mcpEndpointMismatchError{
			Configured: resolvedConfigured,
			Requested:  validatedInferred,
		}
	}

	return resolvedConfigured, nil
}

func publicDiscoveryMcpEndpointForRequest(ctx *apptheory.Context) (string, error) {
	if strings.TrimSpace(os.Getenv("MCP_ENDPOINT")) == "" {
		return "", fmt.Errorf("MCP_ENDPOINT is required for public discovery metadata")
	}
	return validatedMcpEndpointForRequest(ctx)
}

func configuredMcpEndpointForRequest(ctx *apptheory.Context) (string, error) {
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

	resolvedConfigured, err := resolveConfiguredMcpEndpointForRequest(validatedConfigured, ctx)
	if err != nil {
		return "", fmt.Errorf("resolve configured MCP endpoint: %w", err)
	}

	return resolvedConfigured, nil
}

func validatedMcpEndpoint(raw string) (string, error) {
	u, err := validatedPublicBaseURL(raw)
	if err != nil {
		return "", err
	}
	info, err := parseMcpEndpointPath(u.Path)
	if err != nil {
		return "", err
	}
	return canonicalAbsoluteURL(u, buildMcpEndpointPath(info)), nil
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

// ProbeAuthorizationServerIssuer resolves the authoritative issuer published by
// an OAuth authorization server. Protected resources may be hosted on a
// different origin, so callers must publish this returned issuer rather than
// assuming the resource origin is also the authorization server identifier.
func ProbeAuthorizationServerIssuer(ctx context.Context, authorizationServerURL string) (string, error) {
	if _, err := validatedPublicBaseURL(authorizationServerURL); err != nil {
		return "", fmt.Errorf("validate authorization server URL: %w", err)
	}

	metadataURL := oauthAuthorizationServerMetadataURL(authorizationServerURL)
	issuer, err := probeAuthorizationServerMetadata(ctx, metadataURL)
	if err != nil {
		return "", fmt.Errorf("probe %s: %w", metadataURL, err)
	}
	issuer = strings.TrimSpace(issuer)
	if _, err := validatedPublicBaseURL(issuer); err != nil {
		return "", fmt.Errorf("validate authorization server issuer %q: %w", issuer, err)
	}
	return issuer, nil
}

func authorizationServerURLForMcpEndpoint(mcpEndpoint string) (string, error) {
	validatedEndpoint, err := validatedMcpEndpoint(mcpEndpoint)
	if err != nil {
		return "", err
	}

	base, err := url.Parse(validatedEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	info, err := parseMcpEndpointPath(base.Path)
	if err != nil {
		return "", err
	}
	return canonicalAbsoluteURL(base, info.BasePath), nil
}

func oauthAuthorizationServerMetadataURLForMcpEndpoint(mcpEndpoint string) (string, error) {
	baseURL, err := authorizationServerURLForMcpEndpoint(mcpEndpoint)
	if err != nil {
		return "", err
	}
	return oauthAuthorizationServerMetadataURL(baseURL), nil
}

func defaultProbeAuthorizationServerMetadata(ctx context.Context, metadataURL string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var metadata authorizationServerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&metadata); err != nil {
		return "", fmt.Errorf("decode metadata response: %w", err)
	}

	issuer := strings.TrimSpace(metadata.Issuer)
	if issuer == "" {
		return "", fmt.Errorf("metadata response missing issuer")
	}
	return issuer, nil
}

func cachedAuthorizationServerIssuer() string {
	authorizationServerIssuerCache.mu.RLock()
	defer authorizationServerIssuerCache.mu.RUnlock()
	return authorizationServerIssuerCache.issuer
}

func setAuthorizationServerIssuer(issuer string) {
	authorizationServerIssuerCache.mu.Lock()
	defer authorizationServerIssuerCache.mu.Unlock()
	authorizationServerIssuerCache.issuer = strings.TrimSpace(issuer)
}

func resolveConfiguredMcpEndpointForRequest(configured string, ctx *apptheory.Context) (string, error) {
	info, err := parsedMcpEndpoint(configured)
	if err != nil {
		return "", err
	}
	if info.Actor != mcpActorPlaceholder {
		return configured, nil
	}

	actor := actorFromRequestContext(ctx)
	if actor == "" {
		return configured, nil
	}

	u, err := url.Parse(configured)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	info.Actor = actor
	return canonicalAbsoluteURL(u, buildMcpEndpointPath(info)), nil
}

func parsedMcpEndpoint(raw string) (mcpEndpointInfo, error) {
	u, err := validatedPublicBaseURL(raw)
	if err != nil {
		return mcpEndpointInfo{}, err
	}
	return parseMcpEndpointPath(u.Path)
}

func parseMcpEndpointPath(path string) (mcpEndpointInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return mcpEndpointInfo{}, fmt.Errorf("path must include /mcp")
	}
	path = strings.TrimRight(path, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	trimmed := strings.TrimPrefix(path, "/")
	segments := strings.Split(trimmed, "/")
	mcpIndex := -1
	for i, segment := range segments {
		if segment == "mcp" {
			mcpIndex = i
			break
		}
	}
	if mcpIndex < 0 {
		return mcpEndpointInfo{}, fmt.Errorf("path must include /mcp")
	}
	if len(segments) > mcpIndex+2 {
		return mcpEndpointInfo{}, fmt.Errorf("path may only include one segment after /mcp")
	}

	basePath := ""
	if mcpIndex > 0 {
		basePath = "/" + strings.Join(segments[:mcpIndex], "/")
	}
	actor := ""
	if len(segments) == mcpIndex+2 {
		actor = strings.TrimSpace(segments[mcpIndex+1])
		if actor == "" {
			return mcpEndpointInfo{}, fmt.Errorf("actor segment after /mcp must be non-empty")
		}
	}

	return mcpEndpointInfo{
		BasePath: basePath,
		Actor:    actor,
	}, nil
}

func buildMcpEndpointPath(info mcpEndpointInfo) string {
	path := strings.TrimRight(strings.TrimSpace(info.BasePath), "/")
	if path == "" {
		path = "/mcp"
	} else {
		path += "/mcp"
	}
	if actor := strings.TrimSpace(info.Actor); actor != "" {
		path += "/" + actor
	}
	return path
}

func canonicalAbsoluteURL(u *url.URL, path string) string {
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

func actorFromRequestContext(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	if actor := strings.TrimSpace(ctx.Params["actor"]); actor != "" {
		return actor
	}
	path := mcpEndpointPathFromRequest(ctx.Request.Path)
	if !strings.HasPrefix(path, "/mcp/") {
		return ""
	}
	actor := strings.TrimSpace(strings.TrimPrefix(path, "/mcp/"))
	if actor == "" || strings.Contains(actor, "/") {
		return ""
	}
	return actor
}
