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
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

func seedTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
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
