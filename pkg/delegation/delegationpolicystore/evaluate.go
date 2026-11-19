// SPDX-License-Identifier: MIT

package delegationpolicystore

// Candidate is a delegation target evaluated against a
// DelegationPolicy: a runtime, connector, or pool described by its
// identifier, type, and labels. The gateway builds a Candidate from
// the resolved target of a `delegate_task` call.
type Candidate struct {
	// ID is the candidate's identifier (runtime name, connector id,
	// or pool name).
	ID string

	// Type is the candidate's resource type — for example `agent` or
	// `connector`.
	Type string

	// Labels are the candidate's tags, matched against a Target's
	// MatchLabels.
	Labels map[string]string
}

// Matches reports whether c satisfies this §8.3 target. A target
// matches when the candidate carries every label in MatchLabels, its
// ID is among IDs (when IDs is non-empty), and its Type is among Types
// (when Types is non-empty). An empty target matches every candidate.
func (t Target) Matches(c Candidate) bool {
	for k, v := range t.MatchLabels {
		if c.Labels[k] != v {
			return false
		}
	}
	if len(t.IDs) > 0 && !inList(t.IDs, c.ID) {
		return false
	}
	if len(t.Types) > 0 && !inList(t.Types, c.Type) {
		return false
	}
	return true
}

// Evaluate applies the §8.3 tag-based rule set to a candidate and
// reports whether the delegation to that candidate is permitted.
//
// The rule set is an allow-list — it replaces the legacy
// `allowedRuntimes` / `allowedConnectors` / `allowedPools` runtime
// fields — so the default is deny: a candidate is permitted only when
// a rule with `allow: true` matches it. A matching rule with
// `allow: false` is an explicit deny that overrides any allow; the
// §8.3 least-privilege discipline ("restriction only, never
// expansion") makes deny-overrides the safe precedence.
func (p DelegationPolicy) Evaluate(c Candidate) bool {
	allowed := false
	for _, r := range p.Rules {
		if !r.Target.Matches(c) {
			continue
		}
		if !r.Allow {
			return false
		}
		allowed = true
	}
	return allowed
}

// inList reports whether v is present in list.
func inList(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
