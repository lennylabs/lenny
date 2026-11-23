// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// These tests cover the coverage gate, which is contributor tooling over
// the specification structure rather than a platform behavior, so they
// carry no specification annotation. They build on the digest fixture:
// its branch adds POOL-R3 with no test, modifies the text of POOL-R1,
// which pkg/pool/pool_test.go cites and the open issue POOL-I1 lists,
// retires POOL-R2, and modifies an operation and a definition block.

// coverageOf runs the gate on the fixture and fails the test on an error.
func coverageOf(t *testing.T, f *fixture) CoverageReport {
	t.Helper()
	rep, err := CheckCoverage(context.Background(), f.root)
	if err != nil {
		t.Fatalf("check coverage: %v", err)
	}
	return rep
}

// requirement returns the report's entry for an identifier.
func requirement(t *testing.T, rep CoverageReport, id string) RequirementCoverage {
	t.Helper()
	for _, c := range rep.Requirements {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("the report holds no entry for %s: %+v", id, rep.Requirements)
	return RequirementCoverage{}
}

// uncoveredIDs returns the identifiers the report leaves uncovered.
func uncoveredIDs(rep CoverageReport) string {
	var ids []string
	for _, c := range rep.Uncovered() {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ",")
}

func TestCoverageRefusesAnAddedRequirementWithNoCitingTestOrIssue(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rep := coverageOf(t, f)
	if rep.Base != f.base || rep.BaseRef != "main" {
		t.Errorf("the report compared with %s (%s); want %s (main)", rep.Base, rep.BaseRef, f.base)
	}
	var ids []string
	for _, c := range rep.Requirements {
		ids = append(ids, c.ID+" "+c.Change)
	}
	// The retired POOL-R2, the operation, and the definition blocks are
	// outside the gate.
	if got := strings.Join(ids, ","); got != "POOL-R3 added,POOL-R1 modified" {
		t.Errorf("the gate checked %q; want the added POOL-R3 and the modified POOL-R1", got)
	}
	if got := uncoveredIDs(rep); got != "POOL-R3" {
		t.Fatalf("uncovered %q; want POOL-R3", got)
	}
	msg := rep.Uncovered()[0].String()
	mustContain(t, msg, "POOL-R3 (added on spec/capabilities/pools/warm-pools.md)", "`// spec: POOL-R3`", "issues/")
}

func TestCoverageCountsTheTestsAndIssuesOfAModifiedRequirement(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := requirement(t, coverageOf(t, f), "POOL-R1")
	if strings.Join(c.Tests, ",") != "pkg/pool/pool_test.go::TestPoolKeepsWarmPods" {
		t.Errorf("tests %v; want the one test that cites POOL-R1", c.Tests)
	}
	// The closed POOL-I2 also lists POOL-R1 and does not count.
	if strings.Join(c.Issues, ",") != "issues/POOL/POOL-I1-too-few-pods.md" {
		t.Errorf("issues %v; want the open POOL-I1 alone", c.Issues)
	}
	if !c.Covered() {
		t.Error("a requirement with a citing test reads as uncovered")
	}
}

func TestCoveragePassesAnAddedRequirementThatAnUncommittedTestCites(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// The gate reads the working tree, as the annotation lint does, so an
	// untracked test counts, and so does one identifier of several in an
	// annotation.
	f.write(map[string]string{"pkg/pool/drain_test.go": `// SPDX-License-Identifier: MIT

package pool

import "testing"

// spec: POOL-R1, POOL-R3
func TestPoolDrainsOnDeletion(t *testing.T) {}
`})
	rep := coverageOf(t, f)
	if got := uncoveredIDs(rep); got != "" {
		t.Errorf("uncovered %q; want none", got)
	}
	if c := requirement(t, rep, "POOL-R3"); strings.Join(c.Tests, ",") != "pkg/pool/drain_test.go::TestPoolDrainsOnDeletion" {
		t.Errorf("POOL-R3 tests %v", c.Tests)
	}
}

func TestCoverageCountsOnlyATestFunctionsDocComment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"a helper in a test file": `// spec: POOL-R3
func drainHelper() {}
`,
		"an annotation separated from its test by a blank line": `// spec: POOL-R3

func TestPoolDrainsOnDeletion(t *testing.T) {}
`,
		"a section citation of a numbered heading": `// spec: §1 (Numbered)
func TestPoolDrainsOnDeletion(t *testing.T) {}
`,
		"an annotation inside the test body": `func TestPoolDrainsOnDeletion(t *testing.T) {
	// spec: POOL-R3
}
`,
		"a test under testdata": "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			file := "pkg/pool/drain_test.go"
			src := "// SPDX-License-Identifier: MIT\n\npackage pool\n\nimport \"testing\"\n\n" + body
			if body == "" {
				file = "pkg/pool/testdata/drain_test.go"
				src = "// SPDX-License-Identifier: MIT\n\npackage pool\n\nimport \"testing\"\n\n// spec: POOL-R3\nfunc TestPoolDrainsOnDeletion(t *testing.T) {}\n"
			}
			f.write(map[string]string{file: src})
			if got := uncoveredIDs(coverageOf(t, f)); got != "POOL-R3" {
				t.Errorf("uncovered %q; want POOL-R3, because %s does not cite it from a test function", got, name)
			}
		})
	}
}

func TestCoverageCountsAnOpenOrPlannedIssueThatListsTheRequirement(t *testing.T) {
	t.Parallel()
	issue := func(status string) string {
		return "---\nid: POOL-I3\n" + status + "kind: test-gap\nrequirements: [KLABEL-R2, POOL-R3]\n---\n\n# Draining has no test\n"
	}
	cases := []struct {
		name, path, content string
		covers              bool
	}{
		{"open", "issues/POOL/POOL-I3-drain.md", issue("status: open\n"), true},
		{"planned", "issues/POOL/POOL-I3-drain.md", issue("status: planned\nchange: 0200.S2\n"), true},
		{"no status, which is open", "issues/POOL/POOL-I3-drain.md", issue(""), true},
		{"under another page's code", "issues/ADMPOOLS/ADMPOOLS-I1-drain.md", issue("status: open\n"), true},
		{"under a folder that is no page code", "issues/tmp/waiver.md", issue("status: open\n"), false},
		{"closed", "issues/POOL/POOL-I3-drain.md", issue("status: closed\nclosed: fixed\n"), false},
		{"filed under issues/closed", "issues/closed/POOL/POOL-I3-drain.md", issue("status: open\n"), false},
		{"named in the text only", "issues/POOL/POOL-I3-drain.md", "---\nid: POOL-I3\nstatus: open\nrequirements: [POOL-R1]\n---\n\n# POOL-R3 has no test\n", false},
		{"with no front matter", "issues/POOL/POOL-I3-drain.md", "# POOL-R3 has no test\n", false},
		{"a file that is not a record", "issues/POOL/notes.txt", issue("status: open\n"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.write(map[string]string{tc.path: tc.content})
			c := requirement(t, coverageOf(t, f), "POOL-R3")
			if c.Covered() != tc.covers {
				t.Errorf("an issue %s: covered %v, want %v (issues %v)", tc.name, c.Covered(), tc.covers, c.Issues)
			}
			if tc.covers && strings.Join(c.Issues, ",") != tc.path {
				t.Errorf("issues %v; want %s", c.Issues, tc.path)
			}
		})
	}
}

func TestCoverageRefusesAModifiedRequirementWithNoCitingTestOrIssue(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Remove the one test that cites POOL-R1 and close its open issue.
	if err := os.Remove(filepath.Join(f.root, "pkg", "pool", "pool_test.go")); err != nil {
		t.Fatal(err)
	}
	f.write(map[string]string{"issues/POOL/POOL-I1-too-few-pods.md": strings.Replace(openIssue, "status: open", "status: closed", 1)})
	rep := coverageOf(t, f)
	if got := uncoveredIDs(rep); got != "POOL-R3,POOL-R1" {
		t.Errorf("uncovered %q; want the added POOL-R3 and the modified POOL-R1", got)
	}
}

func TestCoverageReadsTheIssueStatusAsWritten(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// The coverage view of lenny-test reads `Open` as neither open nor
	// planned, and the gate reads the record the same way.
	f.write(map[string]string{"issues/POOL/POOL-I3-drain.md": "---\nid: POOL-I3\nstatus: Open\nrequirements: [POOL-R3]\n---\n"})
	if c := requirement(t, coverageOf(t, f), "POOL-R3"); c.Covered() {
		t.Errorf("an issue with status Open covers POOL-R3: %v", c.Issues)
	}
}

// skippedNaming reports whether one of the report's skipped records names
// a path.
func skippedNaming(rep CoverageReport, p string) bool {
	for _, s := range rep.Skipped {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func TestCoverageSkipsAnIssueRecordWhoseFrontMatterDoesNotParse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// The record names POOL-R3 and does not parse: it covers nothing and
	// is reported, and the open POOL-I1 still covers POOL-R1, so one bad
	// record does not stop the requirements it does not name.
	p := "issues/POOL/POOL-I3-drain.md"
	f.write(map[string]string{p: "---\nid: POOL-I3\nrequirements: [POOL-R3\n---\n\n# Broken\n"})
	rep := coverageOf(t, f)
	if got := uncoveredIDs(rep); got != "POOL-R3" {
		t.Errorf("uncovered %q; want POOL-R3", got)
	}
	if !skippedNaming(rep, "parse the front matter of "+p) {
		t.Errorf("skipped %v; want the malformed record named", rep.Skipped)
	}
}

func TestCoverageLeavesOutAReservedIssueCodeWithoutAReport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// GATE belongs to no page: its record covers nothing and is not
	// reported, while a folder that is neither a page code nor reserved
	// is reported.
	f.write(map[string]string{
		"issues/GATE/GATE-I1-lint.md":  "---\nid: GATE-I1\nstatus: open\nrequirements: [POOL-R3]\n---\n",
		"issues/NOPAGE/NOPAGE-I1-x.md": "---\nid: NOPAGE-I1\nstatus: open\nrequirements: [POOL-R3]\n---\n",
	})
	rep := coverageOf(t, f)
	if got := uncoveredIDs(rep); got != "POOL-R3" {
		t.Errorf("uncovered %q; want POOL-R3, because neither record names a page code", got)
	}
	if skippedNaming(rep, "issues/GATE/") {
		t.Errorf("skipped %v; a reserved issue code is reported", rep.Skipped)
	}
	if !skippedNaming(rep, "issues/NOPAGE/NOPAGE-I1-x.md") {
		t.Errorf("skipped %v; want the record under an unregistered code named", rep.Skipped)
	}
}

func TestCoverageSkipsASymlinkedIssueRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "POOL-I3.md")
	if err := os.WriteFile(outside, []byte("---\nid: POOL-I3\nstatus: open\nrequirements: [POOL-R3]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "issues", "POOL", "POOL-I3-drain.md")); err != nil {
		t.Fatal(err)
	}
	rep := coverageOf(t, f)
	if got := uncoveredIDs(rep); got != "POOL-R3" {
		t.Errorf("uncovered %q; want POOL-R3, because a record outside the tree cannot cover a requirement", got)
	}
	if !skippedNaming(rep, "issues/POOL/POOL-I3-drain.md") {
		t.Errorf("skipped %v; want the symbolic link named", rep.Skipped)
	}
}

func TestCoverageCountsOnlyTestsTheGoToolBuilds(t *testing.T) {
	t.Parallel()
	test := func(constraint string) string {
		return "// SPDX-License-Identifier: MIT\n\n" + constraint + "package pool\n\nimport \"testing\"\n\n// spec: POOL-R3\nfunc TestPoolDrainsOnDeletion(t *testing.T) {}\n"
	}
	cases := []struct {
		name, path, content string
		covers              bool
	}{
		{"a tier's build tag", "pkg/pool/drain_test.go", test("//go:build component\n\n"), true},
		{"a negated tag", "pkg/pool/drain_test.go", test("//go:build !windows\n\n"), true},
		{"the ignore tag", "pkg/pool/drain_test.go", test("//go:build ignore\n\n"), false},
		{"ignore with another tag", "pkg/pool/drain_test.go", test("//go:build ignore && component\n\n"), false},
		{"a contradiction", "pkg/pool/drain_test.go", test("//go:build component && !component\n\n"), false},
		{"a folder that opens with an underscore", "pkg/pool/_old/drain_test.go", test(""), false},
		{"a folder that opens with a dot", "pkg/pool/.old/drain_test.go", test(""), false},
		{"a nested module", "tools/lint/drain_test.go", test(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.write(map[string]string{tc.path: tc.content, "tools/lint/go.mod": "module example.com/lint\n\ngo 1.22\n"})
			if c := requirement(t, coverageOf(t, f), "POOL-R3"); c.Covered() != tc.covers {
				t.Errorf("a test under %s: covered %v, want %v (tests %v)", tc.name, c.Covered(), tc.covers, c.Tests)
			}
		})
	}
}

func TestBuildableReadsTheConstraintBeforeThePackageClause(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"package pool\n": true,
		"// Copyright.\n\n//go:build ignore\n\npackage pool\n":                                     false,
		"/* a\nblock */\n//go:build ignore\npackage pool\n":                                        false,
		"package pool\n\n//go:build ignore\n":                                                      true,
		"//go:build (\n\npackage pool\n":                                                           true,
		"//go:build a || b || c || d || e || f || g || h || i || j || k || l || m\npackage pool\n": true,
	}
	for src, want := range cases {
		if got := buildable([]byte(src)); got != want {
			t.Errorf("buildable(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestCoverageLeavesOutARequirementThatMovesWithItsTextUnchanged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Restore the base text of every item, then move the page: POOL-R1
	// changes page and keeps its text, and POOL-R2 is back.
	if err := os.Remove(filepath.Join(f.root, "spec", "capabilities", "pools", "warm-pools.md")); err != nil {
		t.Fatal(err)
	}
	f.write(map[string]string{"spec/capabilities/pooling/warm-pools.md": warmPoolsBase})
	rep := coverageOf(t, f)
	if len(rep.Requirements) != 0 {
		t.Errorf("the gate checked %+v; want nothing, because no requirement's text changed", rep.Requirements)
	}
	sc, err := CompareSpec(context.Background(), f.root, f.base)
	if err != nil {
		t.Fatal(err)
	}
	moved := false
	for _, c := range sc.Changes {
		moved = moved || (c.Item().ID == "POOL-R1" && c.Moved())
	}
	if !moved {
		t.Error("the fixture does not move POOL-R1, so the case tests nothing")
	}
}

func TestCoverageOfABranchThatChangesNoRequirementIsEmpty(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.git("checkout", "-q", "main")
	f.git("checkout", "-q", "-b", "change/0200.S2-nothing")
	f.write(map[string]string{"pkg/other/other.go": "// SPDX-License-Identifier: MIT\n\npackage other\n\n// Other does nothing.\nfunc Other() {}\n"})
	rep := coverageOf(t, f)
	if len(rep.Requirements) != 0 || len(rep.Uncovered()) != 0 {
		t.Errorf("the gate checked %+v on a branch that changes no requirement", rep.Requirements)
	}
}

func TestCoverageFailsWhenMainSharesNoHistoryWithTheBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// An orphan branch shares no history with main. annotation.MergeBase
	// returns ErrNoBase only when no integration ref exists, so the gate
	// fails here rather than skipping, since a skip counts as a pass.
	f.git("checkout", "-q", "--orphan", "unrelated")
	f.commit("unrelated history", "2026-03-01T12:00:00")
	if _, err := CheckCoverage(context.Background(), f.root); err == nil || errors.Is(err, annotation.ErrNoBase) {
		t.Errorf("err %v; want an error other than annotation.ErrNoBase", err)
	}
}

func TestTextChangedReadsAddedAndModifiedTextOnly(t *testing.T) {
	t.Parallel()
	a := &Item{Kind: KindRequirement, ID: "POOL-R1", File: "a.md", Text: "one"}
	cases := []struct {
		name string
		c    ItemChange
		want bool
	}{
		{"added", ItemChange{Change: Added, New: a}, true},
		{"text modified", ItemChange{Change: Modified, Old: a, New: &Item{ID: "POOL-R1", File: "a.md", Text: "two"}}, true},
		{"moved with the same text", ItemChange{Change: Modified, Old: a, New: &Item{ID: "POOL-R1", File: "b.md", Text: "one"}}, false},
		{"moved with its text changed", ItemChange{Change: Modified, Old: a, New: &Item{ID: "POOL-R1", File: "b.md", Text: "two"}}, true},
		{"retired", ItemChange{Change: Retired, Old: a}, false},
	}
	for _, tc := range cases {
		if got := textChanged(tc.c); got != tc.want {
			t.Errorf("%s: textChanged %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIgnoredPathFollowsTheGoToolsFolderRules(t *testing.T) {
	t.Parallel()
	modules := []string{"tools/lint"}
	cases := map[string]bool{
		"pkg/pool/drain_test.go":       false,
		"pkg/pool/_old/drain_test.go":  true,
		"pkg/pool/.old/drain_test.go":  true,
		"pkg/pool/_drain_test.go":      true,
		"tools/lint/drain_test.go":     true,
		"tools/linter/drain_test.go":   false,
		"tools/lint/sub/drain_test.go": true,
	}
	for p, want := range cases {
		if got := ignoredPath(p, modules); got != want {
			t.Errorf("ignoredPath(%q) = %v, want %v", p, got, want)
		}
	}
}
