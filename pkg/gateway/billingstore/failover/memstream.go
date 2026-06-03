// SPDX-License-Identifier: MIT

package failover

import (
	"context"
	"sort"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// MemStream is an in-process StreamTier. It models the §11.2.1 Tier 1
// durable stream without Redis: Publish enqueues an event, Drain
// replays the queued events into the primary store in sequence order.
//
// MemStream is the single-node and test stand-in for the Redis stream.
// It is not durable across a process restart, so a multi-replica
// production deployment wires failover/redisstream instead; MemStream
// gives a single-replica deployment the same two-tier code path without
// a Redis dependency, and it backs the pipeline's unit tests.
type MemStream struct {
	mu     sync.Mutex
	events []billingstore.Event
	// failPublish, when set, makes Publish return it — tests use this to
	// drive the pipeline down to Tier 2.
	failPublish error
}

var _ StreamTier = (*MemStream)(nil)

// NewMemStream returns an empty in-process stream tier.
func NewMemStream() *MemStream { return &MemStream{} }

// Publish implements StreamTier. It enqueues e unless the stream has
// been put into a failing state via SetUnavailable.
func (m *MemStream) Publish(_ context.Context, e billingstore.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPublish != nil {
		return m.failPublish
	}
	m.events = append(m.events, e)
	return nil
}

// Pending implements StreamTier: the count of events queued in the
// stream that have not yet been drained to the primary store.
func (m *MemStream) Pending(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events), nil
}

// SetUnavailable makes Publish return err (Redis-unavailable
// simulation). Pass nil to restore normal operation.
func (m *MemStream) SetUnavailable(err error) {
	m.mu.Lock()
	m.failPublish = err
	m.mu.Unlock()
}

// PurgeUser implements StreamTier. It drops every queued event for
// userID within tenantID and returns the count removed — the §12.8
// step-5 erasure of the in-process stream's staged events.
//
// spec: §12.8 line 788 (Billing write-ahead buffer), step 5.
func (m *MemStream) PurgeUser(_ context.Context, tenantID, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]billingstore.Event, 0, len(m.events))
	purged := 0
	for _, e := range m.events {
		if e.TenantID == tenantID && e.UserID == userID {
			purged++
			continue
		}
		kept = append(kept, e)
	}
	m.events = kept
	return purged, nil
}

// Drain replays every queued event into the primary store in
// provisional-sequence order and removes the events it successfully
// flushed. This models the §11.2.1 billing-flusher consumer group: on a
// successful primary INSERT the entry is acknowledged and deleted from
// the stream. The primary store assigns the authoritative sequence
// number, so Drain clears the provisional one before re-Appending.
//
// Drain stops at the first event the primary store still rejects,
// leaving it and every later event queued for the next cycle. It
// returns the number of events flushed and the error that halted the
// drain (nil on a complete drain).
func (m *MemStream) Drain(ctx context.Context, primary billingstore.Store) (int, error) {
	m.mu.Lock()
	pending := append([]billingstore.Event(nil), m.events...)
	m.mu.Unlock()
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].TenantID != pending[j].TenantID {
			return pending[i].TenantID < pending[j].TenantID
		}
		return pending[i].SequenceNumber < pending[j].SequenceNumber
	})

	flushed := 0
	for _, e := range pending {
		replay := e
		replay.SequenceNumber = 0
		if _, err := primary.Append(ctx, replay); err != nil {
			m.removeFlushed(pending[:flushed])
			return flushed, err
		}
		flushed++
	}
	m.removeFlushed(pending[:flushed])
	return flushed, nil
}

// removeFlushed drops the drained events from the queue, matched by
// tenant id and provisional sequence number.
func (m *MemStream) removeFlushed(done []billingstore.Event) {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]billingstore.Event, 0, len(m.events))
	for _, e := range m.events {
		if flushed[key{e.TenantID, e.SequenceNumber}] {
			continue
		}
		kept = append(kept, e)
	}
	m.events = kept
}
