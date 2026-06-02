// SPDX-License-Identifier: MIT

package auditbatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingFlush struct {
	mu      sync.Mutex
	batches [][]Item
	err     error
	ch      chan int
}

func (r *recordingFlush) flush(_ context.Context, items []Item) error {
	r.mu.Lock()
	if r.err != nil {
		err := r.err
		r.mu.Unlock()
		return err
	}
	batch := append([]Item(nil), items...)
	r.batches = append(r.batches, batch)
	ch := r.ch
	r.mu.Unlock()
	if ch != nil {
		ch <- len(items)
	}
	return nil
}

func (r *recordingFlush) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, b := range r.batches {
		n += len(b)
	}
	return n
}

type countingMetrics struct {
	mu            sync.Mutex
	flushed, lost int
	flushCalls    int
	failureCalls  int
}

func (m *countingMetrics) Flushed(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushed += n
	m.flushCalls++
}
func (m *countingMetrics) FlushFailed(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lost += n
	m.failureCalls++
}

// spec: §12.3 line 81 — Flush drains the buffered items into one batch.
func TestBuffer_FlushDrains_spec_12_3(t *testing.T) {
	rf := &recordingFlush{}
	b := New(rf.flush, Config{}, nil)
	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if rf.total() != 2 {
		t.Errorf("flushed %d items, want 2", rf.total())
	}
	// Buffer is now empty: a second flush is a no-op.
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if rf.total() != 2 {
		t.Errorf("flushed %d items after empty flush, want 2", rf.total())
	}
}

// spec: §12.3 line 81 — an empty buffer never invokes the flush
// callback.
func TestBuffer_EmptyFlushNoCall_spec_12_3(t *testing.T) {
	rf := &recordingFlush{}
	b := New(rf.flush, Config{}, nil)
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(rf.batches) != 0 {
		t.Errorf("flush callback invoked on empty buffer: %v", rf.batches)
	}
}

// spec: §12.3 lines 81-83 — a flush failure drops the batch (accepted
// T2 data loss) and reports the loss through Metrics.
func TestBuffer_FlushErrorDropsAndReports_spec_12_3(t *testing.T) {
	rf := &recordingFlush{err: errors.New("postgres down")}
	m := &countingMetrics{}
	b := New(rf.flush, Config{}, m)
	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	if err := b.Flush(context.Background()); err == nil {
		t.Fatal("Flush: expected error, got nil")
	}
	if m.lost != 2 || m.failureCalls != 1 {
		t.Errorf("metrics lost=%d failureCalls=%d, want 2/1", m.lost, m.failureCalls)
	}
	// The dropped batch is not retained: a subsequent successful flush
	// has nothing to write.
	rf.mu.Lock()
	rf.err = nil
	rf.mu.Unlock()
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("recovery Flush: %v", err)
	}
	if rf.total() != 0 {
		t.Errorf("dropped batch was replayed: %d items", rf.total())
	}
}

// spec: §12.3 line 81 — Run flushes as soon as the buffer reaches
// BatchSize without waiting for the interval.
func TestBuffer_RunFlushesOnSize_spec_12_3(t *testing.T) {
	rf := &recordingFlush{ch: make(chan int, 4)}
	// Long interval so only the size trigger can fire within the test.
	b := New(rf.flush, Config{FlushInterval: time.Hour, BatchSize: 3}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	for i := 0; i < 3; i++ {
		b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	}
	select {
	case n := <-rf.ch:
		if n != 3 {
			t.Errorf("size-triggered flush wrote %d, want 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("size-triggered flush did not fire")
	}
}

// spec: §12.3 line 81 — Run flushes on the interval even when the
// buffer never reaches BatchSize.
func TestBuffer_RunFlushesOnInterval_spec_12_3(t *testing.T) {
	rf := &recordingFlush{ch: make(chan int, 4)}
	b := New(rf.flush, Config{FlushInterval: 20 * time.Millisecond, BatchSize: 1000}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	select {
	case n := <-rf.ch:
		if n != 1 {
			t.Errorf("interval flush wrote %d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interval flush did not fire")
	}
}

// spec: §12.3 line 81 — on ctx cancellation Run flushes the remaining
// buffer once so a graceful shutdown does not drop already-buffered T2
// events.
func TestBuffer_RunFinalFlushOnShutdown_spec_12_3(t *testing.T) {
	rf := &recordingFlush{}
	b := New(rf.flush, Config{FlushInterval: time.Hour, BatchSize: 1000}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	b.Enqueue(Item{TenantID: "platform", EventType: "cross_tenant_read"})
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if rf.total() != 1 {
		t.Errorf("shutdown flush wrote %d items, want 1", rf.total())
	}
}

// spec: §12.3 line 81 — zero config fills the documented defaults.
func TestBuffer_DefaultConfig_spec_12_3(t *testing.T) {
	b := New(func(context.Context, []Item) error { return nil }, Config{}, nil)
	if b.cfg.FlushInterval != DefaultConfig().FlushInterval {
		t.Errorf("FlushInterval = %v, want %v", b.cfg.FlushInterval, DefaultConfig().FlushInterval)
	}
	if b.cfg.BatchSize != DefaultConfig().BatchSize {
		t.Errorf("BatchSize = %d, want %d", b.cfg.BatchSize, DefaultConfig().BatchSize)
	}
}
