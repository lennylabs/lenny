// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

var remediationStepHeading = regexp.MustCompile(`(?m)^### (R[0-9]+[a-z]?)\.`)

// readOpenRemediationBlockers returns the set of remediation step ids that
// gateway-runtime-comms-remediation.md still declares as a plan step. A
// pending-implementation deferral is retired when the step its blocker names
// lands and that step's heading leaves the document.
func readOpenRemediationBlockers(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	if err != nil {
		t.Fatalf("read gateway-runtime-comms-remediation.md: %v", err)
	}
	out := map[string]bool{}
	for _, m := range remediationStepHeading.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	return out
}

// spec: 29.4 (the interrupt, terminate, and delete trace), 29.10 (the
//
//	structural analysis of the concurrent-session pod); tests/README.md
//	("`spec-map-exceptions.yaml` | Spec sections explicitly exempt from the
//	'every section has at least one test' rule, with justifications.")
//
// diagnosis: A pending-implementation deferral records that a section's trace
//
//	cannot be asserted yet, and it is retired by the remediation step its
//	blocker names rather than by the section acquiring a test of some other
//	kind. §29.4 turns on the session terminate route reaching the pod and
//	§29.10 on the concurrent-slot harness. Both sections are cited by
//	documentation-mirror reconciliation cases that assert neither leg, so a
//	spec-map key for the section is not evidence that the trace is covered,
//	and an excepted section is allowed to carry tests. A failure here means
//	either the deferral was dropped while its blocker is still an open step in
//	gateway-runtime-comms-remediation.md, which records coverage that does not
//	exist, or the blocker's step has landed and the stale deferral should go
//	with it.
func TestSpecMapExceptionsKeepTraceDeferralsUntilTheirBlockerLands(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	coverage, err := loadHeadingCoverage(scope.DirReader(root))
	if err != nil {
		t.Fatalf("load the spec map and its exception register: %v", err)
	}
	open := readOpenRemediationBlockers(t, root)

	deferred := map[string]string{
		"29.4":  "R10",
		"29.10": "R7",
	}
	for section, blocker := range deferred {
		row, ok := coverage.exceptions[section]
		if !open[blocker] {
			if ok {
				t.Errorf("gateway-runtime-comms-remediation.md no longer declares step %s, "+
					"so the §%s pending-implementation deferral it blocked is stale; remove "+
					"the §%s entry from tests/spec-map-exceptions.yaml", blocker, section, section)
			}
			continue
		}
		if !ok {
			t.Errorf("tests/spec-map-exceptions.yaml has no entry for §%s while "+
				"gateway-runtime-comms-remediation.md still declares step %s; the trace §%s "+
				"states is unassertable until that step lands, and the section's spec-map "+
				"tests[] entries do not assert it, so dropping the deferral records coverage "+
				"that does not exist", section, blocker, section)
			continue
		}
		if row.Reason != reasonPendingImplementation {
			t.Errorf("tests/spec-map-exceptions.yaml §%s carries reason %q; the section waits "+
				"on remediation step %s, so its reason is %s",
				section, row.Reason, blocker, reasonPendingImplementation)
		}
		if row.Blocker != blocker {
			t.Errorf("tests/spec-map-exceptions.yaml §%s names blocker %q; the trace waits on "+
				"remediation step %s", section, row.Blocker, blocker)
		}
	}
}
