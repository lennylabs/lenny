// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the test files of the delegation tree
// membership and lifecycle work (proposal 0106) and tests/spec-map.json.
//
// The harness maps a specification section to the tests that verify it
// through tests/spec-map.json. A test whose `// spec:` annotation names a
// section that the map does not list it under is invisible to that mapping,
// so a section can read as covered by fewer tests than verify it. This check
// holds every test function in the work's test files to the map: each test
// carrying a `// spec:` annotation is listed as `<file>::<TestName>` under
// every section the annotation names that the map carries.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// delegationTreeTestFiles are the test files the delegation tree work added
// or changed.
var delegationTreeTestFiles = []string{
	"cmd/lenny-gateway/deadlock_fail_test.go",
	"pkg/delegation/recovery/onresult_test.go",
	"pkg/delegation/recovery/recovery_test.go",
	"pkg/gateway/mcpfabric/delegationtree/deadlock/manager_test.go",
	"pkg/gateway/mcpfabric/delegationtree/orphancleanup/dispositions_test.go",
	"pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup_test.go",
	"pkg/gateway/mcpfabric/delegationtree/orphancleanup/state_contract_test.go",
	"pkg/gateway/mcpfabric/delegationtree/orphancleanup/terminal_stamp_test.go",
	"pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery_test.go",
	"pkg/gateway/mcpfabric/mcptools/cancel_release_test.go",
	"pkg/gateway/sessionserver/bind_rollback_test.go",
	"pkg/gateway/sessionserver/cascade_on_expire_test.go",
	"pkg/gateway/sessionserver/delegated_child_materialize_test.go",
	"pkg/gateway/sessionserver/derive_failure_audit_test.go",
	"pkg/gateway/sessionserver/derive_failure_lineage_internal_test.go",
	"pkg/gateway/sessionserver/derive_test.go",
	"pkg/gateway/sessionserver/finalize_race_internal_test.go",
	"pkg/gateway/sessionserver/lifecycle_internal_test.go",
	"pkg/gateway/sessionserver/terminal_context_internal_test.go",
	"pkg/gateway/sessionserver/terminal_writer_inventory_test.go",
	"pkg/gateway/sessionserver/terminal_writers_internal_test.go",
	"pkg/gateway/sessionserver/tree_archive_test.go",
	"pkg/gateway/sessionserver/treerecovery_internal_test.go",
	"pkg/session/state/state_test.go",
	"tests/testinfra/lifecyclefence/paths_test.go",
	"tests/testinfra/orphanfixture/orphanfixture_test.go",
	"tests/tier11_docs/build_gaps_delegation_tree_records_currency_test.go",
	"tests/tier11_docs/delegation_tree_spec_map_currency_test.go",
	"tests/tier2_component/stores/sessionserver_orphan_cap_primary_test.go",
	"tests/tier2_component/stores/sessionstore_orphan_membership_test.go",
	"tests/tier4_integration/derived_session_root_test.go",
	"tests/tier7a_load_local/orphan_cascade_sweeper_race_test.go",
	"tests/tier7a_load_local/watchdog_replica_sweep_race_test.go",
	"tests/tier8_chaos/orphan_cascade_replica_stop_test.go",
	"tests/tier8_chaos/sweep_fault_continuation_test.go",
}

var (
	testFuncDecl    = regexp.MustCompile(`^func (Test\w+)\(t \*testing\.T\)`)
	sectionMarked   = regexp.MustCompile(`§\s*(\d+(?:\.\d+)*)`)
	sectionBare     = regexp.MustCompile(`(?:^|[\s,(;])(\d+(?:\.\d+)+)(?:$|[\s,);:])`)
	annotationLabel = regexp.MustCompile(`^//\s*\w+:`)
)

// specMapTests reads tests/spec-map.json into section -> set of test entries.
func specMapTests(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec-map: %v", err)
	}
	out := make(map[string]map[string]bool, len(doc.Sections))
	for id, s := range doc.Sections {
		set := make(map[string]bool, len(s.Tests))
		for _, e := range s.Tests {
			set[e] = true
		}
		out[id] = set
	}
	return out
}

// annotatedTest is a test function and the sections its `// spec:`
// annotation names.
type annotatedTest struct {
	name     string
	sections []string
}

// annotatedTests returns every top-level test function in src whose doc
// comment carries a `// spec:` annotation, with the sections it names.
func annotatedTests(src string) []annotatedTest {
	lines := strings.Split(src, "\n")
	var out []annotatedTest
	for i, l := range lines {
		m := testFuncDecl.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		start := i
		for start > 0 && strings.HasPrefix(lines[start-1], "//") {
			start--
		}
		if secs := specAnnotationSections(lines[start:i]); len(secs) > 0 {
			out = append(out, annotatedTest{name: m[1], sections: secs})
		}
	}
	return out
}

// specAnnotationSections returns the section numbers each `// spec:`
// paragraph in doc names. A paragraph runs from the `// spec:` line to the
// next blank comment line or the next labelled annotation. Sections written
// with a § mark are taken when the paragraph carries any; otherwise bare
// dotted numbers are taken.
func specAnnotationSections(doc []string) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < len(doc); i++ {
		line := strings.TrimSpace(doc[i])
		if !strings.HasPrefix(line, "// spec:") {
			continue
		}
		para := []string{strings.TrimPrefix(line, "// spec:")}
		for i+1 < len(doc) {
			next := strings.TrimSpace(doc[i+1])
			if next == "//" || annotationLabel.MatchString(next) {
				break
			}
			para = append(para, strings.TrimPrefix(next, "//"))
			i++
		}
		for _, s := range paragraphSections(strings.Join(para, " ")) {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func paragraphSections(text string) []string {
	var out []string
	matches := sectionMarked.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		matches = sectionBare.FindAllStringSubmatch(text, -1)
	}
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// diagnosis: a test function in one of the delegation tree work's test files
// carries a `// spec:` annotation naming a section that tests/spec-map.json
// does not list it under, so the harness's section-to-test mapping cannot see
// it. Add `<file>::<TestName>` to the tests of every section the annotation
// names, or correct the annotation when it names the wrong section.
//
// spec: §17.2 (test annotations and the spec map), §8.10 (Delegation Tree
// Recovery), §8.8 (TaskRecord and TaskResult Schema)
func TestDelegationTreeTestsAreMappedUnderTheirSections_spec_17_2(t *testing.T) {
	root := repoRoot(t)
	smap := specMapTests(t, root)
	for _, file := range delegationTreeTestFiles {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("read %s: %v", file, err)
			continue
		}
		for _, at := range annotatedTests(string(src)) {
			entry := file + "::" + at.name
			for _, sec := range at.sections {
				tests, ok := smap[sec]
				if !ok {
					continue // the map carries leaf sections only
				}
				if !tests[entry] {
					t.Errorf("tests/spec-map.json section %q does not list %s", sec, entry)
				}
			}
		}
	}
}

// diagnosis: the spec-annotation parser behind the spec-map currency check
// stopped reading a wrapped `// spec:` paragraph or a bare section list, so
// the check would pass tests it cannot see. Fix the parser.
//
// spec: §17.2 (test annotations and the spec map)
func TestSpecAnnotationSectionsReadsWrappedAndBareForms_spec_17_2(t *testing.T) {
	cases := []struct {
		name string
		doc  []string
		want []string
	}{
		{"marked wrapped", []string{"// spec: §12.6 (StoreRouter), §12.8 step 16,", "// §4.5 (Workspace lineage), §8.6", "//", "// diagnosis: x 9.9"}, []string{"12.6", "12.8", "4.5", "8.6"}},
		{"bare", []string{"// spec: 8.10 (Delegation Tree Recovery), 8.8 (TaskRecord and TaskResult", "// Schema), 7.1 (Session Lifecycle)"}, []string{"8.10", "8.8", "7.1"}},
		{"none", []string{"// diagnosis: §8.10"}, nil},
	}
	for _, c := range cases {
		got := specAnnotationSections(c.doc)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
