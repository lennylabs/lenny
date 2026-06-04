// SPDX-License-Identifier: MIT

// Package saltlockpg is the Postgres-backed §12.8 line 856 salt-rotation
// advisory lock. It serializes a tenant's salt-rotation migration against
// its per-user erasure pseudonymization across gateway replicas, so a
// pseudonymize never reads or destroys a salt a rotation is actively
// using.
//
// The lock is a session-level Postgres advisory lock keyed on a stable
// 64-bit hash of `erasure_salt_migration:{tenant_id}`. Acquire and
// release run on the same pooled connection; the protected function runs
// in between on its own connections (it only needs the mutual exclusion,
// not the locking connection).
package saltlockpg

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
)

// Lock implements erasurejob.SaltRotationLock over a Postgres pool.
type Lock struct {
	pool *pgxpool.Pool
}

// New returns a Lock backed by pool.
func New(pool *pgxpool.Pool) *Lock { return &Lock{pool: pool} }

var _ erasurejob.SaltRotationLock = (*Lock)(nil)

// lockKey derives the §12.8 line 856 advisory-lock key for a tenant. The
// FNV-1a hash of `erasure_salt_migration:{tenant_id}` is reinterpreted as
// a signed 64-bit integer, the type pg_advisory_lock accepts. The hash is
// stable across processes so every replica computes the same key.
func lockKey(tenantID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("erasure_salt_migration:" + tenantID))
	return int64(h.Sum64())
}

// WithSaltLock acquires the per-tenant advisory lock, runs fn, and
// releases the lock on the same connection. It blocks until the lock is
// granted or ctx is done.
func (l *Lock) WithSaltLock(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	key := lockKey(tenantID)
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("saltlockpg: acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("saltlockpg: acquire advisory lock for %q: %w", tenantID, err)
	}
	defer func() {
		// Release on a background-derived context so a cancelled ctx still
		// frees the session-level lock before the connection returns to the
		// pool.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", key)
	}()

	return fn(ctx)
}
