// SPDX-License-Identifier: MIT

// Internal coverage for the §12.8 step-5 billing write-ahead buffer
// erasure: PurgeStagedByUser must drop the target user's staged events
// from both the Tier 1 stream and the Tier 2 in-memory buffer, leaving
// every other user's and tenant's staged events intact so a later flush
// cannot re-insert the erased user's raw user_id into Postgres.
//
// spec: §12.8 line 788 (Billing write-ahead buffer), step 5.
package failover

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

func purgeEvent(tenant, user string, seq uint64) billingstore.Event {
	return billingstore.Event{TenantID: tenant, UserID: user, SequenceNumber: seq, EventType: billingstore.EventSessionCreated}
}

func TestPurgeStagedByUser_PurgesBufferAndStream_spec_12_8_788(t *testing.T) {
	ctx := context.Background()
	mem := NewMemStream()
	p := New(Options{Primary: billingstore.NewMemory(), Stream: mem})

	// Tier 1 stream: two acme users plus a same-name user in another tenant.
	for _, e := range []billingstore.Event{
		purgeEvent("acme", "alice", 1),
		purgeEvent("acme", "bob", 2),
		purgeEvent("globex", "alice", 3),
	} {
		if err := mem.Publish(ctx, e); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	// Tier 2 buffer: an outage residue holding one alice and one bob event.
	p.mu.Lock()
	p.buffer = []billingstore.Event{purgeEvent("acme", "alice", 4), purgeEvent("acme", "bob", 5)}
	p.mu.Unlock()

	n, err := p.PurgeStagedByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("PurgeStagedByUser: %v", err)
	}
	// One acme/alice in the stream + one acme/alice in the buffer.
	if n != 2 {
		t.Fatalf("purged = %d, want 2", n)
	}

	// The buffer retains only the bob event.
	p.mu.Lock()
	gotBuf := append([]billingstore.Event(nil), p.buffer...)
	p.mu.Unlock()
	if len(gotBuf) != 1 || gotBuf[0].UserID != "bob" {
		t.Fatalf("buffer after purge = %+v, want only acme/bob", gotBuf)
	}

	// The stream retains acme/bob and globex/alice (cross-tenant survivor).
	pending, err := mem.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 2 {
		t.Fatalf("stream pending = %d, want 2 (acme/bob, globex/alice)", pending)
	}
}

func TestMemStreamPurgeUser_ScopedToTenantAndUser_spec_12_8_788(t *testing.T) {
	ctx := context.Background()
	m := NewMemStream()
	for _, e := range []billingstore.Event{
		purgeEvent("acme", "alice", 1),
		purgeEvent("acme", "alice", 2),
		purgeEvent("acme", "bob", 3),
		purgeEvent("globex", "alice", 4),
	} {
		if err := m.Publish(ctx, e); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	n, err := m.PurgeUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("PurgeUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged = %d, want 2", n)
	}
	pending, _ := m.Pending(ctx)
	if pending != 2 {
		t.Fatalf("pending = %d, want 2 (acme/bob, globex/alice)", pending)
	}
}
