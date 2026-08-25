// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// slotAddressAbsenceTestFile is the tier-3 file that pins the absence of a
// slot address from the client-facing REST surfaces. Its cases each assert
// one spec section's contract, and tests/spec-map.json must credit each case
// to the sections that case actually exercises. The map validator only checks
// that a reference resolves, so a case filed under an unrelated section is
// invisible to it: this test closes that gap for the file.
const slotAddressAbsenceTestFile = "tests/tier3_contract/rest_sessions/slot_address_absence_test.go"

// specAnnotationSectionRE matches a section id in a `// spec:` annotation,
// with or without the section sign, and captures the dotted id.
var specAnnotationSectionRE = regexp.MustCompile(`§?(\d+(?:\.\d+)*)`)

// specMapFunctionEntries returns, per spec-section id, the test function
// names that tests/spec-map.json registers from the given repo-relative
// file. Entries without a `::TestName` selector name the whole file and
// are not per-case registrations, so they are skipped.
func specMapFunctionEntries(t *testing.T, file string) map[string][]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string][]string{}
	for id, sec := range doc.Sections {
		for _, entry := range sec.Tests {
			idx := strings.Index(entry, "::")
			if idx < 0 || entry[:idx] != file {
				continue
			}
			out[id] = append(out[id], entry[idx+2:])
		}
	}
	return out
}

// specAnnotationSections returns, per test function name declared in the
// given Go file, the set of spec-section ids the function's `// spec:`
// annotation names.
func specAnnotationSections(t *testing.T, file string) map[string]map[string]bool {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	lines := strings.Split(string(body), "\n")
	out := map[string]map[string]bool{}
	funcRE := regexp.MustCompile(`^func (Test\w+)\(`)
	for i, line := range lines {
		m := funcRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// Walk back over the contiguous comment block above the
		// declaration and collect every section id it names.
		sections := map[string]bool{}
		for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
			for _, s := range specAnnotationSectionRE.FindAllStringSubmatch(lines[j], -1) {
				sections[s[1]] = true
			}
		}
		out[m[1]] = sections
	}
	return out
}

// spec: 5.2 (client error on exhaustion), 7.1 (session lifecycle normal
// flow), 7.2 (message dispatch), 4.1 (message scope)
//
// Every per-case spec-map registration of a slot-address-absence test names
// a section that case's own `// spec:` annotation carries. A registration
// under a section the case does not exercise credits that section with
// coverage no regression in it would break, and leaves the section the case
// does pin unmapped.
func TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise(t *testing.T) {
	registered := specMapFunctionEntries(t, slotAddressAbsenceTestFile)
	annotated := specAnnotationSections(t, slotAddressAbsenceTestFile)
	if len(registered) == 0 {
		t.Fatalf("spec-map.json registers no case from %s", slotAddressAbsenceTestFile)
	}
	for section, fns := range registered {
		for _, fn := range fns {
			sections, ok := annotated[fn]
			if !ok {
				t.Errorf("spec-map.json section %s registers %s, which %s does not declare", section, fn, slotAddressAbsenceTestFile)
				continue
			}
			if !sections[section] {
				t.Errorf("spec-map.json section %s registers %s, whose spec annotation names %v instead", section, fn, sortedSectionIDs(sections))
			}
		}
	}
}

// sortedSectionIDs renders a section-id set in a stable order for failure
// messages.
func sortedSectionIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
