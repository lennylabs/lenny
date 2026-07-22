// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §25.5 best-effort recovery flush racing a
// concurrent emit that succeeds against the recovered Redis.
//
// Two independent triggers invoke the flush (the source-health probe's
// down-to-up edge and the first XADD that succeeds after one failed), so an
// ordinary emit runs alongside it. The flush must not put an event that already
// reached ops:events:stream back on the stream: the webhook worker pages the
// stream by position with no eventKey dedup of its own, so a duplicate entry is
// a duplicate webhook delivery.
//
// spec: §25.5 (best-effort recovery flush, eventKey dedup).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// gatedStreamClient holds the first retained-window scan the recovery flush
// issues open after Redis has answered it, so a test can land a concurrent emit
// inside the flush at a controlled point. The flush reads its retained window
// via XREVRANGE (redisSource.retainedWindow), so the gate is on XRevRangeN;
// everything else forwards straight through.
type gatedStreamClient struct {
	opsstream.RedisStreamClient
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newGatedStreamClient(inner opsstream.RedisStreamClient) *gatedStreamClient {
	return &gatedStreamClient{
		RedisStreamClient: inner,
		entered:           make(chan struct{}),
		release:           make(chan struct{}),
	}
}

func (g *gatedStreamClient) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := g.RedisStreamClient.XRevRangeN(ctx, stream, start, stop, count)
	first := false
	g.once.Do(func() { first = true })
	if !first {
		return cmd
	}
	// Redis has already answered, so the result the flush is about to consume
	// predates whatever the concurrent emit writes while this call is parked.
	close(g.entered)
	<-g.release
	return cmd
}

// TestOpsEventStreamRecoveryFlushDoesNotDuplicateConcurrentEmit runs the §25.5
// recovery flush against a real Redis while an ordinary emit succeeds
// underneath it, and asserts every eventKey appears exactly once on
// ops:events:stream. The concurrent emit lands after the flush has read the
// stream's retained keys, so a flush that snapshots those keys before it takes
// the outage window sees the event as absent from the stream, finds it inside
// the window it then queries, and re-emits it.
//
// diagnosis: a failure means the §25.5 recovery flush re-emits an event that
// already reached ops:events:stream. The webhook worker pages the stream by
// position and does not deduplicate by eventKey, so the duplicate entry is
// delivered to every matching subscription a second time.
func TestOpsEventStreamRecoveryFlushDoesNotDuplicateConcurrentEmit(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const runs = 5
	for run := 0; run < runs; run++ {
		streamKey := fmt.Sprintf("ops:events:stream:flushdedup:%d", run)

		gated := newGatedStreamClient(opsstream.NewRedisStreamClient(rd.Client))
		svc := opsstream.New(opsstream.Options{
			RedisClient:    gated,
			RedisStreamKey: streamKey,
			ReplicaID:      "ops-1",
		})
		emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			Client:    rd.Client,
			Buffer:    eventbuffer.NewEventBuffer(0),
			StreamKey: streamKey,
			MaxLen:    1000,
			ReplicaID: "ops-1",
		})
		svc.SetRedisReEmitter(emitter.Emit)

		// Two events whose XADD failed during the outage: they are in the local
		// ring only, and the flush exists to carry them to the stream.
		var firstFailed uint64
		for i := 1; i <= 2; i++ {
			ev := gwevents.OperationalEvent{
				ID:          fmt.Sprintf("ops-1:%d00%d:1", run, i),
				Type:        "dev.lenny.escalation_created",
				SpecVersion: gwevents.CloudEventsSpecVersion,
				Time:        time.Unix(int64(1000+i), 0).UTC(),
			}
			id, err := svc.Publish(ctx, ev)
			if err != nil {
				t.Fatalf("run %d: buffer outage event %s: %v", run, ev.ID, err)
			}
			if firstFailed == 0 {
				firstFailed = id
			}
		}
		svc.MarkRedisWriteFailure(firstFailed)

		// Redis recovers and the replica-level edge fires the flush.
		type flushResult struct {
			n   int
			err error
		}
		done := make(chan flushResult, 1)
		go func() {
			n, err := svc.FlushBufferedToRedis(ctx)
			done <- flushResult{n: n, err: err}
		}()

		// An ordinary emit succeeds against the recovered Redis while the flush
		// is in flight: it publishes into the local ring and XADDs, exactly as
		// the fan-out emitter does.
		<-gated.entered
		live := gwevents.OperationalEvent{
			ID:          fmt.Sprintf("ops-1:%d999:1", run),
			Type:        "dev.lenny.escalation_created",
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(2000, 0).UTC(),
		}
		buffered, err := svc.PublishBuffered(ctx, live)
		if err != nil {
			t.Fatalf("run %d: publish concurrent event: %v", run, err)
		}
		if err := emitter.Emit(ctx, buffered.Event); err != nil {
			t.Fatalf("run %d: emit concurrent event: %v", run, err)
		}
		close(gated.release)

		res := <-done
		if res.err != nil {
			t.Fatalf("run %d: recovery flush: %v", run, res.err)
		}

		counts := streamKeyCounts(t, rd.Client, streamKey)
		for key, n := range counts {
			if n != 1 {
				t.Fatalf("run %d: eventKey %s is on %s %d times, want exactly 1: the recovery flush re-emitted an event that already reached the stream (counts %v)", run, key, streamKey, n, counts)
			}
		}
		for i := 1; i <= 2; i++ {
			key := fmt.Sprintf("ops-1:%d00%d:1", run, i)
			if counts[key] != 1 {
				t.Fatalf("run %d: outage-window event %s never reached the stream (counts %v)", run, key, counts)
			}
		}
		if counts[live.ID] != 1 {
			t.Fatalf("run %d: the concurrently emitted event %s is on the stream %d times, want exactly 1 (counts %v)", run, live.ID, counts[live.ID], counts)
		}
	}
}

// streamKeyCounts returns how many entries the stream holds per eventKey.
func streamKeyCounts(t *testing.T, client redis.UniversalClient, streamKey string) map[string]int {
	t.Helper()
	msgs, err := client.XRange(context.Background(), streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", streamKey, err)
	}
	counts := map[string]int{}
	for _, m := range msgs {
		var ev gwevents.OperationalEvent
		raw, ok := m.Values["event"].(string)
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			continue
		}
		counts[ev.ID]++
	}
	return counts
}
