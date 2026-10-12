// SPDX-License-Identifier: MIT

package session

import "strings"

// reasonErrorCodes maps a stored failure reason to the error code the
// §8.8 TaskResult error block and the §8.10 child_failed event carry,
// for every reason whose code the specification names:
//
//   - deadlock_timeout surfaces as DEADLOCK_TIMEOUT, the §15.1 catalog
//     code §8.8 names for the deepest blocked tasks the subtree deadlock
//     detector fails.
//   - runtime_crash surfaces as RUNTIME_CRASH, the code in the §8.8
//     failure example, whose retryPolicy names the reason runtime_crash.
//   - setup_command_failed surfaces as SETUP_COMMAND_FAILED, the §15.1
//     catalog code for a non-zero setup command.
//
// A reason absent from this map surfaces verbatim; see TaskErrorCode.
//
// spec: §8.8 (TaskRecord and TaskResult Schema), §15.1 (REST API, error
// code catalog).
var reasonErrorCodes = map[FailureReason]string{
	FailureDeadlockTimeout:    "DEADLOCK_TIMEOUT",
	FailureRuntimeCrash:       "RUNTIME_CRASH",
	FailureSetupCommandFailed: "SETUP_COMMAND_FAILED",
}

// TaskErrorCode returns the error code a terminal, non-completed child
// surfaces in its §8.8 TaskResult error block and its §8.10 child_failed
// event, given the stored failure reason and the terminal state.
//
//   - A reason the specification maps to a catalog code returns that code
//     (reasonErrorCodes).
//   - An `expired:*` reason returns verbatim, because §8.8 surfaces an
//     expired child as failed with the expiry reason as the code, such as
//     `expired:deadline`.
//   - An empty reason returns the per-state `CHILD_<STATE>` literal.
//   - Any other reason returns verbatim. The specification names no
//     catalog code for it, so the stored reason is the most specific
//     value available.
//
// The stored reason itself is unchanged: the §7.3 retry classifier keys
// on the lowercase reason, so only the reported code is mapped.
//
// spec: §8.8 (TaskRecord and TaskResult Schema), §8.10 (Delegation Tree
// Recovery), §7.3 (Retry and Resume).
func TaskErrorCode(reason string, state State) string {
	if reason == "" {
		return "CHILD_" + strings.ToUpper(string(state))
	}
	if code, ok := reasonErrorCodes[FailureReason(reason)]; ok {
		return code
	}
	return reason
}
