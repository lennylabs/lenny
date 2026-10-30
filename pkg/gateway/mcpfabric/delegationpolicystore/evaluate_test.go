// SPDX-License-Identifier: MIT

package delegationpolicystore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
)

// spec: §8.3 DelegationPolicy tag-based matching evaluated at
// delegation time.

func TestTargetMatchesLabels(t *testing.T) {
	target := delegationpolicystore.Target{
		MatchLabels: map[string]string{"team": "platform"},
	}
	if !target.Matches(delegationpolicystore.Candidate{
		Labels: map[string]string{"team": "platform", "tier": "gold"},
	}) {
		t.Error("a candidate carrying the required label must match")
	}
	if target.Matches(delegationpolicystore.Candidate{
		Labels: map[string]string{"team": "research"},
	}) {
		t.Error("a candidate with a different label value must not match")
	}
	if target.Matches(delegationpolicystore.Candidate{}) {
		t.Error("a candidate missing the required label must not match")
	}
}

func TestTargetMatchesIDsAndTypes(t *testing.T) {
	target := delegationpolicystore.Target{
		IDs:   []string{"github", "jira"},
		Types: []string{"connector"},
	}
	if !target.Matches(delegationpolicystore.Candidate{ID: "github", Type: "connector"}) {
		t.Error("a candidate with a listed id and type must match")
	}
	if target.Matches(delegationpolicystore.Candidate{ID: "slack", Type: "connector"}) {
		t.Error("a candidate whose id is not listed must not match")
	}
	if target.Matches(delegationpolicystore.Candidate{ID: "github", Type: "agent"}) {
		t.Error("a candidate whose type is not listed must not match")
	}
}

func TestEmptyTargetMatchesEverything(t *testing.T) {
	var target delegationpolicystore.Target
	if !target.Matches(delegationpolicystore.Candidate{ID: "anything", Type: "agent"}) {
		t.Error("an empty target must match every candidate")
	}
}

func TestEvaluateDefaultDeny(t *testing.T) {
	// A policy with no matching rule denies — the rule set is an
	// allow-list.
	p := delegationpolicystore.DelegationPolicy{
		Rules: []delegationpolicystore.Rule{
			{Target: delegationpolicystore.Target{Types: []string{"connector"}}, Allow: true},
		},
	}
	if p.Evaluate(delegationpolicystore.Candidate{ID: "claude", Type: "agent"}) {
		t.Error("a candidate matched by no rule must be denied")
	}
}

func TestEvaluateAllowsMatchedCandidate(t *testing.T) {
	p := delegationpolicystore.DelegationPolicy{
		Rules: []delegationpolicystore.Rule{
			{
				Target: delegationpolicystore.Target{
					MatchLabels: map[string]string{"team": "platform"},
					Types:       []string{"agent"},
				},
				Allow: true,
			},
		},
	}
	allowed := p.Evaluate(delegationpolicystore.Candidate{
		ID:     "claude-worker",
		Type:   "agent",
		Labels: map[string]string{"team": "platform"},
	})
	if !allowed {
		t.Error("a candidate matched by an allow rule must be permitted")
	}
}

func TestEvaluateDenyOverridesAllow(t *testing.T) {
	// §8.3 least-privilege: a matching deny rule overrides a matching
	// allow rule regardless of order.
	p := delegationpolicystore.DelegationPolicy{
		Rules: []delegationpolicystore.Rule{
			{Target: delegationpolicystore.Target{Types: []string{"agent"}}, Allow: true},
			{Target: delegationpolicystore.Target{IDs: []string{"untrusted"}}, Allow: false},
		},
	}
	if p.Evaluate(delegationpolicystore.Candidate{ID: "untrusted", Type: "agent"}) {
		t.Error("a candidate matched by a deny rule must be denied even when an allow rule also matches")
	}
	if !p.Evaluate(delegationpolicystore.Candidate{ID: "trusted", Type: "agent"}) {
		t.Error("a candidate matched only by the allow rule must be permitted")
	}
}

func TestEvaluateEmptyPolicyDeniesAll(t *testing.T) {
	var p delegationpolicystore.DelegationPolicy
	if p.Evaluate(delegationpolicystore.Candidate{ID: "claude", Type: "agent"}) {
		t.Error("a policy with no rules must deny every candidate")
	}
}
