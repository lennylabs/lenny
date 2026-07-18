//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: §4.9 (cross-tenant credential-delivery combinations)
// diagnosis: migration 0175 did not add the delivery_mode, spiffe_binding,
// allow_direct_mode_standard_isolation, and
// allow_proxy_mode_spiffe_binding_disabled columns to sandbox_warm_pools
// with the correct NOT NULL/DEFAULT, or its .down.sql did not drop them.
// The two text fields carry the pool-definition credential-delivery
// combination the pool-registration and admission layers inspect and
// default to '' (inherit the runtime default). The two opt-in booleans are
// deployer acknowledgments of the same class as allow_standard_isolation and
// must default to false so a pool that never opts in acknowledges no
// forbidden combination (fail closed): a true default would silently
// acknowledge the direct+standard or proxy+disabled combination the deployer
// never opted into.
func TestProdMigration0175PoolDeliveryModeColumns(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// The two text combination fields: NOT NULL, default '' (inherit).
	for _, col := range []string{"delivery_mode", "spiffe_binding"} {
		mustHaveColumn(t, ctx, pg, "sandbox_warm_pools", col)
		if got := columnType(t, ctx, pg, "sandbox_warm_pools", col); got != "text" {
			t.Errorf("sandbox_warm_pools.%s type: got %q, want text", col, got)
		}
		mustBeNotNull(t, ctx, pg, "sandbox_warm_pools", col)
		if def := columnDefault(t, ctx, pg, "sandbox_warm_pools", col); def != "''::text" {
			t.Errorf("sandbox_warm_pools.%s default: got %q, want ''::text (inherit)", col, def)
		}
	}

	// The two opt-in acknowledgments: NOT NULL, default false (no
	// acknowledgment). A true default would fail open.
	for _, col := range []string{
		"allow_direct_mode_standard_isolation",
		"allow_proxy_mode_spiffe_binding_disabled",
	} {
		mustHaveColumn(t, ctx, pg, "sandbox_warm_pools", col)
		if got := columnType(t, ctx, pg, "sandbox_warm_pools", col); got != "boolean" {
			t.Errorf("sandbox_warm_pools.%s type: got %q, want boolean", col, got)
		}
		mustBeNotNull(t, ctx, pg, "sandbox_warm_pools", col)
		if def := columnDefault(t, ctx, pg, "sandbox_warm_pools", col); def != "false" {
			t.Errorf("sandbox_warm_pools.%s default: got %q, want false (fail closed)", col, def)
		}
	}

	// Rolling migration 0175 back drops all four columns.
	pg.MigrateTo(t, dir, 174)
	for _, col := range []string{
		"delivery_mode", "spiffe_binding",
		"allow_direct_mode_standard_isolation",
		"allow_proxy_mode_spiffe_binding_disabled",
	} {
		mustNotHaveColumn(t, ctx, pg, "sandbox_warm_pools", col)
	}
}

// mustBeNotNull fails the test when the named column is nullable. The §4.9
// combination fields and their opt-in acknowledgments carry server-side
// DEFAULTs, so the NOT NULL constraint is safe under the §10.5 expand-
// contract rule and keeps the credential-delivery combination total.
func mustBeNotNull(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) {
	t.Helper()
	var isNullable string
	if err := pg.Pool.QueryRow(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, col).Scan(&isNullable); err != nil {
		t.Fatalf("read is_nullable of %s.%s: %v", table, col, err)
	}
	if isNullable != "NO" {
		t.Errorf("%s.%s is_nullable: got %q, want NO", table, col, isNullable)
	}
}

// columnDefault returns the column_default expression Postgres records for
// the named column, or "" when the column has no default.
func columnDefault(t *testing.T, ctx context.Context, pg *containers.Postgres, table, col string) string {
	t.Helper()
	var def *string
	if err := pg.Pool.QueryRow(ctx, `
		SELECT column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, col).Scan(&def); err != nil {
		t.Fatalf("read column_default of %s.%s: %v", table, col, err)
	}
	if def == nil {
		return ""
	}
	return *def
}
