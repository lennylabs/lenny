// SPDX-License-Identifier: MIT

package subsystem_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
)

// spec: §4.1 — a Limiter admits up to MaxConcurrent in-flight
// requests; queued callers count toward the QueueDepth gauge.
func TestLimiterBlocksAtMaxConcurrent(t *testing.T) {
	l := &subsystem.Limiter{MaxConcurrent: 2}

	// Take the two slots.
	r1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if got := l.InFlight(); got != 2 {
		t.Fatalf("InFlight() = %d, want 2", got)
	}
	if got := l.QueueDepth(); got != 0 {
		t.Fatalf("QueueDepth() = %d, want 0", got)
	}

	// A third caller queues. Run it asynchronously so the test can
	// observe the QueueDepth bump.
	queued := make(chan struct{})
	released := make(chan struct{})
	go func() {
		r3, err := l.Acquire(context.Background())
		if err != nil {
			t.Errorf("third Acquire: %v", err)
		}
		close(queued)
		<-released
		if r3 != nil {
			r3()
		}
	}()

	// Spin briefly until the queued caller registers.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if l.QueueDepth() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := l.QueueDepth(); got != 1 {
		t.Fatalf("QueueDepth() = %d, want 1", got)
	}

	// Releasing one slot lets the queued caller proceed.
	r1()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("queued caller did not unblock after slot release")
	}

	close(released)
	r2()
	// Give the goroutine a moment to release the third slot.
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if l.InFlight() == 0 && l.QueueDepth() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if l.InFlight() != 0 || l.QueueDepth() != 0 {
		t.Fatalf("after release: InFlight=%d QueueDepth=%d, want 0/0", l.InFlight(), l.QueueDepth())
	}
}

// spec: §4.1 — context cancellation while queued returns
// ErrLimiterStopped without acquiring a slot.
func TestLimiterContextCancelReturnsError(t *testing.T) {
	l := &subsystem.Limiter{MaxConcurrent: 1}
	r1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.Acquire(ctx)
		done <- err
	}()

	// Wait for the caller to register as queued.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if l.QueueDepth() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, subsystem.ErrLimiterStopped) {
			t.Fatalf("got %v, want %v", err, subsystem.ErrLimiterStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire did not unblock on context cancellation")
	}
}

// spec: §4.1 — TryAcquire returns immediately, admitting only when
// a slot is free; saturated callers see (nil, false).
func TestLimiterTryAcquire(t *testing.T) {
	l := &subsystem.Limiter{MaxConcurrent: 1}
	r1, ok := l.TryAcquire()
	if !ok {
		t.Fatal("first TryAcquire should succeed on an empty limiter")
	}
	if _, ok := l.TryAcquire(); ok {
		t.Fatal("second TryAcquire on a full limiter must return false")
	}
	r1()
	if _, ok := l.TryAcquire(); !ok {
		t.Fatal("TryAcquire after release should succeed")
	}
}

// spec: §4.1 — MaxConcurrent <= 0 is treated as unbounded; the
// queue-depth gauge always reports zero in that mode.
func TestLimiterUnboundedNeverQueues(t *testing.T) {
	l := &subsystem.Limiter{}
	releases := make([]func(), 0, 8)
	for i := 0; i < 8; i++ {
		r, err := l.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		releases = append(releases, r)
	}
	if l.QueueDepth() != 0 {
		t.Fatalf("QueueDepth() = %d, want 0 for unbounded limiter", l.QueueDepth())
	}
	if l.InFlight() != 8 {
		t.Fatalf("InFlight() = %d, want 8", l.InFlight())
	}
	for _, r := range releases {
		r()
	}
	if l.InFlight() != 0 {
		t.Fatalf("InFlight() after release = %d, want 0", l.InFlight())
	}
}

// spec: §4.1 — release is idempotent: calling the returned function
// twice does not over-decrement the in-flight counter.
func TestLimiterReleaseIdempotent(t *testing.T) {
	l := &subsystem.Limiter{MaxConcurrent: 1}
	r, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	r()
	r() // second call must be a no-op
	if l.InFlight() != 0 {
		t.Fatalf("InFlight() = %d, want 0", l.InFlight())
	}
	if _, ok := l.TryAcquire(); !ok {
		t.Fatal("limiter should be free after release")
	}
}

// TestLimiterConcurrentCallers stresses the limiter with many
// goroutines to confirm InFlight and QueueDepth remain consistent
// and ultimately drain to zero.
func TestLimiterConcurrentCallers(t *testing.T) {
	l := &subsystem.Limiter{MaxConcurrent: 4}
	const callers = 64
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := l.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			// Hold briefly to force contention.
			time.Sleep(time.Millisecond)
			r()
		}()
	}
	wg.Wait()
	if got := l.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0", got)
	}
	if got := l.QueueDepth(); got != 0 {
		t.Fatalf("QueueDepth() = %d, want 0", got)
	}
}
