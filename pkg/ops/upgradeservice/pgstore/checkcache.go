// SPDX-License-Identifier: MIT

package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// CheckCacheStore is the Postgres-backed §25.8 release-channel cache over
// platform_upgrade_check_cache (migration 0124). It holds the last
// successful upgrade-check so an unreachable channel serves cached data
// (§25.8 line 3413) and an air-gapped install can pre-populate the row.
// Construct with NewCheckCache.
type CheckCacheStore struct {
	pool *pgxpool.Pool
}

// NewCheckCache returns a CheckCacheStore backed by pool.
func NewCheckCache(pool *pgxpool.Pool) *CheckCacheStore { return &CheckCacheStore{pool: pool} }

// Load returns the cached check. ok is false when the cache is empty.
func (s *CheckCacheStore) Load(ctx context.Context) (upgradeservice.CachedCheck, bool, error) {
	var (
		checkedAt time.Time
		current   string
		respRaw   []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT checked_at, current_version, response
		 FROM platform_upgrade_check_cache WHERE id=$1`, singleton).
		Scan(&checkedAt, &current, &respRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return upgradeservice.CachedCheck{}, false, nil
	}
	if err != nil {
		return upgradeservice.CachedCheck{}, false, err
	}
	var m releasechannel.Manifest
	if len(respRaw) > 0 {
		if err := json.Unmarshal(respRaw, &m); err != nil {
			return upgradeservice.CachedCheck{}, false, err
		}
	}
	return upgradeservice.CachedCheck{
		CheckedAt:      checkedAt.UTC(),
		CurrentVersion: current,
		Manifest:       m,
	}, true, nil
}

// Save replaces the cached check with the latest successful result. The
// ttl_seconds column keeps its table default (21600, 6h per §25.8 line
// 3414); the hourly check cron refreshes the row regardless of TTL.
func (s *CheckCacheStore) Save(ctx context.Context, cached upgradeservice.CachedCheck) error {
	respRaw, err := json.Marshal(cached.Manifest)
	if err != nil {
		return err
	}
	latest := cached.Manifest.Version
	_, err = s.pool.Exec(ctx,
		`INSERT INTO platform_upgrade_check_cache
		   (id, checked_at, current_version, latest_version, response)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (id) DO UPDATE SET
		   checked_at      = EXCLUDED.checked_at,
		   current_version = EXCLUDED.current_version,
		   latest_version  = EXCLUDED.latest_version,
		   response        = EXCLUDED.response`,
		singleton, cached.CheckedAt.UTC(), cached.CurrentVersion, latest, respRaw)
	return err
}

// Compile-time guard.
var _ upgradeservice.CheckCache = (*CheckCacheStore)(nil)
