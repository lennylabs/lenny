// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// TestResumeAttemptExitCells_spec_7_3 runs the resume-attempt exit cells on
// the lifecycle harness: a landed commit, a pre-claim failure, and a failure
// after the entry write on every attempt, each followed by the invariant
// checker.
//
// spec: §7.3 (Resume driver), §7.3 (Credential leases on the replacement pod),
// §6.2 (Pod State Machine)
func TestResumeAttemptExitCells_spec_7_3(t *testing.T) {
	lifecycleharness.RunResumeExitCells(t, nil)
}
