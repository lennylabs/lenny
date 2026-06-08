// SPDX-License-Identifier: MIT

package quota

import "fmt"

// EnforcementMode selects how the gateway enforces token quota counters.
// spec: §12.4 line 268 ("In-memory quota budgets with Postgres
// reconciliation ... deployers enable it via `quotaEnforcementMode:
// in_memory_reconciled`").
type EnforcementMode string

const (
	// EnforcementModeRedis is the default: the QuotaEvaluator reads the
	// §12.4 Redis token-usage counters on the admission path and the
	// recorder writes them on each upstream response, with the §11.2 line
	// 44 Postgres checkpoint providing durability. spec: §12.4 (the
	// counters under t:{tenant_id}:quota:tokens:{user_id}:{window}).
	EnforcementModeRedis EnforcementMode = "redis"

	// EnforcementModeInMemoryReconciled selects the §12.4 line 268
	// per-replica in-memory budget allocation drawn from Postgres: each
	// gateway replica requests a budget slice from Postgres on startup
	// (1/N of the tenant's remaining budget, where N is the replica
	// count), decrements it locally per request, and reconciles with
	// Postgres periodically or when the slice is 80% consumed. It is not
	// the default; deployers select it when Redis-based quota drift during
	// outages is unacceptable.
	EnforcementModeInMemoryReconciled EnforcementMode = "in_memory_reconciled"
)

// DefaultEnforcementMode is the §12.4 default: Redis-backed counters. The
// in-memory reconciled mode is opt-in per the spec.
const DefaultEnforcementMode = EnforcementModeRedis

// AllEnforcementModes returns the closed §12.4 enforcement-mode enum.
func AllEnforcementModes() []EnforcementMode {
	return []EnforcementMode{EnforcementModeRedis, EnforcementModeInMemoryReconciled}
}

// IsValid reports whether m is one of the §12.4 enforcement modes.
func (m EnforcementMode) IsValid() bool {
	for _, v := range AllEnforcementModes() {
		if m == v {
			return true
		}
	}
	return false
}

// ParseEnforcementMode resolves the configured quotaEnforcementMode
// string. An empty value selects the default (Redis); any other value
// outside the closed enum is a configuration error so the gateway can
// fail closed at startup rather than silently fall back.
// spec: §12.4 line 268.
func ParseEnforcementMode(s string) (EnforcementMode, error) {
	if s == "" {
		return DefaultEnforcementMode, nil
	}
	m := EnforcementMode(s)
	if !m.IsValid() {
		return "", fmt.Errorf("quota: invalid quotaEnforcementMode %q (want one of %q, %q)",
			s, EnforcementModeRedis, EnforcementModeInMemoryReconciled)
	}
	return m, nil
}
