// SPDX-License-Identifier: MIT

package poolscaling

// CapacityPlanningDefaultsWarning is the §16.5 line 601 warning the
// PoolScalingController logs when a Tier 2 or Tier 3 deployment runs the
// workload-profile values at their defaults. The text is transcribed
// verbatim from the spec so log scrapers can match on it. spec:
// spec/16_observability.md line 601.
const CapacityPlanningDefaultsWarning = "[WARN] capacityPlanning Helm values are at defaults — review Section 16.5 workload profile table and substitute observed values before production promotion"

// CapacityPlanning carries the §16.5 workload-profile assumptions the
// tier sizing formulas (warm pool sizing, Redis ops budgets, gateway
// replica counts) depend on. The PoolScalingController reads these from
// the capacityPlanning.* Helm values at startup. spec: §16.5 lines
// 590-601.
type CapacityPlanning struct {
	// AvgSessionDurationSeconds feeds the Little's Law warm-pool claim
	// rate and the delegation-budget Redis ops/s estimate. Default 333.
	AvgSessionDurationSeconds float64
	// DelegationParticipationRate is the fraction of sessions that
	// perform at least one delegation. Default 0.05.
	DelegationParticipationRate float64
	// AvgDelegationsPerDelegatingSession scales the budget_reserve.lua
	// invocation rate. Default 10.
	AvgDelegationsPerDelegatingSession float64
	// AvgChildSessionSeconds is the average child-delegation duration,
	// used in the delegation fan-out minWarm formula. Default 60.
	AvgChildSessionSeconds float64
	// AvgWorkspaceSizeMB feeds checkpoint bandwidth and MinIO throughput
	// budgets. Default 100.
	AvgWorkspaceSizeMB float64
	// SessionIdleFraction is the fraction of active sessions with no
	// active LLM call; it scales the LLM Proxy concurrency estimate.
	// Default 0.30.
	SessionIdleFraction float64
}

// DefaultCapacityPlanning returns the §16.5 lines 594-599 default
// workload profile.
func DefaultCapacityPlanning() CapacityPlanning {
	return CapacityPlanning{
		AvgSessionDurationSeconds:          333,
		DelegationParticipationRate:        0.05,
		AvgDelegationsPerDelegatingSession: 10,
		AvgChildSessionSeconds:             60,
		AvgWorkspaceSizeMB:                 100,
		SessionIdleFraction:                0.30,
	}
}

// AtDefaults reports whether the profile is unchanged from the §16.5
// default workload profile. Each field is compared against the default
// literal; an operator who substitutes any observed value clears the
// flag. spec: §16.5 line 601.
func (c CapacityPlanning) AtDefaults() bool {
	return c == DefaultCapacityPlanning()
}

// ShouldWarnCapacityPlanningDefaults reports whether the
// PoolScalingController must log CapacityPlanningDefaultsWarning: the
// workload profile is at its defaults and the deployment is a
// production tier (Tier 2 or Tier 3). Tier 1 is single-node development
// and CI, so unsubstituted defaults there are expected and not warned.
// spec: §16.5 line 601.
func ShouldWarnCapacityPlanningDefaults(c CapacityPlanning, tier string) bool {
	switch tier {
	case "tier2", "tier3":
		return c.AtDefaults()
	default:
		return false
	}
}

// agentTierDefaultSafetyFactor and agentTier3SafetyFactor are the §17.8.2
// normative per-tier agent-type safety_factor defaults: 1.5 at Tier 1/2,
// 1.2 at Tier 3. The Tier 3 override is applied because per-pool demand is
// larger, allowing a thinner fractional buffer to achieve the same
// absolute headroom. spec: spec/17_deployment-topology.md line 1008.
const (
	agentTierDefaultSafetyFactor = 1.5
	agentTier3SafetyFactor       = 1.2
)

// DefaultSafetyFactorForTier returns the §17.8.2 normative agent-type
// safety_factor default for the deployment tier: 1.2 at Tier 3, 1.5
// otherwise. The PoolScalingController applies this automatically when
// capacityPlanning.tier is set and the operator has not pinned
// poolScaling.safetyFactor explicitly; a pinned value or a per-pool
// SafetyFactor always wins. mcp-type pools whose normative default
// differs (2.0 at Tier 1/2, 1.5 at Tier 3) set the per-pool SafetyFactor
// explicitly — the controller applies one default value per deployment,
// consistent with the §17.8.2 "the controller applies one safety_factor
// value per pool" statement. spec: spec/17_deployment-topology.md lines
// 1008, 1010; spec/04_system-components.md line 523.
func DefaultSafetyFactorForTier(tier string) float64 {
	if tier == "tier3" {
		return agentTier3SafetyFactor
	}
	return agentTierDefaultSafetyFactor
}
