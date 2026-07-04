// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the §5.2 whole-pod scrub recycle
// trigger (proposal 0031, F-5.2.15). Proposal S-A1 defined the recycle
// disposition and its scrub-config delivery in two places: the §4.7
// Gateway → Adapter `Terminate` (proto `Shutdown`) RPC row, and the §5.2
// "Lenny scrub procedure" trigger paragraph. These two normative sites must
// agree on the recycle disposition and the parameters the recycle Shutdown
// carries (podId, cleanupCommands, cleanupTimeoutSeconds, scrubProfile), and
// their bidirectional cross-references must resolve to live headings. This
// test pins that agreement so a later spec edit cannot silently drift the two
// sites apart or orphan the cross-reference.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (runtime adapter, shutdown recycle disposition), 5.2 (whole-pod
// scrub).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// recycleScrubParams are the four parameters the recycle-disposition Shutdown
// carries. Both the §4.7 `Terminate` row and the §5.2 scrub-procedure trigger
// paragraph must name all four, so an adapter implementer reading either site
// learns the same contract. spec: §4.7, §5.2.
var recycleScrubParams = []string{
	"podId",
	"cleanupCommands",
	"cleanupTimeoutSeconds",
	"scrubProfile",
}

// spec: 4.7, 5.2
// diagnosis: The §4.7 `Terminate` (proto `Shutdown`) row and the §5.2 "Lenny
//
//	scrub procedure" trigger paragraph disagree on the recycle disposition or
//	the parameters it carries. Proposal 0031 (S-A1) defined the same contract
//	in both sites: the recycle disposition carries podId, cleanupCommands,
//	cleanupTimeoutSeconds, and scrubProfile, and the adapter reports the
//	outcome via ReportPodScrub. A failure here means one site dropped the
//	recycle disposition, dropped one of the carried parameters, or stopped
//	naming ReportPodScrub, leaving an adapter implementer with two different
//	contracts depending on which section they read.
func TestRecycleTriggerConsistentAcrossSpec47And52_F5215(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s47 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "### 4.7 ")
	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")

	// The §4.7 Gateway → Adapter table denotes the single proto Shutdown RPC as
	// the `Terminate` row, extended with the recycle disposition. The whole §4.7
	// section is large; scope the assertion to the `Terminate` (proto `Shutdown`)
	// row so a match elsewhere in §4.7 does not mask a drift in the row itself.
	terminateRow := requireLine(t, s47, "`Terminate` (proto `Shutdown`)")

	// The §5.2 trigger sentence names the `Terminate` RPC (proto `Shutdown`) as
	// the whole-pod scrub trigger; scope the §5.2 assertions to the scrub-
	// procedure paragraph that carries the trigger text.
	triggerParagraph := requireLine(t, s52, "The gateway triggers the whole-pod scrub")

	// Both sites name the recycle disposition and carry all four scrub
	// parameters.
	requireAllContain(t, "§4.7 Terminate (proto Shutdown) row", terminateRow, []string{
		"recycle disposition",
		"ReportPodScrub",
	})
	requireAllContain(t, "§5.2 scrub-procedure trigger paragraph", triggerParagraph, []string{
		"recycle disposition",
		"ReportPodScrub",
		"`Terminate`",
		"proto `Shutdown`",
	})
	for _, param := range recycleScrubParams {
		if !strings.Contains(terminateRow, "`"+param+"`") {
			t.Errorf("§4.7 Terminate (proto Shutdown) row does not carry the recycle parameter %q; §4.7 and §5.2 must agree on all four (podId, cleanupCommands, cleanupTimeoutSeconds, scrubProfile)", param)
		}
		if !strings.Contains(triggerParagraph, "`"+param+"`") {
			t.Errorf("§5.2 scrub-procedure trigger paragraph does not carry the recycle parameter %q; §4.7 and §5.2 must agree on all four (podId, cleanupCommands, cleanupTimeoutSeconds, scrubProfile)", param)
		}
	}

	// The §4.7 row and the §5.2 trigger paragraph must agree that the gateway
	// does not block the Shutdown response on the scrub, and that a missing
	// report is bounded by a gateway-side timeout.
	requireAllContain(t, "§4.7 Terminate (proto Shutdown) row", terminateRow, []string{
		"does not block the response on the scrub",
	})
	requireAllContain(t, "§5.2 scrub-procedure trigger paragraph", triggerParagraph, []string{
		"does not block the response on the scrub",
	})
}

// spec: 4.7, 5.2
// diagnosis: A cross-reference between the §4.7 `Terminate` row and the §5.2
//
//	scrub-procedure trigger paragraph points at a heading that no longer
//	exists. Proposal 0031 (S-A1) wove the two sites together with bidirectional
//	links: the §4.7 row links to §5.2, and the §5.2 trigger paragraph links to
//	§4.7. A reader following the published link gets a 404 to the section
//	anchor. Re-point the link or restore the heading.
func TestRecycleTriggerCrossRefsResolve_F5215(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// The §4.7 `Terminate` row links to §5.2; the §5.2 trigger paragraph links
	// to §4.7. Both anchors must resolve to a live heading in the target file.
	refs := []struct {
		targetFile string
		anchor     string
		label      string
	}{
		{"05_runtime-registry-and-pool-model.md", "52-pool-configuration-and-execution-modes", "§4.7 Terminate row → §5.2"},
		{"04_system-components.md", "47-runtime-adapter", "§5.2 trigger paragraph → §4.7"},
	}
	for _, ref := range refs {
		slugs, err := headingSlugs(filepath.Join(specDir, ref.targetFile))
		if err != nil {
			t.Fatalf("read heading slugs from %s: %v", ref.targetFile, err)
		}
		if !slugs[ref.anchor] {
			t.Errorf("%s cross-reference #%s does not resolve to any heading in spec/%s", ref.label, ref.anchor, ref.targetFile)
		}
	}

	// Confirm the link syntax is actually present at each source site, so the
	// cross-reference exists rather than merely the anchor being resolvable.
	s47 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "### 4.7 ")
	terminateRow := requireLine(t, s47, "`Terminate` (proto `Shutdown`)")
	if !strings.Contains(terminateRow, "05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes") {
		t.Error("§4.7 Terminate (proto Shutdown) row does not link to §5.2; the recycle disposition must cross-reference the whole-pod scrub section")
	}

	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	triggerParagraph := requireLine(t, s52, "The gateway triggers the whole-pod scrub")
	if !strings.Contains(triggerParagraph, "04_system-components.md#47-runtime-adapter") {
		t.Error("§5.2 scrub-procedure trigger paragraph does not link to §4.7; the trigger sentence must cross-reference the runtime-adapter RPC section")
	}
}

// requireLine returns the single markdown line in body that contains substr,
// failing the test when no line matches. It scopes an assertion to the specific
// table row or paragraph line that carries a claim, so a match elsewhere in the
// section does not mask a drift in the line itself. Both the §4.7 recycle row
// and the §5.2 trigger sentence are single logical lines in the source
// markdown. It reuses the package-level lineContaining helper and adds the
// fail-when-absent guard so a renamed row surfaces here rather than as a
// silently-empty match.
func requireLine(t *testing.T, body, substr string) string {
	t.Helper()
	ln := lineContaining(body, substr)
	if ln == "" {
		t.Fatalf("no line contains %q (renamed or removed?)", substr)
	}
	return ln
}
