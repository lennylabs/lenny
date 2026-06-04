// SPDX-License-Identifier: MIT

// Package failover implements the §11.2.1 two-tier billing failover
// pipeline. It wraps the durable billingstore.Store (Postgres) so a
// billing event is never lost when the primary store is transiently
// unavailable.
//
// The §11.2.1 write path has three stages:
//
//	Primary  — the synchronous billingstore.Store (Postgres) write.
//	Tier 1   — a durable Redis stream intermediate buffer. On a primary
//	           write failure the event is published to the stream; a
//	           background flusher replays it into Postgres in sequence
//	           order once the primary recovers. The stream survives an
//	           individual gateway-replica crash.
//	Tier 2   — a bounded in-memory write-ahead buffer on the replica.
//	           When the Redis stream is also unavailable, the event is
//	           queued here and flushed once either store recovers.
//
// When the in-memory buffer fills before either store recovers, Append
// returns ErrBufferFull. §11.2.1 has the gateway translate this to a
// 503 on session-progressing requests so no billable work proceeds
// without a corresponding billing record.
//
// The pipeline is the integration point: Pipeline implements
// billingstore.Store, so the gateway wires it in place of the bare
// store. The Redis stream tier (StreamTier) is an interface — the
// durable implementation lives in failover/redisstream; tests inject an
// in-memory fake so the failover, buffering, and replay logic is
// exercised without a live Redis.
package failover

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// DefaultWriteAheadBufferSize is the §11.2.1 billingWriteAheadBufferSize
// default: the Tier 2 in-memory write-ahead buffer holds at most this
// many events per gateway replica before Append starts rejecting.
const DefaultWriteAheadBufferSize = 10_000

// DefaultFlushInterval is the §12.3 line 76 billingFlushIntervalMs
// default expressed as a duration: how often the background flusher
// re-attempts the primary store and drains the buffered batch.
const DefaultFlushInterval = 500 * time.Millisecond

// DefaultFlushBatchSize is the §12.3 line 76 billingFlushBatchSize
// default: the maximum number of buffered events the flusher drains
// into the primary store in a single multi-row replay batch.
const DefaultFlushBatchSize = 50

// DefaultFlushMaxPending is the §12.3 line 76 billingFlushMaxPending
// default: when the Tier 2 buffer grows past this many events the
// pipeline flushes immediately (out of band of the interval tick) and
// emits the billing_flush_pressure metric.
const DefaultFlushMaxPending = 500

// ErrBufferFull is returned by Append when both the primary store and
// the Redis stream tier are unavailable and the Tier 2 in-memory
// write-ahead buffer is full. §11.2.1: the gateway maps this to a 503
// on session-progressing requests so no billable work proceeds without
// a billing record.
var ErrBufferFull = errors.New("billingstore/failover: write-ahead buffer is full; billing cannot be recorded")

// StreamTier is the §11.2.1 Tier 1 durable intermediate buffer. The
// production implementation (failover/redisstream) is an XADD-based
// Redis stream with a billing-flusher consumer group; an in-memory fake
// backs tests. A nil StreamTier disables Tier 1, so the pipeline fails
// straight from the primary store to the Tier 2 in-memory buffer.
type StreamTier interface {
	// Publish writes a billing event to the durable stream. The event
	// already carries the provisional sequence number assigned by the
	// pipeline. A non-nil error means the stream itself is unavailable
	// and the pipeline must fall through to Tier 2.
	Publish(ctx context.Context, e billingstore.Event) error

	// Pending reports the number of events currently held in the stream
	// that have not yet been flushed to the primary store. The pipeline
	// surfaces this for the §11.2.1 BillingStreamBackpressure signal.
	Pending(ctx context.Context) (int, error)

	// PurgeUser removes every staged billing event for userID within
	// tenantID from the stream and returns the count removed. It is the
	// §12.8 step-5 erasure of the Tier 1 half of the billing write-ahead
	// buffer: a staged event flushed after the user's durable billing
	// rows were pseudonymized would re-insert the raw user_id into
	// Postgres, so the GDPR erasure job purges the stream before
	// completing.
	//
	// spec: §12.8 line 788 (Billing write-ahead buffer), step 5.
	PurgeUser(ctx context.Context, tenantID, userID string) (int, error)
}

// Clock returns the current time. Production passes time.Now; tests
// inject a deterministic clock.
type Clock func() time.Time

// Options configures a Pipeline.
type Options struct {
	// Primary is the durable billing store (Postgres). Required.
	Primary billingstore.Store

	// Stream is the §11.2.1 Tier 1 durable Redis stream. Nil disables
	// Tier 1; the pipeline then fails directly to the Tier 2 buffer.
	Stream StreamTier

	// WriteAheadBufferSize bounds the Tier 2 in-memory buffer. Zero or
	// negative uses DefaultWriteAheadBufferSize.
	WriteAheadBufferSize int

	// FlushInterval is how often the background flusher re-attempts the
	// primary store. Zero uses DefaultFlushInterval (§12.3
	// billingFlushIntervalMs).
	FlushInterval time.Duration

	// BatchSize bounds the number of buffered events drained into the
	// primary store per flush call (§12.3 billingFlushBatchSize), so the
	// replay proceeds as bounded multi-row batches rather than one
	// unbounded statement. Zero or negative uses DefaultFlushBatchSize.
	BatchSize int

	// MaxPending is the §12.3 billingFlushMaxPending threshold: once the
	// Tier 2 buffer grows beyond it, Append flushes immediately and
	// invokes OnFlushPressure. Zero or negative uses
	// DefaultFlushMaxPending; it is clamped to at most
	// WriteAheadBufferSize so a small buffer still signals pressure.
	MaxPending int

	// OnFlushPressure is invoked each time an Append finds the buffer
	// over MaxPending. The gateway wires it to the §12.3
	// billing_flush_pressure metric. Nil disables the callback.
	OnFlushPressure func()

	// Clock overrides time.Now. Nil uses time.Now.
	Clock Clock
}

// Tier names the stage of the §11.2.1 pipeline that accepted an event.
type Tier string

const (
	// TierPrimary — the event committed synchronously to the primary
	// store (the common, healthy case).
	TierPrimary Tier = "primary"
	// TierStream — the primary store was unavailable; the event was
	// published to the Tier 1 durable Redis stream.
	TierStream Tier = "stream"
	// TierBuffer — the primary store and the Redis stream were both
	// unavailable; the event landed in the Tier 2 in-memory buffer.
	TierBuffer Tier = "buffer"
)

// Pipeline is the §11.2.1 two-tier billing failover pipeline. It
// implements billingstore.Store, so it is a drop-in replacement for the
// bare durable store.
type Pipeline struct {
	primary    billingstore.Store
	stream     StreamTier
	bufferCap  int
	interval   time.Duration
	batchSize  int
	maxPending int
	onPressure func()
	clock      Clock

	mu       sync.Mutex
	buffer   []billingstore.Event // Tier 2 write-ahead buffer, sequence order.
	seq      map[string]uint64    // provisional per-tenant sequence counter.
	lastTier Tier                 // tier the most recent Append landed in.
}

var _ billingstore.Store = (*Pipeline)(nil)

// New returns a configured Pipeline. Primary is required; New panics if
// it is nil, because a pipeline with no durable backing cannot record
// billing.
func New(opts Options) *Pipeline {
	if opts.Primary == nil {
		panic("billingstore/failover: Options.Primary is required")
	}
	cap := opts.WriteAheadBufferSize
	if cap <= 0 {
		cap = DefaultWriteAheadBufferSize
	}
	interval := opts.FlushInterval
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultFlushBatchSize
	}
	maxPending := opts.MaxPending
	if maxPending <= 0 {
		maxPending = DefaultFlushMaxPending
	}
	// A small write-ahead buffer must still be able to signal pressure:
	// clamp the threshold below the hard capacity so ErrBufferFull is
	// preceded by at least one billing_flush_pressure emission.
	if maxPending >= cap {
		maxPending = cap - 1
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Pipeline{
		primary:    opts.Primary,
		stream:     opts.Stream,
		bufferCap:  cap,
		interval:   interval,
		batchSize:  batchSize,
		maxPending: maxPending,
		onPressure: opts.OnFlushPressure,
		clock:      clock,
		seq:        make(map[string]uint64),
	}
}

// Append implements billingstore.Store. It walks the §11.2.1 two-tier
// failover path: the synchronous primary write, then the Tier 1 durable
// Redis stream, then the Tier 2 in-memory write-ahead buffer. Append
// returns ErrBufferFull only when every tier is exhausted.
//
// On the primary path the committed event carries the sequence number
// the primary store assigned. On the Tier 1 and Tier 2 paths the event
// carries a provisional sequence number — §11.2.1 has the flusher
// acquire the authoritative Postgres sequence value at flush time, so
// the provisional number orders the replay but is renumbered on the
// real INSERT.
func (p *Pipeline) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if err := billingstore.Validate(e); err != nil {
		return billingstore.Event{}, err
	}
	// The common, healthy path: a synchronous primary write.
	committed, err := p.primary.Append(ctx, e)
	if err == nil {
		p.setLastTier(TierPrimary)
		return committed, nil
	}
	// §11.2.1: a billing event is never dropped on a primary failure.
	// Stamp a provisional sequence number so the replay preserves the
	// monotonic ordering guarantee, then route to Tier 1.
	provisional := billingstore.Normalize(e, p.clock())
	provisional.SequenceNumber = p.nextProvisional(e.TenantID)

	if p.stream != nil {
		if serr := p.stream.Publish(ctx, provisional); serr == nil {
			p.setLastTier(TierStream)
			return provisional, nil
		}
	}
	// Tier 2: the bounded in-memory write-ahead buffer.
	overPending, err := p.bufferEvent(provisional)
	if err != nil {
		return billingstore.Event{}, err
	}
	p.setLastTier(TierBuffer)
	// §12.3 line 76: once the buffer exceeds billingFlushMaxPending the
	// gateway flushes immediately and emits billing_flush_pressure. The
	// flush is best-effort — if the primary is still down it drains
	// nothing and the events stay buffered for the next cycle.
	if overPending {
		if hook := p.pressureHook(); hook != nil {
			hook()
		}
		_, _ = p.FlushBuffer(ctx)
	}
	return provisional, nil
}

// SetFlushPressureHook installs (or replaces) the §12.3 line 76
// billing_flush_pressure callback after construction. The gateway wires
// gatewaymetrics.IncBillingFlushPressure here once the metric registry
// exists, which is built after the Pipeline. Safe to call before any
// Append because access is mutex-guarded.
func (p *Pipeline) SetFlushPressureHook(fn func()) {
	p.mu.Lock()
	p.onPressure = fn
	p.mu.Unlock()
}

// pressureHook returns the current billing_flush_pressure callback under
// the lock so a deferred SetFlushPressureHook does not race Append.
func (p *Pipeline) pressureHook() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.onPressure
}

// Since implements billingstore.Store. Reads are served from the
// primary store. Events still sitting in the Tier 1 stream or the Tier
// 2 buffer have not yet been assigned an authoritative sequence number,
// so they are intentionally not returned: a consumer detects the gap
// and replays once the flusher has renumbered and committed them.
func (p *Pipeline) Since(ctx context.Context, tenantID string, since uint64, limit int) ([]billingstore.Event, error) {
	return p.primary.Since(ctx, tenantID, since, limit)
}

// SessionTotals implements billingstore.Store. Per-session usage is read
// from the durable primary store; events still buffered behind a primary
// outage have not been renumbered yet and are intentionally excluded, the
// same gap-then-replay contract Since honours. spec: §15.1; §11.2.1.
// F-15.2.3.
func (p *Pipeline) SessionTotals(ctx context.Context, tenantID, sessionID string) (billingstore.SessionUsage, error) {
	return p.primary.SessionTotals(ctx, tenantID, sessionID)
}

// PseudonymizeUser implements billingstore.Store. It rewrites the
// durable ledger through the primary store and then pseudonymizes any
// matching events still sitting in the Tier 2 write-ahead buffer, so a
// user erasure that races a primary outage does not leave the user id
// in the buffered events the flusher will later commit. It returns the
// total count rewritten across both.
//
// spec: §12.8 tenant-controlled billing erasure.
func (p *Pipeline) PseudonymizeUser(ctx context.Context, tenantID, userID string, salt []byte) (int, error) {
	n, err := p.primary.PseudonymizeUser(ctx, tenantID, userID, salt)
	if err != nil {
		return 0, err
	}
	pseudonym := billingstore.Pseudonymize(userID, salt)
	p.mu.Lock()
	for i := range p.buffer {
		if p.buffer[i].TenantID == tenantID && p.buffer[i].UserID == userID {
			p.buffer[i].UserID = pseudonym
			n++
		}
	}
	p.mu.Unlock()
	return n, nil
}

// DeleteByUser implements billingstore.Store. Billing user erasure
// pseudonymizes rather than deletes, so this delegates to the primary
// no-op and leaves the buffer untouched.
//
// spec: §12.1 line 5.
func (p *Pipeline) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return p.primary.DeleteByUser(ctx, tenantID, userID)
}

// PurgeStagedByUser removes every staged billing event for userID within
// tenantID from both the Tier 1 durable stream and the Tier 2 in-memory
// write-ahead buffer, returning the total count removed across both. It
// is the §12.8 step-5 erasure primitive: staged events flushed after the
// user's durable billing rows were pseudonymized (the §12.8 step-15
// PseudonymizeUser the erasure job runs in its pseudonymizing phase)
// would re-insert the raw user_id into Postgres, so the GDPR erasure job
// purges the buffer in its store-deleting phase, before pseudonymization.
//
// spec: §12.8 line 788 (Billing write-ahead buffer purge), step 5.
func (p *Pipeline) PurgeStagedByUser(ctx context.Context, tenantID, userID string) (int, error) {
	streamPurged, err := p.stream.PurgeUser(ctx, tenantID, userID)
	if err != nil {
		return streamPurged, err
	}
	p.mu.Lock()
	kept := make([]billingstore.Event, 0, len(p.buffer))
	bufferPurged := 0
	for _, e := range p.buffer {
		if e.TenantID == tenantID && e.UserID == userID {
			bufferPurged++
			continue
		}
		kept = append(kept, e)
	}
	p.buffer = kept
	p.mu.Unlock()
	return streamPurged + bufferPurged, nil
}

// DeleteByTenant implements billingstore.Store. It removes the tenant's
// durable events through the primary store and drops any of the
// tenant's events still held in the Tier 2 buffer so a tenant teardown
// that races an outage does not later flush deleted-tenant rows. It
// returns the total count removed across both.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (p *Pipeline) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	n, err := p.primary.DeleteByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	kept := p.buffer[:0]
	for _, e := range p.buffer {
		if e.TenantID == tenantID {
			n++
			continue
		}
		kept = append(kept, e)
	}
	p.buffer = kept
	delete(p.seq, tenantID)
	p.mu.Unlock()
	return n, nil
}

// DeleteOlderThan implements billingstore.Store. It prunes the tenant's
// durable events older than cutoff through the primary store and drops
// any of the tenant's buffered Tier 2 events older than cutoff so the
// retention sweep does not later flush rows it should have pruned. It
// returns the total count removed across both.
//
// spec: §11.2.1 line 151. F-11.2.15.
func (p *Pipeline) DeleteOlderThan(ctx context.Context, tenantID string, cutoff time.Time) (int, error) {
	n, err := p.primary.DeleteOlderThan(ctx, tenantID, cutoff)
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	kept := p.buffer[:0]
	for _, e := range p.buffer {
		if e.TenantID == tenantID && e.CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	p.buffer = kept
	p.mu.Unlock()
	return n, nil
}

// nextProvisional returns the next provisional per-tenant sequence
// number for an event that could not reach the primary store.
func (p *Pipeline) nextProvisional(tenantID string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq[tenantID]++
	return p.seq[tenantID]
}

// bufferEvent appends e to the Tier 2 write-ahead buffer. It returns
// ErrBufferFull when the buffer is at capacity, which §11.2.1 maps to a
// 503 on session-progressing requests. The bool reports whether the
// append left the buffer over the §12.3 billingFlushMaxPending
// threshold so the caller can emit billing_flush_pressure and flush.
func (p *Pipeline) bufferEvent(e billingstore.Event) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buffer) >= p.bufferCap {
		return false, ErrBufferFull
	}
	p.buffer = append(p.buffer, e)
	return len(p.buffer) > p.maxPending, nil
}

func (p *Pipeline) setLastTier(t Tier) {
	p.mu.Lock()
	p.lastTier = t
	p.mu.Unlock()
}

// LastTier reports the §11.2.1 tier the most recent Append landed in.
// It is an observability accessor — metrics and tests read it to
// confirm which path an event took.
func (p *Pipeline) LastTier() Tier {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastTier
}

// BufferLen reports the number of events currently held in the Tier 2
// in-memory write-ahead buffer. §11.2.1: the gateway alerts and starts
// rejecting session-progressing requests as this approaches the
// configured buffer size.
func (p *Pipeline) BufferLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buffer)
}

// BufferFull reports whether the Tier 2 buffer is at capacity. The
// gateway consults this to decide whether to reject a new
// session-progressing request with a 503.
func (p *Pipeline) BufferFull() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buffer) >= p.bufferCap
}

// FlushBuffer drains the Tier 2 in-memory write-ahead buffer into the
// primary store. §11.2.1: queued events are flushed in sequence-number
// order once connectivity is restored, preserving the monotonic
// ordering guarantee. The flusher acquires the authoritative Postgres
// sequence number at flush time — the provisional number stamped during
// the outage is dropped — so FlushBuffer re-Appends each event with its
// correction and cost fields intact and lets the primary store assign
// the real sequence.
//
// FlushBuffer stops at the first event the primary store still rejects,
// leaving that event and every later one in the buffer so the next
// flush cycle retries from the same point. It returns the number of
// events successfully flushed and the error that halted the drain (nil
// when the buffer drained completely).
func (p *Pipeline) FlushBuffer(ctx context.Context) (int, error) {
	// Snapshot the buffer under the lock, ordered by provisional
	// sequence so the replay preserves §11.2.1 monotonic ordering.
	p.mu.Lock()
	pending := append([]billingstore.Event(nil), p.buffer...)
	batchSize := p.batchSize
	p.mu.Unlock()
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].TenantID != pending[j].TenantID {
			return pending[i].TenantID < pending[j].TenantID
		}
		return pending[i].SequenceNumber < pending[j].SequenceNumber
	})
	// §12.3 billingFlushBatchSize: drain at most one batch per call so
	// the replay is a bounded multi-row INSERT. RunFlusher loops until
	// the buffer is empty, so a backlog still drains fully.
	if batchSize > 0 && len(pending) > batchSize {
		pending = pending[:batchSize]
	}

	flushed := 0
	for _, e := range pending {
		replay := e
		// §11.2.1: the primary store assigns the authoritative sequence
		// number at flush time; clear the provisional one.
		replay.SequenceNumber = 0
		if _, err := p.primary.Append(ctx, replay); err != nil {
			p.removeFlushed(pending[:flushed])
			return flushed, err
		}
		flushed++
	}
	p.removeFlushed(pending[:flushed])
	return flushed, nil
}

// removeFlushed drops the events in done from the Tier 2 buffer. An
// event is matched by tenant id and provisional sequence number; events
// appended during the flush keep their place.
func (p *Pipeline) removeFlushed(done []billingstore.Event) {
	if len(done) == 0 {
		return
	}
	type key struct {
		tenant string
		seq    uint64
	}
	flushed := make(map[key]bool, len(done))
	for _, e := range done {
		flushed[key{e.TenantID, e.SequenceNumber}] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.buffer[:0]
	for _, e := range p.buffer {
		if flushed[key{e.TenantID, e.SequenceNumber}] {
			continue
		}
		kept = append(kept, e)
	}
	// Re-slice into a fresh backing array so the dropped events are not
	// retained by the underlying capacity.
	p.buffer = append([]billingstore.Event(nil), kept...)
}

// RunFlusher runs the §11.2.1 background flush loop until ctx is
// cancelled. On each tick it drains the Tier 2 write-ahead buffer into
// the primary store. The Tier 1 Redis stream drains itself through its
// own consumer-group flusher in the redisstream implementation; this
// loop owns only the in-process Tier 2 buffer.
func (p *Pipeline) RunFlusher(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Drain the backlog in §12.3 billingFlushBatchSize batches
			// until the buffer empties or a batch fails (primary still
			// down), in which case the next tick retries from the head.
			for p.BufferLen() > 0 {
				flushed, err := p.FlushBuffer(ctx)
				if err != nil || flushed == 0 {
					break
				}
			}
		}
	}
}
