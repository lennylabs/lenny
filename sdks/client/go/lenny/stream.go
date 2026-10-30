// SPDX-License-Identifier: MIT

package lenny

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// StreamEvent is one decoded frame from the §15.1
// GET /v1/sessions/{id}/events Server-Sent Events stream.
type StreamEvent struct {
	// Seq is the monotonic per-session sequence the gateway assigns.
	// It is the cursor a reconnect resumes from: the SDK sends the
	// last delivered Seq as the Last-Event-ID header.
	Seq uint64

	// Type is the SSE event type (the `event:` field), for example
	// message_delivered, response, or state_changed.
	Type string

	// Data is the event payload, the JSON object carried in the
	// `data:` field.
	Data json.RawMessage
}

// StreamOptions configures a StreamEvents call.
type StreamOptions struct {
	// LastEventID is the sequence the stream resumes after. Zero
	// starts from the beginning of the retained backlog. A non-zero
	// value sends the §15.1 Last-Event-ID header on the initial
	// request so the gateway replays only events after that cursor.
	LastEventID uint64
}

// Stream is a live handle to a §15.1 session event stream. Construct
// it with Client.StreamEvents. The caller ranges over Events until
// the channel closes, then calls Err to learn why the stream ended.
//
// A Stream is not safe for concurrent use; a single goroutine reads
// Events and then reads Err.
type Stream struct {
	events chan StreamEvent
	done   chan struct{}
	err    error
}

// Events returns the channel of decoded events. The channel is
// closed when the stream ends: on context cancellation, on a
// non-retryable HTTP status, or when the gateway closes the stream
// without a transport error. After the channel closes, call Err.
func (s *Stream) Events() <-chan StreamEvent { return s.events }

// Err returns the error that ended the stream. It returns nil when
// the context was canceled (a clean caller-requested stop) or when
// the gateway closed the stream cleanly. Err must be read only after
// the Events channel is observed closed.
func (s *Stream) Err() error {
	<-s.done
	return s.err
}

// StreamEvents opens the §15.1 GET /v1/sessions/{id}/events
// Server-Sent Events stream for a session and returns a Stream that
// delivers decoded events on a channel.
//
// On a transport disconnect the Stream reconnects automatically,
// sending the last delivered sequence as the Last-Event-ID header so
// the gateway replays the retained backlog from that cursor. Any
// replayed event whose Seq is at or below the last delivered Seq is
// dropped, so the §15.1 reconnect contract holds: the backlog is
// replayed, no event is skipped, and no event is delivered twice.
// Reconnect attempts are spaced by the Client retry policy's backoff.
//
// The stream stops when ctx is canceled (Err then reports nil), when
// the gateway returns a non-retryable HTTP status (Err reports the
// typed APIError), or when the gateway closes the stream without a
// transport error. A retryable HTTP status (429, 5xx) on the initial
// request or on a reconnect is retried like a transport disconnect.
func (c *Client) StreamEvents(ctx context.Context, sessionID string, opt StreamOptions) *Stream {
	s := &Stream{
		events: make(chan StreamEvent),
		done:   make(chan struct{}),
	}
	go c.runStream(ctx, sessionID, opt.LastEventID, s)
	return s
}

// runStream is the Stream's background loop. It opens the SSE
// connection, relays decoded events, and reconnects on a transport
// disconnect until ctx is canceled or a non-retryable status ends
// the stream.
//
// The gateway holds the SSE connection open until the client
// disconnects; it does not close a live stream on its own. The SDK
// therefore treats any connection close, including a clean EOF, as
// an unexpected disconnect and reconnects, the same reconnect
// behavior the WHATWG EventSource model specifies. A reconnect that
// delivers no further event is retried up to the retry policy's
// MaxAttempts so a permanently unavailable stream does not loop
// forever; a reconnect that delivers at least one event resets the
// attempt counter, so a long-lived stream reconnects indefinitely.
func (c *Client) runStream(ctx context.Context, sessionID string, lastSeq uint64, s *Stream) {
	defer close(s.events)
	defer close(s.done)

	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/events"
	policy := c.retry
	// failures counts consecutive reconnects that delivered no event.
	// It bounds reconnection against a permanently broken stream and
	// spaces attempts with the retry policy's backoff. A connection
	// that delivers an event resets it to zero.
	failures := 0

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if failures > 0 {
			delay := policy.delayForAttempt(failures, c.rng)
			if err := sleepCtx(ctx, delay); err != nil {
				return
			}
		}

		delivered, retErr := c.streamOnce(ctx, path, lastSeq, s)
		progressed := delivered > lastSeq
		if delivered > lastSeq {
			lastSeq = delivered
		}

		switch {
		case ctx.Err() != nil:
			// The caller canceled the context. A clean stop.
			return
		case retErr != nil && retErr.permanent:
			s.err = retErr.err
			return
		default:
			// A transport disconnect, a clean EOF, or a retryable
			// status. Reconnect. A connection that delivered an event
			// restarts the attempt counter; otherwise the counter
			// advances toward the MaxAttempts ceiling.
			if progressed {
				failures = 0
				continue
			}
			failures++
			if failures >= policy.MaxAttempts {
				// The stream made no progress across the full retry
				// budget. Surface the last failure, or nil when the
				// connection kept ending at a clean EOF.
				if retErr != nil {
					s.err = retErr.err
				}
				return
			}
		}
	}
}

// streamError carries the outcome of one streamOnce connection. A
// permanent error ends the stream; a non-permanent error triggers a
// reconnect. A nil streamError means the connection ended at a clean
// EOF, which the reconnect loop also treats as a disconnect.
type streamError struct {
	err       error
	permanent bool
}

// streamOnce opens one SSE connection, relays every frame with Seq
// greater than afterSeq onto the Stream channel, and returns the
// highest Seq it delivered together with the reason the connection
// ended. A nil return error means the gateway closed the stream
// cleanly.
func (c *Client) streamOnce(ctx context.Context, path string, afterSeq uint64, s *Stream) (uint64, *streamError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return afterSeq, &streamError{err: fmt.Errorf("lenny: build stream request: %w", err), permanent: true}
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", userAgent)
	if afterSeq > 0 {
		// The §15.1 reconnect cursor. The gateway replays the retained
		// backlog with Seq greater than this value before live
		// delivery resumes.
		req.Header.Set("Last-Event-ID", strconv.FormatUint(afterSeq, 10))
	}
	if c.tenantID != "" {
		req.Header.Set("X-Lenny-Tenant-ID", c.tenantID)
	}
	if c.auth != nil {
		if err := c.auth.Apply(req); err != nil {
			return afterSeq, &streamError{err: fmt.Errorf("lenny: apply auth: %w", err), permanent: true}
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// A transport-level failure (connection refused, reset
		// mid-stream, DNS). Reconnect.
		return afterSeq, &streamError{err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		apiErr := decodeAPIError(resp.StatusCode, body)
		// A retryable status (429, 5xx) is treated like a transport
		// disconnect: reconnect after a backoff. Any other status
		// ends the stream and surfaces the typed error.
		return afterSeq, &streamError{err: apiErr, permanent: !apiErr.Retryable}
	}

	delivered := afterSeq
	parser := newSSEParser(resp.Body)
	for {
		ev, err := parser.next()
		if err != nil {
			if err == io.EOF {
				// The gateway closed the stream. With a live context
				// this is a clean end; the caller's runStream decides
				// whether to reconnect.
				return delivered, nil
			}
			// A read error mid-stream. Reconnect from the last
			// delivered cursor.
			return delivered, &streamError{err: err}
		}
		// Drop a replayed event at or below the last delivered
		// sequence so a reconnect does not surface a duplicate.
		if ev.Seq != 0 && ev.Seq <= delivered {
			continue
		}
		select {
		case <-ctx.Done():
			return delivered, &streamError{err: ctx.Err()}
		case s.events <- ev:
			if ev.Seq > delivered {
				delivered = ev.Seq
			}
		}
	}
}

// sseParser decodes a Server-Sent Events byte stream into
// StreamEvents. It accumulates the id, event, and data fields of one
// frame and emits a StreamEvent when the blank line that terminates
// the frame is read.
type sseParser struct {
	scanner *bufio.Scanner
}

// newSSEParser returns an sseParser reading from r. The scanner
// buffer is sized for large data payloads.
func newSSEParser(r io.Reader) *sseParser {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	return &sseParser{scanner: sc}
}

// next returns the next complete SSE frame. It returns io.EOF when
// the stream ends with no partial frame buffered, or a non-EOF error
// when the underlying reader fails.
func (p *sseParser) next() (StreamEvent, error) {
	var (
		ev      StreamEvent
		data    strings.Builder
		started bool
	)
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if line == "" {
			// A blank line terminates the frame. Dispatch it only when
			// a recognized field was seen, so a blank line that follows
			// only comment lines or blank padding is skipped rather
			// than dispatched as an empty frame.
			if !started {
				continue
			}
			ev.Data = json.RawMessage(data.String())
			return ev, nil
		}
		field, value := splitSSELine(line)
		switch field {
		case "id":
			started = true
			if n, err := strconv.ParseUint(value, 10, 64); err == nil {
				ev.Seq = n
			}
		case "event":
			started = true
			ev.Type = value
		case "data":
			started = true
			// The SSE format joins multiple data lines with a newline.
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		default:
			// A comment line (an empty field name, the line started
			// with a colon) or an unknown field name. The SSE format
			// permits both; neither contributes to the frame.
		}
	}
	if err := p.scanner.Err(); err != nil {
		return StreamEvent{}, err
	}
	// The reader reached EOF. A frame buffered without its blank-line
	// terminator is discarded; a clean stream ends on a frame
	// boundary.
	return StreamEvent{}, io.EOF
}

// splitSSELine splits one SSE line into its field name and value.
// The SSE format separates them with the first colon and strips a
// single leading space from the value. A line with no colon is a
// field name with an empty value.
func splitSSELine(line string) (field, value string) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, ""
	}
	field = line[:idx]
	value = line[idx+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value
}
