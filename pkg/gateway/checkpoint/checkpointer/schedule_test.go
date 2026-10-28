// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// scheduleCountingAdapter counts the checkpoint streams the gateway
// opens and answers each one like fakeCheckpointAdapter.
type scheduleCountingAdapter struct {
	fakeCheckpointAdapter
	n *atomic.Int32
}

func (f scheduleCountingAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	f.n.Add(1)
	return f.fakeCheckpointAdapter.Checkpoint(stream)
}

// fakeClock is an injectable Checkpointer.Now.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// spec: 4.4 (Event / Checkpoint Store) — the **Jitter:** rule of periodic
// checkpoint scheduling: the first periodic checkpoint of a session fires
// periodicCheckpointIntervalSeconds plus a jitter of at most
// periodicCheckpointJitterFraction of the interval after session start,
// and every later one fires at the fixed interval.
func TestSweepDueDelaysFirstPeriodicCheckpoint_spec_4_4(t *testing.T) {
	const interval = 600 * time.Second
	const sessionID = "acme-jitter-session"
	var calls atomic.Int32
	registry := podsession.NewRegistry()
	store := memstore.New()
	registry.Put(&podsession.BindResult{
		SessionID: sessionID, TenantID: "acme",
		Adapter: dialAdapter(t, scheduleCountingAdapter{n: &calls}),
	})
	runningSession(t, store, "acme", sessionID)

	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	cp := &checkpointer.Checkpointer{
		Sessions: store, Registry: registry, Interval: interval,
		JitterFraction: checkpointer.DefaultJitterFraction, Now: clock.Now,
	}
	ctx := context.Background()
	firstDue := start.Add(checkpointer.FirstCheckpointDelay(sessionID, interval, checkpointer.DefaultJitterFraction))

	cp.SweepDue(ctx) // observes the session at start.
	clock.Set(start.Add(interval - time.Second))
	cp.SweepDue(ctx)
	clock.Set(firstDue.Add(-time.Second))
	cp.SweepDue(ctx)
	if got := calls.Load(); got != 0 {
		t.Fatalf("checkpoint ran %d times before the jittered first instant", got)
	}
	clock.Set(firstDue)
	cp.SweepDue(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("checkpoints after the first instant = %d, want 1", got)
	}
	clock.Set(firstDue.Add(interval - time.Second))
	cp.SweepDue(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("a second checkpoint ran before the fixed interval elapsed (%d)", got)
	}
	clock.Set(firstDue.Add(interval))
	cp.SweepDue(ctx)
	if got := calls.Load(); got != 2 {
		t.Fatalf("checkpoints after one interval = %d, want 2", got)
	}
	if row, _ := store.Get(ctx, "acme", sessionID); row.WorkspaceSnapshot == nil {
		t.Error("the scheduled checkpoint recorded no WorkspaceSnapshot")
	}
}

// spec: 4.4 (Event / Checkpoint Store) — "Failed periodic checkpoints are
// logged and retried on the next interval".
func TestSweepDueRetriesFailedCheckpointNextInterval_spec_4_4(t *testing.T) {
	const interval = time.Minute
	var calls atomic.Int32
	registry := podsession.NewRegistry()
	store := memstore.New()
	registry.Put(&podsession.BindResult{
		SessionID: "bad", TenantID: "acme",
		Adapter: dialAdapter(t, scheduleCountingAdapter{fakeCheckpointAdapter: fakeCheckpointAdapter{fail: true}, n: &calls}),
	})
	runningSession(t, store, "acme", "bad")

	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	var failures atomic.Int32
	cp := &checkpointer.Checkpointer{
		Sessions: store, Registry: registry, Interval: interval, Now: clock.Now,
		OnError: func(string, error) { failures.Add(1) },
	}
	ctx := context.Background()
	cp.SweepDue(ctx)
	clock.Set(start.Add(interval))
	cp.SweepDue(ctx)
	cp.SweepDue(ctx) // same instant: no second attempt.
	clock.Set(start.Add(2 * interval))
	cp.SweepDue(ctx)
	if calls.Load() != 2 || failures.Load() != 2 {
		t.Errorf("attempts = %d, failures = %d, want 2 and 2", calls.Load(), failures.Load())
	}
}
