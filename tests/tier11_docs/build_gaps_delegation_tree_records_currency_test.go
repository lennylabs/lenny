// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the delegation tree
// membership and lifecycle work (proposal 0106) and the tree they describe.
//
// The build loops read BUILD-GAPS.md and the remediation plan as statements
// about the current tree. These checks hold the delegation tree records to it:
//
//   - Every finding the work resolved is closed with a proposal 0106
//     resolution naming the proposal, and every test function the resolution
//     names is declared in a test file the same resolution names.
//   - The parent-freshness finding the work handed to proposal 0104 is closed
//     on 0104's implementation, and its resolution names existing tests.
//   - The defect entries filed ahead of the work are confirmed in place, and
//     none still describes a symbol the work deleted as present.
//   - The deleted resume-window reader and tree-recovery expiry writer stay
//     deleted while the records say so.
//   - The remediation plan's P4a entry records the implementation and the
//     position it left.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// delegationTreeProposal is the directory of the proposal whose records these
// checks hold, as the records name it.
const delegationTreeProposal = "proposals/0106_fix_the-delegation-tree-s-membership-and-lifecycle-are-under/"

// delegationTreeNoteLabel opens the resolution the work added to each finding
// it resolved.
const delegationTreeNoteLabel = "**Resolution (proposal 0106"

// delegationTreeResolved are the findings the work resolved.
var delegationTreeResolved = []string{
	"F-8.2.30", "F-8.8.18", "F-8.8.19", "F-8.9.13", "F-8.10.19", "F-8.10.20",
	"F-8.10.21", "F-8.10.22", "F-8.10.23", "F-8.10.25", "F-8.10.26",
}

// delegationTreeFiledDefects are the entries filed from the work's list of
// defects it does not stage.
var delegationTreeFiledDefects = []string{"F-4.2.17", "F-4.5.21", "F-8.8.22", "F-8.10.28"}

// Tree paths whose deleted symbols the records describe as deleted.
const (
	treeRecoveryOrchestratorFile = "pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go"
	sessionTerminalFile          = "pkg/gateway/sessionserver/session_terminal.go"
)

// diagnosis: a finding the delegation tree work resolved was reopened, lost
// its proposal 0106 resolution, the resolution no longer names the proposal
// or a commit, names a test function that no test file it names declares, or
// names a proposal's change or step label. Restore the resolution with the
// current test names, or reopen the finding when the tree no longer holds the
// fix.
//
// spec: 8.10 (Delegation Tree Recovery), 8.8 (TaskRecord and TaskResult
// Schema), 7.1 (Session Lifecycle), 12.6 (Storage Architecture)
func TestDelegationTreeRecordsCloseTheirMembers_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range delegationTreeResolved {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if f.open || !strings.HasSuffix(strings.TrimSpace(f.heading), "— CLOSED") {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is not closed", f.line, f.heading)
			}
			para := labelledParagraph(f.body, delegationTreeNoteLabel)
			if para == "" || !strings.Contains(para, delegationTreeProposal) {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0106 resolution naming %s", f.line, f.heading, delegationTreeProposal)
			}
			if len(recordTestRefs(para)) == 0 {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names no test file", f.line, f.heading)
			}
			for _, m := range undeclaredRecordTests(root, para) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names %s, which no test file it names declares", f.line, f.heading, m)
			}
			if l := proposalScaffoldLabel.FindString(para); l != "" {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names the proposal label %q", f.line, f.heading, l)
			}
			if strings.Contains(f.body, "not yet implemented") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still describes proposal 0106 as not yet implemented", f.line, f.heading)
			}
		})
	}
}

// diagnosis: F-8.10.17 was reopened, lost the resolution that closes it on
// proposal 0104's implementation, or names a test that no longer exists; or
// it again says it waits for 0104. Restore the resolution, or reopen the
// finding when the creation fence no longer refuses a child of a terminal
// parent.
//
// spec: 8.10 (Delegation Tree Recovery), 8.2 (Delegation Mechanism), 12.8
// (Compliance Interfaces)
func TestDelegationTreeParentFreshnessClosesOnTheCreationFence_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	f := requireFinding(t, buildGapsFindingsByID(t, root), "F-8.10.17")
	if f.open || !strings.HasSuffix(strings.TrimSpace(f.heading), "— CLOSED") {
		t.Fatalf("BUILD-GAPS.md:%d %s\n  is not closed although proposal 0104 is implemented", f.line, f.heading)
	}
	para := labelledParagraph(f.body, "**Resolution (proposal 0104")
	if para == "" {
		t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0104 resolution", f.line, f.heading)
	}
	for _, w := range []string{podNoJWTProposal, "`DELEGATION_PARENT_REVOKED`", "orphan-cleanup sweeper"} {
		if !strings.Contains(para, w) {
			t.Errorf("BUILD-GAPS.md:%d %s\n  resolution does not mention %q", f.line, f.heading, w)
		}
	}
	if len(recordTestRefs(para)) == 0 {
		t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names no test file", f.line, f.heading)
	}
	for _, m := range undeclaredRecordTests(root, para) {
		t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names %s, which no test file it names declares", f.line, f.heading, m)
	}
	if strings.Contains(f.body, "The finding stays OPEN until 0104 is implemented.") {
		t.Errorf("BUILD-GAPS.md:%d %s\n  still says it waits for proposal 0104", f.line, f.heading)
	}
}

// diagnosis: a defect entry filed ahead of the delegation tree work still asks
// that work to update it, lost the record that it was confirmed in place, or
// describes a symbol the work deleted (remainingResumeWindow, expireSession)
// as present. Restore the confirmation and the past-tense evidence rather than
// filing a duplicate entry.
//
// spec: 8.10 (Delegation Tree Recovery), 4.2 (Session Manager), 4.5
// (Artifact Store), 16.1 (Metrics)
func TestDelegationTreeFiledDefectsAreConfirmed_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range delegationTreeFiledDefects {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if !f.open {
				if !strings.Contains(f.body, "**Resolution") {
					t.Fatalf("BUILD-GAPS.md:%d %s\n  is closed with no resolution", f.line, f.heading)
				}
				return
			}
			if strings.Contains(f.body, "implementor updates this entry") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still asks proposal 0106's implementation to update it", f.line, f.heading)
			}
			if !strings.Contains(f.body, "confirmed as the entry for this defect when 0106 was implemented") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not record its confirmation by proposal 0106's implementation", f.line, f.heading)
			}
			for _, stale := range []string{"after 0106's implementation", "once 0106 lands", "CODE-B deletes", "CODE-E moves"} {
				if strings.Contains(f.body, stale) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  still contains %q, which describes proposal 0106 as not yet landed", f.line, f.heading, stale)
				}
			}
		})
	}
}

// diagnosis: the tree regained the per-node resume-window reader or the
// tree-recovery expiry writer that the F-4.2.17, F-8.10.19, F-8.10.20, and
// F-8.10.28 records describe as deleted. Delete the symbol again, or reopen
// the findings and restate their evidence.
//
// spec: 8.10 (Delegation Tree Recovery), 6.2 (Pod State Machine)
func TestDelegationTreeDeletedSymbolsStayDeleted_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct{ file, symbol string }{
		{treeRecoveryOrchestratorFile, "func remainingResumeWindow("},
		{sessionTerminalFile, ") expireSession("},
	} {
		if strings.Contains(fileText(t, root, c.file), c.symbol) {
			t.Errorf("%s declares %q again, which the BUILD-GAPS records describe as deleted", c.file, c.symbol)
		}
	}
}

// diagnosis: the remediation plan's P4a entry lost the record of proposal
// 0106's implementation, the findings it closed, the F-8.10.17 closure on
// proposal 0104, or the position it left; or the filed-defect block still
// describes 0106 as pending. Restore the entry as the work left it.
//
// spec: 8.10 (Delegation Tree Recovery)
func TestDelegationTreePlanEntryRecordsTheImplementation_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	_, rest, ok := strings.Cut(plan, "  - **Outcome (P4a).** Proposal 0106")
	if !ok {
		t.Fatal("gateway-runtime-comms-remediation.md has no P4a outcome entry")
	}
	entry, _, _ := strings.Cut(rest, "  - **Outcome (P4b).**")
	for _, w := range []string{
		delegationTreeProposal,
		"Its implementation landed",
		"F-8.10.17 is closed on 0104's implementation",
		"`aab694190`",
		"Position:",
	} {
		if !strings.Contains(entry, w) {
			t.Errorf("the P4a entry does not mention %q", w)
		}
	}
	for _, id := range delegationTreeResolved {
		if !strings.Contains(entry, id) && !mentionedInRange(entry, id) {
			t.Errorf("the P4a entry does not name %s among the findings it closed", id)
		}
	}
	if l := proposalScaffoldLabel.FindString(entry); l != "" {
		t.Errorf("the P4a entry names the proposal label %q", l)
	}
	for _, stale := range []string{"remains and is being drafted as proposal 0107", "(P4a, approved\n2026-10-27, implementation pending)"} {
		if strings.Contains(plan, stale) {
			t.Errorf("gateway-runtime-comms-remediation.md still contains %q", stale)
		}
	}
}

// mentionedInRange reports whether text names id inside the closed range
// "F-8.10.19 to F-8.10.23", the one range the P4a entry uses.
func mentionedInRange(text, id string) bool {
	if !strings.Contains(text, "F-8.10.19 to F-8.10.23") {
		return false
	}
	switch id {
	case "F-8.10.20", "F-8.10.21", "F-8.10.22":
		return true
	}
	return false
}
