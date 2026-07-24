// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: Server-Sent Events streaming for the generated Platform SDK.
// This file is hand-maintained, not generated.

package platform

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
)

// SSEEvent is one parsed Server-Sent Event.
type SSEEvent struct {
	// Event is the SSE event name, or "message" when the server omitted one.
	Event string
	// Data is the event payload, parsed as JSON when it is valid JSON and
	// kept as a JSON string otherwise.
	Data JSONValue
}

// SSEStream is an open Server-Sent Events response. Iterate it with Next,
// read each event with Event, and check Err when the loop ends:
//
//	stream, err := client.AI().Chat().Completions().Stream(ctx, input)
//	if err != nil { … }
//	defer stream.Close()
//	for stream.Next() {
//	    fmt.Println(stream.Event().Event, stream.Event().Data)
//	}
//	if err := stream.Err(); err != nil { … }
//
// A non-2xx status is reported by the method that opened the stream, not
// here — the stream only exists once the server accepted the request.
type SSEStream struct {
	body    io.ReadCloser
	reader  *bufio.Reader
	current SSEEvent
	err     error
	done    bool
}

// streamSSE opens a Server-Sent Events stream. Any method and body is
// allowed: the platform's streaming endpoints are POSTs.
func (c *HTTPClient) streamSSE(ctx context.Context, spec requestSpec) (*SSEStream, error) {
	if err := c.checkRefreshOnly(spec.Path); err != nil {
		return nil, err
	}
	body, err := encodeBody(spec)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, spec, body, "text/event-stream")
	if err != nil {
		return nil, err
	}
	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseAPIError(data, resp.StatusCode)
	}
	return newSSEStream(resp), nil
}

func newSSEStream(resp *http.Response) *SSEStream {
	return &SSEStream{body: resp.Body, reader: bufio.NewReader(resp.Body)}
}

// Next advances to the next event, reporting false at end of stream or on
// error. Check Err after the loop to tell the two apart.
func (s *SSEStream) Next() bool {
	if s.done {
		return false
	}
	var name string
	var data []string

	for {
		line, err := s.reader.ReadString('\n')
		atEOF := err == io.EOF
		if err != nil && !atEOF {
			s.err = err
			s.done = true
			return false
		}

		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			// A blank line dispatches the buffered event. At EOF an empty
			// tail line is just the end of the body, not a dispatch.
			if event, ok := buildSSEEvent(name, data); ok {
				s.current = event
				s.done = atEOF
				return true
			}
			if atEOF {
				s.done = true
				return false
			}
			name, data = "", nil
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}

		if atEOF {
			// The server closed mid-event: dispatch whatever was buffered.
			if event, ok := buildSSEEvent(name, data); ok {
				s.current = event
				s.done = true
				return true
			}
			s.done = true
			return false
		}
	}
}

// Event returns the event Next just advanced to.
func (s *SSEStream) Event() SSEEvent { return s.current }

// Err returns the error that ended the stream, if any.
func (s *SSEStream) Err() error { return s.err }

// Close releases the underlying response body. It is safe to call more than
// once, and safe to defer immediately after opening the stream.
func (s *SSEStream) Close() error {
	s.done = true
	if s.body == nil {
		return nil
	}
	body := s.body
	s.body = nil
	return body.Close()
}

func buildSSEEvent(name string, data []string) (SSEEvent, bool) {
	if name == "" && len(data) == 0 {
		return SSEEvent{}, false
	}
	raw := strings.Join(data, "\n")
	var payload JSONValue
	if err := payload.UnmarshalJSON([]byte(raw)); err != nil {
		payload = JSONOf(raw)
	}
	if name == "" {
		name = "message"
	}
	return SSEEvent{Event: name, Data: payload}, true
}
