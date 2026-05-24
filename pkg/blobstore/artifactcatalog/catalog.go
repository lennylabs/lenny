// SPDX-License-Identifier: MIT

// Package artifactcatalog is the §12.5 catalog of ArtifactStore
// objects. It mirrors the MinIO blob objects with their tenant,
// session, lifecycle state (live | soft_deleted | tombstoned),
// SSE-KMS key alias, legal-hold flag, and tombstone retention
// deadline. The §12.5 GC sweep walks the catalog to hard-prune
// tombstoned objects whose deadline has passed; the §12.8 erasure
// orchestrator queries by (tenant, session) when removing per-user
// state.
//
// The catalog backs the MinIO blob store: a write produces one
// catalog row alongside the bucket object; a §12.5 soft-delete
// transitions the row from 'live' to 'soft_deleted' and starts
// the tombstone retention timer; the GC sweep transitions
// 'soft_deleted' to 'tombstoned' once the retention window expires
// and deletes the row + bucket object together.
package artifactcatalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// State is the §12.5 artifact-store lifecycle state.
type State string

const (
	StateLive        State = "live"
	StateSoftDeleted State = "soft_deleted"
	StateTombstoned  State = "tombstoned"
)

// ArtifactType is the §4.4 / §12.5 artifact-kind tag. The §4.4 line 291
// eviction-context accounting path writes rows with
// ArtifactTypeEvictionContext so the §12.5 GC sweep can drive the
// matching MinIO delete on the session-level cleanup boundary.
type ArtifactType string

const (
	// ArtifactTypeWorkspace: the canonical workspace tar / sealed
	// bundle. Default for every catalog row prior to migration 0063.
	ArtifactTypeWorkspace ArtifactType = "workspace"
	// ArtifactTypeEvictionContext: the §4.4 eviction-fallback
	// last-message context object (> 2 KB) at the canonical key
	// `/{tenant_id}/eviction/{session_id}/context`.
	// spec: §4.4 line 291.
	ArtifactTypeEvictionContext ArtifactType = "eviction_context"
	// ArtifactTypeCheckpoint: a workspace-checkpoint blob (full or
	// partial-manifest chunks). Reserved for the §10.1 wiring.
	ArtifactTypeCheckpoint ArtifactType = "checkpoint"
	// ArtifactTypeExport: an exported file subset for delegation
	// (§8.7).
	ArtifactTypeExport ArtifactType = "export"
	// ArtifactTypeSessionLog: the §4.4 line 226 runtime-stderr session
	// log object at the canonical key
	// `/{tenant_id}/sessions/{session_id}/stderr.log`. Written by the
	// session-log shipper on session terminal state; included in the
	// §12.5 GC sweep on the same lifecycle as every other artifact
	// kind.
	// spec: §4.4 line 226.
	ArtifactTypeSessionLog ArtifactType = "session_log"
)

// Record is one row in the §12.5 artifact_store catalog.
type Record struct {
	URI               string
	TenantID          string
	SessionID         string
	PartID            string
	MimeType          string
	SizeBytes         int64
	State             State
	// ArtifactType is the §4.4 / §12.5 artifact-kind tag. Empty maps
	// to ArtifactTypeWorkspace (the default the migration 0063 column
	// stamps onto pre-existing rows).
	ArtifactType ArtifactType
	KMSKeyAlias       string
	LegalHold         bool
	SoftDeletedAt     time.Time
	TombstoneDeadline time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Store is the §12.5 catalog API.
type Store interface {
	Insert(ctx context.Context, r Record) error
	Get(ctx context.Context, uri string) (Record, error)
	SoftDelete(ctx context.Context, uri string, deadline time.Time) error
	Tombstone(ctx context.Context, uri string) error
	HardPruneExpired(ctx context.Context, now time.Time) (int, error)
	ListBySession(ctx context.Context, tenantID, sessionID string) ([]Record, error)
	SetLegalHold(ctx context.Context, uri string, hold bool) error
	// IsLegalHeldAt reports whether any row scoped to (tenant, session)
	// carries legal_hold=true. The MinIO blob store calls it at
	// DeleteBySession time so a blob whose §12.5 catalog row records a
	// §12.8 hold survives the per-session sweep.
	//
	// spec: §12.8 line 735.
	IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error)
	// SessionsWithLegalHoldAndCheckpoints returns the §12.8 line 739
	// legal-hold reconciler's candidate set: every (tenant, session)
	// pair where legal_hold=true and the session has at least one
	// recorded checkpoint row in the catalog. The reconciler then
	// asks for the per-session row set via ListBySession to detect
	// rotation gaps.
	//
	// spec: §12.8 line 739.
	SessionsWithLegalHoldAndCheckpoints(ctx context.Context) ([]SessionRef, error)
}

// SessionRef is the (tenant, session) projection
// SessionsWithLegalHoldAndCheckpoints returns.
type SessionRef struct {
	TenantID  string
	SessionID string
}

// ErrNotFound is returned by Get when no row matches uri.
var ErrNotFound = errors.New("artifactcatalog: not found")

// PgStore is the Postgres-backed Store implementation against
// migration 0049's artifact_store table.
type PgStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// New returns a PgStore over pool. clock provides the timestamp
// used for SoftDelete / Tombstone / UpdatedAt; pass nil for
// time.Now.UTC.
func New(pool *pgxpool.Pool, clock func() time.Time) *PgStore {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &PgStore{pool: pool, clock: clock}
}

var _ Store = (*PgStore)(nil)

// Insert records a new artifact row. A duplicate URI fails with
// the underlying constraint violation.
func (s *PgStore) Insert(ctx context.Context, r Record) error {
	if r.URI == "" || r.TenantID == "" {
		return errors.New("artifactcatalog: URI and TenantID are required")
	}
	now := s.clock().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.State == "" {
		r.State = StateLive
	}
	if r.ArtifactType == "" {
		r.ArtifactType = ArtifactTypeWorkspace
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO artifact_store (
			uri, tenant_id, session_id, part_id,
			mime_type, size_bytes, state, kms_key_alias,
			legal_hold, artifact_type, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		r.URI, r.TenantID, r.SessionID, r.PartID,
		r.MimeType, r.SizeBytes, string(r.State), nullableString(r.KMSKeyAlias),
		r.LegalHold, string(r.ArtifactType), r.CreatedAt, now)
	if err != nil {
		return fmt.Errorf("artifactcatalog: insert %s: %w", r.URI, err)
	}
	return nil
}

// Get returns the catalog row for uri.
func (s *PgStore) Get(ctx context.Context, uri string) (Record, error) {
	var (
		out Record
		kms *string
		sd  *time.Time
		td  *time.Time
	)
	row := s.pool.QueryRow(ctx, `
		SELECT uri, tenant_id, session_id, part_id,
		       mime_type, size_bytes, state, kms_key_alias,
		       legal_hold, artifact_type, soft_deleted_at, tombstone_deadline,
		       created_at, updated_at
		FROM artifact_store WHERE uri = $1`, uri)
	var state, artifactType string
	err := row.Scan(&out.URI, &out.TenantID, &out.SessionID, &out.PartID,
		&out.MimeType, &out.SizeBytes, &state, &kms,
		&out.LegalHold, &artifactType, &sd, &td,
		&out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	out.State = State(state)
	out.ArtifactType = ArtifactType(artifactType)
	if kms != nil {
		out.KMSKeyAlias = *kms
	}
	if sd != nil {
		out.SoftDeletedAt = *sd
	}
	if td != nil {
		out.TombstoneDeadline = *td
	}
	return out, nil
}

// SoftDelete transitions a live row to soft_deleted and records
// the tombstone retention deadline.
func (s *PgStore) SoftDelete(ctx context.Context, uri string, deadline time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE artifact_store
		   SET state = $2, soft_deleted_at = $3,
		       tombstone_deadline = $4, updated_at = $3
		 WHERE uri = $1 AND state = 'live'`,
		uri, string(StateSoftDeleted), s.clock().UTC(), deadline)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the row is missing or it has already moved past live.
		return ErrNotFound
	}
	return nil
}

// Tombstone transitions a soft_deleted row to tombstoned. The §12.5
// GC sweep calls it after the soft-delete retention window expires
// but before HardPruneExpired removes the row + bucket object.
func (s *PgStore) Tombstone(ctx context.Context, uri string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE artifact_store
		   SET state = $2, updated_at = $3
		 WHERE uri = $1 AND state = 'soft_deleted'`,
		uri, string(StateTombstoned), s.clock().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// HardPruneExpired removes every tombstoned row whose
// tombstone_deadline is before now (and which is not under a
// legal hold). Returns the count removed so the caller can also
// delete the matching bucket objects.
func (s *PgStore) HardPruneExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM artifact_store
		 WHERE state = 'tombstoned'
		   AND tombstone_deadline IS NOT NULL
		   AND tombstone_deadline <= $1
		   AND legal_hold = false`,
		now.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ListBySession returns every catalog row for one session.
func (s *PgStore) ListBySession(ctx context.Context, tenantID, sessionID string) ([]Record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT uri, tenant_id, session_id, part_id,
		       mime_type, size_bytes, state, kms_key_alias,
		       legal_hold, artifact_type, soft_deleted_at, tombstone_deadline,
		       created_at, updated_at
		FROM artifact_store WHERE tenant_id = $1 AND session_id = $2
		ORDER BY uri`, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			r            Record
			state        string
			artifactType string
			kms          *string
			sd           *time.Time
			td           *time.Time
		)
		if err := rows.Scan(&r.URI, &r.TenantID, &r.SessionID, &r.PartID,
			&r.MimeType, &r.SizeBytes, &state, &kms,
			&r.LegalHold, &artifactType, &sd, &td,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.State = State(state)
		r.ArtifactType = ArtifactType(artifactType)
		if kms != nil {
			r.KMSKeyAlias = *kms
		}
		if sd != nil {
			r.SoftDeletedAt = *sd
		}
		if td != nil {
			r.TombstoneDeadline = *td
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetLegalHold flips the §12.5 legal_hold flag for uri.
func (s *PgStore) SetLegalHold(ctx context.Context, uri string, hold bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE artifact_store SET legal_hold = $2, updated_at = $3 WHERE uri = $1`,
		uri, hold, s.clock().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IsLegalHeldAt implements Store. Returns true when any catalog row
// scoped to (tenantID, sessionID) carries legal_hold=true.
//
// spec: §12.8 line 735.
func (s *PgStore) IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error) {
	var held bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM artifact_store
		     WHERE tenant_id = $1 AND session_id = $2 AND legal_hold = true
		)`, tenantID, sessionID).Scan(&held)
	if err != nil {
		return false, err
	}
	return held, nil
}

// SessionsWithLegalHoldAndCheckpoints implements Store. Returns every
// (tenant, session) pair with legal_hold=true and at least one
// checkpoint row in the catalog. The §12.8 line 739 reconciler walks
// the result set looking for gaps in each session's checkpoint
// sequence.
//
// spec: §12.8 line 739.
func (s *PgStore) SessionsWithLegalHoldAndCheckpoints(ctx context.Context) ([]SessionRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT tenant_id, session_id
		  FROM artifact_store
		 WHERE legal_hold = true
		   AND artifact_type = $1`, string(ArtifactTypeCheckpoint))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRef
	for rows.Next() {
		var r SessionRef
		if err := rows.Scan(&r.TenantID, &r.SessionID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
