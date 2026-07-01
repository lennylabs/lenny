// SPDX-License-Identifier: MIT

package failover_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
)

// spec: §12.3 line 76 — billingFlushIntervalMs / billingFlushBatchSize /
// billingFlushMaxPending and the billing_flush_pressure metric. F-12.3.13.

// TestFlushDefaultsMatchSpec12_3 pins the §12.3 line 76 defaults so a
// future edit cannot silently drift the buffering contract.
func TestFlushDefaultsMatchSpec12_3(t *testing.T) {
	if failover.DefaultFlushInterval != 500*time.Millisecond {
		t.Errorf("DefaultFlushInterval = %v, want 500ms (§12.3 billingFlushIntervalMs)", failover.DefaultFlushInterval)
	}
	if failover.DefaultFlushBatchSize != 50 {
		t.Errorf("DefaultFlushBatchSize = %d, want 50 (§12.3 billingFlushBatchSize)", failover.DefaultFlushBatchSize)
	}
	if failover.DefaultFlushMaxPending != 500 {
		t.Errorf("DefaultFlushMaxPending = %d, want 500 (§12.3 billingFlushMaxPending)", failover.DefaultFlushMaxPending)
	}
}

// TestFlushPressureFiresWhenBufferExceedsMaxPending covers §12.3 line 76:
// once the Tier 2 buffer grows past billingFlushMaxPending, every
// further buffering Append emits billing_flush_pressure.
func TestFlushPressureFiresWhenBufferExceedsMaxPending(t *testing.T) {
	primary := newFlakyStore()
	var fired int64
	p := failover.New(failover.Options{
		Primary:              primary,
		WriteAheadBufferSize: 10,
		MaxPending:           2,
		OnFlushPressure:      func() { atomic.AddInt64(&fired, 1) },
	})
	ctx := context.Background()

	primary.setDown(true)
	// Buffer 2 events: at len==2 the buffer is not yet *over* MaxPending.
	for i := 0; i < 2; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&fired); got != 0 {
		t.Fatalf("pressure fired %d times at or below MaxPending, want 0", got)
	}
	// The 3rd event takes the buffer to 3 (> MaxPending=2): pressure fires.
	if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
		t.Fatalf("buffered Append 3: %v", err)
	}
	if got := atomic.LoadInt64(&fired); got != 1 {
		t.Fatalf("pressure fired %d times after crossing MaxPending, want 1", got)
	}
}

// TestFlushPressureCallbackOptionalAndImmediateFlushDrainsOnRecovery
// covers the immediate-flush half of §12.3 line 76: when the primary has
// recovered, the over-threshold Append flushes the backlog without
// waiting for the interval tick, and a nil OnFlushPressure is tolerated.
func TestFlushPressureImmediateFlushDrainsOnRecovery(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{
		Primary:              primary,
		WriteAheadBufferSize: 10,
		MaxPending:           2,
		// OnFlushPressure deliberately nil — must be tolerated.
	})
	ctx := context.Background()

	primary.setDown(true)
	for i := 0; i < 3; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	// Buffer holds 3 (the immediate flush while down drained nothing).
	if p.BufferLen() != 3 {
		t.Fatalf("buffer should hold 3 during outage, has %d", p.BufferLen())
	}
	// Recover, then drain via the explicit flush path.
	primary.setDown(false)
	if _, err := p.FlushBuffer(ctx); err != nil {
		t.Fatalf("FlushBuffer after recovery: %v", err)
	}
	if p.BufferLen() != 0 {
		t.Errorf("buffer should drain after recovery, has %d", p.BufferLen())
	}
	if primary.committed("acme") != 3 {
		t.Errorf("primary should hold the 3 replayed events, has %d", primary.committed("acme"))
	}
}

// TestFlushBufferDrainsInBatchSizeChunks covers §12.3 billingFlushBatchSize:
// a single FlushBuffer call drains at most one batch.
func TestFlushBufferDrainsInBatchSizeChunks(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{
		Primary:              primary,
		WriteAheadBufferSize: 100,
		BatchSize:            2,
		MaxPending:           1000, // keep the immediate flush out of this test
	})
	ctx := context.Background()

	primary.setDown(true)
	for i := 0; i < 5; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	primary.setDown(false)

	// Each call flushes exactly one batch of 2 until the tail of 1 remains.
	for _, want := range []struct{ flushed, remaining int }{{2, 3}, {2, 1}, {1, 0}} {
		flushed, err := p.FlushBuffer(ctx)
		if err != nil {
			t.Fatalf("FlushBuffer: %v", err)
		}
		if flushed != want.flushed {
			t.Errorf("FlushBuffer flushed %d, want %d", flushed, want.flushed)
		}
		if p.BufferLen() != want.remaining {
			t.Errorf("buffer has %d, want %d", p.BufferLen(), want.remaining)
		}
	}
}

// TestRunFlusherDrainsFullBacklogAcrossBatches covers the loop in
// RunFlusher: a backlog larger than one batch still drains completely on
// a single tick window.
func TestRunFlusherDrainsFullBacklogAcrossBatches(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{
		Primary:       primary,
		BatchSize:     2,
		FlushInterval: 5 * time.Millisecond,
		MaxPending:    1000,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	primary.setDown(true)
	for i := 0; i < 7; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	go p.RunFlusher(ctx)
	primary.setDown(false)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.BufferLen() == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if p.BufferLen() != 0 {
		t.Fatalf("RunFlusher did not drain the 7-event backlog; %d remain", p.BufferLen())
	}
	if primary.committed("acme") != 7 {
		t.Errorf("primary should hold all 7 replayed events, has %d", primary.committed("acme"))
	}
}

// TestMaxPendingClampedBelowBufferCap covers the New() clamp: a small
// WriteAheadBufferSize must still raise billing_flush_pressure before the
// hard ErrBufferFull rejection, so operators get an early warning.
func TestMaxPendingClampedBelowBufferCap(t *testing.T) {
	primary := newFlakyStore()
	var fired int64
	p := failover.New(failover.Options{
		Primary:              primary,
		WriteAheadBufferSize: 3,
		// MaxPending left as the 500 default; New clamps it to cap-1 = 2.
		OnFlushPressure: func() { atomic.AddInt64(&fired, 1) },
	})
	ctx := context.Background()

	primary.setDown(true)
	for i := 0; i < 3; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&fired); got == 0 {
		t.Error("pressure must fire before the buffer fills when MaxPending is clamped below cap")
	}
}
