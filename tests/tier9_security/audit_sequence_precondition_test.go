// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 unit-scope coverage of the audit-chain precondition helpers.
// These cases run without a cluster: they pin what the audit-integrity
// cases do when the environment cannot supply what a probe needs.
package tier9_security_test

import "testing"

// TestAuditSequenceProbeIsBlockedWithoutAStoreAddress pins the
// precondition the per-tenant audit sequence probe states when the
// caller could not resolve the Postgres pod IP. The probe shells psql at
// that address, so an unresolvable one used to fail the audit-chain
// continuity case on an environment gap instead of reporting the
// precondition. The reason opens with a skip category so the TESTING.md
// §17.9 classifier accepts it without a register entry.
//
// diagnosis: a failure here means the audit-chain continuity case will
// hard-fail on a cluster whose lenny-postgres pod reports no pod IP,
// reporting an environment gap as an integrity defect.
//
// spec: §11.7, §15.1
func TestAuditSequenceProbeIsBlockedWithoutAStoreAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"", "   "} {
		if !auditSequenceProbeBlocked(address) {
			t.Errorf("the probe is not blocked for store address %q, so the continuity case "+
				"queries psql with no host and fails on the environment", address)
		}
	}

	if auditSequenceProbeBlocked("10.244.0.7") {
		t.Error("the probe is blocked for a store address that resolved")
	}
}
