//go:build component

// SPDX-License-Identifier: MIT

// Production-schema component test for migration 0188
// (audit_redaction_receipts_drop_fk), which drops the foreign key from
// audit_redaction_receipts.audit_event_id onto audit_log.id. A
// RedactionReceipt outlives the audit row it names (§12.8 RedactionReceipt
// schema): the §16.4 retention sweep and tenant-deletion Phase 4 delete
// audit rows, including PII-redacted dead-lettered ones, and never delete
// receipts. The test drives both deletes through the audit store against a
// row redacted by the dead-letter redaction service and checks that each
// succeeds, that the receipts and the gdpr.* rows remain, and that each
// receipt's signature still verifies over the stored tuple.
package migrations_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/audit/jcs"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/deadletterredaction"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// receiptFKPriorVersion is the schema version immediately below the
// migration that drops the receipt foreign key.
const receiptFKPriorVersion = 187

// receiptFKName is the default name Postgres gave the inline REFERENCES
// on audit_redaction_receipts.audit_event_id in migration 0160.
const receiptFKName = "audit_redaction_receipts_audit_event_id_fkey"

// receiptSigningKey is the HMAC key the test's redaction service signs
// receipts with; the verification below recomputes the MAC under it.
var receiptSigningKey = []byte("0123456789abcdef0123456789abcdef")

// Audit timestamps: oldAt predates the retention cutoff, recentAt does not.
var (
	receiptOldAt    = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	receiptRecentAt = time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	receiptCutoff   = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
)

// spec: 12.8 (Compliance Interfaces: RedactionReceipt schema), 16.4 (Logging)
// diagnosis: migration 0188 did not drop the audit_redaction_receipts
//
//	foreign key onto audit_log.id, or its .down.sql does not re-add it NOT
//	VALID. With the key in place the §16.4 retention sweep and the
//	tenant-deletion Phase 4 audit delete fail with SQLSTATE 23503 on any
//	tenant holding a PII-redacted dead-lettered row, so retention stalls
//	and tenant deletion cannot finish. A receipt missing after the delete,
//	or one whose signature no longer verifies, means the receipt store was
//	mutated rather than left to outlive the row.
func TestAuditRedactionReceiptsDropFKMigration_spec_12_8(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	if n := receiptForeignKeyCount(t, ctx, pg); n != 0 {
		t.Fatalf("audit_redaction_receipts carries %d foreign keys after migration 0188, want 0", n)
	}
	if def := indexDef(t, ctx, pg, "idx_audit_redaction_receipts_event"); def == "" {
		t.Errorf("idx_audit_redaction_receipts_event is missing after migration 0188")
	}
	mustHaveTablePrivilege(t, ctx, pg, "lenny_erasure", "audit_redaction_receipts", "INSERT")
	mustHaveTablePrivilege(t, ctx, pg, "lenny_app", "audit_redaction_receipts", "SELECT")

	store := auditstore.New(pg.Router(t))
	seedReceiptTenant(t, ctx, pg, "acme")
	redactAliceDeadLetters(t, ctx, pg, store)

	t.Run("retention sweep deletes an aged redacted row and keeps its receipt", func(t *testing.T) {
		assertPruneKeepsReceipt(t, ctx, pg, store)
	})
	t.Run("tenant delete removes redacted rows and keeps receipts and gdpr rows", func(t *testing.T) {
		assertTenantDeleteKeepsReceipts(t, ctx, pg, store)
	})

	pg.MigrateTo(t, dir, receiptFKPriorVersion)
	assertReceiptForeignKeyNotValid(t, ctx, pg)
}

// seedReceiptTenant inserts the tenant row and provisions its per-tenant
// audit sequence, which the store's sealed append draws from.
func seedReceiptTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant %s: %v", tenant, err)
	}
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+
			" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence for %s: %v", tenant, err)
	}
}

// redactAliceDeadLetters appends alice's aged row (seq 1), alice's recent
// row (seq 2), and bob's recent row (seq 3), marks alice's rows
// dead_lettered, and redacts them through the dead-letter redaction
// service, which writes one signed receipt per row and four gdpr.* events.
func redactAliceDeadLetters(t *testing.T, ctx context.Context, pg *containers.Postgres, store *auditstore.Store) {
	t.Helper()
	appends := []struct {
		eventType, payload string
		at                 time.Time
	}{
		{"session.created", `{"user_id":"alice@acme.com","note":"aged"}`, receiptOldAt},
		{"session.created", `{"sub":"alice@acme.com"}`, receiptRecentAt},
		{"tool.called", `{"actor":"bob@acme.com"}`, receiptRecentAt},
	}
	for _, a := range appends {
		if _, err := store.Append(ctx, "acme", a.eventType, json.RawMessage(a.payload), a.at); err != nil {
			t.Fatalf("append %s: %v", a.eventType, err)
		}
	}
	if err := execTenant(ctx, pg, "acme",
		`UPDATE audit_log SET ocsf_translation_state = 'dead_lettered'
		 WHERE tenant_id = 'acme' AND sequence_number IN (1, 2)`); err != nil {
		t.Fatalf("mark alice's rows dead_lettered: %v", err)
	}
	svc := deadletterredaction.New(deadletterredaction.Config{
		Store:  store,
		Emit:   store,
		Signer: deadletterredaction.NewHMACReceiptSigner("boot", receiptSigningKey),
		Clock:  func() time.Time { return receiptRecentAt },
	})
	n, err := svc.RedactForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("RedactForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("redacted %d rows, want alice's 2", n)
	}
	assertReceiptsVerify(t, ctx, pg, 2)
}

// assertPruneKeepsReceipt runs the §16.4 retention sweep with no SIEM
// configured and a cutoff between the aged and the recent rows. Only the
// aged redacted row is deleted; its receipt stays and still verifies.
func assertPruneKeepsReceipt(t *testing.T, ctx context.Context, pg *containers.Postgres, store *auditstore.Store) {
	t.Helper()
	pruned, err := store.PruneRetention(ctx, "acme", auditstore.PruneOptions{GeneralCutoff: receiptCutoff})
	if isForeignKeyViolation(err) {
		t.Fatalf("PruneRetention on a redacted row raised a foreign-key violation: %v", err)
	}
	if err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	if pruned != 1 {
		t.Errorf("PruneRetention deleted %d rows, want the aged redacted row only", pruned)
	}
	if n := countAuditRows(t, ctx, pg, "acme", "sequence_number = 1"); n != 0 {
		t.Errorf("aged redacted row survives the retention sweep: %d rows", n)
	}
	assertReceiptsVerify(t, ctx, pg, 2)
}

// assertTenantDeleteKeepsReceipts runs the tenant-deletion Phase 4 audit
// delete: every non-gdpr.* row goes, including the recent redacted row,
// while both receipts and the four gdpr.* redaction events remain.
func assertTenantDeleteKeepsReceipts(t *testing.T, ctx context.Context, pg *containers.Postgres, store *auditstore.Store) {
	t.Helper()
	deleted, err := store.DeleteByTenant(ctx, "acme")
	if isForeignKeyViolation(err) {
		t.Fatalf("DeleteByTenant on a tenant with a redacted row raised a foreign-key violation: %v", err)
	}
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 2 {
		t.Errorf("DeleteByTenant deleted %d rows, want the recent redacted row and bob's row", deleted)
	}
	if n := countAuditRows(t, ctx, pg, "acme", "event_type NOT LIKE 'gdpr.%'"); n != 0 {
		t.Errorf("%d non-gdpr rows survive the tenant delete, want 0", n)
	}
	if n := countAuditRows(t, ctx, pg, "acme", "event_type LIKE 'gdpr.%'"); n != 4 {
		t.Errorf("tenant delete leaves %d gdpr rows, want the 4 redaction events", n)
	}
	assertReceiptsVerify(t, ctx, pg, 2)
}

// assertReceiptForeignKeyNotValid checks that the down migration re-adds
// the key under its original name, NOT VALID, which Postgres accepts even
// though both receipts now name deleted audit rows.
func assertReceiptForeignKeyNotValid(t *testing.T, ctx context.Context, pg *containers.Postgres) {
	t.Helper()
	var validated bool
	err := pg.Pool.QueryRow(ctx, `
		SELECT convalidated FROM pg_constraint
		WHERE conrelid = 'audit_redaction_receipts'::regclass
		  AND contype = 'f' AND conname = $1`, receiptFKName).Scan(&validated)
	if err != nil {
		t.Fatalf("read %s after rolling 0188 back: %v", receiptFKName, err)
	}
	if validated {
		t.Errorf("%s re-added as validated, want NOT VALID", receiptFKName)
	}
}

// receiptForeignKeyCount counts the foreign keys declared on
// audit_redaction_receipts.
func receiptForeignKeyCount(t *testing.T, ctx context.Context, pg *containers.Postgres) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'audit_redaction_receipts'::regclass AND contype = 'f'`).Scan(&n); err != nil {
		t.Fatalf("count receipt foreign keys: %v", err)
	}
	return n
}

// countAuditRows counts the tenant's audit_log rows matching where, read
// on the owner pool under the tenant's context.
func countAuditRows(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, where string) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND `+where, tenant).Scan(&n); err != nil {
		t.Fatalf("count audit rows (%s): %v", where, err)
	}
	return n
}

// assertReceiptsVerify reads every receipt of tenant acme and recomputes
// its HMAC over the JCS serialization of the signed tuple, so a receipt
// whose audit_event_id or any other signed column changed fails here.
func assertReceiptsVerify(t *testing.T, ctx context.Context, pg *containers.Postgres, want int) {
	t.Helper()
	rows, err := pg.Pool.Query(ctx, `
		SELECT receipt_id::text, audit_event_id::text, tenant_id, sequence_number,
		       original_hash, new_hash, erasure_job_id::text, legal_basis,
		       redactor_identity, "timestamp", signature
		FROM audit_redaction_receipts WHERE tenant_id = 'acme'`)
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	defer rows.Close()
	got := 0
	for rows.Next() {
		var (
			receiptID, eventID, tenant, jobID, basis, redactor string
			seq                                                int64
			origHash, newHash, sig                             []byte
			at                                                 time.Time
		)
		if err := rows.Scan(&receiptID, &eventID, &tenant, &seq, &origHash, &newHash,
			&jobID, &basis, &redactor, &at, &sig); err != nil {
			t.Fatalf("scan receipt: %v", err)
		}
		got++
		if eventID == "" {
			t.Errorf("receipt %s lost its audit_event_id", receiptID)
		}
		tuple, err := json.Marshal(map[string]any{
			"receipt_id":        receiptID,
			"audit_event_id":    eventID,
			"tenant_id":         tenant,
			"sequence_number":   uint64(seq),
			"original_hash":     hex.EncodeToString(origHash),
			"new_hash":          hex.EncodeToString(newHash),
			"erasure_job_id":    jobID,
			"legal_basis":       basis,
			"redactor_identity": redactor,
			"timestamp":         at.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatalf("marshal receipt tuple: %v", err)
		}
		canon, err := jcs.Canonicalize(tuple)
		if err != nil {
			t.Fatalf("canonicalize receipt tuple: %v", err)
		}
		mac := hmac.New(sha256.New, receiptSigningKey)
		mac.Write(canon)
		if !hmac.Equal(mac.Sum(nil), sig) {
			t.Errorf("receipt %s (seq %d) signature does not verify over the stored tuple", receiptID, seq)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate receipts: %v", err)
	}
	if got != want {
		t.Errorf("acme holds %d receipts, want %d", got, want)
	}
}

// isForeignKeyViolation reports whether err is SQLSTATE 23503.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
