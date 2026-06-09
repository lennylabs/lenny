// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed deploymentconfigstore.Store over
// the single-row deployment_config_state table (migration 0163). It is
// the durable baseline the §16.7 deployment-transition audit emitter
// diffs each Helm render against; deploymentconfigstore.Memory is the
// in-memory alternative used by the no-Postgres gateway.
//
// The table is platform-operational singleton state (scope = 'platform'),
// not tenant-isolated, so this store operates on the pool directly without
// a per-tenant transaction context. spec: §16.7 lines 672, 676, 677, 682.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/deploymentconfigstore"
)

// Store is the Postgres-backed deployment-config baseline. Construct with New.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ deploymentconfigstore.Store = (*Store)(nil)

// New returns a Store over pool. The pool must address a database with the
// migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

// NewWithClock returns a Store with an injected clock for deterministic tests.
func NewWithClock(pool *pgxpool.Pool, clock func() time.Time) *Store {
	s := New(pool)
	if clock != nil {
		s.clock = clock
	}
	return s
}

// Get implements deploymentconfigstore.Store. A missing row reports
// found=false (the first-install case).
func (s *Store) Get(ctx context.Context) (deploymentconfigstore.Config, bool, error) {
	var cfg deploymentconfigstore.Config
	err := s.pool.QueryRow(ctx,
		`SELECT cycle_detection_mode, allow_self_recursion, default_max_depth,
		        elicitation_floor, last_revision
		   FROM deployment_config_state
		  WHERE scope = 'platform'`).
		Scan(&cfg.CycleDetectionMode, &cfg.AllowSelfRecursion, &cfg.DefaultMaxDepth,
			&cfg.ElicitationFloor, &cfg.LastRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return deploymentconfigstore.Config{}, false, nil
	}
	if err != nil {
		return deploymentconfigstore.Config{}, false, err
	}
	return cfg, true, nil
}

// Put implements deploymentconfigstore.Store. It upserts the single
// platform-scoped row.
func (s *Store) Put(ctx context.Context, cfg deploymentconfigstore.Config) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO deployment_config_state
		     (scope, cycle_detection_mode, allow_self_recursion, default_max_depth,
		      elicitation_floor, last_revision, updated_at)
		 VALUES ('platform', $1, $2, $3, $4, $5, $6)
		 ON CONFLICT (scope) DO UPDATE SET
		     cycle_detection_mode = EXCLUDED.cycle_detection_mode,
		     allow_self_recursion = EXCLUDED.allow_self_recursion,
		     default_max_depth    = EXCLUDED.default_max_depth,
		     elicitation_floor    = EXCLUDED.elicitation_floor,
		     last_revision        = EXCLUDED.last_revision,
		     updated_at           = EXCLUDED.updated_at`,
		cfg.CycleDetectionMode, cfg.AllowSelfRecursion, cfg.DefaultMaxDepth,
		cfg.ElicitationFloor, cfg.LastRevision, s.clock())
	return err
}
