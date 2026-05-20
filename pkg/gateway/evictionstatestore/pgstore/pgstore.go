// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §12.2.1 EvictionStateStore.
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

// Store is the Postgres-backed §12.2.1 EvictionStateStore. Construct
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

const selectList = `tenant_id, session_id, last_message_context,
	is_minio_key, created_at, updated_at`

// Put upserts the eviction-state row. The created_at column is
// preserved on update; updated_at is advanced under the §12.5
// monotonic-now rule.
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
					is_minio_key, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $5)`,
				r.TenantID, r.SessionID, r.LastMessageContext,
				r.IsMinIOKey, now)
			return err
		case err != nil:
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE session_eviction_state SET
				last_message_context = $3, is_minio_key = $4, updated_at = $5
			WHERE tenant_id = $1 AND session_id = $2`,
			r.TenantID, r.SessionID, r.LastMessageContext, r.IsMinIOKey,
			pgtenant.MonotonicNext(existingUpdated, now))
		return err
	})
}

// Get returns the eviction-state row or ErrNotFound. A cross-tenant
// miss is indistinguishable from a missing row per §12.3 isolation.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (evictionstatestore.Record, error) {
	var out evictionstatestore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_eviction_state
				WHERE tenant_id = $1 AND session_id = $2`,
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

// Delete removes one row. A missing row is not an error so the
// terminal-state cleanup path is idempotent against partial failures.
func (s *Store) Delete(ctx context.Context, tenantID, sessionID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_eviction_state
				WHERE tenant_id = $1 AND session_id = $2`,
			tenantID, sessionID)
		return err
	})
}

// DeleteByUser removes every row in tenantID whose session id is in
// the supplied slice. The orchestrator owns the session-id lookup
// because session_eviction_state does not carry a user_id column.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_eviction_state
				WHERE tenant_id = $1 AND session_id = ANY($2)`,
			tenantID, sessionIDs)
		return err
	})
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_eviction_state WHERE tenant_id = $1`,
			tenantID)
		return err
	})
}

// ListMinIOKeys returns every row in tenantID whose is_minio_key flag
// is true. The §12.5 GC sweep walks the result to drive MinIO
// deletes when a row is removed.
func (s *Store) ListMinIOKeys(ctx context.Context, tenantID string) ([]evictionstatestore.Record, error) {
	var out []evictionstatestore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_eviction_state
				WHERE tenant_id = $1 AND is_minio_key = true
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
	var r evictionstatestore.Record
	if err := row.Scan(
		&r.TenantID, &r.SessionID, &r.LastMessageContext,
		&r.IsMinIOKey, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return evictionstatestore.Record{}, err
	}
	return r, nil
}
