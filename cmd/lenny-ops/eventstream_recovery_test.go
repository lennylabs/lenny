// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// streamedEventKeys returns how many times each CloudEvents id appears on the
// ops:events:stream.
func streamedEventKeys(t *testing.T, client redis.UniversalClient) map[string]int {
	t.Helper()
	entries, err := client.XRange(context.Background(), eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	counts := map[string]int{}
	for _, e := range entries {
		raw, ok := e.Values["event"].(string)
		if !ok {
			t.Fatalf("stream entry missing event field: %+v", e.Values)
		}
		var ev events.OperationalEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("decode stream event: %v", err)
		}
		counts[ev.ID]++
	}
	return counts
}

// spec: 25.5 (best-effort recovery flush) — the source-health probe observes a
// Redis outage up to one refresh interval after it starts, so the events
// lenny-ops emits inside that detection lag are the ones whose XADD already
// failed. The recovery flush must re-emit them, which requires the outage
// window to open at the observed write failure rather than at the probe's later
// observation. This drives an outage with no probe signal at all: only the
// failed XADDs open the window, and every event emitted during the outage must
// reach the recovered stream exactly once.
func TestRecoveryFlushReEmitsEventsBufferedBeforeTheProbeObservesTheOutage(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	local := opsstream.New(opsstream.Options{ReplicaID: "ops-1", RedisClient: opsstream.NewRedisStreamClient(client)})
	em := newRedisFanOutEmitter(client, local, "ops-1", eventbuffer.DefaultStreamMaxLen)
	local.SetRedisReEmitter(em.ReEmit)
	ctx := context.Background()

	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.drift_detected"}); err != nil {
		t.Fatalf("pre-outage Emit: %v", err)
	}

	// Redis goes away. No probe runs, so nothing calls MarkRedisOutage: the
	// only signal is the failing XADD inside each Emit.
	mr.SetError("LOADING Redis is loading the dataset in memory")
	for i := 0; i < 2; i++ {
		if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.escalation_created"}); err == nil {
			t.Fatal("Emit during the outage must surface the failed XADD")
		}
	}
	mr.SetError("")

	flushed, err := local.FlushBufferedToRedis(ctx)
	if err != nil {
		t.Fatalf("recovery flush: %v", err)
	}
	if flushed != 2 {
		t.Fatalf("flush re-emitted %d events; want 2 (both events emitted inside the detection lag)", flushed)
	}

	counts := streamedEventKeys(t, client)
	page := local.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 3 {
		t.Fatalf("local ring holds %d events; want 3", len(page.Events))
	}
	for _, ev := range page.Events {
		if counts[ev.Event.ID] != 1 {
			t.Errorf("event %s is on the recovered stream %d times; want exactly 1", ev.Event.ID, counts[ev.Event.ID])
		}
	}
}

// spec: 25.5 (canonical eventKey across sources) — one emitted event carries
// one eventKey on both the local ring and the shared stream, which is what lets
// the recovery flush's retained-key check and every consumer's own dedup
// collapse the two copies. A second minting on the stream side would put the
// same event on the stream twice under two keys after a recovery flush.
func TestFanOutEmitterWritesOneEventKeyToBothTheRingAndTheStream(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	local := opsstream.New(opsstream.Options{ReplicaID: "ops-1"})
	em := newRedisFanOutEmitter(client, local, "ops-1", eventbuffer.DefaultStreamMaxLen)
	if err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.drift_detected"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	page := local.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("local ring holds %d events; want 1", len(page.Events))
	}
	ringKey := page.Events[0].Event.ID
	counts := streamedEventKeys(t, client)
	if counts[ringKey] != 1 {
		t.Errorf("stream carries the ring event key %q %d times; want 1 (keys on the stream: %v)", ringKey, counts[ringKey], counts)
	}
}

// spec: 25.5 (best-effort recovery flush) — the recovery-flush outage window is
// opened by two signals and the source-health probe observes only one of them.
// A failed XADD opens it the instant the write fails, so a Redis interruption
// shorter than one probe refresh interval leaves a window open with no observed
// down edge behind it. The write path carries its own up edge for that case:
// the first XADD that succeeds after one failed drives the flush. Otherwise the
// events whose XADD failed are abandoned, and the stale window survives into
// the next observed outage and widens that flush back over already-delivered
// history.
//
// This drives the probe loop through an unobserved interruption and then a
// probe-observed outage, and asserts each buffered event reaches the recovered
// stream exactly once and no event is re-emitted by two different flushes.
func TestRecoveryFlushClosesAWindowTheProbeNeverObserved(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	local := opsstream.New(opsstream.Options{ReplicaID: "ops-1", RedisClient: opsstream.NewRedisStreamClient(client)})
	em := newRedisFanOutEmitter(client, local, "ops-1", eventbuffer.DefaultStreamMaxLen)

	// A counting re-emitter records what each flush put back on the stream, so
	// a flush widened by a stale window is visible as an event re-emitted twice.
	var mu sync.Mutex
	var reEmitted []string
	local.SetRedisReEmitter(func(ctx context.Context, ev events.OperationalEvent) error {
		if err := em.ReEmit(ctx, ev); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		reEmitted = append(reEmitted, ev.ID)
		return nil
	})
	ctx := context.Background()

	w := &opsWiring{opsWiringFields: opsWiringFields{
		eventStream:   local,
		flushRequests: make(chan struct{}, eventStreamFlushSignals),
	}}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Both edges report to the flush worker, which is the only caller of the
	// flush, so neither the probe loop nor an emitting subsystem runs it.
	go w.runEventStreamFlushWorker(runCtx)
	// The write path's own recovery edge, the half the probe cannot observe.
	em.onWriteRecovered = w.requestEventStreamFlush
	p := newSourceHealthProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(runCtx, 20*time.Millisecond, client, nil, redisEdgeCallbacks{
			onRedisDown:      w.openEventStreamOutageWindow,
			onRedisRecovered: func(context.Context) { w.requestEventStreamFlush() },
		})
	}()

	// The interruption: the XADD fails and opens the window, and Redis is back
	// before any refresh could observe it going away.
	mr.SetError("LOADING Redis is loading the dataset in memory")
	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.escalation_created"}); err == nil {
		t.Fatal("Emit during the interruption must surface the failed XADD")
	}
	mr.SetError("")
	first := lastRingKey(t, local)
	// The next successful XADD is this replica's own observation of Redis
	// coming back, and it is what closes the window the interruption opened.
	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.drift_detected"}); err != nil {
		t.Fatalf("Emit after the interruption: %v", err)
	}
	awaitStreamed(t, client, first, "the event whose XADD failed during an interruption the probe never observed")

	// A second outage, this one long enough for the probe to observe the down
	// edge, buffering one further event.
	mr.SetError("LOADING Redis is loading the dataset in memory")
	deadline := time.Now().Add(10 * time.Second)
	for p.RedisAvailable() {
		if time.Now().After(deadline) {
			t.Fatal("the probe never observed the second outage")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.drift_detected"}); err == nil {
		t.Fatal("Emit during the second outage must surface the failed XADD")
	}
	mr.SetError("")
	second := lastRingKey(t, local)
	awaitStreamed(t, client, second, "the event buffered during the probe-observed outage")

	// Let further refreshes run: the windows are consumed, so they re-emit
	// nothing more.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	got := append([]string{}, reEmitted...)
	mu.Unlock()
	want := []string{first, second}
	if len(got) != len(want) {
		t.Fatalf("the flushes re-emitted %v; want each buffered event once, in outage order %v "+
			"(a repeat means a window left open by the unobserved interruption widened a later flush)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the flushes re-emitted %v; want %v", got, want)
		}
	}
	counts := streamedEventKeys(t, client)
	for _, key := range want {
		if counts[key] != 1 {
			t.Errorf("event %s is on the recovered stream %d times; want exactly 1", key, counts[key])
		}
	}
}

// lastRingKey returns the eventKey of the newest event in the local ring.
func lastRingKey(t *testing.T, s *opsstream.Service) string {
	t.Helper()
	page := s.Query(0, events.EventFilter{}, 100)
	if len(page.Events) == 0 {
		t.Fatal("the local ring is empty")
	}
	return page.Events[len(page.Events)-1].Event.ID
}

// awaitStreamed waits until key appears exactly once on the ops:events:stream.
func awaitStreamed(t *testing.T, client redis.UniversalClient, key, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if streamedEventKeys(t, client)[key] == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached the recovered stream; its outage window was left open or narrowed "+
				"(keys on the stream: %v)", what, streamedEventKeys(t, client))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// spec: 25.5 (best-effort recovery flush) — the flush fires on the Redis
// down-to-up edge the source-health loop observes, rather than on every refresh
// that finds Redis reachable. A probe whose refreshes all find Redis up has
// observed no outage and must do no flush work at all, and one observed outage
// must produce exactly one flush however many reachable refreshes follow it.
//
// Nothing else pins this gate. The outage window inside the event stream is a
// second guard in a different component, so a flush offered on every refresh
// looks correct until the window is weakened or the flush gains a pre-window
// side effect (a Redis round trip, a metric, a log), at which point it silently
// becomes a per-refresh operation on every replica.
func TestSourceHealthProbeFlushesOnTheRecoveryEdgeOnly_spec_25_5(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	var downs, recoveries atomic.Int64
	p := newSourceHealthProbe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx, 5*time.Millisecond, client, nil, redisEdgeCallbacks{
			onRedisDown:      func() { downs.Add(1) },
			onRedisRecovered: func(context.Context) { recoveries.Add(1) },
		})
	}()

	// Many refreshes with Redis reachable throughout, and no outage anywhere in
	// the history: the flush must never be offered.
	time.Sleep(200 * time.Millisecond)
	if got := recoveries.Load(); got != 0 {
		t.Fatalf("a probe that never observed an outage offered the recovery flush %d time(s) over ~40 reachable refreshes; want 0", got)
	}

	// One observed outage, then many further reachable refreshes.
	mr.SetError("LOADING Redis is loading the dataset in memory")
	awaitCount(t, &downs, 1, "the probe to observe the outage")
	mr.SetError("")
	awaitCount(t, &recoveries, 1, "the probe to flush on the recovery edge")
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if got := recoveries.Load(); got != 1 {
		t.Errorf("one observed outage produced %d flush offers; want exactly 1 (the down-to-up edge)", got)
	}
	if got := downs.Load(); got != 1 {
		t.Errorf("one observed outage produced %d down edges; want exactly 1", got)
	}
}

// awaitCount waits until c reaches want.
func awaitCount(t *testing.T, c *atomic.Int64, want int64, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for c.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (count=%d, want %d)", what, c.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
