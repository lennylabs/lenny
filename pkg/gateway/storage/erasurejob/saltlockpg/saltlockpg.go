// SPDX-License-Identifier: MIT

// Package saltlockpg is the Postgres-backed §12.8 salt-rotation lock. It
// serializes a tenant's salt-rotation migration against its per-user
// erasure pseudonymization across gateway replicas, so a pseudonymize never
// reads or destroys a salt a rotation is actively using.
//
// The lock is a transaction-scoped Postgres advisory lock
// (pg_advisory_xact_lock) keyed on a stable 64-bit hash of
// `erasure_salt_migration:{tenant_id}`, taken inside a transaction on a
// dedicated connection opened outside the pool.
//
// The lock is transaction-scoped so it cannot outlive its holder. Behind
// the §12.3 transaction-mode pooler a transaction keeps one server
// connection from BEGIN to its end, so the lock and its release reach the
// same backend, and a client that dies inside the transaction ends it and
// frees the lock. A session-level lock taken and released in two
// autocommit statements can reach two backends behind that pooler, so the
// unlock misses and every later pseudonymize and rotation of the tenant
// waits on the leaked lock.
//
// The connection is outside the pool because the protected function
// itself queries through the pool (it reads and writes the tenant row). A
// holder that also held a pooled connection, and waiters that each held
// one while blocked on the lock, could exhaust a small pool and deadlock.
// A waiter here holds no pooled connection, so the erasure runner's
// concurrency limit needs no rule tied to the pool's size.
//
// spec: §12.8 (erasure_salt key management), §12.3 (Postgres HA
// Requirements).
package saltlockpg

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// Lock implements erasurejob.SaltRotationLock over a Postgres pool's
// connection configuration.
type Lock struct {
	pool *pgxpool.Pool
}

// New returns a Lock that opens its dedicated connections with pool's
// connection configuration.
func New(pool *pgxpool.Pool) *Lock { return &Lock{pool: pool} }

var _ erasurejob.SaltRotationLock = (*Lock)(nil)

// lockKey derives the §12.8 advisory-lock key for a tenant. The
// FNV-1a hash of `erasure_salt_migration:{tenant_id}` is reinterpreted as
// a signed 64-bit integer, the type pg_advisory_xact_lock accepts. The
// hash is stable across processes so every replica computes the same key.
func lockKey(tenantID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("erasure_salt_migration:" + tenantID))
	return int64(h.Sum64())
}

// cancelDeadline bounds how long a cancelled lock wait waits for the
// server to acknowledge the cancel before the connection is abandoned.
const cancelDeadline = 2 * time.Second

// lockConnConfig returns the pool's connection configuration with a
// context handler that sends the server a cancel request when the context
// ends. The default handler only abandons the socket, which leaves the
// backend waiting on the lock; that backend would then take the lock once
// the holder releases it and keep it until it noticed the closed socket.
// Cancelling on the server ends the wait itself, so a cancelled waiter
// never acquires the lock.
func lockConnConfig(pool *pgxpool.Pool) *pgx.ConnConfig {
	cfg := pool.Config().ConnConfig.Copy()
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: cancelDeadline}
	}
	return cfg
}

// WithSaltLock opens a dedicated connection, begins a transaction, takes
// the tenant's transaction-scoped advisory lock, runs fn, and commits. It
// blocks until the lock is granted or ctx ends; a context that ends during
// the wait or during fn rolls the transaction back, which frees the lock.
// On every return a transaction still open is rolled back and the
// connection closed, both on a context detached from ctx's cancellation.
func (l *Lock) WithSaltLock(ctx context.Context, tenantID string, fn func(context.Context) error) (err error) {
	conn, err := pgx.ConnectConfig(ctx, lockConnConfig(l.pool))
	if err != nil {
		return fmt.Errorf("saltlockpg: open lock connection: %w", err)
	}
	cleanup := context.WithoutCancel(ctx)
	defer func() { _ = conn.Close(cleanup) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("saltlockpg: begin lock transaction: %w", err)
	}
	defer func() {
		// Rollback after a successful Commit returns ErrTxClosed.
		if rerr := tx.Rollback(cleanup); rerr != nil && !errors.Is(rerr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("saltlockpg: roll back lock transaction: %w", rerr)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey(tenantID)); err != nil {
		return fmt.Errorf("saltlockpg: acquire advisory lock for %q: %w", tenantID, err)
	}
	if err := fn(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("saltlockpg: commit lock transaction for %q: %w", tenantID, err)
	}
	return nil
}
