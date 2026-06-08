// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed quotacheckpoint.Store: the §11.2
// durable checkpoint of the Redis token-usage counters. It persists each
// window total to the token_usage_checkpoint table so the §11.2 line 48
// reconstruction and the §24.6 operator reconcile can restore the counters
// via the MAX rule on Redis recovery.
//
// checkpoint_at is stamped server-side with clock_timestamp() rather than
// a client-supplied value so a future staleness sweep compares against the
// database clock.
//
// spec: §11.2 lines 42-48; §24.6 line 99.
package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
)

// Store is the Postgres-backed quotacheckpoint.Store. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ quotacheckpoint.Store = (*Store)(nil)

// Write upserts each checkpoint row. Rows are grouped by tenant so each
// tenant's writes run under its own RLS transaction. On conflict only
// token_total and checkpoint_at are updated (checkpoint_at from
// clock_timestamp()).
func (s *Store) Write(ctx context.Context, rows []quotacheckpoint.Row) error {
	if len(rows) == 0 {
		return nil
	}
	byTenant := make(map[string][]quotacheckpoint.Row)
	for _, r := range rows {
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r)
	}
	for tenantID, trows := range byTenant {
		if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
			for _, r := range trows {
				if _, err := tx.Exec(ctx,
					`INSERT INTO token_usage_checkpoint
					     (tenant_id, scope, subject_id, period, window_label, token_total, checkpoint_at)
					 VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
					 ON CONFLICT (tenant_id, scope, subject_id, period, window_label)
					 DO UPDATE SET token_total = EXCLUDED.token_total,
					               checkpoint_at = clock_timestamp()`,
					r.TenantID, r.Scope, r.SubjectID, r.Period, r.WindowLabel, r.TokenTotal); err != nil {
					return fmt.Errorf("quotacheckpoint/pgstore: upsert %s/%s tenant %q: %w", r.Scope, r.SubjectID, r.TenantID, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// AddTenantTotal atomically folds delta into the per-tenant rollup
// (scope='tenant', subject_id=”) token_total for one window and returns
// the resulting authoritative total. The INSERT … ON CONFLICT DO UPDATE
// runs in a single statement so concurrent reconciles from several gateway
// replicas serialize on the row rather than racing a read-modify-write; no
// replica's contribution is lost. A zero delta reads the current total
// (the §12.4 line 268 startup slice draw) while still materialising the
// row. It is the quotabudget.CheckpointAdder used by the
// in_memory_reconciled enforcement mode. spec: §12.4 line 268; §11.2 line 44.
func (s *Store) AddTenantTotal(ctx context.Context, tenantID, period, windowLabel string, delta int64) (int64, error) {
	var total int64
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO token_usage_checkpoint
			     (tenant_id, scope, subject_id, period, window_label, token_total, checkpoint_at)
			 VALUES ($1, 'tenant', '', $2, $3, $4, clock_timestamp())
			 ON CONFLICT (tenant_id, scope, subject_id, period, window_label)
			 DO UPDATE SET token_total = token_usage_checkpoint.token_total + EXCLUDED.token_total,
			               checkpoint_at = clock_timestamp()
			 RETURNING token_total`,
			tenantID, period, windowLabel, delta).Scan(&total)
	}); err != nil {
		return 0, fmt.Errorf("quotacheckpoint/pgstore: add tenant total tenant %q period %q window %q: %w", tenantID, period, windowLabel, err)
	}
	return total, nil
}

// ListActive reads every checkpoint row across all tenants under the
// platform-admin __all__ sentinel, for the recovery reconstruction.
func (s *Store) ListActive(ctx context.Context) ([]quotacheckpoint.Row, error) {
	var out []quotacheckpoint.Row
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT tenant_id, scope, subject_id, period, window_label, token_total, checkpoint_at
			 FROM token_usage_checkpoint`)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = scanRows(rows)
		return err
	}); err != nil {
		return nil, fmt.Errorf("quotacheckpoint/pgstore: list active: %w", err)
	}
	return out, nil
}

// ListByTenant reads every checkpoint row for tenantID under its own RLS
// transaction, for a per-tenant reconcile.
func (s *Store) ListByTenant(ctx context.Context, tenantID string) ([]quotacheckpoint.Row, error) {
	var out []quotacheckpoint.Row
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT tenant_id, scope, subject_id, period, window_label, token_total, checkpoint_at
			 FROM token_usage_checkpoint WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = scanRows(rows)
		return err
	}); err != nil {
		return nil, fmt.Errorf("quotacheckpoint/pgstore: list tenant %q: %w", tenantID, err)
	}
	return out, nil
}

// DeleteByUser removes the per-user checkpoint rows for (tenantID, userID).
// The tenant rollup (scope='tenant') is not a per-user row and survives a
// single user's erasure. A subject with no rows is a no-op returning
// (0, nil). spec: §12.1 line 5 (mandatory primitive); §12.8 step 6.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	var deleted int
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM token_usage_checkpoint
			 WHERE tenant_id = $1 AND scope = 'user' AND subject_id = $2`,
			tenantID, userID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	}); err != nil {
		return 0, fmt.Errorf("quotacheckpoint/pgstore: delete by user tenant=%q user=%q: %w", tenantID, userID, err)
	}
	return deleted, nil
}

// DeleteByTenant removes every checkpoint row for tenantID. RLS scopes the
// DELETE to the tenant; the returned count is the rows removed. spec:
// §12.1 line 5 (mandatory primitive); §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	var deleted int
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM token_usage_checkpoint WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	}); err != nil {
		return 0, fmt.Errorf("quotacheckpoint/pgstore: delete by tenant %q: %w", tenantID, err)
	}
	return deleted, nil
}

// scanRows reads a result set into Rows.
func scanRows(rows pgx.Rows) ([]quotacheckpoint.Row, error) {
	var out []quotacheckpoint.Row
	for rows.Next() {
		var r quotacheckpoint.Row
		if err := rows.Scan(&r.TenantID, &r.Scope, &r.SubjectID, &r.Period,
			&r.WindowLabel, &r.TokenTotal, &r.CheckpointAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
