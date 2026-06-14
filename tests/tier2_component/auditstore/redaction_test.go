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
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/deadletterredaction"
)

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
