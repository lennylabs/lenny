// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed sessionusage.Store: the §8.8
// per-session token accumulator. It persists each session's running
// input/output token totals to the session_usage table so the §8.8
// TaskResult.usage and TaskResult.treeUsage rollups are correct across
// gateway replicas, where the proxied request and the task's terminal
// materialization may run on different replicas.
//
// updated_at is stamped server-side with clock_timestamp() rather than a
// client-supplied value so a future staleness sweep compares against the
// database clock.
//
// spec: §8.8 lines 897-917; §4.9 line 1468.
package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
)

// Store is the Postgres-backed sessionusage.Store. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	// read is the §12.3 line 146 read-replica pool for the read-heavy
	// rollup lookups. It is set to pool unless WithReadPool wires a
	// separate reader; writes always use pool. spec: §12.3 line 146.
	read *pgxpool.Pool
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithReadPool routes the read paths (Get / GetMany) to a separate
// read-replica pool. A nil pool keeps reads on the primary.
// spec: §12.3 line 146.
func WithReadPool(read *pgxpool.Pool) Option {
	return func(s *Store) {
		if read != nil {
			s.read = read
		}
	}
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool, read: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

var _ sessionusage.Store = (*Store)(nil)

// Add implements sessionusage.Store. The INSERT … ON CONFLICT DO UPDATE
// runs in a single statement so concurrent proxy calls from several
// gateway replicas serialize on the row rather than racing a
// read-modify-write; no replica's contribution is lost.
func (s *Store) Add(ctx context.Context, tenantID, sessionID string, input, output int64) error {
	if tenantID == "" || sessionID == "" {
		return nil
	}
	if input < 0 {
		input = 0
	}
	if output < 0 {
		output = 0
	}
	if input == 0 && output == 0 {
		return nil
	}
	if err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_usage
			     (tenant_id, session_id, input_tokens, output_tokens, updated_at)
			 VALUES ($1, $2, $3, $4, clock_timestamp())
			 ON CONFLICT (tenant_id, session_id)
			 DO UPDATE SET input_tokens = session_usage.input_tokens + EXCLUDED.input_tokens,
			               output_tokens = session_usage.output_tokens + EXCLUDED.output_tokens,
			               updated_at = clock_timestamp()`,
			tenantID, sessionID, input, output)
		return err
	}); err != nil {
		return fmt.Errorf("sessionusage/pgstore: add tenant %q session %q: %w", tenantID, sessionID, err)
	}
	return nil
}

// Get implements sessionusage.Store. A session with no row returns the
// zero Tokens and a nil error.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (sessionusage.Tokens, error) {
	var t sessionusage.Tokens
	if tenantID == "" || sessionID == "" {
		return t, nil
	}
	if err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT input_tokens, output_tokens FROM session_usage
			 WHERE tenant_id = $1 AND session_id::text = $2`,
			tenantID, sessionID).Scan(&t.Input, &t.Output)
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}); err != nil {
		return sessionusage.Tokens{}, fmt.Errorf("sessionusage/pgstore: get tenant %q session %q: %w", tenantID, sessionID, err)
	}
	return t, nil
}

// GetMany implements sessionusage.Store. Sessions with no row are absent
// from the returned map.
func (s *Store) GetMany(ctx context.Context, tenantID string, sessionIDs []string) (map[string]sessionusage.Tokens, error) {
	out := make(map[string]sessionusage.Tokens, len(sessionIDs))
	if tenantID == "" || len(sessionIDs) == 0 {
		return out, nil
	}
	if err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT session_id::text, input_tokens, output_tokens FROM session_usage
			 WHERE tenant_id = $1 AND session_id::text = ANY($2)`,
			tenantID, sessionIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var t sessionusage.Tokens
			if err := rows.Scan(&id, &t.Input, &t.Output); err != nil {
				return err
			}
			out[id] = t
		}
		return rows.Err()
	}); err != nil {
		return nil, fmt.Errorf("sessionusage/pgstore: get many tenant %q: %w", tenantID, err)
	}
	return out, nil
}
