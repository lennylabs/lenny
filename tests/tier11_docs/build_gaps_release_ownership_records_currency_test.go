// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the release-ownership
// work (proposal 0093) and the tree they describe.
//
// The build loops read BUILD-GAPS.md and the remediation plan as statements
// about the current tree. These checks hold the release-ownership records to
// it:
//
//   - The findings the work closed stay closed only while the code that closed
//     them is in the tree, and every test file their resolutions cite exists.
//   - The findings that stay open with accepted departures carry the
//     departure record, and every test file it cites exists.
//   - The findings the work filed exist once each, stay open, and cite only
//     test files that exist.
//   - The findings the work moved out of the release-ownership item name a
//     destination pending owner confirmation and no longer name that item as
//     their owner.
//   - The remediation-plan item is ticked with its implementation date and
//     records the members it moved out.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// It asserts a documentation contract rather than a platform behavior, so it
// cites BUILD-GAPS.md's own "How findings are formatted" section and the
// remediation plan's proposal track rather than a specification section: no
// specification heading states how those records are written, and registering
// the cases under one would credit that section with coverage no regression
// in it could break.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseOwnershipProposal is the directory of the proposal whose records
// these checks hold, as the records name it.
const releaseOwnershipProposal = "proposals/0093_fix_release-ownership-for-pods-concurrent-workspace-slots-per-po/"

// closedReleaseFinding is a finding the release-ownership work closed, a
// declaration whose presence keeps it closed, and an optional extra probe of
// the tree that must also hold while it is closed.
type closedReleaseFinding struct {
	id     string
	file   string
	symbol string
	// holds, when set, reports whether the tree still matches the closure,
	// and what it checks.
	holds func(t testing.TB, root string) (bool, string)
}

// createdSweeperGone reports whether the created sweeper package is absent.
func createdSweeperGone(t testing.TB, root string) (bool, string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, "pkg", "gateway", "session", "createdsweeper"))
	return os.IsNotExist(err), "pkg/gateway/session/createdsweeper is deleted"
}

// userRevokedReasonGone reports whether no production Go file under pkg/ or
// cmd/ still spells the retired USER_REVOKED Shutdown reason.
func userRevokedReasonGone(t testing.TB, root string) (bool, string) {
	t.Helper()
	for _, path := range productionGoFiles(t, root, "pkg", "cmd") {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(body), "USER_REVOKED") {
			rel, _ := filepath.Rel(root, path)
			return false, "no production file spells USER_REVOKED (found in " + filepath.ToSlash(rel) + ")"
		}
	}
	return true, "no production file spells USER_REVOKED"
}

// closedReleaseFindings are the findings the release-ownership work closed.
var closedReleaseFindings = []closedReleaseFinding{
	{id: "F-4.6.26", file: "pkg/gateway/podlifecycle/podsession/reclaim.go", symbol: "Binder.ReclaimClaimed"},
	{id: "F-4.6.27", file: "pkg/gateway/sessionserver/finalize.go", symbol: "Server.reclaimFinalizedPod"},
	{id: "F-4.6.28", file: "pkg/gateway/runtime/watchdog/watchdog.go", symbol: "Watchdog.reasonForState", holds: createdSweeperGone},
	{id: "F-4.9.35", file: "pkg/gateway/sessionserver/finalize.go", symbol: "Server.revokeFinalizeLease"},
	{id: "F-5.2.49", file: "pkg/gateway/podlifecycle/podsession/slotbinder.go", symbol: "Binder.releaseSlotRetrying"},
	{id: "F-7.3.33", file: "pkg/gateway/sessionserver/terminal_hook.go", symbol: "Server.releaseAfterTerminal"},
	{id: "F-7.3.34", file: "pkg/gateway/podlifecycle/podclaim/slotclaimer.go", symbol: "SlotClaimer.rollbackContext"},
	{id: "F-10.1.28", file: "pkg/gateway/sessionserver/terminal_hook.go", symbol: "Server.settleStaleBinding"},
	{id: "F-11.4.11", file: "pkg/gateway/sessionserver/terminal_binding.go", symbol: "Server.ReleaseRevokedBinding", holds: userRevokedReasonGone},
	{id: "F-11.4.15", file: "pkg/gateway/sessionserver/terminal_hook.go", symbol: "Server.ReclaimRevokedPreRunning"},
	{id: "F-11.4.16", file: "pkg/gateway/sessionserver/terminal_hook.go", symbol: "Server.ReclaimRevokedPreRunning"},
}

// diagnosis: a finding the release-ownership work closed no longer matches
// the tree. Either the code that closed it was removed or reintroduced, so the
// finding must be reopened, or its resolution cites a test file that no longer
// exists, so the evidence cannot be rerun; update the resolution to name the
// current test file.
func TestReleaseOwnershipClosedFindingsCiteLandedCodeAndTests(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range closedReleaseFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open, but the release-ownership work closed it", f.line, f.heading)
			}
			if !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(c.file))), c.symbol) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but %s no longer declares %s; reopen it", f.line, f.heading, c.file, c.symbol)
			}
			if c.holds != nil {
				if ok, what := c.holds(t, root); !ok {
					t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the tree no longer holds: %s; reopen it", f.line, f.heading, what)
				}
			}
			if !strings.Contains(f.body, releaseOwnershipProposal) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not name %s as the proposal that closed it", f.line, f.heading, releaseOwnershipProposal)
			}
			assertResolutionEvidence(t, root, f)
		})
	}
}

// diagnosis: the duplicate closure no longer points at the finding that
// carries the resolution, so a reader of the duplicate cannot find the fix.
func TestReleaseOwnershipDuplicateNamesItsPrimary(t *testing.T) {
	root := repoRoot(t)
	dup := requireFinding(t, buildGapsFindingsByID(t, root), "F-11.4.16")
	_, resolution, _ := strings.Cut(dup.body, "**Resolution")
	if !strings.Contains(resolution, "duplicate of F-11.4.15") {
		t.Errorf("BUILD-GAPS.md:%d %s\n  is not closed as a duplicate of F-11.4.15", dup.line, dup.heading)
	}
}

// assertCitedTestFilesExist fails the test when body cites a test file that
// does not exist.
func assertCitedTestFilesExist(t *testing.T, root string, f gapFinding) {
	t.Helper()
	for _, m := range citedTestFile.FindAllStringSubmatch(f.body, -1) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("BUILD-GAPS.md:%d %s\n  cites %s, which does not exist", f.line, f.heading, m[1])
		}
	}
}

// annotatedReleaseFinding is a finding that stays open with an accepted
// departure the release-ownership work recorded on it, and the record's
// opening label.
type annotatedReleaseFinding struct {
	id    string
	label string
}

// annotatedReleaseFindings are the findings that carry an accepted-departure
// record of the release-ownership work and stay open.
var annotatedReleaseFindings = []annotatedReleaseFinding{
	{"F-4.6.29", "**Accepted departures (proposal 0093"},
	{"F-10.1.21", "**Accepted departures (proposal 0093"},
	{"F-10.1.33", "**Accepted departure (proposal 0093"},
	{"F-11.4.10", "**Accepted departure (proposal 0093"},
}

// diagnosis: a finding that records an accepted departure of the
// release-ownership work was closed, lost its departure record, or cites a
// test file that no longer exists. The departure is a residue the work left
// open on purpose; restore the record, and reopen the finding unless a later
// change removed the residue and says so in a resolution note.
func TestReleaseOwnershipDeparturesStayRecordedOnOpenFindings(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, a := range annotatedReleaseFindings {
		t.Run(a.id, func(t *testing.T) {
			f := requireFinding(t, findings, a.id)
			if !f.open {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but it records an accepted departure the release-ownership work left open", f.line, f.heading)
			}
			if !strings.Contains(f.body, a.label) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  carries no %q record", f.line, f.heading, a.label)
			}
			if len(citedTestFile.FindAllString(f.body, -1)) == 0 {
				t.Errorf("BUILD-GAPS.md:%d %s\n  cites no pinning test file", f.line, f.heading)
			}
			assertCitedTestFilesExist(t, root, f)
		})
	}
}

// releaseOwnershipFiledIDs are the findings the release-ownership work filed:
// its accepted departures that have no earlier finding, and the defects in the
// shipped tree it did not stage.
var releaseOwnershipFiledIDs = []string{
	"F-4.6.33", "F-4.6.34", "F-4.6.35", "F-4.6.36", "F-4.9.39",
	"F-5.2.51", "F-5.2.52", "F-5.2.53", "F-5.2.54", "F-5.2.55", "F-5.2.56",
	"F-7.2.43", "F-8.8.21", "F-10.1.38", "F-10.1.39", "F-10.5.15",
	"F-11.4.17", "F-12.4.28",
}

// diagnosis: a finding the release-ownership work filed is missing, filed
// twice, closed without a resolution, no longer names the proposal that filed
// it, or cites a test file that no longer exists. One defect carries one
// finding: when two headings name the same identifier, merge them.
func TestReleaseOwnershipFiledFindingsAreFiledOnce(t *testing.T) {
	root := repoRoot(t)
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))
	findings := buildGapsFindingsByID(t, root)
	for _, id := range releaseOwnershipFiledIDs {
		t.Run(id, func(t *testing.T) {
			if n := findingHeadingCount(body, id); n != 1 {
				t.Fatalf("BUILD-GAPS.md carries %d headings for %s, want 1", n, id)
			}
			f := requireFinding(t, findings, id)
			if !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
			}
			if !strings.Contains(f.body, releaseOwnershipProposal) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not name %s as the proposal that filed it", f.line, f.heading, releaseOwnershipProposal)
			}
			assertCitedTestFilesExist(t, root, f)
		})
	}
}

// movedOutReleaseFindings are the members the release-ownership work moved
// out of the remediation plan's release-ownership item.
var movedOutReleaseFindings = []string{
	"F-4.9.32", "F-4.9.33", "F-4.9.34", "F-4.9.37", "F-5.2.50", "F-10.1.30", "F-10.1.32",
}

// staleReleaseOwner matches a suggested resolution that still names the
// release-ownership item as the finding's owner.
var staleReleaseOwner = regexp.MustCompile(`Owned by proposal P1\b`)

// diagnosis: a finding the release-ownership work moved out of its plan item
// still names that item as its owner, names no destination pending owner
// confirmation, or was closed although the work did not fix it. Restore the
// destination sentence; when the owner confirms or changes a destination,
// replace the pending marker with the confirmed owner.
func TestReleaseOwnershipMovedOutMembersNamePendingDestinations(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range movedOutReleaseFindings {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if !f.open {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the release-ownership work moved it out without fixing it", f.line, f.heading)
			}
			if loc := staleReleaseOwner.FindString(f.body); loc != "" {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still says %q", f.line, f.heading, loc)
			}
			for _, want := range []string{"Proposal 0093 moved this finding out of P1", "pending owner confirmation"} {
				if !strings.Contains(f.body, want) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  does not contain %q", f.line, f.heading, want)
				}
			}
		})
	}
}

// releaseOwnershipPlanItem matches the remediation-plan item for the
// release-ownership proposal, ticked and naming its implementation date.
var releaseOwnershipPlanItem = regexp.MustCompile(
	`(?m)^- \[x\] \*\*P1\. Release ownership for pods, slots, and credential leases\*\* \(proposal 0093, implemented \d{4}-\d{2}-\d{2}\)`,
)

// releaseOwnershipPlanEntry returns the text of the plan's release-ownership
// item, from its checklist line up to the next top-level checklist item.
func releaseOwnershipPlanEntry(plan string) string {
	loc := releaseOwnershipPlanItem.FindStringIndex(plan)
	if loc == nil {
		return ""
	}
	rest := plan[loc[1]:]
	if next := regexp.MustCompile(`(?m)^- \[`).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return plan[loc[0]:loc[1]] + rest
}

// diagnosis: the remediation plan does not record the release-ownership
// proposal as implemented, or its item no longer records a member the
// proposal moved out. Tick the item with its implementation date and list
// each moved-out member with its destination.
func TestRemediationPlanRecordsTheReleaseOwnershipProposal(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	entry := releaseOwnershipPlanEntry(plan)
	if entry == "" {
		t.Fatal("gateway-runtime-comms-remediation.md: the release-ownership item is not ticked with its implementation date")
	}
	if !strings.Contains(entry, "pending owner confirmation") {
		t.Error("the release-ownership item records no members moved out pending owner confirmation")
	}
	for _, id := range movedOutReleaseFindings {
		if !strings.Contains(entry, id) {
			t.Errorf("the release-ownership item does not record the moved-out member %s", id)
		}
	}
}

// diagnosis: the probes above no longer read a plan item or an owner sentence
// the way the checks assume, so they pass or fail vacuously.
func TestReleaseOwnershipRecordProbes(t *testing.T) {
	plan := "- [x] **P1. Release ownership for pods, slots, and credential leases** (proposal 0093, implemented 2026-10-20).\n" +
		"  - **Members moved out, pending owner confirmation.** F-4.9.37\n" +
		"- [ ] **P5. Data lifecycle** F-5.2.50\n"
	entry := releaseOwnershipPlanEntry(plan)
	if !strings.Contains(entry, "F-4.9.37") || strings.Contains(entry, "F-5.2.50") {
		t.Errorf("releaseOwnershipPlanEntry = %q, want the P1 item only", entry)
	}
	if releaseOwnershipPlanEntry("- [ ] **P1. Release ownership for pods, slots, and credential leases** (not yet written).\n") != "" {
		t.Error("releaseOwnershipPlanEntry matched an unticked item")
	}
	if !staleReleaseOwner.MatchString("Owned by proposal P1 (release ownership).") {
		t.Error("staleReleaseOwner does not match the retired owner sentence")
	}
	if staleReleaseOwner.MatchString("Proposal 0093 moved this finding out of P1 (release ownership).") {
		t.Error("staleReleaseOwner matched the moved-out sentence")
	}
}
