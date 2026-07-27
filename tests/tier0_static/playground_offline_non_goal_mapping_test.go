// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// offlineNonGoalSentence is the §27.1 non-goal the tier-0 bundle sweep
// enforces against the embedded playground SPA. It is quoted here so a
// reword of the spec line surfaces as a failure of the drift guard
// below rather than as a silently unenforced non-goal.
const offlineNonGoalSentence = "No offline mode. The playground requires a live gateway."

// offlineNonGoalTestFile is the tier-0 test that enforces the non-goal
// against the embedded bundle.
const offlineNonGoalTestFile = "tests/tier0_static/playground_no_offline_assets_test.go"

// specPlaygroundFile holds §27 and is read directly so the guard reads
// the spec rather than a paraphrase of it.
const specPlaygroundFile = "spec/27_web-playground.md"

// spec: §27.1 (web playground — purpose and non-goals), non-goal 3: "No
//
//	offline mode. The playground requires a live gateway."
//
// diagnosis: §27.1 no longer carries the offline-mode non-goal under the
//
//	heading, so the tier-0 bundle sweep
//	(playground_no_offline_assets_test.go) and the spec-map entry that
//	records it now cite a sentence the spec does not state. Either the
//	non-goal moved to another subsection, in which case repoint this
//	constant and the spec-map entry at the section that now owns it, or
//	it was dropped, in which case the bundle sweep no longer encodes a
//	spec requirement and its removal has to go through the proposal
//	pipeline.
func TestSpecStillStatesTheOfflineNonGoalUnderPurposeAndNonGoals(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, specPlaygroundFile))
	if err != nil {
		t.Fatalf("read %s: %v", specPlaygroundFile, err)
	}
	section := specSubsectionBody(string(body), "### 27.1 ")
	if section == "" {
		t.Fatalf("%s has no `### 27.1 ` heading; the §27 subsection numbering changed", specPlaygroundFile)
	}
	if !strings.Contains(section, offlineNonGoalSentence) {
		t.Errorf("%s §27.1 no longer states %q; %s enforces that non-goal against the embedded SPA bundle",
			specPlaygroundFile, offlineNonGoalSentence, offlineNonGoalTestFile)
	}
}

// spec: §27.1 (web playground — purpose and non-goals), non-goal 3: "No
//
//	offline mode. The playground requires a live gateway." tests/README.md
//	defines tests/spec-map-exceptions.yaml as holding "Spec sections
//	explicitly exempt from the \"every section has at least one test\"
//	rule, with justifications."
//
// diagnosis: §27.1 is recorded in tests/spec-map-exceptions.yaml as a
//
//	section with no behavior to verify, or its behavior is registered in
//	tests/spec-map.json under a different section id, while the tier-0
//	bundle sweep does verify one of its non-goals. Both states mislead a
//	reviewer auditing coverage: the exception says the section needs no
//	test when a test already pins it, and a reference filed under the §27
//	chapter entry leaves `lenny-test --spec 27.1` selecting nothing.
//	Register the sweep under the "27.1" section of tests/spec-map.json and
//	keep the section out of the exceptions file.
func TestOfflineNonGoalIsMappedToItsSectionRatherThanExcepted(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)

	if readSpecMapExceptedSections(t, root)["27.1"] {
		t.Errorf("tests/spec-map-exceptions.yaml excepts §27.1 from the \"every section has at "+
			"least one test\" rule, but %s verifies its non-goal %q against the embedded "+
			"playground bundle; the exemption under-reports delivered coverage",
			offlineNonGoalTestFile, offlineNonGoalSentence)
	}

	mapped := readSpecMapTests(t)["27.1"]
	for _, path := range mapped {
		if path == offlineNonGoalTestFile {
			return
		}
	}
	t.Errorf("tests/spec-map.json §27.1 records tests %v, which do not include %s, the sweep "+
		"that enforces the section's %q non-goal against the embedded playground bundle",
		mapped, offlineNonGoalTestFile, offlineNonGoalSentence)
}

// specSubsectionBody returns the text of the subsection introduced by
// heading, up to the next heading of the same or a higher level. It
// returns the empty string when the heading is absent.
func specSubsectionBody(doc, heading string) string {
	start := strings.Index(doc, heading)
	if start < 0 {
		return ""
	}
	rest := doc[start+len(heading):]
	for _, marker := range []string{"\n### ", "\n## ", "\n# "} {
		if end := strings.Index(rest, marker); end >= 0 {
			rest = rest[:end]
		}
	}
	return rest
}
