// SPDX-License-Identifier: MIT

// Package pgstore is the §12.4 Postgres advisory-lock fallback for the
// §12.2 LeaseStore. The Redis-backed leasestore.Store is the primary
// coordination surface; when Redis is unavailable the
// leasestore.Failover wrapper routes lease operations here so a Redis
// outage does not break session-coordination lease acquisition (it only
// raises latency, per the §12.4 failure-behavior table).
//
// A lease is a row in session_leases keyed by (tenant_id, session_id)
// carrying the holder and an expires_at TTL. Each mutating operation
// runs inside a transaction that first takes a per-(tenant, session)
// transaction-level advisory lock (pg_advisory_xact_lock), so the
// read-then-write compare-and-set is atomic across gateway replicas —
// the role the Lua scripts play on the Redis fast path. The advisory
// lock releases automatically when the transaction commits or rolls
// back. A row whose expires_at has passed is treated as absent, exactly
// as a Redis key's PX TTL expiry would remove it.
//
// spec: §12.4 line 206 ("Distributed session leases | Fall back to
// Postgres advisory locks (higher latency)"); §12.4 line 181.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed LeaseStore fallback. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// pgstore satisfies the §12.2 LeaseStore role interface so it is a
// drop-in fallback behind the failover wrapper.
var _ leasestore.LeaseStore = (*Store)(nil)

// leaseKey is the §12.4 key the advisory-lock hash is derived from. It
// matches the Redis key form so the two stores lock the same logical
// resource namespace.
func leaseKey(tenantID, sessionID string) string {
	return "t:" + tenantID + ":lease:session:" + sessionID
}

// advisoryLock takes the per-(tenant, session) transaction-level
// advisory lock so the surrounding compare-and-set is serialized across
// replicas. hashtextextended maps the key string to the bigint
// pg_advisory_xact_lock requires; the lock releases at transaction end.
func advisoryLock(ctx context.Context, tx pgx.Tx, tenantID, sessionID string) error {
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		leaseKey(tenantID, sessionID))
	return err
}

// Acquire claims the session lease for holder with the given TTL. It
// succeeds when no live lease exists or the caller already holds it
// (the TTL is refreshed); it returns leasestore.ErrHeld when a live
// lease is held by a different holder.
func (s *Store) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	if ttl <= 0 {
		return leasestore.Lease{}, fmt.Errorf("leasestore/pgstore: ttl must be positive, got %s", ttl)
	}
	now := s.now()
	expires := now.Add(ttl)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if err := advisoryLock(ctx, tx, tenantID, sessionID); err != nil {
			return err
		}
		var (
			curHolder  string
			curExpires time.Time
		)
		row := tx.QueryRow(ctx,
			`SELECT holder, expires_at FROM session_leases
			 WHERE tenant_id = $1 AND session_id = $2`, tenantID, sessionID)
		switch err := row.Scan(&curHolder, &curExpires); {
		case errors.Is(err, pgx.ErrNoRows):
			// No row — free to acquire.
		case err != nil:
			return err
		default:
			// A live lease held by a different replica blocks the claim.
			if curExpires.After(now) && curHolder != holder {
				return leasestore.ErrHeld
			}
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO session_leases (tenant_id, session_id, holder, expires_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (tenant_id, session_id)
			 DO UPDATE SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at`,
			tenantID, sessionID, holder, expires)
		return err
	})
	if err != nil {
		return leasestore.Lease{}, err
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder, ExpiresAt: expires}, nil
}

// Renew extends the lease TTL only when holder is the current holder and
// the lease has not lapsed. Returns leasestore.ErrNotHeld otherwise.
func (s *Store) Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	if ttl <= 0 {
		return leasestore.Lease{}, fmt.Errorf("leasestore/pgstore: ttl must be positive, got %s", ttl)
	}
	now := s.now()
	expires := now.Add(ttl)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if err := advisoryLock(ctx, tx, tenantID, sessionID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE session_leases SET expires_at = $4
			 WHERE tenant_id = $1 AND session_id = $2 AND holder = $3 AND expires_at > $5`,
			tenantID, sessionID, holder, expires, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return leasestore.ErrNotHeld
		}
		return nil
	})
	if err != nil {
		return leasestore.Lease{}, err
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder, ExpiresAt: expires}, nil
}

// Release drops the lease when holder owns it. It is a no-op when the
// lease is already gone or held by another replica.
func (s *Store) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if err := advisoryLock(ctx, tx, tenantID, sessionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM session_leases
			 WHERE tenant_id = $1 AND session_id = $2 AND holder = $3`,
			tenantID, sessionID, holder)
		return err
	})
}

// Get returns the current live lease for the session, or
// leasestore.ErrNotFound when none is held or the lease has lapsed.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	now := s.now()
	var (
		holder  string
		expires time.Time
	)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT holder, expires_at FROM session_leases
			 WHERE tenant_id = $1 AND session_id = $2 AND expires_at > $3`,
			tenantID, sessionID, now)
		return row.Scan(&holder, &expires)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	if err != nil {
		return leasestore.Lease{}, err
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder, ExpiresAt: expires}, nil
}

// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. Session
// leases are coordination state keyed by session, not user data, and
// every lease carries a TTL that self-expires, so there is nothing
// user-scoped to erase here; the §12.8 step-1 lease release for the
// user's sessions is driven per session by the erasure orchestrator. It
// rejects empty arguments (§12.8 line 753) and otherwise returns
// (0, nil), matching the Redis store's behavior.
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 step 1.
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, leasestore.ErrEmptyScope
	}
	return 0, nil
}

// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// removes every lease row under the tenant for the §12.8 Phase-4 tenant
// teardown and returns the count dropped. Idempotent: a tenant with no
// leases is a no-op returning (0, nil).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, leasestore.ErrEmptyScope
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM session_leases WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}
