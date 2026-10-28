// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the change that removed
// the session-creation mint and the internal delegation child-token exchange
// (proposal 0104) and the tree they describe.
//
// The build loops read BUILD-GAPS.md and TEST-GAPS.md as statements about the
// current tree. These checks hold the records that change closed, resolved,
// or updated to it:
//
//   - The session-creation mint finding and the child-token finding stay
//     closed only while the unsigned child-token minter is absent, and every
//     test their resolutions name exists.
//   - The child-token test gap is resolved against the creation-fence tests
//     that replaced it, and every test it names exists.
//   - The findings filed from the change's unstaged-defect list stay open,
//     and a sentinel error their evidence names is either declared in the
//     delegation package or named in a sentence that says it was deleted.
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

// podNoJWTProposal is the directory of the proposal whose records these
// checks hold, as the records name it.
const podNoJWTProposal = "proposals/0104_fix_two-gaps-leave-lenny-s-session-scoped-and-delegation/"

// Tree paths the records describe.
const (
	childTokenPackageDir  = "pkg/gateway/mcpfabric/delegation/childtoken"
	delegationServiceFile = "pkg/gateway/mcpfabric/delegation/service.go"
)

// diagnosis: F-10.2.25 or F-8.2.31 was reopened, lost its proposal 0104
// resolution, or names a test that no longer exists; or the tree regained
// the unsigned child-token minter or the sentinels it alone produced. Restore
// the resolution, or reopen the finding when the tree no longer holds the
// change.
//
// spec: 8.2 (Delegation Mechanism), 10.2 (Authentication), 12.8 (Compliance Interfaces)
func TestPodNoJWTRecordsCloseTheMintFindings_spec_8_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, id := range []string{"F-10.2.25", "F-8.2.31"} {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if f.open || !strings.HasSuffix(strings.TrimSpace(f.heading), "— CLOSED") {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is not closed", f.line, f.heading)
			}
			_, res, ok := strings.Cut(f.body, "**Resolution (proposal 0104")
			if !ok || !strings.Contains(res, podNoJWTProposal) {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no proposal 0104 resolution naming %s", f.line, f.heading, podNoJWTProposal)
			}
			if strings.Contains(f.body, "**Suggested resolution:**") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still carries the stale suggested resolution", f.line, f.heading)
			}
			if len(recordTestRefs(res)) == 0 {
				t.Errorf("BUILD-GAPS.md:%d %s\n  resolution names no test", f.line, f.heading)
			}
			assertRecordTestRefsExist(t, root, "BUILD-GAPS.md "+id, res)
		})
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(childTokenPackageDir))); err == nil {
		t.Errorf("%s exists again, so the unsigned minter F-8.2.31 records as deleted is back", childTokenPackageDir)
	}
	svc := fileText(t, root, delegationServiceFile)
	for _, gone := range []string{"ErrParentRevoked =", "ErrAuditContention ="} {
		if strings.Contains(svc, gone) {
			t.Errorf("%s declares %q again, which the F-8.2.31 resolution records as deleted", delegationServiceFile, gone)
		}
	}
}

// diagnosis: T-8.2.7 was reopened or lost its proposal 0104 resolution, or
// its resolution names a test that no longer exists. Restore the record as
// the change left it, or record the later change that moved it.
//
// spec: 8.2 (Delegation Mechanism), 12.8 (Compliance Interfaces)
func TestPodNoJWTTestGapRecords_spec_8_2(t *testing.T) {
	root := repoRoot(t)
	resolved, body := testGapsEntry(t, root, "T-8.2.7")
	if !resolved {
		t.Fatal("TEST-GAPS.md T-8.2.7 is not resolved")
	}
	_, res, ok := strings.Cut(body, "**Resolution (proposal 0104")
	if !ok || !strings.Contains(res, podNoJWTProposal) {
		t.Fatalf("TEST-GAPS.md T-8.2.7 carries no proposal 0104 resolution naming %s", podNoJWTProposal)
	}
	for _, want := range []string{
		"creation_fence_parent_race_test.go::TestCreationFenceRaceAgainstParentTerminalWrite_spec_12_8",
		"parent_freshness_test.go::TestDelegateParentEndedBeforeInsertRefuses_spec_8_2",
		"DELEGATION_PARENT_REVOKED",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("TEST-GAPS.md T-8.2.7 resolution does not mention %q", want)
		}
	}
	assertRecordTestRefsExist(t, root, "TEST-GAPS.md T-8.2.7", res)
}

// sentinelRef matches a backticked delegation sentinel error name.
var sentinelRef = regexp.MustCompile("`(Err[A-Z]\\w*)`")

// diagnosis: a finding filed from proposal 0104's unstaged-defect list was
// closed without a resolution, or its evidence names a delegation sentinel the
// package no longer declares without saying it was deleted, so the record
// describes a handler the tree no longer has. Refresh the evidence from the
// tree.
//
// spec: 8.5 (Delegation Tools), 10.2 (Authentication), 13.3 (Credential Flow)
func TestPodNoJWTFiledDefectsMatchTheTree_spec_8_5(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	svc := fileText(t, root, delegationServiceFile)
	for _, id := range []string{"F-8.5.26", "F-10.2.29", "F-13.3.22"} {
		t.Run(id, func(t *testing.T) {
			f := requireFinding(t, findings, id)
			if !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is closed without a resolution", f.line, f.heading)
			}
			if !strings.Contains(f.body, podNoJWTProposal) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  no longer names %s", f.line, f.heading, podNoJWTProposal)
			}
			if strings.Contains(f.body, "ahead of 0104's REC-1") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  still says it was filed ahead of the record closure that has landed", f.line, f.heading)
			}
			_, ev, _ := strings.Cut(f.body, "**Evidence:**")
			ev, _, _ = strings.Cut(ev, "\n")
			for _, sentence := range strings.SplitAfter(ev, ". ") {
				if strings.Contains(sentence, "deleted") {
					continue
				}
				for _, m := range sentinelRef.FindAllStringSubmatch(sentence, -1) {
					if !strings.Contains(svc, "\t"+m[1]+" = errors.New(") {
						t.Errorf("BUILD-GAPS.md:%d %s\n  evidence names %s, which %s does not declare", f.line, f.heading, m[1], delegationServiceFile)
					}
				}
			}
		})
	}
}
