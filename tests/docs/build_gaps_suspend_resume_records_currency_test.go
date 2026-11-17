// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the session suspend and
// resume work and the tree they describe.
//
// The build loops read BUILD-GAPS.md and the remediation plan as statements
// about the current tree. These checks hold those statements to the code:
//
//   - The findings the work closed stay closed only while the code that
//     closed them is in the tree, and every test file their resolutions cite
//     exists.
//   - The findings the work filed exist once each, and those a declaration
//     probe can observe stay open while the probe still finds the gap.
//   - A skip that names a finding as its blocker names an open finding.
//   - The remediation-plan item is ticked with its implementation date.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 7.3 (Retry and Resume), 7.2 (Interactive Session Model), 29.3
// (Interactive message send), 10.1.1 (Stateless Replicas and Per-Session
// Coordination)

package docs_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// closedSuspendResumeFinding is a finding the suspend and resume work closed
// and a declaration whose presence keeps it closed.
type closedSuspendResumeFinding struct {
	id     string
	file   string
	symbol string
}

// closedSuspendResumeFindings are the findings the suspend and resume work
// closed.
var closedSuspendResumeFindings = []closedSuspendResumeFinding{
	{"F-11.3.35", "pkg/gateway/sessionserver/suspend_release.go", "Server.ReleaseSuspendedPod"},
	{"F-11.3.36", "pkg/gateway/sessionserver/suspend_release.go", "Server.SuspendIdle"},
	{"F-15.1.41", "pkg/gateway/sessionserver/resume_driver.go", "Server.DriveResume"},
	{"F-7.3.29", "pkg/gateway/sessionserver/resume_driver.go", "Server.DriveResume"},
	{"F-10.1.25", "pkg/gateway/coordination/coordination/coordination.go", "adoptedState"},
	{"F-7.3.30", "pkg/gateway/sessionserver/resume_driver.go", "Server.DriveResume"},
	{"F-7.2.27", "pkg/gateway/sessionserver/resume_driver.go", "Server.DriveResume"},
	{"F-8.10.18", "pkg/gateway/sessionserver/treerecovery.go", "sessionNodeReattacher.ReattachNode"},
}

// diagnosis: a finding the suspend and resume work closed no longer matches
// the tree. Either the code that closed it was removed, so the finding must
// be reopened, or its resolution cites a test file that no longer exists, so
// the evidence cannot be rerun; update the resolution to name the current
// test file.
func TestClosedSuspendResumeFindingsCiteLandedCodeAndTests_spec_7_3(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range closedSuspendResumeFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open, but the suspend and resume work closed it", f.line, f.heading)
			}
			if !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(c.file))), c.symbol) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but %s no longer declares %s; reopen it", f.line, f.heading, c.file, c.symbol)
			}
			assertResolutionEvidence(t, root, f)
		})
	}
}

// assertResolutionEvidence fails the test when a closed finding carries no
// resolution, names a proposal-internal step label, cites no test file, or
// cites a test file that does not exist.
func assertResolutionEvidence(t *testing.T, root string, f gapFinding) {
	t.Helper()
	if !strings.Contains(f.body, "**Resolution") {
		t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
	}
	// Only the resolution is checked for a proposal step label: the finding's
	// own Spec line may cite a step the specification numbers, such as a
	// §29.6 step.
	_, resolution, _ := strings.Cut(f.body, "**Resolution")
	if label := proposalStepLabel.FindString(resolution); label != "" {
		t.Errorf("BUILD-GAPS.md:%d %s\n  names the proposal-internal label %q; describe the commits it groups instead", f.line, f.heading, label)
	}
	cited := citedTestFile.FindAllStringSubmatch(f.body, -1)
	if len(cited) == 0 {
		t.Errorf("BUILD-GAPS.md:%d %s\n  cites no test file in its resolution", f.line, f.heading)
	}
	for _, m := range cited {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("BUILD-GAPS.md:%d %s\n  cites %s, which does not exist", f.line, f.heading, m[1])
		}
	}
}

// suspendResumeFiledIDs are the findings the suspend and resume work filed
// for the gaps it left, including the off-holder running-row finding its
// tests filed.
var suspendResumeFiledIDs = []string{
	"F-4.4.29", "F-4.9.36", "F-4.9.37", "F-5.2.50", "F-6.2.29", "F-6.2.30",
	"F-7.1.30", "F-7.2.38", "F-7.2.39", "F-7.2.40", "F-7.2.41", "F-7.2.42",
	"F-7.3.35", "F-8.3.6", "F-10.1.26", "F-10.1.35", "F-10.1.36", "F-10.1.37",
	"F-10.4.14", "F-11.4.16", "F-12.8.37", "F-15.1.53", "F-29.3.1", "F-29.3.2",
}

// findingHeadingCount returns how many finding headings in body carry id.
func findingHeadingCount(body, id string) int {
	pat := regexp.MustCompile(`(?m)^### - \[( |x)\] ` + regexp.QuoteMeta(id) + ` `)
	return len(pat.FindAllStringIndex(body, -1))
}

// diagnosis: a finding the suspend and resume work filed is missing, closed
// without a resolution, or filed twice. One defect carries one finding: when
// two headings name the same identifier, merge them into the one the earlier
// step filed.
func TestSuspendResumeFiledFindingsAreFiledOnce_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	body := readRecord(t, filepath.Join(root, "BUILD-GAPS.md"))
	findings := buildGapsFindingsByID(t, root)
	for _, id := range suspendResumeFiledIDs {
		t.Run(id, func(t *testing.T) {
			if n := findingHeadingCount(body, id); n != 1 {
				t.Fatalf("BUILD-GAPS.md carries %d headings for %s, want 1", n, id)
			}
			if f := requireFinding(t, findings, id); !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
			}
		})
	}
}

// diagnosis: the finding that records the buffering of an off-holder message
// in the serving replica's own inbox and the finding that records the
// missing off-holder forward no longer point at each other or at the step
// that owns them, so a reader of one cannot find the other.
func TestOffHolderInboxFindingCrossReferencesTheForwardFinding_spec_29_3(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	inbox := requireFinding(t, findings, "F-29.3.2")
	forward := requireFinding(t, findings, "F-7.2.38")
	for _, want := range []string{"F-7.2.38", "R19", "durableInbox: false"} {
		if !strings.Contains(inbox.body, want) {
			t.Errorf("BUILD-GAPS.md:%d %s\n  does not name %s", inbox.line, inbox.heading, want)
		}
	}
	if !strings.Contains(forward.body, "F-29.3.2") {
		t.Errorf("BUILD-GAPS.md:%d %s\n  does not name F-29.3.2", forward.line, forward.heading)
	}
}

// filedSuspendResumeGap is a filed finding with a probe that reports whether
// the tree still has its gap.
type filedSuspendResumeGap struct {
	id      string
	what    string
	present func(t testing.TB, root string) bool
}

// productionGoFiles returns the non-test Go files under the given repository
// directories.
func productionGoFiles(t testing.TB, root string, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return out
}

// selectorUsedOutside reports whether any production Go file under pkg/ and
// cmd/ other than the files in exclude references sel as a selector or calls
// it as a method.
func selectorUsedOutside(t testing.TB, root, sel string, exclude ...string) bool {
	t.Helper()
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[filepath.Join(root, filepath.FromSlash(e))] = true
	}
	for _, path := range productionGoFiles(t, root, "pkg", "cmd") {
		if skip[path] {
			continue
		}
		found := false
		ast.Inspect(parseGoFile(t, path), func(n ast.Node) bool {
			if se, ok := n.(*ast.SelectorExpr); ok && se.Sel.Name == sel {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// filedSuspendResumeGaps are the filed findings whose gap a declaration
// probe can observe.
var filedSuspendResumeGaps = []filedSuspendResumeGap{
	{
		id:   "F-7.2.40",
		what: "no production code calls ClearInboxOnAcquire, the only emitter of inbox_cleared",
		present: func(t testing.TB, root string) bool {
			return !selectorUsedOutside(t, root, "ClearInboxOnAcquire")
		},
	},
	{
		id:   "F-7.2.42",
		what: "no production code references sessioninbox.ReasonDurableInboxTTLExpired",
		present: func(t testing.TB, root string) bool {
			return !selectorUsedOutside(t, root, "ReasonDurableInboxTTLExpired")
		},
	},
	{
		id:   "F-12.8.37",
		what: "the tenant-deletion eraser list has no session message queue eraser",
		present: func(t testing.TB, root string) bool {
			file := parseGoFile(t, filepath.Join(root, "cmd", "lenny-gateway", "tenant_erasers.go"))
			return !methodMentionsSelector(t, file, "buildTenantErasers", "EraseSession") &&
				!methodMentionsSelector(t, file, "tenantErasersBeforeSessions", "EraseSession")
		},
	},
}

// methodMentionsSelector reports whether the body of the method named fn in
// file references any selector named sel. It fails the test when file
// declares no such method, so a rename does not turn the probe vacuous.
func methodMentionsSelector(t testing.TB, file *ast.File, fn, sel string) bool {
	t.Helper()
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Recv == nil || d.Name.Name != fn || d.Body == nil {
			continue
		}
		found := false
		ast.Inspect(d.Body, func(n ast.Node) bool {
			if se, ok := n.(*ast.SelectorExpr); ok && se.Sel.Name == sel {
				found = true
			}
			return !found
		})
		return found
	}
	t.Fatalf("no method %s with a body; update the probe to the function that builds the list", fn)
	return false
}

// diagnosis: a finding the suspend and resume work filed disagrees with the
// tree. Either it was closed while the gap its evidence names is still in the
// code, or the gap was fixed and the finding is still open. Close it with a
// resolution note when the gap is fixed, and reopen it when it was closed
// without a fix; do not change the code to match the record.
func TestFiledSuspendResumeFindingsMatchTheTree_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, gap := range filedSuspendResumeGaps {
		t.Run(gap.id, func(t *testing.T) {
			f := requireFinding(t, findings, gap.id)
			present := gap.present(t, root)
			switch {
			case present && !f.open:
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the tree still has the gap: %s", f.line, f.heading, gap.what)
			case !present && f.open:
				t.Errorf("BUILD-GAPS.md:%d %s\n  is open, but the tree no longer has the gap it names (%s no longer holds); close it with a resolution note", f.line, f.heading, gap.what)
			}
		})
	}
}

// blockedOnFinding matches a skip reason that names a BUILD-GAPS finding as
// its blocker.
var blockedOnFinding = regexp.MustCompile(`"blocked: (F-\d+(?:\.\d+)+)\b`)

// blockedFindingSkips returns, for every Go test file under the given
// directories, the finding identifiers its skip reasons name as blockers.
func blockedFindingSkips(t testing.TB, root string, dirs ...string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range blockedOnFinding.FindAllStringSubmatch(string(body), -1) {
				rel, _ := filepath.Rel(root, path)
				out[m[1]] = append(out[m[1]], filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return out
}

// diagnosis: a test skips with "blocked:" on a BUILD-GAPS finding that is
// closed or absent, so the skip outlived its reason and hides a case that
// now has to run; remove the skip.
func TestBlockedSkipsNameOpenFindings_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	skips := blockedFindingSkips(t, root, "tests", "pkg", "cmd", "sdks")
	for id, files := range skips {
		f, ok := findings[id]
		switch {
		case !ok:
			t.Errorf("%v skip on %s, which BUILD-GAPS.md does not carry", files, id)
		case !f.open:
			t.Errorf("%v skip on %s, which is closed (BUILD-GAPS.md:%d); remove the skip", files, id, f.line)
		}
	}
}

// suspendResumePlanItem matches the remediation-plan item for the session
// suspend-and-resume proposal, ticked and naming its implementation date.
var suspendResumePlanItem = regexp.MustCompile(`(?m)^- \[x\] Session suspend-and-resume proposal \(proposal 0092, implemented \d{4}-\d{2}-\d{2}\)`)

// diagnosis: the remediation plan does not record the session
// suspend-and-resume proposal as implemented, or still lists the tree-recovery
// finding it closed among proposal P4's members. Tick the item with its
// implementation date and drop the closed member.
func TestRemediationPlanRecordsTheSuspendResumeProposal_spec_7_3(t *testing.T) {
	root := repoRoot(t)
	plan := readRecord(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	if !suspendResumePlanItem.MatchString(plan) {
		t.Error("gateway-runtime-comms-remediation.md: the session suspend-and-resume item is not ticked with its implementation date")
	}
	for _, stale := range []string{"plus F-8.10.18 (TT-48)", "counting TT-48", "(TT-48, TT-49"} {
		if strings.Contains(plan, stale) {
			t.Errorf("gateway-runtime-comms-remediation.md still contains %q, although proposal 0092 closed F-8.10.18", stale)
		}
	}
}

// diagnosis: the probes above no longer read a skip reason or a selector the
// way the checks assume, so they pass or fail vacuously.
func TestSuspendResumeRecordProbes_spec_7_2(t *testing.T) {
	// The specimen is split before "blocked:" so the file scan of
	// TestBlockedSkipsNameOpenFindings_spec_7_2 does not read it as a skip.
	if got := blockedOnFinding.FindStringSubmatch(`t.Skip("` + `blocked: F-1.2.3 (owner: R1) x")`); len(got) < 2 || got[1] != "F-1.2.3" {
		t.Errorf("blockedOnFinding read %v, want F-1.2.3", got)
	}
	if blockedOnFinding.MatchString(`t.Skip("blocked: the e2e Postgres pod is not running")`) {
		t.Error("blockedOnFinding matched an environment skip that names no finding")
	}
	body := "### - [ ] F-1.2.3 — a [Low] — OPEN\n### - [x] F-1.2.3 — b [Low] — CLOSED\n### - [ ] F-1.2.30 — c [Low] — OPEN\n"
	if n := findingHeadingCount(body, "F-1.2.3"); n != 2 {
		t.Errorf("findingHeadingCount = %d, want 2 (F-1.2.30 is a different finding)", n)
	}
	if !suspendResumePlanItem.MatchString("- [x] Session suspend-and-resume proposal (proposal 0092, implemented 2026-10-15), after") {
		t.Error("suspendResumePlanItem does not match a ticked item")
	}
	if suspendResumePlanItem.MatchString("- [ ] Session suspend-and-resume proposal (proposal 0092, in review), after") {
		t.Error("suspendResumePlanItem matched an unticked item")
	}
}
