// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the two properties the §25.5 live SSE tail's
// bounded XREAD BLOCK rests on.
//
// The spec states the per-connection live tail as XREAD BLOCK 0. The shipped
// tail issues a bounded block instead, because go-redis does not interrupt an
// in-flight deadline-free blocked read on context cancellation, so a literal
// BLOCK 0 parks a goroutine per disconnected connection forever. The bound is
// only sound while it preserves the delivery semantics the spec line is about:
// the tail sleeps inside Redis and wakes on the XADD, so an event is delivered
// promptly rather than at the end of the block interval. This pins both halves,
// so raising the interval into poll territory, or returning to a block a
// cancelled connection cannot escape, fails here rather than degrading
// delivery silently.
//
// spec: §25.5 (per-connection live tail, independent read cursor).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// tailBlockInterval is the block this test configures the tail with. It is long
// enough that a delivery arriving inside it cannot have come from the block
// elapsing, and short enough that a cancelled tail exits well within the
// goroutine-drain deadline below.
const tailBlockInterval = 4 * time.Second

// TestOpsEventStreamTailDeliversInsideTheBlockAndExitsOnCancel asserts an event
// XADDed while every connection sits in its blocked XREAD is delivered far
// inside one block interval, and that every tail goroutine exits once the
// connections are cancelled.
//
// diagnosis: a slow delivery means the live tail is polling rather than
// blocking inside Redis, so §25.5 SSE delivery latency has become the block
// interval; goroutines that never drain mean a disconnected connection leaks
// its tail, which is the failure the bounded block exists to prevent.
func TestOpsEventStreamTailDeliversInsideTheBlockAndExitsOnCancel(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:tailblock"
	const connections = 8

	health := &switchHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
		TailBlock:      tailBlockInterval,
	})

	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		MaxLen:    1000,
		ReplicaID: "gw-1",
	})
	emit := func(key string, at time.Time) {
		t.Helper()
		if err := emitter.Emit(ctx, gwevents.OperationalEvent{
			ID:              key,
			Type:            gwevents.EventType("alert_fired").CloudEventsType(),
			Subject:         "pool/" + key,
			Severity:        "warning",
			SpecVersion:     gwevents.CloudEventsSpecVersion,
			Time:            at.UTC(),
			DataContentType: gwevents.ContentTypeJSON,
			Data:            json.RawMessage(`{"alert":"x"}`),
		}); err != nil {
			t.Fatalf("emit %s: %v", key, err)
		}
	}

	baseline := stableGoroutines()

	emit("gw-1:1000:1", time.Unix(1000, 0))
	readers := make([]*sseReader, connections)
	for i := range readers {
		readers[i] = openSSEReader(svc)
	}
	awaitAll(t, readers, 1, "the Redis-served backlog")

	// Every connection is now parked in its blocked XREAD. An entry XADDed here
	// must wake the block rather than wait it out.
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	emit("gw-1:2000:1", time.Unix(2000, 0))
	awaitAll(t, readers, 2, "the event XADDed while the tail was blocked")
	if latency := time.Since(start); latency > tailBlockInterval/2 {
		t.Fatalf("event XADDed into a blocked tail took %s to reach every connection; want well inside the %s block interval (the tail must wake on the XADD, not on the block elapsing)", latency, tailBlockInterval)
	}

	for _, r := range readers {
		r.close()
	}

	// Every cancelled connection's tail goroutine must exit. The bounded block
	// caps that at one interval; allow a margin for scheduling.
	deadline := time.Now().Add(tailBlockInterval * 3)
	for {
		if runtime.NumGoroutine() <= baseline+connections/2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines after cancelling %d SSE connections: %d; want back near the %d baseline (a tail that outlives its connection leaks a goroutine per disconnect)", connections, runtime.NumGoroutine(), baseline)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stableGoroutines returns a goroutine count taken after the runtime has
// settled, so container startup and client pool goroutines are counted in the
// baseline rather than read as a leak.
func stableGoroutines() int {
	for i := 0; i < 5; i++ {
		runtime.Gosched()
		time.Sleep(100 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}
