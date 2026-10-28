// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// steppedClock is the injected Checkpointer.Now. The test moves it and Run
// reads it. calls counts every read so the test can tell that Run has
// re-evaluated the schedule after a move.
type steppedClock struct {
	mu    sync.Mutex
	now   time.Time
	calls int
}

func (c *steppedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.now
}

// set moves the clock and returns the read count at the move.
func (c *steppedClock) set(t time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	return c.calls
}

func (c *steppedClock) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// clockedAdapter answers every Checkpoint with a Summary and records the
// injected clock's reading when each stream opens, which is the instant the
// schedule fired. inflight counts open streams.
type clockedAdapter struct {
	adapterv1.UnimplementedAdapterServer
	clock *steppedClock

	mu       sync.Mutex
	fired    []time.Time
	inflight int
}

func (a *clockedAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	a.mu.Lock()
	a.inflight++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.inflight--
		a.mu.Unlock()
	}()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	at := a.clock.Now()
	a.mu.Lock()
	a.fired = append(a.fired, at)
	a.mu.Unlock()
	return stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Summary{Summary: &adapterv1.CheckpointSummary{TotalBytes: 1}},
	})
}

func (a *clockedAdapter) snapshot() (fired []time.Time, inflight int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]time.Time(nil), a.fired...), a.inflight
}

// quietReads is how many clock reads after a move the test waits for before
// it treats Run as having evaluated the moved clock. Between the schedule's
// read and the adapter stream opening, the checkpoint path reads the clock
// once more (the duration histogram's start), so a checkpoint that fires at
// the moved instant is in flight, or already recorded, before this many
// reads accumulate.
const quietReads = 10

// moveAndSettle moves the clock to at and waits until Run has re-read it
// several times with no checkpoint in flight, so any checkpoint due at that
// instant has fired and recorded exactly at.
func moveAndSettle(t *testing.T, clock *steppedClock, adapter *clockedAdapter, at time.Time) {
	t.Helper()
	base := clock.set(at)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, inflight := adapter.snapshot(); inflight == 0 && clock.callCount()-base >= quietReads {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run did not re-evaluate the schedule at %v within the deadline", at)
		}
		time.Sleep(time.Millisecond)
	}
}

// spec: §4.4 (Periodic checkpoint scheduling) — the default
// periodicCheckpointIntervalSeconds is 600 s; each session's first periodic
// checkpoint is scheduled at interval + random(0, interval × jitterFraction)
// after the session starts, and later ones at the fixed interval from the
// previous checkpoint.
// diagnosis: Run with an unset Interval did not schedule the 600-second
// default: the first periodic checkpoint fired outside [600 s, 720 s] after
// the binding was first observed (an interval other than the default, or
// jitter applied wrongly), or the second did not fire exactly 600 s after
// the first (the default did not reach the steady-state reschedule).
func TestRunUsesTenMinuteDefaultIntervalWhenUnset(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	clock := &steppedClock{now: t0}
	adapter := &clockedAdapter{clock: clock}

	registry := podsession.NewRegistry()
	store := memstore.New()
	registry.Put(&podsession.BindResult{SessionID: "sess-alice", TenantID: "acme", Adapter: dialAdapter(t, adapter)})
	runningSession(t, store, "acme", "sess-alice")

	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		// Interval is deliberately unset: the subject is the default.
		JitterFraction: checkpointer.DefaultJitterFraction,
		ScheduleTick:   time.Millisecond,
		Now:            clock.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cp.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Run's opening pass registers the binding at t0, so every delay below
	// is measured from t0.
	moveAndSettle(t, clock, adapter, t0)

	const (
		interval  = 600 * time.Second
		jitterCap = interval + interval/5 // interval × (1 + DefaultJitterFraction)
	)

	// Nothing fires before the 600-second floor.
	moveAndSettle(t, clock, adapter, t0.Add(interval-time.Second))
	if fired, _ := adapter.snapshot(); len(fired) != 0 {
		t.Fatalf("a periodic checkpoint fired at %v, before the 600 s default interval", fired[0].Sub(t0))
	}

	// Step one second at a time through the jitter window; the first
	// periodic checkpoint fires inside [600 s, 720 s].
	var first time.Time
	for at := t0.Add(interval); !at.After(t0.Add(jitterCap)); at = at.Add(time.Second) {
		moveAndSettle(t, clock, adapter, at)
		if fired, _ := adapter.snapshot(); len(fired) > 0 {
			if len(fired) != 1 {
				t.Fatalf("%d checkpoints fired in the first window, want 1", len(fired))
			}
			first = fired[0]
			break
		}
	}
	if first.IsZero() {
		t.Fatalf("no periodic checkpoint fired by %v, the end of the default jitter window", jitterCap)
	}
	if d := first.Sub(t0); d < interval || d > jitterCap {
		t.Fatalf("first periodic checkpoint at %v, want within [%v, %v]", d, interval, jitterCap)
	}

	// The next one is exactly 600 s after the first, with no further jitter.
	moveAndSettle(t, clock, adapter, first.Add(interval-time.Second))
	if fired, _ := adapter.snapshot(); len(fired) != 1 {
		t.Fatalf("second periodic checkpoint fired at %v after the first, before the 600 s interval", fired[1].Sub(first))
	}
	moveAndSettle(t, clock, adapter, first.Add(interval))
	fired, _ := adapter.snapshot()
	if len(fired) != 2 {
		t.Fatalf("checkpoints after first + 600 s = %d, want 2", len(fired))
	}
	if gap := fired[1].Sub(fired[0]); gap != interval {
		t.Fatalf("second periodic checkpoint %v after the first, want exactly %v", gap, interval)
	}
}
