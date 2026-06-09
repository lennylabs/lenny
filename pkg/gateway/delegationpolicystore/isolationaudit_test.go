// SPDX-License-Identifier: MIT

package delegationpolicystore

import "testing"

// ranks mirror pkg/sandbox/isolation.Rank so the audit tests do not pull
// in the isolation package: standard=0, sandboxed=1, microvm=2.
const (
	rankStandard  = 0
	rankSandboxed = 1
	rankMicrovm   = 2
)

func pc(name, profile string, rank int, c Candidate) PoolCandidate {
	return PoolCandidate{PoolName: name, IsolationProfile: profile, IsolationRank: rank, Candidate: c}
}

// TestAuditIsolation_ParentStrongerThanTarget_spec_24_14 verifies that a
// single allow rule matching two pools reports the (stronger-parent,
// weaker-target) pair and not the reverse.
func TestAuditIsolation_ParentStrongerThanTarget_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:     "team-agents",
		TenantID: "acme",
		Rules:    []Rule{{Target: Target{Types: []string{"agent"}}, Allow: true}},
	}}
	pools := []PoolCandidate{
		pc("kata-pool", "microvm", rankMicrovm, Candidate{ID: "coder", Type: "agent"}),
		pc("runc-pool", "standard", rankStandard, Candidate{ID: "chat", Type: "agent"}),
	}
	got := AuditIsolation(policies, pools)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.SourcePool != "kata-pool" || v.SourceProfile != "microvm" {
		t.Errorf("source: want kata-pool/microvm, got %s/%s", v.SourcePool, v.SourceProfile)
	}
	if v.TargetPool != "runc-pool" || v.TargetProfile != "standard" {
		t.Errorf("target: want runc-pool/standard, got %s/%s", v.TargetPool, v.TargetProfile)
	}
	if v.Policy != "team-agents" || v.TenantID != "acme" || v.RuleIndex != 0 {
		t.Errorf("identity: got policy=%s tenant=%s rule=%d", v.Policy, v.TenantID, v.RuleIndex)
	}
}

// TestAuditIsolation_EqualOrStricterTarget_spec_24_14 verifies that a
// target at least as restrictive as every matched parent never violates.
func TestAuditIsolation_EqualOrStricterTarget_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:  "p",
		Rules: []Rule{{Target: Target{}, Allow: true}}, // empty target matches all
	}}
	pools := []PoolCandidate{
		pc("a", "standard", rankStandard, Candidate{ID: "a"}),
		pc("b", "sandboxed", rankSandboxed, Candidate{ID: "b"}),
		pc("c", "microvm", rankMicrovm, Candidate{ID: "c"}),
	}
	got := AuditIsolation(policies, pools)
	// Every ordered (stronger→weaker) pair under the all-matching rule is
	// a violation: micro→standard, micro→sandboxed, sandboxed→standard.
	if len(got) != 3 {
		t.Fatalf("want 3 violations from the strength ladder, got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if v.SourceProfile == v.TargetProfile {
			t.Errorf("equal-profile pair reported: %+v", v)
		}
	}
}

// TestAuditIsolation_DenyRuleNotAudited_spec_24_14 verifies that a deny
// rule never produces a monotonicity violation — a denied target is
// rejected by policy, not by the isolation check.
func TestAuditIsolation_DenyRuleNotAudited_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:  "deny-only",
		Rules: []Rule{{Target: Target{Types: []string{"agent"}}, Allow: false}},
	}}
	pools := []PoolCandidate{
		pc("kata", "microvm", rankMicrovm, Candidate{ID: "coder", Type: "agent"}),
		pc("runc", "standard", rankStandard, Candidate{ID: "chat", Type: "agent"}),
	}
	if got := AuditIsolation(policies, pools); len(got) != 0 {
		t.Fatalf("deny rule should yield no violations, got %d: %+v", len(got), got)
	}
}

// TestAuditIsolation_DenyOverridesAllow_spec_24_14 verifies the §8.3
// deny-overrides-allow precedence: a target an allow rule matches but a
// later deny rule shadows is excluded, while a non-shadowed weaker pool
// matched by the same allow rule still violates.
func TestAuditIsolation_DenyOverridesAllow_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name: "mixed",
		Rules: []Rule{
			{Target: Target{Types: []string{"agent"}}, Allow: true},
			{Target: Target{IDs: []string{"chat"}}, Allow: false}, // shadow the runc target
		},
	}}
	pools := []PoolCandidate{
		pc("kata", "microvm", rankMicrovm, Candidate{ID: "coder", Type: "agent"}),
		pc("runc", "standard", rankStandard, Candidate{ID: "chat", Type: "agent"}),
		pc("gvisor", "sandboxed", rankSandboxed, Candidate{ID: "build", Type: "agent"}),
	}
	got := AuditIsolation(policies, pools)
	// runc (chat) is denied, so kata→runc is not reported; kata→gvisor is.
	if len(got) != 1 {
		t.Fatalf("want 1 violation (kata→gvisor), got %d: %+v", len(got), got)
	}
	if got[0].TargetPool != "gvisor" {
		t.Errorf("want target gvisor (runc is deny-shadowed), got %s", got[0].TargetPool)
	}
}

// TestAuditIsolation_UnknownProfileSkipped_spec_24_14 verifies that a
// pool with an unrankable isolation profile neither violates as a target
// nor as a source.
func TestAuditIsolation_UnknownProfileSkipped_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:  "p",
		Rules: []Rule{{Target: Target{}, Allow: true}},
	}}
	pools := []PoolCandidate{
		pc("kata", "microvm", rankMicrovm, Candidate{ID: "coder"}),
		pc("weird", "bogus", -1, Candidate{ID: "weird"}),
	}
	if got := AuditIsolation(policies, pools); len(got) != 0 {
		t.Fatalf("unknown profile must not violate, got %d: %+v", len(got), got)
	}
}

// TestAuditIsolation_LabelMatch_spec_24_14 verifies that MatchLabels
// scopes the matched set: only pools carrying the labels participate.
func TestAuditIsolation_LabelMatch_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:  "platform-only",
		Rules: []Rule{{Target: Target{MatchLabels: map[string]string{"team": "platform"}}, Allow: true}},
	}}
	pools := []PoolCandidate{
		pc("kata", "microvm", rankMicrovm, Candidate{ID: "coder", Labels: map[string]string{"team": "platform"}}),
		pc("runc-in", "standard", rankStandard, Candidate{ID: "chat", Labels: map[string]string{"team": "platform"}}),
		pc("runc-out", "standard", rankStandard, Candidate{ID: "ext", Labels: map[string]string{"team": "support"}}),
	}
	got := AuditIsolation(policies, pools)
	if len(got) != 1 {
		t.Fatalf("want 1 violation (only labelled pools match), got %d: %+v", len(got), got)
	}
	if got[0].TargetPool != "runc-in" {
		t.Errorf("want target runc-in, got %s (runc-out is unlabelled)", got[0].TargetPool)
	}
}

// TestAuditIsolation_StableSort_spec_24_14 verifies the report is sorted
// by (policy, tenant, ruleIndex, source, target).
func TestAuditIsolation_StableSort_spec_24_14(t *testing.T) {
	policies := []DelegationPolicy{
		{Name: "z", Rules: []Rule{{Target: Target{}, Allow: true}}},
		{Name: "a", Rules: []Rule{{Target: Target{}, Allow: true}}},
	}
	pools := []PoolCandidate{
		pc("kata", "microvm", rankMicrovm, Candidate{ID: "c"}),
		pc("runc", "standard", rankStandard, Candidate{ID: "d"}),
	}
	got := AuditIsolation(policies, pools)
	if len(got) != 2 {
		t.Fatalf("want 2 violations, got %d", len(got))
	}
	if got[0].Policy != "a" || got[1].Policy != "z" {
		t.Errorf("not sorted by policy: %s then %s", got[0].Policy, got[1].Policy)
	}
}

// TestAuditIsolation_NoPolicies_spec_24_14 verifies the empty inputs path.
func TestAuditIsolation_NoPolicies_spec_24_14(t *testing.T) {
	if got := AuditIsolation(nil, []PoolCandidate{pc("a", "standard", rankStandard, Candidate{ID: "a"})}); len(got) != 0 {
		t.Fatalf("no policies → no violations, got %+v", got)
	}
}

// TestProactiveWarningsForPool_spec_8_3_350 verifies the §8.3 line 350
// proactive single-pool filter: when a weaker pool is registered, the
// audit reports only the warnings where that pool is the weaker
// delegation target a stronger parent could reach, and reports nothing
// when the registered pool is the strongest target.
func TestProactiveWarningsForPool_spec_8_3_350(t *testing.T) {
	policies := []DelegationPolicy{{
		Name:     "team-agents",
		TenantID: "acme",
		Rules:    []Rule{{Target: Target{Types: []string{"agent"}}, Allow: true}},
	}}
	pools := []PoolCandidate{
		pc("kata-pool", "microvm", rankMicrovm, Candidate{ID: "coder", Type: "agent"}),
		pc("runc-pool", "standard", rankStandard, Candidate{ID: "chat", Type: "agent"}),
	}

	// Registering the weak runc-pool surfaces the kata-pool→runc-pool warning.
	warns := ProactiveWarningsForPool(policies, pools, "runc-pool")
	if len(warns) != 1 {
		t.Fatalf("want 1 warning for runc-pool, got %d: %+v", len(warns), warns)
	}
	if warns[0].SourcePool != "kata-pool" || warns[0].TargetPool != "runc-pool" {
		t.Errorf("warning = %+v, want kata-pool→runc-pool", warns[0])
	}

	// Registering the strong kata-pool surfaces nothing — no stronger parent.
	if got := ProactiveWarningsForPool(policies, pools, "kata-pool"); len(got) != 0 {
		t.Errorf("kata-pool (strongest) must produce no warnings, got %+v", got)
	}

	// A pool not in the inventory produces nothing.
	if got := ProactiveWarningsForPool(policies, pools, "ghost"); len(got) != 0 {
		t.Errorf("unknown pool must produce no warnings, got %+v", got)
	}
}
