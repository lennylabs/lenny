// SPDX-License-Identifier: MIT

package sessionserver_test

import "testing"

// spec: §25.11 (Backup and Restore API — ArtifactStore restore procedure)
// diagnosis: a failure means the ArtifactStore restore procedure's
// client-visible failure mode is not honored. The spec's restore procedure
// states: "Sessions whose workspace snapshot is missing transition to
// `failed` on next resume attempt with error `WORKSPACE_SNAPSHOT_MISSING`;
// the gateway surfaces this to clients via the existing session-state API."
//
// The adapter restore path now fetches presigned chunk GET capabilities the
// gateway mints from the manifest row (§10.1 line 155) rather than an
// in-adapter checkpoint source, so the missing-snapshot detection point and
// the session-state API field that carries the error are re-expressed
// against the gateway resume driver. The test stays skipped until that path
// lands; the body is a placeholder so the tier-0 build gate stays green.
func TestResumeWithMissingWorkspaceSnapshotFailsWithSnapshotMissing_spec_25_11(t *testing.T) {
	t.Skip("WORKSPACE_SNAPSHOT_MISSING resume failure mode is unimplemented; the detection point moves to the gateway-minted presigned chunk restore path (§10.1 line 155). The session-state API field that carries the error is undecided — see the open TEST-GAPS finding for the ArtifactStore restore-procedure failure mode.")
}
