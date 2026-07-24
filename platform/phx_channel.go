// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: Phoenix Channel subscription for the generated Platform SDK.
// This file is hand-maintained, not generated.

package platform

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ChannelState is the lifecycle state of a channel subscription.
type ChannelState string

const (
	ChannelClosed  ChannelState = "closed"
	ChannelJoining ChannelState = "joining"
	ChannelJoined  ChannelState = "joined"
	ChannelLeaving ChannelState = "leaving"
	ChannelErrored ChannelState = "errored"
)

// ChannelReply is the envelope a Phoenix push replies with.
type ChannelReply struct {
	// Status is "ok" or "error".
	Status string
	// Response is the reply payload.
	Response JSONValue
}

// ChannelError is a failed channel operation: a rejected join, a timeout, or
// a transport failure. Match it with errors.As:
//
//	var chErr *platform.ChannelError
//	if errors.As(err, &chErr) { … }
type ChannelError struct {
	// Op is the operation that failed ("join", "push", "leave", "send").
	Op string
	// Topic is the channel topic involved.
	Topic string
	// Reason is a human-readable explanation.
	Reason string
	// Payload is the server's rejection payload, when there was one.
	Payload JSONValue
}

func (e *ChannelError) Error() string {
	return fmt.Sprintf("platform: channel %s on %q failed: %s", e.Op, e.Topic, e.Reason)
}

// pendingPushCap bounds the per-event replay buffer. A server that pushes on
// join can deliver before the caller has registered a handler; buffering a
// bounded number of payloads makes that race invisible without letting an
// unhandled event stream grow without limit.
const pendingPushCap = 32

type replyResult struct {
	payload JSONValue
	err     error
}

type channelHandler struct {
	id       int
	callback func(JSONValue)
}

// Channel is a single Phoenix channel subscription on one topic. Obtain one
// from Socket.Channel, or from a generated join helper.
type Channel struct {
	socket *Socket
	topic  string

	mu            sync.Mutex
	state         ChannelState
	joinRef       string
	pending       map[string]chan replyResult
	handlers      map[string][]channelHandler
	handlerSeq    int
	pendingPushes map[string][]JSONValue
}

func newChannel(socket *Socket, topic string) *Channel {
	return &Channel{
		socket:        socket,
		topic:         topic,
		state:         ChannelClosed,
		pending:       map[string]chan replyResult{},
		handlers:      map[string][]channelHandler{},
		pendingPushes: map[string][]JSONValue{},
	}
}

// Topic returns the channel's topic.
func (c *Channel) Topic() string { return c.topic }

// State returns the channel's lifecycle state.
func (c *Channel) State() ChannelState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// IsJoined reports whether the channel is currently joined.
func (c *Channel) IsJoined() bool { return c.State() == ChannelJoined }

// Join joins the channel and returns the server's join response payload.
// A rejected join, a timeout, or a dead socket all surface as *ChannelError.
func (c *Channel) Join(ctx context.Context, payload any) (JSONValue, error) {
	ref := c.socket.makeRef()

	c.mu.Lock()
	if c.state == ChannelJoined {
		c.mu.Unlock()
		return JSONValue{}, &ChannelError{
			Op:     "join",
			Topic:  c.topic,
			Reason: "channel is already joined; leave it first",
		}
	}
	c.state = ChannelJoining
	c.joinRef = ref
	c.mu.Unlock()

	envelope, err := c.sendAndAwaitReply(ctx, ref, ref, "phx_join", payload)
	if err != nil {
		c.setState(ChannelErrored)
		return JSONValue{}, err
	}

	if envelope.Get("status").StringValue() == "ok" {
		c.setState(ChannelJoined)
		return envelope.Get("response"), nil
	}
	c.setState(ChannelErrored)
	return JSONValue{}, &ChannelError{
		Op:      "join",
		Topic:   c.topic,
		Reason:  "server rejected the join",
		Payload: envelope.Get("response"),
	}
}

// Leave leaves the channel. A leave that times out is not an error — the
// server tears the subscription down regardless.
func (c *Channel) Leave(ctx context.Context) error {
	if c.State() == ChannelClosed {
		return nil
	}
	c.mu.Lock()
	c.state = ChannelLeaving
	joinRef := c.joinRef
	c.mu.Unlock()

	ref := c.socket.makeRef()
	_, err := c.sendAndAwaitReply(ctx, joinRef, ref, "phx_leave", map[string]any{})
	c.finishLeave()

	var chErr *ChannelError
	if err != nil && asChannelError(err, &chErr) && chErr.Reason == timeoutReason {
		return nil
	}
	return err
}

// Push sends an event and waits for the server's reply envelope.
func (c *Channel) Push(ctx context.Context, event string, payload any) (*ChannelReply, error) {
	c.mu.Lock()
	joinRef := c.joinRef
	c.mu.Unlock()

	ref := c.socket.makeRef()
	envelope, err := c.sendAndAwaitReply(ctx, joinRef, ref, event, payload)
	if err != nil {
		return nil, err
	}
	status := envelope.Get("status").StringValue()
	if status == "" {
		status = "error"
	}
	return &ChannelReply{Status: status, Response: envelope.Get("response")}, nil
}

// On registers a callback for a channel event and returns an unsubscribe
// function. Pushes buffered before the first handler was registered are
// replayed immediately.
func (c *Channel) On(event string, callback func(JSONValue)) func() {
	c.mu.Lock()
	c.handlerSeq++
	id := c.handlerSeq
	c.handlers[event] = append(c.handlers[event], channelHandler{id: id, callback: callback})
	replay := c.pendingPushes[event]
	delete(c.pendingPushes, event)
	c.mu.Unlock()

	for _, payload := range replay {
		callback(payload)
	}

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		remaining := c.handlers[event][:0]
		for _, handler := range c.handlers[event] {
			if handler.id != id {
				remaining = append(remaining, handler)
			}
		}
		c.handlers[event] = remaining
	}
}

const timeoutReason = "timed out waiting for a reply"

func (c *Channel) sendAndAwaitReply(
	ctx context.Context,
	joinRef string,
	ref string,
	event string,
	payload any,
) (JSONValue, error) {
	replies := make(chan replyResult, 1)
	c.mu.Lock()
	c.pending[ref] = replies
	c.mu.Unlock()

	if err := c.socket.send(joinRef, ref, c.topic, event, payload); err != nil {
		c.discardPending(ref)
		return JSONValue{}, wrapChannelError(err, event, c.topic)
	}

	timer := time.NewTimer(c.socket.timeout)
	defer timer.Stop()

	select {
	case result := <-replies:
		if result.err != nil {
			return JSONValue{}, result.err
		}
		return result.payload, nil
	case <-timer.C:
		c.discardPending(ref)
		return JSONValue{}, &ChannelError{Op: event, Topic: c.topic, Reason: timeoutReason}
	case <-ctx.Done():
		c.discardPending(ref)
		return JSONValue{}, ctx.Err()
	}
}

func (c *Channel) discardPending(ref string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, ref)
}

func (c *Channel) setState(state ChannelState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

func (c *Channel) finishLeave() {
	c.mu.Lock()
	c.state = ChannelClosed
	c.joinRef = ""
	c.mu.Unlock()
	c.socket.removeChannel(c.topic)
}

// handleMessage dispatches one inbound frame addressed to this channel.
func (c *Channel) handleMessage(joinRef string, ref string, event string, payload JSONValue) {
	c.mu.Lock()
	currentJoinRef := c.joinRef
	c.mu.Unlock()
	// Ignore messages left over from a previous join of the same topic.
	if joinRef != "" && currentJoinRef != "" && joinRef != currentJoinRef {
		return
	}

	switch event {
	case "phx_reply":
		if ref == "" {
			return
		}
		c.mu.Lock()
		replies := c.pending[ref]
		delete(c.pending, ref)
		c.mu.Unlock()
		if replies != nil {
			replies <- replyResult{payload: payload}
		}
	case "phx_close":
		c.setState(ChannelClosed)
		c.trigger("phx_close", JSONOf(map[string]any{}))
	case "phx_error":
		c.setState(ChannelErrored)
		c.trigger("phx_error", payload)
	default:
		c.deliver(event, payload)
	}
}

func (c *Channel) deliver(event string, payload JSONValue) {
	c.mu.Lock()
	handlers := c.handlers[event]
	if len(handlers) == 0 {
		buffer := c.pendingPushes[event]
		if len(buffer) >= pendingPushCap {
			buffer = buffer[1:]
		}
		c.pendingPushes[event] = append(buffer, payload)
		c.mu.Unlock()
		return
	}
	snapshot := make([]channelHandler, len(handlers))
	copy(snapshot, handlers)
	c.mu.Unlock()

	for _, handler := range snapshot {
		handler.callback(payload)
	}
}

func (c *Channel) trigger(event string, payload JSONValue) {
	c.mu.Lock()
	snapshot := make([]channelHandler, len(c.handlers[event]))
	copy(snapshot, c.handlers[event])
	c.mu.Unlock()
	for _, handler := range snapshot {
		handler.callback(payload)
	}
}

// handleSocketDisconnect fails every in-flight reply so callers see the
// disconnect instead of waiting out their timeout.
func (c *Channel) handleSocketDisconnect() {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]chan replyResult{}
	c.state = ChannelErrored
	c.mu.Unlock()

	for _, replies := range pending {
		replies <- replyResult{
			err: &ChannelError{Op: "reply", Topic: c.topic, Reason: "socket disconnected"},
		}
	}
}

func wrapChannelError(err error, op string, topic string) error {
	var chErr *ChannelError
	if asChannelError(err, &chErr) {
		return chErr
	}
	return &ChannelError{Op: op, Topic: topic, Reason: err.Error()}
}

func asChannelError(err error, target **ChannelError) bool {
	if chErr, ok := err.(*ChannelError); ok {
		*target = chErr
		return true
	}
	return false
}
