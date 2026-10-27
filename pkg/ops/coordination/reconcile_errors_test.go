// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// errRedisWrite is the injected Redis write failure.
var errRedisWrite = errors.New("redis: write refused")

// faultyRedisTier is a Tier 2 fake whose SetEpoch and RemoveByID fail
// while their failure budgets last. RemoveByID drops the lock from the
// active set on success, so a later reconciliation no longer sees it.
type faultyRedisTier struct {
	*fakeTier
	setEpochFailures int
	removeFailures   int
	setEpochCalls    int
	removeCalls      []string
	// calls records the order of SetEpoch, RemoveByID, and ActiveLocks.
	calls []string
}

func newFaultyRedisTier() *faultyRedisTier {
	return &faultyRedisTier{fakeTier: newFakeTier(StoreRedis)}
}

func (f *faultyRedisTier) SetEpoch(ctx context.Context, v uint64) error {
	f.setEpochCalls++
	f.calls = append(f.calls, "SetEpoch")
	if f.setEpochFailures > 0 {
		f.setEpochFailures--
		return errRedisWrite
	}
	return f.fakeTier.SetEpoch(ctx, v)
}

func (f *faultyRedisTier) ActiveLocks(ctx context.Context) ([]Lock, error) {
	f.calls = append(f.calls, "ActiveLocks")
	return f.fakeTier.ActiveLocks(ctx)
}

func (f *faultyRedisTier) RemoveByID(_ context.Context, id string) error {
	f.removeCalls = append(f.removeCalls, id)
	f.calls = append(f.calls, "RemoveByID:"+id)
	if f.removeFailures > 0 {
		f.removeFailures--
		return errRedisWrite
	}
	kept := f.active[:0]
	for _, l := range f.active {
		if l.ID != id {
			kept = append(kept, l)
		}
	}
	f.active = kept
	return nil
}

// conflictingPGTier is a Tier 1 fake whose reconciler reports a
// pre-outage-wins conflict for every Redis lock passed to it on the
// scope a pre-outage Postgres lock holds, as the pgstore does.
type conflictingPGTier struct {
	*fakeTier
	preOutageScope string
	epoch          uint64
}

func (p *conflictingPGTier) Reconcile(_ context.Context, redisEpoch uint64, redisLocks []Lock, _ time.Time) (ReconcileOutcome, error) {
	p.reconciled++
	out := ReconcileOutcome{Epoch: max(p.epoch, redisEpoch)}
	for _, l := range redisLocks {
		if l.Scope == p.preOutageScope {
			out.Conflicts = append(out.Conflicts, SplitBrainConflict{
				Scope: l.Scope, Winner: "pre_outage", WinnerHolder: "alice",
				LoserHolder: l.AcquiredBy, LoserLockID: l.ID, LoserWasActive: true,
			})
		}
	}
	return out, nil
}

func newReconcileFixture() (*Service, *conflictingPGTier, *faultyRedisTier, *recordingMetrics) {
	pg := &conflictingPGTier{fakeTier: newFakeTier(StorePostgres), preOutageScope: "pool:p", epoch: 2}
	rd := newFaultyRedisTier()
	rd.epoch = 3
	rd.active = []Lock{{ID: "lock-r", Scope: "pool:p", AcquiredBy: "bob"}}
	m := &recordingMetrics{}
	svc := NewService(ServiceOptions{Postgres: pg, Redis: rd, Metrics: m, Audit: &recordingAudit{}})
	return svc, pg, rd, m
}

// spec: 25.4 (The lenny-ops Service: Tier Transitions and Split-Brain
// Prevention, deterministic split-brain resolution rule "The Redis lock is
// removed")
func TestReconcileReportsAndRetriesFailedLoserRemoval_spec_25_4(t *testing.T) {
	svc, _, rd, m := newReconcileFixture()
	rd.removeFailures = 1
	ctx := context.Background()

	err := svc.Reconcile(ctx)
	if !errors.Is(err, errRedisWrite) || !strings.Contains(err.Error(), "lock-r") {
		t.Fatalf("first pass with a failing removal: err %v, want an error wrapping the Redis failure and naming lock-r", err)
	}
	if len(m.splitBrain) != 1 {
		t.Fatalf("first pass split-brain metric = %v, want one detection recorded despite the removal failure", m.splitBrain)
	}
	if len(rd.active) != 1 {
		t.Fatalf("losing lock removed on a failed RemoveByID: active %v", rd.active)
	}

	// The next pass retries the removal before it reads the Redis locks,
	// so the surviving loser is removed rather than re-detected.
	rd.calls = nil
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("second pass after the Redis write recovers: %v", err)
	}
	if len(rd.calls) < 2 || rd.calls[0] != "RemoveByID:lock-r" || rd.calls[1] != "ActiveLocks" {
		t.Fatalf("second pass call order = %v, want the pending removal before ActiveLocks", rd.calls)
	}
	if len(rd.active) != 0 {
		t.Fatalf("losing lock survived the retried removal: active %v", rd.active)
	}
	if len(m.splitBrain) != 1 {
		t.Fatalf("split-brain metric after the retry = %v, want the conflict counted once", m.splitBrain)
	}

	// Once removed, nothing is pending.
	before := len(rd.removeCalls)
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if len(rd.removeCalls) != before {
		t.Fatalf("third pass removed %v again, want no pending removal", rd.removeCalls[before:])
	}
}

// spec: 25.4 (The lenny-ops Service: Tier Transitions and Split-Brain
// Prevention, deterministic split-brain resolution rule "The Redis lock is
// removed")
func TestReconcileRetryKeepsFailingRemovalPending_spec_25_4(t *testing.T) {
	svc, _, rd, m := newReconcileFixture()
	rd.removeFailures = 3 // the pass-1 removal, the pass-2 retry, and the pass-2 re-detection removal
	ctx := context.Background()
	if err := svc.Reconcile(ctx); err == nil {
		t.Fatal("first pass: nil error with a failing removal")
	}
	if err := svc.Reconcile(ctx); !errors.Is(err, errRedisWrite) {
		t.Fatalf("second pass with the removal still failing: %v, want the Redis failure", err)
	}
	// The retry failed, so the loser was still present and re-detected.
	if len(m.splitBrain) != 2 {
		t.Fatalf("split-brain metric = %v, want the surviving loser re-detected", m.splitBrain)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("third pass after recovery: %v", err)
	}
	if len(rd.active) != 0 {
		t.Fatalf("losing lock survived recovery: %v", rd.active)
	}
}

// spec: 25.4 (The lenny-ops Service: Tier Transitions and Split-Brain
// Prevention, "Outage epochs" stored in both Postgres and Redis)
func TestReconcileReportsAndRetriesFailedEpochMirror_spec_25_4(t *testing.T) {
	svc, _, rd, m := newReconcileFixture()
	rd.active = nil
	rd.epoch = 7
	rd.setEpochFailures = 1
	ctx := context.Background()

	err := svc.Reconcile(ctx)
	if !errors.Is(err, errRedisWrite) || !strings.Contains(err.Error(), "epoch") {
		t.Fatalf("pass with a failing epoch mirror: err %v, want an error wrapping the Redis failure and naming the epoch", err)
	}
	// The Postgres side committed, so the tracked epoch still advances.
	if m.outageEpoch != 7 || svc.currentEpoch() != 7 {
		t.Fatalf("tracked epoch = %d (metric %d), want 7", svc.currentEpoch(), m.outageEpoch)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("next pass after the Redis write recovers: %v", err)
	}
	if rd.setEpochCalls != 2 {
		t.Fatalf("SetEpoch calls = %d, want the mirror retried on the next pass", rd.setEpochCalls)
	}
}

// spec: 25.4 (The lenny-ops Service: Tier Transitions and Split-Brain
// Prevention)
func TestReconcileJoinsBothRedisWriteFailures_spec_25_4(t *testing.T) {
	svc, _, rd, _ := newReconcileFixture()
	rd.setEpochFailures, rd.removeFailures = 1, 1
	err := svc.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "epoch") || !strings.Contains(err.Error(), "lock-r") {
		t.Fatalf("pass with both writes failing: err %v, want both failures reported", err)
	}
}

// spec: 25.4 (The lenny-ops Service: Tier Transitions and Split-Brain
// Prevention)
func TestReconcileSucceedsWhenRedisWritesSucceed_spec_25_4(t *testing.T) {
	svc, _, rd, _ := newReconcileFixture()
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rd.removeCalls) != 1 || rd.removeCalls[0] != "lock-r" || rd.epoch != 3 {
		t.Fatalf("removals %v epoch %d, want [lock-r] and 3", rd.removeCalls, rd.epoch)
	}
}
