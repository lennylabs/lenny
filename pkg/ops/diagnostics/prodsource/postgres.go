// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// PGReader is the §25.6 Postgres reader. It reads session and pod state
// from the sessions and agent_pod_state tables and credential-pool load
// from credential_leases. spec: §25.6 lines 2885-2906. F-25.6.1.
type PGReader struct {
	pool *pgxpool.Pool
}

// NewPGReader returns a PGReader over pool.
func NewPGReader(pool *pgxpool.Pool) *PGReader { return &PGReader{pool: pool} }

// Compile-time assertion that *PGReader satisfies the seam.
var _ Postgres = (*PGReader)(nil)

// Session reads the sessions row joined to its agent_pod_state row. A
// session id that is not a valid UUID, or that matches no row, returns
// Found=false (the §25.6 SESSION_NOT_FOUND path) rather than an error.
func (r *PGReader) Session(ctx context.Context, sessionID string) (SessionRow, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return SessionRow{Found: false}, nil
	}
	const q = `
		SELECT s.state, s.runtime_ref, s.pool_ref, s.failure_class, s.failure_reason,
		       COALESCE(p.pod_id, ''), COALESCE(p.state, ''), COALESCE(p.node_name, '')
		FROM sessions s
		LEFT JOIN agent_pod_state p ON p.session_id = s.id::text
		WHERE s.id = $1`
	var row SessionRow
	err := r.pool.QueryRow(ctx, q, sessionID).Scan(
		&row.State, &row.Runtime, &row.Pool, &row.FailureClass, &row.FailureReason,
		&row.PodID, &row.PodState, &row.NodeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionRow{Found: false}, nil
	}
	if err != nil {
		return SessionRow{}, err
	}
	row.SessionID = sessionID
	row.Found = true
	return row, nil
}

// PoolPodCounts reads the per-state pod-count breakdown for a warm pool
// from agent_pod_state. The §6.2 pod-state strings map onto the §25.6
// PodCountBreakdown buckets: warming → Warming, idle → Idle,
// claimed → Claimed, failed → Failed. Other states (draining,
// terminating) are not counted in the breakdown. found is false when the
// pool has no pod rows.
func (r *PGReader) PoolPodCounts(ctx context.Context, poolName string) (diagnostics.PodCountBreakdown, bool, error) {
	const q = `SELECT state, count(*) FROM agent_pod_state WHERE pool_id = $1 GROUP BY state`
	rows, err := r.pool.Query(ctx, q, poolName)
	if err != nil {
		return diagnostics.PodCountBreakdown{}, false, err
	}
	defer rows.Close()
	var counts diagnostics.PodCountBreakdown
	found := false
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return diagnostics.PodCountBreakdown{}, false, err
		}
		found = true
		switch state {
		case "warming":
			counts.Warming += n
		case "idle":
			counts.Idle += n
		case "claimed":
			counts.Claimed += n
		case "failed":
			counts.Failed += n
		}
	}
	if err := rows.Err(); err != nil {
		return diagnostics.PodCountBreakdown{}, false, err
	}
	return counts, found, nil
}

// CredentialPoolLoad reads the active-lease load for a credential pool
// from the platform-global credential_leases table. found is false when
// the pool has no lease rows.
func (r *PGReader) CredentialPoolLoad(ctx context.Context, poolName string) (CredentialPoolLoad, error) {
	const q = `SELECT credential_id, count(*) FROM credential_leases WHERE pool_id = $1 GROUP BY credential_id`
	rows, err := r.pool.Query(ctx, q, poolName)
	if err != nil {
		return CredentialPoolLoad{}, err
	}
	defer rows.Close()
	load := CredentialPoolLoad{LeasesByCredential: map[string]int{}}
	for rows.Next() {
		var credID string
		var n int
		if err := rows.Scan(&credID, &n); err != nil {
			return CredentialPoolLoad{}, err
		}
		load.Found = true
		load.ActiveLeases += n
		if credID != "" {
			load.LeasesByCredential[credID] = n
		}
	}
	if err := rows.Err(); err != nil {
		return CredentialPoolLoad{}, err
	}
	return load, nil
}
