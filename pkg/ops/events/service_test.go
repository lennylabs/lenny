// SPDX-License-Identifier: MIT

package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

func fixedNow() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) }

// spec: §25.5 — Publish assigns the §25.3 CloudEvents envelope
// (specversion + time + id) and records the event in the buffer.
func TestService_Publish_StampsEnvelope(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	id, err := s.Publish(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
	page := s.Query(0, events.EventFilter{}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("page events: %d", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.SpecVersion != events.CloudEventsSpecVersion {
		t.Errorf("specversion: %q", ev.SpecVersion)
	}
	if ev.Time.IsZero() {
		t.Error("time should be stamped")
	}
	if ev.ID == "" {
		t.Error("id should be assigned")
	}
}

// spec: §25.5 — the SSE endpoint replays buffered events whose id is
// > the Last-Event-ID header before switching to live delivery.
func TestService_Stream_ReplaysBacklog(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	s.Publish(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "warning"})
	s.Publish(context.Background(), events.OperationalEvent{Type: "dev.lenny.health_status_changed", Severity: "info"})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))

	frames := parseSSEFrames(rec.Body.String())
	if len(frames) != 2 {
		t.Fatalf("backlog frames: %d (%q)", len(frames), rec.Body.String())
	}
	if frames[0].Type != "dev.lenny.alert_fired" {
		t.Errorf("frame[0].Type: %q", frames[0].Type)
	}
	if frames[1].Type != "dev.lenny.health_status_changed" {
		t.Errorf("frame[1].Type: %q", frames[1].Type)
	}
}

// spec: §25.5 lines 2679-2680 — Last-Event-ID (the CloudEvents id /
// eventKey) skips already-seen events on reconnect.
func TestService_Stream_ResumesAfterLastEventID(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	s.Publish(context.Background(), events.OperationalEvent{Type: "first"})
	s.Publish(context.Background(), events.OperationalEvent{Type: "second"})

	// The resume cursor is the CloudEvents id of the first event, read
	// back from the buffer (clients echo the SSE id: line).
	firstKey := s.Query(0, events.EventFilter{}, 0).Events[0].Event.ID

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	req.Header.Set("Last-Event-ID", firstKey)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))

	frames := parseSSEFrames(rec.Body.String())
	if len(frames) != 1 {
		t.Fatalf("frames after resume: %d (%q)", len(frames), rec.Body.String())
	}
	if frames[0].Type != "second" {
		t.Errorf("frame.Type: %q", frames[0].Type)
	}
}

// spec: §25.5 — live events flow to a connected subscriber. (Verified via the
// synchronous ActiveStreams count after a goroutine handles the request.)
func TestService_Stream_LiveDelivery(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})

	pipeR, pipeW := newPipeRecorder()
	defer pipeW.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.HandleStream(pipeW, platformAdminReq(req.WithContext(ctx)))
	}()

	// Wait for the handler to install its subscription.
	waitFor(t, func() bool { return s.ActiveStreams() == 1 })

	s.Publish(context.Background(), events.OperationalEvent{Type: "live"})
	frame := readOneSSEFrame(t, pipeR)
	if frame.Type != "live" {
		t.Errorf("live frame: %q", frame.Type)
	}
	cancel()
	<-done
}

// spec: §25.5 — ?eventType= filters the SSE stream (matches the
// polling endpoint).
func TestService_Stream_FilterByEventType(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	s.Publish(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	s.Publish(context.Background(), events.OperationalEvent{Type: "dev.lenny.upgrade_progressed"})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream?eventType=alert_fired", nil)
	rec := newStreamingRecorder()
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))

	frames := parseSSEFrames(rec.Body.String())
	if len(frames) != 1 || frames[0].Type != "dev.lenny.alert_fired" {
		t.Fatalf("filtered frames: %+v", frames)
	}
}

// spec: §25.5 lines 2687-2699 / §25.2 — the polling endpoint returns
// the canonical pagination envelope (items + opaque cursor + hasMore +
// cursorKind + headCursor) and the cursor round-trips to the next page.
func TestService_Poll_PaginationEnvelope(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})
	for i := 0; i < 5; i++ {
		s.Publish(context.Background(), events.OperationalEvent{Type: "alert_fired"})
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events?limit=3", nil)
	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(req))

	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("first page size: %d", len(page.Items))
	}
	if page.Pagination.Cursor == "" {
		t.Error("cursor should be a non-empty opaque string")
	}
	if page.Pagination.CursorKind != "buffer" {
		t.Errorf("cursorKind: %q, want buffer", page.Pagination.CursorKind)
	}
	if page.Pagination.HeadCursor == "" {
		t.Error("headCursor should be set on a live buffer")
	}
	if !page.Pagination.HasMore {
		t.Error("hasMore should be true")
	}

	// Round-trip the opaque cursor into the next page request.
	next := "/v1/admin/events?limit=3&cursor=" + url.QueryEscape(page.Pagination.Cursor)
	req = httptest.NewRequest(http.MethodGet, next, nil)
	rec = httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(req))
	page = opsstream.EventPage{}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page.Items) != 2 || page.Pagination.HasMore {
		t.Fatalf("page2: items=%d hasMore=%v", len(page.Items), page.Pagination.HasMore)
	}
	if page.Pagination.GapDetected {
		t.Error("a valid round-tripped cursor must not report a gap")
	}
}

// spec: §25.5 — Webhook fan-out callback fires for every published
// event so the existing webhook worker keeps delivering.
func TestService_Publish_FansOutToWebhook(t *testing.T) {
	var got []string
	var mu sync.Mutex
	s := opsstream.New(opsstream.Options{
		Capacity: 16,
		Now:      fixedNow,
		Webhook: func(_ context.Context, e events.OperationalEvent) {
			mu.Lock()
			got = append(got, e.Type)
			mu.Unlock()
		},
	})
	s.Publish(context.Background(), events.OperationalEvent{Type: "a"})
	s.Publish(context.Background(), events.OperationalEvent{Type: "b"})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("webhook payload: %+v", got)
	}
}

// spec: §25.5 — the active-connection count drops to zero after the SSE client
// disconnects.
func TestService_Unsubscribe_OnClientDisconnect(t *testing.T) {
	s := opsstream.New(opsstream.Options{Capacity: 16, Now: fixedNow})

	pipeR, pipeW := newPipeRecorder()
	defer pipeR.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.HandleStream(pipeW, platformAdminReq(req.WithContext(ctx)))
	}()
	waitFor(t, func() bool { return s.ActiveStreams() == 1 })

	cancel()
	pipeW.Close()
	<-done
	waitFor(t, func() bool { return s.ActiveStreams() == 0 })
}

type ssEvent struct {
	ID   string
	Type string
	Data string
}

// parseSSEFrames parses an SSE response body into discrete frames.
func parseSSEFrames(body string) []ssEvent {
	var out []ssEvent
	var cur ssEvent
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "" && cur != (ssEvent{}):
			out = append(out, cur)
			cur = ssEvent{}
		case strings.HasPrefix(line, "id: "):
			cur.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.Data = strings.TrimPrefix(line, "data: ")
		}
	}
	if cur != (ssEvent{}) {
		out = append(out, cur)
	}
	return out
}

// streamingRecorder is an http.ResponseWriter + http.Flusher used by
// the SSE handler tests.
type streamingRecorder struct {
	*httptest.ResponseRecorder
}

func newStreamingRecorder() *streamingRecorder {
	return &streamingRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (s *streamingRecorder) Flush() {}

// pipeRecorder is a streaming ResponseWriter that pipes writes to a
// reader so the test can read live frames synchronously.
type pipeWriter struct {
	header http.Header
	w      *pipeWriterAdapter
	status int
}

type pipeReader struct {
	r interface{ ReadString(byte) (string, error) }
}

type pipeWriterAdapter struct {
	wr   chan []byte
	done chan struct{}
}

func newPipeRecorder() (*pipeReaderWrap, *pipeWriter) {
	ch := make(chan []byte, 16)
	done := make(chan struct{})
	pw := &pipeWriter{
		header: http.Header{},
		w:      &pipeWriterAdapter{wr: ch, done: done},
	}
	pr := &pipeReaderWrap{ch: ch, done: done}
	return pr, pw
}

func (p *pipeWriter) Header() http.Header  { return p.header }
func (p *pipeWriter) WriteHeader(code int) { p.status = code }
func (p *pipeWriter) Write(b []byte) (int, error) {
	buf := make([]byte, len(b))
	copy(buf, b)
	select {
	case p.w.wr <- buf:
		return len(b), nil
	case <-p.w.done:
		return 0, http.ErrBodyNotAllowed
	}
}
func (p *pipeWriter) Flush() {}
func (p *pipeWriter) Close() {
	select {
	case <-p.w.done:
	default:
		close(p.w.done)
	}
}

type pipeReaderWrap struct {
	ch   chan []byte
	done chan struct{}
	buf  string
}

func (p *pipeReaderWrap) Close() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}

// readOneSSEFrame blocks until one SSE frame (id/event/data terminated
// by a blank line) has been read from the pipe.
func readOneSSEFrame(t *testing.T, p *pipeReaderWrap) ssEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if i := strings.Index(p.buf, "\n\n"); i >= 0 {
			frame := p.buf[:i+1]
			p.buf = p.buf[i+2:]
			frames := parseSSEFrames(frame)
			if len(frames) == 0 {
				continue
			}
			return frames[0]
		}
		select {
		case b := <-p.ch:
			p.buf += string(b)
		case <-deadline:
			t.Fatalf("timed out waiting for SSE frame (buffered=%q)", p.buf)
		}
	}
}

// waitFor polls cond until it returns true or the deadline elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within deadline")
}

// keep imports stable for refactors.
var _ = bufio.NewReader
