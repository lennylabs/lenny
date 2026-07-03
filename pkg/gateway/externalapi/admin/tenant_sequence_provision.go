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
// the primary CREATE SEQUENCE is an idempotent no-op under IF NOT EXISTS.
//
// A nil billingAuditDDLPool leaves provisioning inactive: the in-memory /
// SQLite topology uses no Postgres sequence, and the store's own
// MAX-based assignment stays in effect there.
//
// The helper is idempotent: CREATE SEQUENCE IF NOT EXISTS is a no-op on
// a retried create, and the setval re-seed converges on the same value.
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
	if err := createSequence(ctx, r.billingAuditDDLPool, billingSeq); err != nil {
		return fmt.Errorf("provision billing sequence for tenant %s: %w", tenantID, err)
	}
	if err := createSequence(ctx, r.billingAuditDDLPool, auditSeq); err != nil {
		return fmt.Errorf("provision audit sequence for tenant %s: %w", tenantID, err)
	}

	// The §13.3 issued-token write-before-issue path seals its per-tenant
	// audit row on the primary pool, not the billing/audit shard, so the
	// audit sequence must exist on the primary as well. In the
	// single-instance topology primaryDDLPool == billingAuditDDLPool and
	// this is an idempotent no-op. spec: §12.3, §11.7.
	if r.primaryDDLPool != nil && r.primaryDDLPool != r.billingAuditDDLPool {
		if err := createSequence(ctx, r.primaryDDLPool, auditSeq); err != nil {
			return fmt.Errorf("provision primary audit sequence for tenant %s: %w", tenantID, err)
		}
	}

	// Re-seed each sequence to the tenant's current per-tenant
	// MAX(sequence_number) when the ledger already holds rows numbered by
	// the pre-Path-A MAX+1 scheme, so the first nextval does not collide
	// with an existing (tenant_id, sequence_number) primary key. The
	// re-seed runs on the DDL connection that owns the sequence (holding
	// the UPDATE privilege setval needs) and reads the ledger MAX through
	// the DDL role's SELECT grant inside a SET LOCAL app.current_tenant
	// tenant-RLS context, which the FORCE ROW LEVEL SECURITY hard-error
	// policy on both ledger tables requires (an unset GUC raises
	// configuration_invalid). spec: §11.2.1, §11.7.
	if err := reseedSequence(ctx, r.billingAuditDDLPool, tenantID, "billing_events", billingSeq); err != nil {
		return fmt.Errorf("re-seed billing sequence for tenant %s: %w", tenantID, err)
	}
	if err := reseedSequence(ctx, r.billingAuditDDLPool, tenantID, "audit_log", auditSeq); err != nil {
		return fmt.Errorf("re-seed audit sequence for tenant %s: %w", tenantID, err)
	}
	if r.primaryDDLPool != nil && r.primaryDDLPool != r.billingAuditDDLPool {
		// The primary carries its own audit_log sub-chain for the §13.3
		// issued-token write path; re-seed its audit sequence from that
		// instance's per-tenant MAX independently.
		if err := reseedSequence(ctx, r.primaryDDLPool, tenantID, "audit_log", auditSeq); err != nil {
			return fmt.Errorf("re-seed primary audit sequence for tenant %s: %w", tenantID, err)
		}
	}
	return nil
}

// createSequence issues CREATE SEQUENCE IF NOT EXISTS for a derived
// sequence name on the given CREATE-privileged DDL pool. The name is a
// seqname-derived identifier (literal prefix + 40-hex digest), so it is
// injection-safe by construction and interpolated directly (DDL cannot
// bind identifiers).
//
// spec: §15.1, §10.2.
func createSequence(ctx context.Context, pool *pgxpool.Pool, name string) error {
	stmt := "CREATE SEQUENCE IF NOT EXISTS " + name + " START WITH 1 INCREMENT BY 1 NO CYCLE"
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("create sequence %s: %w", name, err)
	}
	return nil
}

// reseedSequence sets seqName's value to the tenant's current
// MAX(sequence_number) in table when the ledger already holds rows, so
// the next nextval returns a value strictly greater than the existing
// per-tenant maximum. It runs inside pgtenant.InTx on the DDL connection
// so the FORCE ROW LEVEL SECURITY hard-error policy on the ledger admits
// the tenant's own rows (an unset app.current_tenant would raise
// configuration_invalid). A tenant with no rows has MAX 0, in which case
// the sequence is left at its fresh START WITH 1 and the first nextval
// returns 1.
//
// setval(seq, v, is_called): with is_called true, the next nextval
// returns v+1. For a tenant whose rows top out at MAX=N the re-seed sets
// the sequence to N (is_called true) so the next nextval returns N+1,
// strictly above the existing maximum.
//
// spec: §11.2.1, §11.7.
func reseedSequence(ctx context.Context, pool *pgxpool.Pool, tenantID, table, seqName string) error {
	// table is a compile-time-constant ledger name ("billing_events" /
	// "audit_log") and seqName is the seqname-derived identifier, both
	// safe to interpolate; pgtenant.InTx validates the tenant id before
	// setting the RLS GUC.
	return pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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
		if _, err := tx.Exec(ctx,
			"SELECT setval('"+seqName+"', $1, true)", maxSeq); err != nil {
			return fmt.Errorf("setval %s to %d: %w", seqName, maxSeq, err)
		}
		return nil
	})
}
