// SPDX-License-Identifier: MIT

package failover_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover"
)

var pipelineSalt = []byte("0123456789abcdef0123456789abcdef")

// spec: §12.8 — PseudonymizeUser rewrites the durable ledger through the
// primary AND any of the user's events still in the Tier 2 buffer, so a
// user erasure that races a primary outage does not leak the user id
// into events the flusher will later commit.
func TestPipelinePseudonymizeCoversBuffer_spec_12_8(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary}) // no stream → Tier 2 buffer.
	ctx := context.Background()

	// One event commits to the primary while it is healthy.
	if _, err := p.Append(ctx, sessionEvent("acme", "s1")); err != nil {
		t.Fatalf("primary Append: %v", err)
	}
	// The primary goes down; the next event lands in the Tier 2 buffer.
	primary.setDown(true)
	if _, err := p.Append(ctx, sessionEvent("acme", "s2")); err != nil {
		t.Fatalf("buffered Append: %v", err)
	}
	if p.BufferLen() != 1 {
		t.Fatalf("buffer should hold 1 event, has %d", p.BufferLen())
	}
	primary.setDown(false)

	n, err := p.PseudonymizeUser(ctx, "acme", "alice@acme", pipelineSalt)
	if err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}
	// One primary event + one buffered event, both alice's. A count of 2
	// proves the buffer branch matched and rewrote the buffered event;
	// had the Pipeline only delegated to the primary, the count would be 1.
	if n != 2 {
		t.Fatalf("PseudonymizeUser rewrote %d events, want 2 (primary + buffer)", n)
	}
	// The durable (primary) event carries the pseudonym, not the original id.
	want := billingstore.Pseudonymize("alice@acme", pipelineSalt)
	events, _ := p.Since(ctx, "acme", 0, 0)
	for _, e := range events {
		if e.UserID != want {
			t.Fatalf("primary event %d not pseudonymized: UserID=%q, want %q", e.SequenceNumber, e.UserID, want)
		}
	}
}

// spec: §12.1 line 5, §12.8 Phase 4 — DeleteByTenant removes the
// tenant's durable events and drops the tenant's buffered events so a
// teardown that races an outage does not flush deleted-tenant rows.
func TestPipelineDeleteByTenantDropsBuffer_spec_12_1(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary}) // no stream → Tier 2 buffer.
	ctx := context.Background()

	if _, err := p.Append(ctx, sessionEvent("acme", "s1")); err != nil {
		t.Fatalf("primary Append: %v", err)
	}
	primary.setDown(true)
	if _, err := p.Append(ctx, sessionEvent("acme", "s2")); err != nil {
		t.Fatalf("buffered acme Append: %v", err)
	}
	if _, err := p.Append(ctx, sessionEvent("globex", "s3")); err != nil {
		t.Fatalf("buffered globex Append: %v", err)
	}
	if p.BufferLen() != 2 {
		t.Fatalf("buffer should hold 2 events, has %d", p.BufferLen())
	}
	primary.setDown(false)

	n, err := p.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	// One primary acme event + one buffered acme event.
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d events, want 2", n)
	}
	if p.BufferLen() != 1 {
		t.Fatalf("buffer should retain globex's event, has %d", p.BufferLen())
	}
	if primary.committed("acme") != 0 {
		t.Fatalf("acme still has %d primary events, want 0", primary.committed("acme"))
	}
}

// spec: §12.1 line 5 — billing DeleteByUser is the documented no-op; the
// Pipeline delegates to the primary and reports (0, nil).
func TestPipelineDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	primary := newFlakyStore()
	p := failover.New(failover.Options{Primary: primary})
	ctx := context.Background()
	if _, err := p.Append(ctx, sessionEvent("acme", "s1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	n, err := p.DeleteByUser(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteByUser removed %d, want 0", n)
	}
	if primary.committed("acme") != 1 {
		t.Fatalf("event count = %d, want 1 (no-op)", primary.committed("acme"))
	}
}
