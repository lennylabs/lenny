//go:build component

// SPDX-License-Identifier: MIT

package auditstore_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/audit/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/receiptkeys"
)

// batchEmitter appends each event through Store.AppendBatch, the §12.3
// batched-insert path, so the erasure's own events take that path.
type batchEmitter struct{ store *auditstore.Store }

func (b batchEmitter) Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	return audit.Row{}, b.store.AppendBatch(ctx, []auditbatch.Item{{TenantID: tenantID, EventType: eventType, Payload: payload, At: at}})
}

// receiptOriginalHash reads the original_hash of the receipt for the row
// at seq, as lowercase hex.
func receiptOriginalHash(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string, seq uint64) string {
	t.Helper()
	var orig []byte
	if err := pg.Pool.QueryRow(ctx,
		`SELECT original_hash FROM audit_redaction_receipts WHERE tenant_id = $1 AND sequence_number = $2`,
		tenant, int64(seq)).Scan(&orig); err != nil {
		t.Fatalf("read the receipt of row %d: %v", seq, err)
	}
	return hex.EncodeToString(orig)
}

// TestARowAppendedAfterARedactedTailRowLinksToItsOriginalHash pins the
// append after a redacted tail row over Postgres: when the erasure redacts
// the tenant's newest audit row, the gdpr.erasure_deadletter_redacted event
// it appends next, and a later row, link to the redacted row's preserved
// pre-redaction hash (its receipt's original_hash), so the chain verifier
// reads redacted_gdpr, the periodic receipt scan counts nothing missing, and
// the startup and periodic continuity check reads verified.
// It runs once with the erasure's events on the synchronous append path and
// once on the batched-insert path.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
// diagnosis: the row appended after a redacted tail row carries the tail's
// post-redaction hash as its prev_hash, so the chain reads broken at the
// erasure's own event; the append path's tail read (scanTail) does not
// apply the tail's receipt, or the batched path seals without it.
func TestARowAppendedAfterARedactedTailRowLinksToItsOriginalHash(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	keys, signer := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		tenant string
		emit   deadletterredaction.Emitter
	}{
		{"acme", store},
		{"globex", batchEmitter{store}},
	} {
		t.Run(c.tenant, func(t *testing.T) {
			seedTenant(t, ctx, pg, c.tenant)
			if _, err := store.Append(ctx, c.tenant, "session.created", json.RawMessage(`{"user_id":"alice@acme.com"}`), at); err != nil {
				t.Fatalf("append: %v", err)
			}
			markDeadLettered(t, ctx, pg, c.tenant, 1)
			svc := deadletterredaction.New(deadletterredaction.Config{
				Store: store, Emit: c.emit, Signer: signer, Clock: func() time.Time { return at },
			})
			if n, err := svc.RedactForUser(ctx, c.tenant, "alice@acme.com"); err != nil || n != 1 {
				t.Fatalf("RedactForUser = %d, %v; want 1", n, err)
			}
			if _, err := store.Append(ctx, c.tenant, "session.created", json.RawMessage(`{"x":1}`), at); err != nil {
				t.Fatalf("append after the erasure: %v", err)
			}

			rows, err := store.Rows(ctx, c.tenant)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 4 || rows[1].EventType != "gdpr.erasure_deadletter_redacted" {
				t.Fatalf("rows = %d, second %q; want 4 with the redaction event second", len(rows), rows[1].EventType)
			}
			if want := receiptOriginalHash(t, ctx, pg, c.tenant, rows[0].Seq); rows[1].PrevHash != want {
				t.Errorf("the redaction event's prev_hash = %s, want the receipt's original_hash %s", rows[1].PrevHash, want)
			}
			vr, err := store.Verify(ctx, c.tenant)
			if err != nil {
				t.Fatal(err)
			}
			if vr.Integrity != audit.ChainRedactedGDPR {
				t.Errorf("chain = %q at %d (%s), want redacted_gdpr", vr.Integrity, vr.BreakSeq, vr.Detail)
			}
		})
	}

	missing, err := integrity.CheckRedactionReceipts(ctx, pg.Pool, keys)
	if err != nil {
		t.Fatalf("CheckRedactionReceipts: %v", err)
	}
	for _, m := range missing {
		if m.Missing != 0 {
			t.Errorf("receipt scan counted %d missing for %s, want 0", m.Missing, m.TenantID)
		}
	}
	// The startup and periodic continuity check follows the same link.
	continuity, err := integrity.CheckChainContinuityRecent(ctx, pg.Pool, pg.Pool, 1000)
	if err != nil {
		t.Fatalf("CheckChainContinuityRecent: %v", err)
	}
	for _, r := range continuity {
		if r.Result.Integrity != audit.ChainVerified {
			t.Errorf("continuity of %s = %q at %d (%s), want verified", r.TenantID, r.Result.Integrity, r.Result.BreakSeq, r.Result.Detail)
		}
	}
}

// TestARedactedTailRowWithoutAReceiptStillReadsBroken pins that the append
// path takes a redacted tail row's link from its receipt alone: a tail row
// rewritten to the redacted form with no receipt, the rewrite §12.8 calls a
// tamper, is linked by its current content, and both the chain verifier and
// the periodic receipt scan report it, although the tenant has no receipt
// at all and the successor links to the rewritten content.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
// diagnosis: a redaction-marked tail row with no receipt reads verified or
// redacted_gdpr, or the receipt scan does not count it; the append path or
// the verifier accepts the redaction marker in place of a signed receipt,
// or the verifier skips the marker when the tenant has no receipts.
func TestARedactedTailRowWithoutAReceiptStillReadsBroken(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")
	keys, _ := receiptkeys.Load(t, "", nil)
	store := auditstore.New(pg.Router(t), auditstore.WithReceiptVerifier(keys))
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for _, payload := range []string{`{"x":0}`, `{"user_id":"alice@acme.com"}`} {
		if _, err := store.Append(ctx, "acme", "session.created", json.RawMessage(payload), at); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Rewrite the tail row as a redaction would, but write no receipt.
	redacted := audit.BuildRedactedPayload("session.created", "mapping_missing", at)
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET payload = $2::jsonb, payload_canonical_json = $3::jsonb
		 WHERE tenant_id = 'acme' AND sequence_number = $1`,
		2, string(redacted), string(audit.CanonicalPayload(redacted))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, "acme", "session.created", json.RawMessage(`{"x":1}`), at); err != nil {
		t.Fatalf("append after the rewrite: %v", err)
	}
	vr, err := store.Verify(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if vr.Integrity != audit.ChainBroken || vr.BreakSeq != 2 {
		t.Errorf("chain = %q at %d (%s), want broken at 2", vr.Integrity, vr.BreakSeq, vr.Detail)
	}
	missing, err := integrity.CheckRedactionReceipts(ctx, pg.Pool, keys)
	if err != nil {
		t.Fatalf("CheckRedactionReceipts: %v", err)
	}
	if len(missing) != 1 || missing[0].TenantID != "acme" || missing[0].Missing != 1 {
		t.Errorf("receipt scan = %+v, want one missing receipt for acme", missing)
	}
}
