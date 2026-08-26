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

// slotAddressCaseFiles is the inventory of test files that landed with the
// per-session slot address contract: the cases that pin the session
// identifier as the only address on both intra-pod legs, the per-slot pod
// filesystem layout, and the per-session credential file. Each of them must
// be credited in tests/spec-map.json under every spec section its own
// `// spec:` annotation names. The map validator walks tier 2 through tier
// 10 only, and it checks that a reference resolves rather than that a case
// is credited to the sections it exercises, so a tier-0, tier-11, `cmd/`, or
// package-local case can carry an annotation no section records.
var slotAddressCaseFiles = []string{
	"cmd/lenny-compliance/full_test.go",
	"cmd/lenny-compliance/sessionecho_test.go",
	"tests/tier0_static/adapter_proto_message_scope_test.go",
	"tests/tier10_conformance/scaffold_battery_test.go",
	"tests/tier11_docs/credential_path_literal_sweep_test.go",
	"tests/tier11_docs/frame_address_spec_citation_test.go",
	"tests/tier11_docs/recycle_scrub_trigger_consistency_test.go",
	"tests/tier11_docs/redis_key_prefix_registry_test.go",
	"tests/tier11_docs/retirement_sweep_surfaces_test.go",
	"tests/tier11_docs/rotation_ceiling_cotenant_reconciliation_test.go",
	"tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go",
	"tests/tier2_component/warmlayout/warm_layout_test.go",
	"tests/tier3_contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go",
	"tests/tier3_contract/adapter_jsonl/gateway_envelope_address_test.go",
	"tests/tier3_contract/adapter_jsonl/subprocess_envelope_address_test.go",
	"tests/tier3_contract/adapter_session_address/session_address_wire_test.go",
	"tests/tier3_contract/rest_sessions/slot_address_absence_test.go",
	"tests/tier7a_load_local/coordinator_hold_termination_race_test.go",
	"tests/tier7a_load_local/podmcp_arming_handoff_test.go",
}

// citedSectionRE matches one spec-section id at the head of a comma-separated
// item of a `// spec:` annotation. The id is anchored to the head of the item
// and must be followed by the opening parenthesis of its gloss or by the end
// of the item, so a number that appears inside a gloss ("a Phase 3 contract
// migration", "the latest 2 checkpoints") is not read as a citation.
var citedSectionRE = regexp.MustCompile(`^§?(\d+(?:\.\d+)*)(\s*\(|\s*$)`)

// annotatedSectionsInFile returns every spec-section id that the `// spec:`
// annotations anywhere in the given file name. An annotation may wrap across
// comment lines, so each one is joined with the contiguous comment lines
// below it up to the blank comment line, the `diagnosis:` tag, or the next
// annotation.
func annotatedSectionsInFile(t *testing.T, file string) map[string]bool {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	lines := strings.Split(string(body), "\n")
	out := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "// spec:") {
			continue
		}
		block := []string{strings.TrimSpace(strings.TrimPrefix(trimmed, "// spec:"))}
		j := i + 1
		for ; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(next, "//") {
				break
			}
			next = strings.TrimSpace(strings.TrimPrefix(next, "//"))
			if next == "" || strings.HasPrefix(next, "diagnosis:") || strings.HasPrefix(next, "spec:") {
				break
			}
			block = append(block, next)
		}
		i = j - 1
		for _, item := range strings.Split(strings.Join(block, " "), ",") {
			if m := citedSectionRE.FindStringSubmatch(strings.TrimSpace(item)); m != nil {
				out[m[1]] = true
			}
		}
	}
	return out
}

// specMapSectionsForFile returns the spec-section ids under which
// tests/spec-map.json registers the given file, whether the entry names the
// whole file or one of its cases with a `::TestName` selector.
func specMapSectionsForFile(t *testing.T, file string) (map[string]bool, map[string]bool) {
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
	declared := map[string]bool{}
	registered := map[string]bool{}
	for id, sec := range doc.Sections {
		declared[id] = true
		for _, entry := range sec.Tests {
			if entry == file || strings.HasPrefix(entry, file+"::") {
				registered[id] = true
			}
		}
	}
	return declared, registered
}

// spec: 28.5.3 (the session identifier addresses every session-scoped frame),
// 4.5 (one address per gRPC request), 6.1 (the per-session credential file),
// 6.4 (the per-slot workspace tree)
//
// Every case that landed with the per-session slot address contract is
// credited in tests/spec-map.json under each spec section its own annotation
// names. A section an annotation names but no map entry records has no test
// against it in the coverage view, and `lenny-test --spec <section>` does not
// select the case that pins it.
func TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate(t *testing.T) {
	for _, file := range slotAddressCaseFiles {
		declared, registered := specMapSectionsForFile(t, file)
		annotated := annotatedSectionsInFile(t, file)
		missing := map[string]bool{}
		for section := range annotated {
			if declared[section] && !registered[section] {
				missing[section] = true
			}
		}
		if len(missing) > 0 {
			t.Errorf("spec-map.json does not credit %s to section(s) %v its `// spec:` annotation names", file, sortedSectionIDs(missing))
		}
	}
}
