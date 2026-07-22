// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the two properties the §25.5 live SSE tail
// rests on: an event XADDed while every connection sits in its XREAD BLOCK 0
// reaches all of them promptly, and every tail goroutine exits once its
// connection is cancelled.
//
// go-redis does not interrupt a deadline-free blocked read on context
// cancellation, so each tail owns a client of its own and closes it when the
// connection ends. That close is what ends the blocked read; a tail that
// relied on the context alone would leak a goroutine per disconnect, which is
// what the goroutine-drain assertion here catches.
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

// tailDeliveryBudget is the ceiling on how long an event XADDed into a set of
// blocked tails may take to reach every connection. A blocking read wakes on
// the XADD, so the real figure is milliseconds; the budget leaves room for a
// loaded machine while staying far below any poll cadence.
const tailDeliveryBudget = 2 * time.Second

// tailDrainBudget is how long every cancelled connection's tail goroutine has
// to exit. Closing the tail's own client ends its blocked read at once, so the
// budget only covers scheduling.
const tailDrainBudget = 10 * time.Second

// TestOpsEventStreamTailDeliversPromptlyAndExitsOnCancel asserts an event
// XADDed while every connection sits in its blocked XREAD reaches all of them
// promptly, and that every tail goroutine exits once the connections are
// cancelled.
//
// diagnosis: a slow delivery means the live tail is polling rather than
// blocking inside Redis, so §25.5 SSE delivery latency has become the poll
// cadence; goroutines that never drain mean a disconnected connection leaks
// its tail, because the tail is not closing the client its blocked read runs
// on.
func TestOpsEventStreamTailDeliversPromptlyAndExitsOnCancel(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:tailblock"
	const connections = 8

	health := &switchHealth{}
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
	if latency := time.Since(start); latency > tailDeliveryBudget {
		t.Fatalf("event XADDed into a blocked tail took %s to reach every connection; want under %s (the tail must wake on the XADD)", latency, tailDeliveryBudget)
	}

	for _, r := range readers {
		r.close()
	}

	// Every cancelled connection's tail goroutine must exit. Closing the tail's
	// own client ends the blocked read at once; allow a margin for scheduling.
	deadline := time.Now().Add(tailDrainBudget)
	for {
		if runtime.NumGoroutine() <= baseline+connections/2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines after cancelling %d SSE connections: %d; want back near the %d baseline (a tail that does not close its own client outlives its connection and leaks a goroutine per disconnect)", connections, runtime.NumGoroutine(), baseline)
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
