// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed runtimeupgradestore.Store over
// the runtime_upgrade table. It is the durable home for the §10.5
// RuntimeUpgrade phase so an operator-driven runtime image rollout
// survives a gateway restart; runtimeupgradestore.Memory is the
// in-memory alternative.
//
// One upgrade targets one SandboxWarmPool, so the table is keyed by pool
// name. It is platform-operational state (one cluster runtime catalog)
// rather than tenant-isolated: no lenny_tenant_guard trigger and no RLS
// policy, and this store operates on the pool directly. The optimistic
// version column serializes concurrent phase transitions across gateway
// replicas. spec: §10.5 lines 466-540.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
)

// Store is the Postgres-backed RuntimeUpgrade registry. Construct with New.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ runtimeupgradestore.Store = (*Store)(nil)

// New returns a Store over pool. The pool must address a database that
// has the migrations/ schema applied (the runtime_upgrade table).
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

// NewWithClock returns a Store with an injected clock, for tests that
// need a deterministic UpdatedAt.
func NewWithClock(pool *pgxpool.Pool, clock func() time.Time) *Store {
	s := New(pool)
	if clock != nil {
		s.clock = clock
	}
	return s
}

// scanner is the QueryRow.Scan surface, isolated so scanRecord's
// column→Record mapping is exercised at tier-1 without a live Postgres.
type scanner interface {
	Scan(dest ...any) error
}

const selectColumns = `
	pool, phase, prior_phase, new_image, previous_pool_spec, schema_version,
	drain_first, canary_percent, stabilization_window_secs, drain_timeout_secs,
	auto_advance, pause_reason, paused_at, phase_entered_at, draining_sessions,
	version, created_at, updated_at`

// scanRecord maps a runtime_upgrade row onto a Record. The column order
// must match selectColumns; the nullable paused_at scans through a
// *time.Time and normalizes to UTC.
func scanRecord(row scanner) (runtimeupgradestore.Record, error) {
	var (
		rec      runtimeupgradestore.Record
		spec     []byte
		canary   int64
		draining int64
		pausedAt *time.Time
	)
	if err := row.Scan(
		&rec.Pool, &rec.Phase, &rec.PriorPhase, &rec.NewImage, &spec, &rec.SchemaVersion,
		&rec.DrainFirst, &canary, &rec.StabilizationWindowSeconds, &rec.DrainTimeoutSeconds,
		&rec.AutoAdvance, &rec.PauseReason, &pausedAt, &rec.PhaseEnteredAt, &draining,
		&rec.Version, &rec.CreatedAt, &rec.UpdatedAt,
	); err != nil {
		return runtimeupgradestore.Record{}, err
	}
	rec.CanaryPercent = int(canary)
	rec.DrainingSessions = int(draining)
	if spec != nil {
		rec.PreviousPoolSpec = spec
	}
	if pausedAt != nil {
		rec.PausedAt = pausedAt.UTC()
	}
	rec.PhaseEnteredAt = rec.PhaseEnteredAt.UTC()
	rec.CreatedAt = rec.CreatedAt.UTC()
	rec.UpdatedAt = rec.UpdatedAt.UTC()
	return rec, nil
}

// Get implements runtimeupgradestore.Store.
func (s *Store) Get(ctx context.Context, pool string) (runtimeupgradestore.Record, bool, error) {
	rec, err := scanRecord(s.pool.QueryRow(ctx,
		`SELECT `+selectColumns+` FROM runtime_upgrade WHERE pool = $1`, pool))
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimeupgradestore.Record{}, false, nil
	}
	if err != nil {
		return runtimeupgradestore.Record{}, false, fmt.Errorf("runtimeupgradestore: get: %w", err)
	}
	return rec, true, nil
}

// List implements runtimeupgradestore.Store.
func (s *Store) List(ctx context.Context) ([]runtimeupgradestore.Record, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+selectColumns+` FROM runtime_upgrade ORDER BY pool`)
	if err != nil {
		return nil, fmt.Errorf("runtimeupgradestore: list: %w", err)
	}
	defer rows.Close()
	var out []runtimeupgradestore.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("runtimeupgradestore: list scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runtimeupgradestore: list rows: %w", err)
	}
	return out, nil
}

// Put implements runtimeupgradestore.Store with an optimistic version
// guard. The first write (expectVersion 0) inserts the row; later writes
// UPDATE ... WHERE version = expectVersion and report ErrConflict when no
// row matched.
func (s *Store) Put(ctx context.Context, rec runtimeupgradestore.Record, expectVersion int64) (runtimeupgradestore.Record, error) {
	now := s.clock()
	next := expectVersion + 1
	var pausedAt *time.Time
	if !rec.PausedAt.IsZero() {
		t := rec.PausedAt.UTC()
		pausedAt = &t
	}
	phaseEntered := rec.PhaseEnteredAt
	if phaseEntered.IsZero() {
		phaseEntered = now
	}
	var spec []byte
	if len(rec.PreviousPoolSpec) > 0 {
		spec = rec.PreviousPoolSpec
	}
	if expectVersion == 0 {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO runtime_upgrade
			  (pool, phase, prior_phase, new_image, previous_pool_spec, schema_version,
			   drain_first, canary_percent, stabilization_window_secs, drain_timeout_secs,
			   auto_advance, pause_reason, paused_at, phase_entered_at, draining_sessions,
			   version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $17)`,
			rec.Pool, rec.Phase, rec.PriorPhase, rec.NewImage, spec, rec.SchemaVersion,
			rec.DrainFirst, rec.CanaryPercent, rec.StabilizationWindowSeconds, rec.DrainTimeoutSeconds,
			rec.AutoAdvance, rec.PauseReason, pausedAt, phaseEntered, rec.DrainingSessions,
			next, now)
		if err != nil {
			if isUniqueViolation(err) {
				return runtimeupgradestore.Record{}, runtimeupgradestore.ErrConflict
			}
			return runtimeupgradestore.Record{}, fmt.Errorf("runtimeupgradestore: insert: %w", err)
		}
		rec.CreatedAt = now
	} else {
		tag, err := s.pool.Exec(ctx, `
			UPDATE runtime_upgrade
			SET phase = $2, prior_phase = $3, new_image = $4, previous_pool_spec = $5,
			    schema_version = $6, drain_first = $7, canary_percent = $8,
			    stabilization_window_secs = $9, drain_timeout_secs = $10, auto_advance = $11,
			    pause_reason = $12, paused_at = $13, phase_entered_at = $14,
			    draining_sessions = $15, version = $16, updated_at = $17
			WHERE pool = $1 AND version = $18`,
			rec.Pool, rec.Phase, rec.PriorPhase, rec.NewImage, spec, rec.SchemaVersion,
			rec.DrainFirst, rec.CanaryPercent, rec.StabilizationWindowSeconds, rec.DrainTimeoutSeconds,
			rec.AutoAdvance, rec.PauseReason, pausedAt, phaseEntered, rec.DrainingSessions,
			next, now, expectVersion)
		if err != nil {
			return runtimeupgradestore.Record{}, fmt.Errorf("runtimeupgradestore: update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return runtimeupgradestore.Record{}, runtimeupgradestore.ErrConflict
		}
	}
	rec.Version = next
	rec.UpdatedAt = now
	rec.PhaseEnteredAt = phaseEntered
	return rec, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	type sqlStater interface{ SQLState() string }
	var s sqlStater
	if errors.As(err, &s) {
		return s.SQLState() == "23505"
	}
	return false
}
