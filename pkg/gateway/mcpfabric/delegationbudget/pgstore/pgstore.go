// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed delegationbudget.Store: the
// §11.2 line 29 durable checkpoint of the §8.2 delegation tree budget
// counters. It persists the tree-wide counters to the
// delegation_tree_budget table so the §11.2 line 48 reconstruction can
// restore them via the MAX rule on Redis recovery.
//
// The checkpoint Write touches only the counter columns and
// checkpoint_at; it never clobbers the §8.6 extension_denied /
// cool_off_expiry columns on the same row (a separate Postgres-backed
// BudgetSource owns those). checkpoint_at is stamped server-side with
// clock_timestamp() so the reconstruction's staleness test compares
// against the database clock per §8.6 line 733.
//
// spec: §11.2 lines 29, 44, 48; §12.4 lines 193, 218.
package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed delegationbudget.Store. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var _ delegationbudget.Store = (*Store)(nil)

// Write upserts each checkpoint row. Rows are grouped by tenant so each
// tenant's writes run under its own RLS transaction (app.current_tenant
// set to that tenant). On conflict only the counter columns and
// checkpoint_at are updated; extension_denied / cool_off_expiry are left
// intact. checkpoint_at is stamped from clock_timestamp() rather than a
// client-supplied value.
func (s *Store) Write(ctx context.Context, rows []delegationbudget.Checkpoint) error {
	if len(rows) == 0 {
		return nil
	}
	byTenant := make(map[string][]delegationbudget.Checkpoint)
	for _, r := range rows {
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r)
	}
	for tenantID, trees := range byTenant {
		if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
			for _, t := range trees {
				if _, err := tx.Exec(ctx,
					`INSERT INTO delegation_tree_budget
					     (tenant_id, root_session_id, tree_size, token_budget_consumed, tree_memory_bytes, checkpoint_at)
					 VALUES ($1, $2, $3, $4, $5, clock_timestamp())
					 ON CONFLICT (tenant_id, root_session_id)
					 DO UPDATE SET tree_size = EXCLUDED.tree_size,
					               token_budget_consumed = EXCLUDED.token_budget_consumed,
					               tree_memory_bytes = EXCLUDED.tree_memory_bytes,
					               checkpoint_at = clock_timestamp()`,
					t.TenantID, t.RootSessionID, t.TreeSize, t.TokenBudgetConsumed, t.TreeMemoryBytes); err != nil {
					return fmt.Errorf("delegationbudget/pgstore: upsert tree %q: %w", t.RootSessionID, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListActive reads every checkpoint row across all tenants for the
// recovery reconstruction. It runs under the platform-admin __all__
// sentinel; the caller is responsible for the RBAC gate (the gateway
// recovery loop is platform-internal).
func (s *Store) ListActive(ctx context.Context) ([]delegationbudget.Checkpoint, error) {
	var out []delegationbudget.Checkpoint
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT tenant_id, root_session_id, tree_size, token_budget_consumed, tree_memory_bytes, checkpoint_at
			 FROM delegation_tree_budget`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c delegationbudget.Checkpoint
			if err := rows.Scan(&c.TenantID, &c.RootSessionID, &c.TreeSize,
				&c.TokenBudgetConsumed, &c.TreeMemoryBytes, &c.CheckpointAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	}); err != nil {
		return nil, fmt.Errorf("delegationbudget/pgstore: list active: %w", err)
	}
	return out, nil
}

// DeleteByTenant removes every checkpoint row for tenantID. RLS scopes
// the DELETE to the tenant; the returned count is the rows removed. A
// tenant with no rows is a no-op returning (0, nil).
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	var deleted int
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM delegation_tree_budget WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	}); err != nil {
		return 0, fmt.Errorf("delegationbudget/pgstore: delete by tenant %q: %w", tenantID, err)
	}
	return deleted, nil
}
