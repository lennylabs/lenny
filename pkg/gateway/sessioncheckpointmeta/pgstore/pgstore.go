// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §10.1 session_checkpoint_meta
// store. It persists barrier metadata to the session_checkpoint_meta
// table from migration 0148 and applies the §12.3 tenant-context RLS
// guard via pgtenant.InTx.
//
// spec: §10.1 lines 178-181, 393.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/sessioncheckpointmeta"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed §10.1 session_checkpoint_meta store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. now selects the timestamp
// source; a nil now uses time.Now in UTC.
func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, now: now}
}

var _ sessioncheckpointmeta.Store = (*Store)(nil)

// Upsert inserts or overwrites the single row for (tenant, session)
// via an ON CONFLICT update so a barrier on a session that already has
// a row replaces the prior generation's metadata.
func (s *Store) Upsert(ctx context.Context, r sessioncheckpointmeta.Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("sessioncheckpointmeta: tenant and session ids are required")
	}
	now := s.now()
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_checkpoint_meta (
				tenant_id, session_id, coordination_generation,
				barrier_id, last_tool_call_id, last_tool_call_sequence,
				checkpoint_ref, workspace_recovery_fraction, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (tenant_id, session_id) DO UPDATE SET
				coordination_generation = EXCLUDED.coordination_generation,
				barrier_id = EXCLUDED.barrier_id,
				last_tool_call_id = EXCLUDED.last_tool_call_id,
				last_tool_call_sequence = EXCLUDED.last_tool_call_sequence,
				checkpoint_ref = EXCLUDED.checkpoint_ref,
				workspace_recovery_fraction = EXCLUDED.workspace_recovery_fraction,
				updated_at = EXCLUDED.updated_at`,
			r.TenantID, r.SessionID, r.CoordinationGeneration,
			r.BarrierID, r.LastToolCallID, r.LastToolCallSequence,
			r.CheckpointRef, r.WorkspaceRecoveryFraction, now)
		return err
	})
}

// Get returns the row for (tenantID, sessionID) or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (sessioncheckpointmeta.Record, error) {
	var out sessioncheckpointmeta.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var frac *float64
		row := tx.QueryRow(ctx,
			`SELECT tenant_id, session_id, coordination_generation,
				barrier_id, last_tool_call_id, last_tool_call_sequence,
				checkpoint_ref, workspace_recovery_fraction, updated_at
			FROM session_checkpoint_meta
			WHERE tenant_id = $1 AND session_id = $2`,
			tenantID, sessionID)
		scanErr := row.Scan(&out.TenantID, &out.SessionID, &out.CoordinationGeneration,
			&out.BarrierID, &out.LastToolCallID, &out.LastToolCallSequence,
			&out.CheckpointRef, &frac, &out.UpdatedAt)
		if scanErr != nil {
			return scanErr
		}
		out.WorkspaceRecoveryFraction = frac
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessioncheckpointmeta.Record{}, sessioncheckpointmeta.ErrNotFound
	}
	if err != nil {
		return sessioncheckpointmeta.Record{}, err
	}
	return out, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// sessionIDs. The §12.8 orchestrator owns the session-id lookup.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if tenantID == "" {
		return errors.New("sessioncheckpointmeta: erasure requires a non-empty tenant_id")
	}
	if len(sessionIDs) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_checkpoint_meta
			WHERE tenant_id = $1 AND session_id = ANY($2)`,
			tenantID, sessionIDs)
		return err
	})
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return errors.New("sessioncheckpointmeta: erasure requires a non-empty tenant_id")
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_checkpoint_meta WHERE tenant_id = $1`, tenantID)
		return err
	})
}
