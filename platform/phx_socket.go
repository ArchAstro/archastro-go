// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: Phoenix Channels socket for the generated Platform SDK.
// This file is hand-maintained, not generated. Port of the Python SDK's
// archastro/phx_channel package.
//
// Wire protocol (vsn 2.0.0): JSON arrays [join_ref, ref, topic, event, payload]

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Socket defaults, matching the other ArchAstro SDKs.
const (
	DefaultHeartbeatInterval = 30 * time.Second
	DefaultChannelTimeout    = 10 * time.Second
)

// SocketOption customizes a Socket at construction time.
type SocketOption func(*Socket)

// WithSocketParams adds connect parameters to the socket query string.
func WithSocketParams(params map[string]string) SocketOption {
	return func(s *Socket) {
		if s.params == nil {
			s.params = map[string]string{}
		}
		for key, value := range params {
			s.params[key] = value
		}
	}
}

// WithSocketHeartbeat sets the heartbeat interval.
func WithSocketHeartbeat(interval time.Duration) SocketOption {
	return func(s *Socket) { s.heartbeat = interval }
}

// WithSocketTimeout sets the default timeout for joins, pushes, and leaves.
func WithSocketTimeout(timeout time.Duration) SocketOption {
	return func(s *Socket) { s.timeout = timeout }
}

// WithSocketAutoReconnect toggles reconnection. It is off by default: the
// SDK surfaces disconnects to callers rather than silently retrying.
func WithSocketAutoReconnect(enabled bool) SocketOption {
	return func(s *Socket) { s.autoReconnect = enabled }
}

// Socket is a WebSocket connection to a Phoenix server: it owns the
// heartbeat, the message ref counter, and the per-topic channels.
type Socket struct {
	rawURL        string
	params        map[string]string
	heartbeat     time.Duration
	timeout       time.Duration
	autoReconnect bool

	writeMu sync.Mutex

	mu                  sync.Mutex
	conn                *websocket.Conn
	connected           bool
	refCounter          int
	channels            map[string]*Channel
	pendingHeartbeatRef string
	closing             chan struct{}
}

// NewSocket builds an unconnected socket. Call Connect before using it.
func NewSocket(rawURL string, opts ...SocketOption) *Socket {
	socket := &Socket{
		rawURL:    rawURL,
		params:    map[string]string{},
		heartbeat: DefaultHeartbeatInterval,
		timeout:   DefaultChannelTimeout,
		channels:  map[string]*Channel{},
	}
	for _, opt := range opts {
		opt(socket)
	}
	return socket
}

// Connect dials the server and starts the receive and heartbeat loops.
func (s *Socket) Connect(ctx context.Context) error {
	target, err := s.connectURL()
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: s.timeout}
	conn, _, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		return fmt.Errorf("platform: connecting to %s: %w", s.rawURL, err)
	}

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	s.closing = make(chan struct{})
	closing := s.closing
	s.mu.Unlock()

	go s.receiveLoop(conn, closing)
	go s.heartbeatLoop(closing)
	return nil
}

// IsConnected reports whether the socket currently has a live connection.
func (s *Socket) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Close disconnects the socket and stops its background loops. It is safe
// to call on an already-closed socket.
func (s *Socket) Close() error {
	s.mu.Lock()
	conn := s.conn
	closing := s.closing
	s.conn = nil
	s.connected = false
	s.closing = nil
	s.mu.Unlock()

	if closing != nil {
		close(closing)
	}
	if conn == nil {
		return nil
	}
	// Best-effort courtesy close frame, then tear the connection down.
	s.writeMu.Lock()
	_ = conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	s.writeMu.Unlock()
	return conn.Close()
}

// Channel returns the channel for a topic, creating it on first use.
func (s *Socket) Channel(topic string) *Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.channels[topic]; ok {
		return existing
	}
	channel := newChannel(s, topic)
	s.channels[topic] = channel
	return channel
}

func (s *Socket) removeChannel(topic string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channels, topic)
}

func (s *Socket) makeRef() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refCounter++
	return strconv.Itoa(s.refCounter)
}

func (s *Socket) connectURL() (string, error) {
	parsed, err := url.Parse(s.rawURL)
	if err != nil {
		return "", fmt.Errorf("platform: invalid socket URL %q: %w", s.rawURL, err)
	}
	query := parsed.Query()
	for key, value := range s.params {
		query.Set(key, value)
	}
	query.Set("vsn", "2.0.0")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// send writes one Phoenix frame.
func (s *Socket) send(joinRef string, ref string, topic string, event string, payload any) error {
	s.mu.Lock()
	conn := s.conn
	connected := s.connected
	s.mu.Unlock()
	if conn == nil || !connected {
		return &ChannelError{Op: "send", Topic: topic, Reason: "socket is not connected"}
	}

	frame := []any{nullable(joinRef), nullable(ref), topic, event, orEmptyObject(payload)}
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("platform: encoding %s frame for %s: %w", event, topic, err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return &ChannelError{Op: "send", Topic: topic, Reason: err.Error()}
	}
	return nil
}

func nullable(ref string) any {
	if ref == "" {
		return nil
	}
	return ref
}

func orEmptyObject(payload any) any {
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func (s *Socket) receiveLoop(conn *websocket.Conn, closing chan struct{}) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-closing:
				// Expected: Close tore the connection down.
			default:
				s.handleDisconnect()
			}
			return
		}

		var frame []JSONValue
		if err := json.Unmarshal(data, &frame); err != nil || len(frame) != 5 {
			continue
		}
		s.dispatch(
			frame[0].StringValue(),
			frame[1].StringValue(),
			frame[2].StringValue(),
			frame[3].StringValue(),
			frame[4],
		)
	}
}

func (s *Socket) dispatch(joinRef, ref, topic, event string, payload JSONValue) {
	s.mu.Lock()
	isHeartbeat := ref != "" && ref == s.pendingHeartbeatRef
	if isHeartbeat {
		s.pendingHeartbeatRef = ""
	}
	channel := s.channels[topic]
	s.mu.Unlock()

	if isHeartbeat || channel == nil {
		return
	}
	channel.handleMessage(joinRef, ref, event, payload)
}

func (s *Socket) handleDisconnect() {
	s.mu.Lock()
	s.connected = false
	channels := make([]*Channel, 0, len(s.channels))
	for _, channel := range s.channels {
		channels = append(channels, channel)
	}
	s.mu.Unlock()

	for _, channel := range channels {
		channel.handleSocketDisconnect()
	}
}

func (s *Socket) heartbeatLoop(closing chan struct{}) {
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-closing:
			return
		case <-ticker.C:
			if !s.IsConnected() {
				return
			}
			s.mu.Lock()
			stale := s.pendingHeartbeatRef != ""
			s.mu.Unlock()
			if stale {
				// The previous heartbeat was never acknowledged — the
				// connection is dead even though the socket looks open.
				_ = s.Close()
				return
			}
			ref := s.makeRef()
			s.mu.Lock()
			s.pendingHeartbeatRef = ref
			s.mu.Unlock()
			if err := s.send("", ref, "phoenix", "heartbeat", map[string]any{}); err != nil {
				return
			}
		}
	}
}
