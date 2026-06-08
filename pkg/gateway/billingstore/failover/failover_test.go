// SPDX-License-Identifier: MIT

package failover_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover"
)

// spec: §11.2.1 two-tier billing failover pipeline.

// flakyStore is a billingstore.Store that delegates to an in-memory
// ledger but can be put into a failing state to model a Postgres
// outage. It backs the failover, buffering, and replay tests.
type flakyStore struct {
	mu      sync.Mutex
	inner   *billingstore.Memory
	down    bool
	appends int
}

func newFlakyStore() *flakyStore {
	return &flakyStore{inner: billingstore.NewMemory()}
}

// errPrimaryDown is the simulated Postgres-unavailable error.
var errPrimaryDown = errors.New("flakyStore: primary unavailable")

func (f *flakyStore) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return billingstore.Event{}, errPrimaryDown
	}
	f.mu.Lock()
	f.appends++
	f.mu.Unlock()
	return f.inner.Append(ctx, e)
}

func (f *flakyStore) Since(ctx context.Context, tenantID string, since uint64, limit int) ([]billingstore.Event, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return nil, errPrimaryDown
	}
	return f.inner.Since(ctx, tenantID, since, limit)
}

func (f *flakyStore) SinceFiltered(ctx context.Context, tenantID string, since uint64, limit int, labelFilter map[string]string) ([]billingstore.Event, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return nil, errPrimaryDown
	}
	return f.inner.SinceFiltered(ctx, tenantID, since, limit, labelFilter)
}

func (f *flakyStore) SessionTotals(ctx context.Context, tenantID, sessionID string) (billingstore.SessionUsage, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return billingstore.SessionUsage{}, errPrimaryDown
	}
	return f.inner.SessionTotals(ctx, tenantID, sessionID)
}

func (f *flakyStore) EnvironmentTotals(ctx context.Context, tenantID, environmentID string) (billingstore.SessionUsage, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return billingstore.SessionUsage{}, errPrimaryDown
	}
	return f.inner.EnvironmentTotals(ctx, tenantID, environmentID)
}

// The erasure primitives delegate to the in-memory ledger; the failover
// tests do not model an erasure-time outage.
func (f *flakyStore) PseudonymizeUser(ctx context.Context, tenantID, userID string, salt []byte) (int, error) {
	return f.inner.PseudonymizeUser(ctx, tenantID, userID, salt)
}

func (f *flakyStore) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return f.inner.DeleteByUser(ctx, tenantID, userID)
}

func (f *flakyStore) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	return f.inner.DeleteByTenant(ctx, tenantID)
}

func (f *flakyStore) DeleteOlderThan(ctx context.Context, tenantID string, cutoff time.Time) (int, error) {
	return f.inner.DeleteOlderThan(ctx, tenantID, cutoff)
}

// setDown toggles the simulated outage.
func (f *flakyStore) setDown(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

// committed reports how many events landed in the primary ledger.
func (f *flakyStore) committed(tenantID string) int {
	events, _ := f.inner.Since(context.Background(), tenantID, 0, 0)
	return len(events)
}

func sessionEvent(tenant, session string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenant,
		UserID:    "alice@" + tenant,
		SessionID: session,
		EventType: billingstore.EventSessionCreated,
	}
}

// TestPrimaryPathCommitsSynchronously covers the §11.2.1 common case:
// when the primary store is healthy, Append commits straight through to
// it and reports the primary tier.
func TestPrimaryPathCommitsSynchronously(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary, Stream: failover.NewMemStream()})

	got, err := p.Append(context.Background(), sessionEvent("acme", "s1"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.SequenceNumber != 1 {
		t.Errorf("primary-path sequence: got %d, want 1", got.SequenceNumber)
	}
	if p.LastTier() != failover.TierPrimary {
		t.Errorf("LastTier: got %q, want primary", p.LastTier())
	}
	if primary.committed("acme") != 1 {
		t.Errorf("primary should hold 1 event, has %d", primary.committed("acme"))
	}
	if p.BufferLen() != 0 {
		t.Errorf("buffer should be empty on the primary path, has %d", p.BufferLen())
	}
}

// TestFailoverRoutesToStreamTier covers §11.2.1 Tier 1: when the primary
// write fails and the Redis stream is available, the event is published
// to the stream rather than dropped or buffered in memory.
func TestFailoverRoutesToStreamTier(t *testing.T) {
	primary := newFlakyStore()
	stream := failover.NewMemStream()
	p := failover.New(failover.Options{Primary: primary, Stream: stream})

	primary.setDown(true)
	got, err := p.Append(context.Background(), sessionEvent("acme", "s1"))
	if err != nil {
		t.Fatalf("Append during outage should not error when the stream is up: %v", err)
	}
	if p.LastTier() != failover.TierStream {
		t.Errorf("LastTier: got %q, want stream", p.LastTier())
	}
	if got.SequenceNumber != 1 {
		t.Errorf("provisional sequence: got %d, want 1", got.SequenceNumber)
	}
	if n, _ := stream.Pending(context.Background()); n != 1 {
		t.Errorf("stream should hold 1 event, has %d", n)
	}
	if p.BufferLen() != 0 {
		t.Errorf("Tier 2 buffer must stay empty while the stream is up, has %d", p.BufferLen())
	}
}

// TestFailoverFallsThroughToBufferWhenStreamDown covers §11.2.1 Tier 2:
// when the primary store and the Redis stream are both unavailable, the
// event lands in the in-memory write-ahead buffer.
func TestFailoverFallsThroughToBufferWhenStreamDown(t *testing.T) {
	primary := newFlakyStore()
	stream := failover.NewMemStream()
	p := failover.New(failover.Options{Primary: primary, Stream: stream})

	primary.setDown(true)
	stream.SetUnavailable(errors.New("redis down"))
	got, err := p.Append(context.Background(), sessionEvent("acme", "s1"))
	if err != nil {
		t.Fatalf("Append with a buffer slot free should not error: %v", err)
	}
	if p.LastTier() != failover.TierBuffer {
		t.Errorf("LastTier: got %q, want buffer", p.LastTier())
	}
	if got.SequenceNumber != 1 {
		t.Errorf("provisional sequence: got %d, want 1", got.SequenceNumber)
	}
	if p.BufferLen() != 1 {
		t.Errorf("Tier 2 buffer should hold 1 event, has %d", p.BufferLen())
	}
}

// TestNilStreamFailsStraightToBuffer confirms a pipeline with no Tier 1
// stream (single-node, no Redis) fails the primary write directly into
// the Tier 2 buffer.
func TestNilStreamFailsStraightToBuffer(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary}) // no Stream.

	primary.setDown(true)
	if _, err := p.Append(context.Background(), sessionEvent("acme", "s1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if p.LastTier() != failover.TierBuffer {
		t.Errorf("LastTier: got %q, want buffer", p.LastTier())
	}
}

// TestBufferFullRejectsWith503Signal covers the §11.2.1 invariant: when
// every tier is exhausted and the in-memory buffer is full, Append
// returns ErrBufferFull so the gateway can reject the request with a
// 503 — no billable work proceeds without a billing record.
func TestBufferFullRejectsWith503Signal(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{
		Primary:              primary,
		WriteAheadBufferSize: 2, // a tiny buffer so the test fills it fast.
	})

	primary.setDown(true)
	// The first two events fill the buffer.
	for i := 0; i < 2; i++ {
		if _, err := p.Append(context.Background(), sessionEvent("acme", "s")); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if !p.BufferFull() {
		t.Fatal("buffer should be full after 2 appends into a size-2 buffer")
	}
	// The third must be rejected with ErrBufferFull.
	_, err := p.Append(context.Background(), sessionEvent("acme", "s"))
	if !errors.Is(err, failover.ErrBufferFull) {
		t.Errorf("a full buffer must reject with ErrBufferFull, got %v", err)
	}
}

// TestFlushBufferReplaysInSequenceOrderOnRecovery covers §11.2.1: queued
// events are flushed to the primary store in sequence-number order once
// connectivity is restored, and the buffer drains.
func TestFlushBufferReplaysInSequenceOrderOnRecovery(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	ctx := context.Background()

	// Outage: five events accumulate in the Tier 2 buffer.
	primary.setDown(true)
	for i := 0; i < 5; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	if p.BufferLen() != 5 {
		t.Fatalf("buffer should hold 5 events, has %d", p.BufferLen())
	}

	// Recovery: the flusher drains the buffer into the primary store.
	primary.setDown(false)
	flushed, err := p.FlushBuffer(ctx)
	if err != nil {
		t.Fatalf("FlushBuffer: %v", err)
	}
	if flushed != 5 {
		t.Errorf("FlushBuffer reported %d flushed, want 5", flushed)
	}
	if p.BufferLen() != 0 {
		t.Errorf("buffer should be empty after a full flush, has %d", p.BufferLen())
	}
	// The primary store assigns the authoritative sequence numbers at
	// flush time; they must be a contiguous 1..5 in order.
	events, _ := primary.Since(ctx, "acme", 0, 0)
	if len(events) != 5 {
		t.Fatalf("primary should hold 5 events after the flush, has %d", len(events))
	}
	for i, e := range events {
		if e.SequenceNumber != uint64(i+1) {
			t.Errorf("event %d: sequence %d, want %d", i, e.SequenceNumber, i+1)
		}
	}
}

// TestFlushBufferStopsAtFirstStillFailingEvent covers the §11.2.1
// partial-recovery case: if the primary store is still rejecting, the
// flush halts and leaves the events buffered for the next cycle.
func TestFlushBufferStopsAtFirstStillFailingEvent(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	ctx := context.Background()

	primary.setDown(true)
	for i := 0; i < 3; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("buffered Append %d: %v", i, err)
		}
	}
	// The primary store is still down: a flush makes no progress and
	// surfaces the error.
	flushed, err := p.FlushBuffer(ctx)
	if err == nil {
		t.Error("FlushBuffer should surface the primary error while it is down")
	}
	if flushed != 0 {
		t.Errorf("FlushBuffer should flush 0 events while the primary is down, flushed %d", flushed)
	}
	if p.BufferLen() != 3 {
		t.Errorf("the 3 events must stay buffered for the next cycle, buffer has %d", p.BufferLen())
	}
}

// TestRunFlusherDrainsBufferOnRecovery covers the background flush loop:
// once the primary recovers, RunFlusher drains the buffer without an
// explicit FlushBuffer call.
func TestRunFlusherDrainsBufferOnRecovery(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{
		Primary:       primary,
		FlushInterval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	primary.setDown(true)
	for i := 0; i < 4; i++ {
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
		t.Fatalf("RunFlusher did not drain the buffer; %d events remain", p.BufferLen())
	}
	if primary.committed("acme") != 4 {
		t.Errorf("primary should hold 4 flushed events, has %d", primary.committed("acme"))
	}
}

// TestStreamDrainReplaysToPrimary covers the §11.2.1 stream-flusher
// model: the MemStream drains its queued events into the primary store
// in sequence order once the primary recovers.
func TestStreamDrainReplaysToPrimary(t *testing.T) {
	primary := newFlakyStore()
	stream := failover.NewMemStream()
	p := failover.New(failover.Options{Primary: primary, Stream: stream})
	ctx := context.Background()

	primary.setDown(true)
	for i := 0; i < 3; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("stream Append %d: %v", i, err)
		}
	}
	if n, _ := stream.Pending(ctx); n != 3 {
		t.Fatalf("stream should hold 3 events, has %d", n)
	}

	primary.setDown(false)
	flushed, err := stream.Drain(ctx, primary)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if flushed != 3 {
		t.Errorf("Drain reported %d flushed, want 3", flushed)
	}
	if n, _ := stream.Pending(ctx); n != 0 {
		t.Errorf("stream should be empty after a full drain, has %d", n)
	}
	if primary.committed("acme") != 3 {
		t.Errorf("primary should hold 3 events after the drain, has %d", primary.committed("acme"))
	}
}

// TestAppendValidatesBeforeFailover confirms a malformed event is
// rejected by validation before it can consume a buffer slot.
func TestAppendValidatesBeforeFailover(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	primary.setDown(true)

	// An event with no event type fails billingstore.Validate.
	_, err := p.Append(context.Background(), billingstore.Event{TenantID: "acme"})
	if !errors.Is(err, billingstore.ErrInvalidEvent) {
		t.Errorf("a malformed event should fail validation, got %v", err)
	}
	if p.BufferLen() != 0 {
		t.Errorf("a rejected event must not consume a buffer slot, buffer has %d", p.BufferLen())
	}
}

// TestSinceReadsFromPrimary confirms Since is served from the primary
// store and reflects flushed events.
func TestSinceReadsFromPrimary(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := p.Append(ctx, sessionEvent("acme", "s")); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	events, err := p.Since(ctx, "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("Since: got %d events, want 3", len(events))
	}
}

// TestProvisionalSequenceIsPerTenant confirms the provisional sequence
// numbers stamped during an outage are independent per tenant, matching
// the §11.2.1 per-tenant sequence model.
func TestProvisionalSequenceIsPerTenant(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	ctx := context.Background()
	primary.setDown(true)

	a, _ := p.Append(ctx, sessionEvent("acme", "s1"))
	b, _ := p.Append(ctx, sessionEvent("globex", "s2"))
	a2, _ := p.Append(ctx, sessionEvent("acme", "s3"))
	if a.SequenceNumber != 1 || b.SequenceNumber != 1 {
		t.Errorf("each tenant's provisional sequence starts at 1: acme=%d globex=%d",
			a.SequenceNumber, b.SequenceNumber)
	}
	if a2.SequenceNumber != 2 {
		t.Errorf("acme's second provisional sequence: got %d, want 2", a2.SequenceNumber)
	}
}
