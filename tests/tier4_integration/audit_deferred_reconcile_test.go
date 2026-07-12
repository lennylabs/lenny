// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §25.9 degraded-mode deferred-write
// reconciliation path. When lenny-ops creates audit events during a
// Postgres outage, the events are buffered in audit_log_deferred_writes
// and later applied to audit_log in original-timestamp order. The chain
// hash is re-computed for the affected range, which intentionally moves
// those events from chainIntegrity: verified to
// chainIntegrity: rechained_post_outage so operators can distinguish a
// legitimate post-outage rechain from a tamper-broken chain, and the
// lenny_audit_chain_rechained_post_outage_total counter increments.
//
// The reconciliation driver that reads audit_log_deferred_writes, applies
// the buffered events to audit_log in original-timestamp order, re-hashes
// the affected range, and stamps the affected rows rechained_post_outage
// does not exist in the product yet: pkg/audit documents it as living "in
// later phases", auditstore exposes no reconcile entry point, and the
// per-row verifier (pkg/audit chain classifyRow) never emits
// rechained_post_outage from the outage path. This test is therefore
// skipped pending that driver. It seeds the pre-reconciliation state with
// existing building blocks and documents the assertions the driver must
// satisfy once built, so un-skipping it exercises the behavior end to end.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §25.9 (Degraded-mode write semantics) — "Deferred writes are
// applied to audit_log during reconciliation in original-timestamp
// order. The chain hash is re-computed for the affected range, which
// intentionally breaks chainIntegrity: verified for those events to
// chainIntegrity: rechained_post_outage so operators can distinguish
// tamper-broken chains from legitimately-rechained ones." The
// lenny_audit_chain_rechained_post_outage_total counter (§25.9 Metrics)
// counts chain segments rechained after a Postgres outage.
//
// diagnosis: the §25.9 deferred-write reconciliation must apply buffered
// outage-time audit events to audit_log in original-timestamp order and
// re-hash the affected range so those rows read rechained_post_outage
// (not verified, and not broken), while a separately tampered row stays
// broken and the rechained counter fires exactly for the rechained range.
// A failure means the operator signal that distinguishes a legitimate
// post-outage rechain from tampering is wrong.
func TestAuditDeferredReconcile(t *testing.T) {
	t.Skip("§25.9 deferred-write reconciliation driver not yet built; audit_log_deferred_writes has no reconcile path and the verifier never stamps rechained_post_outage from the outage path (open TEST-GAPS finding).")
	t.Parallel()
	ctx := context.Background()

	// Live stack: a real Postgres container with the production
	// migrations, including 0125_audit_log_deferred_writes.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})

	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	provisionAuditSequence(t, pg, tenant)

	store := auditstore.New(pg.Router(t))

	// Pre-outage: two events committed while Postgres was available. These
	// bound the range below which reconciliation must not touch.
	base := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	for i, et := range []string{"session.created", "session.completed"} {
		if _, err := store.Append(ctx, tenant, et, json.RawMessage(`{}`), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("pre-outage append %d: %v", i, err)
		}
	}
	if res, err := store.Verify(ctx, tenant); err != nil {
		t.Fatalf("Verify pre-outage: %v", err)
	} else if res.Integrity != audit.ChainVerified {
		t.Fatalf("pre-outage chain integrity = %q, want verified", res.Integrity)
	}

	// Outage window: lenny-ops buffered two audit events into
	// audit_log_deferred_writes with their original (outage-time)
	// timestamps, out of order relative to their deferred_at insert order,
	// so the reconciler must sort by the payload's original timestamp
	// rather than by insertion order.
	outageStart := base.Add(10 * time.Minute)
	deferred := []struct {
		originalAt time.Time
		eventType  string
	}{
		{outageStart.Add(2 * time.Minute), "lock.flushed"},
		{outageStart.Add(1 * time.Minute), "escalation.buffered"},
	}
	for _, d := range deferred {
		payload := map[string]any{"event_type": d.eventType, "original_timestamp": d.originalAt.Format(time.RFC3339Nano)}
		pb, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal deferred payload: %v", err)
		}
		if _, err := pg.Pool.Exec(ctx,
			`INSERT INTO audit_log_deferred_writes (event_payload, replica_id) VALUES ($1, $2)`,
			pb, "lenny-ops-0"); err != nil {
			t.Fatalf("seed deferred write %q: %v", d.eventType, err)
		}
	}

	// Reconciliation (driver not yet built) must, on Postgres recovery:
	//   1. Read the audit_log_deferred_writes rows, apply them to audit_log
	//      in original-timestamp order (escalation.buffered before
	//      lock.flushed), stamping applied_at.
	//   2. Re-compute the chain hash for the affected range so those two
	//      rows read chainIntegrity: rechained_post_outage via
	//      GET /v1/admin/audit-events (envelope chainIntegrityReport
	//      .rechained_post_outage == 2), while the pre-outage rows stay
	//      verified.
	//   3. Increment lenny_audit_chain_rechained_post_outage_total by the
	//      size of the rechained range (2), and not for any row outside it.
	//
	// A separately tampered post-outage row must stay chainIntegrity:
	// broken through this same query, so a real tamper is never masked as
	// a legitimate rechain. Once the driver lands, replace this comment
	// with: the reconcile call, a GET /v1/admin/audit-events assertion on
	// the per-row chainIntegrity and the envelope report, a tamper of one
	// committed row asserted broken, and a counter-delta assertion on
	// lenny_audit_chain_rechained_post_outage_total.
	_ = deferred
}
