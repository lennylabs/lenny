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
	// DELETE; see the ...ReceiptReferencedRow subtest below and the recorded
	// follow-up). DeleteByTenant deletes only audit_log, so the receipt
	// remnant §12.8 line 831 keeps exempt must survive regardless.
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
// two requirements collide. Proposal 0028 did not address this ordering; the
// fix (skip receipt-referenced rows, delete the receipts, or make the FK ON
// DELETE SET NULL) needs a spec decision and is tracked as a follow-up. This
// test records the collision so the fix flips it green rather than
// discovering the abort in production teardown.
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
	// deletes nothing (the transaction aborts). This is the known
	// collision the recorded follow-up tracks.
	_, err := store.DeleteByTenant(ctx, "fk-block")
	if err == nil {
		t.Fatalf("DeleteByTenant unexpectedly succeeded; the receipt FK-ordering fix has landed, flip this test to assert receipt retention and remove the follow-up")
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
