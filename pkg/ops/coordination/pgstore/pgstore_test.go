// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// setup brings up an embedded Postgres, applies migration 0121 (the
// platform-scoped ops_remediation_locks / ops_lock_epoch /
// ops_lock_conflicts tables), and returns a connected pool + store. It
// downloads the PostgreSQL bundle, so it is skipped under -short.
func setup(t *testing.T) (*pgstore.Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; §17.4 forbids hardcoded ports and they collide under parallel tests
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	up, err := migrations.FS.ReadFile("0121_ops_remediation_locks.up.sql")
	if err != nil {
		t.Fatalf("read 0121: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0121: %v", err)
	}
	return pgstore.New(pool), pool, ctx
}

func mustAcquire(t *testing.T, s *pgstore.Store, ctx context.Context, scope, by string, ttl int) *coordination.Lock {
	t.Helper()
	lock, err := s.Acquire(ctx, coordination.LockRequest{Scope: scope, Operation: "scale", AcquiredBy: by, TTLSeconds: ttl}, 0)
	if err != nil {
		t.Fatalf("acquire %s: %v", scope, err)
	}
	return lock
}

// spec: §25.4 lines 2170, 2187, 2193 — Tier 1 acquire uses UNIQUE(scope)
// as the CAS; a non-expired lock yields REMEDIATION_LOCK_CONFLICT;
// release frees the scope.
func TestPgAcquireConflictRelease_spec_25_4(t *testing.T) {
	s, _, ctx := setup(t)
	lock := mustAcquire(t, s, ctx, "pool:p", "alice", 300)
	if lock.LockStore != coordination.StorePostgres || lock.Epoch != 0 {
		t.Errorf("lock = %+v, want postgres/epoch0", lock)
	}
	_, err := s.Acquire(ctx, coordination.LockRequest{Scope: "pool:p", AcquiredBy: "bob"}, 0)
	if coordination.CodeOf(err) != coordination.ErrCodeConflict {
		t.Fatalf("second acquire = %v, want CONFLICT", err)
	}
	if err := s.Release(ctx, lock.ID, "bob"); coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Errorf("release by non-owner = %v, want NOT_OWNED", err)
	}
	if err := s.Release(ctx, lock.ID, "alice"); err != nil {
		t.Fatalf("release by owner: %v", err)
	}
	// Scope is free again.
	mustAcquire(t, s, ctx, "pool:p", "bob", 300)
}

// spec: §25.4 lines 2193, 2303 — an expired row does not block a fresh
// acquire (lazy cleanup), and the periodic Reap removes expired rows.
func TestPgExpiryAndReap_spec_25_4(t *testing.T) {
	s, pool, ctx := setup(t)
	lock := mustAcquire(t, s, ctx, "pool:p", "alice", 300)
	// Force the row to be expired.
	if _, err := pool.Exec(ctx, `UPDATE ops_remediation_locks SET expires_at = now() - interval '1 hour' WHERE id=$1`, lock.ID); err != nil {
		t.Fatal(err)
	}
	// A fresh acquire on the same scope succeeds (the expired row is reaped
	// in the acquire statement).
	fresh := mustAcquire(t, s, ctx, "pool:p", "bob", 300)
	if fresh.AcquiredBy != "bob" {
		t.Errorf("fresh acquire holder = %q, want bob", fresh.AcquiredBy)
	}
	// Expire it and reap explicitly.
	if _, err := pool.Exec(ctx, `UPDATE ops_remediation_locks SET expires_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	n, err := s.Reap(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reap = %d (%v), want 1", n, err)
	}
}

// spec: §25.4 lines 2303-2306, 2282-2295 — identity-based Extend and the
// audited Steal recording the prior holder.
func TestPgExtendSteal_spec_25_4(t *testing.T) {
	s, _, ctx := setup(t)
	lock := mustAcquire(t, s, ctx, "pool:p", "alice", 300)
	if _, err := s.Extend(ctx, lock.ID, 600, "bob"); coordination.CodeOf(err) != coordination.ErrCodeNotOwned {
		t.Errorf("extend by non-owner = %v, want NOT_OWNED", err)
	}
	ext, err := s.Extend(ctx, lock.ID, 600, "alice")
	if err != nil || ext.Revision != 1 {
		t.Fatalf("extend = %+v (%v), want revision 1", ext, err)
	}
	stolen, err := s.Steal(ctx, lock.ID, coordination.StealRequest{Confirm: true, AcquiredBy: "carol", TTLSeconds: 300})
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if stolen.AcquiredBy != "carol" || stolen.StolenFrom != "alice" {
		t.Errorf("steal = %+v, want carol/alice", stolen)
	}
}

// spec: §25.4 lines 2218-2222, 2230 — the outage epoch counter: default 0,
// increment advances it, SetEpoch applies the MAX (never backward).
func TestPgEpoch_spec_25_4(t *testing.T) {
	s, _, ctx := setup(t)
	if e, _ := s.Epoch(ctx); e != 0 {
		t.Fatalf("initial epoch = %d, want 0", e)
	}
	if e, err := s.IncrementEpoch(ctx); err != nil || e != 1 {
		t.Fatalf("increment = %d (%v), want 1", e, err)
	}
	if err := s.SetEpoch(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(ctx, 3); err != nil { // lower: ignored by GREATEST
		t.Fatal(err)
	}
	if e, _ := s.Epoch(ctx); e != 7 {
		t.Errorf("epoch = %d, want 7", e)
	}
}

// spec: §25.4 lines 2255-2267 — the deterministic split-brain resolution:
// both active → pre-outage (Postgres) wins; a Redis-only scope is copied
// in; the conflict is recorded in ops_lock_conflicts.
func TestPgReconcileSplitBrainPreOutageWins_spec_25_4(t *testing.T) {
	s, pool, ctx := setup(t)
	// Pre-outage Postgres lock on pool:p (active), held by alice.
	mustAcquire(t, s, ctx, "pool:p", "alice", 300)

	now := time.Now().UTC()
	redisLocks := []coordination.Lock{
		// Post-outage Redis lock on the same scope (active) — the collision.
		{
			ID: "lock-r1", Scope: "pool:p", Operation: "scale", AcquiredBy: "bob",
			AcquiredAt: now, ExpiresAt: now.Add(5 * time.Minute), Epoch: 4,
		},
		// Redis-only scope — no pre-outage Postgres lock, so it is copied in.
		{
			ID: "lock-r2", Scope: "pool:q", Operation: "scale", AcquiredBy: "carol",
			AcquiredAt: now, ExpiresAt: now.Add(5 * time.Minute), Epoch: 4,
		},
	}
	out, err := s.Reconcile(ctx, 4, redisLocks, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Epoch != 4 {
		t.Errorf("reconciled epoch = %d, want 4 (MAX)", out.Epoch)
	}
	if out.Copied != 1 {
		t.Errorf("copied = %d, want 1 (pool:q)", out.Copied)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0].Winner != "pre_outage" || !out.Conflicts[0].LoserWasActive {
		t.Fatalf("conflicts = %+v, want one pre_outage winner with active loser", out.Conflicts)
	}
	// pool:p is still alice's; pool:q now carol's.
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]string{}
	for _, l := range all {
		holders[l.Scope] = l.AcquiredBy
	}
	if holders["pool:p"] != "alice" {
		t.Errorf("pool:p holder = %q, want alice (pre-outage wins)", holders["pool:p"])
	}
	if holders["pool:q"] != "carol" {
		t.Errorf("pool:q holder = %q, want carol (copied)", holders["pool:q"])
	}
	// The conflict is recorded for post-incident audit.
	var conflictRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ops_lock_conflicts WHERE winner='pre_outage'`).Scan(&conflictRows); err != nil {
		t.Fatal(err)
	}
	if conflictRows != 1 {
		t.Errorf("ops_lock_conflicts rows = %d, want 1", conflictRows)
	}
}

// spec: §25.4 lines 2259-2260 — when the pre-outage Postgres lock is
// expired by clock, the Redis lock wins and replaces the Postgres row.
func TestPgReconcileSplitBrainRedisWins_spec_25_4(t *testing.T) {
	s, pool, ctx := setup(t)
	lock := mustAcquire(t, s, ctx, "pool:p", "alice", 300)
	// Expire the pre-outage Postgres lock.
	if _, err := pool.Exec(ctx, `UPDATE ops_remediation_locks SET expires_at = now() - interval '1 hour' WHERE id=$1`, lock.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	redisLocks := []coordination.Lock{
		{
			ID: "lock-r1", Scope: "pool:p", Operation: "scale", AcquiredBy: "bob",
			AcquiredAt: now, ExpiresAt: now.Add(5 * time.Minute), Epoch: 9,
		},
	}
	out, err := s.Reconcile(ctx, 9, redisLocks, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0].Winner != "post_outage" {
		t.Fatalf("conflicts = %+v, want one post_outage winner", out.Conflicts)
	}
	all, _ := s.List(ctx)
	if len(all) != 1 || all[0].AcquiredBy != "bob" || all[0].Epoch != 9 {
		t.Errorf("after reconcile = %+v, want bob/epoch9 (Redis wins)", all)
	}
}

// spec: §25.4 line 2266 — when the pre-outage Postgres lock is not
// expired and the post-outage Redis lock has expired by clock, Postgres
// wins: the Redis lock is discarded and the Postgres lock is retained
// with its pre-outage epoch. The conflict is logged with a non-active
// loser.
func TestPgReconcileSplitBrainPostgresWinsRedisExpired_spec_25_4(t *testing.T) {
	s, pool, ctx := setup(t)
	// Pre-outage Postgres lock on pool:p (active), held by alice at epoch 0.
	mustAcquire(t, s, ctx, "pool:p", "alice", 300)

	now := time.Now().UTC()
	redisLocks := []coordination.Lock{
		// Post-outage Redis lock on the same scope, already expired by clock
		// at reconciliation time (acquiredAt/expiresAt both in the past).
		{
			ID: "lock-r1", Scope: "pool:p", Operation: "scale", AcquiredBy: "bob",
			AcquiredAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-1 * time.Hour), Epoch: 6,
		},
	}
	out, err := s.Reconcile(ctx, 6, redisLocks, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Epoch != 6 {
		t.Errorf("reconciled epoch = %d, want 6 (MAX)", out.Epoch)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0].Winner != "pre_outage" || out.Conflicts[0].LoserWasActive {
		t.Fatalf("conflicts = %+v, want one pre_outage winner with a non-active loser", out.Conflicts)
	}
	// pool:p stays alice's at the pre-outage epoch 0; the expired Redis lock
	// is not copied in.
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].AcquiredBy != "alice" || all[0].Epoch != 0 {
		t.Errorf("after reconcile = %+v, want alice/epoch0 (Postgres wins, pre-outage epoch retained)", all)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ops_lock_conflicts WHERE winner='pre_outage' AND NOT loser_was_active`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("ops_lock_conflicts pre_outage/non-active rows = %d, want 1", rows)
	}
}

// TestPgServerTime_spec_25_4 asserts the exported ServerTime reads the
// Postgres now() at time zone 'UTC' clock the Postgres-Redis clock-skew
// sampler compares against the Redis TIME clock. The embedded Postgres
// and the test process share a host clock, so the read sits within a
// small window of the local wall clock; the assertion proves the read
// returns a live UTC server time rather than a zero value.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring); the Tier 1
// clock is Postgres now() at time zone 'UTC'.
func TestPgServerTime_spec_25_4(t *testing.T) {
	s, _, ctx := setup(t)
	before := time.Now().UTC()
	got, err := s.ServerTime(ctx)
	if err != nil {
		t.Fatalf("ServerTime: %v", err)
	}
	after := time.Now().UTC()
	if got.IsZero() {
		t.Fatal("ServerTime returned the zero time; expected a live server clock read")
	}
	// The server clock shares the host clock with the test, so it must sit
	// inside the [before-2s, after+2s] window. A wider tolerance absorbs
	// the round-trip and the per-second granularity without flaking.
	lo, hi := before.Add(-2*time.Second), after.Add(2*time.Second)
	if got.Before(lo) || got.After(hi) {
		t.Errorf("ServerTime = %v, outside the expected window [%v, %v]", got, lo, hi)
	}
}

// TestPgServerTimeStoreUnavailable_spec_25_4 covers the ServerTime
// Postgres-outage branch: a pool pointed at an unreachable address fails
// the now() read with a transport error, which the store must classify as
// coordination.ErrStoreUnavailable rather than a generic error, so the
// clock-skew sampler skips the sample during a Postgres outage instead of
// reporting a spurious skew. It needs no Postgres bundle because the
// connection never succeeds.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring; a Postgres
// outage surfaces as ErrStoreUnavailable so the sampler skips the sample).
func TestPgServerTimeStoreUnavailable_spec_25_4(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 127.0.0.1:1 is the discard port: no Postgres listens there, so the
	// first query fails with a connection-refused transport error.
	pool, err := pgxpool.New(ctx, "postgres://lenny:lenny@127.0.0.1:1/lenny?sslmode=disable")
	if err != nil {
		t.Fatalf("pool New: %v", err)
	}
	defer pool.Close()
	s := pgstore.New(pool)
	if _, err := s.ServerTime(ctx); err == nil {
		t.Fatal("ServerTime against an unreachable Postgres returned nil; want ErrStoreUnavailable")
	} else if !errors.Is(err, coordination.ErrStoreUnavailable) {
		t.Fatalf("ServerTime outage error = %v, want coordination.ErrStoreUnavailable", err)
	}
}
