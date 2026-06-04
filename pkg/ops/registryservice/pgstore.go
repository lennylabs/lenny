// SPDX-License-Identifier: MIT

package registryservice

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgSingleton is the fixed primary key of platform_registry_config: the
// table holds at most one runtime override row.
const pgSingleton = "singleton"

// PgStore is the Postgres-backed §25.8 registry-override store over the
// platform_registry_config singleton (migration 0135). A runtime PUT
// survives a lenny-ops restart or a leader handoff, satisfying the §25.8
// line 3362 "stored in Postgres, takes effect on next image resolution"
// contract.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore returns a PgStore over pool. The pool must point at a
// database with migration 0135 applied.
func NewPgStore(pool *pgxpool.Pool) *PgStore { return &PgStore{pool: pool} }

// Load returns the persisted override. ok is false when no runtime
// override has been written.
func (s *PgStore) Load(ctx context.Context) (Override, bool, error) {
	var (
		url           string
		overridesRaw  []byte
		pullSecret    string
		requireDigest bool
		updatedAt     time.Time
		updatedBy     string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT url, overrides, pull_secret_name, require_digest, updated_at, updated_by
		   FROM platform_registry_config WHERE id=$1`, pgSingleton).
		Scan(&url, &overridesRaw, &pullSecret, &requireDigest, &updatedAt, &updatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Override{}, false, nil
	}
	if err != nil {
		return Override{}, false, err
	}
	var overrides map[string]string
	if len(overridesRaw) > 0 {
		if err := json.Unmarshal(overridesRaw, &overrides); err != nil {
			return Override{}, false, err
		}
	}
	return Override{
		URL:            url,
		Overrides:      overrides,
		PullSecretName: pullSecret,
		RequireDigest:  requireDigest,
		UpdatedAt:      updatedAt.UTC(),
		UpdatedBy:      updatedBy,
	}, true, nil
}

// Save upserts the singleton override row.
func (s *PgStore) Save(ctx context.Context, o Override) error {
	overrides := o.Overrides
	if overrides == nil {
		overrides = map[string]string{}
	}
	raw, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO platform_registry_config
		   (id, url, overrides, pull_secret_name, require_digest, updated_at, updated_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
		   url              = EXCLUDED.url,
		   overrides        = EXCLUDED.overrides,
		   pull_secret_name = EXCLUDED.pull_secret_name,
		   require_digest   = EXCLUDED.require_digest,
		   updated_at       = EXCLUDED.updated_at,
		   updated_by       = EXCLUDED.updated_by`,
		pgSingleton, o.URL, raw, o.PullSecretName, o.RequireDigest, o.UpdatedAt.UTC(), o.UpdatedBy)
	return err
}

// Compile-time guard: PgStore satisfies the Store contract.
var _ Store = (*PgStore)(nil)
