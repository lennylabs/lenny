// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.8 platform-upgrade durable
// state: the upgradeservice.Store over the platform_upgrade_state
// singleton (migration 0124) and the upgradeservice.CheckCache over the
// platform_upgrade_check_cache singleton.
//
// The upgrade-state store is the durability backbone §25.8 line 3560
// requires: the in-flight phase lives in Postgres, not in process
// memory, so a lenny-ops restart or a leader-election handoff during a
// long pause resumes the upgrade from its persisted phase rather than
// losing it. The check cache realizes the §25.8 line 3413-3414
// release-channel cache: a successful upgrade-check writes the cache, an
// unreachable channel serves the cached manifest with cached=true, and
// an air-gapped install can pre-populate the row.
//
// Both tables are platform-scoped (the §25 control plane is not
// multi-tenanted at this boundary; §25.4 line 1492 lists them among the
// PlatformPostgres() tables), so the store runs no tenant-scoped
// transaction and the tables carry no RLS policy.
//
// spec: §25.8 lines 3560, 3413-3414, 3579-3605.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// singleton is the fixed primary key of both §25.8 tables: each holds at
// most one row (the in-flight upgrade, the last channel check).
const singleton = "singleton"

// Store is the Postgres-backed §25.8 upgrade-state store. Construct with
// New. It persists the upgradeservice.State to platform_upgrade_state so
// a paused upgrade survives a leader handoff.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema (including 0124) applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// stateMeta holds the §25.8 State fields the platform_upgrade_state
// typed columns do not capture. They ride in the metadata JSONB so a
// Load reconstructs the exact State the orchestrator saved. The typed
// columns (target_version, current_phase, started_by, started_at,
// completed_at, paused_at) mirror the spec schema for SQL introspection.
type stateMeta struct {
	OperationID    string            `json:"operationId"`
	ImageDigest    string            `json:"imageDigest,omitempty"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	Paused         bool              `json:"paused"`
	Verified       bool              `json:"verified"`
	Reason         string            `json:"reason,omitempty"`
	PreviousImages map[string]string `json:"previousImages,omitempty"`
	OpsHeartbeat   time.Time         `json:"opsRollHeartbeat,omitempty"`
}

// Load returns the platform_upgrade_state singleton. ok is false when no
// upgrade has ever been recorded (the cold-start condition). A transport
// error is returned so the caller surfaces the §25.8 line 3610
// "Postgres down: upgrade state machine operations fail" case.
func (s *Store) Load(ctx context.Context) (upgradeservice.State, bool, error) {
	var (
		targetVersion string
		currentPhase  string
		startedBy     string
		startedAt     time.Time
		targetImgsRaw []byte
		upgradeErr    *string
		metaRaw       []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT target_version, current_phase, started_by, started_at, target_images, error, metadata
		 FROM platform_upgrade_state WHERE id=$1`, singleton).
		Scan(&targetVersion, &currentPhase, &startedBy, &startedAt, &targetImgsRaw, &upgradeErr, &metaRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return upgradeservice.State{}, false, nil
	}
	if err != nil {
		return upgradeservice.State{}, false, err
	}
	var meta stateMeta
	if len(metaRaw) > 0 {
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return upgradeservice.State{}, false, err
		}
	}
	var targetImages map[string]string
	if len(targetImgsRaw) > 0 {
		if err := json.Unmarshal(targetImgsRaw, &targetImages); err != nil {
			return upgradeservice.State{}, false, err
		}
	}
	upgradeErrVal := ""
	if upgradeErr != nil {
		upgradeErrVal = *upgradeErr
	}
	st := upgradeservice.State{
		OperationID:    meta.OperationID,
		Phase:          upgrade.Phase(currentPhase),
		TargetVersion:  targetVersion,
		ImageDigest:    meta.ImageDigest,
		TargetImages:   nilIfEmpty(targetImages),
		PreviousImages: meta.PreviousImages,
		StartedBy:      startedBy,
		StartedAt:      startedAt.UTC(),
		UpdatedAt:      meta.UpdatedAt.UTC(),
		Paused:         meta.Paused,
		Verified:       meta.Verified,
		Reason:         meta.Reason,
		Error:          upgradeErrVal,
		OpsHeartbeat:   meta.OpsHeartbeat.UTC(),
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = st.StartedAt
	}
	return st, true, nil
}

// Save upserts the singleton row, replacing any prior record. A new
// upgrade overwrites the prior terminal one, matching the §25.8
// single-upgrade-at-a-time model.
func (s *Store) Save(ctx context.Context, st upgradeservice.State) error {
	metaRaw, err := json.Marshal(stateMeta{
		OperationID:    st.OperationID,
		ImageDigest:    st.ImageDigest,
		UpdatedAt:      st.UpdatedAt.UTC(),
		Paused:         st.Paused,
		Verified:       st.Verified,
		Reason:         st.Reason,
		PreviousImages: st.PreviousImages,
		OpsHeartbeat:   st.OpsHeartbeat.UTC(),
	})
	if err != nil {
		return err
	}
	targetImages := st.TargetImages
	if targetImages == nil {
		targetImages = map[string]string{}
	}
	targetImgsRaw, err := json.Marshal(targetImages)
	if err != nil {
		return err
	}
	var upgradeErr *string
	if st.Error != "" {
		e := st.Error
		upgradeErr = &e
	}
	// completed_at / paused_at mirror the spec schema for SQL queries: a
	// terminal upgrade stamps completed_at, a paused one stamps paused_at.
	var completedAt, pausedAt *time.Time
	switch {
	case upgrade.IsTerminal(st.Phase):
		t := st.UpdatedAt.UTC()
		completedAt = &t
	case st.Paused:
		t := st.UpdatedAt.UTC()
		pausedAt = &t
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO platform_upgrade_state
		   (id, target_version, target_images, current_phase, started_by,
		    started_at, paused_at, completed_at, error, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO UPDATE SET
		   target_version = EXCLUDED.target_version,
		   target_images  = EXCLUDED.target_images,
		   current_phase  = EXCLUDED.current_phase,
		   started_by     = EXCLUDED.started_by,
		   started_at     = EXCLUDED.started_at,
		   paused_at      = EXCLUDED.paused_at,
		   completed_at   = EXCLUDED.completed_at,
		   error          = EXCLUDED.error,
		   metadata       = EXCLUDED.metadata`,
		singleton, st.TargetVersion, targetImgsRaw, string(st.Phase), st.StartedBy,
		st.StartedAt.UTC(), pausedAt, completedAt, upgradeErr, metaRaw)
	return err
}

// nilIfEmpty returns nil for an empty map so a round-tripped State keeps
// the TargetImages field omitempty-clean (an empty `{}` column decodes to
// an empty map, which the orchestrator treats as "no explicit plan").
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// Compile-time guard: Store satisfies the orchestrator's persistence
// contract.
var _ upgradeservice.Store = (*Store)(nil)
