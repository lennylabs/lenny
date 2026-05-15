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
func TestVerifyAcceptsMigratedSchema(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	if err := integrity.Verify(context.Background(), pg.Pool); err != nil {
		t.Errorf("Verify on a freshly migrated schema: %v", err)
	}
}

// spec: 11.7 item 1
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
