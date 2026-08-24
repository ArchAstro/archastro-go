// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Test support: Prism lifecycle, SDK client factories, and the assertion
// helpers the generated contract tests call. This file is hand-maintained,
// not generated — the Go analogue of the Swift suite's ContractSupport and
// the Python suite's conftest.py.

package contracttests

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ArchAstro/archastro-go/platform"
)

// TestMain runs the suite and then reaps every subprocess it started, so a
// failed run never leaves a Prism or harness listener behind.
func TestMain(m *testing.M) {
	code := m.Run()
	killTrackedProcesses()
	os.Exit(code)
}

// ─── Environment ────────────────────────────────────────────────

// repoRoot is the module root, derived from this file's compile-time path.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(file))
}

func envOr(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func prismPort() string { return envOr("PRISM_PORT", "4040") }

func prismURL() string { return "http://127.0.0.1:" + prismPort() }

func specPath() string {
	return envOr("OPENAPI_SPEC_PATH", filepath.Join(repoRoot(), "specs", "platform-openapi.json"))
}

func prismBin() string {
	return envOr("PRISM_BIN", filepath.Join(repoRoot(), "node_modules", ".bin", "prism"))
}

func harnessBin() string {
	return envOr("ARCHASTRO_HARNESS_BIN", filepath.Join(
		repoRoot(), "node_modules", "@archastro", "channel-harness", "dist", "bin.js",
	))
}

// requireChannelTests skips a test unless the channel/stream suites are
// opted into, mirroring the TypeScript, Python, and Swift suites.
func requireChannelTests(t *testing.T) {
	t.Helper()
	switch envOr("ARCHASTRO_RUN_CHANNEL_CONTRACT_TESTS", "") {
	case "1", "true", "TRUE", "yes", "YES":
		return
	default:
		t.Skip("set ARCHASTRO_RUN_CHANNEL_CONTRACT_TESTS=1 to run the channel and stream contract tests")
	}
}

// ─── SDK clients ────────────────────────────────────────────────

func restClient(t *testing.T) *platform.Client {
	t.Helper()
	ensurePrism(t)
	return platform.NewClient(
		platform.WithBaseURL(prismURL()),
		platform.WithAccessToken("test-token"),
		platform.WithDefaultHeaders(map[string]string{"x-archastro-api-key": "pk_test-key"}),
	)
}

// errorClient asks Prism for a specific documented status code via the
// Prefer header, so error-path assertions exercise the real response shape.
func errorClient(t *testing.T, code int) *platform.Client {
	t.Helper()
	ensurePrism(t)
	return platform.NewClient(
		platform.WithBaseURL(prismURL()),
		platform.WithAccessToken("test-token"),
		platform.WithDefaultHeaders(map[string]string{
			"x-archastro-api-key": "pk_test-key",
			"Prefer":              fmt.Sprintf("code=%d", code),
		}),
	)
}

// ─── Assertions ─────────────────────────────────────────────────

// use is the sink for generated assertions that only need a value to exist.
func use(...any) {}

// isoTime parses a timestamp literal in generated test arguments.
func isoTime(value string) platform.Time {
	return platform.MustParseTime(value)
}

func requireAPIError(t *testing.T, err error, status int) {
	t.Helper()
	var apiErr *platform.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected a *platform.APIError, got %v", err)
	}
	if apiErr.Status != status {
		t.Fatalf("expected status %d, got %d (%s)", status, apiErr.Status, apiErr.Message)
	}
}

func requireChannelError(t *testing.T, err error) {
	t.Helper()
	var chErr *platform.ChannelError
	if !errors.As(err, &chErr) {
		t.Fatalf("expected a *platform.ChannelError, got %v", err)
	}
}

func requireStrings(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func requireJSON(t *testing.T, got platform.JSONValue, want platform.JSONValue) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

// awaitFirst waits for the first value delivered to a callback-fed channel.
func awaitFirst[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a channel payload")
		var zero T
		return zero
	}
}

// ─── Subprocess lifecycle ───────────────────────────────────────

var (
	trackedMu        sync.Mutex
	trackedProcesses []*exec.Cmd

	prismOnce sync.Once
	prismErr  error
)

func track(cmd *exec.Cmd) {
	trackedMu.Lock()
	defer trackedMu.Unlock()
	trackedProcesses = append(trackedProcesses, cmd)
}

func killTrackedProcesses() {
	trackedMu.Lock()
	processes := trackedProcesses
	trackedProcesses = nil
	trackedMu.Unlock()

	for _, cmd := range processes {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
}

// ensurePrism boots the Prism mock once per test binary. A Prism already
// listening on the port serves the same spec, so it is reused as-is.
func ensurePrism(t *testing.T) {
	t.Helper()
	prismOnce.Do(func() { prismErr = startPrism() })
	if prismErr != nil {
		t.Fatalf("prism unavailable: %v", prismErr)
	}
}

func startPrism() error {
	if prismAnswers() {
		return nil
	}
	bin := prismBin()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("prism bin not found at %s — set PRISM_BIN or run 'npm ci' in the repo root", bin)
	}

	cmd := exec.Command(bin, prismArgs()...) //nolint:gosec // path is repo-local or operator-supplied
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting prism: %w", err)
	}
	track(cmd)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if prismAnswers() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("prism did not start on port %s within 60s", prismPort())
}

func prismArgs() []string {
	// Static responses keep shape-contract tests deterministic. Prism's dynamic
	// mode delegates nested response examples to json-schema-faker, which can
	// crash while expanding valid array schemas such as Activity Feed entries.
	return []string{
		"mock", specPath(),
		"--port", prismPort(),
		"--host", "127.0.0.1",
	}
}

func prismAnswers() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(prismURL() + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true
}
