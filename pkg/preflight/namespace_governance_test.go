// SPDX-License-Identifier: MIT

package preflight

import (
	"strings"
	"testing"
)

// spec: §17.6 lines 501-502; §17.2. F-17.6.1.
func TestCheckNamespaceResourceGovernance_spec_17_6_501(t *testing.T) {
	statuses := []NamespaceGovernanceStatus{
		// A not-yet-created namespace (fresh install) is skipped.
		{Name: "lenny-agents-new", Exists: false},
		// An existing, fully-governed namespace passes.
		{Name: "lenny-agents", Exists: true, HasResourceQuota: true, HasLimitRange: true},
	}
	if d := CheckNamespaceResourceQuotas(statuses); !d.Passed {
		t.Fatalf("governed + not-yet-created should pass: %s", d.Reason)
	}
	if d := CheckNamespaceLimitRanges(statuses); !d.Passed {
		t.Fatalf("governed + not-yet-created should pass: %s", d.Reason)
	}

	missingRQ := []NamespaceGovernanceStatus{
		{Name: "lenny-agents-kata", Exists: true, HasResourceQuota: false, HasLimitRange: true},
	}
	d := CheckNamespaceResourceQuotas(missingRQ)
	if d.Passed || !strings.Contains(d.Reason, "ResourceQuota missing in namespace 'lenny-agents-kata'") {
		t.Fatalf("existing namespace without ResourceQuota should fail, got passed=%v reason=%q", d.Passed, d.Reason)
	}

	missingLR := []NamespaceGovernanceStatus{
		{Name: "lenny-agents", Exists: true, HasResourceQuota: true, HasLimitRange: false},
	}
	d = CheckNamespaceLimitRanges(missingLR)
	if d.Passed || !strings.Contains(d.Reason, "LimitRange missing in namespace 'lenny-agents'") {
		t.Fatalf("existing namespace without LimitRange should fail, got passed=%v reason=%q", d.Passed, d.Reason)
	}
	if !strings.Contains(d.Reason, "BestEffort") {
		t.Fatalf("LimitRange failure should cite the BestEffort rationale: %q", d.Reason)
	}
}
