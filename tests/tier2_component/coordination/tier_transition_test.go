//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.4 remediation-lock tier transition and
// split-brain reconciliation, wired end to end against a real Postgres
// (Tier 1) and a real Redis (Tier 2) through the tiered coordination
// Service. The store-level unit tests exercise pgstore.Reconcile against
// a hand-built Redis-lock slice; this test drives the whole recovery
// path — the Redis-side outage-epoch increment of the Postgres→Tier-2
// transition, post-outage Tier 2 acquisitions stamped with that epoch,
// and the single-transaction advisory-lock reconciliation pass — with
// the reconciler reading the live Redis store, so the wiring the unit
// tests bypass is covered.
package coordination_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// recMetrics captures the §25.4 lock metric signals for assertion.
type recMetrics struct {
	outageEpoch uint64
	splitBrain  []string
}

func (m *recMetrics) SetActiveStore(string)        {}
func (m *recMetrics) SetOutageEpoch(e uint64)      { m.outageEpoch = e }
func (m *recMetrics) SplitBrainDetected(p string)  { m.splitBrain = append(m.splitBrain, p) }
func (m *recMetrics) StealDone(string)             {}
func (m *recMetrics) SetClockSkew(string, float64) {}

// recAudit captures the §25.4 lock audit event names for assertion.
type recAudit struct{ splitBrain int }

func (a *recAudit) LockEvent(_ context.Context, event string, _ coordination.Lock, _ map[string]any) {
	if event == coordination.AuditLockSplitBrainDetected {
		a.splitBrain++
	}
}

// TestTierTransitionSplitBrainReconciliation_spec_25_4 drives the §25.4
// Postgres-down-then-up transition against a real pgstore + real
// redisstore through the tiered Service and asserts the recovery
// reconciliation: MAX(redis,postgres) is written to both epoch stores,
// each post-outage Redis lock is resolved by the deterministic
// split-brain rule (pre-outage wins when both are active; Redis wins when
// the pre-outage Postgres lock has expired), a Redis-only scope is copied
// into Postgres, and every conflict is logged to ops_lock_conflicts and
// surfaced on the split-brain metric and audit event.
//
// diagnosis: a failure means the tiered remediation-lock Service does not
// reconcile a real Postgres+Redis pair after an outage as §25.4 requires
// — the epoch stores drift, the split-brain resolution rule picks the
// wrong holder, or the ops_lock_conflicts audit trail is wrong — so two
// agents can run conflicting remediations after a Postgres recovery.
//
// spec: §25.4 lines 2220-2267 (tier transitions, outage epochs, the
// single-transaction advisory-lock reconciliation, and the deterministic
// split-brain resolution rule).
func TestTierTransitionSplitBrainReconciliation_spec_25_4(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	// Apply the §25.4 remediation-lock schema (migration 0121: the
	// ops_remediation_locks / ops_lock_epoch / ops_lock_conflicts tables).
	up, err := migrations.FS.ReadFile("0121_ops_remediation_locks.up.sql")
	if err != nil {
		t.Fatalf("read migration 0121: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration 0121: %v", err)
	}

	pgStore := pgstore.New(pg.Pool)
	rdStore := redisstore.New(rd.Client)
	metrics := &recMetrics{}
	audit := &recAudit{}
	svc := coordination.NewService(coordination.ServiceOptions{
		Postgres: pgStore,
		Redis:    rdStore,
		Metrics:  metrics,
		Audit:    audit,
	})

	// Pre-outage: two Tier 1 (Postgres) locks, acquired while Postgres is
	// the serving tier. pool:alpha (alice) stays active through the outage;
	// pool:bravo (dave) lapses during it (its expiry is moved into the past
	// below to model a lock the holder could not release while Postgres was
	// unreachable).
	if _, err := svc.Acquire(ctx, coordination.LockRequest{Scope: "pool:alpha", Operation: "scale", AcquiredBy: "alice", TTLSeconds: 300}); err != nil {
		t.Fatalf("pre-outage acquire pool:alpha: %v", err)
	}
	bravo, err := svc.Acquire(ctx, coordination.LockRequest{Scope: "pool:bravo", Operation: "scale", AcquiredBy: "dave", TTLSeconds: 300})
	if err != nil {
		t.Fatalf("pre-outage acquire pool:bravo: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE ops_remediation_locks SET expires_at = now() - interval '1 hour' WHERE id = $1`, bravo.ID); err != nil {
		t.Fatalf("expire pre-outage pool:bravo: %v", err)
	}

	// Postgres → Tier 2 transition: the Service increments the Redis-side
	// outage epoch (§25.4 line 2220, "increments the Redis-side epoch via
	// INCR ops:lock-epoch:current") so every Tier 2 acquisition carries it.
	// This is the store effect of Service.onPostgresUnavailable; drive it
	// directly since the live container cannot return a genuine outage.
	postEpoch, err := rdStore.IncrementEpoch(ctx)
	if err != nil {
		t.Fatalf("increment redis epoch: %v", err)
	}
	if postEpoch != 1 {
		t.Fatalf("post-outage redis epoch = %d, want 1", postEpoch)
	}

	// Post-outage Tier 2 (Redis) acquisitions, each stamped with the
	// incremented epoch: bob collides with the still-active alice on
	// pool:alpha; carol collides with the expired dave on pool:bravo; erin
	// takes pool:delta, which has no pre-outage Postgres lock.
	for _, a := range []coordination.LockRequest{
		{Scope: "pool:alpha", Operation: "scale", AcquiredBy: "bob", TTLSeconds: 300},
		{Scope: "pool:bravo", Operation: "scale", AcquiredBy: "carol", TTLSeconds: 300},
		{Scope: "pool:delta", Operation: "scale", AcquiredBy: "erin", TTLSeconds: 300},
	} {
		lock, err := rdStore.Acquire(ctx, a, postEpoch)
		if err != nil {
			t.Fatalf("post-outage redis acquire %s: %v", a.Scope, err)
		}
		if lock.Epoch != 1 {
			t.Errorf("redis lock %s epoch = %d, want 1 (stamped from ops:lock-epoch:current)", a.Scope, lock.Epoch)
		}
	}

	// Recovery: run the reconciliation pass. It reads the live Redis locks
	// and epoch, holds the advisory lock, writes MAX(redis,postgres) back to
	// Postgres, copies the Redis-only scope, and resolves the collisions.
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// MAX(redis=1, postgres=0) = 1 is written to the Postgres epoch store
	// and mirrored back to Redis so both hold the reconciled value.
	var pgEpoch int64
	if err := pg.Pool.QueryRow(ctx, `SELECT current FROM ops_lock_epoch WHERE id = 'singleton'`).Scan(&pgEpoch); err != nil {
		t.Fatalf("read postgres epoch: %v", err)
	}
	if pgEpoch != 1 {
		t.Errorf("postgres epoch after reconcile = %d, want 1 (MAX)", pgEpoch)
	}
	rdEpoch, err := rdStore.Epoch(ctx)
	if err != nil {
		t.Fatalf("read redis epoch: %v", err)
	}
	if rdEpoch != 1 {
		t.Errorf("redis epoch after reconcile = %d, want 1 (MAX mirrored back)", rdEpoch)
	}
	if metrics.outageEpoch != 1 {
		t.Errorf("outage epoch metric = %d, want 1", metrics.outageEpoch)
	}

	// Postgres now holds the winners: alpha stays with the pre-outage alice
	// (both active → pre-outage wins), bravo flips to carol (pre-outage
	// expired → Redis wins), and delta is erin's copied Redis-only lock.
	holders := map[string]string{}
	locks, err := pgStore.List(ctx)
	if err != nil {
		t.Fatalf("list postgres locks: %v", err)
	}
	for _, l := range locks {
		holders[l.Scope] = l.AcquiredBy
	}
	if holders["pool:alpha"] != "alice" {
		t.Errorf("pool:alpha holder = %q, want alice (both active → pre-outage wins)", holders["pool:alpha"])
	}
	if holders["pool:bravo"] != "carol" {
		t.Errorf("pool:bravo holder = %q, want carol (pre-outage expired → Redis wins)", holders["pool:bravo"])
	}
	if holders["pool:delta"] != "erin" {
		t.Errorf("pool:delta holder = %q, want erin (Redis-only scope copied in)", holders["pool:delta"])
	}

	// ops_lock_conflicts records both resolved collisions: one pre_outage
	// winner over an active loser (alpha), one post_outage winner (bravo).
	var preOutageActive, postOutage int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ops_lock_conflicts WHERE winner = 'pre_outage' AND loser_was_active`).Scan(&preOutageActive); err != nil {
		t.Fatalf("count pre_outage conflicts: %v", err)
	}
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ops_lock_conflicts WHERE winner = 'post_outage'`).Scan(&postOutage); err != nil {
		t.Fatalf("count post_outage conflicts: %v", err)
	}
	if preOutageActive != 1 {
		t.Errorf("pre_outage/active conflict rows = %d, want 1 (pool:alpha)", preOutageActive)
	}
	if postOutage != 1 {
		t.Errorf("post_outage conflict rows = %d, want 1 (pool:bravo)", postOutage)
	}

	// Each resolved conflict raises the split-brain metric (by scope
	// pattern) and the split-brain audit event.
	if len(metrics.splitBrain) != 2 {
		t.Errorf("split-brain metric fired %d times, want 2", len(metrics.splitBrain))
	}
	for _, p := range metrics.splitBrain {
		if p != "pool:{name}" {
			t.Errorf("split-brain metric label = %q, want pool:{name}", p)
		}
	}
	if audit.splitBrain != 2 {
		t.Errorf("split-brain audit events = %d, want 2", audit.splitBrain)
	}
}

// TestSplitBrainLosingHolderReceives409_spec_25_4 drives a both-locks-active
// split-brain (a pre-outage Postgres lock and a still-active post-outage
// Redis lock on the same scope), runs the recovery reconciliation, and then
// asserts the observable §25.4 contract for the losing Redis holder: its
// next heartbeat (Extend) returns 409 REMEDIATION_LOCK_CONFLICT carrying
// splitBrain:true, winner:"pre_outage", and winnerHolder set to the
// pre-outage acquiredBy. The existing reconciliation test asserts the
// split-brain metric and audit event fire; this test pins the failure path
// the loser actually observes, which the metric/audit assertions do not
// cover.
//
// diagnosis: a failure means the deterministic split-brain resolution does
// not notify the losing holder — the losing Redis lock is left live so the
// loser's heartbeat succeeds (or returns a bare 404) instead of the 409
// REMEDIATION_LOCK_CONFLICT with splitBrain:true — so the loser keeps
// running a remediation the winner now owns exclusively, which is the exact
// silent split-brain §25.4 is designed to prevent.
//
// spec: §25.4 line 2267 ("Not expired | Not expired | Pre-outage (Postgres)
// wins ... The Redis lock is removed; the Redis lock holder receives 409
// REMEDIATION_LOCK_CONFLICT with splitBrain: true, winner: "pre_outage",
// winnerHolder: "<pre-outage acquiredBy>" on its next heartbeat/list/release
// call.") and line 2271 (the losing holder is "always notified via the
// heartbeat path").
func TestSplitBrainLosingHolderReceives409_spec_25_4(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	up, err := migrations.FS.ReadFile("0121_ops_remediation_locks.up.sql")
	if err != nil {
		t.Fatalf("read migration 0121: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration 0121: %v", err)
	}

	pgStore := pgstore.New(pg.Pool)
	rdStore := redisstore.New(rd.Client)
	svc := coordination.NewService(coordination.ServiceOptions{
		Postgres: pgStore,
		Redis:    rdStore,
	})

	// Pre-outage: alice holds pool:alpha at Tier 1 (Postgres) and stays
	// active through the outage.
	if _, err := svc.Acquire(ctx, coordination.LockRequest{Scope: "pool:alpha", Operation: "scale", AcquiredBy: "alice", TTLSeconds: 300}); err != nil {
		t.Fatalf("pre-outage acquire pool:alpha: %v", err)
	}

	// Postgres → Tier 2 transition: bump the Redis-side outage epoch so the
	// post-outage acquisition carries it.
	postEpoch, err := rdStore.IncrementEpoch(ctx)
	if err != nil {
		t.Fatalf("increment redis epoch: %v", err)
	}

	// Post-outage: bob acquires the same scope at Tier 2 (Redis) while
	// Postgres is unreachable, still active at reconciliation time. bob is
	// the losing holder once Postgres recovers (both active → pre-outage
	// alice wins).
	bob, err := rdStore.Acquire(ctx, coordination.LockRequest{Scope: "pool:alpha", Operation: "scale", AcquiredBy: "bob", TTLSeconds: 300}, postEpoch)
	if err != nil {
		t.Fatalf("post-outage redis acquire pool:alpha: %v", err)
	}

	// Recovery: reconcile. alice keeps pool:alpha; bob is the loser.
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The losing holder's next heartbeat must return 409
	// REMEDIATION_LOCK_CONFLICT with splitBrain:true, winner:"pre_outage",
	// winnerHolder:"alice".
	bobCtx := coordination.WithCaller(ctx, "bob")
	_, err = svc.Extend(bobCtx, bob.ID, 300)
	if err == nil {
		t.Fatalf("losing holder heartbeat succeeded; want 409 REMEDIATION_LOCK_CONFLICT splitBrain")
	}
	if code := coordination.CodeOf(err); code != coordination.ErrCodeConflict {
		t.Errorf("losing holder heartbeat error code = %q, want %q (REMEDIATION_LOCK_CONFLICT)", code, coordination.ErrCodeConflict)
	}
	if !coordination.IsSplitBrain(err) {
		t.Errorf("losing holder heartbeat error IsSplitBrain = false, want true (splitBrain:true)")
	}
	winner, winnerHolder := coordination.SplitBrainDetails(err)
	if winner != "pre_outage" {
		t.Errorf("split-brain winner = %q, want pre_outage", winner)
	}
	if winnerHolder != "alice" {
		t.Errorf("split-brain winnerHolder = %q, want alice (the pre-outage acquiredBy)", winnerHolder)
	}

	// The losing Redis lock is removed (§25.4 line 2267 "The Redis lock is
	// removed") so a later Postgres outage cannot resurface it.
	if got, err := rdStore.Get(ctx, bob.ID); coordination.CodeOf(err) != coordination.ErrCodeNotFound {
		t.Errorf("losing Redis lock still present after reconcile: lock=%v err=%v, want REMEDIATION_LOCK_NOT_FOUND", got, err)
	}

	// The losing holder is notified on the release path too (§25.4 line
	// 2267 names heartbeat/list/release).
	if relErr := svc.Release(bobCtx, bob.ID); !coordination.IsSplitBrain(relErr) {
		t.Errorf("losing holder release error IsSplitBrain = false (err=%v), want split-brain 409", relErr)
	}
}
