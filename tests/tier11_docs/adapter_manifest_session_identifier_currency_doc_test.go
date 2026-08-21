// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the manifest member that names the session.
//
// The adapter manifest is one pod-global file that the adapter rewrites before
// each session's runtime start, so it is authoritative for the session whose
// start last wrote it. On a pod holding more than one bound session a later
// start replaces `sessionId`, and an earlier session's runtime is still
// processing against the value it read at its own start.
//
// The retired reading is that the member names the pod. A field row that
// scopes the value to the pod tells a co-tenanted runtime that a later
// manifest read still names its own session, which is the collision the
// currency rule exists for.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest field set and manifest currency)

package tier11_docs_test

import (
	"testing"
)

// spec: 4.7
// diagnosis: the `sessionId` row of the adapter manifest field reference
//
//	contradicts the manifest currency rule stated in the same section's lead.
//	The member names the session whose start last wrote the file, and a later
//	start on a co-tenanted pod replaces it. A row that calls it the session
//	identifier "for this pod" tells a runtime serving an earlier session that a
//	later manifest read still names its own session.
func TestAdapterManifestSessionIdRowStatesTheCurrencyRule(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}

	row := lineContaining(manifest, "| `sessionId` |")
	if row == "" {
		t.Fatal("docs/reference/adapter-contract.md: the manifest `sessionId` field row was not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md manifest `sessionId` row", row, []string{
		"The session whose start last wrote the manifest.",
		"On a pod holding more than one bound session a later start replaces it, so a runtime reads it at its own start rather than re-reading it later.",
	})
	requireNoneContain(t, "adapter-contract.md manifest `sessionId` row", row, []string{
		"The session identifier for this pod",
	})
}
