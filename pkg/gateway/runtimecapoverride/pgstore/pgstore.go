// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §5.1 line 49 per-tenant runtime
// capability override store. It persists rows to the
// runtime_capability_overrides table from migration 0154 and applies the
// §12.3 tenant-context RLS guard via pgtenant.InTx.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// Store is the Postgres-backed per-tenant runtime capability override
// store.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ runtimecapoverride.Store = (*Store)(nil)

// Get returns the override for (tenantID, runtime).
func (s *Store) Get(ctx context.Context, tenantID, runtime string) (runtimestore.CapabilityOverride, bool, error) {
	var (
		out   runtimestore.CapabilityOverride
		found bool
	)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var raw []byte
		row := tx.QueryRow(ctx,
			`SELECT override FROM runtime_capability_overrides
				WHERE tenant_id = $1 AND runtime_name = $2`,
			tenantID, runtime)
		if err := row.Scan(&raw); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return runtimestore.CapabilityOverride{}, false, err
	}
	return out, found, nil
}

// Put upserts the override for (tenantID, runtime).
func (s *Store) Put(ctx context.Context, tenantID, runtime string, o runtimestore.CapabilityOverride) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO runtime_capability_overrides (tenant_id, runtime_name, override, updated_at)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (tenant_id, runtime_name)
				DO UPDATE SET override = EXCLUDED.override, updated_at = now()`,
			tenantID, runtime, raw)
		return err
	})
}

// Delete removes the override for (tenantID, runtime). Deleting a missing
// override is not an error.
func (s *Store) Delete(ctx context.Context, tenantID, runtime string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM runtime_capability_overrides
				WHERE tenant_id = $1 AND runtime_name = $2`,
			tenantID, runtime)
		return err
	})
}

// List returns every override scoped to tenantID, keyed by runtime name.
func (s *Store) List(ctx context.Context, tenantID string) (map[string]runtimestore.CapabilityOverride, error) {
	out := map[string]runtimestore.CapabilityOverride{}
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT runtime_name, override FROM runtime_capability_overrides
				WHERE tenant_id = $1
				ORDER BY runtime_name`,
			tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name string
				raw  []byte
			)
			if err := rows.Scan(&name, &raw); err != nil {
				return err
			}
			var o runtimestore.CapabilityOverride
			if err := json.Unmarshal(raw, &o); err != nil {
				return err
			}
			out[name] = o
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
