// SPDX-License-Identifier: MIT

package tier0_static

import (
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §27.4 (web playground — UI surface). The section specifies
//
//	executable client behavior, not layout alone: the runtime picker
//	"Lists runtimes visible to the caller (filtered by
//	`playground.allowedRuntimes` and caller scopes)"; session
//	configuration is "A form generated from the runtime's
//	`runtimeOptionsSchema` ... The delegation-policy field is a
//	client-side visibility affordance shown only when the minted
//	playground bearer's effective scope grants `tools:sessions:write`";
//	and the chat screen "Includes an Interrupt button, a Cancel button, a
//	raw-frame inspector ... and a \"Copy as client SDK snippet\" button
//	that emits equivalent code in Go/Python/TS". tests/README.md defines
//	the exceptions file as holding "Spec sections explicitly exempt from
//	the \"every section has at least one test\" rule, with
//	justifications."
//
// diagnosis: a §27 section is listed in tests/spec-map-exceptions.yaml
//
//	while tests/spec-map.json records tests for it. The exceptions file
//	waives one rule only, that a section carry at least one test
//	(cmd/lenny-test/cmd_validate.go, validateSpecMapCoverage), so an
//	exception on a section that already carries tests waives nothing and
//	tells a reader the opposite of what the map says. For §27.4 the
//	claim is also wrong on its face: the picker filter, the
//	schema-generated config form with its scope-gated delegation-policy
//	field, and the Interrupt, Cancel, raw-frame-inspector, and
//	SDK-snippet chat controls are observable behaviors with tests, so
//	only pure visual layout would be non-executable. Remove the
//	section's entry from tests/spec-map-exceptions.yaml and let its
//	tests[] account for it. The same repository precedent removed the
//	§25.5, §25.8, §25.12, and §25.13 entries once those sections shipped
//	coverage.
func TestSection27ExemptionsDoNotCoverSectionsThatCarryTests(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	excepted := readSpecMapExceptedSections(t, root)
	tests := readSpecMapTests(t)

	contradictory := []string{}
	for section := range excepted {
		if section != "27" && !strings.HasPrefix(section, "27.") {
			continue
		}
		if len(tests[section]) > 0 {
			contradictory = append(contradictory, section)
		}
	}
	sort.Strings(contradictory)
	if len(contradictory) > 0 {
		t.Errorf("tests/spec-map-exceptions.yaml exempts §%s from the \"every section has "+
			"at least one test\" rule, but tests/spec-map.json already records tests for "+
			"each; the exemption waives nothing and misreports the section as carrying no "+
			"verifiable behavior. Remove the entry.",
			strings.Join(contradictory, ", §"))
	}
}

// spec: §27.4 (web playground — UI surface), item 1 ("Lists runtimes
//
//	visible to the caller (filtered by `playground.allowedRuntimes` and
//	caller scopes)"), item 2 ("A form generated from the runtime's
//	`runtimeOptionsSchema` ... Also exposes: workspace plan upload
//	(drag-drop tarball), delegation policy selection, and session
//	labels"), and item 3 ("Includes an Interrupt button, a Cancel
//	button, a raw-frame inspector ... and a \"Copy as client SDK
//	snippet\" button")
//
// diagnosis: the §27.4 entry of tests/spec-map.json no longer names the
//
//	screen-walk test that drives the three screens and their controls.
//	That test is what makes §27.4 a covered section rather than an
//	exempt one, so dropping the reference reopens the case for a
//	section-wide exemption. Restore the reference, or repoint it if the
//	file was renamed.
func TestSpecMapUISurfaceNamesTheScreenWalkTest(t *testing.T) {
	t.Parallel()

	const screenWalk = "pkg/gateway/mcpfabric/playground/ui_screens_test.go"

	got := readSpecMapTests(t)["27.4"]
	if len(got) == 0 {
		t.Fatalf("tests/spec-map.json §27.4 records no tests; the UI surface ships in "+
			"pkg/gateway/mcpfabric/playground and its controls are exercised by %s", screenWalk)
	}
	for _, path := range got {
		if path == screenWalk {
			return
		}
	}
	t.Errorf("tests/spec-map.json §27.4 tests %v do not include %s, the walk over the "+
		"runtime picker, session configuration, and chat controls", got, screenWalk)
}
