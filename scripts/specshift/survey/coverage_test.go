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
		{"under another page's code", "issues/CLAIM/CLAIM-I1-drain.md", issue("status: open\n"), true},
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

func TestCoverageRefusesAnIssueRecordWhoseFrontMatterDoesNotParse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(map[string]string{"issues/POOL/POOL-I3-drain.md": "---\nid: POOL-I3\nrequirements: [POOL-R3\n---\n\n# Broken\n"})
	_, err := CheckCoverage(context.Background(), f.root)
	if err == nil || !strings.Contains(err.Error(), "issues/POOL/POOL-I3-drain.md") {
		t.Errorf("err %v; want an error that names the malformed record", err)
	}
}

func TestCoverageRefusesASymlinkedIssueRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "POOL-I3.md")
	if err := os.WriteFile(outside, []byte("---\nid: POOL-I3\nstatus: open\nrequirements: [POOL-R3]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "issues", "POOL", "POOL-I3-drain.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckCoverage(context.Background(), f.root); err == nil || !strings.Contains(err.Error(), "POOL-I3-drain.md") {
		t.Errorf("err %v; want the symbolic link refused, so a record outside the tree cannot cover a requirement", err)
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

func TestCoverageReturnsErrNoBaseWithoutAMergeBase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// An orphan branch shares no history with main.
	f.git("checkout", "-q", "--orphan", "unrelated")
	f.commit("unrelated history", "2026-03-01T12:00:00")
	if _, err := CheckCoverage(context.Background(), f.root); !errors.Is(err, annotation.ErrNoBase) {
		t.Errorf("err %v; want annotation.ErrNoBase", err)
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
		{"retired", ItemChange{Change: Retired, Old: a}, false},
	}
	for _, tc := range cases {
		if got := textChanged(tc.c); got != tc.want {
			t.Errorf("%s: textChanged %v, want %v", tc.name, got, tc.want)
		}
	}
}
