// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the data-lifecycle work
// (proposal 0094) and the tree they describe.
//
// The build loops read BUILD-GAPS.md, the remediation plan, and the claim
// register as statements about the current tree. These checks hold the
// data-lifecycle records to it:
//
//   - The findings the work closed stay closed only while the code that closed
//     them is in the tree, and every test file their resolutions cite exists.
//   - The findings the work left open carry the annotation that says which half
//     landed and who owns the rest.
//   - The findings the work filed exist once each, in the state and with the
//     owner the work gave them, and cite only test files that exist.
//   - The remediation plan's data-lifecycle item is ticked, names the residual
//     the drain leaves to the terminal pipeline proposal, and no longer says
//     that erasure waits for terminal hooks, which the specification dropped.
//   - The claim register carries the terminal-tail residual as an ABSENT row
//     deferred to the off-holder eviction step, naming the drain it describes
//     and the finding that records it.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks. It
// asserts a documentation contract rather than a platform behavior, so it
// carries no specification-section credit: no specification heading states how
// these records are written, and the claim register's own §28.4 gates already
// hold the register's format.

package tier11_docs_test

import (
	"encoding/json"
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dataLifecycleProposal is the directory of the proposal whose records these
// checks hold, as the records name it.
const dataLifecycleProposal = "proposals/0094_fix_data-lifecycle-user-erasure-tenant-deletion-and-suspension-f/"

// closedLifecycleFinding is a finding the data-lifecycle work closed, a
// declaration whose presence keeps it closed, and an optional extra probe of
// the tree that must also hold while it is closed.
type closedLifecycleFinding struct {
	id     string
	file   string
	symbol string
	// holds, when set, reports whether the tree still matches the closure,
	// and what it checks.
	holds func(t testing.TB, root string) (bool, string)
}

// fileContains returns a probe that reports whether the repo-relative file
// contains want.
func fileContains(rel, want string) func(testing.TB, string) (bool, string) {
	return func(t testing.TB, root string) (bool, string) {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return strings.Contains(string(body), want), rel + " contains " + want
	}
}

// fileOmits returns a probe that reports whether the repo-relative file does
// not contain unwanted.
func fileOmits(rel, unwanted string) func(testing.TB, string) (bool, string) {
	return func(t testing.TB, root string) (bool, string) {
		t.Helper()
		ok, _ := fileContains(rel, unwanted)(t, root)
		return !ok, rel + " does not contain " + unwanted
	}
}

// listFilterReadsPrimary reports whether sessionstore.ListFilter still
// declares the Primary field that routes a lifecycle listing to the primary.
func listFilterReadsPrimary(t testing.TB, root string) (bool, string) {
	t.Helper()
	const rel = "pkg/gateway/session/sessionstore/sessionstore.go"
	what := rel + " declares ListFilter.Primary"
	file := parseGoFile(t, filepath.Join(root, filepath.FromSlash(rel)))
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "ListFilter" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return false, what
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == "Primary" {
						return true, what
					}
				}
			}
		}
	}
	return false, what
}

const (
	creationFenceFile = "pkg/gateway/session/sessionstore/creation_fence.go"
	drainFile         = "pkg/gateway/externalapi/admin/drain.go"
	responsesFile     = "pkg/gateway/environment/translator/open_responses.go"
	erasurePgFile     = "pkg/gateway/storage/erasurejob/pgstore/pgstore.go"
	terminatorFile    = "cmd/lenny-gateway/tenantdeletion.go"
	saltLockFile      = "pkg/gateway/storage/erasurejob/saltlockpg/saltlockpg.go"
)

// closedLifecycleFindings are the findings the data-lifecycle work closed,
// including the ones it filed and closed in the same change.
var closedLifecycleFindings = []closedLifecycleFinding{
	{id: "F-12.8.20", file: erasurePgFile, symbol: "Store.Start"},
	{
		id: "F-12.8.26", file: creationFenceFile, symbol: "CreationMarkers.Refusal",
		holds: fileContains("pkg/gateway/session/sessionstore/pgstore/creation_fence.go", "FOR SHARE"),
	},
	{id: "F-12.8.27", file: drainFile, symbol: "DrainSessions", holds: listFilterReadsPrimary},
	{id: "F-12.8.28", file: "pkg/gateway/storage/erasurejob/runner_phases.go", symbol: "Runner.runDrain"},
	{id: "F-12.8.31", file: creationFenceFile, symbol: "CreationMarkers.Refusal"},
	{
		id: "F-12.8.32", file: drainFile, symbol: "DrainSessions",
		holds: fileOmits(responsesFile, "DELETE /v1/responses"),
	},
	{id: "F-12.8.33", file: terminatorFile, symbol: "tenantSessionTerminator"},
	{id: "F-12.8.36", file: "pkg/gateway/storage/erasurejob/runner_sweep.go", symbol: "Runner.RunPending"},
	{
		id: "F-12.8.38", file: terminatorFile, symbol: "tenantSessionTerminator",
		holds: fileContains("cmd/lenny-gateway/adminrouter.go", "Terminator: tenantSessionTerminator"),
	},
	{id: "F-12.8.39", file: erasurePgFile, symbol: "Store.Complete"},
	{
		id: "F-12.8.40", file: saltLockFile, symbol: "Lock.WithSaltLock",
		holds: fileContains(saltLockFile, "SELECT pg_advisory_xact_lock("),
	},
	{
		id: "F-15.1.48", file: responsesFile, symbol: "OpenResponsesHandler.handleGet",
		holds: fileOmits(responsesFile, "DELETE /v1/responses"),
	},
	{
		id: "F-15.1.50", file: "pkg/gateway/externalapi/admin/tenant_suspend.go", symbol: "Router.handleSuspendTenant",
		holds: fileContains("pkg/gateway/externalapi/admin/tenant_suspend.go", "context.WithoutCancel(req.Context())"),
	},
	{id: "F-15.1.51", file: creationFenceFile, symbol: "CreationMarkers.Refusal"},
}

// diagnosis: a finding the data-lifecycle work closed no longer matches the
// tree. Either the code that closed it was removed or the defect was
// reintroduced, so the finding must be reopened, or its resolution cites a test
// file that no longer exists; update the resolution to name the current file.
func TestDataLifecycleClosedFindingsCiteLandedCodeAndTests(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range closedLifecycleFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open, but the data-lifecycle work closed it", f.line, f.heading)
			}
			if !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(c.file))), c.symbol) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but %s no longer declares %s; reopen it", f.line, f.heading, c.file, c.symbol)
			}
			if c.holds != nil {
				if ok, what := c.holds(t, root); !ok {
					t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the tree no longer holds: %s; reopen it", f.line, f.heading, what)
				}
			}
			if !strings.Contains(f.body, "Closed by proposal 0094 (`"+dataLifecycleProposal+"`)") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  has no resolution naming %s as the proposal that closed it", f.line, f.heading, dataLifecycleProposal)
			}
			assertResolutionEvidence(t, root, f)
		})
	}
}

// diagnosis: the erasure job store finding lost the record of why it was
// reopened, so its two resolutions read as contradicting each other. Restore
// the reopening note between the gated closure and the proposal's resolution.
func TestDataLifecycleReopenedErasureStoreFindingRecordsTheReopening(t *testing.T) {
	root := repoRoot(t)
	f := requireFinding(t, buildGapsFindingsByID(t, root), "F-12.8.20")
	reopened := strings.Index(f.body, "**Reopened (")
	resolved := strings.Index(f.body, "**Resolution (58e7d670b")
	if reopened < 0 || resolved < 0 || reopened > resolved {
		t.Errorf("BUILD-GAPS.md:%d %s\n  does not record its reopening ahead of the resolution that closed it under proposal 0094", f.line, f.heading)
	}
}

// annotatedLifecycleFinding is a finding the data-lifecycle work left open
// and annotated, and the owner the annotation names for the rest.
type annotatedLifecycleFinding struct {
	id    string
	owner string
}

// annotatedLifecycleFindings are the open findings that carry an annotation
// of the data-lifecycle work.
var annotatedLifecycleFindings = []annotatedLifecycleFinding{
	{"F-11.7.22", "P2"},
	{"F-11.4.14", "P5b"},
	{"F-11.4.12", "P5b"},
	{"F-11.4.13", "P5b"},
	{"F-12.8.41", "P5b"},
}

// diagnosis: a finding the data-lifecycle work left open on purpose was
// closed, or lost the annotation naming which half landed and who owns the
// rest. Restore the annotation, and reopen the finding unless a later change
// closed the remaining half and says so in a resolution note.
func TestDataLifecycleAnnotationsStayOnOpenFindings(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, a := range annotatedLifecycleFindings {
		t.Run(a.id, func(t *testing.T) {
			f := requireFinding(t, findings, a.id)
			if !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution, but the data-lifecycle work left part of it open", f.line, f.heading)
			}
			_, note, ok := strings.Cut(f.body, "**Annotation (proposal 0094")
			if !ok {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no data-lifecycle annotation", f.line, f.heading)
			}
			note, _, _ = strings.Cut(note, "\n")
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(a.owner) + `\b`).MatchString(note) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  annotation does not name %s as the owner of the remaining half", f.line, f.heading, a.owner)
			}
		})
	}
}

// filedLifecycleFinding is a finding the data-lifecycle work filed, whether
// the work closed it, and the owner sentence an open one carries.
type filedLifecycleFinding struct {
	id     string
	closed bool
	owner  string
}

// filedLifecycleFindings are the findings the data-lifecycle work filed.
var filedLifecycleFindings = []filedLifecycleFinding{
	{id: "F-12.8.38", closed: true},
	{id: "F-12.8.39", closed: true},
	{id: "F-12.8.40", closed: true},
	{id: "F-12.8.41", owner: "Owned by proposal P5b"},
	{id: "F-12.8.42", owner: "Owned by proposal P5b"},
	{id: "F-12.8.43", owner: "Owned by proposal P2"},
	{id: "F-12.8.44", owner: "Owner: unassigned."},
	{id: "F-12.8.45", owner: "Owner: unassigned."},
	{id: "F-12.8.46", owner: "Owner: unassigned."},
	{id: "F-12.8.47", owner: "Owner: unassigned."},
	{id: "F-12.8.48", owner: "Owner: unassigned."},
	{id: "F-12.3.25", owner: "Owner: unassigned."},
	{id: "F-15.1.55", owner: "Owner: unassigned."},
}

// diagnosis: a finding the data-lifecycle work filed is missing, filed twice,
// in a state the work did not give it without a resolution, no longer names
// the proposal that filed it or its owner, or cites a test file that no longer
// exists. One defect carries one finding: when two headings name the same
// identifier, merge them.
func TestDataLifecycleFiledFindingsAreFiledOnce(t *testing.T) {
	root := repoRoot(t)
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))
	findings := buildGapsFindingsByID(t, root)
	for _, c := range filedLifecycleFindings {
		t.Run(c.id, func(t *testing.T) {
			if n := findingHeadingCount(body, c.id); n != 1 {
				t.Fatalf("BUILD-GAPS.md carries %d headings for %s, want 1", n, c.id)
			}
			f := requireFinding(t, findings, c.id)
			if !strings.Contains(f.body, dataLifecycleProposal) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not name %s as the proposal that filed it", f.line, f.heading, dataLifecycleProposal)
			}
			if c.closed {
				if f.open {
					t.Errorf("BUILD-GAPS.md:%d %s\n  is open, but the data-lifecycle work closed it", f.line, f.heading)
				}
				return
			}
			if !f.open && !strings.Contains(f.body, "**Resolution") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
			}
			if f.open && !strings.Contains(f.body, c.owner) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not carry %q", f.line, f.heading, c.owner)
			}
			assertCitedTestFilesExist(t, root, f)
		})
	}
}

// dataLifecyclePlanItem matches the remediation-plan item for the
// data-lifecycle proposal, ticked and naming its implementation date.
var dataLifecyclePlanItem = regexp.MustCompile(
	"(?m)^- \\[x\\] \\*\\*P5\\. Data lifecycle: user erasure, tenant deletion and suspension, full_revoke, and\\n" +
		"  `DELETE /v1/responses/\\{id\\}`\\*\\* \\(proposal 0094, implemented \\d{4}-\\d{2}-\\d{2}\\)",
)

// dataLifecyclePlanEntry returns the text of the plan's data-lifecycle item,
// from its checklist line up to the next top-level checklist item.
func dataLifecyclePlanEntry(plan string) string {
	loc := dataLifecyclePlanItem.FindStringIndex(plan)
	if loc == nil {
		return ""
	}
	rest := plan[loc[1]:]
	if next := regexp.MustCompile(`(?m)^- \[`).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return plan[loc[0]:loc[1]] + rest
}

// diagnosis: the remediation plan does not record the data-lifecycle proposal
// as implemented, still says erasure waits for terminal hooks, which the
// specification dropped, or no longer names the residual the drain leaves to
// P2 or the members moved to P5b. Restore the item from the BUILD-GAPS.md
// records it summarizes.
func TestRemediationPlanRecordsTheDataLifecycleProposal(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	entry := dataLifecyclePlanEntry(plan)
	if entry == "" {
		t.Fatal("gateway-runtime-comms-remediation.md: the data-lifecycle item is not ticked with its implementation date")
	}
	if strings.Contains(entry, "waits for their terminal hooks") {
		t.Error("the data-lifecycle item still says erasure waits for the sessions' terminal hooks")
	}
	for _, want := range []string{"F-12.8.43", "P2", "P5b", "P1 (0093)", "F-11.4.12", "F-11.4.13", "R21"} {
		if !strings.Contains(entry, want) {
			t.Errorf("the data-lifecycle item does not mention %s", want)
		}
	}
}

// claimRow is the part of a claim-register row these checks read.
type claimRow struct {
	Claim      string `json:"claim"`
	Status     string `json:"status"`
	SpecAnchor string `json:"spec_anchor"`
	DeferralID string `json:"deferral_id"`
	Surface    string `json:"surface"`
	Note       string `json:"note"`
}

// diagnosis: the claim register lost the row for the terminal-tail residual
// of the lifecycle drain, or the row no longer names the inter-replica heading,
// the off-holder eviction step, the drain, or the finding that records the
// residual. Restore the row in scripts/seed-claim-register.py and regenerate
// tests/claim-map.json; never edit the register by hand.
func TestClaimRegisterCarriesTheDrainTerminalTailResidual(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "tests", "claim-map.json"))
	if err != nil {
		t.Fatalf("read tests/claim-map.json: %v", err)
	}
	var doc struct {
		Claims []claimRow `json:"claims"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse tests/claim-map.json: %v", err)
	}
	var rows []claimRow
	for _, c := range doc.Claims {
		if c.SpecAnchor == "#2854-inter-replica" && strings.Contains(c.Surface, "DrainSessions") {
			rows = append(rows, c)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("tests/claim-map.json carries %d inter-replica rows naming DrainSessions, want 1", len(rows))
	}
	row := rows[0]
	if row.Status != "ABSENT" || row.DeferralID != "R21" {
		t.Errorf("terminal-tail row has status %q and deferral %q, want ABSENT and R21", row.Status, row.DeferralID)
	}
	for _, want := range []string{"F-12.8.43", "P2"} {
		if !strings.Contains(row.Note, want) {
			t.Errorf("terminal-tail row note does not name %s", want)
		}
	}
	if !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(drainFile))), "DrainSessions") {
		t.Errorf("the terminal-tail row names DrainSessions, which %s no longer declares", drainFile)
	}
	f := requireFinding(t, buildGapsFindingsByID(t, root), "F-12.8.43")
	if !f.open {
		t.Errorf("BUILD-GAPS.md:%d %s\n  is closed while the claim register still carries its ABSENT row", f.line, f.heading)
	}
}

// diagnosis: the probes above no longer read a plan item or a source file the
// way the checks assume, so they pass or fail vacuously.
func TestDataLifecycleRecordProbes(t *testing.T) {
	plan := "- [x] **P5. Data lifecycle: user erasure, tenant deletion and suspension, full_revoke, and\n" +
		"  `DELETE /v1/responses/{id}`** (proposal 0094, implemented 2026-10-21).\n" +
		"  - **Scope.** F-12.8.43\n" +
		"- [ ] **P5b. Data lifecycle, second half** F-12.8.41\n"
	entry := dataLifecyclePlanEntry(plan)
	if !strings.Contains(entry, "F-12.8.43") || strings.Contains(entry, "F-12.8.41") {
		t.Errorf("dataLifecyclePlanEntry = %q, want the P5 item only", entry)
	}
	unticked := strings.Replace(plan, "- [x] **P5.", "- [ ] **P5.", 1)
	if dataLifecyclePlanEntry(unticked) != "" {
		t.Error("dataLifecyclePlanEntry matched an unticked item")
	}

	root := t.TempDir()
	rel := "probe.go"
	if err := os.WriteFile(filepath.Join(root, rel), []byte("package p\n// DELETE /v1/responses\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fileOmits(rel, "DELETE /v1/responses")(t, root); ok {
		t.Error("fileOmits reported absent a string the file contains")
	}
	if ok, _ := fileContains(rel, "pg_advisory_xact_lock")(t, root); ok {
		t.Error("fileContains reported present a string the file lacks")
	}
}
