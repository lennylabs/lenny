//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §11.7 audit integrity verification
// (pkg/audit/integrity), exercised against a real Postgres container.
// It confirms Verify accepts the migrated schema and that each check
// detects the corresponding tamper: a forbidden ledger grant, a
// disabled or dropped integrity trigger, and an immutability trigger
// function stripped of its erasure-mode guard.
package auditintegrity_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

func mustExec(t *testing.T, pg *containers.Postgres, sql string) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// spec: 11.7
// diagnosis: integrity.Verify rejected a freshly migrated production
// schema. The audit integrity checks in pkg/audit/integrity disagree
// with the §11.7 schema produced by the migrations under migrations/.
func TestVerifyAcceptsMigratedSchema(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	if err := integrity.Verify(context.Background(), pg.Pool); err != nil {
		t.Errorf("Verify on a freshly migrated schema: %v", err)
	}
}

// spec: 11.7 item 1
// diagnosis: integrity.VerifyGrants did not detect an UPDATE grant on
// the audit_log ledger. The grant-inspection query in
// pkg/audit/integrity fails to flag forbidden write privileges on the
// append-only audit tables.
func TestVerifyGrantsDetectsForbiddenLedgerGrant(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	if err := integrity.VerifyGrants(ctx, pg.Pool); err != nil {
		t.Fatalf("VerifyGrants clean: %v", err)
	}
	// A superuser grants UPDATE on the audit ledger to lenny_app.
	mustExec(t, pg, `GRANT UPDATE ON audit_log TO lenny_app`)
	err := integrity.VerifyGrants(ctx, pg.Pool)
	if err == nil {
		t.Fatal("VerifyGrants should reject an UPDATE grant on audit_log")
	}
	if !strings.Contains(err.Error(), "audit_log") {
		t.Errorf("error should name the offending table: %v", err)
	}
}

// spec: 11.7 item 1 (trigger-enabled check)
// diagnosis: integrity.VerifyTriggersEnabled did not report a disabled
// or dropped integrity trigger. The trigger-enumeration query in
// pkg/audit/integrity misses triggers that are disabled via ALTER
// TABLE or removed via DROP TRIGGER.
func TestVerifyTriggersDetectsDisabledAndDropped(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	if err := integrity.VerifyTriggersEnabled(ctx, pg.Pool); err != nil {
		t.Fatalf("VerifyTriggersEnabled clean: %v", err)
	}
	// A superuser disables the per-tenant guard on one table.
	mustExec(t, pg, `ALTER TABLE sessions DISABLE TRIGGER lenny_tenant_guard`)
	err := integrity.VerifyTriggersEnabled(ctx, pg.Pool)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("VerifyTriggersEnabled should report the disabled trigger, got %v", err)
	}
	mustExec(t, pg, `ALTER TABLE sessions ENABLE TRIGGER lenny_tenant_guard`)
	// Dropping the audit immutability trigger removes the name entirely.
	mustExec(t, pg, `DROP TRIGGER lenny_audit_immutability ON audit_log`)
	err = integrity.VerifyTriggersEnabled(ctx, pg.Pool)
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("VerifyTriggersEnabled should report the dropped trigger, got %v", err)
	}
}

// spec: 11.7 item 7
// diagnosis: integrity.VerifyErasureGuard did not detect a trigger
// function rewritten without its erasure-mode guard clause. The
// function-body inspection in pkg/audit/integrity fails to confirm the
// guard is present in lenny_audit_immutability.
func TestVerifyErasureGuardDetectsStrippedGuard(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	if err := integrity.VerifyErasureGuard(ctx, pg.Pool); err != nil {
		t.Fatalf("VerifyErasureGuard clean: %v", err)
	}
	// A migration rollback replaces the function body, silently
	// dropping the erasure-mode guard clause.
	mustExec(t, pg, `
		CREATE OR REPLACE FUNCTION lenny_audit_immutability() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'audit_log is append-only';
		END;
		$$ LANGUAGE plpgsql`)
	err := integrity.VerifyErasureGuard(ctx, pg.Pool)
	if err == nil || !strings.Contains(err.Error(), "lenny_audit_immutability") {
		t.Fatalf("VerifyErasureGuard should report the stripped guard, got %v", err)
	}
}
