// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: HTTP transport for the generated Platform SDK.
// This file is hand-maintained, not generated. Port of the Python SDK's
// archastro/platform/runtime/http_client.py.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// defaultAPIPrefix is the path prefix PathPrefix rewrites, matching the
// generated default API version.
const defaultAPIPrefix = "/api/v1"

// RawResponse is the payload of an operation that returns bytes rather than
// JSON (file downloads, exports).
type RawResponse struct {
	// Content is the response body.
	Content []byte
	// MimeType is the response Content-Type, or "text/plain" when absent.
	MimeType string
}

// HTTPClient is the transport every generated resource method issues
// requests through. It owns auth headers, one-shot 401 token refresh
// (deduplicated across concurrent requests), structured APIError parsing,
// and SSE streaming.
type HTTPClient struct {
	baseURL         string
	pathPrefix      string
	defaultHeaders  map[string]string
	accessTokenFunc func() string
	client          *http.Client
	streamClient    *http.Client
	refreshOnly     bool

	mu             sync.Mutex
	accessToken    string
	refreshHandler func(ctx context.Context) (string, error)
	inFlight       *refreshCall
}

// refreshCall lets concurrent 401s piggyback on one refresh instead of
// stampeding the auth endpoint.
type refreshCall struct {
	done  chan struct{}
	token string
	err   error
}

func newHTTPClient(cfg *ClientConfig) *HTTPClient {
	client := cfg.HTTPClient
	streamClient := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
		streamClient = &http.Client{}
	}
	headers := make(map[string]string, len(cfg.DefaultHeaders))
	for key, value := range cfg.DefaultHeaders {
		headers[key] = value
	}
	return &HTTPClient{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		pathPrefix:      cfg.PathPrefix,
		defaultHeaders:  headers,
		accessTokenFunc: cfg.AccessTokenFunc,
		client:          client,
		streamClient:    streamClient,
		refreshOnly:     cfg.refreshOnly,
		accessToken:     cfg.AccessToken,
		refreshHandler:  cfg.RefreshHandler,
	}
}

// SetAccessToken replaces the bearer token sent on every request.
func (c *HTTPClient) SetAccessToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = token
}

// AccessToken returns the bearer token currently in use.
func (c *HTTPClient) AccessToken() string {
	if c.accessTokenFunc != nil {
		return c.accessTokenFunc()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
}

// SetRefreshHandler installs the callback used to mint a new access token
// when a request comes back 401.
func (c *HTTPClient) SetRefreshHandler(fn func(ctx context.Context) (string, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshHandler = fn
}

// requestSpec is one outbound request, as the generated resource methods
// describe it.
type requestSpec struct {
	Method string
	Path   string
	Body   any
	Query  url.Values
	Header http.Header
}

// fetch issues a request and decodes the JSON body into T.
//
// A bodyless 204 decodes as JSON null when T is JSONValue; for typed models
// it is an error, because the operation promised a body.
func fetch[T any](c *HTTPClient, ctx context.Context, spec requestSpec) (T, error) {
	var zero T
	data, resp, err := c.execute(ctx, spec)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(data)) == 0 {
		var empty T
		if _, ok := any(empty).(JSONValue); ok {
			return empty, nil
		}
		return zero, &APIError{
			Status:  resp.StatusCode,
			Code:    "empty_response",
			Message: "server returned no body for an operation that promises one",
		}
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, fmt.Errorf("platform: decoding %s %s: %w", spec.Method, spec.Path, err)
	}
	return out, nil
}

// requestVoid issues a request and discards any response body.
func (c *HTTPClient) requestVoid(ctx context.Context, spec requestSpec) error {
	_, _, err := c.execute(ctx, spec)
	return err
}

// requestRaw issues a request and returns the raw bytes plus MIME type.
func (c *HTTPClient) requestRaw(ctx context.Context, spec requestSpec) (*RawResponse, error) {
	data, resp, err := c.execute(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &RawResponse{Content: data, MimeType: mimeTypeOf(resp)}, nil
}

// execute performs the request with the auth gate, one-shot 401
// auto-refresh, and structured error handling.
func (c *HTTPClient) execute(ctx context.Context, spec requestSpec) ([]byte, *http.Response, error) {
	if err := c.checkRefreshOnly(spec.Path); err != nil {
		return nil, nil, err
	}
	body, err := encodeBody(spec)
	if err != nil {
		return nil, nil, err
	}

	data, resp, err := c.roundTrip(ctx, spec, body, "")
	if err != nil {
		return nil, nil, err
	}

	// Auto-refresh: on a 401 outside the auth endpoints, take one shot at a
	// token refresh and retry. Concurrent 401s share the same refresh.
	if resp.StatusCode == http.StatusUnauthorized && !isAuthPath(spec.Path) {
		if token, ok := c.refreshAccessToken(ctx); ok {
			c.SetAccessToken(token)
			data, resp, err = c.roundTrip(ctx, spec, body, "")
			if err != nil {
				return nil, nil, err
			}
		}
	}

	if resp.StatusCode >= 400 {
		return nil, resp, parseAPIError(data, resp.StatusCode)
	}
	return data, resp, nil
}

func (c *HTTPClient) roundTrip(
	ctx context.Context,
	spec requestSpec,
	body []byte,
	accept string,
) ([]byte, *http.Response, error) {
	req, err := c.newRequest(ctx, spec, body, accept)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("platform: %s %s: %w", spec.Method, spec.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("platform: reading %s %s: %w", spec.Method, spec.Path, err)
	}
	return data, resp, nil
}

func (c *HTTPClient) newRequest(
	ctx context.Context,
	spec requestSpec,
	body []byte,
	accept string,
) (*http.Request, error) {
	target := c.baseURL + c.transformPath(spec.Path)
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("platform: invalid URL %q: %w", target, err)
	}
	if len(spec.Query) > 0 {
		parsed.RawQuery = spec.Query.Encode()
	}

	method := spec.Method
	if method == "" {
		method = http.MethodGet
	}
	sendsBody := len(body) > 0 && method != http.MethodGet && method != http.MethodHead

	var reader io.Reader
	if sendsBody {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("platform: building %s %s: %w", method, spec.Path, err)
	}

	for key, value := range c.defaultHeaders {
		req.Header.Set(key, value)
	}
	if sendsBody || accept == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token := c.AccessToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, values := range spec.Header {
		for _, value := range values {
			req.Header.Set(key, value)
		}
	}
	return req, nil
}

func (c *HTTPClient) transformPath(path string) string {
	if c.pathPrefix == "" {
		return path
	}
	if strings.HasPrefix(path, defaultAPIPrefix) {
		return c.pathPrefix + strings.TrimPrefix(path, defaultAPIPrefix)
	}
	return path
}

func (c *HTTPClient) checkRefreshOnly(path string) error {
	if c.refreshOnly && !isAuthPath(path) {
		return fmt.Errorf("%w: %s", ErrRefreshOnly, path)
	}
	return nil
}

func isAuthPath(path string) bool {
	return strings.HasPrefix(path, defaultAPIPrefix+"/auth/")
}

// refreshAccessToken runs the refresh handler at most once per burst of
// 401s. It reports false when no handler is installed or the refresh failed
// — the caller then surfaces the original 401.
func (c *HTTPClient) refreshAccessToken(ctx context.Context) (string, bool) {
	c.mu.Lock()
	if c.refreshHandler == nil {
		c.mu.Unlock()
		return "", false
	}
	if existing := c.inFlight; existing != nil {
		c.mu.Unlock()
		<-existing.done
		if existing.err != nil {
			return "", false
		}
		return existing.token, true
	}
	call := &refreshCall{done: make(chan struct{})}
	handler := c.refreshHandler
	c.inFlight = call
	c.mu.Unlock()

	call.token, call.err = handler(ctx)
	close(call.done)

	c.mu.Lock()
	c.inFlight = nil
	c.mu.Unlock()

	if call.err != nil {
		return "", false
	}
	return call.token, true
}

func encodeBody(spec requestSpec) ([]byte, error) {
	if spec.Body == nil {
		return nil, nil
	}
	data, err := json.Marshal(spec.Body)
	if err != nil {
		return nil, fmt.Errorf("platform: encoding body for %s %s: %w", spec.Method, spec.Path, err)
	}
	return data, nil
}

func mimeTypeOf(resp *http.Response) string {
	if value := resp.Header.Get("Content-Type"); value != "" {
		return value
	}
	return "text/plain"
}

// websocketURL derives the Phoenix socket endpoint from an HTTP base URL.
func websocketURL(baseURL string, path string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
