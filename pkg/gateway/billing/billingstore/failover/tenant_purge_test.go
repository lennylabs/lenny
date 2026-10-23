// SPDX-License-Identifier: MIT

// Coverage for the tenant-deletion Phase 4 erasure of the billing
// write-ahead buffer: the Tier 1 stream purge (MemStream.PurgeTenant) and
// its ordering inside Pipeline.DeleteByTenant, which purges the stream
// before the durable rows so a stream flusher cannot re-insert a deleted
// tenant's row, and returns before the durable delete when the purge fails.
package failover_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
)

// orderingStream is a StreamTier over a MemStream that records, at the
// moment PurgeTenant runs, how many durable events the primary still holds
// for the tenant, and can be made to fail the purge.
type orderingStream struct {
	*failover.MemStream
	primary *flakyStore

	purgeErr          error
	purgeCalls        int
	primaryAtPurge    int
	primaryAtPurgeSet bool
}

func (s *orderingStream) PurgeTenant(ctx context.Context, tenantID string) (int, error) {
	s.purgeCalls++
	s.primaryAtPurge = s.primary.committed(tenantID)
	s.primaryAtPurgeSet = true
	if s.purgeErr != nil {
		return 0, s.purgeErr
	}
	return s.MemStream.PurgeTenant(ctx, tenantID)
}

// stageAcrossTiers lands one acme event in the primary, then one acme and
// one globex event in each of the Tier 1 stream and the Tier 2 buffer.
func stageAcrossTiers(t *testing.T, p *failover.Pipeline, primary *flakyStore, stream *failover.MemStream) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.Append(ctx, sessionEvent("acme", "s1")); err != nil {
		t.Fatalf("primary Append: %v", err)
	}
	primary.setDown(true)
	for _, e := range []billingstore.Event{sessionEvent("acme", "s2"), sessionEvent("globex", "s3")} {
		if _, err := p.Append(ctx, e); err != nil {
			t.Fatalf("stream Append: %v", err)
		}
	}
	stream.SetUnavailable(errors.New("stream unavailable"))
	for _, e := range []billingstore.Event{sessionEvent("acme", "s4"), sessionEvent("globex", "s5")} {
		if _, err := p.Append(ctx, e); err != nil {
			t.Fatalf("buffered Append: %v", err)
		}
	}
	stream.SetUnavailable(nil)
	primary.setDown(false)
	if pending, _ := stream.Pending(ctx); pending != 2 || p.BufferLen() != 2 || primary.committed("acme") != 1 {
		t.Fatalf("staging: stream=%d buffer=%d primary(acme)=%d, want 2, 2, 1", pending, p.BufferLen(), primary.committed("acme"))
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4)
// MemStream.PurgeTenant removes the tenant's staged events only, and a
// repeated purge removes nothing and succeeds.
func TestMemStreamPurgeTenantRemovesOnlyTenant_spec_12_8(t *testing.T) {
	ctx := context.Background()
	m := failover.NewMemStream()
	for _, e := range []billingstore.Event{
		sessionEvent("acme", "s1"), sessionEvent("acme", "s2"), sessionEvent("globex", "s3"),
	} {
		if err := m.Publish(ctx, e); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	n, err := m.PurgeTenant(ctx, "acme")
	if err != nil || n != 2 {
		t.Fatalf("PurgeTenant = (%d, %v), want (2, nil)", n, err)
	}
	if pending, _ := m.Pending(ctx); pending != 1 {
		t.Fatalf("pending = %d, want 1 (globex survives)", pending)
	}
	if n, err := m.PurgeTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeated PurgeTenant = (%d, %v), want (0, nil)", n, err)
	}
	// The surviving event is globex's: a drain commits it under globex.
	primary := newFlakyStore()
	if _, err := m.Drain(ctx, primary); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if primary.committed("globex") != 1 || primary.committed("acme") != 0 {
		t.Fatalf("drained globex=%d acme=%d, want 1 and 0", primary.committed("globex"), primary.committed("acme"))
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4),
// 12.1. Pipeline.DeleteByTenant purges the tenant's Tier 1 stream before
// it deletes the durable rows, drops the tenant's Tier 2 buffered events,
// counts all three, and leaves the other tenant's staged events in place.
func TestPipelineDeleteByTenantPurgesStreamBeforeDurableRows_spec_12_8(t *testing.T) {
	ctx := context.Background()
	primary := newFlakyStore()
	stream := &orderingStream{MemStream: failover.NewMemStream(), primary: primary}
	p := failover.New(failover.Options{Primary: primary, Stream: stream})
	stageAcrossTiers(t, p, primary, stream.MemStream)

	n, err := p.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 3 {
		t.Fatalf("DeleteByTenant removed %d, want 3 (stream + primary + buffer)", n)
	}
	if stream.purgeCalls != 1 || !stream.primaryAtPurgeSet || stream.primaryAtPurge != 1 {
		t.Fatalf("purge calls=%d, primary acme rows at purge=%d; want one purge that ran before the durable delete", stream.purgeCalls, stream.primaryAtPurge)
	}
	if primary.committed("acme") != 0 {
		t.Fatalf("acme durable rows = %d, want 0", primary.committed("acme"))
	}
	if pending, _ := stream.Pending(ctx); pending != 1 {
		t.Fatalf("stream pending = %d, want 1 (globex's)", pending)
	}
	if p.BufferLen() != 1 {
		t.Fatalf("buffer = %d, want 1 (globex's)", p.BufferLen())
	}
	// Draining both tiers commits globex's events and no acme event.
	if _, err := stream.Drain(ctx, primary); err != nil {
		t.Fatalf("stream Drain: %v", err)
	}
	if _, err := p.FlushBuffer(ctx); err != nil {
		t.Fatalf("FlushBuffer: %v", err)
	}
	if primary.committed("acme") != 0 || primary.committed("globex") != 2 {
		t.Fatalf("after drain acme=%d globex=%d, want 0 and 2", primary.committed("acme"), primary.committed("globex"))
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// A stream purge error returns before the durable delete: the tenant's
// durable rows and its buffered events stay, and the error is surfaced so
// the deletion pauses and retries the step.
func TestPipelineDeleteByTenantStreamPurgeErrorKeepsDurableRows_spec_12_8(t *testing.T) {
	ctx := context.Background()
	primary := newFlakyStore()
	purgeErr := errors.New("redis unavailable")
	stream := &orderingStream{MemStream: failover.NewMemStream(), primary: primary, purgeErr: purgeErr}
	p := failover.New(failover.Options{Primary: primary, Stream: stream})
	stageAcrossTiers(t, p, primary, stream.MemStream)

	n, err := p.DeleteByTenant(ctx, "acme")
	if !errors.Is(err, purgeErr) {
		t.Fatalf("DeleteByTenant err = %v, want it to wrap the purge error", err)
	}
	if n != 0 {
		t.Fatalf("DeleteByTenant count = %d on error, want 0", n)
	}
	if primary.committed("acme") != 1 {
		t.Fatalf("acme durable rows = %d, want 1 (the delete must not run after a purge error)", primary.committed("acme"))
	}
	if p.BufferLen() != 2 {
		t.Fatalf("buffer = %d, want 2 (untouched after a purge error)", p.BufferLen())
	}
	if pending, _ := stream.Pending(ctx); pending != 2 {
		t.Fatalf("stream pending = %d, want 2", pending)
	}
}
