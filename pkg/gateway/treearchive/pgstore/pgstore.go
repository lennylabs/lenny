// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §8.10 session_tree_archive.
// It persists settled delegation-tree child results to the
// session_tree_archive table from migration 0100 and applies the §12.3
// tenant-context RLS guard via pgtenant.InTx.
//
// The store is the v1 production backend: a resumed parent replays a
// tree's archived results from Postgres so a coordinator handoff or a
// replica failover does not lose any settled child outcome. The
// in-memory treearchive.Memory backs the developer-mode deployment and,
// fronted by treearchive.Cached, serves as the §8.10 per-replica read
// cache over this store.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// Store is the Postgres-backed §8.10 session_tree_archive. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied. now selects the archived_at
// timestamp source; a nil now uses time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, now: now}
}

var _ treearchive.Store = (*Store)(nil)

// selectList is the column projection for the read paths.
const selectList = `tenant_id, root_session_id::text, node_session_id::text,
	COALESCE(parent_session_id::text, ''), state,
	COALESCE(result::text, ''), settled_at, archived_at, completion_seq`

// Archive upserts a settled node. Re-archiving the same
// (root, node) overwrites the prior record: a node settles once, so a
// re-archive on cascade or a settle after a partial write is
// idempotent. The §8.8 schemaVersion preservation the caller performs
// before Archive is what keeps a re-archive from rewriting a prior
// writer's envelope version.
func (s *Store) Archive(ctx context.Context, n treearchive.ArchivedNode) error {
	if n.TenantID == "" || n.RootSessionID == "" || n.NodeSessionID == "" {
		return errors.New("treearchive: tenant, root, and node ids are required")
	}
	archivedAt := n.ArchivedAt
	if archivedAt.IsZero() {
		archivedAt = s.now()
	}
	return pgtenant.InTx(ctx, s.pool, n.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_tree_archive (
				tenant_id, root_session_id, node_session_id,
				parent_session_id, state, result,
				settled_at, archived_at, completion_seq)
			VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6::jsonb, $7, $8, $9)
			ON CONFLICT (root_session_id, node_session_id) DO UPDATE SET
				tenant_id = EXCLUDED.tenant_id,
				parent_session_id = EXCLUDED.parent_session_id,
				state = EXCLUDED.state,
				result = EXCLUDED.result,
				settled_at = EXCLUDED.settled_at,
				archived_at = EXCLUDED.archived_at,
				completion_seq = EXCLUDED.completion_seq`,
			n.TenantID, n.RootSessionID, n.NodeSessionID,
			nullUUID(n.ParentSessionID), n.State, resultArg(n.Result),
			n.SettledAt, archivedAt, n.CompletionSeq)
		return err
	})
}

// Replay returns every archived node of the tree rooted at
// rootSessionID within tenantID, ordered by settled_at then
// completion_seq — the §8.10 original-settlement order a resumed
// parent observes. The completion_seq tiebreak gives a deterministic
// order when two nodes settled in the same instant.
func (s *Store) Replay(ctx context.Context, tenantID, rootSessionID string) ([]treearchive.ArchivedNode, error) {
	out := make([]treearchive.ArchivedNode, 0)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_tree_archive
				WHERE tenant_id = $1 AND root_session_id = $2::uuid
				ORDER BY settled_at, completion_seq`,
			tenantID, rootSessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			n, err := scanRow(rows)
			if err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns one archived node. ErrNotFound is returned when no
// matching node exists within tenantID. A cross-tenant miss is
// indistinguishable from a missing row per §12.3 isolation.
func (s *Store) Get(ctx context.Context, tenantID, rootSessionID, nodeSessionID string) (treearchive.ArchivedNode, error) {
	var out treearchive.ArchivedNode
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_tree_archive
				WHERE tenant_id = $1 AND root_session_id = $2::uuid
					AND node_session_id = $3::uuid`,
			tenantID, rootSessionID, nodeSessionID)
		n, err := scanRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return treearchive.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = n
		return nil
	})
	if err != nil {
		return treearchive.ArchivedNode{}, err
	}
	return out, nil
}

// GetByNode returns the archived node with nodeSessionID within
// tenantID regardless of which tree it belongs to. A node session id
// is globally unique, so this resolves a settled child without knowing
// its tree root. ErrNotFound is returned when no matching node exists.
func (s *Store) GetByNode(ctx context.Context, tenantID, nodeSessionID string) (treearchive.ArchivedNode, error) {
	var out treearchive.ArchivedNode
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_tree_archive
				WHERE tenant_id = $1 AND node_session_id = $2::uuid`,
			tenantID, nodeSessionID)
		n, err := scanRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return treearchive.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = n
		return nil
	})
	if err != nil {
		return treearchive.ArchivedNode{}, err
	}
	return out, nil
}

// nullUUID returns nil for an empty id so pgx writes SQL NULL into the
// nullable parent_session_id column; a non-empty id is cast to uuid in
// the statement.
func nullUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// resultArg returns nil for an empty result so pgx writes SQL NULL into
// the nullable result jsonb column; a non-empty result is sent as the
// raw JSON document the statement casts to jsonb.
func resultArg(result string) any {
	if result == "" {
		return nil
	}
	return result
}

func scanRow(row pgx.Row) (treearchive.ArchivedNode, error) {
	var n treearchive.ArchivedNode
	if err := row.Scan(
		&n.TenantID, &n.RootSessionID, &n.NodeSessionID,
		&n.ParentSessionID, &n.State, &n.Result,
		&n.SettledAt, &n.ArchivedAt, &n.CompletionSeq,
	); err != nil {
		return treearchive.ArchivedNode{}, err
	}
	return n, nil
}
