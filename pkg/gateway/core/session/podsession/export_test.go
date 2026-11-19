// SPDX-License-Identifier: MIT

package podsession

// Stage-label constants exported to the external test package so a test
// asserts against the constant rather than a copy of its string value.
const (
	SlotFailureWorkspacePrepForTest     = slotFailureWorkspacePrep
	SlotFailureWorkspaceFinalizeForTest = slotFailureWorkspaceFinalize
)

// SlotCleanupFloorForTest is the §5.2 per-slot cleanup floor that bounds a
// detached release context.
const SlotCleanupFloorForTest = slotCleanupFloor
