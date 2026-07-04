//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.8 step-14 OCSF dead-letter PII redaction
// against a real Postgres audit chain. Covers the dead-lettered-row
// scan (user matched by payload, not a user_id column), the in-place
// payload rewrite under a signed RedactionReceipt, the paired §16.7
// gdpr.erasure_deadletter_redacted / _downstream_notified events, and
// that the redacted chain still verifies (redacted_gdpr, not broken).
package auditstore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// receiptCount returns the number of audit_redaction_receipts rows for
// tenant, read directly on the container pool. DeleteByTenant only touches
// audit_log, so the receipts remnant is the standalone compliance-receipt
// set §12.8 line 831 keeps exempt; this counter pins that the teardown
// leaves it intact.
func receiptCount(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_redaction_receipts WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count receipts for %q: %v", tenant, err)
	}
	return n
}

// spec: §12.8 lines 810-829 — DeleteByUser step-14 redacts the user's
// dead-lettered audit rows in place under a signed receipt and emits the
// paired §16.7 events, while leaving other users' rows and the chain
// verifiability intact.
// diagnosis: a failure means DeleteByUser step-14 either misses the
// user's dead-lettered rows, redacts another user's rows, or breaks the
// audit chain verification after redaction.
func TestDeadLetterRedaction_spec_12_8(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	type seed struct {
		eventType string
		payload   string
	}
	for _, s := range []seed{
		{"session.created", `{"user_id":"alice@acme.com","note":"secret-a"}`}, // seq1 alice
		{"tool.called", `{"actor":"bob@acme.com"}`},                           // seq2 bob
		{"session.created", `{"sub":"alice@acme.com"}`},                       // seq3 alice
		{"admin.tenant.created", `{"x":1}`},                                   // seq4 no user
	} {
		if _, err := store.Append(ctx, "acme", s.eventType, json.RawMessage(s.payload), at); err != nil {
			t.Fatalf("append %s: %v", s.eventType, err)
		}
	}
	// Mark the three event rows dead_lettered (seq1, seq2, seq3); seq4 has
	// no user and is left translatable. ocsf_translation_state is a
	// permitted bookkeeping column, so the UPDATE needs only the RLS tenant
	// context, not erasure mode.
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', 'acme', true)"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET ocsf_translation_state='dead_lettered'
		 WHERE tenant_id='acme' AND sequence_number IN (1, 2, 3)`); err != nil {
		t.Fatalf("mark dead_lettered: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Scan: only alice's dead-lettered rows (seq1, seq3) — not bob (seq2),
	// not the translatable admin row (seq4).
	dead, err := store.DeadLetteredForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeadLetteredForUser: %v", err)
	}
	if len(dead) != 2 {
		t.Fatalf("dead-lettered for alice = %d rows, want 2 (seq1, seq3)", len(dead))
	}

	svc := deadletterredaction.New(deadletterredaction.Config{
		Store:    store,
		Emit:     store,
		Signer:   deadletterredaction.NewHMACReceiptSigner("boot", []byte("0123456789abcdef0123456789abcdef")),
		Classify: func(audit.Row) string { return "class_mapping_missing" },
		Clock:    func() time.Time { return at },
	})
	n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("RedactForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("redacted = %d, want 2", n)
	}

	rows, err := store.Rows(ctx, "acme")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	bySeq := map[uint64]audit.Row{}
	var redactedEvents, notifyEvents int
	for _, r := range rows {
		bySeq[r.Seq] = r
		switch r.EventType {
		case "gdpr.erasure_deadletter_redacted":
			redactedEvents++
		case "gdpr.erasure_deadletter_downstream_notified":
			notifyEvents++
		}
	}
	// seq1 + seq3 redacted; alice's PII gone; seq2 (bob) untouched.
	for _, seq := range []uint64{1, 3} {
		if !audit.IsRedactedPayload(bySeq[seq].Payload) {
			t.Errorf("seq %d not redacted: %s", seq, bySeq[seq].Payload)
		}
		if strings.Contains(string(bySeq[seq].Payload), "alice@acme.com") || strings.Contains(string(bySeq[seq].Payload), "secret-a") {
			t.Errorf("seq %d still carries PII: %s", seq, bySeq[seq].Payload)
		}
	}
	if audit.IsRedactedPayload(bySeq[2].Payload) || !strings.Contains(string(bySeq[2].Payload), "bob@acme.com") {
		t.Errorf("bob's row (seq2) must be untouched: %s", bySeq[2].Payload)
	}
	if redactedEvents != 2 || notifyEvents != 2 {
		t.Errorf("emitted events: redacted=%d notify=%d, want 2/2", redactedEvents, notifyEvents)
	}

	// Two signature-bearing receipts at the redacted positions.
	receipts, err := store.Receipts(ctx, "acme")
	if err != nil {
		t.Fatalf("Receipts: %v", err)
	}
	for _, seq := range []uint64{1, 3} {
		rcpt, ok := receipts[seq]
		if !ok || rcpt.Signature == "" || rcpt.OriginalHash == "" {
			t.Errorf("seq %d receipt missing or unsigned: %+v", seq, rcpt)
		}
	}

	// The chain verifies as a lawful post-redaction chain, not broken.
	vr, err := store.Verify(ctx, "acme")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vr.Integrity == audit.ChainBroken {
		t.Fatalf("redacted chain reports broken at seq %d: %s", vr.BreakSeq, vr.Detail)
	}

	// Idempotent: a re-run finds nothing (redacted rows are excluded by the
	// payload marker) and emits no further events.
	n2, err := svc.RedactForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("RedactForUser re-run: %v", err)
	}
	if n2 != 0 {
		t.Errorf("re-run redacted = %d, want 0 (idempotent)", n2)
	}
}

// markDeadLettered flips the ocsf_translation_state of the named sequence
// numbers to dead_lettered on the container pool. ocsf_translation_state is
// a §11.7 bookkeeping column outside the hash-input set, so the UPDATE needs
// only the RLS tenant context, not erasure mode.
func markDeadLettered(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seqs ...uint64) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET ocsf_translation_state='dead_lettered'
		 WHERE tenant_id=$1 AND sequence_number = ANY($2)`,
		tenant, seqs); err != nil {
		t.Fatalf("mark dead_lettered: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// rowIDBySeq returns the audit_log.id of tenant's row at seq, read on the
// container pool. Used to seed an audit_redaction_receipts row whose
// audit_event_id FK (§12.8 line 815, migration 0160) references a real
// audit_log row.
func rowIDBySeq(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seq uint64) string {
	t.Helper()
	var id string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT id::text FROM audit_log WHERE tenant_id=$1 AND sequence_number=$2`,
		tenant, int64(seq)).Scan(&id); err != nil {
		t.Fatalf("row id for %q seq %d: %v", tenant, seq, err)
	}
	return id
}

// seedReceipt inserts one audit_redaction_receipts row for tenant at seq,
// referencing the audit_log row auditEventID. It writes directly on the
// container pool (superuser), standing in for the erasure service's INSERT
// so a teardown test has a receipt remnant to observe without driving the
// full redaction. The hash/identity columns carry deterministic filler; the
// teardown never reads them.
func seedReceipt(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, auditEventID string, seq uint64) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO audit_redaction_receipts (
		receipt_id, audit_event_id, tenant_id, sequence_number,
		original_hash, new_hash, erasure_job_id, legal_basis,
		redactor_identity, "timestamp", signature, signature_kms_key_id
	) VALUES (gen_random_uuid(), $1::uuid, $2, $3,
		'\x00', '\x01', gen_random_uuid(), 'gdpr_art17',
		'erasure-job', now(), '\x02', 'kms-key-1')`,
		auditEventID, tenant, int64(seq)); err != nil {
		t.Fatalf("seed receipt for %q seq %d: %v", tenant, seq, err)
	}
}

// spec: 12.8 (DeleteByTenant gdpr.* skip, line 840), 12.8 (receipt
// exemption, line 831)
// diagnosis: a failure means tenant teardown either wipes the compliance
// receipts that must outlive the tenant or fails to purge the non-receipt
// rows. This test seeds a chain that interleaves ordinary rows,
// dead_lettered rows, and gdpr.% erasure-receipt rows, plus a standalone
// audit_redaction_receipts row, then asserts the Phase-4 DeleteByTenant
// deletes every event_type NOT LIKE 'gdpr.%' row (ordinary and
// dead_lettered alike) and retains every gdpr.% row plus the
// audit_redaction_receipts remnant (§12.8 line 831). It would fail against
// the pre-fix DELETE FROM audit_log WHERE tenant_id = $1, which deleted the
// gdpr.% receipts along with the ordinary rows.
func TestDeleteByTenantSkipsGDPRAndRetainsReceipts_spec_12_8_line840(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "teardown")

	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	// Interleave: seq1 ordinary, seq2 dead_lettered, seq3 gdpr.% receipt,
	// seq4 ordinary, seq5 gdpr.% receipt, seq6 dead_lettered.
	mustAppend(t, ctx, store, "teardown", "session.created", at)
	mustAppend(t, ctx, store, "teardown", "tool.called", at)
	mustAppend(t, ctx, store, "teardown", "gdpr.erasure_completed", at)
	mustAppend(t, ctx, store, "teardown", "admin.user.created", at)
	mustAppend(t, ctx, store, "teardown", "gdpr.erasure_deadletter_redacted", at)
	mustAppend(t, ctx, store, "teardown", "session.completed", at)
	markDeadLettered(t, ctx, pg, "teardown", 2, 6)

	// Seed a standalone audit_redaction_receipts row. It references a
	// surviving gdpr.% row (seq3) so this test isolates the §12.8 line 840
	// gdpr.% skip from the separately-tracked FK-ordering defect (a receipt
	// referencing a to-be-deleted dead_lettered row blocks the teardown
	// DELETE; see TestDeleteByTenantFKBlocksReceiptReferencedRow below and
	// BUILD-GAPS.md F-12.8.25). DeleteByTenant deletes only audit_log, so
	// the receipt remnant §12.8 line 831 keeps exempt must survive
	// regardless.
	seedReceipt(t, ctx, pg, "teardown", rowIDBySeq(t, ctx, pg, "teardown", 3), 3)
	if got := receiptCount(t, ctx, pg, "teardown"); got != 1 {
		t.Fatalf("pre-teardown receipts = %d, want 1", got)
	}

	deleted, err := store.DeleteByTenant(ctx, "teardown")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	// Four non-gdpr.% rows (seq1, seq2, seq4, seq6) are deleted; the count
	// excludes the two retained gdpr.% receipts. Against the pre-fix
	// DELETE FROM audit_log WHERE tenant_id = $1 this would be 6.
	if deleted != 4 {
		t.Fatalf("DeleteByTenant deleted %d rows, want 4 (the two gdpr.%% receipts are retained and excluded from the count)", deleted)
	}
	if seqs := remainingSeqs(t, ctx, store, "teardown"); !equalSeqs(seqs, []uint64{3, 5}) {
		t.Fatalf("after teardown remaining seqs = %v, want [3 5] (the gdpr.%% receipts)", seqs)
	}
	rows, err := store.Rows(ctx, "teardown")
	if err != nil {
		t.Fatalf("Rows after teardown: %v", err)
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.EventType, "gdpr.") {
			t.Errorf("non-gdpr.* row %q (seq %d) survived the teardown", r.EventType, r.Seq)
		}
	}
	// The audit_redaction_receipts remnant survives the teardown intact.
	if got := receiptCount(t, ctx, pg, "teardown"); got != 1 {
		t.Fatalf("post-teardown receipts = %d, want 1 (the standalone remnant §12.8 line 831 keeps exempt)", got)
	}
}

// spec: 12.8 (DeleteByTenant gdpr.* skip, line 840), 12.8 (receipt
// exemption, line 831)
// diagnosis: this pins the CURRENT behavior of the audit_redaction_receipts
// -> audit_log FK (migration 0160, NO ACTION) when Phase-4 DeleteByTenant
// deletes a redacted dead_lettered row that a receipt references: the delete
// aborts with a foreign-key violation (SQLSTATE 23503). §12.8 line 831/842
// exempt the receipts and require them to outlive the tenant, but they
// reference a non-gdpr.% dead_lettered row the teardown must purge, so the
// two requirements collide. Proposal 0028 did not address this ordering and
// asserts "no new migration", so the fix (skip receipt-referenced rows,
// delete the receipts first, or make the FK ON DELETE SET NULL) is out of
// 0028 scope and needs a spec decision; it is tracked as BUILD-GAPS.md
// F-12.8.25. This test records the collision so that fix flips it green
// rather than discovering the abort in production teardown.
func TestDeleteByTenantFKBlocksReceiptReferencedRow_spec_12_8_line840(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "fk-block")

	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	// seq1 is a dead_lettered ordinary row; a receipt references it.
	mustAppend(t, ctx, store, "fk-block", "tool.called", at)
	markDeadLettered(t, ctx, pg, "fk-block", 1)
	seedReceipt(t, ctx, pg, "fk-block", rowIDBySeq(t, ctx, pg, "fk-block", 1), 1)

	// The teardown DELETE of the non-gdpr.% referenced row violates the
	// audit_redaction_receipts FK, so DeleteByTenant returns an error and
	// deletes nothing (the transaction aborts). This is the known collision
	// BUILD-GAPS.md F-12.8.25 tracks.
	_, err := store.DeleteByTenant(ctx, "fk-block")
	if err == nil {
		t.Fatalf("DeleteByTenant unexpectedly succeeded; the F-12.8.25 receipt FK-ordering fix has landed, flip this test to assert receipt retention and close F-12.8.25")
	}
	if !strings.Contains(err.Error(), "audit_redaction_receipts") {
		t.Fatalf("DeleteByTenant error = %v, want a foreign-key violation on audit_redaction_receipts", err)
	}
}

// spec: 12.8 (DeleteByTenant gdpr.* skip, line 840), 12.8 (receipt
// exemption, line 831)
// diagnosis: a failure means tenant teardown deleted a gdpr.% erasure
// receipt in the boundary case where the tenant's only rows are receipts,
// destroying the compliance record §12.8 requires to outlive the tenant.
// This boundary would pass against a DELETE that scoped by anything other
// than event_type NOT LIKE 'gdpr.%', so it pins the gdpr.% predicate.
func TestDeleteByTenantGDPROnlyRemnantUntouched_spec_12_8_line840(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()
	seedTenant(t, ctx, pg, "receipts-only")

	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	// Every row is a gdpr.% receipt: the boundary case where the teardown
	// must delete nothing.
	mustAppend(t, ctx, store, "receipts-only", "gdpr.erasure_completed", at)
	mustAppend(t, ctx, store, "receipts-only", "gdpr.erasure_deadletter_redacted", at)
	mustAppend(t, ctx, store, "receipts-only", "gdpr.erasure_deadletter_downstream_notified", at)

	deleted, err := store.DeleteByTenant(ctx, "receipts-only")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("DeleteByTenant deleted %d rows, want 0 (the tenant holds only gdpr.%% receipts)", deleted)
	}
	if seqs := remainingSeqs(t, ctx, store, "receipts-only"); !equalSeqs(seqs, []uint64{1, 2, 3}) {
		t.Fatalf("after teardown remaining seqs = %v, want [1 2 3] (all gdpr.%% receipts intact)", seqs)
	}
}

// setTenantState advances tenant's tenants.state column on the container
// pool (superuser, RLS-exempt), standing in for the §12.8 deletion
// controller that drives the state machine. The continuity verifier reads
// this column from the control-plane pool to build its deletion skip-set.
func setTenantState(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, state string) {
	t.Helper()
	tag, err := pg.Pool.Exec(ctx, `UPDATE tenants SET state = $2 WHERE id = $1`, tenant, state)
	if err != nil {
		t.Fatalf("set tenant %q state=%q: %v", tenant, state, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set tenant %q state=%q affected %d rows, want 1", tenant, state, tag.RowsAffected())
	}
}

// tamperRow rewrites tenant's row at seq in place under the erasure-mode
// guard (the only path the §11.7 lenny_audit_immutability trigger permits
// an UPDATE), breaking the chain's content hash for that row. The
// continuity verifier must report the resulting break as ChainBroken.
func tamperRow(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seq uint64) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tamper tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('lenny.erasure_mode', 'true', true)"); err != nil {
		t.Fatalf("set erasure mode: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET payload = '{"v":999}'::jsonb
		 WHERE tenant_id = $1 AND sequence_number = $2`, tenant, int64(seq)); err != nil {
		t.Fatalf("tamper UPDATE: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tamper tx: %v", err)
	}
}

// resultFor returns the continuity result for tenant, or nil when the
// tenant was not enumerated (the expected outcome for a tenant in the
// §12.8 deletion skip-set).
func resultFor(results []integrity.ChainContinuityResult, tenant string) *integrity.ChainContinuityResult {
	for i := range results {
		if results[i].TenantID == tenant {
			return &results[i]
		}
	}
	return nil
}

// spec: 12.8 (post-teardown remnant exempt from chain verification, line
// 840), 11.7 (chain integrity)
// diagnosis: a failure means either tenant teardown fires a false
// AuditChainGap on the retained gdpr.* remnant during the
// deleting-or-deleted window, or the deletion-state scoping masks a real
// chain break.
//
// This is the one proposal §6 tier-2 behavior with no other real-Postgres
// coverage: after DeleteByTenant leaves a tenant with only its gdpr.*
// remnant (a deliberately discontinuous chain), both the full-walk
// integrity.CheckChainContinuity and the windowed
// integrity.CheckChainContinuityRecent must exclude that tenant while it
// is state='deleting' (the Phase-4-through-Phase-5 state) and while it is
// state='deleted' (the Phase-6 tombstone), so the whole teardown window,
// and a deletion that stalls mid-way, raises no §16.5 AuditChainGap. The
// broken-state metric is driven by OnChainState("broken") once per
// enumerated result (periodic.go); an excluded tenant is never passed to
// that hook, and a PeriodicCheck.CheckOnce co-located run confirms no
// "broken" state is emitted. Contrast cases pin that the exclusion does
// not mask a live break: a live tenant (state='active') with an intact
// chain still verifies, the 'platform' pseudo-tenant with no tenants row
// is never skipped and still verifies, and a live tenant with a genuinely
// tampered chain still reports Broken(). It would pass trivially against a
// verifier that skipped every tenant, so the live contrast cases are the
// negative control that keeps the exclusion honest. It uses the co-located
// topology (the same pool for auditDB and ctrlDB); the split billing/audit
// pool resolution is separately pinned at tier-1 in
// pkg/audit/integrity/continuity_skipset_test.go.
func TestPostTeardownRemnantExemptFromContinuity_spec_12_8_line840(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()

	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)

	// deleted: a torn-down tenant. Seed an ordinary chain plus two gdpr.%
	// receipts, run the Phase-4 DeleteByTenant so only the gdpr.*-only
	// remnant survives (a chain that no longer starts at genesis and whose
	// first surviving prev_hash links to a purged predecessor), then park
	// it in the deletion state machine.
	seedTenant(t, ctx, pg, "deleted-tenant")
	mustAppend(t, ctx, store, "deleted-tenant", "session.created", at)
	mustAppend(t, ctx, store, "deleted-tenant", "tool.called", at)
	mustAppend(t, ctx, store, "deleted-tenant", "gdpr.erasure_completed", at)
	mustAppend(t, ctx, store, "deleted-tenant", "session.completed", at)
	mustAppend(t, ctx, store, "deleted-tenant", "gdpr.erasure_deadletter_redacted", at)
	if _, err := store.DeleteByTenant(ctx, "deleted-tenant"); err != nil {
		t.Fatalf("DeleteByTenant(deleted-tenant): %v", err)
	}
	// The remnant is a genuinely discontinuous chain: only the gdpr.% rows
	// (seq3, seq5) survive, so it would verify as ChainBroken if walked.
	if seqs := remainingSeqs(t, ctx, store, "deleted-tenant"); !equalSeqs(seqs, []uint64{3, 5}) {
		t.Fatalf("remnant seqs = %v, want [3 5]", seqs)
	}

	// live: a healthy tenant that must still be walked and verify.
	seedTenant(t, ctx, pg, "live-tenant")
	mustAppend(t, ctx, store, "live-tenant", "session.created", at)
	mustAppend(t, ctx, store, "live-tenant", "session.completed", at)

	// platform: the pseudo-tenant with no tenants row (never in the
	// deletion skip-set, 0001_initial_schema.up.sql). It has an audit
	// sequence but no tenants row, so its chain must still be walked and
	// verify.
	provisionAuditSequence(t, ctx, pg, "platform")
	mustAppend(t, ctx, store, "platform", "admin.tenant.created", at)
	mustAppend(t, ctx, store, "platform", "admin.user.created", at)

	// broken: a live tenant with a genuinely tampered chain — the negative
	// control the deletion exclusion must not mask.
	seedTenant(t, ctx, pg, "broken-tenant")
	mustAppend(t, ctx, store, "broken-tenant", "e1", at)
	mustAppend(t, ctx, store, "broken-tenant", "e2", at)
	mustAppend(t, ctx, store, "broken-tenant", "e3", at)
	tamperRow(t, ctx, pg, "broken-tenant", 2)

	// assertWindow runs both verifier entry points (co-located: the same
	// pool for auditDB and ctrlDB) and asserts the four tenants' outcomes.
	assertWindow := func(t *testing.T, phase string) {
		full, err := integrity.CheckChainContinuity(ctx, pg.Pool, pg.Pool)
		if err != nil {
			t.Fatalf("[%s] CheckChainContinuity: %v", phase, err)
		}
		recent, err := integrity.CheckChainContinuityRecent(ctx, pg.Pool, pg.Pool, 1000)
		if err != nil {
			t.Fatalf("[%s] CheckChainContinuityRecent: %v", phase, err)
		}
		for _, tc := range []struct {
			label   string
			results []integrity.ChainContinuityResult
		}{{"full", full}, {"recent", recent}} {
			// The torn-down tenant is excluded from both walks, so it can
			// never reach OnChainState and can never increment the broken
			// metric or fire AuditChainGap.
			if got := resultFor(tc.results, "deleted-tenant"); got != nil {
				t.Errorf("[%s/%s] torn-down tenant enumerated (result=%q); the deletion skip-set must exclude it",
					phase, tc.label, got.Result.Integrity)
			}
			// The genuine break is still reported: the exclusion must not
			// mask a live tampered chain.
			if got := resultFor(tc.results, "broken-tenant"); got == nil {
				t.Errorf("[%s/%s] live tampered tenant not enumerated; the exclusion must not drop live chains", phase, tc.label)
			} else if !got.Broken() {
				t.Errorf("[%s/%s] live tampered tenant = %q, want broken", phase, tc.label, got.Result.Integrity)
			}
			// The healthy live tenant and the platform pseudo-tenant are
			// walked and verify.
			for _, live := range []string{"live-tenant", "platform"} {
				got := resultFor(tc.results, live)
				if got == nil {
					t.Errorf("[%s/%s] live tenant %q not enumerated; only deleting/deleted tenants are skipped", phase, tc.label, live)
					continue
				}
				if got.Broken() {
					t.Errorf("[%s/%s] live tenant %q reported broken: %s", phase, tc.label, live, got.Result.Detail)
				}
			}
			// No result in the walk is broken except the deliberately
			// tampered live tenant, so FirstBroken points at broken-tenant
			// rather than the excluded remnant.
			if fb := integrity.FirstBroken(tc.results); fb != nil && fb.TenantID != "broken-tenant" {
				t.Errorf("[%s/%s] FirstBroken = %q, want broken-tenant (the remnant must not surface as broken)",
					phase, tc.label, fb.TenantID)
			}
		}

		// The broken-state metric is emitted by OnChainState("broken") once
		// per enumerated result on the periodic path. Drive a co-located
		// PeriodicCheck.CheckOnce and assert "broken" is never emitted for
		// the excluded remnant (only for the live tampered tenant), so the
		// teardown fires no false AuditChainGap.
		var brokenStates int
		pc := &integrity.PeriodicCheck{
			DB:     pg.Pool,
			CtrlDB: pg.Pool,
			Cfg:    integrity.PeriodicConfig{ChainSampleN: 1000},
			OnChainState: func(state string) {
				if state == string(audit.ChainBroken) {
					brokenStates++
				}
			},
		}
		pc.CheckOnce(ctx)
		// Exactly one broken state: the deliberately tampered live tenant.
		// The excluded remnant must not contribute a second.
		if brokenStates != 1 {
			t.Errorf("[%s] OnChainState reported %d broken states, want 1 (only broken-tenant; the excluded remnant must not add one)", phase, brokenStates)
		}
	}

	// state='deleting' covers Phases 4, 4a, and 5; the tenant carries it
	// from the Phase-4 audit delete above.
	setTenantState(t, ctx, pg, "deleted-tenant", "deleting")
	assertWindow(t, "deleting")

	// state='deleted' is the Phase-6 tombstone; the exclusion must hold
	// across the whole teardown window.
	setTenantState(t, ctx, pg, "deleted-tenant", "deleted")
	assertWindow(t, "deleted")
}
