// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for SSE framing and stream lifecycle.

package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sseServer(t *testing.T, body string, status int) *HTTPClient {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newHTTPClient(newClientConfig(server.URL))
}

func collect(t *testing.T, stream *SSEStream) []SSEEvent {
	t.Helper()
	var events []SSEEvent
	for stream.Next() {
		events = append(events, stream.Event())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	return events
}

func TestStreamSSEYieldsEventsInOrder(t *testing.T) {
	client := sseServer(t, "event: message_delta\ndata: {\"text\":\"hi\"}\n\n"+
		"event: done\ndata: {}\n\n", http.StatusOK)

	stream, err := client.streamSSE(context.Background(), requestSpec{
		Method: http.MethodPost,
		Path:   "/api/v1/ai/chat/completions/stream",
		Body:   map[string]any{"messages": []any{}},
	})
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	defer stream.Close()

	events := collect(t, stream)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Event != "message_delta" {
		t.Fatalf("expected message_delta, got %q", events[0].Event)
	}
	if got := events[0].Data.Get("text").StringValue(); got != "hi" {
		t.Fatalf("expected the parsed payload, got %q", got)
	}
	if events[1].Event != "done" {
		t.Fatalf("expected done, got %q", events[1].Event)
	}
}

func TestStreamSSEDefaultsTheEventNameAndKeepsRawData(t *testing.T) {
	client := sseServer(t, "data: not json\n\n", http.StatusOK)

	stream, err := client.streamSSE(context.Background(), requestSpec{Path: "/stream"})
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	defer stream.Close()

	events := collect(t, stream)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Event != "message" {
		t.Fatalf("expected the default event name, got %q", events[0].Event)
	}
	if got := events[0].Data.StringValue(); got != "not json" {
		t.Fatalf("expected the raw payload, got %q", got)
	}
}

func TestStreamSSEJoinsMultiLineDataAndDispatchesAtEOF(t *testing.T) {
	// No trailing blank line: the server closed mid-frame, and the buffered
	// event still has to reach the caller.
	client := sseServer(t, "event: chunk\ndata: a\ndata: b\n", http.StatusOK)

	stream, err := client.streamSSE(context.Background(), requestSpec{Path: "/stream"})
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	defer stream.Close()

	events := collect(t, stream)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if got := events[0].Data.StringValue(); got != "a\nb" {
		t.Fatalf("expected the joined data lines, got %q", got)
	}
}

func TestStreamSSERejectsNon2xxBeforeYieldingAnything(t *testing.T) {
	client := sseServer(t, `{"error":{"code":"plan_not_entitled"}}`, http.StatusPaymentRequired)

	_, err := client.streamSSE(context.Background(), requestSpec{Path: "/stream"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", apiErr.Status)
	}
	if apiErr.Code != "plan_not_entitled" {
		t.Fatalf("expected plan_not_entitled, got %q", apiErr.Code)
	}
}

func TestStreamSSECloseIsIdempotent(t *testing.T) {
	client := sseServer(t, "event: a\ndata: {}\n\n", http.StatusOK)

	stream, err := client.streamSSE(context.Background(), requestSpec{Path: "/stream"})
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if stream.Next() {
		t.Fatal("a closed stream must not yield further events")
	}
}
