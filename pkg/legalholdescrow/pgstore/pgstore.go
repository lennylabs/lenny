// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §12.8 legal_hold_escrow_records
// store (migration 0166). The table is platform-scoped — the records must
// survive the tenant tombstone so a hold cleared after Phase 4 still
// resolves the escrow objects to delete — so the store reads and writes
// through the pool directly without the §12.3 per-tenant RLS guard.
//
// spec: §12.8 lines 884-885.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/legalholdescrow"
)

// Store is the Postgres-backed escrow record store.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ legalholdescrow.RecordStore = (*Store)(nil)

// Save inserts or overwrites the (tenant_id, escrow_object_key) record. A
// re-entered Phase 3.5 overwrites the same row rather than duplicating it,
// and clears any prior release so a re-escrowed object is active again.
func (s *Store) Save(ctx context.Context, rec legalholdescrow.Record) error {
	if rec.TenantID == "" || rec.EscrowObjectKey == "" {
		return errors.New("legalholdescrow: tenant_id and escrow_object_key required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO legal_hold_escrow_records (
			tenant_id, escrow_object_key, resource_type, resource_id,
			escrow_region, escrow_kek_id, tenant_delete_job_id,
			session_id, artifact_uri, original_hold_set_at, migrated_at,
			released_at, released_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULL, '')
		ON CONFLICT (tenant_id, escrow_object_key) DO UPDATE SET
			resource_type = EXCLUDED.resource_type,
			resource_id = EXCLUDED.resource_id,
			escrow_region = EXCLUDED.escrow_region,
			escrow_kek_id = EXCLUDED.escrow_kek_id,
			tenant_delete_job_id = EXCLUDED.tenant_delete_job_id,
			session_id = EXCLUDED.session_id,
			artifact_uri = EXCLUDED.artifact_uri,
			original_hold_set_at = EXCLUDED.original_hold_set_at,
			migrated_at = EXCLUDED.migrated_at,
			released_at = NULL,
			released_by = ''`,
		rec.TenantID, rec.EscrowObjectKey, rec.ResourceType, rec.ResourceID,
		rec.EscrowRegion, rec.EscrowKEKID, rec.TenantDeleteJob,
		rec.SessionID, rec.ArtifactURI, nullableTime(rec.OriginalHoldSet), nonZeroTime(rec.MigratedAt))
	return err
}

// ActiveForSession implements RecordStore.
func (s *Store) ActiveForSession(ctx context.Context, tenantID, sessionID string) ([]legalholdescrow.Record, error) {
	return s.query(ctx, tenantID, "session_id", sessionID)
}

// ActiveForArtifact implements RecordStore.
func (s *Store) ActiveForArtifact(ctx context.Context, tenantID, artifactURI string) ([]legalholdescrow.Record, error) {
	return s.query(ctx, tenantID, "artifact_uri", artifactURI)
}

// query returns the tenant's unreleased records whose column equals val.
// The column is one of the two fixed identifiers (session_id /
// artifact_uri), not attacker-controlled.
func (s *Store) query(ctx context.Context, tenantID, column, val string) ([]legalholdescrow.Record, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_id, escrow_object_key, resource_type, resource_id,
			escrow_region, escrow_kek_id, tenant_delete_job_id,
			session_id, artifact_uri, original_hold_set_at, migrated_at
		FROM legal_hold_escrow_records
		WHERE tenant_id = $1 AND `+column+` = $2 AND released_at IS NULL
		ORDER BY escrow_object_key`,
		tenantID, val)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legalholdescrow.Record
	for rows.Next() {
		var (
			r        legalholdescrow.Record
			holdSet  *time.Time
			migrated time.Time
		)
		if err := rows.Scan(&r.TenantID, &r.EscrowObjectKey, &r.ResourceType, &r.ResourceID,
			&r.EscrowRegion, &r.EscrowKEKID, &r.TenantDeleteJob,
			&r.SessionID, &r.ArtifactURI, &holdSet, &migrated); err != nil {
			return nil, err
		}
		if holdSet != nil {
			r.OriginalHoldSet = holdSet.UTC()
		}
		r.MigratedAt = migrated.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReleased flips the record to released. Idempotent: the
// released_at IS NULL predicate makes the transition strictly monotonic so
// a re-cleared hold does not overwrite the original release provenance.
func (s *Store) MarkReleased(ctx context.Context, tenantID, escrowObjectKey, by string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE legal_hold_escrow_records
		SET released_at = $3, released_by = $4
		WHERE tenant_id = $1 AND escrow_object_key = $2 AND released_at IS NULL`,
		tenantID, escrowObjectKey, at.UTC(), by)
	return err
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}
