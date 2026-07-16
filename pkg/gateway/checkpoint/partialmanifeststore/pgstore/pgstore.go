// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §10.1 checkpoint_manifest
// store. It persists rows to the checkpoint_manifest table from
// migration 0175, with the §10.1 line 143-151 at-most-one-active-partial
// invariant enforced by the partial_manifest_active_uniq partial unique
// index, and applies the §12.3 tenant-context RLS guard via
// pgtenant.InTx.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed §10.1 checkpoint_manifest store.
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

const selectList = `tenant_id, checkpoint_id, session_id, slot_id,
	coordination_generation, recovery_generation, partial, manifest_reason,
	chunk_object_key_prefix, chunk_size_bytes, chunk_encoding, chunk_count,
	workspace_bytes_uploaded, reserved_bytes, reservation_released_at,
	baseline_full_checkpoint_bytes, checkpoint_started_at,
	checkpoint_timeout_at, created_at, deleted_at`

// selectListM is selectList qualified with the m. alias so the
// ListReclaimable join can disambiguate the manifest table's columns
// from the sessions table's.
const selectListM = `m.tenant_id, m.checkpoint_id, m.session_id, m.slot_id,
	m.coordination_generation, m.recovery_generation, m.partial, m.manifest_reason,
	m.chunk_object_key_prefix, m.chunk_size_bytes, m.chunk_encoding, m.chunk_count,
	m.workspace_bytes_uploaded, m.reserved_bytes, m.reservation_released_at,
	m.baseline_full_checkpoint_bytes, m.checkpoint_started_at,
	m.checkpoint_timeout_at, m.created_at, m.deleted_at`

// terminalStates is the §7.2 set of terminal session states the §12.5
// backstop treats as "no resume can consume this manifest". The set
// mirrors the api/v1/session terminal values.
const terminalStates = `('completed', 'failed', 'cancelled', 'expired')`

// Put writes the §10.1 intent row for a fresh checkpoint attempt in a
// single transaction: it fences on coordination_generation and
// supersedes prior attempts for the same (session, slot). A write whose
// generation is below an already-active strictly-higher generation is a
// fenced stale-coordinator attempt and is rejected with
// ErrStaleGeneration (§10.1 line 155); otherwise every active partial
// row at or below the incoming generation is soft-deleted
// (manifest_reason = 'superseded') before the INSERT so the
// partial_manifest_active_uniq index never sees two active partial rows.
// A duplicate checkpoint_id is rejected with ErrAlreadyExists.
//
// spec: §10.1 lines 137, 143-151, 155.
func (s *Store) Put(ctx context.Context, r partialmanifeststore.Record) error {
	if err := validatePut(&r); err != nil {
		return err
	}
	return pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		now := s.now()

		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM checkpoint_manifest
				WHERE tenant_id = $1 AND checkpoint_id = $2)`,
			r.TenantID, r.CheckpointID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return partialmanifeststore.ErrAlreadyExists
		}

		// §10.1 line 155 fencing: reject a stale write before any mutation.
		var higherActive bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2 AND slot_id = $3
					AND partial = TRUE AND deleted_at IS NULL
					AND coordination_generation > $4)`,
			r.TenantID, r.SessionID, r.SlotID, r.CoordinationGeneration).Scan(&higherActive); err != nil {
			return err
		}
		if higherActive {
			return partialmanifeststore.ErrStaleGeneration
		}

		// §10.1 line 137 supersede-on-write: soft-delete every active
		// partial row at or below the incoming generation in the same
		// transaction as the INSERT so the active partial set collapses to
		// the new row.
		if _, err := tx.Exec(ctx,
			`UPDATE checkpoint_manifest
				SET deleted_at = $5, manifest_reason = 'superseded'
				WHERE tenant_id = $1 AND session_id = $2 AND slot_id = $3
					AND partial = TRUE AND deleted_at IS NULL
					AND coordination_generation <= $4
					AND checkpoint_id != $6`,
			r.TenantID, r.SessionID, r.SlotID, r.CoordinationGeneration,
			now, r.CheckpointID); err != nil {
			return err
		}

		_, err := tx.Exec(ctx,
			`INSERT INTO checkpoint_manifest (
				tenant_id, checkpoint_id, session_id, slot_id,
				coordination_generation, recovery_generation, partial,
				manifest_reason, chunk_object_key_prefix, chunk_size_bytes,
				chunk_encoding, chunk_count, workspace_bytes_uploaded,
				reserved_bytes, reservation_released_at,
				baseline_full_checkpoint_bytes, checkpoint_started_at,
				checkpoint_timeout_at, created_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
				$14, NULL, $15, $16, $17, $18, NULL)`,
			r.TenantID, r.CheckpointID, r.SessionID, r.SlotID,
			r.CoordinationGeneration, r.RecoveryGeneration, r.Partial,
			r.ManifestReason, r.ChunkObjectKeyPrefix, r.ChunkSizeBytes,
			string(r.ChunkEncoding), r.ChunkCount, r.WorkspaceBytesUploaded,
			r.ReservedBytes, r.BaselineFullCheckpointBytes,
			r.CheckpointStartedAt, r.CheckpointTimeoutAt, now)
		return err
	})
}

// validatePut applies the intent-row defaults and validates the required
// fields.
func validatePut(r *partialmanifeststore.Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("partialmanifeststore: tenant and session ids are required")
	}
	if r.CheckpointID == "" {
		return errors.New("partialmanifeststore: checkpoint_id is required")
	}
	if r.ChunkObjectKeyPrefix == "" {
		return errors.New("partialmanifeststore: chunk_object_key_prefix is required")
	}
	if r.SlotID == "" {
		r.SlotID = partialmanifeststore.SlotDefault
	}
	if r.ChunkEncoding == "" {
		r.ChunkEncoding = partialmanifeststore.ChunkEncodingTar
	}
	if !r.ChunkEncoding.IsValid() {
		return errors.New("partialmanifeststore: invalid chunk_encoding")
	}
	if r.ManifestReason == "" {
		r.ManifestReason = partialmanifeststore.ReasonInProgress
	}
	if !partialmanifeststore.IsValidReason(r.ManifestReason) {
		return errors.New("partialmanifeststore: invalid manifest_reason")
	}
	if r.CheckpointStartedAt.IsZero() || r.CheckpointTimeoutAt.IsZero() {
		return errors.New("partialmanifeststore: checkpoint_started_at and checkpoint_timeout_at are required")
	}
	// The §10.1 step-1 intent row is always partial; only Finalise flips
	// it to false when a checkpoint completes with every byte confirmed.
	r.Partial = true
	return nil
}

// Get returns the row for (tenantID, checkpointID) or ErrNotFound. A
// soft-deleted row is still returned.
func (s *Store) Get(ctx context.Context, tenantID, checkpointID string) (partialmanifeststore.Record, error) {
	var out partialmanifeststore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM checkpoint_manifest
				WHERE tenant_id = $1 AND checkpoint_id = $2`,
			tenantID, checkpointID)
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

// LatestActive returns the highest-coordination_generation active
// partial row for (tenantID, sessionID) — the §4.4 cleanup selector.
func (s *Store) LatestActive(ctx context.Context, tenantID, sessionID string) (partialmanifeststore.Record, error) {
	var out partialmanifeststore.Record
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = $2
					AND partial = TRUE AND deleted_at IS NULL
				ORDER BY coordination_generation DESC, created_at DESC LIMIT 1`,
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

// ConfirmChunk applies the §10.1 line 131 monotonic counter UPDATE under
// the `deleted_at IS NULL` and `chunk_count < n + 1` guards.
func (s *Store) ConfirmChunk(ctx context.Context, tenantID, checkpointID string, n int, workspaceBytesUploaded int64) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE checkpoint_manifest
				SET chunk_count = $3, workspace_bytes_uploaded = $4
				WHERE tenant_id = $1 AND checkpoint_id = $2
					AND deleted_at IS NULL AND chunk_count < $3`,
			tenantID, checkpointID, n+1, workspaceBytesUploaded)
		return err
	})
}

// Finalise stamps the terminal disposition under the `deleted_at IS NULL`
// guard.
func (s *Store) Finalise(ctx context.Context, tenantID, checkpointID string, partial bool, manifestReason string) error {
	if !partialmanifeststore.IsValidReason(manifestReason) {
		return errors.New("partialmanifeststore: invalid manifest_reason")
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE checkpoint_manifest
				SET partial = $3, manifest_reason = $4
				WHERE tenant_id = $1 AND checkpoint_id = $2 AND deleted_at IS NULL`,
			tenantID, checkpointID, partial, manifestReason)
		return err
	})
}

// SoftDelete stamps `deleted_at = now()` on the row under the
// `deleted_at IS NULL` predicate. Returns nil when the row is missing or
// already soft-deleted — the cleanup path is idempotent.
func (s *Store) SoftDelete(ctx context.Context, tenantID, checkpointID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE checkpoint_manifest
				SET deleted_at = $3
				WHERE tenant_id = $1 AND checkpoint_id = $2 AND deleted_at IS NULL`,
			tenantID, checkpointID, s.now())
		return err
	})
}

// ListSoftDeletedBefore returns every row whose deleted_at is older than
// cutoff across every tenant. Used by the §12.5 hard-prune sweep, which
// runs platform-wide.
func (s *Store) ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]partialmanifeststore.Record, error) {
	var out []partialmanifeststore.Record
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM checkpoint_manifest
				WHERE deleted_at IS NOT NULL AND deleted_at < $1
				ORDER BY tenant_id, session_id, checkpoint_id`,
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

// ListReclaimable returns every abandoned active row the §12.5 backstop
// must sweep across every tenant. The terminal-state branch joins the
// sessions table so a row whose session has reached a terminal state is
// reclaimable immediately, while a row on a live session waits out
// maxResumeWindow.
//
// spec: §12.5 GC rule 6.
func (s *Store) ListReclaimable(ctx context.Context, maxResumeWindow time.Duration) ([]partialmanifeststore.Record, error) {
	windowStart := s.now().Add(-maxResumeWindow)
	var out []partialmanifeststore.Record
	if err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectListM+`
				FROM checkpoint_manifest m
				LEFT JOIN sessions sess
					ON sess.tenant_id = m.tenant_id AND sess.id::text = m.session_id
				WHERE m.partial = TRUE AND m.deleted_at IS NULL
					AND (m.manifest_reason != 'in_progress' OR now() > m.checkpoint_timeout_at)
					AND (sess.state IN `+terminalStates+` OR m.created_at < $1)
				ORDER BY m.tenant_id, m.session_id, m.checkpoint_id`,
			windowStart)
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
func (s *Store) HardDelete(ctx context.Context, tenantID, checkpointID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM checkpoint_manifest
				WHERE tenant_id = $1 AND checkpoint_id = $2`,
			tenantID, checkpointID)
		return err
	})
}

// ReleaseReservation stamps reservation_released_at under the
// `reservation_released_at IS NULL` guard and reports the rows affected.
func (s *Store) ReleaseReservation(ctx context.Context, tenantID, checkpointID string) (int64, error) {
	var affected int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE checkpoint_manifest
				SET reservation_released_at = $3
				WHERE tenant_id = $1 AND checkpoint_id = $2
					AND reservation_released_at IS NULL`,
			tenantID, checkpointID, s.now())
		if err != nil {
			return err
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// SumOutstandingReservations sums the tenant's unreleased reservation
// headroom over active rows.
func (s *Store) SumOutstandingReservations(ctx context.Context, tenantID string) (int64, error) {
	var sum int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(GREATEST(reserved_bytes - workspace_bytes_uploaded, 0)), 0)
				FROM checkpoint_manifest
				WHERE tenant_id = $1 AND deleted_at IS NULL
					AND reservation_released_at IS NULL`,
			tenantID).Scan(&sum)
	})
	if err != nil {
		return 0, err
	}
	return sum, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, _ string, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM checkpoint_manifest
				WHERE tenant_id = $1 AND session_id = ANY($2)`,
			tenantID, sessionIDs)
		return err
	})
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM checkpoint_manifest WHERE tenant_id = $1`,
			tenantID)
		return err
	})
}

func scanRow(row pgx.Row) (partialmanifeststore.Record, error) {
	var (
		r             partialmanifeststore.Record
		encoding      string
		reservationAt *time.Time
		baseline      *int64
		deletedAt     *time.Time
	)
	if err := row.Scan(
		&r.TenantID, &r.CheckpointID, &r.SessionID, &r.SlotID,
		&r.CoordinationGeneration, &r.RecoveryGeneration, &r.Partial,
		&r.ManifestReason, &r.ChunkObjectKeyPrefix, &r.ChunkSizeBytes,
		&encoding, &r.ChunkCount, &r.WorkspaceBytesUploaded,
		&r.ReservedBytes, &reservationAt, &baseline,
		&r.CheckpointStartedAt, &r.CheckpointTimeoutAt, &r.CreatedAt, &deletedAt,
	); err != nil {
		return partialmanifeststore.Record{}, err
	}
	r.ChunkEncoding = partialmanifeststore.ChunkEncoding(encoding)
	r.BaselineFullCheckpointBytes = baseline
	if reservationAt != nil {
		r.ReservationReleasedAt = *reservationAt
	}
	if deletedAt != nil {
		r.DeletedAt = *deletedAt
	}
	return r, nil
}
