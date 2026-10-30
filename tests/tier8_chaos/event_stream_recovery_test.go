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

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
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
		svc.HandleStream(rec, platformAdminReq(req))
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

// waitDegradationCount waits for the SSE stream to carry at least want
// :degradation comment lines, which is how a periodic re-emission is
// distinguished from a one-shot announcement on an idle degraded connection.
func waitDegradationCount(t *testing.T, rec *syncBuffer, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(rec.String(), ":degradation") >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("an idle connection received %d :degradation comments over several fall-back poll intervals, want at least %d; §25.5 embeds the envelope in a periodic comment line:\n%s",
		strings.Count(rec.String(), ":degradation"), want, rec.String())
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
// retained-key window scan the configured number of times, so the flush's
// behaviour on a read error can be exercised against a real stream. Every
// other read passes through untouched. The scan is head-anchored (XREVRANGE
// from "+" back to "-"), so that is the read this double faults.
type scanFaultingClient struct {
	opsstream.RedisStreamClient
	scanFailures atomic.Int64
}

func (c *scanFaultingClient) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	if start == "+" && stop == "-" && count == eventbuffer.DefaultStreamMaxLen && c.scanFailures.Add(-1) >= 0 {
		cmd := redis.NewXMessageSliceCmd(ctx)
		cmd.SetErr(fmt.Errorf("connection reset by peer"))
		return cmd
	}
	return c.RedisStreamClient.XRevRangeN(ctx, stream, start, stop, count)
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
	svc.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, url, nil)))
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

// spec: 25.5 (Redis-unavailable fallback — events continue to flow to the SSE
// client with the canonical degradation envelope embedded in a periodic
// :degradation comment line, and the switch back to the healthy source emits
// :degradation {"level":"healthy"}) — the degraded envelope repeats on the
// fall-back poll cadence for the life of the outage, so a connection that sits
// in a degraded stint while nothing is published keeps being told its view is
// degraded. The recovery announcement is a single edge event.
//
// diagnosis: a failure means the degradation envelope is announced once per
// transition rather than on the cadence, so a consumer that attaches mid-outage,
// or whose intermediary buffered or dropped the single announcement, never
// learns it is on the gateway-buffer fall-back and reads a truncated event flow
// as a healthy one; or the recovery comment is emitted more than once per
// recovery edge, which reads as repeated recoveries.
func TestOpsEventStreamRepeatsTheDegradationEnvelopeOnTheFallBackCadence(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A gateway replica that answers with an empty buffer window: the
	// connection has nothing to deliver for the whole outage.
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gwevents.BufferedEventPage{})
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
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: "ops:events:stream:degperiodic",
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	rec := newSyncBuffer()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, platformAdminReq(req))
	}()

	// The outage starts and nothing is published for its duration.
	health.redis.Store(false)
	waitContains(t, rec, sourceGatewayBuffer, 20*time.Second, "the degraded stint's first :degradation line")
	// Idle across several fall-back poll intervals: the envelope repeats on that
	// cadence even though nothing is published and the classification holds.
	waitDegradationCount(t, rec, 3, 20*time.Second)
	if got := strings.Count(rec.String(), "\"level\":\"healthy\""); got != 0 {
		t.Fatalf("the stream announced recovery %d times during the outage:\n%s", got, rec.String())
	}

	// Recovery: the healthy announcement is a single edge event, and the
	// degraded envelope stops.
	degradedLines := strings.Count(rec.String(), sourceGatewayBuffer)
	health.redis.Store(true)
	waitContains(t, rec, "\"level\":\"healthy\"", 10*time.Second, "the recovery announcement on switch back to Redis")
	time.Sleep(2 * gatewayFallbackPollInterval)
	if got := strings.Count(rec.String(), "\"level\":\"healthy\""); got != 1 {
		t.Errorf("recovery announced %d times for one recovery edge; want exactly 1:\n%s", got, rec.String())
	}
	if got := strings.Count(rec.String(), sourceGatewayBuffer); got != degradedLines {
		t.Errorf("the connection carried %d degraded :degradation lines after recovery (%d before); a healthy source carries none:\n%s", got-degradedLines, degradedLines, rec.String())
	}

	cancel()
	<-done
}

// sourceGatewayBuffer is the §25.5 actualSource the degradation envelope carries
// while a connection serves from the gateway-buffer fan-out during a Redis
// outage.
const sourceGatewayBuffer = "gateway-buffer"

// gatewayFallbackPollInterval mirrors the §25.5 SSE fall-back poll cadence, the
// interval the fan-out is re-polled on while Redis is down.
const gatewayFallbackPollInterval = 2 * time.Second

// spec: 25.5 (eventKey dedup across sources, exactly-once across the source
// switch) — an SSE connection held open across a Redis outage receives this
// replica's own events from the local ring while Redis is down. On recovery the
// best-effort flush re-emits exactly those events to ops:events:stream with
// their original eventKeys, so they reach the connection a second time on its
// live tail, arriving after entries carrying newer keys. The connection tracks
// what it has been written, so each event is delivered once however many sources
// hand it over. The pre-fix Redis stint wrote a frame for every entry its tail
// produced, so every connection open across a recovery received the whole outage
// window twice.
//
// diagnosis: a failure means an SSE consumer that holds a connection through a
// Redis outage is re-delivered every lenny-ops event of that outage when the
// recovery flush runs, so an agent acts on the same escalation or drift event
// twice; or the post-recovery event never arrives, meaning the switch back to
// the Redis tail dropped the live stream.
func TestOpsEventStreamOpenConnectionSeesFlushedEventsOnceAcrossRecovery(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const streamKey = "ops:events:stream:flushsse"

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(false)
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
	svc.SetRedisReEmitter(emitter.Emit)

	if err := emitter.Emit(ctx, gwevents.OperationalEvent{
		ID: "gw-1:1000:1", Type: "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed the stream: %v", err)
	}

	rec := newSyncBuffer()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, platformAdminReq(req))
	}()
	waitContains(t, rec, "id: gw-1:1000:1\n", 10*time.Second, "the Redis-served backlog event")

	// The outage: no gateway buffer is reachable either, so the connection
	// serves this replica's ring, which is where the events it emits now land.
	health.redis.Store(false)
	waitContains(t, rec, "lenny-ops-local-buffer", 10*time.Second, "the dual-outage degradation announcement")
	svc.MarkRedisOutage()
	outage := []string{"ops-1:2000:1", "ops-1:2001:1"}
	for _, key := range outage {
		if _, err := svc.Publish(ctx, gwevents.OperationalEvent{
			ID: key, Type: "dev.lenny.escalation_created",
			SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2000, 0).UTC(),
		}); err != nil {
			t.Fatalf("publish outage event %s: %v", key, err)
		}
		waitContains(t, rec, "id: "+key+"\n", 10*time.Second, "the outage-window event served from the local ring")
	}

	// Recovery: the connection returns to the Redis tail, the gateway resumes
	// XADDing, and only then does the flush re-emit the outage window.
	health.redis.Store(true)
	waitContains(t, rec, "\"level\":\"healthy\"", 10*time.Second, "the recovery announcement")
	if err := emitter.Emit(ctx, gwevents.OperationalEvent{
		ID: "gw-1:3000:1", Type: "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(3000, 0).UTC(),
	}); err != nil {
		t.Fatalf("emit the post-recovery event: %v", err)
	}
	waitContains(t, rec, "id: gw-1:3000:1\n", 10*time.Second, "the post-recovery event on the Redis tail")

	if n, err := svc.FlushBufferedToRedis(ctx); err != nil || n != len(outage) {
		t.Fatalf("recovery flush = (%d, %v); want the %d outage-window events", n, err, len(outage))
	}
	// Give the live tail time to hand the flushed entries to the connection.
	time.Sleep(3 * time.Second)

	cancel()
	<-done

	body := rec.String()
	for _, key := range append(append([]string{}, outage...), "gw-1:1000:1", "gw-1:3000:1") {
		if got := strings.Count(body, "id: "+key+"\n"); got != 1 {
			t.Errorf("the open connection received %s %d time(s); want exactly 1:\n%s", key, got, body)
		}
	}
}

// deadRedisClient returns a §25.5 read-side stream client whose every read
// fails immediately, standing in for a Redis that has become unreachable while
// the source-health probe still reports it up. It is built from the running
// container's connection options and then closed, so the failure reaches the
// read path through the same client the production wiring uses.
func deadRedisClient(t *testing.T, rd *containers.Redis) opsstream.RedisStreamClient {
	t.Helper()
	dead := redis.NewClient(rd.Client.Options())
	if err := dead.Close(); err != nil {
		t.Fatalf("close the stand-in Redis client: %v", err)
	}
	return opsstream.NewRedisStreamClient(dead)
}

// spec: 25.5 (degradation matrix — actualSource names the source the response
// was served from; EVENT_STREAM_UNAVAILABLE when both sources are unreachable)
// — the source-health probe refreshes on an interval, so a poll arriving after
// Redis fails and before the probe observes it still selects the Redis source.
// The response that poll's failed read produces must name the source that
// actually served it.
//
// diagnosis: a poll whose Redis read failed is served as a healthy, undegraded,
// empty page with the caller's cursor echoed, which is byte-indistinguishable
// from a healthy idle poll. A caller cannot tell "Redis is unreachable and you
// are missing events" from "no new events", and keeps polling as though its
// view were current.
func TestOpsEventStreamPollWithAFailedRedisReadIsNotReportedHealthy(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})

	// The probe is stale: it still reports Redis reachable.
	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)

	// A gateway replica answering the §25.3 buffer query, so the read has a
	// fall-back to be re-classified onto.
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gwevents.BufferedEventPage{Events: []gwevents.BufferedEvent{
			{ID: 1, Event: gwevents.OperationalEvent{ID: "gw:2000:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2000, 0).UTC()}},
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

	withFallback := opsstream.New(opsstream.Options{
		RedisClient:  deadRedisClient(t, rd),
		SourceHealth: health,
		ReplicaID:    "ops-1",
	})
	withFallback.SetGatewayBufferSource(gwClient)

	rec := httptest.NewRecorder()
	withFallback.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll with a gateway fall-back wired = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode poll page: %v (%s)", err, rec.Body.String())
	}
	if page.Degradation == nil {
		t.Fatalf("a poll whose Redis read failed was served with no degradation envelope: %s", rec.Body.String())
	}
	if page.Degradation.ActualSource != "gateway-buffer" {
		t.Errorf("degradation actualSource = %q, want gateway-buffer: the label must name the source that served the page", page.Degradation.ActualSource)
	}
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw:2000:1" {
		t.Errorf("fall-back page served %d item(s), want the one gateway-buffer event", len(page.Items))
	}

	// With no fall-back source wired, gateway-originated events have nowhere to
	// come from: the §25.5 dual-outage outcome rather than an empty 200.
	noFallback := opsstream.New(opsstream.Options{
		RedisClient:  deadRedisClient(t, rd),
		SourceHealth: health,
		ReplicaID:    "ops-1",
	})
	rec = httptest.NewRecorder()
	noFallback.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("poll with a failed Redis read and no fall-back = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "EVENT_STREAM_UNAVAILABLE") {
		t.Errorf("503 body = %s, want the EVENT_STREAM_UNAVAILABLE code", rec.Body.String())
	}
}

// spec: 25.5 (best-effort recovery flush) — the outage window is the only
// record of which locally buffered events still owe the shared stream a
// re-emit, so a flush whose re-emits fail must leave it open. Redis flapping
// back down inside the recovery edge is the ordinary way that happens: the
// probe reports up, the flush starts, and the XADDs fail.
//
// The flush's two triggers both report a transition rather than a level, so a
// window consumed ahead of a failed flush is never revisited: the next outage
// opens a fresh window at the current ring head, permanently excluding the
// earlier outage's events. The pre-fix flush took the window at the top and
// returned the re-emit error, so this fails against that code — the second
// flush re-emits nothing and the outage events never reach the stream.
//
// diagnosis: a failure means the §25.5 recovery flush discards the events it
// exists to protect. A flush that could not place them on the recovered stream
// dropped the record of which events were still owed, so a consumer that
// connects after the outage never observes them and no later edge can recover
// them.
func TestOpsEventStreamRecoveryFlushKeepsTheWindowOpenWhenReEmitsFail(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:  opsstream.NewRedisStreamClient(rd.Client),
		SourceHealth: health,
		ReplicaID:    "ops-1",
	})

	const key = "ops:events:stream:flushretry"
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: key,
		ReplicaID: "ops-1",
	})

	// The re-emit path fails for as long as Redis is still flapping, then
	// starts placing events once it settles.
	var reEmitDown atomic.Bool
	reEmitDown.Store(true)
	var attempts atomic.Int64
	svc.SetRedisReEmitter(func(ctx context.Context, ev gwevents.OperationalEvent) error {
		attempts.Add(1)
		if reEmitDown.Load() {
			return fmt.Errorf("XADD %s: connection refused", key)
		}
		return emitter.Emit(ctx, ev)
	})

	// Redis goes away and lenny-ops keeps emitting into the local ring.
	outage := []gwevents.OperationalEvent{
		{ID: "ops-1:flap:1", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2001, 0).UTC()},
		{ID: "ops-1:flap:2", Type: "dev.lenny.lock_changed", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2002, 0).UTC()},
	}
	var firstFailedID uint64
	for _, e := range outage {
		id, err := svc.Publish(ctx, e)
		if err != nil {
			t.Fatalf("buffer outage event %s: %v", e.ID, err)
		}
		if firstFailedID == 0 {
			firstFailedID = id
		}
	}
	svc.MarkRedisWriteFailure(firstFailedID)

	// The recovery edge fires while Redis is still flapping: every re-emit
	// fails.
	flushed, err := svc.FlushBufferedToRedis(ctx)
	if err == nil {
		t.Fatal("a flush whose every re-emit failed reported success")
	}
	if flushed != 0 {
		t.Fatalf("failed flush reported %d event(s) re-emitted, want 0", flushed)
	}
	if got := attempts.Load(); got != int64(len(outage)) {
		t.Fatalf("failed flush attempted %d re-emit(s), want %d (one per outage-window event)", got, len(outage))
	}

	// Redis settles. The next edge must find the window still open and place
	// the outage events on the stream.
	reEmitDown.Store(false)
	flushed, err = svc.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("retry flush after Redis settled: %v", err)
	}
	if flushed != len(outage) {
		t.Fatalf("retry flush re-emitted %d event(s), want %d; the failed flush consumed the outage window, so the events it named were abandoned", flushed, len(outage))
	}

	// Exactly once on the stream, and a further edge replays nothing.
	if n, err := svc.FlushBufferedToRedis(ctx); n != 0 || err != nil {
		t.Fatalf("third flush = (%d, %v); want nothing (the window was flushed successfully)", n, err)
	}
	counts := streamEventCounts(t, rd.Client, key)
	for _, e := range outage {
		if counts[e.ID] != 1 {
			t.Errorf("outage event %s is on the stream %d time(s), want exactly 1: %v", e.ID, counts[e.ID], counts)
		}
	}
}

// streamEventCounts decodes every entry on key and counts the eventKeys it
// carries, so a test can assert a flushed event landed exactly once.
func streamEventCounts(t *testing.T, client redis.UniversalClient, key string) map[string]int {
	t.Helper()
	msgs, err := client.XRange(context.Background(), key, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", key, err)
	}
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

// opsBufferReplicaServer serves GET /v1/admin/events/buffer through the genuine
// gateway admin Router over buf, so a chaos run meets the endpoint's real
// pagination (an absent ?limit= defaults to 100, any limit is capped at the
// ring capacity) rather than a stub that ignores the query. The attached
// principal is the §25.4 lenny-ops service account holding platform-admin.
func opsBufferReplicaServer(t *testing.T, buf *eventbuffer.EventBuffer) *httptest.Server {
	t.Helper()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithEventBuffer(buf)
	handler := router.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := authmw.Principal{
			Subject:  "system:serviceaccount:lenny-system:lenny-ops-sa",
			TenantID: "platform",
			Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		}
		handler.ServeHTTP(w, r.WithContext(authmw.WithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gwBufferEvent builds a gateway-originated alert whose eventKey carries the
// emission second, so the §25.5 eventKey ordering matches the emission order.
func gwBufferEvent(sec int64, nonce int) gwevents.OperationalEvent {
	return gwevents.OperationalEvent{
		ID:          fmt.Sprintf("gw:%d:%d", sec, nonce),
		Type:        "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Time:        time.Unix(sec, 0).UTC(),
	}
}

// spec: 25.5 (transparent Redis to gateway-buffer switch; no drop across the
// switch; gapDetected only when the cursor aged out of every replica's ring) —
// an open SSE connection injected with a live Redis outage, against a gateway
// replica whose ring retains more than the §25.3 endpoint's default page, must
// resume at its carried position without a spurious gap and must go on
// receiving events the gateway emits AFTER the switch. The pre-fix fall-back
// fetched the bare buffer path, so the replica answered from since=0 at its
// 100-event default and returned the OLDEST slice of the ring: the carried
// position ordered after that whole stale window (a spurious :gap) and every
// event emitted during the outage landed at the ring head, outside it, so the
// connection received nothing for the duration. This fails against that code.
//
// diagnosis: a failure means the §25.5 Redis-down fall-back serves a stale
// window rather than the retained one — an open connection is told to resync on
// an ordinary source switch, and the gateway events emitted during the outage
// (the events the fall-back exists to carry) never reach it.
func TestOpsEventStreamFallbackDeliversGatewayEventsEmittedAfterTheSwitch(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The replica ring already retains 300 events, three times the endpoint's
	// default page, so the head is only reachable with an explicit ?limit=.
	buf := eventbuffer.NewEventBuffer(eventbuffer.DefaultBufferCapacity)
	for i := 1; i <= 300; i++ {
		buf.Append(gwBufferEvent(int64(1000+i), i))
	}
	replica := opsBufferReplicaServer(t, buf)

	gwClient, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("t"),
		Discovery:         gateway.StaticDiscovery{replica.URL},
		PerRequestTimeout: 3 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}

	health := &toggleHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:  opsstream.NewRedisStreamClient(rd.Client),
		SourceHealth: health,
		ReplicaID:    "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	// A Redis-served event whose eventKey orders inside the retained gateway
	// window, so the position the connection carries into the fall-back is one
	// the retained window can honour.
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		ReplicaID: "ops-1",
	})
	carried := gwevents.OperationalEvent{
		ID:          "ops-1:1200:1",
		Type:        "dev.lenny.drift_detected",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Time:        time.Unix(1200, 0).UTC(),
	}
	if err := emitter.Emit(ctx, carried); err != nil {
		t.Fatalf("seed redis event: %v", err)
	}

	rec := newSyncBuffer()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, platformAdminReq(req))
	}()
	waitContains(t, rec, "ops-1:1200:1", 5*time.Second, "the Redis-served backlog event")

	// Inject the outage and let the connection settle into the fall-back.
	health.redis.Store(false)
	waitContains(t, rec, "gateway-buffer", 6*time.Second, "the gateway-buffer degradation announcement")
	waitContains(t, rec, "gw:1300:300", 6*time.Second, "the newest event the replica ring already retained")

	// The gateway keeps emitting while Redis is down. The new event lands at
	// the ring head, which is exactly what the stale oldest-page window misses.
	after := gwBufferEvent(9000, 1)
	buf.Append(after)
	waitContains(t, rec, after.ID, 8*time.Second, "a gateway event emitted after the switch to the fall-back")

	cancel()
	<-done

	if strings.Contains(rec.String(), ":gap") {
		t.Fatalf("an ordinary switch into the gateway-buffer fall-back emitted a :gap comment; the carried position sits inside the retained window:\n%s", rec.String())
	}
}
