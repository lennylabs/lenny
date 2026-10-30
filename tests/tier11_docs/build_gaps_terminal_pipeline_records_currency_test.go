// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the terminal pipeline
// work (proposal 0100) and the tree they describe.
//
// The build loops read BUILD-GAPS.md as a statement about the current tree.
// These checks hold the terminal pipeline records to it:
//
//   - Every finding the work closed stays closed with a proposal 0100
//     resolution, and every test function a resolution names is declared in a
//     test file the same resolution names.
//   - The accepted departures the work recorded stay on their findings, cite
//     the specification text that states them, and name a pinning test.
//   - The findings the work left open keep the note that moves their owner or
//     points at the finding that owns the rest.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// terminalPipelineProposal is the directory of the proposal whose records
// these checks hold, as the records name it.
const terminalPipelineProposal = "proposals/0100_fix_the-terminal-pipeline-what-happens-between-a-session-s/"

// interReplicaCarrierOwner names the owner the work moved its deferred
// findings to.
const interReplicaCarrierOwner = "inter-replica carrier proposal"

// terminalPipelineClosed are the findings the terminal pipeline work closed.
var terminalPipelineClosed = []string{
	"F-7.1.21", "F-7.1.22", "F-7.1.23", "F-7.1.24", "F-7.1.25", "F-7.1.26",
	"F-7.1.27", "F-7.1.28", "F-7.1.29", "F-7.2.29", "F-7.2.30", "F-7.3.32",
	"F-10.1.27", "F-11.2.30", "F-11.7.22", "F-12.8.56", "F-15.1.42", "F-15.1.44",
}

// recordTestName matches a test function name in a record.
var recordTestName = regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)

// proposalScaffoldLabel matches a change, test, or record label that names a
// part of a proposal document. A BUILD-GAPS.md reader cannot look one up, so
// a resolution names the code site, the specification text, or the test
// instead.
var proposalScaffoldLabel = regexp.MustCompile(`\b(?:CODE|SPEC|TEST|DOC|REC)-\d+\b|\bstep[- ]\d+\b`)

// labelledParagraph returns the line of body that opens with label, or ""
// when body has none.
func labelledParagraph(body, label string) string {
	_, rest, ok := strings.Cut(body, label)
	if !ok {
		return ""
	}
	para, _, _ := strings.Cut(rest, "\n")
	return label + para
}

// undeclaredRecordTests returns each test function name in para that no test
// file para names declares, and each named test file that does not exist.
func undeclaredRecordTests(root, para string) []string {
	var sources []string
	var missing []string
	for _, ref := range recordTestRefs(para) {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref[0])))
		if err != nil {
			missing = append(missing, ref[0])
			continue
		}
		sources = append(sources, string(src))
	}
	for _, name := range recordTestName.FindAllString(para, -1) {
		declared := false
		for _, src := range sources {
			if strings.Contains(src, "func "+name+"(") {
				declared = true
				break
			}
		}
		if !declared {
			missing = append(missing, name)
		}
	}
	return missing
}

// diagnosis: a finding the terminal pipeline work closed was reopened, lost
// its proposal 0100 resolution, names a test function that no test file the
// resolution names declares, or names a proposal's change or step label.
// Restore the resolution with the current test names, or reopen the finding
// when the tree no longer holds the fix.
//
// spec: 7.1 (Normal Flow), 12.3 (Postgres HA Requirements)
func TestTerminalPipelineRecordsCloseTheirMembers_spec_7_1(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range terminalPipelineClosed {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if f.open || !strings.HasSuffix(strings.TrimSpace(f.heading), "— CLOSED") {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is not closed", f.line, f.heading)
			}
			para := labelledParagraph(f.body, "**Resolution (proposal 0100")
			if para == "" || !strings.Contains(para, terminalPipelineProposal) {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0100 resolution naming %s", f.line, f.heading, terminalPipelineProposal)
			}
			for _, m := range undeclaredRecordTests(root, para) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names %s, which no test file it names declares", f.line, f.heading, m)
			}
			if l := proposalScaffoldLabel.FindString(para); l != "" {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names the proposal label %q", f.line, f.heading, l)
			}
		})
	}
}

// diagnosis: an accepted departure of the terminal pipeline work was dropped,
// no longer cites the specification text that states it, or no longer names a
// pinning test that exists. Restore the departure as the work recorded it.
//
// spec: 10.1.7 (preStop Hook Drain), 12.3 (Postgres HA Requirements)
func TestTerminalPipelineDeparturesAreRecorded_spec_10_1_7(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range []struct {
		id   string
		cite string
	}{
		{"F-10.1.27", "**Terminal pipelines at process exit.**"},
		{"F-7.2.29", "§12.3 durability-table row for a lost commit acknowledgement"},
	} {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			para := labelledParagraph(f.body, "**Accepted departure (proposal 0100")
			if para == "" {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0100 accepted departure", f.line, f.heading)
			}
			if !strings.Contains(para, c.cite) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  departure does not cite %q", f.line, f.heading, c.cite)
			}
			if len(recordTestRefs(para)) == 0 {
				t.Errorf("BUILD-GAPS.md:%d %s\n  departure names no pinning test file", f.line, f.heading)
			}
			for _, m := range undeclaredRecordTests(root, para) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  departure names %s, which no test file it names declares", f.line, f.heading, m)
			}
		})
	}
}

// diagnosis: a finding the terminal pipeline work left open was closed with
// no resolution, lost the note that moves its owner or points at the finding
// that owns the rest, or still names P2 as its owner. Restore the note, or
// record the later change that closed the finding.
//
// spec: 12.8 (Compliance Interfaces), 11.2 (Budgets and Quotas), 5.2 (Pool
// Configuration and Execution Modes)
func TestTerminalPipelineOpenFindingsKeepTheirNotes_spec_12_8(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range []struct {
		id    string
		label string
		want  []string
	}{
		{"F-12.8.43", "**Annotation (proposal 0100", []string{"`ReleaseTerminalBinding`", "credential leases", "one checkpoint interval"}},
		{"F-10.1.33", "**Deferral note (proposal 0100", []string{interReplicaCarrierOwner}},
		{"F-11.2.31", "**Pointer note (proposal 0100", []string{"F-11.2.30", "crash window"}},
		{"F-5.2.57", "**Spec:**", []string{"pool admission refuses a pool"}},
	} {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is closed with no resolution", f.line, f.heading)
			}
			para := labelledParagraph(f.body, c.label)
			if para == "" {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no paragraph opening %q", f.line, f.heading, c.label)
			}
			for _, w := range c.want {
				if !strings.Contains(para, w) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  %s paragraph does not mention %q", f.line, f.heading, c.label, w)
				}
			}
			for _, m := range undeclaredRecordTests(root, para) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  names %s, which no test file it names declares", f.line, f.heading, m)
			}
		})
	}
	f := requireFinding(t, findings, "F-12.8.43")
	if f.open {
		if strings.Contains(f.body, "Owned by proposal P2") || !strings.Contains(f.body, "Owned by the "+interReplicaCarrierOwner) {
			t.Errorf("BUILD-GAPS.md:%d %s\n  does not name the %s as its owner", f.line, f.heading, interReplicaCarrierOwner)
		}
	}
}

// diagnosis: the record parsers above no longer read a paragraph or a test
// reference the way the checks assume, so they pass or fail vacuously.
//
// spec: 7.1 (Normal Flow)
func TestTerminalPipelineRecordParsers_spec_7_1(t *testing.T) {
	body := "**Spec:** x\n**Resolution (proposal 0100, d):** y `pkg/p/a_test.go` TestOne.\n**Other:** z\n"
	if got := labelledParagraph(body, "**Resolution (proposal 0100"); got != "**Resolution (proposal 0100, d):** y `pkg/p/a_test.go` TestOne." {
		t.Errorf("labelledParagraph = %q", got)
	}
	if labelledParagraph(body, "**Absent") != "" {
		t.Error("labelledParagraph matched an absent label")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "p"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "p", "a_test.go"), []byte("package p\nfunc TestOne(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := undeclaredRecordTests(root, "`pkg/p/a_test.go` names TestOne."); len(m) != 0 {
		t.Errorf("undeclaredRecordTests reported %v for a declared test", m)
	}
	m := undeclaredRecordTests(root, "`pkg/p/a_test.go` names TestTwo and `pkg/p/b_test.go`.")
	if strings.Join(m, ",") != "pkg/p/b_test.go,TestTwo" {
		t.Errorf("undeclaredRecordTests = %v, want the missing file and the undeclared test", m)
	}
	for _, s := range []string{"CODE-3", "SPEC-5", "TEST-1", "step 4"} {
		if !proposalScaffoldLabel.MatchString(s) {
			t.Errorf("proposalScaffoldLabel does not match %q", s)
		}
	}
	if proposalScaffoldLabel.MatchString("F-7.1.21 and §12.3") {
		t.Error("proposalScaffoldLabel matched a finding identifier or a section number")
	}
}
