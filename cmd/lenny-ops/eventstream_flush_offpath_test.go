// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// spec: 25.5 (the best-effort recovery flush is a replica-level property
// independent of any open read connection, and a re-emit failure does not block
// serving) — the flush runs on a worker of its own. The write path's
// failure-to-success edge reports the recovery and returns; it does not scan the
// retained window or re-emit the outage buffer on the emitting goroutine.
//
// diagnosis: a failure means Emit runs the recovery flush inline. Emit is the
// EventEmitter every lenny-ops subsystem holds (escalation, drift,
// remediation-lock, platform-upgrade, ops self-health), so the first successful
// XADD after a Redis blip blocks a controller reconcile for as long as a
// full-window scan plus one XADD per buffered outage event takes, up to the
// flush's own timeout.
func TestRecoveryFlushDoesNotRunOnTheEmitGoroutine(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	local := opsstream.New(opsstream.Options{ReplicaID: "ops-1", RedisClient: opsstream.NewRedisStreamClient(client)})
	em := newRedisFanOutEmitter(client, local, "ops-1", eventbuffer.DefaultStreamMaxLen)

	// Each re-emit is slow, so a flush over the outage window takes far longer
	// than any single emit may. An inline flush shows up as an Emit that took
	// the whole window.
	const (
		outageEvents  = 20
		perReEmitCost = 25 * time.Millisecond
	)
	var (
		mu        sync.Mutex
		reEmitted []string
	)
	local.SetRedisReEmitter(func(ctx context.Context, ev events.OperationalEvent) error {
		time.Sleep(perReEmitCost)
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
	go w.runEventStreamFlushWorker(runCtx)
	em.onWriteRecovered = w.requestEventStreamFlush

	// The outage: every XADD fails, so each event lands in the local ring alone
	// and the recovery-flush window covers all of them.
	mr.SetError("LOADING Redis is loading the dataset in memory")
	for i := 0; i < outageEvents; i++ {
		if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.escalation_created"}); err == nil {
			t.Fatalf("emit %d during the outage must surface the failed XADD", i)
		}
	}

	// Redis comes back. The first successful XADD is the write path's recovery
	// edge, and it must report that edge rather than serve the flush.
	mr.SetError("")
	started := time.Now()
	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.drift_detected"}); err != nil {
		t.Fatalf("emit after the outage: %v", err)
	}
	elapsed := time.Since(started)

	// The flush over the window costs at least outageEvents*perReEmitCost. An
	// Emit that returns inside a small fraction of that did not run it.
	flushCost := outageEvents * perReEmitCost
	if elapsed > flushCost/4 {
		t.Errorf("the recovery-edge Emit took %s against a pending flush window costing at least %s; the flush ran on the emitting goroutine", elapsed, flushCost)
	}

	// The flush still happens, off the emit path, and covers the window once.
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n := len(reEmitted)
		mu.Unlock()
		if n >= outageEvents {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the recovery flush re-emitted %d of %d buffered events; reporting the edge must not drop the flush", n, outageEvents)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Settle, then confirm no event was re-emitted twice by the two edges.
	time.Sleep(2 * perReEmitCost)
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, key := range reEmitted {
		counts[key]++
	}
	for key, n := range counts {
		if n != 1 {
			t.Errorf("event %s was re-emitted %d times; one outage window flushes once however many edges reported it", key, n)
		}
	}
	if len(counts) != outageEvents {
		t.Errorf("the flush re-emitted %d distinct events, want the %d buffered during the outage", len(counts), outageEvents)
	}
}
