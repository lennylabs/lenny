// SPDX-License-Identifier: MIT

// Package quota encodes the §11.2 budget-and-quota primitives that
// are pure arithmetic: the ResetPeriod enum, the SoftWarning / HardLimit
// threshold tests, the global → tenant → user hierarchical-check rule,
// and the §11.2 Maximum Overshoot Formula's fail-open ceiling.
//
// The Redis Lua scripts (storage_quota_reserve.lua, the per-replica
// fail-open accounting, the cumulative fail-open timer) live in later
// phases that wire this package into the gateway's Redis client. This
// package stays pure so the formulas can be unit-tested against the
// spec's worked numbers without a running Redis.
package quota

import "fmt"

// ResetPeriod is the §11.2 quota-reset-period enum.
type ResetPeriod string

const (
	ResetHourly  ResetPeriod = "hourly"
	ResetDaily   ResetPeriod = "daily"
	ResetMonthly ResetPeriod = "monthly"
	ResetRolling ResetPeriod = "rolling"
)

// AllResetPeriods returns the closed §11.2 enum.
func AllResetPeriods() []ResetPeriod {
	return []ResetPeriod{ResetHourly, ResetDaily, ResetMonthly, ResetRolling}
}

// IsValid reports whether p is one of the four §11.2 reset periods.
func (p ResetPeriod) IsValid() bool {
	for _, v := range AllResetPeriods() {
		if p == v {
			return true
		}
	}
	return false
}

// SoftWarningRatio is the §11.2 soft-warning trigger (80%).
const SoftWarningRatio = 0.80

// HardLimitRatio is the §11.2 hard-limit cap (100%).
const HardLimitRatio = 1.00

// State captures the outcome of a quota check.
type State string

const (
	// StateOK means used < 80% of limit; the request is admitted with
	// no signal.
	StateOK State = "ok"

	// StateSoftWarning means 80% ≤ used < 100% of limit; the request
	// is admitted and a soft-warning billing event is emitted per
	// §11.2.1.
	StateSoftWarning State = "soft_warning"

	// StateHardExceeded means used ≥ 100% of limit; the request is
	// rejected with QUOTA_EXCEEDED.
	StateHardExceeded State = "hard_exceeded"
)

// Check returns the State for a single (used, limit) pair. A
// non-positive limit is treated as unlimited and always returns
// StateOK. A negative used value is invalid and returns
// StateHardExceeded so callers fail closed.
func Check(used, limit int64) State {
	if limit <= 0 {
		return StateOK
	}
	if used < 0 {
		return StateHardExceeded
	}
	ratio := float64(used) / float64(limit)
	switch {
	case ratio >= HardLimitRatio:
		return StateHardExceeded
	case ratio >= SoftWarningRatio:
		return StateSoftWarning
	default:
		return StateOK
	}
}

// Hierarchy captures the §11.2 nested quota chain: global ⊇ tenant ⊇
// user. The spec says: "A user quota cannot exceed its tenant's quota".
// HierarchicalCheck enforces both the nesting rule (configuration) and
// the per-level usage rule (runtime).
type Hierarchy struct {
	// Global, Tenant, User are the configured limits at each scope.
	// 0 or negative means unlimited.
	Global int64
	Tenant int64
	User   int64
}

// Validate reports the §11.2 nesting violations on the limits. A user
// limit greater than its tenant's, or a tenant limit greater than the
// global's, is invalid configuration and is rejected at the admin
// boundary.
func (h Hierarchy) Validate() error {
	if h.User > 0 && h.Tenant > 0 && h.User > h.Tenant {
		return fmt.Errorf("quota: user limit %d exceeds tenant limit %d (§11.2 hierarchy)", h.User, h.Tenant)
	}
	if h.Tenant > 0 && h.Global > 0 && h.Tenant > h.Global {
		return fmt.Errorf("quota: tenant limit %d exceeds global limit %d (§11.2 hierarchy)", h.Tenant, h.Global)
	}
	return nil
}

// HierarchicalCheck returns the most-restrictive State across all
// three scopes. The returned scope identifies which level triggered
// the binding constraint, which the gateway uses for the
// QUOTA_EXCEEDED error's details.scope field.
type HierarchicalCheckResult struct {
	State State
	Scope string // "global" | "tenant" | "user" | ""
	Used  int64
	Limit int64
}

// HierarchicalCheck runs Check at each scope and returns the result
// matching the most restrictive state. The order of resolution is
// HardExceeded > SoftWarning > OK; ties resolve in scope order
// (user < tenant < global) so the closest-to-caller scope is named.
func HierarchicalCheck(globalUsed, tenantUsed, userUsed int64, h Hierarchy) HierarchicalCheckResult {
	scopes := []struct {
		name  string
		used  int64
		limit int64
	}{
		{"user", userUsed, h.User},
		{"tenant", tenantUsed, h.Tenant},
		{"global", globalUsed, h.Global},
	}

	worst := HierarchicalCheckResult{State: StateOK}
	worstRank := -1
	for _, s := range scopes {
		st := Check(s.used, s.limit)
		rank := stateRank(st)
		if rank > worstRank {
			worstRank = rank
			worst = HierarchicalCheckResult{State: st, Scope: s.name, Used: s.used, Limit: s.limit}
		}
	}
	if worst.State == StateOK {
		worst.Scope = ""
	}
	return worst
}

func stateRank(s State) int {
	switch s {
	case StateHardExceeded:
		return 2
	case StateSoftWarning:
		return 1
	default:
		return 0
	}
}

// FailOpenCeiling computes the per-replica fail-open ceiling from
// §11.2's Maximum Overshoot Formula:
//
//	effective_ceiling = min(tenant_limit / max(cached_replica_count, 1),
//	                        per_replica_hard_cap)
//
// A non-positive tenantLimit or cachedReplicaCount is treated as the
// safe lower bound (1) so callers do not divide by zero. A
// non-positive perReplicaHardCap means the cap is disabled; the
// tenant-slice term is used unmodified.
func FailOpenCeiling(tenantLimit int64, cachedReplicaCount int, perReplicaHardCap int64) int64 {
	if tenantLimit <= 0 {
		return 0
	}
	r := cachedReplicaCount
	if r <= 0 {
		r = 1
	}
	slice := tenantLimit / int64(r)
	if perReplicaHardCap <= 0 {
		return slice
	}
	if slice < perReplicaHardCap {
		return slice
	}
	return perReplicaHardCap
}

// PerUserFailOpenCeiling applies the §11.2 / §12.4 per-user fraction
// to the effective ceiling. The default userFailOpenFraction is 0.25;
// values >= 0.5 weaken the monopolization control to the point where
// the §16.5 QuotaFailOpenUserFractionInoperative alert fires.
func PerUserFailOpenCeiling(effectiveCeiling int64, userFailOpenFraction float64) int64 {
	if effectiveCeiling <= 0 || userFailOpenFraction <= 0 {
		return 0
	}
	if userFailOpenFraction > 1 {
		userFailOpenFraction = 1
	}
	return int64(float64(effectiveCeiling) * userFailOpenFraction)
}

// DefaultSyncIntervalSeconds is the §11.2 quotaSyncIntervalSeconds
// default: the cadence at which Redis quota counters are checkpointed
// to Postgres (and on which delegation budget counters are persisted).
// spec: §11.2 line 44 ("default: 30s").
const DefaultSyncIntervalSeconds = 30

// MinSyncIntervalSeconds is the §11.2 floor on quotaSyncIntervalSeconds.
// A higher-throughput tenant lowers the interval toward this floor to
// reduce crash-recovery overshoot exposure, but cannot go below it so
// the checkpoint write cannot busy-loop. spec: §11.2 line 44
// ("minimum: 10s").
const MinSyncIntervalSeconds = 10

// ClampSyncIntervalSeconds applies the §11.2 bounds to a configured
// quotaSyncIntervalSeconds: a non-positive value selects the 30s
// default, and a positive value below the 10s minimum is raised to the
// floor. Any value at or above the floor is returned unchanged.
// spec: §11.2 line 44 (default 30, minimum 10).
func ClampSyncIntervalSeconds(seconds int) int {
	if seconds <= 0 {
		return DefaultSyncIntervalSeconds
	}
	if seconds < MinSyncIntervalSeconds {
		return MinSyncIntervalSeconds
	}
	return seconds
}

// MaxOvershoot computes the §11.2 worst-case quota overshoot during
// normal operation: quotaSyncIntervalSeconds × max_tokens_per_second
// × active_sessions_per_tenant. Returns the bound in tokens.
func MaxOvershoot(quotaSyncIntervalSeconds int, maxTokensPerSecond float64, activeSessions int) float64 {
	if quotaSyncIntervalSeconds <= 0 || maxTokensPerSecond <= 0 || activeSessions <= 0 {
		return 0
	}
	return float64(quotaSyncIntervalSeconds) * maxTokensPerSecond * float64(activeSessions)
}

// ReconcileMax implements the §11.2 MAX rule on Redis recovery:
// restored_counter = MAX(in_memory_counter, postgres_checkpoint).
// Used during Redis recovery so a stale Postgres checkpoint cannot
// reset a counter below actual accumulated usage.
func ReconcileMax(inMemory, postgresCheckpoint int64) int64 {
	if inMemory > postgresCheckpoint {
		return inMemory
	}
	return postgresCheckpoint
}
