// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// receiptFixture is an ES256 key, the key set that trusts it, and a
// builder of scan rows for CheckRedactionReceipts.
type receiptFixture struct {
	key  *ecdsa.PrivateKey
	keys *audit.ReceiptKeySet
}

func newReceiptFixture(t *testing.T) receiptFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := audit.NewReceiptKeySet(audit.ReceiptKey{KeyID: "audit-key-v1", PublicKey: &key.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	return receiptFixture{key: key, keys: keys}
}

// redactedRow returns a redaction-marked audit row of tenant at seq with
// id rowID, as the scan reads it from audit_log.
func redactedRow(tenant, rowID string, seq uint64) audit.Row {
	return audit.Row{
		ID: rowID, Seq: seq, TenantID: tenant, EventType: "session.created",
		EventSchemaVersion: audit.DefaultEventSchemaVersion,
		Payload:            []byte(`{"redacted":true,"event_type":"session.created","error_class":"unknown"}`),
		Timestamp:          time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
		PrevHash:           "0a0b",
	}
}

// signed returns a receipt for row, whose new_hash is the row's content
// hash, signed under the fixture key.
func (f receiptFixture) signed(t *testing.T, row audit.Row) audit.RedactionReceipt {
	t.Helper()
	r := audit.RedactionReceipt{
		ReceiptID: "00000000-0000-4000-8000-0000000000a1", AuditEventID: row.ID, TenantID: row.TenantID,
		RedactedSeq: row.Seq, OriginalHash: "0a0c", NewHash: audit.ComputeHash(row),
		ErasureJobID: "00000000-0000-4000-8000-0000000000b1", LegalBasis: "gdpr_art17",
		RedactorIdentity: "erasure-job:j", Timestamp: audit.ReceiptTimestamp(time.Now()),
		KeyID: "audit-key-v1",
	}
	input, err := audit.ReceiptSigningInput(r)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	rInt, sInt, err := ecdsa.Sign(rand.Reader, f.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	rInt.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])
	r.Signature = sig
	return r
}

// scanRow renders a redacted row and its receipt as the scan returns
// them; a zero receipt renders as the COALESCE defaults of a row with no
// receipt.
func scanRow(row audit.Row, r audit.RedactionReceipt) []any {
	orig, _ := hex.DecodeString(r.OriginalHash)
	nh, _ := hex.DecodeString(r.NewHash)
	prev, _ := hex.DecodeString(row.PrevHash)
	ts := r.Timestamp
	if ts.IsZero() {
		ts = time.Unix(0, 0).UTC()
	}
	sig := r.Signature
	if sig == nil {
		sig = []byte{}
	}
	return []any{
		row.TenantID, row.ID, int64(row.Seq), row.EventType, row.EventSchemaVersion,
		[]byte(row.Payload), row.Timestamp, prev,
		r.ReceiptID, r.AuditEventID, r.TenantID, int64(r.RedactedSeq),
		orig, nh, r.ErasureJobID, r.LegalBasis, r.RedactorIdentity, ts, sig, r.KeyID,
	}
}

// TestCheckRedactionReceiptsClean pins that a deployment with no
// redaction-marked rows reports nothing, so the periodic check never
// touches lenny_audit_redaction_receipt_missing_total.
//
// spec: §12.8 (Compliance Interfaces), §16.5 (Alerting Rules and SLOs)
func TestCheckRedactionReceiptsClean(t *testing.T) {
	f := newReceiptFixture(t)
	got, err := CheckRedactionReceipts(context.Background(), &scriptQuerier{}, f.keys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no orphaned redactions, got %v", got)
	}
}

// TestCheckRedactionReceiptsCountsEveryReceiptThatDoesNotVerify pins that a
// redaction-marked row counts as missing when it has no receipt, when its
// receipt is unsigned, signed under an untrusted key, altered after
// signing, or bound to another row, and when the row was rewritten again
// after its receipt was signed, so its content hash differs from the
// receipt's new_hash; a row whose receipt verifies does not count. Results are per tenant and sorted by tenant.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
func TestCheckRedactionReceiptsCountsEveryReceiptThatDoesNotVerify(t *testing.T) {
	f := newReceiptFixture(t)
	row := redactedRow
	good := f.signed(t, row("acme", "row-1", 1))
	tampered := f.signed(t, row("globex", "row-3", 3))
	tampered.LegalBasis = "operator_acknowledged_override"
	unsigned := f.signed(t, row("globex", "row-4", 4))
	unsigned.Signature = nil
	untrusted := f.signed(t, row("globex", "row-5", 5))
	untrusted.KeyID = "attacker-key"
	otherRow := f.signed(t, row("globex", "row-elsewhere", 6))
	// A row rewritten again after its receipt was signed: the receipt's
	// signed new_hash is the content hash of the first redaction.
	rewritten := row("globex", "row-7", 7)
	rewrittenReceipt := f.signed(t, rewritten)
	rewritten.Payload = []byte(`{"redacted":true,"event_type":"session.created","planted":"bob@acme.com"}`)
	q := &scriptQuerier{redaction: [][]any{
		scanRow(row("acme", "row-1", 1), good),
		scanRow(row("acme", "row-2", 2), audit.RedactionReceipt{}),
		scanRow(row("globex", "row-3", 3), tampered),
		scanRow(row("globex", "row-4", 4), unsigned),
		scanRow(row("globex", "row-5", 5), untrusted),
		scanRow(row("globex", "row-6", 6), otherRow),
		scanRow(rewritten, rewrittenReceipt),
	}}
	got, err := CheckRedactionReceipts(context.Background(), q, f.keys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []RedactionReceiptResult{{TenantID: "acme", Missing: 1}, {TenantID: "globex", Missing: 5}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("CheckRedactionReceipts = %+v, want %+v", got, want)
	}
}

// TestCheckRedactionReceiptsWithoutAVerifierTrustsNoReceipt pins the fail-
// closed default: with no verifier, a row whose receipt is validly signed
// still counts as missing.
//
// spec: §12.8 (Compliance Interfaces)
func TestCheckRedactionReceiptsWithoutAVerifierTrustsNoReceipt(t *testing.T) {
	f := newReceiptFixture(t)
	q := &scriptQuerier{redaction: [][]any{scanRow(redactedRow("acme", "row-1", 1), f.signed(t, redactedRow("acme", "row-1", 1)))}}
	got, err := CheckRedactionReceipts(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Missing != 1 {
		t.Fatalf("CheckRedactionReceipts with no verifier = %+v, want acme missing 1", got)
	}
}

// TestCheckRedactionReceiptsQueryError pins that a scan error (for example
// the audit_redaction_receipts relation absent on a partial migration) is
// surfaced to the caller, which treats it as a non-fatal skipped cycle
// rather than drift.
//
// spec: §11.7 (Audit Logging)
func TestCheckRedactionReceiptsQueryError(t *testing.T) {
	q := &scriptQuerier{queryErr: errors.New("relation \"audit_redaction_receipts\" does not exist")}
	_, err := CheckRedactionReceipts(context.Background(), q, nil)
	if err == nil {
		t.Fatal("expected an error when the receipts relation is unavailable")
	}
}

// TestPeriodicCheckRedactionReceiptMissing pins that the periodic check
// turns each tenant with a receipt that does not verify into one
// OnRedactionReceiptMissing call carrying the count, the gateway adapter
// for lenny_audit_redaction_receipt_missing_total. A missing receipt is a
// compliance page rather than a chain-tamper hard-fail, so CheckOnce does
// not report drift for it.
//
// spec: §16.5 (Alerting Rules and SLOs), §12.8 (Compliance Interfaces)
func TestPeriodicCheckRedactionReceiptMissing(t *testing.T) {
	f := newReceiptFixture(t)
	forged := f.signed(t, redactedRow("acme", "row-1", 1))
	forged.Signature[0] ^= 0xff
	var calls []RedactionReceiptResult
	q := &scriptQuerier{redaction: [][]any{
		scanRow(redactedRow("acme", "row-1", 1), forged),
		scanRow(redactedRow("acme", "row-2", 2), audit.RedactionReceipt{}),
		scanRow(redactedRow("acme", "row-3", 3), f.signed(t, redactedRow("acme", "row-3", 3))),
	}}
	p := &PeriodicCheck{
		DB:              q,
		CtrlDB:          q, // co-located topology: one pool serves both
		Cfg:             PeriodicConfig{ChainSampleN: 1000},
		ReceiptVerifier: f.keys,
		OnRedactionReceiptMissing: func(tenantID string, n int) {
			calls = append(calls, RedactionReceiptResult{TenantID: tenantID, Missing: n})
		},
	}
	if p.CheckOnce(context.Background()) {
		t.Fatal("an orphaned redaction must not be reported as chain drift")
	}
	if len(calls) != 1 || calls[0].TenantID != "acme" || calls[0].Missing != 2 {
		t.Fatalf("OnRedactionReceiptMissing not invoked correctly: %v", calls)
	}
}

// TestPeriodicCheckRedactionReceiptClean pins that a clean redaction scan
// fires no observer, so the receipt-missing series stays at zero.
//
// spec: §11.7 (Audit Logging)
func TestPeriodicCheckRedactionReceiptClean(t *testing.T) {
	f := newReceiptFixture(t)
	fired := false
	q := &scriptQuerier{redaction: [][]any{scanRow(redactedRow("acme", "row-1", 1), f.signed(t, redactedRow("acme", "row-1", 1)))}}
	p := &PeriodicCheck{
		DB:                        q,
		CtrlDB:                    q, // co-located topology: one pool serves both
		Cfg:                       PeriodicConfig{ChainSampleN: 1000},
		ReceiptVerifier:           f.keys,
		OnRedactionReceiptMissing: func(string, int) { fired = true },
	}
	if p.CheckOnce(context.Background()) {
		t.Fatal("clean cycle reported drift")
	}
	if fired {
		t.Fatal("OnRedactionReceiptMissing fired on a clean cycle")
	}
}
