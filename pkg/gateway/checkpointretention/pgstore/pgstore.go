// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §4.4 / §12.5
// session_checkpoints catalog. It persists rotation rows to the
// session_checkpoints table from migration 0067 and applies the
// §12.3 tenant-context RLS guard via pgtenant.InTx.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed §4.4 / §12.5 session_checkpoints
// catalog.
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

var _ checkpointretention.Store = (*Store)(nil)

const selectList = `tenant_id, session_id, slot_id, ref, created_at, retained, deleted_at, schema_version`

// Insert records a new checkpoint row. A duplicate (tenant, session,
// ref) returns checkpointretention.ErrDuplicate.
func (s *Store) Insert(ctx context.Context, r checkpointretention.Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("checkpointretention: tenant and session ids are required")
	}
	if r.Ref == "" {
		return errors.New("checkpointretention: ref is required")
	}
	// The gateway owns schema_version per §15.5 item 7; normalize a
	// zero-value caller field to the v1 baseline.
	schemaVer := r.SchemaVersion
	if schemaVer == 0 {
		schemaVer = checkpointretention.SchemaVersion
	}
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_checkpoints (
				tenant_id, session_id, slot_id, ref,
				created_at, retained, deleted_at, schema_version)
			VALUES ($1, $2, $3, $4, $5, TRUE, NULL, $6)`,
			r.TenantID, r.SessionID, r.SlotID, r.Ref, s.now(), schemaVer)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return checkpointretention.ErrDuplicate
			}
			return err
		}
		return nil
	})
}

// Rotate enforces the §12.5 latest-2 retention policy. The
// implementation lists every active row in CreatedAt-descending
// order, then marks every row after the first RetainedCount with
// retained=false and deleted_at=now() in the same transaction. The
// rows whose retention transitioned to false are returned in
// CreatedAt-ascending order (oldest first) so the caller can
// correlate with MinIO deletions.
func (s *Store) Rotate(ctx context.Context, tenantID, sessionID, slotID string) ([]checkpointretention.Record, error) {
	var transitioned []checkpointretention.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_checkpoints
				WHERE tenant_id = $1 AND session_id = $2 AND slot_id = $3
					AND deleted_at IS NULL
				ORDER BY created_at DESC`,
			tenantID, sessionID, slotID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var active []checkpointretention.Record
		for rows.Next() {
			r, err := scanRow(rows)
			if err != nil {
				return err
			}
			active = append(active, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		now := s.now()
		for i, row := range active {
			if i < checkpointretention.RetainedCount {
				continue
			}
			ct, err := tx.Exec(ctx,
				`UPDATE session_checkpoints
					SET retained = FALSE, deleted_at = $4
					WHERE tenant_id = $1 AND session_id = $2 AND ref = $3
						AND deleted_at IS NULL`,
				row.TenantID, row.SessionID, row.Ref, now)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				continue
			}
			row.Retained = false
			row.DeletedAt = now
			transitioned = append(transitioned, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Reverse to ascending CreatedAt (oldest first) — `active` was
	// descending, so the appended rows are descending too.
	for i, j := 0, len(transitioned)-1; i < j; i, j = i+1, j-1 {
		transitioned[i], transitioned[j] = transitioned[j], transitioned[i]
	}
	return transitioned, nil
}

// List returns every row for (tenantID, sessionID, slotID) in
// CreatedAt-descending order.
func (s *Store) List(ctx context.Context, tenantID, sessionID, slotID string) ([]checkpointretention.Record, error) {
	var out []checkpointretention.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_checkpoints
				WHERE tenant_id = $1 AND session_id = $2 AND slot_id = $3
				ORDER BY created_at DESC`,
			tenantID, sessionID, slotID)
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

// ListSoftDeletedBefore returns rows whose deleted_at is older than
// cutoff across every tenant.
func (s *Store) ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]checkpointretention.Record, error) {
	var out []checkpointretention.Record
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM session_checkpoints
				WHERE deleted_at IS NOT NULL AND deleted_at < $1
				ORDER BY tenant_id, session_id, created_at`,
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

// HardDelete removes the row entirely. The ref is unique per
// checkpoint, so it alone identifies the row; slotID is accepted for
// interface symmetry with the per-slot rotation surface.
func (s *Store) HardDelete(ctx context.Context, tenantID, sessionID, _ /* slotID */, ref string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_checkpoints
				WHERE tenant_id = $1 AND session_id = $2 AND ref = $3`,
			tenantID, sessionID, ref)
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
			`DELETE FROM session_checkpoints
				WHERE tenant_id = $1 AND session_id = ANY($2)`,
			tenantID, sessionIDs)
		return err
	})
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_checkpoints WHERE tenant_id = $1`,
			tenantID)
		return err
	})
}

func scanRow(row pgx.Row) (checkpointretention.Record, error) {
	var (
		r         checkpointretention.Record
		deletedAt *time.Time
	)
	if err := row.Scan(
		&r.TenantID, &r.SessionID, &r.SlotID, &r.Ref,
		&r.CreatedAt, &r.Retained, &deletedAt, &r.SchemaVersion,
	); err != nil {
		return checkpointretention.Record{}, err
	}
	if deletedAt != nil {
		r.DeletedAt = *deletedAt
	}
	return r, nil
}
