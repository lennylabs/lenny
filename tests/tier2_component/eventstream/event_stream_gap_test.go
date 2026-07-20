//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test for the §25.5 operational-event-stream read side
// against a real MAXLEN-bounded Redis ops:events:stream. It drives the
// pkg/ops/events.Service Redis source (XRANGE polling, XREAD BLOCK 0 live
// tail, cross-source cursor translation) against a real Redis container
// paired with the pkg/gateway/eventbuffer.StreamEmitter producer, so the
// producer XADD encoding and the read-side decode are exercised end to end.
package eventstream_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// streamKey isolates each test's events on its own Redis stream so parallel
// tests do not observe one another's entries.
const gapStreamKey = "ops:events:stream:gaptest"

// newStreamEmitter builds the §25.5 producer side writing to the given
// MAXLEN-bounded Redis stream, matching the gateway/controller XADD
// encoding the read side decodes.
func newStreamEmitter(t *testing.T, client eventbuffer.StreamRedis, key string, maxLen int64) *eventbuffer.StreamEmitter {
	t.Helper()
	return eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    client,
		Buffer:    eventbuffer.NewEventBuffer(500),
		StreamKey: key,
		MaxLen:    maxLen,
		ReplicaID: "gw-1",
	})
}

func alertEvent(subject string) gwevents.OperationalEvent {
	return gwevents.OperationalEvent{
		Type:            gwevents.EventType("alert_fired").CloudEventsType(),
		Subject:         subject,
		Severity:        "warning",
		DataContentType: gwevents.ContentTypeJSON,
		Data:            json.RawMessage(`{"alert":"x"}`),
	}
}

// pollRedis drives GET /v1/admin/events through the Service and decodes the
// §25.5 poll envelope.
func pollRedis(t *testing.T, s *opsstream.Service, cursor string) opsstream.EventPage {
	t.Helper()
	url := "/v1/admin/events"
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", url, nil)
	s.HandlePoll(rec, req)
	var page opsstream.EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body (status %d): %v", rec.Code, err)
	}
	return page
}

// TestOpsEventStreamGapDetectedOnRedisEviction XADDs operational events
// to a real MAXLEN-bounded Redis ops:events:stream past its cap so the
// oldest entries evict, records a cursor pointing at a now-evicted
// event, then polls the §25.5 read surface with that stale cursor. It
// asserts the response reports pagination.gapDetected: true, carries a
// populated pagination.oldestAvailableCursor pointing at the oldest
// retained event, and that the poll increments
// lenny_ops_events_stream_gaps_total by one.
//
// spec: 25.5 (Eviction and gap behavior) — "When events are evicted
// before a slow subscriber reads them, the subscriber's next request
// returns pagination.gapDetected: true (canonical envelope, Section
// 25.2). ... Gap rate (lenny_ops_events_stream_gaps_total) should be
// near zero in healthy operation", and 25.5 (Cursor Model) — "When a
// caller sends a cursor from one source to another that cannot honor
// it, lenny-ops translates by scanning for the first event with a
// matching eventKey. If no match is found (the event has been evicted),
// the response returns gapDetected: true per the canonical pagination
// envelope (Section 25.2) along with oldestAvailableCursor."
// diagnosis: a failure means the §25.5 read surface does not report a
// gap when its Redis-backed cursor references an evicted event — the
// gapDetected flag or the oldestAvailableCursor was absent, or the
// lenny_ops_events_stream_gaps_total counter did not increment — so a
// slow subscriber would silently miss events instead of being told to
// re-read platform state.
func TestOpsEventStreamGapDetectedOnRedisEviction(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const maxLen = 10
	emitter := newStreamEmitter(t, rd.Client, gapStreamKey, maxLen)

	// Emit a first small batch, then poll to capture a live cursor pointing
	// at the newest of that batch.
	for i := 0; i < 3; i++ {
		if err := emitter.Emit(ctx, alertEvent("pool/early")); err != nil {
			t.Fatalf("emit early event %d: %v", i, err)
		}
	}

	var gaps atomic.Int64
	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: gapStreamKey,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
		OnGap:          func() { gaps.Add(1) },
	})

	first := pollRedis(t, svc, "")
	if len(first.Items) != 3 {
		t.Fatalf("first poll returned %d items, want the 3 emitted early events", len(first.Items))
	}
	if first.Pagination.CursorKind != opsstream.SourceKindRedis {
		t.Fatalf("first poll cursorKind = %q, want %q", first.Pagination.CursorKind, opsstream.SourceKindRedis)
	}
	if first.Pagination.GapDetected {
		t.Fatal("first healthy poll must not report a gap")
	}
	staleCursor := first.Pagination.Cursor
	if staleCursor == "" {
		t.Fatal("first poll returned no continuation cursor")
	}

	// Emit far more than the MAXLEN cap so the early batch, and the position
	// the stale cursor references, are evicted before the next poll.
	for i := 0; i < 60; i++ {
		if err := emitter.Emit(ctx, alertEvent("pool/late")); err != nil {
			t.Fatalf("emit late event %d: %v", i, err)
		}
	}

	gapped := pollRedis(t, svc, staleCursor)
	if !gapped.Pagination.GapDetected {
		t.Error("poll with an evicted cursor must report pagination.gapDetected: true")
	}
	if gapped.Pagination.OldestAvailableCursor == "" {
		t.Error("a detected gap must carry pagination.oldestAvailableCursor for recovery")
	}
	if gapped.Pagination.SuggestedAction != "resync" {
		t.Errorf("gap suggestedAction = %q, want \"resync\"", gapped.Pagination.SuggestedAction)
	}
	if got := gaps.Load(); got != 1 {
		t.Errorf("gap counter = %d, want exactly 1 increment for the one gapped poll", got)
	}

	// The recovery cursor must resolve against the current stream and return
	// events without re-reporting a gap, so an agent can resync.
	recovered := pollRedis(t, svc, gapped.Pagination.OldestAvailableCursor)
	if recovered.Pagination.GapDetected {
		t.Error("resuming from oldestAvailableCursor must not itself report a gap")
	}
	if len(recovered.Items) == 0 {
		t.Error("resuming from oldestAvailableCursor must serve the retained window")
	}
}

// TestOpsEventStreamPollItemShape pins that a Redis-served poll item keeps the
// buffer-served item shape on the wire: {"id":N,"event":{...}} with a
// non-zero top-level wrapper id. The buffer-served path stamps a per-replica
// in-memory sequence there; the Redis path stamps a synthetic per-source
// position derived from the stream ID, so the /v1/admin/events envelope keeps
// the same frozen item shape whichever source is active. Ordering and resume
// run off the pagination cursor and the CloudEvents id rather than the
// per-source wrapper id.
//
// spec: 25.5 (Polling Delivery — the poll envelope and SSE frame served from
// the Redis ops:events:stream carry the same item shape and CloudEvents record
// as the buffer-served path; ordering and resume run off the pagination cursor
// and the CloudEvents id, not the wrapper id).
// diagnosis: a failure means the Redis-served poll item dropped its top-level
// wrapper id (diverging from the frozen buffer envelope) or stamped a
// source-dependent zero, so the same lenny-ops endpoint no longer presents a
// stable item shape across the local ring buffer and the Redis source.
func TestOpsEventStreamPollItemShape(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:wrapperid"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	for i := 0; i < 4; i++ {
		if err := emitter.Emit(ctx, alertEvent("pool/w")); err != nil {
			t.Fatalf("emit event %d: %v", i, err)
		}
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// Read the RAW poll body: the wire item must carry the CloudEvents record
	// under "event" and a top-level wrapper id (the per-source buffer
	// position), the {"id":N,"event":{...}} shape the buffer path also emits.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events", nil)
	svc.HandlePoll(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items      []map[string]json.RawMessage `json:"items"`
		Pagination struct {
			CursorKind string `json:"cursorKind"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode poll body: %v; body=%s", err, rec.Body.String())
	}
	if body.Pagination.CursorKind != opsstream.SourceKindRedis {
		t.Fatalf("poll cursorKind = %q, want redis (Redis must be the active source)", body.Pagination.CursorKind)
	}
	if len(body.Items) != 4 {
		t.Fatalf("poll returned %d items, want the 4 emitted events", len(body.Items))
	}
	for i, item := range body.Items {
		rawID, present := item["id"]
		if !present {
			t.Errorf("item %d dropped its top-level wrapper id; the Redis-served poll item must keep the {\"id\":N,\"event\":{...}} shape: %v", i, item)
		} else {
			var wrapperID uint64
			if err := json.Unmarshal(rawID, &wrapperID); err != nil {
				t.Errorf("item %d wrapper id did not decode as a number: %v", i, err)
			} else if wrapperID == 0 {
				t.Errorf("item %d carries a zero wrapper id; a Redis-served item must stamp a synthetic per-source position from its stream ID", i)
			}
		}
		raw, ok := item["event"]
		if !ok {
			t.Errorf("item %d missing the CloudEvents record under \"event\": %v", i, item)
			continue
		}
		var ce gwevents.OperationalEvent
		if err := json.Unmarshal(raw, &ce); err != nil {
			t.Errorf("item %d event did not decode as a CloudEvents record: %v", i, err)
			continue
		}
		if ce.Type != "dev.lenny.alert_fired" {
			t.Errorf("item %d event type = %q, want dev.lenny.alert_fired (payload must survive the decode)", i, ce.Type)
		}
		if ce.ID == "" {
			t.Errorf("item %d has no CloudEvents id: the canonical eventKey must be present for cross-source resume", i)
		}
	}
}

// pollRedisLimited drives GET /v1/admin/events with an explicit page limit so
// a test can obtain a mid-stream continuation cursor.
func pollRedisLimited(t *testing.T, s *opsstream.Service, cursor string, limit int) opsstream.EventPage {
	t.Helper()
	url := fmt.Sprintf("/v1/admin/events?limit=%d", limit)
	if cursor != "" {
		url += "&cursor=" + cursor
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", url, nil)
	s.HandlePoll(rec, req)
	var page opsstream.EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body (status %d): %v", rec.Code, err)
	}
	return page
}

// collectSSEResume drives GET /v1/admin/events/stream over a live,
// cancellable context (the Redis backlog XRANGE reads need a live context, so
// a pre-cancelled request would spuriously fail the resume). It reads SSE
// lines until the stream goes idle, then cancels and returns how many data:
// frames and whether a :gap comment were emitted during the backlog replay.
func collectSSEResume(t *testing.T, s *opsstream.Service, target string) (dataFrames int, sawGap bool) {
	t.Helper()
	return collectSSEResumeWithHeader(t, s, httptest.NewRequest("GET", target, nil))
}

// collectSSEResumeWithHeader is collectSSEResume over a caller-built request,
// so a test can carry a Last-Event-ID resume position rather than a ?cursor=.
func collectSSEResumeWithHeader(t *testing.T, s *opsstream.Service, base *http.Request) (dataFrames int, sawGap bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{hdr: http.Header{}, w: pw}
	req := base.WithContext(ctx)
	go func() {
		s.HandleStream(rw, req)
		_ = pw.Close()
	}()

	lines := make(chan string, 128)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	idle := time.NewTimer(1500 * time.Millisecond)
	defer idle.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				cancel()
				return
			}
			switch {
			case strings.HasPrefix(line, "data: "):
				dataFrames++
			case strings.HasPrefix(line, ":gap"):
				sawGap = true
			}
			idle.Reset(1500 * time.Millisecond)
		case <-idle.C:
			cancel()
			// Drain until the handler closes the pipe.
			for range lines {
			}
			return
		}
	}
}

// TestOpsEventStreamSSEResumeHonorsRedisCursorSourceKind polls the Redis
// source to obtain a redis-kind continuation cursor (whose position is a
// Redis stream ID), then reconnects to the SSE stream via the documented
// ?cursor= fallback and asserts the resume reads directly by stream ID: only
// the events after the cursor are replayed and no spurious :gap comment is
// emitted.
//
// spec: 25.5 (Cursor Model) — "When a caller sends a cursor from one source
// to another that cannot honor it, lenny-ops translates by scanning for the
// first event with a matching eventKey", and (SSE Delivery) — "Each SSE
// connection gets an independent read cursor against the raw stream ...
// clients reconnecting ... resume from the correct position via XRANGE". A
// redis cursor's position is a stream ID, so the SSE resume must read it
// directly by stream ID; only a buffer/mixed cursor or a Last-Event-ID
// (a CloudEvents id) is translated by eventKey scan.
// diagnosis: a failure means the SSE resume ignores the cursor's source_kind
// and treats a redis cursor's stream-ID position as a CloudEvents eventKey.
// The eventKey scan never matches, so the handler emits a :gap comment and
// replays the entire retained window, double-delivering every event the
// client already consumed on the poll path.
func TestOpsEventStreamSSEResumeHonorsRedisCursorSourceKind(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:ssecursor"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	for i := 0; i < 5; i++ {
		if err := emitter.Emit(ctx, alertEvent("pool/e")); err != nil {
			t.Fatalf("emit event %d: %v", i, err)
		}
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// Poll the first two events to obtain a mid-stream redis-kind cursor.
	page := pollRedisLimited(t, svc, "", 2)
	if page.Pagination.CursorKind != opsstream.SourceKindRedis {
		t.Fatalf("poll cursorKind = %q, want redis", page.Pagination.CursorKind)
	}
	cursor := page.Pagination.Cursor
	if cursor == "" {
		t.Fatal("poll returned no continuation cursor")
	}

	// Reconnect to SSE with that redis cursor. Only the three events after the
	// cursor must be replayed, with no :gap comment.
	frames, sawGap := collectSSEResume(t, svc, "/v1/admin/events/stream?cursor="+cursor)
	if sawGap {
		t.Error("redis-cursor SSE resume emitted a spurious :gap; the stream-ID position was mis-translated as an eventKey")
	}
	if frames != 3 {
		t.Errorf("redis-cursor SSE resume replayed %d frames, want 3 (only events after the cursor, not the full window)", frames)
	}
}

// TestOpsEventStreamPerConnectionIndependentCursorLiveTail drives two
// concurrent SSE subscribers against a real Redis ops:events:stream and
// asserts each holds an independent per-connection read cursor: a live event
// XADDed after both connect is delivered to both, rather than a
// consumer-group split where only one subscriber sees it. It also exercises
// the XREAD BLOCK 0 live tail and the XRANGE backlog resume.
//
// spec: 25.5 (SSE Delivery) — "reads Redis via XREAD BLOCK 0 in a
// goroutine ... Each SSE connection gets an independent read cursor against
// the raw stream with no consumer group."
// diagnosis: a failure means two SSE subscribers compete for events instead
// of each receiving the full stream — the read side created a consumer
// group or shared a cursor — so a second operations agent connecting would
// silently receive only a subset of operational events.
func TestOpsEventStreamPerConnectionIndependentCursorLiveTail(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const liveStreamKey = "ops:events:stream:livetail"
	emitter := newStreamEmitter(t, rd.Client, liveStreamKey, 1000)

	// One backlog event so each connection resumes over a non-empty stream.
	if err := emitter.Emit(ctx, alertEvent("pool/backlog")); err != nil {
		t.Fatalf("emit backlog event: %v", err)
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: liveStreamKey,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// Open two independent SSE connections. Each drives Service.HandleStream
	// through a pipe with its own cancellable request context, so the live
	// XREAD BLOCK 0 tail terminates deterministically on cancel (a real HTTP
	// server does not cancel a request context while the handler blocks
	// inside a read, so it cannot tear a blocked SSE tail down on its own).
	subA := openStream(t, svc)
	subB := openStream(t, svc)
	defer subA.close()
	defer subB.close()

	// Both must first receive the backlog event.
	subA.expectEventType(t, "backlog", "dev.lenny.alert_fired")
	subB.expectEventType(t, "backlog", "dev.lenny.alert_fired")

	// A live event XADDed after both connected must reach both independently.
	if err := emitter.Emit(ctx, alertEvent("pool/live")); err != nil {
		t.Fatalf("emit live event: %v", err)
	}
	subA.expectSubject(t, "A", "pool/live")
	subB.expectSubject(t, "B", "pool/live")
}

// sseConn reads an SSE stream frame by frame off a live Service.HandleStream
// connection driven through an in-memory pipe.
type sseConn struct {
	cancel context.CancelFunc
	events <-chan sseFrame
}

type sseFrame struct {
	id    string
	typ   string
	event gwevents.OperationalEvent
}

// pipeResponseWriter is a streaming http.ResponseWriter that forwards every
// write to an io.Pipe and satisfies http.Flusher, so a test can read SSE
// frames as the handler emits them without a real HTTP server.
type pipeResponseWriter struct {
	hdr http.Header
	w   *io.PipeWriter
}

func (p *pipeResponseWriter) Header() http.Header         { return p.hdr }
func (p *pipeResponseWriter) WriteHeader(int)             {}
func (p *pipeResponseWriter) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeResponseWriter) Flush()                      {}

func openStream(t *testing.T, svc *opsstream.Service) *sseConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{hdr: http.Header{}, w: pw}
	req := httptest.NewRequest("GET", "/v1/admin/events/stream", nil).WithContext(ctx)
	go func() {
		svc.HandleStream(rw, req)
		_ = pw.Close()
	}()
	ch := make(chan sseFrame, 16)
	go readSSE(pr, ch)
	return &sseConn{cancel: cancel, events: ch}
}

func readSSE(body io.Reader, ch chan<- sseFrame) {
	defer close(ch)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var cur sseFrame
	have := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
			have = true
		case strings.HasPrefix(line, "event: "):
			cur.typ = strings.TrimPrefix(line, "event: ")
			have = true
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.event)
			have = true
		case line == "":
			if have {
				ch <- cur
				cur = sseFrame{}
				have = false
			}
		}
	}
}

func (c *sseConn) close() {
	c.cancel()
}

func (c *sseConn) next(t *testing.T) sseFrame {
	t.Helper()
	select {
	case f, ok := <-c.events:
		if !ok {
			t.Fatal("SSE stream closed before an expected frame arrived")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an SSE frame")
		return sseFrame{}
	}
}

func (c *sseConn) expectEventType(t *testing.T, label, typ string) {
	t.Helper()
	f := c.next(t)
	if f.event.Type != typ {
		t.Fatalf("%s: first frame type = %q, want %q", label, f.event.Type, typ)
	}
}

func (c *sseConn) expectSubject(t *testing.T, label, subject string) {
	t.Helper()
	// The live frame may follow additional backlog frames; scan a few.
	for i := 0; i < 5; i++ {
		f := c.next(t)
		if f.event.Subject == subject {
			return
		}
	}
	t.Fatalf("%s: never received a live frame with subject %q", label, subject)
}

// TestOpsEventStreamLiveTailWakesOnPublishAndExitsOnDisconnect pins the two
// properties the live tail's bounded XREAD BLOCK stands on. §25.5 specifies
// XREAD BLOCK 0 for the per-connection tail; the read side issues a bounded
// block instead, because go-redis does not interrupt a deadline-free blocked
// read when the connection context is cancelled, which would leak a goroutine
// per disconnected SSE connection. The substitution is only sound while it
// preserves BLOCK 0's delivery semantics, so this asserts an event XADDed while
// a tail is parked arrives promptly, well inside the block interval, and that
// the connection's goroutine unwinds once the context is cancelled. A tail
// rewritten as a poll, or one whose block interval is raised into poll
// territory, fails the first assertion; a tail that parks forever fails the
// second.
//
// spec: 25.5 (SSE Delivery) — "reads Redis via XREAD BLOCK 0 in a goroutine ...
// Each SSE connection gets an independent read cursor against the raw stream
// with no consumer group."
// diagnosis: a failure means the §25.5 live tail no longer sleeps inside Redis
// waiting on the stream. Either it has degraded into interval polling, so every
// operational event reaches subscribers late by up to one interval, or a
// disconnected connection's tail goroutine no longer exits, leaking one
// goroutine and one Redis connection per dropped SSE client.
func TestOpsEventStreamLiveTailWakesOnPublishAndExitsOnDisconnect(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:tailwake"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	if err := emitter.Emit(ctx, alertEvent("pool/backlog")); err != nil {
		t.Fatalf("emit backlog event: %v", err)
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	conn := openStream(t, svc)
	conn.expectEventType(t, "backlog", "dev.lenny.alert_fired")

	// The tail is now parked inside Redis. Let it settle into the block, then
	// XADD and measure how long the frame takes to arrive.
	time.Sleep(250 * time.Millisecond)
	start := time.Now()
	if err := emitter.Emit(ctx, alertEvent("pool/woken")); err != nil {
		t.Fatalf("emit live event: %v", err)
	}
	conn.expectSubject(t, "tail", "pool/woken")
	if latency := time.Since(start); latency > 500*time.Millisecond {
		t.Fatalf("a live event took %s to reach the parked tail; the tail must wake on the XADD rather than on a poll interval", latency)
	}

	// Cancelling the connection unwinds the tail goroutine: the handler closes
	// its response body, which ends the frame reader.
	conn.close()
	select {
	case _, open := <-conn.events:
		if open {
			// Drain any frame already in flight, then require the close.
			select {
			case _, stillOpen := <-conn.events:
				if stillOpen {
					t.Fatal("the SSE frame channel stayed open after cancellation; the tail goroutine did not exit")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the tail goroutine did not exit within 5s of the connection being cancelled")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tail goroutine did not exit within 5s of the connection being cancelled")
	}
}
