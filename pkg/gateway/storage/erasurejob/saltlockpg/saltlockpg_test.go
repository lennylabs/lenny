// SPDX-License-Identifier: MIT

package saltlockpg

import "testing"

// spec: §12.8 line 856 — the advisory-lock key is stable per tenant (so
// every replica computes the same key and they contend on the same lock)
// and distinct across tenants (so two tenants' rotations do not serialize
// against each other). F-12.8.5.
func TestLockKeyStableAndPerTenant_spec_12_8_856(t *testing.T) {
	if lockKey("acme") != lockKey("acme") {
		t.Error("lockKey must be deterministic for the same tenant")
	}
	if lockKey("acme") == lockKey("globex") {
		t.Error("lockKey must differ across tenants so their rotations do not contend")
	}
}
