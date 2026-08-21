// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the addressing of the per-slot
// cleanup report.
//
// A per-slot cleanup runs at every session release on a pod of any
// concurrency, and the adapter reports its outcome through
// `ReportSessionScrub`. The request is session-scoped: it is addressed by the
// identifier of the released session and names no slot. The §4.7 RPC row in
// spec/04_system-components.md states that rule; the reader-facing mirror in
// docs/reference/adapter-contract.md did not, which left an adapter author
// reading the reference page to conclude that the report names the slot it
// cleaned up.
//
// This case pins the rule on both carriers together and asserts they agree on
// the same wording, so a later edit to one cannot reintroduce the divergence on
// its own. It reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (ReportSessionScrub RPC and its addressing), 5.2 (per-slot cleanup
// at each session release)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// sessionScrubAddressingRule is the sentence both carriers state about how the
// per-slot cleanup report is addressed.
const sessionScrubAddressingRule = "addressed by the identifier of the released session and names no slot"

// spec: 4.7, 5.2
// diagnosis: the `ReportSessionScrub` row in spec/04_system-components.md §4.7
//
//	or in docs/reference/adapter-contract.md no longer states that the report
//	is addressed by the identifier of the released session and names no slot.
//	A failure here means one of the two carriers leaves the report's address
//	open, so an adapter author can read the reference page and address the
//	report by a slot identifier the gateway does not accept.
func TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc(t *testing.T) {
	root := repoRoot(t)

	specRow := lineContaining(
		specSection(t, filepath.Join(root, "spec", "04_system-components.md"), "### 4.7 "),
		"| `ReportSessionScrub` |",
	)
	if specRow == "" {
		t.Fatal("spec/04_system-components.md §4.7: ReportSessionScrub RPC row not found (renamed or removed?)")
	}
	requireAllContain(t, "spec §4.7 ReportSessionScrub row", specRow, []string{
		sessionScrubAddressingRule,
	})

	docRow := lineContaining(adapterContractDoc(t, root), "| `ReportSessionScrub` |")
	if docRow == "" {
		t.Fatal("docs/reference/adapter-contract.md: ReportSessionScrub RPC row not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md ReportSessionScrub row", docRow, []string{
		sessionScrubAddressingRule,
	})

	// Both carriers open the rule the same way, so the two statements read as
	// one contract rather than as two independently worded claims.
	const opener = "The request is session-scoped: it is "
	for _, carrier := range []struct{ label, row string }{
		{"spec §4.7 ReportSessionScrub row", specRow},
		{"adapter-contract.md ReportSessionScrub row", docRow},
	} {
		if !strings.Contains(carrier.row, opener+sessionScrubAddressingRule+".") {
			t.Errorf("%s states the addressing rule in wording the other carrier does not share; expected %q", carrier.label, opener+sessionScrubAddressingRule+".")
		}
	}
}
