// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for the runtime's HTTP transport, driven against httptest
// servers. The contract tests prove the generated surface against a spec
// mock; these prove the transport behaviors no generated method can express
// — auth headers, the one-shot 401 refresh, path rewriting, and the typed
// response shapes.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.Handler, opts ...ClientOption) *HTTPClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := newClientConfig(server.URL)
	for _, opt := range opts {
		opt(cfg)
	}
	return newHTTPClient(cfg)
}

func TestRequestSendsAuthAndDefaultHeaders(t *testing.T) {
	var seen http.Header
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}),
		WithAccessToken("test-token"),
		WithDefaultHeaders(map[string]string{"x-archastro-api-key": "pk_test"}),
	)

	if _, err := fetch[JSONValue](client, context.Background(), requestSpec{
		Method: http.MethodGet,
		Path:   "/api/v1/agents",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := seen.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("expected a bearer token, got %q", got)
	}
	if got := seen.Get("x-archastro-api-key"); got != "pk_test" {
		t.Fatalf("expected the API key header, got %q", got)
	}
}

func TestRequestEncodesBodyAndQuery(t *testing.T) {
	var seenBody []byte
	var seenQuery url.Values
	var seenMethod string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenQuery = r.URL.Query()
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"agent_1"}`))
	}))

	type agent struct {
		ID string `json:"id"`
	}
	result, err := fetch[*agent](client, context.Background(), requestSpec{
		Method: http.MethodPost,
		Path:   "/api/v1/agents",
		Body:   map[string]any{"name": "support-bot"},
		Query:  url.Values{"expand": []string{"tools"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", seenMethod)
	}
	if got := seenQuery.Get("expand"); got != "tools" {
		t.Fatalf("expected the query parameter, got %q", got)
	}
	if string(seenBody) != `{"name":"support-bot"}` {
		t.Fatalf("unexpected body: %s", seenBody)
	}
	if result.ID != "agent_1" {
		t.Fatalf("expected the decoded model, got %+v", result)
	}
}

func TestRequestOmitsBodyOnGET(t *testing.T) {
	var seenBody []byte
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))

	if _, err := fetch[JSONValue](client, context.Background(), requestSpec{
		Method: http.MethodGet,
		Path:   "/api/v1/agents",
		Body:   map[string]any{"ignored": true},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seenBody) != 0 {
		t.Fatalf("expected no body on GET, got %s", seenBody)
	}
}

func TestRequestSurfacesStructuredAPIErrors(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"Agent missing"}}`))
	}))

	_, err := fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/agents/x"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an *APIError, got %v", err)
	}
	if apiErr.Status != 404 || apiErr.Code != "not_found" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestVoidOperationsAcceptEmptyResponses(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := client.requestVoid(context.Background(), requestSpec{
		Method: http.MethodDelete,
		Path:   "/api/v1/agents/x",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTypedOperationsRejectEmptyResponses(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	type agent struct {
		ID string `json:"id"`
	}
	// The operation promised a body; a silent zero value would be a lie.
	_, err := fetch[*agent](client, context.Background(), requestSpec{Path: "/api/v1/agents/x"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an *APIError, got %v", err)
	}
	if apiErr.Code != "empty_response" {
		t.Fatalf("expected empty_response, got %q", apiErr.Code)
	}
}

func TestUntypedOperationsDecodeEmptyResponsesAsNull(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	value, err := fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/kv/x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !value.IsNull() {
		t.Fatalf("expected JSON null, got %s", value)
	}
}

func TestRawOperationsReturnBytesAndMimeType(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("PK\x03\x04"))
	}))

	raw, err := client.requestRaw(context.Background(), requestSpec{Path: "/api/v1/files/x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.MimeType != "application/zip" {
		t.Fatalf("expected application/zip, got %q", raw.MimeType)
	}
	if string(raw.Content) != "PK\x03\x04" {
		t.Fatalf("unexpected content: %q", raw.Content)
	}
}

func TestUnauthorizedTriggersOneRefreshAndRetry(t *testing.T) {
	var attempts int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": r.Header.Get("Authorization")})
	}), WithAccessToken("stale"))

	var refreshes int32
	client.SetRefreshHandler(func(ctx context.Context) (string, error) {
		atomic.AddInt32(&refreshes, 1)
		return "fresh", nil
	})

	value, err := fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/agents"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("expected exactly one refresh, got %d", refreshes)
	}
	if got := value.Get("token").StringValue(); got != "Bearer fresh" {
		t.Fatalf("expected the retry to carry the fresh token, got %q", got)
	}
}

func TestConcurrentUnauthorizedRequestsShareOneRefresh(t *testing.T) {
	const callers = 4
	var refreshes int32
	unauthorized := make(chan struct{}, callers)
	release := make(chan struct{})

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			unauthorized <- struct{}{}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), WithAccessToken("stale"))

	client.SetRefreshHandler(func(ctx context.Context) (string, error) {
		atomic.AddInt32(&refreshes, 1)
		// Hold the refresh open so every caller piles up behind this one.
		<-release
		return "fresh", nil
	})

	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/agents"})
		}(i)
	}

	// Every caller has now been 401'd. Pause before releasing the single
	// in-flight refresh so they all reach the refresh gate first — otherwise
	// the first refresh could finish before the others arrive, and each would
	// legitimately start its own, which is not the behavior under test.
	for i := 0; i < callers; i++ {
		<-unauthorized
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	if refreshes != 1 {
		t.Fatalf("expected the refresh to be deduplicated, ran %d times", refreshes)
	}
}

func TestRefreshFailureSurfacesTheOriginalUnauthorized(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
	}))
	client.SetRefreshHandler(func(ctx context.Context) (string, error) {
		return "", ErrRefreshFailed
	})

	_, err := fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/agents"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("expected the original 401, got %d", apiErr.Status)
	}
}

func TestAuthEndpointsDoNotTriggerRefresh(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_credentials"}}`))
	}))
	refreshed := false
	client.SetRefreshHandler(func(ctx context.Context) (string, error) {
		refreshed = true
		return "fresh", nil
	})

	_, err := fetch[JSONValue](client, context.Background(), requestSpec{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/login",
	})
	if err == nil {
		t.Fatal("expected the login failure to surface")
	}
	if refreshed {
		t.Fatal("a failing login must not re-enter the refresh flow")
	}
}

func TestRefreshOnlyClientRejectsNonAuthPaths(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the request should never have left the client")
	}), withRefreshOnly())

	_, err := fetch[JSONValue](client, context.Background(), requestSpec{Path: "/api/v1/agents"})
	if err == nil {
		t.Fatal("expected the refresh-only guard to reject the request")
	}
}

func TestPathPrefixRewritesTheGeneratedAPIPrefix(t *testing.T) {
	var seenPath string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}), WithPathPrefix("/gateway/platform"))

	if _, err := fetch[JSONValue](client, context.Background(), requestSpec{
		Path: "/api/v1/agents",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenPath != "/gateway/platform/agents" {
		t.Fatalf("expected the prefix rewrite, got %q", seenPath)
	}
}

func TestWebsocketURLDerivesTheSocketEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://platform.archastro.ai":  "wss://platform.archastro.ai/socket/api/websocket",
		"http://127.0.0.1:4000":          "ws://127.0.0.1:4000/socket/api/websocket",
		"https://example.com/gateway/":   "wss://example.com/gateway/socket/api/websocket",
		"http://127.0.0.1:4000/?a=1#top": "ws://127.0.0.1:4000/socket/api/websocket",
	}
	for baseURL, want := range cases {
		if got := websocketURL(baseURL, "/socket/api/websocket"); got != want {
			t.Fatalf("websocketURL(%q): expected %q, got %q", baseURL, want, got)
		}
	}
}
