// SPDX-License-Identifier: MIT

// Package quotabudget implements the §12.4 line 268 in-memory quota
// budget enforcement mode (`quotaEnforcementMode: in_memory_reconciled`).
//
// In this mode each gateway replica maintains an in-memory per-tenant
// token-budget allocation drawn from Postgres rather than reading the
// volatile §12.4 Redis counters on the admission path. The mechanism the
// spec mandates:
//
//   - On the first request for a (tenant, reset period) window the replica
//     requests a budget slice from Postgres: 1/N of the tenant's remaining
//     budget, where N is the cached replica count (quota.DrawBudgetSlice).
//   - It decrements the slice locally per request — the recorder reports
//     the proxy-extracted token counts via Add, which never touches
//     Postgres on the hot path until a reconcile is due.
//   - It reconciles with Postgres periodically (default every 30s, the
//     quota.ClampSyncIntervalSeconds cadence) or when the local slice is
//     80% consumed (quota.BudgetSliceReconcileRatio), atomically folding
//     the locally-consumed delta into the durable token_usage_checkpoint
//     tenant rollup and redrawing a fresh slice from the updated remaining
//     budget.
//
// The result tolerates full Redis unavailability for quota enforcement
// with bounded overshoot: at most one slice per replica, because each
// replica holds at most remaining/N unflushed tokens between reconciles.
// The admission gate (the UsageReader adapter consumed by QuotaEvaluator)
// rejects once the persisted base plus the local consumption reaches the
// tenant limit, and fails closed when the slice is exhausted and Postgres
// is unreachable.
//
// This mode is scoped to the §12.4 line 268 per-tenant token budget. The
// per-user and platform-global scopes of the §11.2 hierarchy are not
// tracked here (they remain Redis-backed in the default mode); the
// adapter reports zero for those scopes so QuotaEvaluator gates only on
// the tenant budget when this mode is active.
//
// The tenant limit is resolved from the registry only when a reconcile is
// due (at most once per reconcile interval per tenant, plus the 80%
// crossings), never on the steady-state admission read, so enabling the
// mode does not add a per-request tenant-store lookup beyond the one the
// QuotaEvaluator already performs.
//
// spec: §12.4 line 268; §11.2 line 44 (sync interval, final reconciliation).
package quotabudget

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// CheckpointAdder atomically folds a replica's locally-consumed token
// delta into the durable per-tenant token_usage_checkpoint rollup row for
// one window and returns the resulting authoritative total. The add must
// be atomic across replicas (Postgres serializes the increment) so no
// replica's contribution is lost when several reconcile concurrently. A
// zero delta is a valid read of the current total (the startup slice
// draw). The Postgres implementation is the quotacheckpoint pgstore.
//
// spec: §12.4 line 268; §11.2 line 44.
type CheckpointAdder interface {
	AddTenantTotal(ctx context.Context, tenantID, period, windowLabel string, delta int64) (int64, error)
}

// ReplicaCounter reports the §12.4 line 224 cached replica count used as
// the divisor N in the 1/N slice. failopen.ReplicaCount satisfies it.
type ReplicaCounter interface {
	Get() int
}

// staticReplicas is the fallback ReplicaCounter for a deployment with no
// Kubernetes Endpoints poller (a single-process or Postgres-only gateway):
// it reports a fixed count, floored at 1.
type staticReplicas int

func (s staticReplicas) Get() int {
	if s < 1 {
		return 1
	}
	return int(s)
}

// StaticReplicaCount returns a ReplicaCounter reporting a fixed count. A
// non-positive value reports 1.
func StaticReplicaCount(n int) ReplicaCounter { return staticReplicas(n) }

// entryKeySep separates the tenant id and period in an entry key. A NUL
// byte cannot appear in a tenant id or a reset period.
const entryKeySep = "\x00"

// Options configures a Tracker.
type Options struct {
	// Limits resolves each tenant's §11.2 token budget. Required.
	Limits policy.TenantLimitLookup
	// Adder is the atomic Postgres checkpoint folder. Required.
	Adder CheckpointAdder
	// Replicas supplies the slice divisor N. Required; use
	// StaticReplicaCount(1) when no Endpoints poller is wired.
	Replicas ReplicaCounter
	// ReconcileInterval is the periodic reconcile cadence (§12.4 line 268
	// "default: every 30s"). A non-positive value selects the
	// quota.DefaultSyncIntervalSeconds cadence; a positive value below the
	// §11.2 floor is raised to it.
	ReconcileInterval time.Duration
	// Now overrides the clock for tests. Nil selects time.Now().UTC().
	Now func() time.Time
}

// Tracker holds the per-(tenant, period) in-memory budget slices for the
// in_memory_reconciled enforcement mode. It is safe for concurrent use.
type Tracker struct {
	limits   policy.TenantLimitLookup
	adder    CheckpointAdder
	replicas ReplicaCounter
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

// entry is one tenant's budget slice for a single reset period. The window
// label distinguishes the current window from a rolled-over one; on a
// label change the slice resets and is redrawn from a zero base.
type entry struct {
	mu          sync.Mutex
	windowLabel string
	base        int64 // persisted authoritative total at last reconcile
	consumed    int64 // local consumption since last reconcile (unflushed)
	slice       int64 // this replica's allocation for the current window
	limit       int64 // tenant limit resolved at last reconcile (≤0: unlimited)
	drawn       bool  // a slice has been drawn at least once for this window
	lastFlush   time.Time
}

// New returns a Tracker. It panics on a missing required seam so a
// misconfiguration surfaces at wiring time rather than admitting unmetered
// traffic.
func New(opts Options) *Tracker {
	if opts.Limits == nil || opts.Adder == nil || opts.Replicas == nil {
		panic("quotabudget: New requires Limits, Adder, and Replicas")
	}
	interval := opts.ReconcileInterval
	if interval <= 0 {
		interval = time.Duration(quota.DefaultSyncIntervalSeconds) * time.Second
	} else {
		interval = time.Duration(quota.ClampSyncIntervalSeconds(int(interval/time.Second))) * time.Second
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Tracker{
		limits:   opts.Limits,
		adder:    opts.Adder,
		replicas: opts.Replicas,
		interval: interval,
		now:      now,
		entries:  make(map[string]*entry),
	}
}

// entryFor returns the per-(tenant, period) entry, creating it on first
// use.
func (t *Tracker) entryFor(tenantID string, period quota.ResetPeriod) *entry {
	key := tenantID + entryKeySep + string(period)
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[key]
	if e == nil {
		e = &entry{}
		t.entries[key] = e
	}
	return e
}

// TenantUsed returns the effective per-tenant token usage for the window
// of period containing at, for the QuotaEvaluator's admission comparison:
// the persisted base plus this replica's local unflushed consumption. It
// lazily draws the startup slice and reconciles when the slice is due
// (uninitialised, the interval has elapsed, or the slice is 80%+
// consumed).
//
// When a reconcile is required but Postgres is unreachable, the call
// serves from the remaining slice headroom (bounded overshoot); it fails
// closed (returns an error) only when the slice is exhausted or was never
// drawn, so QuotaEvaluator's fail-closed handling rejects rather than
// admitting an unbounded budget. A rolling-period tenant is not enforced
// through this path (the in-memory budget is defined over fixed windows).
func (t *Tracker) TenantUsed(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) (int64, error) {
	label, err := quotastore.WindowLabel(period, at)
	if err != nil {
		return 0, nil
	}

	e := t.entryFor(tenantID, period)
	e.mu.Lock()
	defer e.mu.Unlock()

	t.rollWindowLocked(e, label)

	if t.needReconcileLocked(e) {
		limit, lErr := t.tenantLimit(ctx, tenantID)
		if lErr != nil {
			if t.mustFailClosedLocked(e) {
				return 0, fmt.Errorf("quotabudget: tenant %q limit lookup failed and budget slice is unavailable: %w", tenantID, lErr)
			}
		} else if rErr := t.reconcileLocked(ctx, tenantID, period, label, e, limit); rErr != nil {
			if t.mustFailClosedLocked(e) {
				return 0, fmt.Errorf("quotabudget: tenant %q budget slice exhausted and Postgres unreachable: %w", tenantID, rErr)
			}
		}
	}
	return e.base + e.consumed, nil
}

// Add folds tokens consumed by one upstream response into the replica's
// local slice for the (tenant, period) window. It is best-effort and runs
// on the response path: it never returns an error and only reaches
// Postgres when the 80%-consumed reconcile threshold is crossed, so a
// Postgres blip never fails the proxied call (the next admission read
// retries the reconcile). A non-positive token count is a no-op.
//
// spec: §12.4 line 268 ("decrements locally per request").
func (t *Tracker) Add(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time, tokens int64) {
	if tokens <= 0 {
		return
	}
	label, err := quotastore.WindowLabel(period, at)
	if err != nil {
		return
	}
	e := t.entryFor(tenantID, period)
	e.mu.Lock()
	defer e.mu.Unlock()
	t.rollWindowLocked(e, label)
	e.consumed += tokens
	// §12.4 line 268: reconcile when the local slice is 80% consumed. Only
	// a drawn, limited entry has a meaningful slice; the limit is re-read
	// only at this (infrequent) crossing, never per recorded response.
	if e.drawn && e.limit > 0 && t.sliceConsumedLocked(e) {
		if limit, lErr := t.tenantLimit(ctx, tenantID); lErr == nil && limit > 0 {
			_ = t.reconcileLocked(ctx, tenantID, period, label, e, limit)
		}
	}
}

// Flush folds every tenant's unflushed local consumption into Postgres.
// It is the §11.2 line 44 final-reconciliation hook, called on graceful
// shutdown so a replica's last slice is not lost. The first error
// encountered is returned; a partial flush still persists the entries that
// succeeded.
func (t *Tracker) Flush(ctx context.Context) error {
	t.mu.Lock()
	type pending struct {
		tenantID string
		period   quota.ResetPeriod
		e        *entry
	}
	var all []pending
	for key, e := range t.entries {
		tenantID, period, ok := splitEntryKey(key)
		if !ok {
			continue
		}
		all = append(all, pending{tenantID: tenantID, period: period, e: e})
	}
	t.mu.Unlock()

	var firstErr error
	for _, p := range all {
		p.e.mu.Lock()
		if p.e.consumed > 0 && p.e.windowLabel != "" && p.e.limit > 0 {
			if rErr := t.reconcileLocked(ctx, p.tenantID, p.period, p.e.windowLabel, p.e, p.e.limit); rErr != nil && firstErr == nil {
				firstErr = rErr
			}
		}
		p.e.mu.Unlock()
	}
	return firstErr
}

// Run drives the §12.4 line 268 periodic reconcile ("reconciles with
// Postgres periodically (default: every 30s)"): every interval it folds
// each tenant's unflushed local consumption into Postgres via Flush, so a
// low-traffic tenant whose admission reads do not themselves trigger the
// interval check still persists its slice on the cadence. It blocks until
// ctx is cancelled, then runs a final Flush so a graceful shutdown loses
// no consumption.
func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), t.interval)
			_ = t.Flush(flushCtx)
			cancel()
			return
		case <-ticker.C:
			_ = t.Flush(ctx)
		}
	}
}

// rollWindowLocked resets the entry when the window label has changed (a
// new hour/day/month), so the budget resets at the window boundary the
// same way the Redis counter's TTL would expire. e.mu must be held.
func (t *Tracker) rollWindowLocked(e *entry, label string) {
	if e.windowLabel == label {
		return
	}
	e.windowLabel = label
	e.base = 0
	e.consumed = 0
	e.slice = 0
	e.limit = 0
	e.drawn = false
	e.lastFlush = time.Time{}
}

// needReconcileLocked reports whether a reconcile is due: the slice was
// never drawn, the periodic interval has elapsed since the last flush, or
// (for a limited tenant) the slice is 80%+ consumed. e.mu must be held.
func (t *Tracker) needReconcileLocked(e *entry) bool {
	if !e.drawn {
		return true
	}
	if t.now().Sub(e.lastFlush) >= t.interval {
		return true
	}
	return e.limit > 0 && t.sliceConsumedLocked(e)
}

// sliceConsumedLocked reports whether the local consumption has reached
// the §12.4 80% reconcile threshold of the slice. A zero slice (the tenant
// is at/near its limit) always reconciles so admission reads the freshest
// authoritative base. e.mu must be held.
func (t *Tracker) sliceConsumedLocked(e *entry) bool {
	if e.slice <= 0 {
		return true
	}
	return float64(e.consumed) >= quota.BudgetSliceReconcileRatio*float64(e.slice)
}

// mustFailClosedLocked reports whether a failed reconcile must reject the
// request: when no slice was ever drawn, or a limited tenant's slice is
// exhausted. Otherwise the remaining slice headroom bounds overshoot and
// the request is served best-effort. e.mu must be held.
func (t *Tracker) mustFailClosedLocked(e *entry) bool {
	if !e.drawn {
		return true
	}
	return e.limit > 0 && e.consumed >= e.slice
}

// reconcileLocked folds the entry's local consumption into Postgres
// atomically, then redraws the slice from the updated remaining budget and
// resets the local counter. An unlimited tenant (limit ≤ 0) writes no
// checkpoint row and is left unenforced. On a Postgres error the entry's
// base/consumed/slice are left unchanged (consumption stays local for the
// next attempt). e.mu must be held.
func (t *Tracker) reconcileLocked(ctx context.Context, tenantID string, period quota.ResetPeriod, label string, e *entry, limit int64) error {
	e.limit = limit
	if limit <= 0 {
		// Unlimited tenant: nothing to enforce, no Postgres row to keep.
		e.base = 0
		e.consumed = 0
		e.slice = 0
		e.drawn = true
		e.lastFlush = t.now()
		return nil
	}
	delta := e.consumed
	newTotal, err := t.adder.AddTenantTotal(ctx, tenantID, string(period), label, delta)
	if err != nil {
		return err
	}
	e.base = newTotal
	e.consumed = 0
	e.slice = quota.DrawBudgetSlice(limit-newTotal, t.replicas.Get())
	e.drawn = true
	e.lastFlush = t.now()
	return nil
}

// tenantLimit resolves the tenant's per-window token limit. An unknown
// tenant surfaces policy.ErrTenantNotFound so the caller fails closed.
func (t *Tracker) tenantLimit(ctx context.Context, tenantID string) (int64, error) {
	lim, err := t.limits.LookupLimits(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	return lim.Tenant, nil
}

// splitEntryKey reverses entryFor's key composition.
func splitEntryKey(key string) (tenantID string, period quota.ResetPeriod, ok bool) {
	i := strings.IndexByte(key, 0)
	if i < 0 {
		return "", "", false
	}
	return key[:i], quota.ResetPeriod(key[i+1:]), true
}
