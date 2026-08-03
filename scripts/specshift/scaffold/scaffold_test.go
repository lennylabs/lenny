// SPDX-License-Identifier: MIT

// Unit cases for the scaffolding-label predicate. The specimens live in
// the fixtures under testdata/ rather than in this file, because this
// file sits inside the domain the tier-11 sweep reads and a specimen
// written here would be a site of the class the sweep reports.
//
// These cases carry no `// spec:` annotation. The rule they decide is
// owned by .claude/skills/implement-proposal/SKILL.md rather than by a
// numbered section under spec/.

package scaffold

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// readFixture returns the text of a fixture under testdata/.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return string(content)
}

// readWant returns the expected sites of a fixture, read from the golden
// file beside it. Each record is a line number and the label text,
// separated by a tab. The expectations sit under testdata/ for the same
// reason the specimens do.
func readWant(t *testing.T, name string) []Site {
	t.Helper()
	var want []Site
	for _, record := range strings.Split(strings.TrimRight(readFixture(t, name), "\n"), "\n") {
		field, text, found := strings.Cut(record, "\t")
		if !found {
			t.Fatalf("the golden record %q carries no tab between the line number and the label", record)
		}
		line, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("read the line number of the golden record %q: %v", record, err)
		}
		want = append(want, Site{Line: line, Text: text})
	}
	return want
}

// TestFindReportsEveryScaffoldingLabelForm asserts that each form a
// proposal numbers itself with is reported, together with the line it
// stands on, so a report names where the label has to be reworded.
func TestFindReportsEveryScaffoldingLabelForm(t *testing.T) {
	got := Find(readFixture(t, "labelled.txt"))
	assertSites(t, got, readWant(t, "labelled.want"))
}

// TestFindKeepsDurableReferences asserts the accept boundary: a
// specification citation, a step the specification itself numbers, a
// sub-step named by what it does, a cipher and store name that read like
// a bare label, the proposal path a commit may carry for traceability,
// and a register entry are all durable references and none of them is a
// site.
func TestFindKeepsDurableReferences(t *testing.T) {
	assertSites(t, Find(readFixture(t, "durable.txt")), nil)
}

// TestFindInProposalTextReadsBareLabelsOffTheProposalLine asserts that a
// text whose subject is a proposal, such as a commit message naming the
// proposal it implements, has its bare labels read on every line rather
// than on the line that names the proposal.
func TestFindInProposalTextReadsBareLabelsOffTheProposalLine(t *testing.T) {
	message := readFixture(t, "message.txt")
	if sites := Find(message); len(sites) != 0 {
		t.Errorf("the line-scoped read reported %d site(s) in a message whose only label stands away from the proposal line", len(sites))
	}
	assertSites(t, FindInProposalText(message), readWant(t, "message.want"))
}

// TestScanCommitsFailsOnASelectionThatReachedNoCommit asserts the
// vacuity boundary: a scan of an empty set fails rather than reporting a
// clean history. A caller whose revision selection stops reaching the
// commits that landed the work otherwise reads the empty scan as
// compliance, and every message added afterwards carries its labels past
// the check unreported.
func TestScanCommitsFailsOnASelectionThatReachedNoCommit(t *testing.T) {
	scan, err := ScanCommits(nil)
	if err == nil {
		t.Fatalf("an empty selection scanned %d message(s) without failing", scan.Read)
	}
	if scan.Read != 0 {
		t.Errorf("the failed scan reports %d message(s) read, want 0", scan.Read)
	}
}

// TestScanCommitsReportsTheLabelsOfEveryMessage asserts that a scan
// reads every message of the selection, reports the labels of the ones
// that carry them keyed by commit, and keeps a message that carries none
// out of the report.
func TestScanCommitsReportsTheLabelsOfEveryMessage(t *testing.T) {
	const labelled, clean = "a1b2c3", "d4e5f6"
	scan, err := ScanCommits(map[string]string{
		labelled: readFixture(t, "message.txt"),
		clean:    readFixture(t, "message-clean.txt"),
	})
	if err != nil {
		t.Fatalf("scan two commit messages: %v", err)
	}
	if scan.Read != 2 {
		t.Errorf("the scan reports %d message(s) read, want 2", scan.Read)
	}
	if _, reported := scan.Sites[clean]; reported {
		t.Errorf("the message carrying only durable references is reported as carrying %v", scan.Sites[clean])
	}
	assertSites(t, scan.Sites[labelled], readWant(t, "message.want"))
}

// assertSites compares the reported sites with the expected ones in
// order.
func assertSites(t *testing.T, got, want []Site) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("reported %d site(s), want %d: %v", len(got), len(want), got)
	}
	for i, site := range got {
		if site != want[i] {
			t.Errorf("site %d is %v, want %v", i, site, want[i])
		}
	}
}
