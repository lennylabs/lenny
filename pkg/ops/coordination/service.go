// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ServiceOptions configures a tiered remediation-lock Service.
type ServiceOptions struct {
	// Postgres is the optional Tier 1 store. When it also implements the
	// reconciliation contract (the pgstore does), the Service runs the
	// §25.4 split-brain reconciliation pass through it.
	Postgres Store
	// Redis is the optional Tier 2 store.
	Redis Store
	// Gate applies the §25.4 ops.locks.memoryTier policy to a Tier 3
	// fall-through acquire. A nil gate grants every Tier 3 acquire (the
	// single-process dev posture).
	Gate *CoordinationGate
	// Metrics receives the §25.4 lock metric signals; nil drops them.
	Metrics Metrics
	// Audit receives the §25.4 lock audit events; nil drops them.
	Audit AuditSink
}

// Service is the §25.4 tiered remediation-lock service. It composes the
// durable Postgres (Tier 1) and Redis (Tier 2) stores over the
// always-present in-memory MemStore (Tier 3): Acquire attempts each tier
// in order so coordination survives a storage outage, the gate governs the
// Tier 3 fall-through, and a leader-only Reconcile pass resolves the
// split-brain a Postgres recovery can expose. Service implements
// RemediationLockService and is safe for concurrent use.
//
// spec: §25.4 lines 2083-2271.
type Service struct {
	durable []Store // [Postgres, Redis], reachable-configured only, in priority order
	pgRecon reconcilingStore
	redis   epochStore
	mem     *MemStore
	memT    Store
	gate    *CoordinationGate
	metrics Metrics
	audit   AuditSink

	mu         sync.Mutex
	activeTier string
	epoch      uint64 // tracked outage epoch stamped onto Tier 3 acquisitions
	pgDown     bool   // true once a Postgres outage has been observed this cycle
}

// NewService returns a tiered remediation-lock service from opts.
func NewService(opts ServiceOptions) *Service {
	mem := NewMemStore()
	s := &Service{
		mem:        mem,
		memT:       memTier{m: mem},
		gate:       opts.Gate,
		metrics:    opts.Metrics,
		audit:      opts.Audit,
		activeTier: StoreMemory,
	}
	if s.metrics == nil {
		s.metrics = NoopMetrics{}
	}
	if opts.Postgres != nil {
		s.durable = append(s.durable, opts.Postgres)
		if rc, ok := opts.Postgres.(reconcilingStore); ok {
			s.pgRecon = rc
		}
		s.activeTier = StorePostgres
	}
	if opts.Redis != nil {
		s.durable = append(s.durable, opts.Redis)
		if es, ok := opts.Redis.(epochStore); ok {
			s.redis = es
		}
		if opts.Postgres == nil {
			s.activeTier = StoreRedis
		}
	}
	s.metrics.SetActiveStore(s.activeTier)
	return s
}

// allTiers returns the durable tiers followed by the in-memory tier, in
// read/mutate priority order.
func (s *Service) allTiers() []Store {
	out := make([]Store, 0, len(s.durable)+1)
	out = append(out, s.durable...)
	out = append(out, s.memT)
	return out
}

func (s *Service) currentEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

func (s *Service) setActive(tier string) {
	s.mu.Lock()
	if s.activeTier != tier {
		s.activeTier = tier
		s.metrics.SetActiveStore(tier)
	}
	if tier == StorePostgres {
		s.pgDown = false
	}
	s.mu.Unlock()
}

// onPostgresUnavailable handles the §25.4 line 2220 Postgres→Tier-2
// transition: the first time a Postgres outage is observed in a cycle, the
// Redis-side outage epoch is incremented and stamped as the new tracked
// epoch so Tier 2 (and Tier 3) acquisitions carry it for later split-brain
// detection. Subsequent observations within the same outage are no-ops.
func (s *Service) onPostgresUnavailable(ctx context.Context) {
	s.mu.Lock()
	if s.pgDown {
		s.mu.Unlock()
		return
	}
	s.pgDown = true
	s.mu.Unlock()
	if s.redis == nil {
		return
	}
	if next, err := s.redis.IncrementEpoch(ctx); err == nil {
		s.mu.Lock()
		if next > s.epoch {
			s.epoch = next
		}
		epoch := s.epoch
		s.mu.Unlock()
		s.metrics.SetOutageEpoch(epoch)
	}
}

// ActiveTier reports the §25.4 lock store currently serving acquisitions
// (postgres, redis, or memory). The HTTP layer consults it so the
// ops.locks.memoryTier gate evaluates only when the in-memory tier is
// actually serving.
func (s *Service) ActiveTier() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTier
}

// MemoryTierWarning returns the §25.4 line 2215 replica-local degradation
// warning when the configured ops.locks.memoryTier is "always", or the
// empty string otherwise. The HTTP layer attaches it to a granted lock
// whose lockStore is memory.
func (s *Service) MemoryTierWarning() string {
	if s.gate == nil {
		return ""
	}
	if dec := s.gate.Evaluate(); dec.Warning != "" {
		return dec.Warning
	}
	return ""
}

// Acquire creates a §25.4 remediation lock, attempting the durable tiers
// (Postgres then Redis) before the gated in-memory fall-through. A live
// conflict at any tier returns REMEDIATION_LOCK_CONFLICT; a Tier 3
// acquire rejected by the ops.locks.memoryTier policy returns
// REMEDIATION_LOCK_NO_COORDINATION.
func (s *Service) Acquire(ctx context.Context, req LockRequest) (*Lock, error) {
	if req.Scope == "" {
		return nil, &Error{Code: ErrCodeConflict, Message: "scope is required"}
	}
	for _, st := range s.durable {
		lock, err := st.Acquire(ctx, req, s.currentEpoch())
		if err == nil {
			s.setActive(st.Tier())
			s.afterAcquire(ctx, lock)
			return lock, nil
		}
		if errors.Is(err, ErrStoreUnavailable) {
			if st.Tier() == StorePostgres {
				s.onPostgresUnavailable(ctx)
			}
			continue
		}
		return nil, err
	}
	// Tier 3: the in-memory fall-through, gated by ops.locks.memoryTier.
	if s.gate != nil {
		if dec := s.gate.Evaluate(); dec.Reject {
			return nil, &Error{
				Code: ErrCodeNoCoordination,
				Message: "both Postgres and Redis are unreachable and the in-memory lock tier is not " +
					"permitted for this deployment (ops.locks.memoryTier); retry after storage recovers",
			}
		}
	}
	s.mem.SetEpoch(s.currentEpoch())
	lock, err := s.mem.Acquire(ctx, req)
	if err != nil {
		return nil, err
	}
	s.setActive(StoreMemory)
	s.afterAcquire(ctx, lock)
	return lock, nil
}

// afterAcquire emits the §25.4 acquire metric and audit event.
func (s *Service) afterAcquire(ctx context.Context, lock *Lock) {
	if lock == nil {
		return
	}
	if s.audit != nil {
		s.audit.LockEvent(ctx, AuditLockAcquired, *lock, nil)
	}
}

// List returns the union of every active §25.4 lock across the reachable
// tiers, deduplicated by id (the highest reachable tier wins) and ordered
// by scope.
func (s *Service) List(ctx context.Context) ([]Lock, error) {
	seen := make(map[string]bool)
	var out []Lock
	var lastErr error
	any := false
	for _, st := range s.allTiers() {
		locks, err := st.List(ctx)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		any = true
		for _, l := range locks {
			if seen[l.ID] {
				continue
			}
			seen[l.ID] = true
			out = append(out, l)
		}
	}
	if !any && lastErr != nil {
		return nil, lastErr
	}
	sortByScope(out)
	return out, nil
}

// Get returns a single §25.4 lock from the first reachable tier that holds
// it, or REMEDIATION_LOCK_NOT_FOUND.
func (s *Service) Get(ctx context.Context, lockID string) (*Lock, error) {
	for _, st := range s.allTiers() {
		lock, err := st.Get(ctx, lockID)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if CodeOf(err) == ErrCodeNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		return lock, nil
	}
	return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
}

// Extend renews a §25.4 lock's TTL in the tier that holds it. The caller
// identity travels through the context (the HTTP layer sets it).
func (s *Service) Extend(ctx context.Context, lockID string, ttlSeconds int) (*Lock, error) {
	caller := callerFrom(ctx)
	for _, st := range s.allTiers() {
		lock, err := st.Extend(ctx, lockID, ttlSeconds, caller)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if CodeOf(err) == ErrCodeNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.audit != nil {
			s.audit.LockEvent(ctx, AuditLockExtended, *lock, nil)
		}
		return lock, nil
	}
	return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
}

// Release frees a §25.4 lock in the tier that holds it. The caller
// identity travels through the context.
func (s *Service) Release(ctx context.Context, lockID string) error {
	caller := callerFrom(ctx)
	for _, st := range s.allTiers() {
		// Capture the lock for the audit event before the release removes it.
		lock, getErr := st.Get(ctx, lockID)
		if errors.Is(getErr, ErrStoreUnavailable) {
			continue
		}
		if CodeOf(getErr) == ErrCodeNotFound {
			continue
		}
		err := st.Release(ctx, lockID, caller)
		if err != nil {
			return err
		}
		if s.audit != nil && lock != nil {
			s.audit.LockEvent(ctx, AuditLockReleased, *lock, nil)
		}
		return nil
	}
	return &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
}

// Steal takes over a §25.4 lock in the tier that holds it, recording the
// prior holder and emitting the lock_stolen audit event and steal metric.
func (s *Service) Steal(ctx context.Context, lockID string, req StealRequest) (*Lock, error) {
	if req.AcquiredBy == "" {
		req.AcquiredBy = callerFrom(ctx)
	}
	for _, st := range s.allTiers() {
		lock, err := st.Steal(ctx, lockID, req)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if CodeOf(err) == ErrCodeNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		s.metrics.StealDone(scopePattern(lock.Scope))
		if s.audit != nil {
			s.audit.LockEvent(ctx, AuditLockStolen, *lock, map[string]any{
				"stolenFrom": lock.StolenFrom,
				"reason":     req.Reason,
			})
		}
		return lock, nil
	}
	return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
}

// Reap drops expired §25.4 locks across the reachable tiers, emitting a
// lock_expired audit event for each in-memory lock removed (the durable
// tiers reap by bulk query and do not enumerate individual rows). It
// returns the total reaped. §25.4 runs it on a 60s periodic goroutine.
func (s *Service) Reap(ctx context.Context) int {
	total := 0
	for _, expired := range s.mem.ReapExpired() {
		total++
		if s.audit != nil {
			s.audit.LockEvent(ctx, AuditLockExpired, expired, nil)
		}
	}
	for _, st := range s.durable {
		if n, err := st.Reap(ctx); err == nil {
			total += n
		}
	}
	return total
}

// Reconcile runs the §25.4 leader-only reconciliation pass when a Postgres
// tier is configured: it reads the active Redis locks and Redis epoch,
// brings the Postgres epoch up to MAX, copies the Redis locks into
// Postgres, and resolves split-brain per the deterministic rule. Each
// resolved conflict raises the split-brain metric and emits the
// lock_split_brain_detected audit event. It is a no-op without a Postgres
// tier (nothing to reconcile to).
//
// spec: §25.4 lines 2226-2267.
func (s *Service) Reconcile(ctx context.Context) error {
	if s.pgRecon == nil {
		return nil
	}
	var redisEpoch uint64
	var redisLocks []Lock
	if s.redis != nil {
		if e, err := s.redis.Epoch(ctx); err == nil {
			redisEpoch = e
		}
		if locks, err := s.redis.ActiveLocks(ctx); err == nil {
			redisLocks = locks
		}
	}
	out, err := s.pgRecon.Reconcile(ctx, redisEpoch, redisLocks, time.Now().UTC())
	if err != nil {
		return err
	}
	// On recovery the Postgres epoch becomes the reconciled MAX; mirror it
	// back to Redis so both stores hold the same value (§25.4 line 2221).
	if s.redis != nil {
		_ = s.redis.SetEpoch(ctx, out.Epoch)
	}
	s.mu.Lock()
	if out.Epoch > s.epoch {
		s.epoch = out.Epoch
	}
	s.pgDown = false
	epoch := s.epoch
	s.mu.Unlock()
	s.metrics.SetOutageEpoch(epoch)
	remover, _ := s.redis.(redisLockRemover)
	for _, c := range out.Conflicts {
		// §25.4 line 2267: when the pre-outage Postgres holder wins over a
		// still-active post-outage Redis lock, the losing Redis lock is
		// removed so a later Postgres outage cannot resurface it. The losing
		// holder is separately notified with the split-brain 409 (recorded in
		// ops_lock_conflicts) on its next heartbeat/list/release.
		if remover != nil && c.Winner == "pre_outage" && c.LoserWasActive && c.LoserLockID != "" {
			_ = remover.RemoveByID(ctx, c.LoserLockID)
		}
		s.metrics.SplitBrainDetected(scopePattern(c.Scope))
		if s.audit != nil {
			s.audit.LockEvent(ctx, AuditLockSplitBrainDetected, Lock{Scope: c.Scope, AcquiredBy: c.WinnerHolder}, map[string]any{
				"winner":         c.Winner,
				"winnerHolder":   c.WinnerHolder,
				"loserHolder":    c.LoserHolder,
				"loserWasActive": c.LoserWasActive,
			})
		}
	}
	return nil
}

// memTier adapts *MemStore to the Store interface so the Tier 3 store
// participates in the uniform read/mutate path. The epoch passed to
// Acquire is synced onto the MemStore so an in-memory lock carries the
// current outage epoch.
type memTier struct{ m *MemStore }

func (t memTier) Tier() string { return StoreMemory }
func (t memTier) Acquire(ctx context.Context, req LockRequest, epoch uint64) (*Lock, error) {
	t.m.SetEpoch(epoch)
	return t.m.Acquire(ctx, req)
}
func (t memTier) List(ctx context.Context) ([]Lock, error) { return t.m.List(ctx) }
func (t memTier) Get(ctx context.Context, id string) (*Lock, error) {
	return t.m.Get(ctx, id)
}

func (t memTier) Extend(ctx context.Context, id string, ttl int, caller string) (*Lock, error) {
	return t.m.ExtendAs(ctx, id, ttl, caller)
}

func (t memTier) Release(ctx context.Context, id, caller string) error {
	return t.m.ReleaseAs(ctx, id, caller)
}

func (t memTier) Steal(ctx context.Context, id string, req StealRequest) (*Lock, error) {
	return t.m.Steal(ctx, id, req)
}
func (t memTier) Reap(_ context.Context) (int, error)     { return t.m.Reap(), nil }
func (t memTier) Epoch(_ context.Context) (uint64, error) { return t.m.Epoch(), nil }

// sortByScope orders a lock slice by scope for a deterministic response.
func sortByScope(locks []Lock) {
	for i := 1; i < len(locks); i++ {
		for j := i; j > 0 && locks[j-1].Scope > locks[j].Scope; j-- {
			locks[j-1], locks[j] = locks[j], locks[j-1]
		}
	}
}

// Compile-time guard that *Service satisfies the lock-service contract.
var _ RemediationLockService = (*Service)(nil)
