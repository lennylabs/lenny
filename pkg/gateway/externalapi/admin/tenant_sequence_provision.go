// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// provisionTenantSequences creates the per-tenant billing and audit
// Postgres sequences a runtime-created tenant needs before its first
// billing or audit event, and re-seeds each sequence to the tenant's
// current MAX(sequence_number) when the ledger already holds rows.
//
// §11.2.1 assigns billing_events.sequence_number and §11.7 assigns
// audit_log.sequence_number by nextval on a dedicated per-tenant
// sequence (billing_seq_<40hex> / audit_seq_<40hex>, the §10.2
// length-bounded safe-derived names), which retains its counter across
// the retention sweep and §12.8 teardown deletes that a MAX+1 scheme
// cannot survive. §15.1 mandates the tenant-create handler provision
// both sequences, because a fresh install has no tenants when the deploy
// migration runs (§17.4), so the first billing/audit Append for a
// runtime-created tenant would otherwise call nextval on a nonexistent
// relation and reject the write. Both runtime tenant-creation paths
// (handleCreateTenant for POST /v1/admin/tenants and upsertTenants for
// the POST /v1/admin/bootstrap seed path) call this one helper so the
// sequence-creation logic has a single canonical implementation.
//
// The DDL runs through the CREATE-privileged DDL connection (the
// migration-0173 lenny_ddl role), not the lenny_app pool the StoreRouter
// resolves for Append: lenny_app holds no CREATE ON SCHEMA. The billing
// and audit sequences are created on the billing/audit instance where
// billing_events and audit_log live; the audit sequence is additionally
// created on the primary instance, because the §13.3 issued-token
// write-before-issue path seals its per-tenant audit row on the primary
// (issuedtokenstore.New(w.pgPool)) rather than the billing/audit shard,
// so its nextval('audit_seq_<tenant-40hex>') must resolve there too. In
// the single-instance topology primaryDDLPool == billingAuditDDLPool and
// the primary limb is skipped because the shard limb already created the
// audit sequence on that same instance.
//
// A nil billingAuditDDLPool leaves provisioning inactive: the in-memory /
// SQLite topology uses no Postgres sequence, and the store's own
// MAX-based assignment stays in effect there.
//
// The helper is idempotent under the §15.1 CREATE SEQUENCE IF NOT EXISTS
// form: a retried or concurrent create is a no-op for the sequence object
// itself, and the to_regclass pre-check skips the re-seed when the sequence
// was already present, so a live sequence that has already handed out values
// is never dragged backward on a repeat call. A concurrent provision that
// races past the pre-check does not error on 42P07 (IF NOT EXISTS absorbs
// it) and its re-seed is GREATEST-clamped so it cannot regress the winner's
// advance.
//
// spec: §15.1, §11.2.1, §11.7, §10.2. F-11.2.10.
func (r *Router) provisionTenantSequences(ctx context.Context, tenantID string) error {
	if r.billingAuditDDLPool == nil {
		// In-memory / SQLite topology: no Postgres sequence to provision.
		return nil
	}

	billingSeq := seqname.BillingSequenceName(tenantID)
	auditSeq := seqname.AuditSequenceName(tenantID)

	// Billing and audit sequences on the billing/audit instance, where
	// billing_events (StoreRouter.BillingShard) and audit_log
	// (StoreRouter.AuditShard) physically live. Both derived names are a
	// fixed literal prefix plus a 40-hex digest, injection-safe by
	// construction, so no identifier allowlisting is needed before
	// interpolation. spec: §15.1, §10.2.
	if err := provisionSequence(ctx, r.billingAuditDDLPool, tenantID, "billing_events", billingSeq); err != nil {
		return fmt.Errorf("provision billing sequence for tenant %s: %w", tenantID, err)
	}
	if err := provisionSequence(ctx, r.billingAuditDDLPool, tenantID, "audit_log", auditSeq); err != nil {
		return fmt.Errorf("provision audit sequence for tenant %s: %w", tenantID, err)
	}

	// The §13.3 issued-token write-before-issue path seals its per-tenant
	// audit row on the primary pool, not the billing/audit shard, so the
	// audit sequence must exist on the primary as well. In the
	// single-instance topology primaryDDLPool == billingAuditDDLPool and
	// this is skipped because the shard limb already provisioned it. The
	// primary carries its own audit_log sub-chain for the §13.3
	// issued-token write path, so a newly-created primary audit sequence
	// re-seeds from that instance's per-tenant MAX independently.
	// spec: §12.3, §11.7.
	if r.primaryDDLPool != nil && r.primaryDDLPool != r.billingAuditDDLPool {
		if err := provisionSequence(ctx, r.primaryDDLPool, tenantID, "audit_log", auditSeq); err != nil {
			return fmt.Errorf("provision primary audit sequence for tenant %s: %w", tenantID, err)
		}
	}
	return nil
}

// provisionSequence issues the §15.1 CREATE SEQUENCE IF NOT EXISTS for the
// derived per-tenant sequence and, only on the newly-created path, re-seeds
// it to the tenant's current MAX(sequence_number) so the first nextval
// clears any rows the ledger already held under the pre-Path-A MAX+1 scheme.
// The existence pre-check, the CREATE, and the re-seed all run in one
// transaction on the CREATE-privileged DDL connection. The IF NOT EXISTS
// form makes the create itself atomic against a concurrent provision (the
// loser no-ops instead of raising 42P07), and the to_regclass pre-check
// fences the re-seed to the case where this call found the sequence absent,
// so a retried provision that observes the sequence already present skips
// the re-seed and never drags a live sequence backward.
//
// Fencing the re-seed to the newly-created case is the monotonicity
// guarantee. A Postgres sequence legitimately advances past the committed
// MAX(sequence_number) after a transaction rollback (§11.2.1: "Postgres
// sequences may produce gaps on transaction rollback"), so an
// unconditional setval to MAX would drag an already-live sequence back
// below a value it had already issued and reuse a (tenant_id,
// sequence_number) primary key. Running the re-seed only when this call
// created the sequence, plus clamping the setval target to at least the
// sequence's current value, guarantees the sequence never moves backward
// below its issued high-water mark.
//
// The whole operation runs inside pgtenant.InTx so the FORCE ROW LEVEL
// SECURITY hard-error policy on the ledger admits the tenant's own rows
// during the MAX read (an unset app.current_tenant would raise
// configuration_invalid). table is a compile-time-constant ledger name
// ("billing_events" / "audit_log") and seqName is the seqname-derived
// identifier, both injection-safe to interpolate (DDL cannot bind
// identifiers); pgtenant.InTx validates the tenant id before setting the
// RLS GUC.
//
// spec: §15.1, §11.2.1, §11.7, §10.2.
func provisionSequence(ctx context.Context, pool *pgxpool.Pool, tenantID, table, seqName string) error {
	return pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		created, err := createSequenceIfAbsent(ctx, tx, seqName)
		if err != nil {
			return err
		}
		if !created {
			// A retried or repeat provision of an already-live sequence: leave
			// it exactly where the previous provision and any subsequent
			// nextval calls advanced it. Re-seeding here would risk moving the
			// sequence backward below an already-issued value.
			return nil
		}
		return reseedNewSequence(ctx, tx, tenantID, table, seqName)
	})
}

// createSequenceIfAbsent issues the §15.1-mandated CREATE SEQUENCE IF NOT
// EXISTS for the derived sequence and reports whether this call created a
// fresh sequence, which fences the re-seed to the newly-created case
// (Decision 7). It first reads to_regclass to decide re-seed intent: a NULL
// result means the sequence is absent and this call intends to create and
// re-seed it, a non-NULL result means another provision already created it
// and this call leaves it untouched. The CREATE is then issued in the
// spec-mandated IF NOT EXISTS form so a concurrent provision that raced past
// the to_regclass check (a create racing the bootstrap seed path, or the
// same create retried concurrently across two gateway replicas) degrades to
// a benign no-op rather than a 42P07 "relation already exists" that would
// fail the tenant create closed. The to_regclass snapshot inside a READ
// COMMITTED transaction does not serialize against another transaction's
// in-flight CREATE, so IF NOT EXISTS supplies the atomicity the pre-check
// alone cannot; the pre-check only chooses whether this call attempts the
// re-seed, and a lost race there simply skips a re-seed the winning call
// already performed.
//
// The name is a seqname-derived identifier (literal prefix + 40-hex
// digest), injection-safe by construction and interpolated directly
// because DDL cannot bind identifiers.
//
// spec: §15.1, §10.2.
func createSequenceIfAbsent(ctx context.Context, tx pgx.Tx, seqName string) (bool, error) {
	var regclass *string
	if err := tx.QueryRow(ctx, "SELECT to_regclass($1)", seqName).Scan(&regclass); err != nil {
		return false, fmt.Errorf("check sequence %s existence: %w", seqName, err)
	}
	if _, err := tx.Exec(ctx, createSequenceStmt(seqName)); err != nil {
		return false, fmt.Errorf("create sequence %s: %w", seqName, err)
	}
	// regclass == nil is the pre-check's "absent, so this call intends to
	// create and re-seed" verdict. IF NOT EXISTS makes a concurrent create
	// race benign; a call that lost that race still reports created=true here
	// but its re-seed is clamped by GREATEST to the sequence's current value,
	// so it cannot regress a sequence the winner already advanced.
	return regclass == nil, nil
}

// createSequenceStmt builds the §15.1-mandated per-tenant sequence DDL. The
// IF NOT EXISTS form is load-bearing: it makes the CREATE atomic against a
// concurrent provision of the same tenant sequence, so a racing loser no-ops
// instead of raising 42P07. seqName is a seqname-derived identifier (literal
// prefix + 40-hex digest), injection-safe by construction and interpolated
// directly because DDL cannot bind identifiers.
//
// spec: §15.1, §10.2.
func createSequenceStmt(seqName string) string {
	return "CREATE SEQUENCE IF NOT EXISTS " + seqName + " START WITH 1 INCREMENT BY 1 NO CYCLE"
}

// reseedNewSequence sets a freshly-created seqName to the tenant's current
// MAX(sequence_number) in table when the ledger already holds rows, so the
// next nextval returns a value strictly greater than the existing
// per-tenant maximum and does not collide with an existing (tenant_id,
// sequence_number) primary key. A tenant with no rows has MAX 0, in which
// case the sequence is left at its fresh START WITH 1 and the first
// nextval returns 1.
//
// setval(seq, v, is_called): with is_called true, the next nextval returns
// v+1. For a tenant whose rows top out at MAX=N the re-seed sets the
// sequence to N (is_called true) so the next nextval returns N+1, strictly
// above the existing maximum. The target is clamped to at least the
// sequence's current value with GREATEST, so even under a concurrent
// nextval on the just-created sequence the setval can never move it
// backward below a value already issued.
//
// spec: §11.2.1, §11.7.
func reseedNewSequence(ctx context.Context, tx pgx.Tx, tenantID, table, seqName string) error {
	var maxSeq int64
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(MAX(sequence_number), 0) FROM "+table+" WHERE tenant_id = $1",
		tenantID).Scan(&maxSeq); err != nil {
		return fmt.Errorf("read MAX(sequence_number) from %s: %w", table, err)
	}
	if maxSeq == 0 {
		// No pre-existing rows: leave the fresh sequence at START WITH 1.
		return nil
	}
	// Clamp to the sequence's current last_value so the setval is monotonic:
	// it never regresses the sequence below a value nextval already handed
	// out on this just-created object. For a fresh sequence with no nextval
	// draws, last_value is 1 and is_called is false, so GREATEST(maxSeq, 1)
	// resolves to maxSeq and the next nextval returns maxSeq+1 as required.
	if _, err := tx.Exec(ctx,
		"SELECT setval('"+seqName+"', GREATEST($1, (SELECT last_value FROM "+seqName+")), true)",
		maxSeq); err != nil {
		return fmt.Errorf("setval %s to %d: %w", seqName, maxSeq, err)
	}
	return nil
}
