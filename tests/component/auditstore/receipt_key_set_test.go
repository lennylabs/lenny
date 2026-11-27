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

// TestARetiredListedReceiptKeyVerifiesFromPostgresAfterTwoRotations pins
// the retired-key list over Postgres: a receipt signed under audit-key-v1
// reads redacted_gdpr in the chain verifier and counts as present in the
// periodic receipt scan two rotations later, when platform:audit-signing
// names v3, platform:audit-verify names v2, and audit-key-v1 is listed as a
// retired key; with the list empty the same row reads broken and the scan
// counts it missing.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
// diagnosis: a receipt signed by a retired receipt key that the gateway
// setting lists stops verifying once neither alias names its key, or a
// verifier trusts a retired key the setting does not list; the receipt key
// set is not built from the aliases and the list, or the KMS lookup by key
// identifier returns another key than the one that signed.
func TestARetiredListedReceiptKeyVerifiesFromPostgresAfterTwoRotations(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")
	keyFile := filepath.Join(t.TempDir(), "master.key")
	_, signerV1 := receiptkeys.Load(t, keyFile, map[string]string{kms.AliasAuditSigning: "audit-key-v1"})

	writer := auditstore.New(pg.Router(t))
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for _, payload := range []string{`{"user_id":"alice@acme.com"}`, `{"actor":"bob@acme.com"}`} {
		if _, err := writer.Append(ctx, "acme", "session.created", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	markDeadLettered(t, ctx, pg, "acme", 1)
	svc := deadletterredaction.New(deadletterredaction.Config{
		Store: writer, Emit: writer, Signer: signerV1, Clock: func() time.Time { return at },
	})
	if n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com"); err != nil || n != 1 {
		t.Fatalf("RedactForUser = %d, %v; want 1", n, err)
	}

	rotated := map[string]string{kms.AliasAuditSigning: "audit-key-v3", kms.AliasAuditVerify: "audit-key-v2"}
	listed, _ := receiptkeys.Load(t, keyFile, rotated, "audit-key-v1")
	unlisted, _ := receiptkeys.Load(t, keyFile, rotated)
	for _, c := range []struct {
		name        string
		keys        *audit.ReceiptKeySet
		want        audit.ChainIntegrity
		wantMissing int
	}{
		{"the retired key listed", listed, audit.ChainRedactedGDPR, 0},
		{"the retired key not listed", unlisted, audit.ChainBroken, 1},
	} {
		vr, err := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(c.keys)).Verify(ctx, "acme")
		if err != nil {
			t.Fatalf("%s: Verify: %v", c.name, err)
		}
		if vr.Integrity != c.want {
			t.Errorf("%s: chain = %q at %d (%s), want %q", c.name, vr.Integrity, vr.BreakSeq, vr.Detail, c.want)
		}
		missing, err := integrity.CheckRedactionReceipts(ctx, pg.Pool, c.keys)
		if err != nil {
			t.Fatalf("%s: CheckRedactionReceipts: %v", c.name, err)
		}
		got := 0
		for _, m := range missing {
			got += m.Missing
		}
		if got != c.wantMissing {
			t.Errorf("%s: receipt scan counted %d missing, want %d", c.name, got, c.wantMissing)
		}
	}
}

// TestASecondErasureOfAUserTheRedactedRowNamedFindsNothingToRedact pins
// that a dead-lettered row is redacted at most once: after the erasure of
// alice redacts a row that also named bob, the erasure of bob selects no
// row, writes no second receipt, and leaves the row's single receipt
// verifying, so no chained receipt is needed for a second redaction.
//
// spec: §12.8 (Compliance Interfaces)
// diagnosis: a later erasure rewrote an already-redacted row or wrote a
// second receipt for it; the dead-letter scan no longer excludes rows that
// carry the redaction marker, or the redacted payload still names a user
// in a key the scan matches.
func TestASecondErasureOfAUserTheRedactedRowNamedFindsNothingToRedact(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for _, payload := range []string{`{"user_id":"alice@acme.com","target_user_id":"bob@acme.com"}`, `{"x":1}`} {
		if _, err := store.Append(ctx, "acme", "session.created", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	markDeadLettered(t, ctx, pg, "acme", 1)
	svc := deadletterredaction.New(deadletterredaction.Config{
		Store: store, Emit: store, Signer: signer, Clock: func() time.Time { return at },
	})
	if n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com"); err != nil || n != 1 {
		t.Fatalf("erasure of alice = %d, %v; want 1", n, err)
	}
	if n, err := svc.RedactForUser(ctx, "acme", "bob@acme.com"); err != nil || n != 0 {
		t.Fatalf("erasure of bob = %d, %v; want 0, because the row no longer names bob", n, err)
	}
	if got := receiptCount(t, ctx, pg, "acme"); got != 1 {
		t.Errorf("receipts = %d, want 1", got)
	}
	vr, err := store.Verify(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainRedactedGDPR {
		t.Errorf("chain = %q at %d (%s), want redacted_gdpr", vr.Integrity, vr.BreakSeq, vr.Detail)
	}
}
