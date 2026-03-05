package soulapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	envBaseURL  = "LESSER_SOUL_API_BASE_URL"
	envTimeoutS = "LESSER_SOUL_API_TIMEOUT_SECONDS"
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
	Status  int
	Body    []byte
	Headers http.Header
}

func (e *APIError) Error() string {
	if e == nil {
		return "lesser soul api error"
	}
	msg := strings.TrimSpace(string(e.Body))
	if msg == "" {
		return fmt.Sprintf("lesser soul api error (status=%d)", e.Status)
	}
	if len(msg) > 512 {
		msg = msg[:512] + "…"
	}
	return fmt.Sprintf("lesser soul api error (status=%d): %s", e.Status, msg)
}

func (e *APIError) RetryAfterSeconds() int {
	if e == nil || len(e.Headers) == 0 {
		return 0
	}
	raw := strings.TrimSpace(e.Headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

func (c *Client) DoJSON(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) (any, error) {
	if c == nil || c.baseURL == nil || c.http == nil {
		return nil, fmt.Errorf("lesser soul api client not initialized")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if method == "" || path == "" {
		return nil, fmt.Errorf("missing method or path")
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
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: respBody, Headers: resp.Header.Clone()}
	}

	if len(respBody) == 0 {
		return map[string]any{}, nil
	}

	var out any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return out, nil
}

func resolveBaseURL() (*url.URL, error) {
	raw := strings.TrimSpace(os.Getenv(envBaseURL))
	if raw == "" {
		return nil, fmt.Errorf("%s is required", envBaseURL)
	}
	return parseBaseURL(raw)
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
