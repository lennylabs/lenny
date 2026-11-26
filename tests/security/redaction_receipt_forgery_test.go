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

// seedRedactedRow appends two audit rows for alice, dead-letters the first,
// and redacts it through the dead-letter redaction service with signer, so
// the row carries a validly signed receipt.
func seedRedactedRow(t *testing.T, ctx context.Context, pg *containers.Postgres, store *auditstore.Store, signer deadletterredaction.Signer) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, receiptForgeryTenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(receiptForgeryTenant)+
		" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for _, payload := range []string{`{"user_id":"alice@acme.com","note":"secret"}`, `{"actor":"bob@acme.com"}`} {
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
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	ctx := context.Background()
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	seedRedactedRow(t, ctx, pg, store, signer)

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
		})
	}
}

// TestARedactedRowRewrittenAgainAfterItsReceiptReadsBrokenAndPages pins
// that a receipt authorizes only the content it was signed for: a writer
// with the erasure grant that rewrites an already-redacted row's payload,
// keeping the redaction marker and leaving the genuine receipt in place,
// leaves the row broken in the chain verifier and counts it missing in the
// periodic receipt scan, which pages AuditRedactionReceiptMissing.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §16.5
// (Alerting Rules and SLOs)
// diagnosis: a redacted audit row whose payload was rewritten after its
// receipt was signed still reads redacted_gdpr, so a writer with the
// lenny_erasure grant can plant content in a redacted row; the chain
// verifier or the periodic scan does not compare the row's recomputed
// content hash with the receipt's signed new_hash.
func TestARedactedRowRewrittenAgainAfterItsReceiptReadsBrokenAndPages(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	ctx := context.Background()
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	seedRedactedRow(t, ctx, pg, store, signer)
	if vr, err := store.Verify(ctx, receiptForgeryTenant); err != nil || vr.Integrity != audit.ChainRedactedGDPR {
		t.Fatalf("genuine redaction: chain = %+v, %v; want redacted_gdpr", vr, err)
	}

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
	planted := `{"redacted": true, "event_type": "session.created", "note": "planted after the receipt"}`
	if _, err := tx.Exec(ctx, `UPDATE audit_log SET payload = $2::jsonb, payload_canonical_json = $2::jsonb
		WHERE tenant_id = $1 AND sequence_number = 1`, receiptForgeryTenant, planted); err != nil {
		t.Fatalf("rewrite the redacted row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	vr, err := store.Verify(ctx, receiptForgeryTenant)
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainBroken || vr.BreakSeq != 1 {
		t.Errorf("rewritten again: chain = %q at %d (%s), want broken at 1", vr.Integrity, vr.BreakSeq, vr.Detail)
	}
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
