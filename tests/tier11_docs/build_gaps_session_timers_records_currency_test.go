// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the per-state session
// deadline and watchdog sweep work (proposal 0105) and the tree they describe.
//
// The build loops read BUILD-GAPS.md and the remediation plan as statements
// about the current tree. These checks hold the session timer records to it:
//
//   - Every finding the work resolved carries a proposal 0105 resolution note
//     naming the proposal, and every test function a note names is declared
//     in a test file the same note names.
//   - The notes that carry a specific record (the F-8.3.1 premise, the
//     F-11.3.39 emission deferral, and the F-11.3.40 split) keep it.
//   - The findings the work left open stay open with their owner notes.
//   - The defect entries filed ahead of the work are confirmed in place, and
//     the remediation plan's P3 entry states the replica assignment that
//     landed.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// sessionTimersProposal is the directory of the proposal whose records these
// checks hold, as the records name it.
const sessionTimersProposal = "proposals/0105_fix_the-per-state-session-deadlines-and-the-watchdog-sweeps/"

// sessionTimersNoteLabel opens the resolution the work added to each finding
// it resolved. The label read "**Resolution note" while the findings were
// open and became the closed-entry "**Resolution" label when they closed.
const sessionTimersNoteLabel = "**Resolution (proposal 0105"

// sessionTimersResolved are the findings the work resolved.
var sessionTimersResolved = []string{
	"F-6.2.28", "F-7.2.31", "F-8.3.1", "F-10.1.34", "F-11.3.37", "F-11.3.38",
	"F-11.3.39", "F-11.3.40", "F-11.3.41", "F-11.3.42", "F-11.3.43",
}

// sessionTimersFiledDefects are the entries filed from the work's list of
// defects it does not stage.
var sessionTimersFiledDefects = []string{
	"F-5.2.58", "F-7.1.32", "F-7.2.45", "F-8.2.32", "F-9.1.12", "F-11.3.45",
	"F-11.3.46", "F-15.1.61", "F-15.1.62", "F-15.4.5",
}

// diagnosis: a finding the session timer work resolved lost its proposal 0105
// resolution note, the note no longer names the proposal, names a test
// function that no test file the note names declares, or names a proposal's
// change or step label. Restore the note with the current test names, or
// reopen the finding when the tree no longer holds the fix.
//
// spec: 6.2 (Pod State Machine), 10.1.1 (Stateless Replicas and Per-Session
// Coordination), 11.3 (Timeouts and Cancellation)
func TestSessionTimersRecordsResolveTheirMembers_spec_6_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range sessionTimersResolved {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			para := labelledParagraph(f.body, sessionTimersNoteLabel)
			if para == "" || !strings.Contains(para, sessionTimersProposal) {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0105 resolution note naming %s", f.line, f.heading, sessionTimersProposal)
			}
			if len(recordTestRefs(para)) == 0 {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution note names no test file", f.line, f.heading)
			}
			for _, m := range undeclaredRecordTests(root, para) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution note names %s, which no test file it names declares", f.line, f.heading, m)
			}
			if l := proposalScaffoldLabel.FindString(para); l != "" {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution note names the proposal label %q", f.line, f.heading, l)
			}
		})
	}
}

// diagnosis: a resolution note lost the specific record it carries: that the
// F-8.3.1 "stored nowhere" premise was already false, that the F-11.3.39 fix
// discharges the status_change deferral of F-7.1.11 and F-7.2.1, or that the
// F-11.3.40 expiry-warning half moved to F-11.3.46. Restore the sentence.
//
// spec: 8.3 (Delegation Policy and Lease), 7.2 (Interactive Session Model),
// 11.3 (Timeouts and Cancellation)
func TestSessionTimersNotesKeepTheirRecords_spec_8_3(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range []struct {
		id   string
		want []string
	}{
		{"F-8.3.1", []string{"premise was already false", "`storeLeaseFromSlice`", "F-8.2.2", "F-8.2.32"}},
		{"F-11.3.39", []string{"`status_change(input_required)`", "`status_change(running)`", "F-7.1.11", "F-7.2.1"}},
		{"F-11.3.40", []string{"sweep-write half", "F-11.3.46"}},
		{"F-6.2.28", []string{"without a code change", "`readResumable`"}},
		{"F-10.1.34", []string{"§10.1.4 orphan-session reconciler", "`leaderGate`"}},
	} {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			para := labelledParagraph(f.body, sessionTimersNoteLabel)
			for _, w := range c.want {
				if !strings.Contains(para, w) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  resolution note does not mention %q", f.line, f.heading, w)
				}
			}
		})
	}
}

// diagnosis: a finding the session timer work left open was closed with no
// resolution, or lost the note recording that proposal 0105 does not stage it
// and that its owner is unassigned. Restore the note, or record the later
// change that closed the finding.
//
// spec: 11.3 (Timeouts and Cancellation), 7.2 (Interactive Session Model)
func TestSessionTimersOpenFindingsKeepTheirNotes_spec_11_3(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range []struct {
		id   string
		want []string
	}{
		{"F-7.2.43", []string{"Owner: unassigned", "does not stage it"}},
		{"F-5.2.57", []string{"**Note (proposal 0105", "does not stage this finding", "owner is unassigned"}},
		{"F-10.1.36", nil},
	} {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if !f.open {
				if !strings.Contains(f.body, "**Resolution") {
					t.Fatalf("BUILD-GAPS.md:%d %s\n  is closed with no resolution", f.line, f.heading)
				}
				return
			}
			for _, w := range c.want {
				if !strings.Contains(f.body, w) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  does not mention %q", f.line, f.heading, w)
				}
			}
			if strings.Contains(f.body, sessionTimersNoteLabel) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  carries a proposal 0105 resolution note although the work left it open", f.line, f.heading)
			}
		})
	}
}

// diagnosis: a defect entry filed ahead of the session timer work still asks
// that work to confirm it, or lost the record that it was confirmed in place.
// Restore the confirmation sentence rather than filing a duplicate entry.
//
// spec: 7.2 (Interactive Session Model)
func TestSessionTimersFiledDefectsAreConfirmed_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range sessionTimersFiledDefects {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if strings.Contains(f.body, "implementor updates this entry") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still asks proposal 0105's implementation to confirm it", f.line, f.heading)
			}
			if !strings.Contains(f.body, "confirmed as the entry for this defect when 0105 was implemented") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not record its confirmation by proposal 0105's implementation", f.line, f.heading)
			}
			if strings.Contains(f.body, "After 0105 lands") || strings.Contains(f.body, "Once 0105 lands") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  describes proposal 0105 as not yet landed", f.line, f.heading)
			}
		})
	}
}

// diagnosis: the remediation plan's P3 entry describes the watchdog as
// leader-gated, or lost the replica assignment, the input-wait decision
// against R20, or the position that landed with proposal 0105. Restore the
// entry as the work left it.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.4
// (Coordinator-Loss Detection and Hold State)
func TestSessionTimersPlanEntryStatesTheLandedDesign_spec_10_1_1(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	_, rest, ok := strings.Cut(plan, "- [x] **P3. Session timers and sweep ownership**")
	if !ok {
		t.Fatal("gateway-runtime-comms-remediation.md has no ticked P3 entry")
	}
	entry, _, _ := strings.Cut(rest, "\nProposals, third wave:")
	if strings.Contains(entry, "The owner is the watchdog, leader-gated.") {
		t.Error("the P3 entry still describes the watchdog as leader-gated")
	}
	for _, w := range []string{
		"run on every replica",
		"Only the §10.1.4 orphan-session reconciler is leader-gated.",
		"`state_entered_at`",
		"`perChildMaxAge`",
		"R20",
		"`LNK-GWCONTROL`",
		"Position:",
		sessionTimersProposal,
	} {
		if !strings.Contains(entry, w) {
			t.Errorf("the P3 entry does not mention %q", w)
		}
	}
	if l := proposalScaffoldLabel.FindString(entry); l != "" {
		t.Errorf("the P3 entry names the proposal label %q", l)
	}
}
