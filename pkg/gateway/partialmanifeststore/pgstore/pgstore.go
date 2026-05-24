// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §4.4 partial-checkpoint
// manifest store. It persists rows to the
// session_partial_checkpoint_manifest table from migration 0062 and
// applies the §12.3 tenant-context RLS guard via pgtenant.InTx.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed §4.4 partial-manifest store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool. now selects the timestamp
// source; a nil now uses time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, now: now}
}

var _ partialmanifeststore.Store = (*Store)(nil)

const selectList = `tenant_id, session_id, generation,
	partial_object_key_prefix, chunk_encoding,
	created_at, deleted_at`

// Put inserts a fresh row. A second Put for the same composite key
// refreshes the partial_object_key_prefix / chunk_encoding columns
// and preserves the original created_at. Re-Put on a soft-deleted
// row is rejected because a partial manifest, once cleaned up, is
// terminal.
func (s *Store) Put(ctx context.Context, r partialmanifeststore.Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("partialmanifeststore: tenant and session ids are required")
	}
	if r.PartialObjectKeyPrefix == "" {
		return errors.New("partialmanifeststore: partial_object_key_prefix is required")
	}
	if r.ChunkEncoding == "" {
		r.ChunkEncoding = partialmanifeststore.ChunkEncodingTar
	}
	if !r.ChunkEncoding.IsValid() {
		return errors.New("partialmanifeststore: invalid chunk_encoding")
	}
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		var (
			existingDeletedAt *time.Time
			existingCreatedAt time.Time
		)
		err := tx.QueryRow(ctx,
			`SELECT created_at, deleted_at FROM session_partial_checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2 AND generation = $3`,
			r.TenantID, r.SessionID, r.Generation).Scan(&existingCreatedAt, &existingDeletedAt)
		now := s.now()
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx,
				`INSERT INTO session_partial_checkpoint_manifest (
					tenant_id, session_id, generation,
					partial_object_key_prefix, chunk_encoding,
					created_at, deleted_at)
				VALUES ($1, $2, $3, $4, $5, $6, NULL)`,
				r.TenantID, r.SessionID, r.Generation,
				r.PartialObjectKeyPrefix, string(r.ChunkEncoding), now)
			return err
		case err != nil:
			return err
		}
		if existingDeletedAt != nil {
			return errors.New("partialmanifeststore: row already soft-deleted")
		}
		_, err = tx.Exec(ctx,
			`UPDATE session_partial_checkpoint_manifest SET
				partial_object_key_prefix = $4, chunk_encoding = $5
			WHERE tenant_id = $1 AND session_id = $2 AND generation = $3
				AND deleted_at IS NULL`,
			r.TenantID, r.SessionID, r.Generation,
			r.PartialObjectKeyPrefix, string(r.ChunkEncoding))
		return err
	})
}

// Get returns the row for (tenantID, sessionID, generation) or
// ErrNotFound. A soft-deleted row is still returned.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string, generation int64) (partialmanifeststore.Record, error) {
	var out partialmanifeststore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_partial_checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2 AND generation = $3`,
			tenantID, sessionID, generation)
		r, err := scanRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return partialmanifeststore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return partialmanifeststore.Record{}, err
	}
	return out, nil
}

// LatestActive returns the highest-generation active row for
// (tenantID, sessionID).
func (s *Store) LatestActive(ctx context.Context, tenantID, sessionID string) (partialmanifeststore.Record, error) {
	var out partialmanifeststore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM session_partial_checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2 AND deleted_at IS NULL
				ORDER BY generation DESC LIMIT 1`,
			tenantID, sessionID)
		r, err := scanRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return partialmanifeststore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return partialmanifeststore.Record{}, err
	}
	return out, nil
}

// SoftDelete stamps `deleted_at = now()` on the row under the
// `deleted_at IS NULL` predicate. Returns nil with no error when the
// row is missing or already soft-deleted — the cleanup path is
// idempotent.
func (s *Store) SoftDelete(ctx context.Context, tenantID, sessionID string, generation int64) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE session_partial_checkpoint_manifest
				SET deleted_at = $4
				WHERE tenant_id = $1 AND session_id = $2 AND generation = $3
					AND deleted_at IS NULL`,
			tenantID, sessionID, generation, s.now())
		return err
	})
}

// ListSoftDeletedBefore returns every row whose deleted_at is older
// than cutoff across every tenant. Used by the §12.5 hard-prune
// sweep, which runs platform-wide.
func (s *Store) ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]partialmanifeststore.Record, error) {
	// Cross-tenant query: the sweep is a background worker that runs
	// without a tenant context. The session_partial_checkpoint_manifest
	// has RLS enabled, so this path must bypass it the same way the
	// retention GC and the §12.5 sweep do — through pgtenant.InAllTenants.
	var out []partialmanifeststore.Record
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_partial_checkpoint_manifest
				WHERE deleted_at IS NOT NULL AND deleted_at < $1
				ORDER BY tenant_id, session_id, generation`,
			cutoff)
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
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// HardDelete removes the row entirely.
func (s *Store) HardDelete(ctx context.Context, tenantID, sessionID string, generation int64) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_partial_checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2 AND generation = $3`,
			tenantID, sessionID, generation)
		return err
	})
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_partial_checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = ANY($2)`,
			tenantID, sessionIDs)
		return err
	})
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_partial_checkpoint_manifest WHERE tenant_id = $1`,
			tenantID)
		return err
	})
}

func scanRow(row pgx.Row) (partialmanifeststore.Record, error) {
	var (
		r         partialmanifeststore.Record
		encoding  string
		deletedAt *time.Time
	)
	if err := row.Scan(
		&r.TenantID, &r.SessionID, &r.Generation,
		&r.PartialObjectKeyPrefix, &encoding,
		&r.CreatedAt, &deletedAt,
	); err != nil {
		return partialmanifeststore.Record{}, err
	}
	r.ChunkEncoding = partialmanifeststore.ChunkEncoding(encoding)
	if deletedAt != nil {
		r.DeletedAt = *deletedAt
	}
	return r, nil
}
