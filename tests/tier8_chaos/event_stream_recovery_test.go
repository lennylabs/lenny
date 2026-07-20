// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the §25.5 operational event-stream read side under
// a Redis outage: the best-effort recovery flush that re-emits locally
// buffered lenny-ops events to the recovered ops:events:stream, and the
// transparent mid-connection source switch from the Redis stream to the
// gateway-buffer fall-back and back. Both run against a real Redis container
// so the flush and the live XREAD tail exercise the production Redis paths
// rather than an in-memory fake.
package tier8_chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// toggleHealth is a SourceHealth whose reachability flips at runtime to inject
// and clear a Redis outage on the read path.
type toggleHealth struct {
	redis   atomic.Bool
	gateway atomic.Bool
}

func (h *toggleHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *toggleHealth) GatewayAvailable() bool { return h.gateway.Load() }

// spec: 25.5 (best-effort recovery flush, eventKey dedup) — lenny-ops keeps
// emitting its own events into the local ring while Redis is unreachable (the
// XADD fails); on recovery those events must reach the shared
// ops:events:stream so a consumer that connects only after Redis is back still
// observes them, deduplicated by eventKey so an event that did reach Redis
// before the outage is not duplicated. The events here are emitted inside the
// detection lag, before anything observes the outage, so the flush reaches them
// only when its window opens at the first failed XADD rather than at the
// source-health probe's later observation.
//
// diagnosis: a failure means the §25.5 recovery flush is broken — either the
// locally buffered outage-window events never reach the recovered Redis stream
// (abandoned), or an event already on the stream is re-emitted and duplicated,
// so a consumer sees it twice.
func TestOpsEventStreamRecoveryFlushReEmitsBufferedEventsAfterRedisRecovers(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{RedisClient: opsstream.NewRedisStreamClient(rd.Client), SourceHealth: health, ReplicaID: "ops-1"})

	// The re-emit path writes directly to the recovered stream through the
	// production StreamEmitter, which preserves each event's existing eventKey.
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		ReplicaID: "ops-1",
	})
	svc.SetRedisReEmitter(emitter.Emit)

	// One event reached Redis before the outage.
	pre := gwevents.OperationalEvent{ID: "ops-1:pre:1", Type: "dev.lenny.drift_detected", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()}
	if err := emitter.Emit(ctx, pre); err != nil {
		t.Fatalf("seed pre-outage event: %v", err)
	}

	// The pre-outage event is in the local ring too (it was emitted locally
	// before the outage started).
	if _, err := svc.Publish(ctx, pre); err != nil {
		t.Fatalf("buffer pre-outage event: %v", err)
	}

	// Redis goes away. Nothing observes it yet: the source-health probe
	// refreshes on an interval, so the events lenny-ops emits in that detection
	// lag land in the local ring while their XADD fails, with no outage marker
	// recorded first. The fan-out emitter opens the window at the first event
	// whose XADD failed, which is what has to bring those events into the
	// flush.
	var firstFailedID uint64
	for _, e := range []gwevents.OperationalEvent{
		{ID: "ops-1:out:1", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1001, 0).UTC()},
		{ID: "ops-1:out:2", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1002, 0).UTC()},
	} {
		id, err := svc.Publish(ctx, e)
		if err != nil {
			t.Fatalf("buffer local event %s: %v", e.ID, err)
		}
		if firstFailedID == 0 {
			firstFailedID = id
		}
	}
	svc.MarkRedisWriteFailure(firstFailedID)

	// Redis recovers: the replica-level edge detector fires the flush.
	flushed, err := svc.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("recovery flush: %v", err)
	}
	if flushed != 2 {
		t.Fatalf("flush re-emitted %d events; want 2 (the two outage-window events, not the pre-outage one)", flushed)
	}

	msgs, err := rd.Client.XRange(ctx, eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	counts := map[string]int{}
	for _, m := range msgs {
		var ev gwevents.OperationalEvent
		if raw, ok := m.Values["event"].(string); ok {
			_ = json.Unmarshal([]byte(raw), &ev)
		}
		counts[ev.ID]++
	}
	if counts["ops-1:pre:1"] != 1 {
		t.Errorf("pre-outage event on the stream %d times; want exactly 1 (eventKey dedup must not duplicate it)", counts["ops-1:pre:1"])
	}
	if counts["ops-1:out:1"] != 1 || counts["ops-1:out:2"] != 1 {
		t.Errorf("outage-window events not flushed exactly once: %v", counts)
	}
}

// syncBuffer is a thread-safe streaming ResponseWriter+Flusher so an SSE
// handler running in a goroutine can be read concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
	hdr http.Header
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{hdr: http.Header{}} }

func (s *syncBuffer) Header() http.Header { return s.hdr }
func (s *syncBuffer) WriteHeader(int)     {}
func (s *syncBuffer) Flush()              {}
func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// spec: 25.5 (transparent Redis to gateway-buffer switch, recovery, cross-switch
// no-drop) — an open SSE connection injected with a Redis outage mid-stream
// stays open, switches to the gateway-buffer fan-out fall-back (announcing the
// degradation), and on recovery switches back to the Redis XREAD tail announcing
// :degradation {"level":"healthy"}. Across the switch each event is delivered
// exactly once: the gateway StreamEmitter XADDs every gateway-originated event
// to ops:events:stream as well as the per-replica buffer, so the Redis-served
// window and the gateway-buffer window overlap; an event already delivered from
// Redis must not be re-delivered when the connection re-polls the gateway
// buffer. The pre-fix serveGateway opened each stint with a fresh delivered set
// and ignored the carried resume position, so it re-delivered the whole
// overlapping window; this asserts the overlapping event is delivered once.
//
// diagnosis: a failure means the §25.5 transparent source switch is broken —
// the connection does not fall back to the gateway buffer during the outage
// (no degradation announced, no gateway event served), it does not return to
// the Redis tail on recovery (no healthy announcement), or it re-delivers an
// event across the switch (the overlapping event appears more than once).
func TestOpsEventStreamSwitchesToGatewayBufferAndBackOnRedisOutage(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The gateway replica's buffer window overlaps the Redis-served window: it
	// holds the pre-outage event the connection already saw over Redis
	// (ops-1:1000:1) plus a newer gateway-originated event (gw:2000:1). The
	// overlap models the StreamEmitter writing gateway events to both Redis and
	// the buffer, and is what makes a fresh-delivered-set serveGateway
	// re-deliver the already-seen event.
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gwevents.BufferedEventPage{Events: []gwevents.BufferedEvent{
			{ID: 1, Event: gwevents.OperationalEvent{ID: "ops-1:1000:1", Type: "dev.lenny.drift_detected", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()}},
			{ID: 2, Event: gwevents.OperationalEvent{ID: "gw:2000:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2000, 0).UTC()}},
		}})
	}))
	defer gwSrv.Close()
	gwClient, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("t"),
		Discovery:         gateway.StaticDiscovery{gwSrv.URL},
		PerRequestTimeout: 3 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{RedisClient: opsstream.NewRedisStreamClient(rd.Client), SourceHealth: health, ReplicaID: "ops-1"})
	svc.SetGatewayBufferSource(gwClient)

	// Seed a Redis-stream event the connection sees before the outage.
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{Client: rd.Client, Buffer: eventbuffer.NewEventBuffer(0), ReplicaID: "ops-1"})
	if err := emitter.Emit(ctx, gwevents.OperationalEvent{ID: "ops-1:1000:1", Type: "dev.lenny.drift_detected", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()}); err != nil {
		t.Fatalf("seed redis event: %v", err)
	}

	rec := newSyncBuffer()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, req)
	}()

	// Redis-mode delivery of the seeded event.
	waitContains(t, rec, "ops-1:1000:1", 5*time.Second, "the Redis-served backlog event")

	// Inject the Redis outage: the open connection switches to the gateway
	// buffer, announces the degradation, and serves the newer gateway event.
	// It must NOT re-serve ops-1:1000:1, which it already delivered from Redis
	// and which also sits in the gateway buffer window (the overlap).
	health.redis.Store(false)
	waitContains(t, rec, "gateway-buffer", 5*time.Second, "the gateway-buffer degradation announcement")
	waitContains(t, rec, "gw:2000:1", 5*time.Second, "the gateway-originated event from the fan-out fall-back")

	// The gateway event reaches Redis too (the StreamEmitter XADDs it), so the
	// switch back can resume from it cleanly. Emit it after the fall-back has
	// served it and after the Redis tail was torn down, so no live tail races
	// the fan-out on it.
	if err := emitter.Emit(ctx, gwevents.OperationalEvent{ID: "gw:2000:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2000, 0).UTC()}); err != nil {
		t.Fatalf("emit gateway event to redis: %v", err)
	}

	// Recover Redis: the connection switches back to the XREAD tail and
	// announces recovery.
	health.redis.Store(true)
	waitContains(t, rec, "\"level\":\"healthy\"", 6*time.Second, "the recovery announcement on switch back to Redis")

	cancel()
	<-done

	// The pre-outage event was delivered once over Redis and sits in the
	// gateway buffer window too; the switch into the fall-back must not
	// re-deliver it. Count the SSE id: line, which appears once per delivered
	// frame. The pre-fix serveGateway re-delivered it, yielding two id: lines.
	if got := strings.Count(rec.String(), "id: ops-1:1000:1\n"); got != 1 {
		t.Fatalf("overlapping pre-outage event delivered %d times across the source switch; want exactly 1:\n%s", got, rec.String())
	}
}

func waitContains(t *testing.T, rec *syncBuffer, want string, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%q) on the SSE stream:\n%s", what, want, rec.String())
}

// spec: 25.5 (best-effort recovery flush scoped to the outage window, eventKey
// dedup) — the flush re-emits the events buffered during the outage. An event
// that reached Redis long before the outage and has since been trimmed off the
// MAXLEN-bounded shared stream is not one of them: the stream is trimmed by
// every producer's traffic, so absence from its retained window is not evidence
// that an event never arrived. Re-emitting on that signal alone puts an
// already-delivered event back at the head of the stream, where a consumer
// resuming by stream position receives it a second time — the duplicate
// delivery the flush's eventKey dedup exists to prevent. The pre-fix flush
// snapshotted the whole local ring and used the retained window as its only
// guard, so it replayed the trimmed event on every Redis recovery, including a
// blip that buffered nothing; this fails against that code.
//
// diagnosis: a failure means the §25.5 recovery flush is not bounded by the
// outage window — it re-emits events that already reached Redis, so a consumer
// tailing the recovered ops:events:stream is delivered old operational events
// as though they were newly emitted, once per Redis recovery edge.
func TestOpsEventStreamRecoveryFlushDoesNotReplayTrimmedPreOutageEvents(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:flushtrim"
	const maxLen = 3

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		MaxLen:    maxLen,
		ReplicaID: "ops-1",
	})
	svc.SetRedisReEmitter(emitter.Emit)

	// A lenny-ops event that reached Redis well before the outage, and is also
	// resident in the local ring.
	pre := gwevents.OperationalEvent{ID: "ops-1:pre:1", Type: "dev.lenny.drift_detected", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()}
	if err := emitter.Emit(ctx, pre); err != nil {
		t.Fatalf("seed pre-outage event: %v", err)
	}
	if _, err := svc.Publish(ctx, pre); err != nil {
		t.Fatalf("buffer pre-outage event locally: %v", err)
	}

	// Ordinary gateway traffic trims it off the bounded stream.
	for i := 0; i < maxLen+2; i++ {
		if err := emitter.Emit(ctx, gwevents.OperationalEvent{
			ID:          fmt.Sprintf("gw-1:2000:%d", i),
			Type:        "dev.lenny.alert_fired",
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(2000+int64(i), 0).UTC(),
		}); err != nil {
			t.Fatalf("emit gateway traffic %d: %v", i, err)
		}
	}
	// Redis trims MAXLEN approximately (whole nodes at a time), so force the
	// exact trim the traffic will eventually cause.
	if err := rd.Client.XTrimMaxLen(ctx, streamKey, maxLen).Err(); err != nil {
		t.Fatalf("trim stream: %v", err)
	}
	if inStream(t, rd.Client, streamKey)["ops-1:pre:1"] != 0 {
		t.Fatal("the pre-outage event was expected to be trimmed off the bounded stream before the outage")
	}

	// A consumer has tailed the stream up to here.
	consumerAt := streamHead(t, rd.Client, streamKey)

	// The outage: one lenny-ops event lands in the local ring only.
	health.redis.Store(false)
	svc.MarkRedisOutage()
	out := gwevents.OperationalEvent{ID: "ops-1:out:1", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(3000, 0).UTC()}
	if _, err := svc.Publish(ctx, out); err != nil {
		t.Fatalf("buffer outage-window event: %v", err)
	}

	// Redis recovers and the replica-level edge detector flushes the window.
	health.redis.Store(true)
	flushed, err := svc.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("recovery flush: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("flush re-emitted %d events; want 1 (only the outage-window event)", flushed)
	}

	// What the consumer receives when it resumes from where it left off.
	delivered := afterPosition(t, rd.Client, streamKey, consumerAt)
	if delivered["ops-1:pre:1"] != 0 {
		t.Errorf("the consumer was re-delivered the trimmed pre-outage event %d time(s); the flush must be bounded by the outage window", delivered["ops-1:pre:1"])
	}
	if delivered["ops-1:out:1"] != 1 {
		t.Errorf("the outage-window event reached the resuming consumer %d time(s); want exactly 1", delivered["ops-1:out:1"])
	}

	// A second recovery edge with nothing buffered since re-emits nothing.
	if n, err := svc.FlushBufferedToRedis(ctx); n != 0 || err != nil {
		t.Fatalf("a repeated recovery edge re-emitted %d event(s) (err %v); a flapping probe must not replay the ring", n, err)
	}
}

// inStream counts each eventKey currently retained on the stream.
func inStream(t *testing.T, cli redis.UniversalClient, key string) map[string]int {
	t.Helper()
	msgs, err := cli.XRange(context.Background(), key, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", key, err)
	}
	return countKeys(msgs)
}

// streamHead returns the newest stream ID, the position a consumer tailing the
// stream would resume after.
func streamHead(t *testing.T, cli redis.UniversalClient, key string) string {
	t.Helper()
	msgs, err := cli.XRevRangeN(context.Background(), key, "+", "-", 1).Result()
	if err != nil {
		t.Fatalf("XRevRange %s: %v", key, err)
	}
	if len(msgs) == 0 {
		return "0"
	}
	return msgs[0].ID
}

// afterPosition counts the eventKeys a consumer resuming from position receives.
func afterPosition(t *testing.T, cli redis.UniversalClient, key, position string) map[string]int {
	t.Helper()
	msgs, err := cli.XRange(context.Background(), key, "("+position, "+").Result()
	if err != nil {
		t.Fatalf("XRange %s from %s: %v", key, position, err)
	}
	return countKeys(msgs)
}

func countKeys(msgs []redis.XMessage) map[string]int {
	counts := map[string]int{}
	for _, m := range msgs {
		var ev gwevents.OperationalEvent
		if raw, ok := m.Values["event"].(string); ok {
			_ = json.Unmarshal([]byte(raw), &ev)
		}
		counts[ev.ID]++
	}
	return counts
}

// scanFaultingClient wraps a live Redis client and fails the recovery flush's
// retained-key XRANGE scan the configured number of times, so the flush's
// behaviour on a read error can be exercised against a real stream. Every
// other read passes through untouched.
type scanFaultingClient struct {
	opsstream.RedisStreamClient
	scanFailures atomic.Int64
}

func (c *scanFaultingClient) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	if start == "-" && stop == "+" && count == eventbuffer.DefaultStreamMaxLen && c.scanFailures.Add(-1) >= 0 {
		cmd := redis.NewXMessageSliceCmd(ctx)
		cmd.SetErr(fmt.Errorf("connection reset by peer"))
		return cmd
	}
	return c.RedisStreamClient.XRangeN(ctx, stream, start, stop, count)
}

// spec: 25.5 (best-effort recovery flush) — the down-to-up edge fires once per
// transition, so the flush must not consume the outage window before the read
// that can fail. A retained-key scan that fails on the recovery edge (a
// connection reset in the instant after the probe's PING succeeded, or a
// cluster failover) drops the deduplication optimization, not the window: the
// events this replica buffered during the outage still reach the recovered
// ops:events:stream, exactly once. The pre-fix flush took the window first and
// returned on the scan error, abandoning every buffered event permanently;
// this fails against that code.
//
// diagnosis: a failure means a transient Redis read error on the recovery edge
// permanently loses every lenny-ops-originated event buffered during the
// outage — the failure the recovery flush exists to prevent — or the flush
// duplicates an event on the stream when its scan does succeed.
func TestOpsEventStreamRecoveryFlushSurvivesRetainedKeyScanFailure(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	faulting := &scanFaultingClient{RedisStreamClient: opsstream.NewRedisStreamClient(rd.Client)}
	faulting.scanFailures.Store(1)

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{RedisClient: faulting, SourceHealth: health, ReplicaID: "ops-1"})

	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		ReplicaID: "ops-1",
	})
	svc.SetRedisReEmitter(emitter.Emit)

	// Redis goes away and this replica buffers two of its own events whose
	// XADD failed.
	var firstFailedID uint64
	for _, e := range []gwevents.OperationalEvent{
		{ID: "ops-1:scanfail:1", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1001, 0).UTC()},
		{ID: "ops-1:scanfail:2", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1002, 0).UTC()},
	} {
		id, err := svc.Publish(ctx, e)
		if err != nil {
			t.Fatalf("buffer local event %s: %v", e.ID, err)
		}
		if firstFailedID == 0 {
			firstFailedID = id
		}
	}
	svc.MarkRedisWriteFailure(firstFailedID)

	// The recovery edge fires once, and its retained-key scan fails.
	flushed, err := svc.FlushBufferedToRedis(ctx)
	if err == nil {
		t.Error("the failed retained-key scan must be reported to the caller")
	}
	if flushed != 2 {
		t.Fatalf("flush re-emitted %d events after a failed retained-key scan; want 2 (the outage window must not be abandoned)", flushed)
	}

	// A second edge with the scan working must not replay the window.
	if n, err := svc.FlushBufferedToRedis(ctx); n != 0 || err != nil {
		t.Fatalf("repeat flush = (%d, %v); want nothing, the window was already flushed", n, err)
	}

	msgs, err := rd.Client.XRange(ctx, eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	counts := map[string]int{}
	for _, m := range msgs {
		var ev gwevents.OperationalEvent
		if raw, ok := m.Values["event"].(string); ok {
			_ = json.Unmarshal([]byte(raw), &ev)
		}
		counts[ev.ID]++
	}
	if counts["ops-1:scanfail:1"] != 1 || counts["ops-1:scanfail:2"] != 1 {
		t.Fatalf("outage-window events did not reach the recovered stream exactly once: %v", counts)
	}
}

// pollOnce runs one §25.5 poll against svc and returns the decoded page.
func pollOnce(t *testing.T, svc *opsstream.Service, cursor string) opsstream.EventPage {
	t.Helper()
	url := "/v1/admin/events"
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	rec := httptest.NewRecorder()
	svc.HandlePoll(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s = %d: %s", url, rec.Code, rec.Body.String())
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode poll page: %v (%s)", err, rec.Body.String())
	}
	return page
}

// spec: 25.5 (cross-source cursor translation, best-effort recovery flush,
// exactly-once across the transition) — the recovery flush re-emits the events
// a replica buffered during a Redis outage with their original eventKeys, so
// they land at the tail of ops:events:stream carrying keys older than the
// gateway entries XADDed in the window between Redis becoming reachable and the
// flush running. Stream order and eventKey order disagree over that window, and
// a polling consumer must still advance: every event exactly once, with a cursor
// that never moves backwards. The pre-fix translation stopped its scan at the
// first entry ordering after the carried cursor, so a cursor minted from the
// flushed tail resolved back to the last pre-outage position and every
// subsequent poll replayed the whole retained window instead of advancing.
//
// diagnosis: a failure means a §25.5 polling consumer does not make forward
// progress across a Redis recovery — its cursor rewinds behind the flushed tail
// and it is re-delivered operational events it already consumed on every poll
// until the flushed entries are trimmed off the stream, or it never receives the
// flushed outage-window events at all.
func TestOpsEventStreamPollingAdvancesAcrossAnOutOfOrderRecoveryFlush(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:flushorder"

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		MaxLen:    1000,
		ReplicaID: "gw-1",
	})
	xadd := func(key string, at int64) {
		t.Helper()
		if err := emitter.Emit(ctx, gwevents.OperationalEvent{
			ID:          key,
			Type:        "dev.lenny.alert_fired",
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(at, 0).UTC(),
		}); err != nil {
			t.Fatalf("emit %s: %v", key, err)
		}
	}
	svc.SetRedisReEmitter(emitter.Emit)

	// Before the outage the stream holds two gateway events and the consumer
	// has polled to the head.
	xadd("gw-1:1000:1", 1000)
	xadd("gw-1:1001:1", 1001)
	first := pollOnce(t, svc, "")
	if len(first.Items) != 2 {
		t.Fatalf("pre-outage poll returned %d items; want the 2 seeded events", len(first.Items))
	}
	cursor := first.Pagination.Cursor

	// The outage: this replica keeps emitting its own events into the local
	// ring while their XADD fails.
	health.redis.Store(false)
	svc.MarkRedisOutage()
	for _, key := range []string{"ops-1:2000:1", "ops-1:2001:1"} {
		if _, err := svc.Publish(ctx, gwevents.OperationalEvent{
			ID:          key,
			Type:        "dev.lenny.escalation_created",
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(2000, 0).UTC(),
		}); err != nil {
			t.Fatalf("buffer outage event %s: %v", key, err)
		}
	}

	// Redis comes back. The gateway resumes XADDing immediately, while the
	// replica's flush waits on its next source-health probe, so these fresh
	// keys land ahead of the older flushed ones.
	health.redis.Store(true)
	xadd("gw-1:3000:1", 3000)
	xadd("gw-1:3001:1", 3001)

	// The flush now appends the outage-window events with their original keys,
	// so the retained window is no longer in eventKey order.
	if n, err := svc.FlushBufferedToRedis(ctx); err != nil || n != 2 {
		t.Fatalf("recovery flush = (%d, %v); want the 2 outage-window events", n, err)
	}

	// The consumer resumes from where it left off and must see each event once.
	seen := map[string]int{}
	for poll := 0; poll < 4; poll++ {
		page := pollOnce(t, svc, cursor)
		for _, item := range page.Items {
			seen[item.Event.ID]++
		}
		if page.Pagination.GapDetected {
			t.Fatalf("poll %d reported a spurious gap for a cursor inside the retained window: %s", poll, page.Pagination.GapReason)
		}
		if page.Pagination.Cursor != "" {
			cursor = page.Pagination.Cursor
		}
	}
	for _, key := range []string{"gw-1:3000:1", "gw-1:3001:1", "ops-1:2000:1", "ops-1:2001:1"} {
		if seen[key] != 1 {
			t.Errorf("post-recovery poller received %s %d time(s); want exactly 1 (%v)", key, seen[key], seen)
		}
	}
	for _, key := range []string{"gw-1:1000:1", "gw-1:1001:1"} {
		if seen[key] != 0 {
			t.Errorf("the poller was re-delivered the pre-outage event %s %d time(s); its cursor rewound behind the flushed tail", key, seen[key])
		}
	}
}
