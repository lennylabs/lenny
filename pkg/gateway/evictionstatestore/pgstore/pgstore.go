// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §12.2 EvictionStateStore.
// It persists eviction-state rows to the session_eviction_state table
// from migration 0045 and applies the §12.3 tenant-context RLS guard
// via pgtenant.InTx.
//
// The store is the v1 production backend; the in-memory MemoryStore in
// the parent package backs the developer-mode deployment.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed §12.2 EvictionStateStore. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. The pool must point at a
// database with the migrations/ schema applied. now selects the
// timestamp source; a nil now uses time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, now: now}
}

var _ evictionstatestore.Store = (*Store)(nil)

// selectList is the column projection for read paths. The trailing
// columns are the §4.4 lines 265–273 fields added in migration 0060
// plus the §4.4 line 281 soft-delete tombstone added in migration
// 0064: recovery_generation, coordination_generation,
// conversation_cursor, evicted_at, workspace_lost, context_truncated,
// deleted_at.
const selectList = `tenant_id, session_id, last_message_context,
	is_minio_key, created_at, updated_at,
	recovery_generation, coordination_generation, conversation_cursor,
	evicted_at, workspace_lost, context_truncated, deleted_at`

// Put upserts the eviction-state row. The created_at column is
// preserved on update; updated_at is advanced under the §12.5
// monotonic-now rule. The §4.4 lines 268–273 mandated columns are
// written verbatim from the Record on both insert and update so the
// §7.2 resume path observes the latest generation, cursor, and
// truncation state. A Put on a previously soft-deleted row clears the
// tombstone so a session that re-enters the eviction-fallback path
// surfaces a live row to the §7.2 resume path again.
func (s *Store) Put(ctx context.Context, r evictionstatestore.Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("evictionstatestore: tenant and session ids are required")
	}
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		var existingUpdated time.Time
		err := tx.QueryRow(ctx,
			`SELECT updated_at FROM session_eviction_state
				WHERE tenant_id = $1 AND session_id = $2`,
			r.TenantID, r.SessionID).Scan(&existingUpdated)
		now := s.now()
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx,
				`INSERT INTO session_eviction_state (
					tenant_id, session_id, last_message_context,
					is_minio_key, created_at, updated_at,
					recovery_generation, coordination_generation,
					conversation_cursor, evicted_at,
					workspace_lost, context_truncated)
				VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9, $10, $11)`,
				r.TenantID, r.SessionID, r.LastMessageContext,
				r.IsMinIOKey, now,
				r.RecoveryGeneration, r.CoordinationGeneration,
				r.ConversationCursor, nullTime(r.EvictedAt),
				r.WorkspaceLost, r.ContextTruncated)
			return err
		case err != nil:
			return err
		}
		// Clear the §4.4 line 281 tombstone on re-Put so a resurrected
		// session does not appear soft-deleted to the resume path.
		_, err = tx.Exec(ctx,
			`UPDATE session_eviction_state SET
				last_message_context = $3, is_minio_key = $4, updated_at = $5,
				recovery_generation = $6, coordination_generation = $7,
				conversation_cursor = $8, evicted_at = $9,
				workspace_lost = $10, context_truncated = $11,
				deleted_at = NULL
			WHERE tenant_id = $1 AND session_id = $2`,
			r.TenantID, r.SessionID, r.LastMessageContext, r.IsMinIOKey,
			pgtenant.MonotonicNext(existingUpdated, now),
			r.RecoveryGeneration, r.CoordinationGeneration,
			r.ConversationCursor, nullTime(r.EvictedAt),
			r.WorkspaceLost, r.ContextTruncated)
		return err
	})
}

// nullTime returns a *time.Time so pgx writes a SQL NULL when t is
// zero; non-zero values flow through unchanged. The §4.4 EvictedAt
// column is nullable so callers that omit the timestamp keep the
// distinction visible to read paths.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// Get returns the eviction-state row or ErrNotFound. A cross-tenant
// miss is indistinguishable from a missing row per §12.3 isolation.
// Soft-deleted rows are filtered out — the §7.2 resume path observes
// a `deleted_at IS NULL` invariant per §4.4 line 281.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (evictionstatestore.Record, error) {
	var out evictionstatestore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_eviction_state
				WHERE tenant_id = $1 AND session_id = $2
					AND deleted_at IS NULL`,
			tenantID, sessionID)
		r, err := scanRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return evictionstatestore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return evictionstatestore.Record{}, err
	}
	return out, nil
}

// Delete stamps `deleted_at = now()` on the row under the
// `deleted_at IS NULL` predicate so a stale-leader retry, a
// crash-resumed terminal-cleanup, or the §12.5 GC backstop racing the
// primary cleanup all observe `rows_affected == 0` on the second
// writer and skip side effects.
// spec: §4.4 line 281.
func (s *Store) Delete(ctx context.Context, tenantID, sessionID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE session_eviction_state
				SET deleted_at = $3
				WHERE tenant_id = $1 AND session_id = $2
					AND deleted_at IS NULL`,
			tenantID, sessionID, s.now())
		return err
	})
}

// DeleteByUser soft-deletes every row in tenantID whose session id is
// in the supplied slice. The orchestrator owns the session-id lookup
// because session_eviction_state does not carry a user_id column.
// spec: §4.4 line 281.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE session_eviction_state
				SET deleted_at = $3
				WHERE tenant_id = $1 AND session_id = ANY($2)
					AND deleted_at IS NULL`,
			tenantID, sessionIDs, s.now())
		return err
	})
}

// DeleteByTenant soft-deletes every row scoped to tenantID.
// Idempotent. spec: §4.4 line 281.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE session_eviction_state
				SET deleted_at = $2
				WHERE tenant_id = $1 AND deleted_at IS NULL`,
			tenantID, s.now())
		return err
	})
}

// SweepDeletedBefore hard-deletes every soft-deleted row whose
// `deleted_at` is strictly older than cutoff and returns the number
// of rows removed. The §12.5 GC backstop runs this once per retention
// cycle after the tombstone window has elapsed so the row is
// physically removed in tandem with its mirrored `artifact_store`
// row. Cross-tenant: the sweep is a background worker that runs
// without a tenant context — it uses pgtenant.InAllTenants the same
// way the §12.5 retention GC and the partial-manifest sweep do.
// spec: §4.4 line 281 / §12.5 GC concurrency model rule 6.
func (s *Store) SweepDeletedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	var removed int64
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM session_eviction_state
				WHERE deleted_at IS NOT NULL AND deleted_at < $1`,
			cutoff)
		if err != nil {
			return err
		}
		removed = tag.RowsAffected()
		return nil
	}); err != nil {
		return 0, err
	}
	return int(removed), nil
}

// ListMinIOKeys returns every active row in tenantID whose
// is_minio_key flag is true. The §12.5 GC sweep walks the result to
// drive MinIO deletes when a row is removed; soft-deleted rows are
// excluded so the sweep does not double-delete MinIO objects whose
// owning row is already in the tombstone window awaiting hard prune.
func (s *Store) ListMinIOKeys(ctx context.Context, tenantID string) ([]evictionstatestore.Record, error) {
	var out []evictionstatestore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_eviction_state
				WHERE tenant_id = $1 AND is_minio_key = true
					AND deleted_at IS NULL
				ORDER BY session_id`,
			tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRow(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func scanRow(row pgx.Row) (evictionstatestore.Record, error) {
	var (
		r         evictionstatestore.Record
		evictedAt *time.Time
		deletedAt *time.Time
	)
	if err := row.Scan(
		&r.TenantID, &r.SessionID, &r.LastMessageContext,
		&r.IsMinIOKey, &r.CreatedAt, &r.UpdatedAt,
		&r.RecoveryGeneration, &r.CoordinationGeneration,
		&r.ConversationCursor, &evictedAt,
		&r.WorkspaceLost, &r.ContextTruncated, &deletedAt,
	); err != nil {
		return evictionstatestore.Record{}, err
	}
	if evictedAt != nil {
		r.EvictedAt = *evictedAt
	}
	if deletedAt != nil {
		r.DeletedAt = *deletedAt
	}
	return r, nil
}
