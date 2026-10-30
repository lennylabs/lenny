// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fastStreamClient builds a Client whose reconnect backoff is short
// enough for a unit test. The retry policy governs the §15.1 stream
// reconnect spacing the same way it governs REST retries.
func fastStreamClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(baseURL, WithRetryPolicy(RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// sseFrame formats one §15.1 SSE frame: id, event, and data lines
// terminated by a blank line.
func sseFrame(seq uint64, typ, data string) string {
	return fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", seq, typ, data)
}

func TestSSEParserDecodesFrames(t *testing.T) {
	// spec: §15.1 SSE frame format `id:`/`event:`/`data:`.
	raw := sseFrame(1, "message_delivered", `{"content":"hello"}`) +
		sseFrame(2, "response", `{"text":"hi"}`) +
		sseFrame(7, "state_changed", `{"state":"running"}`)
	p := newSSEParser(strings.NewReader(raw))

	want := []StreamEvent{
		{Seq: 1, Type: "message_delivered", Data: []byte(`{"content":"hello"}`)},
		{Seq: 2, Type: "response", Data: []byte(`{"text":"hi"}`)},
		{Seq: 7, Type: "state_changed", Data: []byte(`{"state":"running"}`)},
	}
	for i, w := range want {
		got, err := p.next()
		if err != nil {
			t.Fatalf("frame %d: unexpected error %v", i, err)
		}
		if got.Seq != w.Seq || got.Type != w.Type || string(got.Data) != string(w.Data) {
			t.Errorf("frame %d: got %+v, want %+v", i, got, w)
		}
	}
	if _, err := p.next(); err != io.EOF {
		t.Errorf("after last frame: want io.EOF, got %v", err)
	}
}

func TestSSEParserSkipsCommentsAndBlankRuns(t *testing.T) {
	// spec: §15.1 SSE stream; the SSE format permits comment lines
	// and blank-line padding between frames.
	raw := ": keepalive comment\n\n" +
		sseFrame(3, "response", `{"ok":true}`) +
		"\n\n" +
		": another comment\n" +
		sseFrame(4, "response", `{"ok":false}`)
	p := newSSEParser(strings.NewReader(raw))

	first, err := p.next()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if first.Seq != 3 || string(first.Data) != `{"ok":true}` {
		t.Errorf("first frame: got %+v", first)
	}
	second, err := p.next()
	if err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if second.Seq != 4 || string(second.Data) != `{"ok":false}` {
		t.Errorf("second frame: got %+v", second)
	}
	if _, err := p.next(); err != io.EOF {
		t.Errorf("after last frame: want io.EOF, got %v", err)
	}
}

func TestSSEParserJoinsMultilineData(t *testing.T) {
	// spec: §15.1 SSE stream; the SSE format joins repeated `data:`
	// lines with a newline.
	raw := "id: 9\nevent: response\ndata: line one\ndata: line two\n\n"
	p := newSSEParser(strings.NewReader(raw))
	ev, err := p.next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if string(ev.Data) != "line one\nline two" {
		t.Errorf("multiline data: got %q", string(ev.Data))
	}
}

func TestSSEParserDiscardsPartialTrailingFrame(t *testing.T) {
	// spec: §15.1 SSE stream; a frame without its terminating blank
	// line is incomplete and is not emitted.
	raw := sseFrame(1, "response", `{"a":1}`) + "id: 2\nevent: response\ndata: {\"b\":2}\n"
	p := newSSEParser(strings.NewReader(raw))
	if _, err := p.next(); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if _, err := p.next(); err != io.EOF {
		t.Errorf("partial trailing frame: want io.EOF, got %v", err)
	}
}

func TestSplitSSELine(t *testing.T) {
	// spec: §15.1 SSE frame field parsing.
	cases := []struct {
		line, field, value string
	}{
		{"id: 5", "id", "5"},
		{"event: response", "event", "response"},
		{"data:{\"x\":1}", "data", `{"x":1}`},
		{"data:  two spaces", "data", " two spaces"},
		{": comment", "", "comment"},
		{"bareword", "bareword", ""},
	}
	for _, tc := range cases {
		field, value := splitSSELine(tc.line)
		if field != tc.field || value != tc.value {
			t.Errorf("splitSSELine(%q) = (%q, %q), want (%q, %q)",
				tc.line, field, value, tc.field, tc.value)
		}
	}
}

func TestStreamEventsDeliversEventsInOrder(t *testing.T) {
	// spec: §15.1 GET /v1/sessions/{id}/events SSE stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for seq := uint64(1); seq <= 3; seq++ {
			fmt.Fprint(w, sseFrame(seq, "response", fmt.Sprintf(`{"n":%d}`, seq)))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := c.StreamEvents(ctx, "sess_1", StreamOptions{})
	var got []uint64
	for ev := range stream.Events() {
		got = append(got, ev.Seq)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream ended with error: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("delivered sequences: got %v, want [1 2 3]", got)
	}
}

func TestStreamEventsReconnectsAndDeduplicates(t *testing.T) {
	// spec: §15.1 streaming-reconnect-with-cursor; a reconnect
	// resumes via Last-Event-ID and replays the backlog without a
	// gap or a duplicate.
	//
	// The server holds the full event log [1..6]. The first
	// connection delivers events 1 through 3, then drops the
	// connection. On the reconnect the client sends Last-Event-ID: 3;
	// the server, mimicking the gateway, replays event 3 again (an
	// inclusive backlog boundary the SDK must deduplicate) and then
	// delivers 4 through 6.
	const total = 6
	var (
		mu        sync.Mutex
		lastSeen  []string // Last-Event-ID header on each connection
		connCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connCount++
		conn := connCount
		lastSeen = append(lastSeen, r.Header.Get("Last-Event-ID"))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		if conn == 1 {
			// First connection: deliver 1..3, then drop mid-stream.
			for seq := uint64(1); seq <= 3; seq++ {
				fmt.Fprint(w, sseFrame(seq, "response", fmt.Sprintf(`{"n":%d}`, seq)))
				flusher.Flush()
			}
			return
		}
		// Reconnect: resume after the supplied cursor, but replay the
		// cursor event itself so the SDK exercises its dedup path.
		after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
		start := after
		if start == 0 {
			start = 1
		}
		for seq := start; seq <= total; seq++ {
			fmt.Fprint(w, sseFrame(seq, "response", fmt.Sprintf(`{"n":%d}`, seq)))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := c.StreamEvents(ctx, "sess_reconnect", StreamOptions{})
	var got []uint64
	for ev := range stream.Events() {
		got = append(got, ev.Seq)
		if len(got) == total {
			// Every event arrived; stop the stream cleanly.
			cancel()
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream ended with error: %v", err)
	}

	// Every event arrived exactly once, in order, with no gap.
	if len(got) != total {
		t.Fatalf("delivered %d events, want %d: %v", len(got), total, got)
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Errorf("position %d: got seq %d, want %d (full: %v)", i, seq, i+1, got)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if connCount < 2 {
		t.Fatalf("expected at least one reconnect, saw %d connection(s)", connCount)
	}
	// The reconnect carried the cursor of the last delivered event.
	if lastSeen[0] != "" {
		t.Errorf("first connection sent Last-Event-ID %q, want empty", lastSeen[0])
	}
	if lastSeen[1] != "3" {
		t.Errorf("reconnect sent Last-Event-ID %q, want \"3\"", lastSeen[1])
	}
}

func TestStreamEventsResumesFromSuppliedCursor(t *testing.T) {
	// spec: §15.1 streaming-reconnect-with-cursor; a caller-supplied
	// LastEventID sets the Last-Event-ID header on the initial
	// request.
	var (
		mu        sync.Mutex
		firstSeen string
		connCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if connCount == 0 {
			firstSeen = r.Header.Get("Last-Event-ID")
		}
		connCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, sseFrame(11, "response", `{"n":11}`))
		flusher.Flush()
	}))
	defer srv.Close()

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	stream := c.StreamEvents(ctx, "sess_resume", StreamOptions{LastEventID: 10})
	first, ok := <-stream.Events()
	if !ok {
		t.Fatal("stream closed before delivering an event")
	}
	if first.Seq != 11 {
		t.Errorf("delivered seq %d, want 11", first.Seq)
	}
	cancel()
	for range stream.Events() {
		// Drain any event delivered before the cancel was observed.
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if firstSeen != "10" {
		t.Errorf("initial request Last-Event-ID = %q, want \"10\"", firstSeen)
	}
}

func TestStreamEventsStopsOnNonRetryableStatus(t *testing.T) {
	// spec: §15.1 GET /v1/sessions/{id}/events returns 404
	// RESOURCE_NOT_FOUND for an unknown session; a non-retryable
	// status ends the stream with the typed error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":"RESOURCE_NOT_FOUND","category":"PERMANENT","message":"session not found","retryable":false}}`)
	}))
	defer srv.Close()

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := c.StreamEvents(ctx, "sess_missing", StreamOptions{})
	for range stream.Events() {
		t.Error("a 404 stream delivered an event")
	}
	err := stream.Err()
	if err == nil {
		t.Fatal("a 404 stream ended with a nil error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("stream error is not an *APIError: %T %v", err, err)
	}
	if apiErr.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("error code: got %q, want RESOURCE_NOT_FOUND", apiErr.Code)
	}
}

func TestStreamEventsRetriesRetryableStatus(t *testing.T) {
	// spec: §15.1 GET /v1/sessions/{id}/events; a retryable status
	// (503 EVENT_STREAM_UNAVAILABLE) is retried like a transport
	// disconnect rather than ending the stream.
	var mu sync.Mutex
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		n := attempt
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"code":"EVENT_STREAM_UNAVAILABLE","category":"TRANSIENT","message":"event bus unavailable","retryable":true}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, sseFrame(1, "response", `{"n":1}`))
		flusher.Flush()
	}))
	defer srv.Close()

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := c.StreamEvents(ctx, "sess_503", StreamOptions{})
	var got []uint64
	for ev := range stream.Events() {
		got = append(got, ev.Seq)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream ended with error after a retryable status: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("delivered sequences: got %v, want [1]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempt < 2 {
		t.Errorf("a 503 was not retried; saw %d attempt(s)", attempt)
	}
}

func TestStreamEventsStopsOnContextCancel(t *testing.T) {
	// spec: §15.1 SSE stream; context cancellation stops the stream
	// cleanly and Err reports nil.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, sseFrame(1, "response", `{"n":1}`))
		flusher.Flush()
		// Hold the connection open until the test releases it so the
		// client must observe the context cancellation to stop.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := fastStreamClient(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	stream := c.StreamEvents(ctx, "sess_cancel", StreamOptions{})
	first, ok := <-stream.Events()
	if !ok {
		t.Fatal("stream closed before delivering the first event")
	}
	if first.Seq != 1 {
		t.Errorf("first event seq: got %d, want 1", first.Seq)
	}
	// Cancel; the Events channel must close and Err must report nil.
	cancel()
	for range stream.Events() {
		// Drain any event delivered before the cancel was observed.
	}
	if err := stream.Err(); err != nil {
		t.Errorf("context-canceled stream Err: got %v, want nil", err)
	}
}
