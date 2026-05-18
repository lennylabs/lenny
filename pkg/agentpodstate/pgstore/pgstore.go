// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed agentpodstate.Store,
// persisting the §4.6.1 Sandbox-status mirror to the agent_pod_state
// table.
//
// agent_pod_state is platform-global (§12.6): the mirror is keyed by
// pod_id and tenant_id is a denormalized convenience column, not an
// isolation boundary, so operations here run as plain queries without
// an app.current_tenant context. This follows the same non-RLS
// platform-store pattern as pkg/gateway/connectorstore/pgstore.
package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
)

// Store is the Postgres-backed agent_pod_state mirror. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ agentpodstate.Store = (*Store)(nil)

// upsertSQL bulk-UPSERTs one mirror row keyed on pod_id. updated_at is
// advanced to now() on every write so MirrorLagSeconds reflects the
// most recent mirror pass.
const upsertSQL = `INSERT INTO agent_pod_state (
	pod_id, pool_id, state, tenant_id, session_id,
	isolation_profile, execution_mode, resource_version, node_name, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (pod_id) DO UPDATE SET
	pool_id = EXCLUDED.pool_id,
	state = EXCLUDED.state,
	tenant_id = EXCLUDED.tenant_id,
	session_id = EXCLUDED.session_id,
	isolation_profile = EXCLUDED.isolation_profile,
	execution_mode = EXCLUDED.execution_mode,
	resource_version = EXCLUDED.resource_version,
	node_name = EXCLUDED.node_name,
	updated_at = now()`

// Sync converges the mirror for poolID to the observed set in a single
// transaction: every observed row is UPSERTed keyed on pod_id, then any
// agent_pod_state row for poolID whose pod_id is not in observed is
// deleted. The DELETE is scoped to poolID, so a Sync for one pool never
// removes another pool's rows.
func (s *Store) Sync(ctx context.Context, poolID string, observed []agentpodstate.PodState) error {
	if poolID == "" {
		return agentpodstate.ErrEmptyPoolID
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("agentpodstate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// UPSERT every observed row. A pgx.Batch issues the statements in
	// one round trip; the surrounding transaction makes the convergence
	// atomic.
	keep := make([]string, 0, len(observed))
	if len(observed) > 0 {
		batch := &pgx.Batch{}
		for _, p := range observed {
			batch.Queue(upsertSQL,
				p.PodID, poolID, p.State,
				nullable(p.TenantID), nullable(p.SessionID),
				p.IsolationProfile, p.ExecutionMode, p.ResourceVersion,
				nullable(p.NodeName))
			keep = append(keep, p.PodID)
		}
		br := tx.SendBatch(ctx, batch)
		for range observed {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("agentpodstate: upsert: %w", err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("agentpodstate: upsert: %w", err)
		}
	}

	// Delete the pool's rows that are no longer observed. With no
	// observed rows, every row for the pool is stale and removed.
	if _, err := tx.Exec(ctx,
		`DELETE FROM agent_pod_state WHERE pool_id = $1 AND pod_id <> ALL($2)`,
		poolID, keep); err != nil {
		return fmt.Errorf("agentpodstate: prune: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("agentpodstate: commit: %w", err)
	}
	return nil
}

// MirrorLagSeconds returns now() - max(updated_at) for poolID's rows,
// the staleness of the mirror for that pool. A pool with no rows has no
// lag, so the result is 0.
func (s *Store) MirrorLagSeconds(ctx context.Context, poolID string) (float64, error) {
	if poolID == "" {
		return 0, agentpodstate.ErrEmptyPoolID
	}
	var lag float64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(EXTRACT(EPOCH FROM now() - max(updated_at)), 0)
		 FROM agent_pod_state WHERE pool_id = $1`, poolID).Scan(&lag)
	if err != nil {
		return 0, fmt.Errorf("agentpodstate: mirror lag: %w", err)
	}
	return lag, nil
}

// nullable maps an empty string to a SQL NULL so the nullable
// tenant_id, session_id, and node_name columns store NULL rather than
// an empty string. A NULL keeps the partial indexes
// (idx_agent_pod_state_session, idx_agent_pod_state_tenant) accurate.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
