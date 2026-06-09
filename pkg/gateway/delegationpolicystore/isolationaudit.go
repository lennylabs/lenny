// SPDX-License-Identifier: MIT

package delegationpolicystore

import "sort"

// PoolCandidate pairs a pool's §8.3 delegation-match identity with its
// isolation profile for the §24.14 `policy audit-isolation` join. The
// gateway matches a delegation target by the runtime it resolves to
// (the delegation-policy filter builds the Candidate from the runtime's
// name, type, and labels), so the audit surfaces each pool as the
// Candidate of the runtime it runs, carrying the pool's own name and
// isolation rank for the monotonicity comparison.
//
// spec: §24.14 line 172 (rule × pool isolation audit); §8.3 line 346
// (isolation monotonicity: standard < sandboxed < microvm).
type PoolCandidate struct {
	// PoolName is the pool's identifier, reported in the violation rows.
	PoolName string
	// IsolationProfile is the pool's isolation profile string, reported
	// verbatim in the violation rows.
	IsolationProfile string
	// IsolationRank is the §8.3 ordering integer (standard=0,
	// sandboxed=1, microvm=2; -1 for an unknown profile). The caller
	// computes it via pkg/sandbox/isolation.Rank so this package stays
	// free of the isolation dependency.
	IsolationRank int
	// Candidate is the §8.3 match candidate the pool's runtime presents
	// to a DelegationPolicy rule.
	Candidate Candidate
}

// IsolationViolation is one §24.14 (rule, source pool, target pool)
// combination where a delegation from the source pool to the target
// pool would be rejected at runtime by the §8.3 isolation-monotonicity
// check (error code ISOLATION_MONOTONICITY_VIOLATED): both pools are
// matched by the same allow rule, the policy permits the target as a
// delegation target, and the source pool's isolation profile is
// strictly more restrictive than the target's.
type IsolationViolation struct {
	Policy        string
	TenantID      string
	RuleIndex     int
	SourcePool    string
	SourceProfile string
	TargetPool    string
	TargetProfile string
}

// AuditIsolation performs the §24.14 client-side join: for every
// DelegationPolicy allow rule, it pairs each pool the rule matches as a
// candidate delegation target against every other pool the same rule
// matches as a candidate parent, and reports the pairs where a
// delegation from the parent to the target would fail the §8.3
// monotonicity check (parent isolation strictly stronger than target
// isolation).
//
// Only allow rules are considered: a deny rule prevents the target from
// being a delegation target at all, so such a delegation is rejected by
// policy rather than by the isolation check. A target shadowed by a
// deny rule elsewhere in the same policy (the §8.3 deny-overrides-allow
// precedence) is excluded via the policy-level Evaluate guard, so the
// audit reports only combinations that would reach and fail the runtime
// monotonicity check rather than a policy denial.
//
// Results are sorted by (policy, tenant, rule index, source pool,
// target pool) so the report is stable across runs.
//
// spec: §24.14 line 172; §8.3 lines 346-352.
func AuditIsolation(policies []DelegationPolicy, pools []PoolCandidate) []IsolationViolation {
	var out []IsolationViolation
	for _, p := range policies {
		for i, r := range p.Rules {
			if !r.Allow {
				continue
			}
			var matched []PoolCandidate
			for _, pc := range pools {
				if r.Target.Matches(pc.Candidate) {
					matched = append(matched, pc)
				}
			}
			for _, target := range matched {
				// Target shadowed by a deny rule is rejected by policy,
				// not by the monotonicity check; skip it. An unknown
				// target profile cannot be ranked, so it cannot violate.
				if target.IsolationRank < 0 || !p.Evaluate(target.Candidate) {
					continue
				}
				for _, source := range matched {
					if source.PoolName == target.PoolName || source.IsolationRank < 0 {
						continue
					}
					// A parent strictly more restrictive than the target
					// fails §8.3: the child (target) would weaken
					// isolation, so the delegation is rejected.
					if source.IsolationRank > target.IsolationRank {
						out = append(out, IsolationViolation{
							Policy:        p.Name,
							TenantID:      p.TenantID,
							RuleIndex:     i,
							SourcePool:    source.PoolName,
							SourceProfile: source.IsolationProfile,
							TargetPool:    target.PoolName,
							TargetProfile: target.IsolationProfile,
						})
					}
				}
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		x, y := out[a], out[b]
		switch {
		case x.Policy != y.Policy:
			return x.Policy < y.Policy
		case x.TenantID != y.TenantID:
			return x.TenantID < y.TenantID
		case x.RuleIndex != y.RuleIndex:
			return x.RuleIndex < y.RuleIndex
		case x.SourcePool != y.SourcePool:
			return x.SourcePool < y.SourcePool
		default:
			return x.TargetPool < y.TargetPool
		}
	})
	return out
}

// ProactiveWarningsForPool runs the §8.3 line 350 proactive
// pool-registration audit for a single newly registered or updated pool:
// it returns the subset of the full monotonicity audit where poolName is
// the less-restrictive delegation target a more-restrictive parent pool
// could reach through the same allow rule. The gateway emits one
// pool.isolation_warning per returned violation after a pool create or
// update. pools must include the registered pool itself.
//
// spec: §8.3 lines 349-352 (proactive pool-registration enforcement).
func ProactiveWarningsForPool(policies []DelegationPolicy, pools []PoolCandidate, poolName string) []IsolationViolation {
	all := AuditIsolation(policies, pools)
	out := all[:0]
	for _, v := range all {
		if v.TargetPool == poolName {
			out = append(out, v)
		}
	}
	return out
}
