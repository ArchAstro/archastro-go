// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for channel dispatch logic that needs no socket. The full
// Phoenix protocol is exercised end to end by the generated channel contract
// tests against the @archastro/channel-harness service.

package platform

import (
	"errors"
	"testing"
)

func TestChannelReplaysPushesBufferedBeforeTheFirstHandler(t *testing.T) {
	// A server that pushes on join can beat the caller to registering a
	// handler; buffering makes that race invisible.
	channel := newChannel(nil, "api:object:test-id")
	channel.handleMessage("", "", "field_updated", JSONOf(map[string]any{"n": 1}))
	channel.handleMessage("", "", "field_updated", JSONOf(map[string]any{"n": 2}))

	var seen []int
	channel.On("field_updated", func(payload JSONValue) {
		seen = append(seen, payload.Get("n").IntValue())
	})

	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("expected the buffered pushes replayed in order, got %v", seen)
	}
}

func TestChannelUnsubscribeStopsDelivery(t *testing.T) {
	channel := newChannel(nil, "api:object:test-id")
	count := 0
	unsubscribe := channel.On("field_updated", func(JSONValue) { count++ })

	channel.handleMessage("", "", "field_updated", JSONValue{})
	unsubscribe()
	channel.handleMessage("", "", "field_updated", JSONValue{})

	if count != 1 {
		t.Fatalf("expected exactly one delivery, got %d", count)
	}
}

func TestChannelDropsMessagesFromAStaleJoin(t *testing.T) {
	channel := newChannel(nil, "api:chat:team:t:thread:x")
	channel.joinRef = "7"

	delivered := 0
	channel.On("message_added", func(JSONValue) { delivered++ })

	channel.handleMessage("3", "", "message_added", JSONValue{})
	if delivered != 0 {
		t.Fatal("a message from a previous join must not reach handlers")
	}

	channel.handleMessage("7", "", "message_added", JSONValue{})
	if delivered != 1 {
		t.Fatalf("expected the current join's message, got %d deliveries", delivered)
	}
}

func TestChannelPendingPushBufferIsBounded(t *testing.T) {
	channel := newChannel(nil, "api:object:test-id")
	for i := 0; i < pendingPushCap+10; i++ {
		channel.handleMessage("", "", "tick", JSONOf(map[string]any{"i": i}))
	}

	var seen []int
	channel.On("tick", func(payload JSONValue) {
		seen = append(seen, payload.Get("i").IntValue())
	})

	if len(seen) != pendingPushCap {
		t.Fatalf("expected the buffer capped at %d, got %d", pendingPushCap, len(seen))
	}
	// The cap drops the oldest, so the newest push always survives.
	if seen[len(seen)-1] != pendingPushCap+9 {
		t.Fatalf("expected the newest push retained, got %d", seen[len(seen)-1])
	}
}

func TestChannelDisconnectFailsInFlightReplies(t *testing.T) {
	channel := newChannel(nil, "api:object:test-id")
	replies := make(chan replyResult, 1)
	channel.pending["1"] = replies

	channel.handleSocketDisconnect()

	result := <-replies
	var chErr *ChannelError
	if !errors.As(result.err, &chErr) {
		t.Fatalf("expected a *ChannelError, got %v", result.err)
	}
	if channel.State() != ChannelErrored {
		t.Fatalf("expected the channel to be errored, got %s", channel.State())
	}
}

func TestChannelStateTransitionsOnServerLifecycleEvents(t *testing.T) {
	channel := newChannel(nil, "api:object:test-id")
	channel.setState(ChannelJoined)

	channel.handleMessage("", "", "phx_error", JSONValue{})
	if channel.State() != ChannelErrored {
		t.Fatalf("expected errored after phx_error, got %s", channel.State())
	}

	channel.handleMessage("", "", "phx_close", JSONValue{})
	if channel.State() != ChannelClosed {
		t.Fatalf("expected closed after phx_close, got %s", channel.State())
	}
}
