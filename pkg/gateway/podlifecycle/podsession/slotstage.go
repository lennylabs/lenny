// SPDX-License-Identifier: MIT

package podsession

// §5.2 lenny_slot_failure_total error_type labels: the
// concurrent-mode slot bind stages whose failure terminates a reserved
// slot. The set is finite so the metric stays low-cardinality.
const (
	slotFailureWorkspacePrep = "workspace_prep"
	// slotFailureWorkspaceFinalize labels a FinalizeWorkspace failure so the
	// metric separates it from a staging failure. It is a metric label only:
	// the SlotBindError at the finalize site keeps slotFailureWorkspacePrep,
	// because SlotBindError.Reason keys the transient classification of a
	// workspace-stage FailedPrecondition on that stage.
	slotFailureWorkspaceFinalize    = "workspace_finalize"
	slotFailureSetup                = "setup"
	slotFailureCredentialAssignment = "credential_assignment"
	slotFailureSessionStart         = "session_start"
	// slotFailureConnect labels a reservation-bearing failure before the
	// post-connection bind stages run (resolve, dial, version handshake).
	// It is used only on the SlotBindError for retry classification; it is
	// not a lenny_slot_failure_total error_type value.
	slotFailureConnect = "connect"
	// slotFailureResume labels a failed adapter Resume on the SlotBindError
	// Binder.Resume returns, so the caller's slot accounting reads the reclaim
	// disposition off the chain. Like slotFailureConnect it is not a
	// lenny_slot_failure_total error_type value.
	slotFailureResume = "resume"
)
