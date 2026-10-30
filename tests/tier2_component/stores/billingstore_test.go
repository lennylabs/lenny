//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2.1 billing event ledger, exercising the
// Postgres-backed pkg/gateway/billingstore/pgstore against a real
// container. Covers the append/round-trip path, the monotonic
// per-tenant sequence_number, the Since replay query with its limit,
// cross-tenant isolation, and the minimum-field validation.
package stores_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// billingTenant seeds a fresh tenant and provisions its per-tenant
// billing Postgres sequence (billing_seq_<40hex>, the §10.2
// safe-derived name) so the store's nextval-based Append/InsertFromStream
// resolve a real sequence object. Production provisions this sequence in
// the §15.1 tenant-create handler; the store test provisions it directly
// on the container pool because freshTenant only inserts the tenants row.
//
// spec: §11.2.1, §10.2, §15.1.
func billingTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	tenant := freshTenant(t, ctx, pg)
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.BillingSequenceName(tenant)+
			" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision billing sequence for %q: %v", tenant, err)
	}
	return tenant
}

// deleteBillingRows removes every billing_events row for tenant through a
// transaction that sets lenny.erasure_mode (the §11.7
// lenny_billing_immutability trigger permits a DELETE only under erasure
// mode) and app.current_tenant (the FORCE ROW LEVEL SECURITY policy). It
// mirrors the store's DeleteByTenant SQL so the regression test can empty
// a tenant's ledger without wiring the lenny_erasure role.
//
// spec: §11.2.1 retention sweep, §12.8 teardown; §11.7 immutability.
func deleteBillingRows(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM billing_events WHERE tenant_id = $1", tenant)
		return err
	})
	if err != nil {
		t.Fatalf("delete billing rows for %q: %v", tenant, err)
	}
}

// spec: 11.2.1
// diagnosis: the Postgres-backed billing event ledger in
// pkg/gateway/billingstore/pgstore did not behave as specified. Append
// must round-trip an event, the per-tenant sequence_number must be
// monotonic, Since must replay events after a sequence and respect its
// limit, the ledger must be isolated per tenant, and an event with no
// event type must be rejected.
func TestBillingStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := billingpg.New(pg.Router(t))
	ctx := context.Background()

	t.Run("append and round-trip an event", func(t *testing.T) {
		tenant := billingTenant(t, ctx, pg)
		want := billingstore.Event{
			TenantID:     tenant,
			UserID:       "alice@acme.com",
			SessionID:    newUUID(t),
			ExperimentID: "exp-1",
			VariantID:    "variant-a",
			EventType:    billingstore.EventSessionCreated,
			TokensInput:  120,
			TokensOutput: 45,
		}
		committed, err := store.Append(ctx, want)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if committed.SequenceNumber != 1 {
			t.Errorf("first event sequence: got %d, want 1", committed.SequenceNumber)
		}
		got, err := store.Since(ctx, tenant, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Since: got %d events, want 1", len(got))
		}
		e := got[0]
		if e.UserID != want.UserID || e.SessionID != want.SessionID ||
			e.ExperimentID != want.ExperimentID || e.VariantID != want.VariantID ||
			e.EventType != want.EventType || e.TokensInput != want.TokensInput ||
			e.TokensOutput != want.TokensOutput {
			t.Errorf("event mismatch:\n got %+v\nwant %+v", e, want)
		}
		if e.SchemaVersion != 1 {
			t.Errorf("SchemaVersion: got %d, want 1", e.SchemaVersion)
		}
		if e.CreatedAt.IsZero() {
			t.Error("CreatedAt was not persisted")
		}
	})

	t.Run("sequence is monotonic and per-tenant", func(t *testing.T) {
		tenantA := billingTenant(t, ctx, pg)
		tenantB := billingTenant(t, ctx, pg)
		for want := uint64(1); want <= 3; want++ {
			got, err := store.Append(ctx, billingstore.Event{
				TenantID: tenantA, EventType: billingstore.EventSessionCreated,
			})
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			if got.SequenceNumber != want {
				t.Errorf("tenant A event: seq %d, want %d", got.SequenceNumber, want)
			}
		}
		first, err := store.Append(ctx, billingstore.Event{
			TenantID: tenantB, EventType: billingstore.EventSessionCreated,
		})
		if err != nil {
			t.Fatalf("Append tenant B: %v", err)
		}
		if first.SequenceNumber != 1 {
			t.Errorf("tenant B's first event: seq %d, want 1 (sequence is per-tenant)", first.SequenceNumber)
		}
	})

	t.Run("Since returns events after a sequence", func(t *testing.T) {
		tenant := billingTenant(t, ctx, pg)
		for i := 0; i < 5; i++ {
			if _, err := store.Append(ctx, billingstore.Event{
				TenantID: tenant, EventType: billingstore.EventSessionCreated,
			}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		got, err := store.Since(ctx, tenant, 2, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("Since(2): got %d events, want 3", len(got))
		}
		for i, e := range got {
			if e.SequenceNumber != uint64(i+3) {
				t.Errorf("event %d: seq %d, want %d", i, e.SequenceNumber, i+3)
			}
		}
	})

	t.Run("Since respects the limit", func(t *testing.T) {
		tenant := billingTenant(t, ctx, pg)
		for i := 0; i < 6; i++ {
			if _, err := store.Append(ctx, billingstore.Event{
				TenantID: tenant, EventType: billingstore.EventSessionCreated,
			}); err != nil {
				t.Fatalf("Append %d: %v", i, err)
			}
		}
		got, err := store.Since(ctx, tenant, 0, 2)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("Since with limit 2: got %d events, want 2", len(got))
		}
	})

	t.Run("the ledger is isolated per tenant", func(t *testing.T) {
		tenantA := billingTenant(t, ctx, pg)
		tenantB := billingTenant(t, ctx, pg)
		if _, err := store.Append(ctx, billingstore.Event{
			TenantID: tenantA, EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Since(ctx, tenantB, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("tenant B must not see tenant A's events, got %d", len(got))
		}
	})

	t.Run("append rejects an event with no event type", func(t *testing.T) {
		tenant := billingTenant(t, ctx, pg)
		_, err := store.Append(ctx, billingstore.Event{TenantID: tenant})
		if !errors.Is(err, billingstore.ErrInvalidEvent) {
			t.Errorf("append without an event type: got %v, want ErrInvalidEvent", err)
		}
	})

	// spec: §11.2.1 — Event schema (all events): the event-type-specific
	// conditional fields round-trip through the conditional_fields JSONB
	// column, and an event that carries none reads back nil (the
	// null/absent contract). F-11.2.12.
	t.Run("conditional_fields round-trip", func(t *testing.T) {
		tenant := billingTenant(t, ctx, pg)
		old := false
		neu := true
		want := billingstore.Event{
			TenantID:  tenant,
			SessionID: newUUID(t),
			EventType: billingstore.EventType("delegation_policy.export_scan_strengthened"),
			Conditional: &billingstore.Conditional{
				PolicyName:           "export-policy",
				TransitionTS:         "2026-06-02T00:00:00Z",
				OldScanExportedFiles: &old,
				NewScanExportedFiles: &neu,
				AffectedPolicyNames:  []string{"p1", "p2"},
			},
		}
		if _, err := store.Append(ctx, want); err != nil {
			t.Fatalf("Append with conditional: %v", err)
		}
		// A second event with no conditional block coexists with the first.
		if _, err := store.Append(ctx, billingstore.Event{
			TenantID: tenant, EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("Append plain: %v", err)
		}
		got, err := store.Since(ctx, tenant, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("Since: got %d events, want 2", len(got))
		}
		c := got[0].Conditional
		if c == nil {
			t.Fatal("conditional_fields dropped on round-trip")
		}
		if c.PolicyName != "export-policy" || c.TransitionTS != "2026-06-02T00:00:00Z" {
			t.Errorf("conditional scalar mismatch: %+v", c)
		}
		if c.OldScanExportedFiles == nil || *c.OldScanExportedFiles != false ||
			c.NewScanExportedFiles == nil || *c.NewScanExportedFiles != true {
			t.Errorf("conditional boolean transitions mismatch: %+v", c)
		}
		if len(c.AffectedPolicyNames) != 2 {
			t.Errorf("affectedPolicyNames mismatch: %+v", c.AffectedPolicyNames)
		}
		if got[1].Conditional != nil {
			t.Errorf("plain event must read back nil Conditional, got %+v", got[1].Conditional)
		}
	})
}

// spec: 11.2.1 (sequencing authority), 10.2 (safe-derived sequence name)
// diagnosis: the §11.2.1 billing sequence_number must be drawn from a
// dedicated per-tenant Postgres sequence whose counter is independent of
// the table rows, so it retains monotonicity across the retention sweep
// and §12.8 teardown deletes. The pre-fix code assigned
// COALESCE(MAX(sequence_number), 0) + 1, which restarts at 1 after every
// row for a tenant is deleted and reuses sequence numbers, breaking the
// gap-detection and replay contract. A failure here means the ledger
// reverted to a MAX+1 scheme (or a nextval sequence that is re-seeded
// backward) that regresses across a full delete. F-11.2.10.
func TestBillingSequenceMonotonicAcrossDeletes(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := billingpg.New(pg.Router(t))
	ctx := context.Background()

	tenant := billingTenant(t, ctx, pg)

	// Advance the sequence past 1 with several appends.
	var last uint64
	for i := 0; i < 3; i++ {
		got, err := store.Append(ctx, billingstore.Event{
			TenantID: tenant, EventType: billingstore.EventSessionCreated,
		})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		last = got.SequenceNumber
	}
	if last != 3 {
		t.Fatalf("pre-delete high-water sequence: got %d, want 3", last)
	}

	// Empty the tenant's ledger, as the §11.2.1 retention sweep or §12.8
	// teardown would. With the pre-fix MAX+1 scheme the next Append would
	// restart at 1.
	deleteBillingRows(t, ctx, pg, tenant)
	if remaining, err := store.Since(ctx, tenant, 0, 0); err != nil {
		t.Fatalf("Since after delete: %v", err)
	} else if len(remaining) != 0 {
		t.Fatalf("ledger not emptied: %d rows remain", len(remaining))
	}

	// The sequence retains its counter across the delete: the next Append
	// yields a value strictly above the pre-delete high-water mark, never
	// a reused low number.
	got, err := store.Append(ctx, billingstore.Event{
		TenantID: tenant, EventType: billingstore.EventSessionCreated,
	})
	if err != nil {
		t.Fatalf("Append after delete: %v", err)
	}
	if got.SequenceNumber <= last {
		t.Errorf("sequence regressed across a full delete: got %d, want > %d "+
			"(a MAX+1 scheme would restart at 1)", got.SequenceNumber, last)
	}
	if got.SequenceNumber != last+1 {
		t.Errorf("sequence did not continue monotonically: got %d, want %d", got.SequenceNumber, last+1)
	}
}

// spec: 11.2.1 (flush-time sequence acquire), 10.2 (safe-derived name)
// diagnosis: §11.2.1 requires the failover flusher to acquire the
// authoritative per-tenant sequence value by nextval at flush time,
// discarding the provisional outage-window stream_seq. InsertFromStream
// must draw sequence_number from the same dedicated per-tenant Postgres
// sequence Append uses, so a reclaimed event interleaves monotonically
// with directly-appended events, and the ON CONFLICT idempotency guard
// must still make a redelivered stream entry a no-op. A failure means
// the flusher stamped a provisional or MAX-derived number rather than
// the sequence value. F-11.2.10.
func TestBillingInsertFromStreamUsesSequence(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := billingpg.New(pg.Router(t))
	ctx := context.Background()

	tenant := billingTenant(t, ctx, pg)

	// A directly-appended event takes sequence 1.
	first, err := store.Append(ctx, billingstore.Event{
		TenantID: tenant, EventType: billingstore.EventSessionCreated,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if first.SequenceNumber != 1 {
		t.Fatalf("first Append sequence: got %d, want 1", first.SequenceNumber)
	}

	// A reclaimed stream event flushes through InsertFromStream and must
	// draw the next sequence value (2) from the same sequence, regardless
	// of any provisional stream_seq it carried during the outage.
	streamed := billingstore.Event{
		TenantID:       tenant,
		EventType:      billingstore.EventSessionCreated,
		SequenceNumber: 999, // provisional stream_seq; must be discarded.
	}
	const entryID = "1680000000000-0"
	if err := store.InsertFromStream(ctx, streamed, entryID); err != nil {
		t.Fatalf("InsertFromStream: %v", err)
	}

	got, err := store.Since(ctx, tenant, 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Since: got %d events, want 2", len(got))
	}
	if got[1].SequenceNumber != 2 {
		t.Errorf("flushed event sequence: got %d, want 2 (flush-time nextval, "+
			"provisional stream_seq discarded)", got[1].SequenceNumber)
	}

	// A redelivered stream entry with the same id is a no-op: the
	// ON CONFLICT (tenant_id, stream_entry_id) guard holds and no third
	// row or extra sequence value is committed.
	if err := store.InsertFromStream(ctx, streamed, entryID); err != nil {
		t.Fatalf("InsertFromStream redelivery: %v", err)
	}
	after, err := store.Since(ctx, tenant, 0, 0)
	if err != nil {
		t.Fatalf("Since after redelivery: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("redelivered stream entry was not idempotent: got %d rows, want 2", len(after))
	}
}

// spec: 11.2.1 (concurrent sequence assignment), 10.2
// diagnosis: §11.2.1 draws sequence_number from nextval, which
// serializes concurrent writers atomically without the per-tenant
// advisory lock the pre-fix code held. Concurrent Append calls for one
// tenant must each receive a distinct sequence value with no duplicate
// (tenant_id, sequence_number) primary-key collision and no lost write.
// A failure means the sequence assignment is not atomic under
// concurrency (a MAX+1 read without a lock would collide). This is the
// tier-7a-class atomicity property, exercised at the store tier because
// it needs a real Postgres sequence rather than Kubernetes primitives.
// F-11.2.10.
func TestBillingSequenceConcurrentAppendNoCollision(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := billingpg.New(pg.Router(t))
	ctx := context.Background()

	tenant := billingTenant(t, ctx, pg)

	const writers = 32
	seqs := make(chan uint64, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			got, err := store.Append(ctx, billingstore.Event{
				TenantID: tenant, EventType: billingstore.EventSessionCreated,
			})
			if err != nil {
				errs <- err
				return
			}
			seqs <- got.SequenceNumber
		}()
	}
	wg.Wait()
	close(seqs)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Append: %v", err)
	}

	seen := make(map[uint64]bool, writers)
	for s := range seqs {
		if seen[s] {
			t.Errorf("duplicate sequence_number %d assigned under concurrency", s)
		}
		seen[s] = true
	}
	if len(seen) != writers {
		t.Fatalf("concurrent Append lost writes: got %d distinct sequences, want %d", len(seen), writers)
	}

	// Every committed row is readable and the assigned values form the
	// contiguous 1..writers band nextval hands out with no rollbacks.
	rows, err := store.Since(ctx, tenant, 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(rows) != writers {
		t.Fatalf("Since: got %d rows, want %d", len(rows), writers)
	}
	for i, e := range rows {
		if e.SequenceNumber != uint64(i+1) {
			t.Errorf("row %d: sequence %d, want %d (contiguous nextval band)", i, e.SequenceNumber, i+1)
		}
	}
}
