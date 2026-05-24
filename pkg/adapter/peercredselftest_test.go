// SPDX-License-Identifier: MIT

package adapter

import (
	"runtime"
	"testing"
)

// TestPeercredSelftest_spec_4_7_passes_on_loopback verifies the §4.7
// mandatory SO_PEERCRED startup self-test
// (spec/04_system-components.md lines 870-877) succeeds in the test
// environment: a loopback connection to an abstract socket reports the
// process's own UID via SO_PEERCRED. On non-Linux hosts the self-test is
// a no-op and also returns nil.
func TestPeercredSelftest_spec_4_7_passes_on_loopback(t *testing.T) {
	if err := PeercredSelftest(); err != nil {
		t.Fatalf("PeercredSelftest() = %v, want nil", err)
	}
}

// TestPeercredSelftest_spec_4_7_repeatable confirms the self-test cleans
// up its abstract socket so it can run on every pod start without a
// stale-address bind failure.
func TestPeercredSelftest_spec_4_7_repeatable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SO_PEERCRED self-test is Linux-only")
	}
	for i := 0; i < 3; i++ {
		if err := PeercredSelftest(); err != nil {
			t.Fatalf("PeercredSelftest() iteration %d = %v, want nil", i, err)
		}
	}
}
