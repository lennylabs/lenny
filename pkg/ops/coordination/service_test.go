// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"testing"
	"time"
)

// fakeTier is a controllable §25.4 lock store tier for the tiered-Service
// tests. It can simulate an outage (every method returns
// ErrStoreUnavailable), holds locks by scope/id, and tracks its epoch. The
// postgres variant additionally records Reconcile calls and returns a
// configured outcome.
type fakeTier struct {
	tier        string
	unavailable bool
	byID        map[string]*Lock
	byScope     map[string]string
	epoch       uint64
	active      []Lock
	outcome     ReconcileOutcome
	reconciled  int
	lastRedisE  uint64
}

func newFakeTier(tier string) *fakeTier {
	return &fakeTier{tier: tier, byID: map[string]*Lock{}, byScope: map[string]string{}}
}

func (f *fakeTier) Tier() string { return f.tier }

func (f *fakeTier) Acquire(_ context.Context, req LockRequest, epoch uint64) (*Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	if id, held := f.byScope[req.Scope]; held {
		l := f.byID[id]
		return nil, &Error{Code: ErrCodeConflict, Message: "scope " + req.Scope + " held by " + l.AcquiredBy}
	}
	l := &Lock{
		ID: NewLockID(), Scope: req.Scope, Operation: req.Operation,
		AcquiredBy: req.AcquiredBy, OperationID: req.OperationID,
		AcquiredAt: time.Now(), ExpiresAt: time.Now().Add(time.Duration(NormalizeTTL(req.TTLSeconds)) * time.Second),
		LockStore: f.tier, Epoch: epoch,
	}
	f.byID[l.ID] = l
	f.byScope[req.Scope] = l.ID
	cp := *l
	return &cp, nil
}

func (f *fakeTier) List(_ context.Context) ([]Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	var out []Lock
	for _, l := range f.byID {
		out = append(out, *l)
	}
	return out, nil
}

func (f *fakeTier) Get(_ context.Context, id string) (*Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	l, ok := f.byID[id]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + id}
	}
	cp := *l
	return &cp, nil
}

func (f *fakeTier) Extend(_ context.Context, id string, ttl int, caller string) (*Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	l, ok := f.byID[id]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + id}
	}
	if l.AcquiredBy != caller {
		return nil, &Error{Code: ErrCodeNotOwned}
	}
	l.Revision++
	cp := *l
	return &cp, nil
}

func (f *fakeTier) Release(_ context.Context, id, caller string) error {
	if f.unavailable {
		return ErrStoreUnavailable
	}
	l, ok := f.byID[id]
	if !ok {
		return &Error{Code: ErrCodeNotFound, Message: "no lock " + id}
	}
	if l.AcquiredBy != caller {
		return &Error{Code: ErrCodeNotOwned}
	}
	delete(f.byScope, l.Scope)
	delete(f.byID, id)
	return nil
}

func (f *fakeTier) Steal(_ context.Context, id string, req StealRequest) (*Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	l, ok := f.byID[id]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + id}
	}
	l.StolenFrom = l.AcquiredBy
	l.AcquiredBy = req.AcquiredBy
	l.Revision++
	cp := *l
	return &cp, nil
}

func (f *fakeTier) Reap(_ context.Context) (int, error) {
	if f.unavailable {
		return 0, ErrStoreUnavailable
	}
	return 0, nil
}

func (f *fakeTier) Epoch(_ context.Context) (uint64, error) {
	if f.unavailable {
		return 0, ErrStoreUnavailable
	}
	return f.epoch, nil
}

func (f *fakeTier) IncrementEpoch(_ context.Context) (uint64, error) {
	if f.unavailable {
		return 0, ErrStoreUnavailable
	}
	f.epoch++
	return f.epoch, nil
}

func (f *fakeTier) SetEpoch(_ context.Context, v uint64) error {
	if f.unavailable {
		return ErrStoreUnavailable
	}
	if v > f.epoch {
		f.epoch = v
	}
	return nil
}

func (f *fakeTier) ActiveLocks(_ context.Context) ([]Lock, error) {
	if f.unavailable {
		return nil, ErrStoreUnavailable
	}
	return f.active, nil
}

func (f *fakeTier) Reconcile(_ context.Context, redisEpoch uint64, _ []Lock, _ time.Time) (ReconcileOutcome, error) {
	f.reconciled++
	f.lastRedisE = redisEpoch
	return f.outcome, nil
}

// recordingMetrics captures the §25.4 lock metric signals.
type recordingMetrics struct {
	active      string
	outageEpoch uint64
	splitBrain  []string
	steals      []string
}

func (m *recordingMetrics) SetActiveStore(s string)      { m.active = s }
func (m *recordingMetrics) SetOutageEpoch(e uint64)      { m.outageEpoch = e }
func (m *recordingMetrics) SplitBrainDetected(p string)  { m.splitBrain = append(m.splitBrain, p) }
func (m *recordingMetrics) StealDone(p string)           { m.steals = append(m.steals, p) }
func (m *recordingMetrics) SetClockSkew(string, float64) {}

// recordingAudit captures the §25.4 lock audit events.
type recordingAudit struct{ events []string }

func (a *recordingAudit) LockEvent(_ context.Context, event string, _ Lock, _ map[string]any) {
	a.events = append(a.events, event)
}

func newWiredService(pg, rd *fakeTier, gate *CoordinationGate) (*Service, *recordingMetrics, *recordingAudit) {
	m := &recordingMetrics{}
	a := &recordingAudit{}
	opts := ServiceOptions{Gate: gate, Metrics: m, Audit: a}
	if pg != nil {
		opts.Postgres = pg
	}
	if rd != nil {
		opts.Redis = rd
	}
	return NewService(opts), m, a
}

// spec: §25.4 lines 2168-2174 — with both stores available the acquire is
// served at Tier 1 (Postgres).
func TestServiceAcquireServesPostgresTier_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	svc, m, a := newWiredService(pg, rd, NewCoordinationGate(MemoryTierSingleReplicaOnly, nil))
	lock, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lock.LockStore != StorePostgres {
		t.Errorf("lockStore = %q, want postgres", lock.LockStore)
	}
	if svc.ActiveTier() != StorePostgres || m.active != StorePostgres {
		t.Errorf("active tier = %q/%q, want postgres", svc.ActiveTier(), m.active)
	}
	if len(a.events) != 1 || a.events[0] != AuditLockAcquired {
		t.Errorf("audit = %v, want one lock_acquired", a.events)
	}
}

// spec: §25.4 lines 2168-2172, 2220 — a Postgres outage falls through to
// Redis (Tier 2) and increments the outage epoch exactly once.
func TestServiceFallsToRedisAndBumpsEpoch_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	pg.unavailable = true
	svc, m, _ := newWiredService(pg, rd, NewCoordinationGate(MemoryTierSingleReplicaOnly, nil))

	lock, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lock.LockStore != StoreRedis {
		t.Errorf("lockStore = %q, want redis", lock.LockStore)
	}
	if rd.epoch != 1 {
		t.Errorf("redis epoch = %d, want 1 (outage transition bumps it)", rd.epoch)
	}
	if m.outageEpoch != 1 {
		t.Errorf("outage epoch metric = %d, want 1", m.outageEpoch)
	}
	// A second acquire during the same outage does not bump the epoch again.
	if _, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:q", AcquiredBy: "bob"}); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if rd.epoch != 1 {
		t.Errorf("redis epoch = %d after second acquire, want 1 (no re-bump in same outage)", rd.epoch)
	}
}

// spec: §25.4 lines 2204-2212 — both stores down falls to the in-memory
// tier, granted under single-replica-only on a single replica.
func TestServiceFallsToMemoryWhenBothDown_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	pg.unavailable, rd.unavailable = true, true
	svc, _, a := newWiredService(pg, rd, NewCoordinationGate(MemoryTierSingleReplicaOnly, nil))
	lock, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lock.LockStore != StoreMemory {
		t.Errorf("lockStore = %q, want memory", lock.LockStore)
	}
	if len(a.events) != 1 || a.events[0] != AuditLockAcquired {
		t.Errorf("audit = %v, want lock_acquired", a.events)
	}
}

// spec: §25.4 lines 2208-2210, 2326 — both stores down with memoryTier
// "never" rejects with REMEDIATION_LOCK_NO_COORDINATION.
func TestServiceMemoryTierNeverRejects_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	pg.unavailable, rd.unavailable = true, true
	svc, _, _ := newWiredService(pg, rd, NewCoordinationGate(MemoryTierNever, nil))
	_, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if CodeOf(err) != ErrCodeNoCoordination {
		t.Fatalf("err = %v, want REMEDIATION_LOCK_NO_COORDINATION", err)
	}
}

// spec: §25.4 line 2092 — a live conflict at a consensus tier is returned
// to the caller, not retried at a lower tier.
func TestServiceConflictDoesNotFallThrough_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	pg.byScope["pool:p"] = "lock-x"
	pg.byID["lock-x"] = &Lock{ID: "lock-x", Scope: "pool:p", AcquiredBy: "bob"}
	svc, _, _ := newWiredService(pg, rd, NewCoordinationGate(MemoryTierAlways, nil))
	_, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if CodeOf(err) != ErrCodeConflict {
		t.Fatalf("err = %v, want REMEDIATION_LOCK_CONFLICT", err)
	}
	// Redis must not have been consulted (no fall-through on a real conflict).
	if len(rd.byID) != 0 {
		t.Errorf("redis received an acquire despite the Tier 1 conflict")
	}
}

// spec: §25.4 line 2131 — List returns the union across reachable tiers,
// deduplicated by id.
func TestServiceListUnionAcrossTiers_spec_25_4(t *testing.T) {
	pg := newFakeTier(StorePostgres)
	svc, _, _ := newWiredService(pg, nil, NewCoordinationGate(MemoryTierAlways, nil))
	if _, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:a", AcquiredBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	// Force a memory-tier lock by simulating a Postgres outage for one acquire.
	pg.unavailable = true
	if _, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:b", AcquiredBy: "bob"}); err != nil {
		t.Fatal(err)
	}
	pg.unavailable = false
	locks, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(locks) != 2 {
		t.Fatalf("list returned %d locks, want 2 (union of postgres + memory)", len(locks))
	}
	if locks[0].Scope != "pool:a" || locks[1].Scope != "pool:b" {
		t.Errorf("list order = %q,%q, want pool:a,pool:b", locks[0].Scope, locks[1].Scope)
	}
}

// spec: §25.4 lines 2126-2131 — Get/Release search the tiers and Release
// emits the audit event.
func TestServiceReleaseEmitsAudit_spec_25_4(t *testing.T) {
	pg := newFakeTier(StorePostgres)
	svc, _, a := newWiredService(pg, nil, NewCoordinationGate(MemoryTierAlways, nil))
	lock, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithCaller(context.Background(), "alice")
	if err := svc.Release(ctx, lock.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := a.events[len(a.events)-1]; got != AuditLockReleased {
		t.Errorf("last audit = %q, want lock_released", got)
	}
	if _, err := svc.Get(context.Background(), lock.ID); CodeOf(err) != ErrCodeNotFound {
		t.Errorf("get after release = %v, want NOT_FOUND", err)
	}
}

// spec: §25.4 lines 2282-2297, 2335 — Steal emits the steal metric (by
// scope pattern) and the lock_stolen audit event.
func TestServiceStealMetricAndAudit_spec_25_4(t *testing.T) {
	pg := newFakeTier(StorePostgres)
	svc, m, a := newWiredService(pg, nil, NewCoordinationGate(MemoryTierAlways, nil))
	lock, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:default-gvisor", AcquiredBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	stolen, err := svc.Steal(context.Background(), lock.ID, StealRequest{Confirm: true, AcquiredBy: "bob"})
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if stolen.StolenFrom != "alice" {
		t.Errorf("stolenFrom = %q, want alice", stolen.StolenFrom)
	}
	if len(m.steals) != 1 || m.steals[0] != "pool:{name}" {
		t.Errorf("steal metric labels = %v, want [pool:{name}]", m.steals)
	}
	if got := a.events[len(a.events)-1]; got != AuditLockStolen {
		t.Errorf("last audit = %q, want lock_stolen", got)
	}
}

// spec: §25.4 lines 2226-2267 — Reconcile drives the Postgres reconciler,
// records split-brain conflicts as a metric + audit event, and syncs the
// epoch to the reconciled MAX.
func TestServiceReconcileSplitBrain_spec_25_4(t *testing.T) {
	pg, rd := newFakeTier(StorePostgres), newFakeTier(StoreRedis)
	rd.epoch = 3
	rd.active = []Lock{{ID: "lock-r", Scope: "pool:p", AcquiredBy: "bob"}}
	pg.outcome = ReconcileOutcome{
		Epoch: 3,
		Conflicts: []SplitBrainConflict{
			{Scope: "pool:p", Winner: "pre_outage", WinnerHolder: "alice", LoserHolder: "bob", LoserWasActive: true},
		},
	}
	svc, m, a := newWiredService(pg, rd, NewCoordinationGate(MemoryTierAlways, nil))
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if pg.reconciled != 1 || pg.lastRedisE != 3 {
		t.Errorf("reconcile call = %d redisEpoch=%d, want 1/3", pg.reconciled, pg.lastRedisE)
	}
	if rd.epoch != 3 {
		t.Errorf("redis epoch after reconcile = %d, want 3 (MAX mirrored back)", rd.epoch)
	}
	if m.outageEpoch != 3 {
		t.Errorf("outage epoch metric = %d, want 3", m.outageEpoch)
	}
	if len(m.splitBrain) != 1 || m.splitBrain[0] != "pool:{name}" {
		t.Errorf("split-brain metric = %v, want [pool:{name}]", m.splitBrain)
	}
	if got := a.events[len(a.events)-1]; got != AuditLockSplitBrainDetected {
		t.Errorf("last audit = %q, want lock_split_brain_detected", got)
	}
}

// spec: §25.4 line 2226 — Reconcile is a no-op without a Postgres tier.
func TestServiceReconcileNoPostgresNoop_spec_25_4(t *testing.T) {
	rd := newFakeTier(StoreRedis)
	svc, _, _ := newWiredService(nil, rd, NewCoordinationGate(MemoryTierAlways, nil))
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// spec: §25.4 lines 2193, 2338 — the reap removes expired in-memory locks
// and emits a lock_expired audit event for each.
func TestServiceReapEmitsExpiredAudit_spec_25_4(t *testing.T) {
	svc, _, a := newWiredService(nil, nil, NewCoordinationGate(MemoryTierAlways, nil))
	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	svc.mem.SetClock(func() time.Time { return base })
	if _, err := svc.Acquire(context.Background(), LockRequest{Scope: "pool:p", AcquiredBy: "alice", TTLSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	a.events = nil
	// Advance past expiry and reap.
	svc.mem.SetClock(func() time.Time { return base.Add(time.Minute) })
	if n := svc.Reap(context.Background()); n != 1 {
		t.Fatalf("reaped = %d, want 1", n)
	}
	if len(a.events) != 1 || a.events[0] != AuditLockExpired {
		t.Errorf("audit = %v, want one lock_expired", a.events)
	}
}

// spec: §25.4 line 2209 — MemoryTierWarning surfaces the replica-local
// warning only under the "always" policy.
func TestServiceMemoryTierWarning_spec_25_4(t *testing.T) {
	always, _, _ := newWiredService(nil, nil, NewCoordinationGate(MemoryTierAlways, nil))
	if always.MemoryTierWarning() == "" {
		t.Error("always-mode warning is empty, want a replica-local warning")
	}
	single, _, _ := newWiredService(nil, nil, NewCoordinationGate(MemoryTierSingleReplicaOnly, nil))
	if single.MemoryTierWarning() != "" {
		t.Errorf("single-replica-only warning = %q, want empty", single.MemoryTierWarning())
	}
}
