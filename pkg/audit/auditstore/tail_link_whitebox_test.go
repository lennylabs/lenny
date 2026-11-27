// SPDX-License-Identifier: MIT

package auditstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// acceptAllReceipts is a ReceiptVerifier that accepts every signature, so
// these tests exercise the link and the receipt bindings alone; the
// signature check has its own tests in pkg/audit.
type acceptAllReceipts struct{}

func (acceptAllReceipts) VerifyReceipt(audit.RedactionReceipt) error { return nil }

// redactAsStored rewrites row's payload to the redacted form and returns the
// row as scanRow reads it back from audit_log (its Hash recomputed from the
// current columns), with the receipt the erasure writes for it.
func redactAsStored(row audit.Row, at time.Time) (audit.Row, audit.RedactionReceipt) {
	rcpt := audit.RedactionReceipt{
		AuditEventID: row.ID,
		TenantID:     row.TenantID,
		RedactedSeq:  row.Seq,
		OriginalHash: row.Hash,
	}
	row.Payload = audit.BuildRedactedPayload(row.EventType, "mapping_missing", at)
	row.Hash = audit.ComputeHash(row)
	rcpt.NewHash = row.Hash
	return row, rcpt
}

// successorOf seals the row an append writes after tail, as sealAndInsert
// does once scanTail has read the tail.
func successorOf(tail audit.Row, eventType string, at time.Time) audit.Row {
	row := audit.Row{
		ID:                 "row-next",
		Seq:                tail.Seq + 1,
		TenantID:           tail.TenantID,
		EventType:          eventType,
		EventSchemaVersion: audit.DefaultEventSchemaVersion,
		Payload:            json.RawMessage(`{"x":1}`),
		Timestamp:          at,
		PrevHash:           audit.LinkHash(tail),
	}
	row.Hash = audit.ComputeHash(row)
	return row
}

// TestARowAppendedAfterARedactedTailLinksToItsOriginalHash pins the link a
// row appended after a redacted tail row carries: markRedacted, which
// scanTail applies to the tail, sets the tail's Hash to its receipt's
// original_hash, so the successor's prev_hash is the preserved
// pre-redaction hash and the chain built from the stored rows reads
// redacted_gdpr. Without the receipt the successor links to the
// post-redaction hash, which the verifier, reading the receipt, reports
// broken.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
func TestARowAppendedAfterARedactedTailLinksToItsOriginalHash(t *testing.T) {
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	chain := audit.NewChain("acme")
	chain.Append("session.created", json.RawMessage(`{"user_id":"alice@acme.com"}`), at)
	stored, rcpt := redactAsStored(chain.Rows()[0], at)
	receipts := map[uint64]audit.RedactionReceipt{stored.Seq: rcpt}

	for _, c := range []struct {
		name      string
		receipted bool
		want      audit.ChainIntegrity
	}{
		{"the tail's receipt read", true, audit.ChainRedactedGDPR},
		{"the tail's receipt not read", false, audit.ChainBroken},
	} {
		t.Run(c.name, func(t *testing.T) {
			tail := stored
			markRedacted(&tail, rcpt.OriginalHash, c.receipted)
			next := successorOf(tail, "gdpr.erasure_deadletter_redacted", at)
			if c.receipted && next.PrevHash != rcpt.OriginalHash {
				t.Fatalf("successor prev_hash = %s, want the receipt's original_hash %s", next.PrevHash, rcpt.OriginalHash)
			}
			// The verifier rebuilds the stored rows as Store.Rows does.
			rows := []audit.Row{stored, next}
			applyReceipts(rows, receipts)
			vr := audit.ChainFromRows("acme", rows, receipts).WithReceiptVerifier(acceptAllReceipts{}).Verify()
			if vr.Integrity != c.want {
				t.Errorf("chain = %q at %d (%s), want %q", vr.Integrity, vr.BreakSeq, vr.Detail, c.want)
			}
		})
	}
}

// TestMarkRedactedAppliesAReceiptOnlyToARedactedRow pins the three cases
// markRedacted distinguishes: a redaction-marked row with a receipt takes
// the receipt's original_hash; a redaction-marked row without one is
// flagged Redacted and keeps its recomputed hash, the missing-receipt case
// the verifier reports broken; and a row without the marker is left as
// read, whatever receipt names it.
//
// spec: §12.8 (Compliance Interfaces)
func TestMarkRedactedAppliesAReceiptOnlyToARedactedRow(t *testing.T) {
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	plain := audit.Row{ID: "r1", Seq: 1, TenantID: "acme", EventType: "session.created", Payload: json.RawMessage(`{"x":1}`), Timestamp: at}
	plain.Hash = audit.ComputeHash(plain)
	redacted, rcpt := redactAsStored(plain, at)

	for _, c := range []struct {
		name         string
		row          audit.Row
		orig         string
		receipted    bool
		wantHash     string
		wantRedacted bool
	}{
		{"a redacted row with a receipt", redacted, rcpt.OriginalHash, true, rcpt.OriginalHash, true},
		{"a redacted row without a receipt", redacted, "", false, redacted.Hash, true},
		{"a redacted row whose receipt has no original_hash", redacted, "", true, redacted.Hash, true},
		{"a row without the marker", plain, "ffff", true, plain.Hash, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			row := c.row
			markRedacted(&row, c.orig, c.receipted)
			if row.Hash != c.wantHash || row.Redacted != c.wantRedacted {
				t.Errorf("hash %s redacted %v, want %s %v", row.Hash, row.Redacted, c.wantHash, c.wantRedacted)
			}
		})
	}
}
