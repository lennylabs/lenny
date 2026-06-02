// SPDX-License-Identifier: MIT

// Package auditbatch is the §12.3 T2 audit-event batching buffer. It is
// the opt-in (`audit.batchingEnabled: true`) batched-insert path for
// non-PII T2 (Internal) audit events: pool metrics, routing events, and
// the platform-internal cross_tenant_read worker receipts. T3/T4
// (Confidential / Restricted) audit events carry user identifiers and
// MUST stay on the synchronous write path; they never enter this
// buffer.
//
// The buffer accumulates items in memory and flushes them as a batch
// when either the flush interval elapses (`auditFlushIntervalMs`,
// default 250ms) or the batch reaches its size cap
// (`auditFlushBatchSize`, default 100). The in-memory buffer has no
// write-ahead log: a gateway crash during a flush interval loses the
// buffered T2 events that have not yet been written to Postgres. This
// is the documented, accepted data-loss tradeoff of batching, which is
// why it is disabled by default and gated behind an explicit operator
// opt-in (§12.3 lines 81-83).
package auditbatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Item is one buffered T2 audit event. TenantID selects the per-tenant
// hash chain; an empty At is stamped at flush time by the FlushFunc.
type Item struct {
	TenantID  string
	EventType string
	Payload   json.RawMessage
	At        time.Time
}

// FlushFunc writes a batch of buffered items durably. The auditstore
// implementation groups them by tenant chain and seals each under one
// per-tenant advisory lock. A non-nil error means the batch was not
// committed; the buffer drops it (the accepted T2 data-loss tradeoff)
// after reporting it through Metrics.
type FlushFunc func(ctx context.Context, items []Item) error

// Metrics is the buffer's optional observability surface. A nil Metrics
// is a valid no-op.
type Metrics interface {
	// Flushed counts items successfully written in a flush batch.
	Flushed(n int)
	// FlushFailed counts a flush batch that failed to commit; n items
	// were lost.
	FlushFailed(n int)
}

// Config pins the §12.3 lines 79-81 batching tunables. Zero fields are
// filled from DefaultConfig.
type Config struct {
	// FlushInterval is auditFlushIntervalMs (default 250ms): the buffer
	// flushes at least this often regardless of fill level.
	FlushInterval time.Duration
	// BatchSize is auditFlushBatchSize (default 100): a flush is
	// triggered as soon as the buffer holds this many items.
	BatchSize int
}

// DefaultConfig returns the §12.3 lines 79-81 batching defaults.
func DefaultConfig() Config {
	return Config{FlushInterval: 250 * time.Millisecond, BatchSize: 100}
}

// Buffer is the §12.3 T2 audit-event batching buffer. Construct with
// New and drive Run in a goroutine. Enqueue is safe for concurrent
// callers.
type Buffer struct {
	flush   FlushFunc
	cfg     Config
	metrics Metrics

	mu      sync.Mutex
	pending []Item
	wake    chan struct{}
}

// New returns a Buffer that flushes through flush. cfg's zero fields are
// filled from DefaultConfig; metrics may be nil.
func New(flush FlushFunc, cfg Config, metrics Metrics) *Buffer {
	def := DefaultConfig()
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = def.FlushInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	return &Buffer{
		flush:   flush,
		cfg:     cfg,
		metrics: metrics,
		wake:    make(chan struct{}, 1),
	}
}

// Enqueue appends a T2 audit event to the in-memory buffer. When the
// buffer reaches BatchSize it signals the Run loop to flush immediately
// rather than waiting for the interval. Enqueue never blocks on the
// flush.
func (b *Buffer) Enqueue(it Item) {
	b.mu.Lock()
	b.pending = append(b.pending, it)
	full := len(b.pending) >= b.cfg.BatchSize
	b.mu.Unlock()
	if full {
		b.signal()
	}
}

func (b *Buffer) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// take swaps out the current pending slice under the lock.
func (b *Buffer) take() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return nil
	}
	items := b.pending
	b.pending = nil
	return items
}

// Flush writes the currently buffered items as one batch. It is a no-op
// when the buffer is empty. A flush error drops the batch (accepted T2
// data loss) after reporting it.
func (b *Buffer) Flush(ctx context.Context) error {
	items := b.take()
	if len(items) == 0 {
		return nil
	}
	if err := b.flush(ctx, items); err != nil {
		if b.metrics != nil {
			b.metrics.FlushFailed(len(items))
		}
		return fmt.Errorf("auditbatch: flush %d items: %w", len(items), err)
	}
	if b.metrics != nil {
		b.metrics.Flushed(len(items))
	}
	return nil
}

// Run drives the §12.3 flush loop: it flushes every FlushInterval and
// whenever Enqueue fills the buffer to BatchSize. On ctx cancellation it
// flushes the remaining buffer once so a graceful shutdown does not drop
// already-buffered T2 events, then returns.
func (b *Buffer) Run(ctx context.Context) {
	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Best-effort final flush on shutdown using a fresh context
			// (ctx is already cancelled).
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = b.Flush(flushCtx)
			cancel()
			return
		case <-ticker.C:
			_ = b.Flush(ctx)
		case <-b.wake:
			_ = b.Flush(ctx)
		}
	}
}
