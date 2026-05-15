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

// Verify runs every §11.7 integrity check and joins the failures, so
// a caller sees all of them rather than just the first.
func Verify(ctx context.Context, db Querier) error {
	return errors.Join(
		VerifyGrants(ctx, db),
		VerifyTriggersEnabled(ctx, db),
		VerifyErasureGuard(ctx, db),
	)
}
