// SPDX-License-Identifier: MIT

//go:build security

package security_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/receiptkeys"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// receiptForgeryTenant is the tenant whose dead-lettered row the test
// redacts and then forges a receipt for.
const receiptForgeryTenant = "acme"

// insideTheChain and asTheTail are the chains seedRedactedRow builds: the
// redacted row followed by another row, and the redacted row alone, so the
// erasure's own events are appended after a redacted tail row.
var (
	insideTheChain = []string{`{"user_id":"alice@acme.com","note":"secret"}`, `{"actor":"bob@acme.com"}`}
	asTheTail      = []string{`{"user_id":"alice@acme.com","note":"secret"}`}
)

// seedTenantChain inserts the test tenant and its audit sequence.
func seedTenantChain(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, receiptForgeryTenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(receiptForgeryTenant)+
		" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
}

// seedRedactedRow appends the audit rows payloads names, dead-letters the
// first, which names alice, and redacts it through the dead-letter
// redaction service with signer, so the row carries a validly signed
// receipt.
func seedRedactedRow(t *testing.T, ctx context.Context, pg *containers.Postgres, store *auditstore.Store, signer deadletterredaction.Signer, payloads []string) {
	t.Helper()
	seedTenantChain(t, ctx, pg)
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for _, payload := range payloads {
		if _, err := store.Append(ctx, receiptForgeryTenant, "session.created", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", receiptForgeryTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_log SET ocsf_translation_state='dead_lettered'
		WHERE tenant_id=$1 AND sequence_number=1`, receiptForgeryTenant); err != nil {
		t.Fatalf("mark dead_lettered: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	svc := deadletterredaction.New(deadletterredaction.Config{Store: store, Emit: store, Signer: signer, Clock: func() time.Time { return at }})
	if n, err := svc.RedactForUser(ctx, receiptForgeryTenant, "alice@acme.com"); err != nil || n != 1 {
		t.Fatalf("RedactForUser = %d, %v; want 1", n, err)
	}
}

// attackerSignature signs the stored receipt's tuple under a key the
// platform does not hold, as a database writer without KMS access can.
func attackerSignature(t *testing.T, r audit.RedactionReceipt) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	input, err := audit.ReceiptSigningInput(r)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	rInt, sInt, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	rInt.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])
	return sig
}

// TestForgedRedactionReceiptLeavesTheRowBrokenAndPages pins that a
// database writer cannot authorize a redaction: a receipt whose signature
// was made by a key outside the KMS, whether it claims the trusted key
// identifier or names the writer's own key, or whose signed columns were
// rewritten after signing, leaves the redacted row broken in the chain
// verifier and counts as missing in the periodic receipt scan, whose
// callback feeds lenny_audit_redaction_receipt_missing_total and the
// AuditRedactionReceiptMissing alert.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §16.5
// (Alerting Rules and SLOs)
// diagnosis: a forged or altered receipt row in audit_redaction_receipts
// is accepted as authorizing a redacted_gdpr discontinuity, so a writer
// with database access can disguise a rewritten audit row as a lawful
// GDPR redaction; the receipt verifier is trusting a key identifier it
// should not, or is not covering every signed column.
func TestForgedRedactionReceiptLeavesTheRowBrokenAndPages(t *testing.T) {
	for _, chain := range []struct {
		name     string
		payloads []string
	}{
		{"a redacted row inside the chain", insideTheChain},
		{"a redacted row that was the tail when the erasure appended its events", asTheTail},
	} {
		t.Run(chain.name, func(t *testing.T) { forgeReceiptsOf(t, chain.payloads) })
	}
}

// forgeReceiptsOf seeds a chain of payloads with its first row redacted
// under a genuine receipt, then forges that receipt in each way a database
// writer can and checks that the chain verifier and the receipt scan report
// every forgery.
func forgeReceiptsOf(t *testing.T, payloads []string) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	ctx := context.Background()
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	seedRedactedRow(t, ctx, pg, store, signer, payloads)

	receipts, err := store.Receipts(ctx, receiptForgeryTenant)
	if err != nil {
		t.Fatal(err)
	}
	genuine, ok := receipts[1]
	if !ok {
		t.Fatalf("no receipt at seq 1: %+v", receipts)
	}
	if vr, err := store.Verify(ctx, receiptForgeryTenant); err != nil || vr.Integrity != audit.ChainRedactedGDPR {
		t.Fatalf("genuine receipt: chain = %+v, %v; want redacted_gdpr", vr, err)
	}

	forgeries := []struct {
		name string
		sql  string
		args func() []any
	}{
		{
			name: "a signature by another key under the trusted key identifier",
			sql:  `UPDATE audit_redaction_receipts SET signature = $2 WHERE receipt_id = $1::uuid`,
			args: func() []any { return []any{genuine.ReceiptID, attackerSignature(t, genuine)} },
		},
		{
			name: "a signature by the writer's own key under its own key identifier",
			sql:  `UPDATE audit_redaction_receipts SET signature = $2, signature_kms_key_id = 'arn:aws:kms:us-east-1:999999999999:key/attacker' WHERE receipt_id = $1::uuid`,
			args: func() []any { return []any{genuine.ReceiptID, attackerSignature(t, genuine)} },
		},
		{
			name: "a signed column rewritten after signing",
			sql:  `UPDATE audit_redaction_receipts SET legal_basis = 'operator_acknowledged_override' WHERE receipt_id = $1::uuid`,
			args: func() []any { return []any{genuine.ReceiptID} },
		},
		{
			name: "an unsigned receipt",
			sql:  `UPDATE audit_redaction_receipts SET signature = NULL WHERE receipt_id = $1::uuid`,
			args: func() []any { return []any{genuine.ReceiptID} },
		},
	}
	restore := func() {
		t.Helper()
		if _, err := pg.Pool.Exec(ctx, `UPDATE audit_redaction_receipts
			SET signature = $2, signature_kms_key_id = $3, legal_basis = $4 WHERE receipt_id = $1::uuid`,
			genuine.ReceiptID, genuine.Signature, genuine.KeyID, genuine.LegalBasis); err != nil {
			t.Fatalf("restore the genuine receipt: %v", err)
		}
	}
	for _, f := range forgeries {
		t.Run(f.name, func(t *testing.T) {
			defer restore()
			if _, err := pg.Pool.Exec(ctx, f.sql, f.args()...); err != nil {
				t.Fatalf("forge the receipt: %v", err)
			}
			vr, err := store.Verify(ctx, receiptForgeryTenant)
			if err != nil {
				t.Fatal(err)
			}
			if vr.Integrity != audit.ChainBroken || vr.BreakSeq != 1 {
				t.Errorf("chain = %q at %d (%s), want broken at 1", vr.Integrity, vr.BreakSeq, vr.Detail)
			}
			expectOnePage(t, ctx, pg, keys)
		})
	}
}

// TestARedactedRowRewrittenAgainAfterItsReceiptReadsBrokenAndPages pins
// that a receipt authorizes only the content it was signed for: a writer
// with the erasure grant that rewrites an already-redacted row's payload,
// whether to other content that keeps the redaction marker or back to the
// row's original content, and leaves the genuine receipt in place, leaves
// the row broken in the chain verifier and counts it missing in the
// periodic receipt scan, which pages AuditRedactionReceiptMissing.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §16.5
// (Alerting Rules and SLOs)
// diagnosis: a redacted audit row whose payload was rewritten after its
// receipt was signed still reads redacted_gdpr or verified, so a writer
// with the lenny_erasure grant can plant content in a redacted row or undo
// an erasure; the chain verifier or the periodic scan does not compare the
// row's recomputed content hash with the receipt's signed new_hash, or does
// not read a row that has a receipt but lost the redaction marker.
func TestARedactedRowRewrittenAgainAfterItsReceiptReadsBrokenAndPages(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	ctx := context.Background()
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	seedRedactedRow(t, ctx, pg, store, signer, insideTheChain)
	if vr, err := store.Verify(ctx, receiptForgeryTenant); err != nil || vr.Integrity != audit.ChainRedactedGDPR {
		t.Fatalf("genuine redaction: chain = %+v, %v; want redacted_gdpr", vr, err)
	}
	var genuine string
	if err := pg.Pool.QueryRow(ctx, `SELECT payload::text FROM audit_log WHERE tenant_id = $1 AND sequence_number = 1`,
		receiptForgeryTenant).Scan(&genuine); err != nil {
		t.Fatal(err)
	}
	for _, rewrite := range []struct{ name, payload string }{
		{"other content that keeps the redaction marker", `{"redacted": true, "event_type": "session.created", "note": "planted after the receipt"}`},
		{"the original content", `{"user_id":"alice@acme.com","note":"secret"}`},
	} {
		t.Run(rewrite.name, func(t *testing.T) {
			rewriteAuditPayload(t, ctx, pg, rewrite.payload)
			defer rewriteAuditPayload(t, ctx, pg, genuine)
			vr, err := store.Verify(ctx, receiptForgeryTenant)
			if err != nil {
				t.Fatal(err)
			}
			if vr.Integrity != audit.ChainBroken || vr.BreakSeq != 1 {
				t.Errorf("chain = %q at %d (%s), want broken at 1", vr.Integrity, vr.BreakSeq, vr.Detail)
			}
			expectOnePage(t, ctx, pg, keys)
		})
	}
}

// rewriteAuditPayload rewrites the payload of the test tenant's first audit
// row under erasure mode, as a writer holding the lenny_erasure grant can.
func rewriteAuditPayload(t *testing.T, ctx context.Context, pg *containers.Postgres, payload string) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{
		"SELECT set_config('app.current_tenant', '" + receiptForgeryTenant + "', true)",
		"SET LOCAL lenny.erasure_mode = 'true'",
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_log SET payload = $2::jsonb, payload_canonical_json = $2::jsonb
		WHERE tenant_id = $1 AND sequence_number = 1`, receiptForgeryTenant, payload); err != nil {
		t.Fatalf("rewrite the redacted row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// expectOnePage runs one cycle of the periodic integrity check and requires
// exactly one receipt-missing signal, for the test tenant. The signal feeds
// lenny_audit_redaction_receipt_missing_total and the
// AuditRedactionReceiptMissing alert.
func expectOnePage(t *testing.T, ctx context.Context, pg *containers.Postgres, keys audit.ReceiptVerifier) {
	t.Helper()
	var paged []string
	p := &integrity.PeriodicCheck{
		DB: pg.Pool, CtrlDB: pg.Pool,
		Cfg:             integrity.PeriodicConfig{ChainSampleN: 1000},
		ReceiptVerifier: keys,
		OnRedactionReceiptMissing: func(tenantID string, n int) {
			for i := 0; i < n; i++ {
				paged = append(paged, tenantID)
			}
		},
	}
	p.CheckOnce(ctx)
	if len(paged) != 1 || paged[0] != receiptForgeryTenant {
		t.Errorf("receipt-missing signal = %v, want one for %s", paged, receiptForgeryTenant)
	}
}

// TestAReceiptPlantedForARewrittenTailRowLeavesItBrokenAndPages pins the
// tail case of a planted receipt: a database writer rewrites the tenant's
// newest audit row to the redacted form and inserts a receipt for it,
// signed by a key outside the KMS under the trusted key identifier, before
// the next append. The append links to the planted receipt's original_hash,
// as it links to any redacted tail row's receipt, and the chain verifier
// still reports the rewritten row broken and the receipt scan pages, so the
// append path taking the link from the receipt gives the writer nothing.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §16.5
// (Alerting Rules and SLOs)
// diagnosis: a rewritten tail row with a planted receipt reads verified or
// redacted_gdpr, so a writer with database access can disguise a rewrite
// of the newest audit row as a lawful GDPR redaction; the verifier accepts
// the receipt's original_hash for the link without verifying its signature.
func TestAReceiptPlantedForARewrittenTailRowLeavesItBrokenAndPages(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	ctx := context.Background()
	keys, _ := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	seedTenantChain(t, ctx, pg)
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	tail, err := store.Append(ctx, receiptForgeryTenant, "session.created", json.RawMessage(`{"user_id":"alice@acme.com","note":"secret"}`), at)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	redacted := audit.BuildRedactedPayload(tail.EventType, "mapping_missing", at)
	rewriteAuditPayload(t, ctx, pg, string(audit.CanonicalPayload(redacted)))
	rewritten, err := store.Get(ctx, receiptForgeryTenant, tail.Seq)
	if err != nil {
		t.Fatal(err)
	}
	planted := audit.RedactionReceipt{
		ReceiptID:        "00000000-0000-4000-8000-000000000001",
		AuditEventID:     tail.ID,
		TenantID:         receiptForgeryTenant,
		RedactedSeq:      tail.Seq,
		OriginalHash:     tail.Hash,
		NewHash:          rewritten.Hash,
		ErasureJobID:     "00000000-0000-4000-8000-000000000002",
		LegalBasis:       "gdpr_art17",
		RedactorIdentity: "attacker",
		Timestamp:        at,
		KeyID:            keys.Keys()[0].KeyID,
	}
	planted.Signature = attackerSignature(t, planted)
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO audit_redaction_receipts (
			receipt_id, audit_event_id, tenant_id, sequence_number, original_hash, new_hash,
			erasure_job_id, legal_basis, redactor_identity, "timestamp", signature, signature_kms_key_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, decode($5, 'hex'), decode($6, 'hex'), $7::uuid, $8, $9, $10, $11, $12)`,
		planted.ReceiptID, planted.AuditEventID, planted.TenantID, int64(planted.RedactedSeq),
		planted.OriginalHash, planted.NewHash, planted.ErasureJobID, planted.LegalBasis,
		planted.RedactorIdentity, planted.Timestamp, planted.Signature, planted.KeyID); err != nil {
		t.Fatalf("plant the receipt: %v", err)
	}
	next, err := store.Append(ctx, receiptForgeryTenant, "session.created", json.RawMessage(`{"x":1}`), at)
	if err != nil {
		t.Fatalf("append after the rewrite: %v", err)
	}
	if next.PrevHash != tail.Hash {
		t.Errorf("the next row's prev_hash = %s, want the planted receipt's original_hash %s", next.PrevHash, tail.Hash)
	}
	vr, err := store.Verify(ctx, receiptForgeryTenant)
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainBroken || vr.BreakSeq != tail.Seq {
		t.Errorf("chain = %q at %d (%s), want broken at %d", vr.Integrity, vr.BreakSeq, vr.Detail, tail.Seq)
	}
	expectOnePage(t, ctx, pg, keys)
}
