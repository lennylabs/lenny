// SPDX-License-Identifier: MIT

package failopen

import (
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
)

// DefaultUserFailOpenFraction is the §12.4 line 222
// `quotaUserFailOpenFraction` default (0.25): a single user may consume at
// most 25% of the tenant's per-replica fail-open allocation.
const DefaultUserFailOpenFraction = 0.25

// UserFractionWeakThreshold is the §12.4 line 222 boundary at or above
// which the per-user fail-open cap is judged substantially weakened (the
// QuotaFailOpenUserFractionInoperative warning fires).
const UserFractionWeakThreshold = 0.5

// Backstop is the §12.4 lines 220-224 in-memory per-replica emergency
// counter the gateway applies to admission requests during a Redis outage.
// It counts requests per key within a fixed one-minute window (matching
// the §11.1 request-rate window) so a single user or tenant cannot send
// unbounded requests through one replica while the shared Redis counter is
// unreachable. The counters are per-replica (never shared), so the
// cluster-wide effective limit is N × the per-replica ceiling, which the
// cached-replica-count division bounds.
//
// spec: §12.4 lines 220-224 ("in-memory per-user request counter as a
// coarse emergency backstop").
type Backstop struct {
	mu  sync.Mutex
	win map[string]*minuteCount
	now func() time.Time
}

// minuteCount is a single key's count within the minute window it started.
type minuteCount struct {
	windowUnixMin int64
	count         int
}

// NewBackstop returns an empty Backstop. now overrides the clock; nil
// selects time.Now.
func NewBackstop(now func() time.Time) *Backstop {
	if now == nil {
		now = time.Now
	}
	return &Backstop{win: map[string]*minuteCount{}, now: now}
}

// Incr increments the per-minute counter for key and returns the running
// count within the current one-minute window. A new minute resets the
// counter so the in-memory backstop tracks the same window the §11.1
// per-minute limit uses.
func (b *Backstop) Incr(key string, now time.Time) int {
	min := now.Unix() / 60
	b.mu.Lock()
	defer b.mu.Unlock()
	mc := b.win[key]
	if mc == nil || mc.windowUnixMin != min {
		mc = &minuteCount{windowUnixMin: min}
		b.win[key] = mc
	}
	mc.count++
	return mc.count
}

// Reset clears every counter. The gateway calls it on the Redis-recovery
// edge so per-user fail-open counters do not bleed into the recovered
// window. spec: §12.4 line 222 ("Per-user fail-open counters are reset
// when Redis recovers").
func (b *Backstop) Reset() {
	b.mu.Lock()
	b.win = map[string]*minuteCount{}
	b.mu.Unlock()
}

// Sweep drops counters whose minute window has elapsed so a long-lived
// replica that saw a brief outage does not retain a map entry per key
// seen during the window forever. Callers invoke it opportunistically.
func (b *Backstop) Sweep(now time.Time) {
	cur := now.Unix() / 60
	b.mu.Lock()
	for k, mc := range b.win {
		if mc.windowUnixMin < cur {
			delete(b.win, k)
		}
	}
	b.mu.Unlock()
}

// Ceilings is the §12.4 line 222/224 per-replica fail-open allocation for
// one tenant: the per-tenant effective ceiling and the per-user ceiling
// derived from it.
type Ceilings struct {
	// Tenant is effective_ceiling = min(tenant_limit /
	// max(cached_replica_count, 1), per_replica_hard_cap).
	Tenant int64
	// User is per_user_failopen_ceiling = effective_ceiling *
	// userFailOpenFraction.
	User int64
}

// ComputeCeilings derives the §12.4 per-replica ceilings for a tenant
// whose configured per-window limit is tenantLimit, given the last-known
// cached replica count, the per-replica hard cap, and the user fraction.
// A non-positive perReplicaHardCap defaults to tenant_limit / 2 per §12.4
// line 224. A non-positive userFraction selects the 0.25 default.
//
// spec: §12.4 lines 222-224; §11.2 Maximum Overshoot Formula.
func ComputeCeilings(tenantLimit int64, cachedReplicaCount int, perReplicaHardCap int64, userFraction float64) Ceilings {
	if tenantLimit <= 0 {
		return Ceilings{}
	}
	cap := perReplicaHardCap
	if cap <= 0 {
		// spec: §12.4 line 224 — per_replica_hard_cap defaults to
		// tenant_limit / 2.
		cap = tenantLimit / 2
		if cap <= 0 {
			cap = 1
		}
	}
	if userFraction <= 0 {
		userFraction = DefaultUserFailOpenFraction
	}
	tenant := quota.FailOpenCeiling(tenantLimit, cachedReplicaCount, cap)
	return Ceilings{
		Tenant: tenant,
		User:   quota.PerUserFailOpenCeiling(tenant, userFraction),
	}
}
