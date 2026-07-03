//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.7 Postgres audit chain
// (pkg/gateway/auditstore), exercised against a real Postgres
// container. Covers append + per-tenant sequence + prev_hash
// chaining, verification, tamper detection via the chain link,
// cross-tenant isolation, and the Get / ErrNotFound path.
package auditstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// provisionAuditSequence creates the tenant's per-tenant audit Postgres
// sequence (audit_seq_<40hex>, the §10.2 safe-derived name) so the
// store's nextval-based sealAndInsert resolves a real sequence object.
// Production provisions this sequence at tenant-creation time through the
// gateway runtime-DDL path; the store tests provision it directly on the
// container pool because they only seed the tenants row.
//
// spec: §11.7, §10.2.
func provisionAuditSequence(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(id)+
			" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence for %q: %v", id, err)
	}
}

func seedTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	provisionAuditSequence(t, ctx, pg, id)
}

// spec: 11.7
// diagnosis: the Postgres audit chain in pkg/gateway/auditstore did
// not behave as specified. Append must build a verifiable per-tenant
// chain with monotonic sequence numbers and prev_hash links, Verify
// must detect a tampered row, chains must be isolated per tenant, and
// Get must return ErrNotFound for an absent sequence number.
func TestAuditStoreContract(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()

	t.Run("append builds a verifiable per-tenant chain", func(t *testing.T) {
		seedTenant(t, ctx, pg, "acme")
		var appended []audit.Row
		for i, et := range []string{"admin.tenant.created", "admin.runtime.created", "admin.user.created"} {
			payload := json.RawMessage(`{"i":` + string(rune('0'+i)) + `}`)
			row, err := store.Append(ctx, "acme", et, payload, time.Now())
			if err != nil {
				t.Fatalf("Append %s: %v", et, err)
			}
			if row.Seq != uint64(i+1) {
				t.Errorf("Append %s seq = %d, want %d", et, row.Seq, i+1)
			}
			appended = append(appended, row)
		}
		if appended[0].PrevHash != audit.GenesisPrevHash {
			t.Errorf("genesis prev_hash = %q, want the sentinel", appended[0].PrevHash)
		}
		if appended[1].PrevHash != audit.LinkHash(appended[0]) {
			t.Error("row 2 prev_hash does not link to row 1")
		}

		rows, err := store.Rows(ctx, "acme")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("Rows returned %d, want 3", len(rows))
		}
		if rows[0].EventType != "admin.tenant.created" || rows[2].Seq != 3 {
			t.Errorf("rows not in sequence order: %+v", rows)
		}
		res, err := store.Verify(ctx, "acme")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainVerified {
			t.Errorf("Verify = %q (%s), want verified", res.Integrity, res.Detail)
		}
	})

	t.Run("verify detects a tampered row via the chain link", func(t *testing.T) {
		seedTenant(t, ctx, pg, "globex")
		for _, et := range []string{"e1", "e2", "e3"} {
			if _, err := store.Append(ctx, "globex", et, json.RawMessage(`{"v":1}`), time.Now()); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		// Tamper row 2's payload in place. The append-only triggers
		// permit an UPDATE only under erasure mode, so the tamper
		// transaction sets both the tenant context and the erasure
		// guard — and the tampered row still breaks the chain at row 3.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tamper tx: %v", err)
		}
		_, _ = tx.Exec(ctx, "SELECT set_config('app.current_tenant', 'globex', true)")
		_, _ = tx.Exec(ctx, "SELECT set_config('lenny.erasure_mode', 'true', true)")
		if _, err := tx.Exec(ctx,
			`UPDATE audit_log SET payload = '{"v":999}'::jsonb
			 WHERE tenant_id = 'globex' AND sequence_number = 2`); err != nil {
			t.Fatalf("tamper UPDATE: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit tamper tx: %v", err)
		}
		res, err := store.Verify(ctx, "globex")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainBroken {
			t.Errorf("Verify of a tampered chain = %q, want broken", res.Integrity)
		}
	})

	t.Run("chains are isolated per tenant", func(t *testing.T) {
		seedTenant(t, ctx, pg, "initech")
		if _, err := store.Append(ctx, "initech", "e1", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// A tenant with no audit rows verifies as an empty chain.
		seedTenant(t, ctx, pg, "umbrella")
		rows, err := store.Rows(ctx, "umbrella")
		if err != nil || len(rows) != 0 {
			t.Errorf("Rows for an unused tenant = %d rows (err %v), want 0", len(rows), err)
		}
		res, err := store.Verify(ctx, "umbrella")
		if err != nil || res.Integrity != audit.ChainVerified {
			t.Errorf("Verify of an empty chain = %q (err %v), want verified", res.Integrity, err)
		}
	})

	t.Run("get returns a row or ErrNotFound", func(t *testing.T) {
		seedTenant(t, ctx, pg, "hooli")
		if _, err := store.Append(ctx, "hooli", "only", json.RawMessage(`{"x":1}`), time.Now()); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Get(ctx, "hooli", 1)
		if err != nil {
			t.Fatalf("Get(1): %v", err)
		}
		if got.EventType != "only" || got.Seq != 1 {
			t.Errorf("Get(1) = %+v", got)
		}
		if _, err := store.Get(ctx, "hooli", 99); !errors.Is(err, auditstore.ErrNotFound) {
			t.Errorf("Get(99): got %v, want ErrNotFound", err)
		}
	})
}

// spec: §16.4 lines 378-382 — the retention pruner deletes rows past
// audit.retentionDays, holds gdpr.* erasure receipts (separate window),
// and honors the SIEM delivery guard (no row past the forwarder's
// high-water mark is dropped unless forced). F-11.7.17.
// diagnosis: a failure means the retention pruner deletes the wrong
// rows: either it drops gdpr.* erasure receipts early, or it drops rows
// the SIEM forwarder has not yet acknowledged, breaching the §16.4
// delivery guard.
func TestPruneRetentionWindowsAndSIEMGuard(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "retain")

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)    // ~400 days before cutoff
	recent := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // inside the window
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// seq 1,2: old non-gdpr; seq 3: old gdpr.* receipt; seq 4: recent.
	mustAppend(t, ctx, store, "retain", "session.created", old)
	mustAppend(t, ctx, store, "retain", "session.created", old)
	mustAppend(t, ctx, store, "retain", "gdpr.erasure_completed", old)
	mustAppend(t, ctx, store, "retain", "session.created", recent)

	// SIEM forwarder has acknowledged only up to sequence 1.
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO siem_delivery_state (tenant_id, last_acked_sequence) VALUES ('retain', 1)`); err != nil {
		t.Fatalf("seed siem_delivery_state: %v", err)
	}

	// With the SIEM guard active and no force, only seq 1 (at or below
	// the high-water mark) is eligible; seq 2 is held even though it is
	// past the window, seq 3 is gdpr-exempt, seq 4 is recent.
	n, err := store.PruneRetention(ctx, "retain", auditstore.PruneOptions{
		GeneralCutoff:  cutoff,
		SIEMConfigured: true,
	})
	if err != nil {
		t.Fatalf("PruneRetention (guarded): %v", err)
	}
	if n != 1 {
		t.Fatalf("guarded prune deleted %d rows, want 1 (SIEM guard holds seq 2)", n)
	}
	if seqs := remainingSeqs(t, ctx, store, "retain"); !equalSeqs(seqs, []uint64{2, 3, 4}) {
		t.Fatalf("after guarded prune remaining seqs = %v, want [2 3 4]", seqs)
	}

	// Forcing past the guard deletes seq 2; the gdpr.* receipt (seq 3)
	// and the recent row (seq 4) survive.
	n, err = store.PruneRetention(ctx, "retain", auditstore.PruneOptions{
		GeneralCutoff:  cutoff,
		SIEMConfigured: true,
		Force:          true,
	})
	if err != nil {
		t.Fatalf("PruneRetention (forced): %v", err)
	}
	if n != 1 {
		t.Fatalf("forced prune deleted %d rows, want 1 (seq 2)", n)
	}
	if seqs := remainingSeqs(t, ctx, store, "retain"); !equalSeqs(seqs, []uint64{3, 4}) {
		t.Fatalf("after forced prune remaining seqs = %v, want [3 4]", seqs)
	}

	// A GDPR cutoff past the receipt's age drops it too.
	n, err = store.PruneRetention(ctx, "retain", auditstore.PruneOptions{
		GeneralCutoff: cutoff,
		GDPRCutoff:    cutoff,
		Force:         true,
	})
	if err != nil {
		t.Fatalf("PruneRetention (gdpr): %v", err)
	}
	if n != 1 {
		t.Fatalf("gdpr prune deleted %d rows, want 1 (seq 3)", n)
	}
	if seqs := remainingSeqs(t, ctx, store, "retain"); !equalSeqs(seqs, []uint64{4}) {
		t.Fatalf("after gdpr prune remaining seqs = %v, want [4]", seqs)
	}
}

// spec: §16.7 line 687 — RetentionWindowStats summarizes the
// non-gdpr.* rows older than the cutoff and reads the SIEM high-water
// mark for the audit.partition_drop_forced payload. F-11.7.17.
// diagnosis: a failure means RetentionWindowStats miscounts the
// droppable window or misreports the SIEM high-water mark, so the
// audit.partition_drop_forced payload would carry wrong numbers.
func TestRetentionWindowStats(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "stats")

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	older := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	mustAppend(t, ctx, store, "stats", "session.created", older)
	mustAppend(t, ctx, store, "stats", "session.created", old)
	mustAppend(t, ctx, store, "stats", "gdpr.erasure_completed", old) // excluded from stats
	mustAppend(t, ctx, store, "stats", "session.created", recent)     // inside window, excluded
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO siem_delivery_state (tenant_id, last_acked_sequence) VALUES ('stats', 7)`); err != nil {
		t.Fatalf("seed siem_delivery_state: %v", err)
	}

	win, err := store.RetentionWindowStats(ctx, "stats", cutoff)
	if err != nil {
		t.Fatalf("RetentionWindowStats: %v", err)
	}
	if win.Count != 2 {
		t.Errorf("Count = %d, want 2 (two old non-gdpr rows)", win.Count)
	}
	if !win.OldestEvent.Equal(older) {
		t.Errorf("OldestEvent = %v, want %v", win.OldestEvent, older)
	}
	if !win.NewestEvent.Equal(old) {
		t.Errorf("NewestEvent = %v, want %v", win.NewestEvent, old)
	}
	if !win.SIEMStateExists || win.SIEMHighWater != 7 {
		t.Errorf("SIEM high-water = %d (exists=%v), want 7/true", win.SIEMHighWater, win.SIEMStateExists)
	}
}

// spec: §16.4 line 378 — SIEMHeldCount reports the non-gdpr.* rows past
// the retention cutoff that the SIEM delivery guard is withholding from
// the drop (sequence_number above the forwarder's high-water mark). A
// non-zero result is the AuditPartitionDropBlocked condition. F-16.4.6.
// diagnosis: a failure means SIEMHeldCount miscounts rows withheld by
// the SIEM delivery guard, so AuditPartitionDropBlocked would fire or
// clear incorrectly and a partition drop could lose undelivered rows.
func TestSIEMHeldCount(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "held")

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// seq 1,2,3: old non-gdpr; seq 4: old gdpr.* receipt (guard-exempt);
	// seq 5: recent (inside the window, not yet eligible to drop).
	mustAppend(t, ctx, store, "held", "session.created", old)
	mustAppend(t, ctx, store, "held", "session.created", old)
	mustAppend(t, ctx, store, "held", "session.created", old)
	mustAppend(t, ctx, store, "held", "gdpr.erasure_completed", old)
	mustAppend(t, ctx, store, "held", "session.created", recent)

	// No siem_delivery_state row: the forwarder has acked nothing
	// (implicit high-water 0), so all three old non-gdpr rows are held.
	if got, err := store.SIEMHeldCount(ctx, "held", cutoff); err != nil {
		t.Fatalf("SIEMHeldCount (no state): %v", err)
	} else if got != 3 {
		t.Fatalf("held (no state) = %d, want 3 (seqs 1-3; gdpr seq 4 and recent seq 5 excluded)", got)
	}

	// Forwarder advances to sequence 2: only seq 3 remains past-TTL and
	// above the high-water mark.
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO siem_delivery_state (tenant_id, last_acked_sequence) VALUES ('held', 2)`); err != nil {
		t.Fatalf("seed siem_delivery_state: %v", err)
	}
	if got, err := store.SIEMHeldCount(ctx, "held", cutoff); err != nil {
		t.Fatalf("SIEMHeldCount (acked 2): %v", err)
	} else if got != 1 {
		t.Fatalf("held (acked 2) = %d, want 1 (only seq 3)", got)
	}

	// Forwarder catches up past every old row: nothing is held.
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE siem_delivery_state SET last_acked_sequence = 5 WHERE tenant_id = 'held'`); err != nil {
		t.Fatalf("advance siem_delivery_state: %v", err)
	}
	if got, err := store.SIEMHeldCount(ctx, "held", cutoff); err != nil {
		t.Fatalf("SIEMHeldCount (acked 5): %v", err)
	} else if got != 0 {
		t.Fatalf("held (acked 5) = %d, want 0 (forwarder caught up)", got)
	}

	// An empty tenant id is rejected; a zero cutoff is rejected.
	if _, err := store.SIEMHeldCount(ctx, "", cutoff); err == nil {
		t.Error("SIEMHeldCount with empty tenant must error")
	}
	if _, err := store.SIEMHeldCount(ctx, "held", time.Time{}); err == nil {
		t.Error("SIEMHeldCount with zero cutoff must error")
	}
}

func mustAppend(t *testing.T, ctx context.Context, store *auditstore.Store, tenant, et string, at time.Time) {
	t.Helper()
	if _, err := store.Append(ctx, tenant, et, json.RawMessage(`{"x":1}`), at); err != nil {
		t.Fatalf("Append %s: %v", et, err)
	}
}

func remainingSeqs(t *testing.T, ctx context.Context, store *auditstore.Store, tenant string) []uint64 {
	t.Helper()
	rows, err := store.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	out := make([]uint64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Seq)
	}
	return out
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// deleteAuditRows empties tenant's audit_log through a transaction that
// sets lenny.erasure_mode (the §11.7 lenny_audit_immutability trigger
// permits a DELETE only under erasure mode) and app.current_tenant (the
// FORCE ROW LEVEL SECURITY policy). It mirrors the store's DeleteByTenant
// SQL so the regression test can simulate a §16.4 retention sweep or a
// §12.8 teardown without wiring the lenny_erasure role. The per-tenant
// audit_seq_<40hex> sequence is deliberately left intact, as it is in
// production: the sequence object is not dropped by a retention sweep.
//
// spec: §11.7 (immutability), §16.4 (retention sweep), §12.8 (teardown).
func deleteAuditRows(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM audit_log WHERE tenant_id = $1", tenant)
		return err
	})
	if err != nil {
		t.Fatalf("delete audit rows for %q: %v", tenant, err)
	}
}

// spec: 11.7 (authoritative total order), 10.2 (safe-derived name)
// diagnosis: the §11.7 audit sequence_number must be drawn by nextval on
// a dedicated per-tenant Postgres sequence whose counter is independent
// of the table rows, so it retains monotonicity across the §16.4
// retention sweep and §12.8 teardown deletes. This is the S6 fix: both
// the empty-table genesis branch and the non-empty tail branch of
// sealAndInsert must draw nextval. A failure here means the store
// reverted to a dense tail ordinal (row.Seq = tail.Seq + 1 with a
// literal 1 at genesis), which restarts at 1 after a full sweep and
// reuses sequence numbers, placing a reactivated tenant's events at or
// below the SIEM delivery high-water mark. This test would fail against
// the pre-S6 code AND against a partial fix that switched only the tail
// branch (the genesis literal 1 would collide with the fresh sequence's
// first nextval). F-11.2.10.
func TestAuditSequenceMonotonicAcrossSweep(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "swept")

	// Advance the chain past 1 with several appends.
	var last uint64
	for i, et := range []string{"session.created", "credential.leased", "session.completed"} {
		row, err := store.Append(ctx, "swept", et, json.RawMessage(`{"i":`+string(rune('0'+i))+`}`), time.Now())
		if err != nil {
			t.Fatalf("Append %s: %v", et, err)
		}
		last = row.Seq
	}
	if last != 3 {
		t.Fatalf("pre-sweep high-water sequence: got %d, want 3", last)
	}

	// Empty the chain, as the §16.4 retention sweep or §12.8 teardown
	// would. The sequence object survives the delete. With the pre-S6
	// dense-ordinal code the next Append reads an empty tail and restarts
	// at the genesis literal 1.
	deleteAuditRows(t, ctx, pg, "swept")
	if rows, err := store.Rows(ctx, "swept"); err != nil {
		t.Fatalf("Rows after sweep: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("chain not emptied: %d rows remain", len(rows))
	}

	// The post-sweep first write is the genesis row (it links to the
	// genesis sentinel because the table is empty) yet its sequence_number
	// continues above the pre-sweep high-water mark rather than restarting
	// at 1. This is the property the dense ordinal cannot provide.
	post, err := store.Append(ctx, "swept", "session.created", json.RawMessage(`{"post":1}`), time.Now())
	if err != nil {
		t.Fatalf("Append after sweep: %v", err)
	}
	if post.PrevHash != audit.GenesisPrevHash {
		t.Errorf("post-sweep first row prev_hash = %q, want the genesis sentinel", post.PrevHash)
	}
	if post.Seq <= last {
		t.Errorf("audit sequence regressed across a sweep: got %d, want > %d "+
			"(a dense tail ordinal restarts at the genesis literal 1)", post.Seq, last)
	}
	if post.Seq != last+1 {
		t.Errorf("audit sequence did not continue monotonically across the sweep: got %d, want %d", post.Seq, last+1)
	}

	// The post-sweep chain of one row still verifies: a non-1 genesis that
	// links to the sentinel is a benign gap, not a broken chain.
	res, err := store.Verify(ctx, "swept")
	if err != nil {
		t.Fatalf("Verify after sweep: %v", err)
	}
	if res.Integrity != audit.ChainVerified {
		t.Errorf("Verify of a post-sweep non-1 genesis chain = %q (%s), want verified", res.Integrity, res.Detail)
	}
}

// spec: 11.7 (concurrency control), 10.2
// diagnosis: §11.7 draws the audit sequence_number from nextval, which
// serializes concurrent writers atomically. Under the retained per-tenant
// prev_hash advisory lock the appends are already serialized, so every
// concurrent Append must receive a distinct, contiguous sequence value
// with no duplicate (tenant_id, sequence_number) primary-key collision
// and no lost write, and the resulting chain must verify. A failure means
// the nextval assignment or the prev_hash chaining is not atomic under
// concurrency. This is the tier-7a-class atomicity property at the store
// tier, exercised here because it needs a real Postgres sequence. This
// test is race-clean. F-11.2.10.
func TestAuditSequenceConcurrentAppendNoCollision(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "concurrent")

	const writers = 16
	seqs := make(chan uint64, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			row, err := store.Append(ctx, "concurrent", "session.created", json.RawMessage(`{}`), time.Now())
			if err != nil {
				errs <- err
				return
			}
			seqs <- row.Seq
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

	// The retained prev_hash advisory lock keeps the chain contiguous and
	// linked; the whole chain verifies.
	rows, err := store.Rows(ctx, "concurrent")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != writers {
		t.Fatalf("Rows: got %d, want %d", len(rows), writers)
	}
	for i, r := range rows {
		if r.Seq != uint64(i+1) {
			t.Errorf("row %d: sequence %d, want %d (contiguous nextval band)", i, r.Seq, i+1)
		}
	}
	if res, err := store.Verify(ctx, "concurrent"); err != nil {
		t.Fatalf("Verify: %v", err)
	} else if res.Integrity != audit.ChainVerified {
		t.Errorf("Verify of the concurrent chain = %q (%s), want verified", res.Integrity, res.Detail)
	}
}
