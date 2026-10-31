// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"testing"
)

// The chunk resolver the derive path wires fails closed: the World never
// binds a derived session, so a call to it is a fixture defect and returns
// an error with no grants rather than a capability.
//
// spec: §7.1 (Derive restore semantics)
func TestUnboundRestoreResolverFailsClosed_spec_7_1(t *testing.T) {
	res, err := unboundRestore{}.Resolve(context.Background(), "acme", "sess_derived", "/acme/checkpoints/sess_derived/ckpt")
	if err == nil {
		t.Fatal("Resolve succeeded, want the never-bound error")
	}
	if len(res.Grants) != 0 {
		t.Errorf("Resolve returned grants %v with its error", res.Grants)
	}
}
