package lesserapi

import (
	"bytes"
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
)

const (
	envBaseURL  = "LESSER_API_BASE_URL"
	envMcpURL   = "MCP_ENDPOINT"
	envTimeoutS = "LESSER_API_TIMEOUT_SECONDS"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

var defaultClient struct {
	once sync.Once
	c    *Client
	err  error
}

func Default() (*Client, error) {
	defaultClient.once.Do(func() {
		base, err := resolveBaseURL()
		if err != nil {
			defaultClient.err = err
			return
		}
		defaultClient.c = &Client{
			baseURL: base,
			http: &http.Client{
				Timeout: resolveTimeout(),
			},
		}
	})
	if defaultClient.c == nil {
		return nil, defaultClient.err
	}
	return defaultClient.c, nil
}

func ResetForTests() {
	defaultClient = struct {
		once sync.Once
		c    *Client
		err  error
	}{}
}

type APIError struct {
	Status int
	Body   []byte
}

func (e *APIError) Error() string {
	if e == nil {
		return "lesser api error"
	}
	msg := strings.TrimSpace(string(e.Body))
	if msg == "" {
		return fmt.Sprintf("lesser api error (status=%d)", e.Status)
	}
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	return fmt.Sprintf("lesser api error (status=%d): %s", e.Status, msg)
}

func (c *Client) DoJSON(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) (any, error) {
	out, _, err := c.DoJSONWithHeaders(ctx, method, path, query, bearerToken, body)
	return out, err
}

func (c *Client) DoJSONWithHeaders(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) (any, http.Header, error) {
	if c == nil || c.baseURL == nil || c.http == nil {
		return nil, nil, fmt.Errorf("lesser api client not initialized")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if method == "" || path == "" {
		return nil, nil, fmt.Errorf("missing method or path")
	}

	endpoint := *c.baseURL
	endpoint.Path = joinPath(endpoint.Path, path)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearerToken))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	headers := resp.Header.Clone()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, headers, &APIError{Status: resp.StatusCode, Body: respBody}
	}

	if len(respBody) == 0 {
		return map[string]any{}, headers, nil
	}

	var out any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return out, headers, nil
}

func resolveBaseURL() (*url.URL, error) {
	if raw := strings.TrimSpace(os.Getenv(envBaseURL)); raw != "" {
		return parseBaseURL(raw)
	}
	if raw := strings.TrimSpace(os.Getenv(envMcpURL)); raw != "" {
		u, err := parseBaseURL(raw)
		if err != nil {
			return nil, err
		}
		if err := stripMcpPath(u); err != nil {
			return nil, err
		}
		return u, nil
	}
	return nil, fmt.Errorf("%s or %s is required", envBaseURL, envMcpURL)
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported base url scheme: %s", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("base url host is empty")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func resolveTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envTimeoutS)); raw != "" {
		if v, err := time.ParseDuration(raw + "s"); err == nil && v > 0 {
			return v
		}
	}
	return 10 * time.Second
}

func joinPath(base string, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	suffix = strings.TrimLeft(strings.TrimSpace(suffix), "/")
	if base == "" {
		return "/" + suffix
	}
	if suffix == "" {
		return base
	}
	return base + "/" + suffix
}

func stripMcpPath(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("base url is nil")
	}

	path := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	if path == "" {
		return fmt.Errorf("mcp endpoint path must include /mcp")
	}

	idx := strings.Index(path, "/mcp")
	if idx < 0 {
		return fmt.Errorf("mcp endpoint path must include /mcp")
	}
	rest := path[idx:]
	if rest != "/mcp" && !strings.HasPrefix(rest, "/mcp/") {
		return fmt.Errorf("mcp endpoint path must include /mcp as a path segment")
	}

	u.Path = path[:idx]
	if u.Path == "/" {
		u.Path = ""
	}
	return nil
}
