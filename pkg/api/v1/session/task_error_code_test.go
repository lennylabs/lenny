// SPDX-License-Identifier: MIT

package session

import "testing"

// TestTaskErrorCodeEveryFailureReason_spec_8_8 maps every FailureReason
// the platform stamps on a terminal row to the error code its §8.8
// TaskResult and §8.10 child_failed event carry. Reasons the
// specification names a code for map to that code; `expired:*` reasons
// and reasons the specification gives no code surface verbatim.
//
// spec: §8.8 (TaskRecord and TaskResult Schema), §15.1 (REST API, error
// code catalog), §8.10 (Delegation Tree Recovery).
func TestTaskErrorCodeEveryFailureReason_spec_8_8(t *testing.T) {
	cases := []struct {
		reason FailureReason
		want   string
	}{
		{FailureDeadlockTimeout, "DEADLOCK_TIMEOUT"},
		{FailureRuntimeCrash, "RUNTIME_CRASH"},
		{FailureSetupCommandFailed, "SETUP_COMMAND_FAILED"},
		{FailureExpiredDeadline, "expired:deadline"},
		{FailureExpiredBudget, "expired:budget"},
		{FailureExpiredLease, "expired:lease"},
		{FailureExpiredIdle, "expired:idle"},
		{FailurePodEvicted, "pod_evicted"},
		{FailureNodeLost, "node_lost"},
		{FailureWorkspaceValidationFailed, "workspace_validation_failed"},
		{FailureOrphanPodTerminated, "orphan_pod_terminated"},
	}
	// Every declared reason must appear in the table, so a reason added
	// later fails this test until its code is decided.
	declared := []FailureReason{
		FailurePodEvicted, FailureNodeLost, FailureRuntimeCrash,
		FailureWorkspaceValidationFailed, FailureSetupCommandFailed,
		FailureExpiredDeadline, FailureExpiredBudget, FailureExpiredLease,
		FailureExpiredIdle, FailureOrphanPodTerminated, FailureDeadlockTimeout,
	}
	covered := map[FailureReason]bool{}
	for _, tc := range cases {
		covered[tc.reason] = true
	}
	for _, r := range declared {
		if !covered[r] {
			t.Errorf("FailureReason %q has no row in the code table", r)
		}
	}
	for _, tc := range cases {
		for _, st := range []State{StateFailed, StateExpired, StateCancelled} {
			if got := TaskErrorCode(string(tc.reason), st); got != tc.want {
				t.Errorf("TaskErrorCode(%q, %s) = %q, want %q", tc.reason, st, got, tc.want)
			}
		}
	}
}

// TestTaskErrorCodeEmptyReasonPerState_spec_8_8 asserts the per-state
// CHILD_<STATE> fallback for a terminal row with no stored reason.
//
// spec: §8.8 (TaskRecord and TaskResult Schema).
func TestTaskErrorCodeEmptyReasonPerState_spec_8_8(t *testing.T) {
	for st, want := range map[State]string{
		StateFailed:    "CHILD_FAILED",
		StateExpired:   "CHILD_EXPIRED",
		StateCancelled: "CHILD_CANCELLED",
	} {
		if got := TaskErrorCode("", st); got != want {
			t.Errorf("TaskErrorCode(\"\", %s) = %q, want %q", st, got, want)
		}
	}
}

// TestTaskErrorCodeUnmappedReasonsVerbatim_spec_8_8 asserts that a
// reason outside the mapped set, including one already spelled as a
// catalog code and a near-miss of a mapped reason, surfaces verbatim
// rather than being rewritten.
//
// spec: §8.8 (TaskRecord and TaskResult Schema).
func TestTaskErrorCodeUnmappedReasonsVerbatim_spec_8_8(t *testing.T) {
	for _, reason := range []string{
		"workspace_seal_timeout",
		"FORCE_TERMINATED",
		"DEADLOCK_TIMEOUT",
		"Deadlock_Timeout",
		"deadlock_timeout ",
		"expired:custom",
	} {
		if got := TaskErrorCode(reason, StateFailed); got != reason {
			t.Errorf("TaskErrorCode(%q) = %q, want it verbatim", reason, got)
		}
	}
}
