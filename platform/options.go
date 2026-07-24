// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: client configuration options for the generated Platform SDK.
// This file is hand-maintained, not generated.

package platform

import (
	"context"
	"net/http"
	"time"
)

// ClientConfig is the resolved configuration a ClientOption mutates.
// Construct it through the generated NewClient rather than directly.
type ClientConfig struct {
	// BaseURL is the platform origin, without a trailing slash.
	BaseURL string
	// AccessToken is the bearer token sent on every request.
	AccessToken string
	// AccessTokenFunc, when set, supersedes AccessToken and is consulted
	// per request — for apps that keep the token in their own store.
	AccessTokenFunc func() string
	// RefreshHandler is invoked once on a 401 to mint a fresh access token.
	RefreshHandler func(ctx context.Context) (string, error)
	// PathPrefix rewrites the generated "/api/v1" prefix, for gateways that
	// mount the API somewhere else.
	PathPrefix string
	// DefaultHeaders are merged into every request.
	DefaultHeaders map[string]string
	// HTTPClient overrides the transport used for both unary requests and
	// SSE streams.
	HTTPClient *http.Client

	// refreshOnly restricts the client to the auth endpoints, so the
	// dedicated refresh client can never re-enter the 401 retry.
	refreshOnly bool
}

// ClientOption customizes a Client at construction time.
type ClientOption func(*ClientConfig)

func newClientConfig(baseURL string) *ClientConfig {
	return &ClientConfig{
		BaseURL:        baseURL,
		DefaultHeaders: map[string]string{},
	}
}

// WithBaseURL points the client at a different platform origin.
func WithBaseURL(baseURL string) ClientOption {
	return func(c *ClientConfig) { c.BaseURL = baseURL }
}

// WithAccessToken sets the bearer token sent on every request.
func WithAccessToken(token string) ClientOption {
	return func(c *ClientConfig) { c.AccessToken = token }
}

// WithAccessTokenFunc supplies the bearer token per request. It takes
// precedence over WithAccessToken.
func WithAccessTokenFunc(fn func() string) ClientOption {
	return func(c *ClientConfig) { c.AccessTokenFunc = fn }
}

// WithRefreshHandler installs the callback used to mint a new access token
// when a request comes back 401.
func WithRefreshHandler(fn func(ctx context.Context) (string, error)) ClientOption {
	return func(c *ClientConfig) { c.RefreshHandler = fn }
}

// WithPathPrefix rewrites the generated API prefix.
func WithPathPrefix(prefix string) ClientOption {
	return func(c *ClientConfig) { c.PathPrefix = prefix }
}

// WithDefaultHeaders merges headers into every request. Repeated options
// accumulate; a later option wins on a duplicate key.
func WithDefaultHeaders(headers map[string]string) ClientOption {
	return func(c *ClientConfig) {
		if c.DefaultHeaders == nil {
			c.DefaultHeaders = map[string]string{}
		}
		for key, value := range headers {
			c.DefaultHeaders[key] = value
		}
	}
}

// WithHTTPClient overrides the transport. The supplied client is used for
// SSE streams too, so give it no Timeout (or a generous one) if you stream.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *ClientConfig) { c.HTTPClient = client }
}

// withRefreshOnly restricts a client to the auth endpoints. Used by the
// generated credential constructor for its dedicated refresh client.
func withRefreshOnly() ClientOption {
	return func(c *ClientConfig) { c.refreshOnly = true }
}

// defaultRequestTimeout bounds unary requests. Streams use a separate
// transport with no timeout, since an SSE response stays open by design.
const defaultRequestTimeout = 30 * time.Second
