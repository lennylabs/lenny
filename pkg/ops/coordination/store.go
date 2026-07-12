// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrStoreUnavailable signals that a remediation-lock Store's backend is
// unreachable. The tiered Service treats it as "fall to the next tier"
// on the acquire path and "skip this store" on the query path, matching
// the §25.4 tiered-storage fallback contract (Postgres → Redis →
// in-memory). Any other error from a Store is a real failure — a
// coordination *Error (conflict, not-owned) propagates to the caller
// unchanged.
//
// spec: §25.4 lines 2164-2172.
var ErrStoreUnavailable = errors.New("remediation-lock store unavailable")

// Store is one §25.4 remediation-lock storage tier. The Service composes
// the durable Postgres (Tier 1) and Redis (Tier 2) stores over the
// always-present in-memory MemStore (Tier 3), attempting each in order so
// an acquire can always reach a consensus tier while one is reachable.
//
// Every tier authors acquiredAt and expiresAt from its own clock (Tier 1
// Postgres now(), Tier 2 Redis TIME, Tier 3 the process clock — §25.4
// "Clock Source"), so the caller passes ttlSeconds, never an expiry. The
// acquire path is a compare-and-set on scope with no check-then-write
// window: a non-expired lock on the same scope yields a *Error with
// ErrCodeConflict; a backend outage yields ErrStoreUnavailable.
//
// spec: §25.4 lines 2164-2202.
type Store interface {
	// Tier returns this store's §25.4 lockStore label (StorePostgres,
	// StoreRedis, or StoreMemory).
	Tier() string
	// Acquire creates a lock on req.Scope stamped with epoch (Tier 1 always
	// passes 0; Tier 2 and Tier 3 pass the current outage epoch). It returns
	// a *Error{ErrCodeConflict} when a non-expired lock holds the scope and
	// ErrStoreUnavailable when the backend is unreachable.
	Acquire(ctx context.Context, req LockRequest, epoch uint64) (*Lock, error)
	// List returns every active (non-expired) lock in this tier.
	List(ctx context.Context) ([]Lock, error)
	// Get returns a single lock by id, or *Error{ErrCodeNotFound}.
	Get(ctx context.Context, lockID string) (*Lock, error)
	// Extend renews a lock's TTL to the tier clock + ttlSeconds. caller MUST
	// equal the current acquiredBy (*Error{ErrCodeNotOwned} otherwise).
	Extend(ctx context.Context, lockID string, ttlSeconds int, caller string) (*Lock, error)
	// Release frees a lock. caller MUST equal the current acquiredBy.
	Release(ctx context.Context, lockID, caller string) error
	// Steal takes over a lock: caller becomes acquiredBy, the prior holder
	// is recorded in stolenFrom, and revision increments.
	Steal(ctx context.Context, lockID string, req StealRequest) (*Lock, error)
	// Reap drops every expired lock and returns the count removed (§25.4
	// "expired rows are cleaned up ... by a periodic goroutine (every 60s)").
	Reap(ctx context.Context) (int, error)
	// Epoch returns this tier's stored §25.4 outage epoch.
	Epoch(ctx context.Context) (uint64, error)
}

// EpochManager mutates a tier's §25.4 outage-epoch counter. Both the
// Postgres and Redis tiers store the epoch (§25.4 line 2218: "stored in
// both Postgres and Redis"); the Service increments the Redis side on a
// Postgres→Tier-2 transition and writes the reconciled MAX back to
// Postgres on recovery.
type EpochManager interface {
	// IncrementEpoch advances the counter by one and returns the new value.
	IncrementEpoch(ctx context.Context) (uint64, error)
	// SetEpoch writes v as the counter when v exceeds the stored value
	// (the §25.4 MAX reconciliation never moves the epoch backward).
	SetEpoch(ctx context.Context, v uint64) error
}

// ReconcileOutcome reports the result of a §25.4 Postgres reconciliation
// pass: the epoch the tiers were brought into sync at, the Redis locks
// copied into Postgres, and the split-brain conflicts resolved.
type ReconcileOutcome struct {
	// Epoch is the reconciled MAX(redis_epoch, postgres_epoch) written back
	// to ops_lock_epoch.
	Epoch uint64
	// Copied is the number of Redis-tier locks inserted into Postgres
	// (scopes with no pre-outage Postgres lock).
	Copied int
	// Conflicts is the resolved split-brain set: one entry per scope held
	// by both a pre-outage Postgres lock and a post-outage Redis lock.
	Conflicts []SplitBrainConflict
}

// SplitBrainConflict records one §25.4 deterministic split-brain
// resolution: a scope held by a pre-outage Postgres lock and a
// post-outage Redis lock at reconciliation time. It mirrors a row written
// to ops_lock_conflicts.
//
// spec: §25.4 lines 2255-2267.
type SplitBrainConflict struct {
	Scope          string
	Winner         string // "pre_outage" | "post_outage"
	WinnerHolder   string // the acquiredBy that retained the lock
	LoserHolder    string // the acquiredBy whose lock was removed
	LoserLockID    string // the id of the losing lock (the removed Redis lock when pre_outage wins)
	LoserWasActive bool   // the loser was non-expired at resolution time
}

// reconcilingStore is a Tier 1 (Postgres) store that additionally manages
// the outage epoch and runs the reconciliation pass. Only the Postgres
// store implements it; the Service type-asserts for it.
type reconcilingStore interface {
	Store
	EpochManager
	// ActiveLocks returns every non-expired lock for the reconciliation
	// snapshot.
	ActiveLocks(ctx context.Context) ([]Lock, error)
	// Reconcile runs the §25.4 single-transaction reconciliation pass while
	// holding pg_advisory_xact_lock: it writes MAX(redisEpoch, pgEpoch) back
	// to ops_lock_epoch, copies the active Redis locks into Postgres, and
	// resolves split-brain per the deterministic rule, recording each
	// conflict in ops_lock_conflicts. now is the resolution-time clock used
	// for the expired-by-clock test.
	Reconcile(ctx context.Context, redisEpoch uint64, redisLocks []Lock, now time.Time) (ReconcileOutcome, error)
}

// epochStore is a Tier 2 (Redis) store that manages the outage epoch and
// exposes its active locks for reconciliation. Only the Redis store
// implements it.
type epochStore interface {
	Store
	EpochManager
	ActiveLocks(ctx context.Context) ([]Lock, error)
}

// redisLockRemover is an optional Tier 2 (Redis) capability: it removes a
// lock by id regardless of holder. The Service type-asserts for it during
// reconciliation to drop a losing split-brain Redis lock (§25.4 line 2267,
// "The Redis lock is removed"). Only the Redis store implements it.
type redisLockRemover interface {
	RemoveByID(ctx context.Context, lockID string) error
}

// Metrics receives the §25.4 remediation-lock metric signals. A nil
// Metrics drops every signal; cmd/lenny-ops wires a Prometheus-backed
// implementation so the documented gauges and counters carry data.
//
// spec: §25.4 lines 2328-2336.
type Metrics interface {
	// SetActiveStore raises lenny_ops_lock_store_active{store} for the
	// currently serving tier and clears the others.
	SetActiveStore(store string)
	// SetOutageEpoch publishes lenny_ops_lock_outage_epoch.
	SetOutageEpoch(epoch uint64)
	// SplitBrainDetected increments
	// lenny_ops_lock_split_brain_detected_total{scope_pattern}.
	SplitBrainDetected(scopePattern string)
	// StealDone increments lenny_ops_lock_steal_total{scope_pattern}.
	StealDone(scopePattern string)
	// SetClockSkew publishes lenny_ops_clock_skew_seconds{pair}.
	SetClockSkew(pair string, seconds float64)
}

// NoopMetrics is the default Metrics that drops every signal.
type NoopMetrics struct{}

func (NoopMetrics) SetActiveStore(string)        {}
func (NoopMetrics) SetOutageEpoch(uint64)        {}
func (NoopMetrics) SplitBrainDetected(string)    {}
func (NoopMetrics) StealDone(string)             {}
func (NoopMetrics) SetClockSkew(string, float64) {}

// AuditSink receives the §25.4 remediation-lock audit events. A nil sink
// drops the event; cmd/lenny-ops wires an implementation that emits onto
// the operational-event stream and the durable audit store.
//
// spec: §25.4 lines 2338-2340.
type AuditSink interface {
	// LockEvent records one lock audit event (remediation.lock_acquired,
	// _extended, _released, _stolen, _expired, or _split_brain_detected).
	LockEvent(ctx context.Context, event string, lock Lock, detail map[string]any)
}

// §25.4 line 2338 audit event names.
const (
	AuditLockAcquired           = "remediation.lock_acquired"
	AuditLockExtended           = "remediation.lock_extended"
	AuditLockReleased           = "remediation.lock_released"
	AuditLockExpired            = "remediation.lock_expired"
	AuditLockStolen             = "remediation.lock_stolen"
	AuditLockSplitBrainDetected = "remediation.lock_split_brain_detected"
)

// scopePattern reduces a concrete lock scope to its §25.4 scope-pattern
// label for the per-pattern metrics (lenny_ops_lock_steal_total,
// _split_brain_detected_total). It keeps the first segment and replaces a
// concrete name with "{name}" so the label cardinality stays bounded:
// "pool:default-gvisor" → "pool:{name}", "platform:*" → "platform:*".
//
// spec: §25.4 lines 2114-2120.
func scopePattern(scope string) string {
	if scope == "" {
		return "unknown"
	}
	head, rest, found := strings.Cut(scope, ":")
	if !found {
		return scope
	}
	switch head {
	case "platform", "upgrade", "restore", "config":
		// Platform-global scopes are already low-cardinality literals.
		return scope
	case "tenant":
		return "tenant:{tenantID}:*"
	default:
		if rest == "" {
			return head
		}
		return head + ":{name}"
	}
}
