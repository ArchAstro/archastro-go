// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Test support: client for the @archastro/channel-harness control API.
// This file is hand-maintained, not generated — the Go counterpart of
// archastro.phx_channel.HarnessServiceClient (Python), the Swift suite's
// HarnessServiceClient, and the TS client in @archastro/channel-harness.
//
// The harness service exposes two surfaces:
//  1. A WebSocket endpoint carrying the real Phoenix channel protocol.
//  2. An HTTP control endpoint for scenario registration, observations, and
//     reset. The same listener serves SSE routes for the stream tests.
//
// There is no in-process shortcut — these tests drive the SAME service the
// TypeScript, Python, and Swift suites drive.

package contracttests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArchAstro/archastro-go/platform"
)

type harnessEndpoints struct {
	WsURL      string `json:"wsUrl"`
	ControlURL string `json:"controlUrl"`
}

var (
	harnessOnce      sync.Once
	harnessErr       error
	harnessResolved  harnessEndpoints
	harnessExclusive sync.Mutex
)

// harnessClient drives one harness-service instance for the duration of a
// single test: scenarios and observations are global server state, so tests
// take the harness one at a time and reset it on entry.
type harnessClient struct {
	endpoints harnessEndpoints
	http      *http.Client
	sockets   []*platform.Socket
}

// withHarness runs body against an exclusive, freshly reset harness.
func withHarness(t *testing.T, body func(h *harnessClient)) {
	t.Helper()
	harnessExclusive.Lock()
	defer harnessExclusive.Unlock()

	endpoints := ensureHarness(t)
	h := &harnessClient{
		endpoints: endpoints,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
	defer h.close()
	h.reset(t)
	body(h)
}

// withHarnessSocket is withHarness plus a connected Phoenix socket.
func withHarnessSocket(t *testing.T, body func(h *harnessClient, socket *platform.Socket)) {
	t.Helper()
	withHarness(t, func(h *harnessClient) {
		body(h, h.openSocket(t))
	})
}

// sdkClient builds an SDK client pointed at the harness HTTP listener, for
// the SSE stream contract tests.
func (h *harnessClient) sdkClient() *platform.Client {
	return platform.NewClient(
		platform.WithBaseURL(h.endpoints.ControlURL),
		platform.WithAccessToken("test-token"),
		platform.WithDefaultHeaders(map[string]string{"x-archastro-api-key": "pk_test-key"}),
	)
}

// openSocket connects a socket to the harness WebSocket endpoint.
// Auto-reconnect stays off so a dropped test connection surfaces
// immediately instead of silently retrying.
func (h *harnessClient) openSocket(t *testing.T) *platform.Socket {
	t.Helper()
	socket := platform.NewSocket(h.endpoints.WsURL, platform.WithSocketAutoReconnect(false))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := socket.Connect(ctx); err != nil {
		t.Fatalf("connecting to the harness socket: %v", err)
	}
	h.sockets = append(h.sockets, socket)
	return socket
}

func (h *harnessClient) close() {
	for _, socket := range h.sockets {
		_ = socket.Close()
	}
	h.sockets = nil
}

// reset clears every scenario, observation, and handler error on the server.
func (h *harnessClient) reset(t *testing.T) {
	t.Helper()
	h.post(t, "/reset", nil, http.StatusOK, http.StatusNoContent, http.StatusCreated)
}

// registerScenario registers a per-topic channel scenario (see the harness
// README for the ScenarioRequest JSON shape).
func (h *harnessClient) registerScenario(t *testing.T, scenario map[string]any) {
	t.Helper()
	h.post(t, "/scenarios", scenario, http.StatusCreated)
}

// registerStreamScenario registers a per-route SSE stream scenario.
func (h *harnessClient) registerStreamScenario(t *testing.T, scenario map[string]any) {
	t.Helper()
	h.post(t, "/stream-scenarios", scenario, http.StatusCreated)
}

// observations returns the inbound frames the server validated, filtered by
// topic and event.
func (h *harnessClient) observations(t *testing.T, topic string, event string) []platform.JSONValue {
	t.Helper()
	target, err := url.Parse(h.endpoints.ControlURL + "/observations")
	if err != nil {
		t.Fatalf("building the observations URL: %v", err)
	}
	query := target.Query()
	if topic != "" {
		query.Set("topic", topic)
	}
	if event != "" {
		query.Set("event", event)
	}
	target.RawQuery = query.Encode()

	resp, err := h.http.Get(target.String())
	if err != nil {
		t.Fatalf("fetching observations: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading observations: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("observations: HTTP %d %s", resp.StatusCode, data)
	}
	var parsed platform.JSONValue
	if err := parsed.UnmarshalJSON(data); err != nil {
		t.Fatalf("decoding observations: %v", err)
	}
	return parsed.Array()
}

func (h *harnessClient) post(t *testing.T, path string, body any, expected ...int) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding %s body: %v", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(http.MethodPost, h.endpoints.ControlURL+path, reader)
	if err != nil {
		t.Fatalf("building POST %s: %v", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	for _, code := range expected {
		if resp.StatusCode == code {
			return
		}
	}
	t.Fatalf("POST %s: expected %v, got HTTP %d %s", path, expected, resp.StatusCode, data)
}

// ─── Harness process lifecycle ──────────────────────────────────

// ensureHarness boots the channel-harness service once per test binary and
// returns the URLs it reported on startup.
func ensureHarness(t *testing.T) harnessEndpoints {
	t.Helper()
	harnessOnce.Do(func() { harnessResolved, harnessErr = startHarness() })
	if harnessErr != nil {
		t.Fatalf("channel harness unavailable: %v", harnessErr)
	}
	return harnessResolved
}

func startHarness() (harnessEndpoints, error) {
	bin := harnessBin()
	if _, err := os.Stat(bin); err != nil {
		return harnessEndpoints{}, fmt.Errorf(
			"channel-harness bin not found at %s — set ARCHASTRO_HARNESS_BIN or run 'npm ci' in the repo root",
			bin,
		)
	}

	cmd := exec.Command("node", bin, specPath()) //nolint:gosec // path is repo-local or operator-supplied
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return harnessEndpoints{}, fmt.Errorf("wiring harness stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return harnessEndpoints{}, fmt.Errorf("starting the channel harness: %w", err)
	}
	track(cmd)

	// The service prints exactly one JSON line with its URLs, then serves.
	lines := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			close(lines)
			return
		}
		lines <- strings.TrimSpace(line)
	}()

	select {
	case line, ok := <-lines:
		if !ok {
			return harnessEndpoints{}, fmt.Errorf("the channel harness exited before reporting its URLs")
		}
		var endpoints harnessEndpoints
		if err := json.Unmarshal([]byte(line), &endpoints); err != nil {
			return harnessEndpoints{}, fmt.Errorf("unparseable harness URL line %q: %w", line, err)
		}
		if endpoints.WsURL == "" || endpoints.ControlURL == "" {
			return harnessEndpoints{}, fmt.Errorf("harness reported incomplete URLs: %q", line)
		}
		return endpoints, nil
	case <-time.After(30 * time.Second):
		return harnessEndpoints{}, fmt.Errorf("the channel harness did not report its URLs within 30s")
	}
}
