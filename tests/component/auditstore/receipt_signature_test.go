//go:build component

// SPDX-License-Identifier: MIT

package auditstore_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/receiptkeys"
)

// TestRedactionReceiptSignedByOneReplicaVerifiesFromPostgresOnAnother pins
// that a receipt one gateway replica writes through the dead-letter
// redaction verifies after a Postgres round trip on a second replica that
// shares only the KMS master key: the timestamptz column returns the
// signed instant, the hashes and identifiers return the signed encodings,
// the chain reads redacted_gdpr, and the periodic receipt scan counts
// nothing missing. A replica over another master key trusts none of them.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
// diagnosis: a receipt written by the redaction service does not verify
// when read back from audit_redaction_receipts on another replica: the
// signed tuple and the stored columns disagree (timestamp precision, hash
// encoding, a column the scan drops), or the key identifier recorded on
// the receipt is not the one the verifier trusts.
func TestRedactionReceiptSignedByOneReplicaVerifiesFromPostgresOnAnother(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")
	keyFile := filepath.Join(t.TempDir(), "master.key")
	refs := map[string]string{kms.AliasAuditSigning: "audit-key-v1"}
	_, signerA := receiptkeys.Load(t, keyFile, refs)
	keysB, _ := receiptkeys.Load(t, keyFile, refs)

	writer := auditstore.New(pg.Router(t))
	// A nanosecond clock exercises the microsecond normalization.
	at := time.Date(2026, 11, 26, 10, 0, 0, 123456789, time.UTC)
	for _, payload := range []string{`{"user_id":"alice@acme.com","note":"secret"}`, `{"actor":"bob@acme.com"}`} {
		if _, err := writer.Append(ctx, "acme", "session.created", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	markDeadLettered(t, ctx, pg, "acme", 1)
	svc := deadletterredaction.New(deadletterredaction.Config{
		Store: writer, Emit: writer, Signer: signerA,
		Clock: func() time.Time { return at },
	})
	if n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com"); err != nil || n != 1 {
		t.Fatalf("RedactForUser = %d, %v; want 1", n, err)
	}

	reader := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keysB))
	receipts, err := reader.Receipts(ctx, "acme")
	if err != nil {
		t.Fatalf("Receipts: %v", err)
	}
	rcpt, ok := receipts[1]
	if !ok {
		t.Fatalf("no receipt at seq 1: %+v", receipts)
	}
	if rcpt.KeyID != "audit-key-v1" || len(rcpt.Signature) != 64 {
		t.Errorf("receipt key id %q, %d-byte signature; want audit-key-v1 and 64 bytes", rcpt.KeyID, len(rcpt.Signature))
	}
	if err := keysB.VerifyReceipt(rcpt); err != nil {
		t.Fatalf("replica B rejected replica A's receipt read from Postgres: %v", err)
	}
	vr, err := reader.Verify(ctx, "acme")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vr.Integrity != audit.ChainRedactedGDPR {
		t.Fatalf("chain = %q at %d (%s), want redacted_gdpr", vr.Integrity, vr.BreakSeq, vr.Detail)
	}
	missing, err := integrity.CheckRedactionReceipts(ctx, pg.Pool, keysB)
	if err != nil {
		t.Fatalf("CheckRedactionReceipts: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("receipt scan counted missing receipts: %+v", missing)
	}

	otherKeys, _ := receiptkeys.Load(t, "", refs)
	vr, err = auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(otherKeys)).Verify(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainBroken || vr.BreakSeq != 1 {
		t.Fatalf("chain under another KMS = %q at %d, want broken at 1", vr.Integrity, vr.BreakSeq)
	}
	vr, err = auditstore.New(pg.Router(t)).Verify(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainBroken {
		t.Fatalf("chain with no receipt verifier = %q, want broken", vr.Integrity)
	}
}
