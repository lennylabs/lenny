// SPDX-License-Identifier: MIT

// Package integrity verifies the §11.7 database-level audit integrity
// controls on a live Postgres connection:
//
//   - the gateway's lenny_app role holds no UPDATE or DELETE grant on
//     the append-only ledger tables (§11.7 item 1);
//   - the tamper-evidence triggers — lenny_tenant_guard and the two
//     ledger immutability triggers — are installed and enabled
//     (§11.7 item 1, trigger-enabled check);
//   - the immutability trigger functions retain the lenny.erasure_mode
//     guard clause that the GDPR erasure path depends on (§11.7
//     item 7).
//
// It also hosts the §12.3 cloud-managed pooler defense
// (VerifyCloudManagedPoolerDefense): under LENNY_POOLER_MODE=external
// the per-transaction lenny_tenant_guard trigger is the load-bearing
// RLS defense, so the gateway refuses to start when it is absent from a
// tenant-scoped table.
//
// The gateway runs Verify at startup; lenny-preflight and the
// periodic background integrity check reuse the same functions. A
// superuser can grant UPDATE/DELETE access or disable a trigger after
// startup, so these checks are also intended to run periodically.
package integrity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// appRole is the gateway's least-privilege database role.
const appRole = "lenny_app"

// erasureRole is the §11.7 item 7 GDPR-erasure database role. Only the
// erasure background job connects as this role.
const erasureRole = "lenny_erasure"

// erasureTables is the closed set of tables on which lenny_erasure may
// legitimately hold a grant per §11.7 item 7: the billing/audit ledgers
// it pseudonymizes/deletes, the erasure_jobs queue it drives, the users
// table it marks processing-restricted, and the audit_redaction_receipts
// table it writes when it redacts a dead-lettered audit row. The spec
// mandates "no grants on non-erasure tables"; any grant on a table
// outside this set is drift that the startup verifier rejects.
//
// spec: §12.8 line 830 — "audit_redaction_receipts is grant-restricted:
// lenny_erasure holds INSERT only" (migration 0160). The erasure job
// persists one signed RedactionReceipt per redacted row, so the INSERT
// grant is an erasure-owned grant rather than scope drift.
var erasureTables = map[string]bool{
	"billing_events":           true,
	"audit_log":                true,
	"erasure_jobs":             true,
	"users":                    true,
	"audit_redaction_receipts": true,
}

// ledgerTables are the §11.7 append-only ledgers: lenny_app may
// INSERT into them but never UPDATE or DELETE.
var ledgerTables = []string{"audit_log", "billing_events"}

// integrityTriggers are the §11.7 tamper-evidence triggers that must
// be installed and enabled.
var integrityTriggers = []string{
	"lenny_tenant_guard",
	"lenny_billing_immutability",
	"lenny_audit_immutability",
}

// immutabilityFunctions are the trigger functions whose body must
// retain the lenny.erasure_mode guard (§11.7 item 7).
var immutabilityFunctions = []string{
	"lenny_audit_immutability",
	"lenny_billing_immutability",
}

// Querier is the read surface the checks need. *pgxpool.Pool
// satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ Querier = (*pgxpool.Pool)(nil)

// VerifyGrants reports an error when the lenny_app role holds an
// UPDATE or DELETE grant on any append-only ledger table. Such a
// grant would let a compromised gateway silently rewrite the audit or
// billing history (§11.7 item 1).
func VerifyGrants(ctx context.Context, db Querier) error {
	rows, err := db.Query(ctx, `
		SELECT table_name, privilege_type
		FROM information_schema.role_table_grants
		WHERE grantee = $1
		  AND table_name = ANY($2)
		  AND privilege_type IN ('UPDATE', 'DELETE')
		ORDER BY table_name, privilege_type`,
		appRole, ledgerTables)
	if err != nil {
		return fmt.Errorf("integrity: query ledger grants: %w", err)
	}
	defer rows.Close()
	var offending []string
	for rows.Next() {
		var table, priv string
		if err := rows.Scan(&table, &priv); err != nil {
			return fmt.Errorf("integrity: scan grant row: %w", err)
		}
		offending = append(offending, fmt.Sprintf("%s on %s", priv, table))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity: iterate grant rows: %w", err)
	}
	if len(offending) > 0 {
		return fmt.Errorf("integrity: role %s holds forbidden ledger grants: %s "+
			"(the append-only ledgers admit INSERT only — §11.7)",
			appRole, strings.Join(offending, ", "))
	}
	return nil
}

// VerifyTriggersEnabled reports an error when a §11.7 integrity
// trigger is missing or has been disabled (pg_trigger.tgenabled =
// 'D'). A superuser can ALTER TABLE ... DISABLE TRIGGER to bypass the
// tamper-evidence and immutability guards without revoking any grant.
func VerifyTriggersEnabled(ctx context.Context, db Querier) error {
	rows, err := db.Query(ctx, `
		SELECT tgname, tgenabled
		FROM pg_trigger
		WHERE tgname = ANY($1) AND NOT tgisinternal`,
		integrityTriggers)
	if err != nil {
		return fmt.Errorf("integrity: query triggers: %w", err)
	}
	defer rows.Close()
	present := map[string]bool{}
	var disabled []string
	for rows.Next() {
		var name string
		var enabled byte
		if err := rows.Scan(&name, &enabled); err != nil {
			return fmt.Errorf("integrity: scan trigger row: %w", err)
		}
		present[name] = true
		if enabled == 'D' {
			disabled = append(disabled, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity: iterate trigger rows: %w", err)
	}
	var missing []string
	for _, name := range integrityTriggers {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(disabled)
	if len(missing) > 0 {
		return fmt.Errorf("integrity: §11.7 triggers absent: %s", strings.Join(missing, ", "))
	}
	if len(disabled) > 0 {
		return fmt.Errorf("integrity: §11.7 triggers disabled: %s", strings.Join(disabled, ", "))
	}
	return nil
}

// TenantGuardCoverageGaps returns the names of tenant-scoped tables
// that lack an enabled lenny_tenant_guard trigger. A table is
// tenant-scoped for this purpose when it has row-level security enabled
// and carries a tenant_id column. An empty result means every such
// table is protected.
//
// The set is computed from the live catalog rather than a hard-coded
// list, so it stays correct as migrations add tables, and it excludes
// tables that are deliberately platform-global (RLS disabled — e.g. the
// §12.5 artifact_store catalog), which carry a tenant_id column but are
// read cross-tenant through annotated platform-admin paths.
//
// spec: §12.3 line 56 — the gateway "queries pg_trigger" for the
// lenny_tenant_guard trigger on tenant-scoped tables. A trigger that has
// been disabled (pg_trigger.tgenabled = 'D', e.g. via ALTER TABLE …
// DISABLE TRIGGER) counts as a gap, matching the VerifyTriggersEnabled
// posture.
func TenantGuardCoverageGaps(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		-- platform-admin-cross-tenant-justification: joins Postgres system catalogs (pg_class, pg_namespace), which carry no tenant_id; this is a schema-metadata coverage check, not a tenant-data query.
		-- platform-admin-cross-tenant-allowed
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r'
		  AND n.nspname = current_schema()
		  AND c.relrowsecurity
		  AND EXISTS (
		      SELECT 1 FROM pg_attribute a
		      WHERE a.attrelid = c.oid
		        AND a.attname = 'tenant_id'
		        AND NOT a.attisdropped
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_trigger t
		      WHERE t.tgrelid = c.oid
		        AND t.tgname = 'lenny_tenant_guard'
		        AND NOT t.tgisinternal
		        AND t.tgenabled <> 'D'
		  )
		ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("integrity: query tenant-guard coverage: %w", err)
	}
	defer rows.Close()
	var gaps []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("integrity: scan coverage row: %w", err)
		}
		gaps = append(gaps, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate coverage rows: %w", err)
	}
	return gaps, nil
}

// CloudManagedPoolerFatalMessage is reproduced verbatim from §12.3
// line 56. The gateway exits with this message when
// LENNY_POOLER_MODE=external but the lenny_tenant_guard trigger is
// absent from one or more tenant-scoped tables, so operators can match
// it against the documented remediation.
const CloudManagedPoolerFatalMessage = "FATAL: cloud-managed pooler mode (LENNY_POOLER_MODE=external) detected but lenny_tenant_guard trigger is absent from tenant-scoped tables — RLS tenant isolation defense is not active; run schema migrations before starting the gateway (Section 12.3)"

// VerifyCloudManagedPoolerDefense enforces the §12.3 lines 49-56 /
// §17.6 line 488 cloud-managed pooler defense at gateway startup. When
// poolerMode is "external" the deployment fronts Postgres with a managed
// proxy (RDS Proxy, Cloud SQL Auth Proxy, Azure PgBouncer integration)
// that cannot run the connect_query __unset__ sentinel, so the
// per-transaction lenny_tenant_guard trigger is the load-bearing RLS
// defense. The check returns CloudManagedPoolerFatalMessage when any
// tenant-scoped table is missing the trigger, so the caller refuses to
// start. Any other poolerMode is a no-op: the in-cluster pooler enforces
// the sentinel via connect_query and the trigger is defense-in-depth.
//
// The check runs independently of the §17.6 preflight Job so that it
// also catches trigger removal after initial installation (e.g. a manual
// migration rollback). spec: §12.3 line 56.
func VerifyCloudManagedPoolerDefense(ctx context.Context, db Querier, poolerMode string) error {
	if poolerMode != "external" {
		return nil
	}
	gaps, err := TenantGuardCoverageGaps(ctx, db)
	if err != nil {
		return err
	}
	if len(gaps) > 0 {
		return errors.New(CloudManagedPoolerFatalMessage)
	}
	return nil
}

// VerifyErasureGuard reports an error when an immutability trigger
// function no longer references current_setting('lenny.erasure_mode').
// Without that guard the GDPR erasure job cannot pseudonymize billing
// rows or delete audit rows; a migration rollback that silently drops
// the clause must fail the gateway startup (§11.7 item 7).
func VerifyErasureGuard(ctx context.Context, db Querier) error {
	for _, fn := range immutabilityFunctions {
		var src string
		err := db.QueryRow(ctx,
			`SELECT prosrc FROM pg_proc WHERE proname = $1`, fn).Scan(&src)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("integrity: trigger function %s is absent", fn)
		}
		if err != nil {
			return fmt.Errorf("integrity: read %s body: %w", fn, err)
		}
		if !strings.Contains(src, "lenny.erasure_mode") {
			return fmt.Errorf("integrity: trigger function %s has lost its "+
				"lenny.erasure_mode guard clause (§11.7 item 7)", fn)
		}
	}
	return nil
}

// VerifyErasureRoleScope reports an error when the lenny_erasure role
// holds a table grant outside the closed erasure table set. §11.7 item 7
// mandates that lenny_erasure have "no grants on non-erasure tables";
// codifying it as a positive startup assertion catches a future
// migration that, say, grants lenny_erasure UPDATE on sessions before it
// silently widens the erasure role's blast radius.
//
// The grants the role legitimately holds (column-scoped UPDATE on
// billing_events, DELETE/INSERT/SELECT on audit_log, the erasure_jobs
// and users grants the erasure path needs) are expected and ignored;
// only a grant on a table absent from erasureTables is reported.
func VerifyErasureRoleScope(ctx context.Context, db Querier) error {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT table_name, privilege_type
		FROM information_schema.role_table_grants
		WHERE grantee = $1
		ORDER BY table_name, privilege_type`, erasureRole)
	if err != nil {
		return fmt.Errorf("integrity: query erasure-role grants: %w", err)
	}
	defer rows.Close()
	var offending []string
	for rows.Next() {
		var table, priv string
		if err := rows.Scan(&table, &priv); err != nil {
			return fmt.Errorf("integrity: scan erasure grant row: %w", err)
		}
		if !erasureTables[table] {
			offending = append(offending, fmt.Sprintf("%s on %s", priv, table))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity: iterate erasure grant rows: %w", err)
	}
	if len(offending) > 0 {
		sort.Strings(offending)
		return fmt.Errorf("integrity: role %s holds grants on non-erasure tables: %s "+
			"(§11.7 item 7 mandates no grants outside the erasure table set)",
			erasureRole, strings.Join(offending, ", "))
	}
	return nil
}

// Verify runs every §11.7 integrity check and joins the failures, so
// a caller sees all of them rather than just the first.
func Verify(ctx context.Context, db Querier) error {
	return errors.Join(
		VerifyGrants(ctx, db),
		VerifyTriggersEnabled(ctx, db),
		VerifyErasureGuard(ctx, db),
		VerifyErasureRoleScope(ctx, db),
	)
}
