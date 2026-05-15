// SPDX-License-Identifier: MIT

package assertions

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// RLSQuerier is the slice of database/sql.DB that the per-tenant
// isolation probe needs. Production code passes *sql.DB; tests
// can stub with a mock if they don't have a real Postgres in
// scope.
type RLSQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// TenantIsolation asserts §13 per-tenant Row-Level Security on
// the named table by:
//
//  1. setting `app.current_tenant` to tenantA and reading the table;
//  2. setting `app.current_tenant` to tenantB and reading the table;
//  3. asserting each tenant sees ONLY their own rows.
//
// The pkColumn names the primary-key column the helper selects.
// The tenantColumn names the column the RLS policy filters on.
// Both must already exist in the schema; the helper does not
// create the table.
//
// The test must have already inserted rows for both tenants
// before calling this helper; isolation is asserted on the
// existing data set.
//
// Usage:
//
//	pg := containers.NewPostgres(t)
//	// ... apply schema + insert rows for tenant-a and tenant-b ...
//	assertions.TenantIsolation(t, pg.DB, "sessions",
//	    assertions.TenantIsolationOptions{
//	        TenantA:      "tenant-a",
//	        TenantB:      "tenant-b",
//	        PKColumn:     "id",
//	        TenantColumn: "tenant_id",
//	    })
type TenantIsolationOptions struct {
	TenantA      string
	TenantB      string
	PKColumn     string
	TenantColumn string
}

// TenantIsolation runs the isolation probe. t.Fatalf on the
// first violation; t.Errorf for cleanup-issue diagnostics.
func TenantIsolation(t testing.TB, q RLSQuerier, table string, opts TenantIsolationOptions) {
	t.Helper()
	if opts.TenantA == "" || opts.TenantB == "" {
		t.Fatalf("TenantIsolation: TenantA and TenantB must both be set")
	}
	if opts.TenantA == opts.TenantB {
		t.Fatalf("TenantIsolation: TenantA and TenantB must differ; got %q", opts.TenantA)
	}
	if opts.PKColumn == "" {
		opts.PKColumn = "id"
	}
	if opts.TenantColumn == "" {
		opts.TenantColumn = "tenant_id"
	}
	ctx := context.Background()

	rowsForTenant := func(tenant string) (idsSeen []string, ownersSeen []string) {
		if _, err := q.ExecContext(ctx, "SET LOCAL app.current_tenant = $1", tenant); err != nil {
			t.Fatalf("TenantIsolation: set local app.current_tenant=%q: %v", tenant, err)
		}
		query := fmt.Sprintf("SELECT %s, %s FROM %s", opts.PKColumn, opts.TenantColumn, table)
		rs, err := q.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("TenantIsolation: query %s as tenant %q: %v", table, tenant, err)
		}
		defer rs.Close()
		for rs.Next() {
			var id, owner string
			if err := rs.Scan(&id, &owner); err != nil {
				t.Fatalf("TenantIsolation: scan %s row: %v", table, err)
			}
			idsSeen = append(idsSeen, id)
			ownersSeen = append(ownersSeen, owner)
		}
		if err := rs.Err(); err != nil {
			t.Fatalf("TenantIsolation: rows iter for tenant %q: %v", tenant, err)
		}
		return idsSeen, ownersSeen
	}

	idsA, ownersA := rowsForTenant(opts.TenantA)
	idsB, ownersB := rowsForTenant(opts.TenantB)

	// 1. Each tenant must see at least one row of its own data.
	if len(idsA) == 0 {
		t.Errorf("TenantIsolation: tenant %q sees zero rows of %s; expected its own data to be visible (was anything inserted?)", opts.TenantA, table)
	}
	if len(idsB) == 0 {
		t.Errorf("TenantIsolation: tenant %q sees zero rows of %s; expected its own data to be visible (was anything inserted?)", opts.TenantB, table)
	}

	// 2. Every row tenant A sees must carry tenant A's id.
	for i, owner := range ownersA {
		if owner != opts.TenantA {
			t.Fatalf("TenantIsolation: tenant %q saw row id=%q owned by %q; RLS leaked cross-tenant data",
				opts.TenantA, idsA[i], owner)
		}
	}
	// 3. Every row tenant B sees must carry tenant B's id.
	for i, owner := range ownersB {
		if owner != opts.TenantB {
			t.Fatalf("TenantIsolation: tenant %q saw row id=%q owned by %q; RLS leaked cross-tenant data",
				opts.TenantB, idsB[i], owner)
		}
	}

	// 4. The two tenants must not share any row IDs.
	seen := map[string]bool{}
	for _, id := range idsA {
		seen[id] = true
	}
	for _, id := range idsB {
		if seen[id] {
			t.Fatalf("TenantIsolation: row id=%q visible to both tenants %q and %q; expected disjoint result sets",
				id, opts.TenantA, opts.TenantB)
		}
	}
}
