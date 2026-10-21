//go:build component

// SPDX-License-Identifier: MIT

package tenantsalt_test

import (
	"context"
	"errors"
	"hash/fnv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/saltlockpg"
)

// saltKey mirrors the salt lock's key derivation, so the test can probe
// the lock from another connection.
func saltKey(tenantID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("erasure_salt_migration:" + tenantID))
	return int64(h.Sum64())
}

// lockFree reports whether a fresh transaction on pool acquires tenant's
// salt lock at once.
func lockFree(t *testing.T, pool *pgxpool.Pool, tenantID string) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, saltKey(tenantID)).Scan(&ok); err != nil {
		t.Fatalf("probe lock: %v", err)
	}
	return ok
}

// advisoryLocks counts the advisory locks any backend holds.
func advisoryLocks(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted`).Scan(&n); err != nil {
		t.Fatalf("pg_locks: %v", err)
	}
	return n
}

// startSaltLockPG starts Postgres with the migrations and returns its
// pool and a second pool capped at one connection.
func startSaltLockPG(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	pg := startPG(t)
	cfg, err := pgxpool.ParseConfig(pg.DSN)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("one-connection pool: %v", err)
	}
	t.Cleanup(one.Close)
	return pg.Pool, one
}

// spec: 12.8 (erasure_salt key management), 12.3 (Postgres HA Requirements)
// diagnosis: a failure means the salt lock does not serialize one tenant's
// pseudonymize and rotation across connections, blocks another tenant,
// holds a pooled connection while it waits or runs (deadlocking a small
// pool), or leaves an advisory lock behind after its holder returns,
// errors, or is cancelled, so later erasures of the tenant wait forever.
func TestSaltLockTransactionScopedOutsidePool_spec_12_8(t *testing.T) {
	t.Parallel()
	pool, one := startSaltLockPG(t)
	ctx := context.Background()

	t.Run("one tenant serializes, another does not wait", func(t *testing.T) {
		lock := saltlockpg.New(pool)
		holding := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- lock.WithSaltLock(ctx, "acme", func(context.Context) error {
				close(holding)
				<-release
				return nil
			})
		}()
		<-holding
		second := make(chan error, 1)
		go func() { second <- lock.WithSaltLock(ctx, "acme", func(context.Context) error { return nil }) }()
		if err := lock.WithSaltLock(ctx, "globex", func(context.Context) error { return nil }); err != nil {
			t.Fatalf("globex while acme is held: %v", err)
		}
		select {
		case err := <-second:
			t.Fatalf("a second acme call returned while the lock was held: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("holder: %v", err)
		}
		if err := <-second; err != nil {
			t.Fatalf("second acme call: %v", err)
		}
	})

	t.Run("holder queries a one-connection pool while a waiter waits", func(t *testing.T) {
		lock := saltlockpg.New(one)
		holding := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- lock.WithSaltLock(ctx, "acme", func(ctx context.Context) error {
				close(holding)
				<-release
				// The fn queries through the same one-connection pool the
				// lock was built over: a lock that held a pooled connection
				// would leave this query nothing to run on.
				qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				var n int
				return one.QueryRow(qctx, `SELECT 1`).Scan(&n)
			})
		}()
		<-holding
		waiter := make(chan error, 1)
		go func() { waiter <- lock.WithSaltLock(ctx, "acme", func(context.Context) error { return nil }) }()
		time.Sleep(100 * time.Millisecond)
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("holder query through the one-connection pool: %v", err)
		}
		if err := <-waiter; err != nil {
			t.Fatalf("waiter: %v", err)
		}
	})

	for _, tc := range []struct {
		name string
		run  func(lock *saltlockpg.Lock) error
	}{
		{"after fn returns", func(lock *saltlockpg.Lock) error {
			return lock.WithSaltLock(ctx, "acme", func(context.Context) error { return nil })
		}},
		{"after fn errors", func(lock *saltlockpg.Lock) error {
			err := lock.WithSaltLock(ctx, "acme", func(context.Context) error { return errors.New("fn failed") })
			if err == nil {
				return errors.New("fn error was not returned")
			}
			return nil
		}},
		{"after a cancel during fn", func(lock *saltlockpg.Lock) error {
			cctx, cancel := context.WithCancel(ctx)
			_ = lock.WithSaltLock(cctx, "acme", func(context.Context) error {
				cancel()
				return context.Canceled
			})
			return nil
		}},
		{"after a cancel during the wait", func(lock *saltlockpg.Lock) error {
			holding := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- lock.WithSaltLock(ctx, "acme", func(context.Context) error {
					close(holding)
					<-release
					return nil
				})
			}()
			<-holding
			cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			if err := lock.WithSaltLock(cctx, "acme", func(context.Context) error { return nil }); err == nil {
				return errors.New("a waiter whose context ended acquired a held lock")
			}
			close(release)
			return <-done
		}},
	} {
		t.Run("no lock left "+tc.name, func(t *testing.T) {
			if err := tc.run(saltlockpg.New(pool)); err != nil {
				t.Fatal(err)
			}
			if !lockFree(t, pool, "acme") {
				t.Error("another connection could not acquire the salt lock at once")
			}
			if n := advisoryLocks(t, pool); n != 0 {
				t.Errorf("pg_locks shows %d advisory locks left behind, want 0", n)
			}
		})
	}
}
