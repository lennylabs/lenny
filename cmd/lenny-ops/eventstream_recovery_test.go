// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"testing"

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

	local := opsstream.New(opsstream.Options{ReplicaID: "ops-1", RedisClient: client})
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
