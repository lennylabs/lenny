// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the data-lifecycle work
// (proposals 0094 and 0095) and the tree they describe.
//
// The build loops read BUILD-GAPS.md, TEST-GAPS.md, the remediation plan, and
// the claim register as statements about the current tree. These checks hold
// the data-lifecycle records to it:
//
//   - The findings the work closed stay closed only while the code that closed
//     them is in the tree, and every test file their resolutions cite exists.
//   - The findings the work left open carry the annotation that says which half
//     landed and who owns the rest, and the open findings that record an
//     accepted departure keep it with its bound.
//   - The findings the work filed exist once each, in the state and with the
//     owner the work gave them, and cite only test files that exist. A finding
//     that blocks a skipped or narrowed test case points at the deviation
//     entry that records it, and that entry exists.
//   - The remediation plan's data-lifecycle items are ticked and name what
//     each proposal closed, left open, and filed, and the first item no longer
//     says that erasure waits for terminal hooks, which the specification
//     dropped.
//   - The claim register carries the terminal-tail residual as an ABSENT row
//     deferred to the off-holder eviction step, naming the drain it describes
//     and the finding that records it, and its message-queue row says both
//     erasure paths erase the queues.
//   - The test-gap records the second-half work reconciled are resolved or
//     annotated as it left them.
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
	return planEntry(plan, dataLifecyclePlanItem)
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

// secondHalfProposal is the directory of the data-lifecycle second-half
// proposal, as the records name it.
const secondHalfProposal = "proposals/0095_fix_data-lifecycle-second-half-the-remaining-tenant-deletion-and/"

// secondHalfDeviations is the deviations file of the second-half proposal,
// which records the skipped and narrowed test cases and the departures.
const secondHalfDeviations = secondHalfProposal +
	"0095_fix_data-lifecycle-second-half-the-remaining-tenant-deletion-and.deviations.md"

const (
	usersFile         = "pkg/gateway/externalapi/admin/users.go"
	tenantErasersFile = "cmd/lenny-gateway/tenant_erasers.go"
	adminRouterFile   = "cmd/lenny-gateway/adminrouter.go"
	erasureOrderFile  = "pkg/gateway/storage/erasure/order.go"
	receiptFKDropFile = "migrations/0188_audit_redaction_receipts_drop_fk.up.sql"
)

// allHold returns a probe that holds when every given probe holds, and names
// the first one that does not.
func allHold(probes ...func(testing.TB, string) (bool, string)) func(testing.TB, string) (bool, string) {
	return func(t testing.TB, root string) (bool, string) {
		t.Helper()
		var what []string
		for _, p := range probes {
			ok, w := p(t, root)
			if !ok {
				return false, w
			}
			what = append(what, w)
		}
		return true, strings.Join(what, "; ")
	}
}

// closedSecondHalfFindings are the findings the second-half work closed. An
// empty symbol means the closure has no Go declaration to probe, and the
// holds probe carries the check.
var closedSecondHalfFindings = []closedLifecycleFinding{
	{
		id: "F-11.4.12", file: usersFile, symbol: "Router.handleInvalidateUser",
		holds: fileContains(usersFile, "context.WithoutCancel(req.Context())"),
	},
	{
		id: "F-11.4.13", file: usersFile, symbol: "FailedRevokeSession",
		holds: fileContains(usersFile, `out["failedSessions"]`),
	},
	{
		id: "F-11.4.14", file: usersFile, symbol: "Router.terminateUserSessions",
		holds: fileContains(usersFile, "ListFilter{UserID: userID, Primary: true}"),
	},
	{id: "F-11.4.17", file: usersFile, symbol: "Router.afterUncertainRevokeWrite"},
	{
		id:    "F-12.8.25",
		holds: fileContains(receiptFKDropFile, "DROP CONSTRAINT IF EXISTS audit_redaction_receipts_audit_event_id_fkey"),
	},
	{
		id: "F-12.8.30", file: terminatorFile, symbol: "tenantCredentialRevoker",
		holds: fileContains(adminRouterFile, "Revoker: tenantCredentialRevoker{"),
	},
	{
		id: "F-12.8.34", file: erasureOrderFile, symbol: "ValidateTenantOrder",
		holds: allHold(
			fileContains(erasureOrderFile, `"tenant_config":`),
			fileContains(tenantErasersFile, `{"coordination_mirror", mirror}`),
		),
	},
	{
		id: "F-12.8.41", file: erasureOrderFile, symbol: "ValidateOrder",
		holds: allHold(
			fileContains(adminRouterFile, `{Name: "checkpoint_manifests", DeleteBySession: manifests}`),
			fileContains(adminRouterFile, `{Name: "checkpoint_meta", DeleteBySession: meta}`),
		),
	},
	{
		id: "F-12.8.42", file: tenantErasersFile, symbol: "",
		holds: allHold(
			fileContains(adminRouterFile, `{Name: "coordination_mirror", DeleteBySession: mirror}`),
			fileContains(tenantErasersFile, `{"coordination_mirror", mirror}`),
		),
	},
}

// diagnosis: a finding the data-lifecycle second half closed no longer
// matches the tree. Either the code that closed it was removed or the defect
// was reintroduced, so the finding must be reopened, or its resolution no
// longer names the proposal or cites a test file that no longer exists;
// update the resolution to name the current file.
func TestDataLifecycleSecondHalfClosedFindingsCiteLandedCodeAndTests(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range closedSecondHalfFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open, but the data-lifecycle second half closed it", f.line, f.heading)
			}
			if c.symbol != "" && !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(c.file))), c.symbol) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but %s no longer declares %s; reopen it", f.line, f.heading, c.file, c.symbol)
			}
			if c.holds != nil {
				if ok, what := c.holds(t, root); !ok {
					t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the tree no longer holds: %s; reopen it", f.line, f.heading, what)
				}
			}
			if !strings.Contains(f.body, "Closed by proposal 0095 (`"+secondHalfProposal+"`)") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  has no resolution naming %s as the proposal that closed it", f.line, f.heading, secondHalfProposal)
			}
			assertResolutionEvidence(t, root, f)
		})
	}
}

// secondHalfNote is a record the second-half work wrote on a finding it did
// not close: the state the finding keeps, and the labels its body carries.
type secondHalfNote struct {
	id     string
	open   bool
	labels []string
}

// secondHalfNotes are the findings the second-half work annotated, narrowed,
// or gave an accepted departure, and left in their state.
var secondHalfNotes = []secondHalfNote{
	{"F-11.4.10", true, []string{"**Resolution note (proposal 0095", "**Accepted departure (proposal 0093"}},
	{"F-12.8.2", false, []string{"**Annotation (proposal 0095", "escrow bucket"}},
	{"F-12.8.35", true, []string{"**Accepted departure (proposal 0095", "Bound:"}},
	{"F-12.8.43", true, []string{"**Accepted departure (proposal 0095): user erasure deletes revoked registered credentials.**", "Bound:"}},
	{"F-12.8.44", true, []string{"**Narrowed (proposal 0095", "Phase 1", "Owner: unassigned."}},
	{"F-12.8.46", true, []string{"tenant-path prefix delete", "Owner: unassigned."}},
}

// diagnosis: a finding the data-lifecycle second half left in its state was
// closed or reopened, or lost the note that says what the second half did to
// it: the resolution note, the narrowing, the annotation, or an accepted
// departure and its bound. Restore the note, and change the state only with a
// resolution that says which change did so.
func TestDataLifecycleSecondHalfNotesStayOnTheirFindings(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, n := range secondHalfNotes {
		t.Run(n.id, func(t *testing.T) {
			f := requireFinding(t, findings, n.id)
			if f.open != n.open {
				t.Errorf("BUILD-GAPS.md:%d %s\n  open = %t, but the data-lifecycle second half left it open = %t", f.line, f.heading, f.open, n.open)
			}
			for _, label := range n.labels {
				if !strings.Contains(f.body, label) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  does not carry %q", f.line, f.heading, label)
				}
			}
			assertCitedTestFilesExist(t, root, f)
		})
	}
}

// secondHalfDeparture is an accepted departure the second-half work recorded
// on a finding, and the label that opens it in BUILD-GAPS.md and in the
// deviations file.
type secondHalfDeparture struct {
	id        string
	finding   string
	deviation string
}

// secondHalfDepartures are the non-spec edge cases the second half accepted.
var secondHalfDepartures = []secondHalfDeparture{
	{"F-4.9.40", "**Accepted departure (proposal 0095): a lease-store error is silent.**", "### Departure: a lease-store error is silent"},
	{"F-12.8.30", "**Accepted departure (proposal 0095): the access-token cache flush is unwired.**", "### Departure: the access-token cache flush is unwired"},
	{"F-12.8.35", "**Accepted departure (proposal 0095):**", "### Departure: checkpoint metadata of a gateway without Postgres (F-12.8.35)"},
	{"F-12.8.34", "**Accepted departure (proposal 0095): a paused Phase 4 reruns every entry.**", "### Departure: a paused Phase 4 reruns every entry"},
	{"F-12.8.34", "**Accepted departure (proposal 0095): a billing stream after the tombstone.**", "### Departure: a billing stream after the tombstone"},
	{"F-12.8.43", "**Accepted departure (proposal 0095): user erasure deletes revoked registered credentials.**", "### Departure: user erasure deletes revoked registered credentials"},
}

// diagnosis: an accepted departure of the data-lifecycle second half lost its
// record or its bound in BUILD-GAPS.md or in the proposal's deviations file.
// A departure is a residue the work left on purpose with a stated bound;
// restore both records, unless a later change removed the residue and says so
// in a resolution note on the finding.
func TestDataLifecycleSecondHalfDeparturesKeepTheirBounds(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	deviations := readDocPage(t, filepath.Join(root, filepath.FromSlash(secondHalfDeviations)))
	for _, d := range secondHalfDepartures {
		t.Run(d.id+" "+d.deviation, func(t *testing.T) {
			f := requireFinding(t, findings, d.id)
			_, record, ok := strings.Cut(f.body, d.finding)
			if !ok {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  does not record the departure %q", f.line, f.heading, d.finding)
			}
			record, _, _ = strings.Cut(record, "\n")
			if !strings.Contains(record, "Bound:") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  records the departure %q with no bound", f.line, f.heading, d.finding)
			}
			_, entry, ok := strings.Cut(deviations, d.deviation+"\n")
			if !ok {
				t.Fatalf("%s has no entry %q", secondHalfDeviations, d.deviation)
			}
			entry, _, _ = strings.Cut(entry, "\n#")
			if !strings.Contains(entry, "**Bound:**") {
				t.Errorf("%s entry %q states no bound", secondHalfDeviations, d.deviation)
			}
		})
	}
}

// secondHalfFiling is a finding the second-half work or its tests filed, the
// owner sentence it carries, and, for a finding that blocks a test case, the
// deviation entry that records the blocked case.
type secondHalfFiling struct {
	id        string
	owner     string
	deviation string
}

// secondHalfFilings are the findings the data-lifecycle second half filed.
// The first two were filed by its cross-cutting tests and block a case of the
// tier-4 tenant deletion test.
var secondHalfFilings = []secondHalfFiling{
	{id: "F-10.2.22", owner: "Owner: unassigned.", deviation: "## Proposed: the tier-4 peer-replica refusal probe checks each Lenny-issued bearer only on its minting replica"},
	{id: "F-27.8.1", owner: "Owner: unassigned.", deviation: "## the tier-4 playground-bearer refusal case is skipped"},
	{id: "F-4.9.40", owner: "Owner: a direct fix."},
	{id: "F-4.9.41", owner: "Owner: a direct fix."},
	{id: "F-10.2.23", owner: "Owner: unassigned."},
	{id: "F-10.2.24", owner: "Owner: unassigned."},
	{id: "F-12.8.49", owner: "Owner: a direct fix."},
	{id: "F-12.8.50", owner: "Owner: a direct fix."},
	{id: "F-12.8.51", owner: "Owner: unassigned."},
	{id: "F-12.8.52", owner: "Owner: unassigned."},
	{id: "F-12.8.53", owner: "Owner: unassigned."},
	{id: "F-12.8.54", owner: "Owner: a direct fix."},
	{id: "F-12.8.55", owner: "Owner: a direct fix."},
	{id: "F-12.8.56", owner: "Owner: proposal P2"},
	{id: "F-12.8.57", owner: "Owner: proposal P4"},
	{id: "F-12.8.58", owner: "Owner: unassigned."},
	{id: "F-12.8.59", owner: "Owner: unassigned."},
	{id: "F-17.6.24", owner: "Owner: a direct fix."},
	{id: "F-27.3.12", owner: "Owner: a direct fix for the mint check; unassigned for the §12.8 scope gap."},
	{id: "F-27.3.13", owner: "Owner: unassigned."},
}

// diagnosis: a finding the data-lifecycle second half filed is missing, filed
// twice, closed without a resolution, no longer names its owner or the
// proposal, or cites a test file that no longer exists; or a finding that
// blocks a test case lost its pointer to the deviation entry recording the
// blocked case, or that entry is gone. One defect carries one finding, so
// merge two headings that name one identifier, and never renumber a filed
// identifier.
func TestDataLifecycleSecondHalfFilingsAreFiledOnceWithTheirOwners(t *testing.T) {
	root := repoRoot(t)
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))
	deviations := readDocPage(t, filepath.Join(root, filepath.FromSlash(secondHalfDeviations)))
	findings := buildGapsFindingsByID(t, root)
	for _, c := range secondHalfFilings {
		t.Run(c.id, func(t *testing.T) {
			if n := findingHeadingCount(body, c.id); n != 1 {
				t.Fatalf("BUILD-GAPS.md carries %d headings for %s, want 1", n, c.id)
			}
			f := requireFinding(t, findings, c.id)
			if !f.open {
				if !strings.Contains(f.body, "**Resolution") {
					t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
				}
				return
			}
			if !strings.Contains(f.body, c.owner) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not carry %q", f.line, f.heading, c.owner)
			}
			if c.deviation == "" && !strings.Contains(f.body, "Filed by proposal 0095 (`"+secondHalfProposal+"`)") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  does not name %s as the proposal that filed it", f.line, f.heading, secondHalfProposal)
			}
			if c.deviation != "" {
				title := strings.TrimPrefix(c.deviation, "## Proposed: ")
				if !strings.Contains(f.body, secondHalfDeviations) || !strings.Contains(f.body, title) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  does not point at the deviation entry %q in %s", f.line, f.heading, title, secondHalfDeviations)
				}
				if !strings.Contains(deviations, c.deviation+"\n") {
					t.Errorf("%s has no entry %q, which %s points at", secondHalfDeviations, c.deviation, c.id)
				}
			}
			assertCitedTestFilesExist(t, root, f)
		})
	}
}

// secondHalfPlanItem matches the remediation-plan item for the data-lifecycle
// second half, ticked and naming its implementation date.
var secondHalfPlanItem = regexp.MustCompile(
	"(?m)^- \\[x\\] \\*\\*P5b\\. Data lifecycle, second half: the remaining erasure and tenant-deletion stores, the audit-receipt\\n" +
		"  foreign key, and full_revoke\\*\\* \\(proposal 0095, implemented \\d{4}-\\d{2}-\\d{2}\\)",
)

// planEntry returns the text of the plan item item matches, from its
// checklist line up to the next top-level checklist item, or "" when no
// ticked item matches.
func planEntry(plan string, item *regexp.Regexp) string {
	loc := item.FindStringIndex(plan)
	if loc == nil {
		return ""
	}
	rest := plan[loc[1]:]
	if next := regexp.MustCompile(`(?m)^- \[`).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return plan[loc[0]:loc[1]] + rest
}

// diagnosis: the remediation plan does not record the data-lifecycle second
// half as implemented, or its Outcome no longer names the findings it closed,
// the one it left open with a note, the N-4 closure, or the findings its tests
// filed. Restore the Outcome from the BUILD-GAPS.md records it summarizes.
func TestRemediationPlanRecordsTheDataLifecycleSecondHalf(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	entry := planEntry(plan, secondHalfPlanItem)
	if entry == "" {
		t.Fatal("gateway-runtime-comms-remediation.md: the P5b item is not ticked with its implementation date")
	}
	if !strings.Contains(entry, "- **Outcome.**") {
		t.Error("the P5b item has no Outcome")
	}
	want := []string{"N-4", "step 1", "step 3", "redis_caches", "F-11.4.10", "F-10.2.22", "F-27.8.1"}
	for _, c := range closedSecondHalfFindings {
		want = append(want, c.id)
	}
	for _, w := range want {
		if !strings.Contains(entry, w) {
			t.Errorf("the P5b item does not mention %s", w)
		}
	}
}

// diagnosis: the claim register's message-queue row still says tenant
// deletion does not erase the queues, or no longer says it does. Edit the
// row's note in scripts/seed-claim-register.py and regenerate
// tests/claim-map.json; never edit the register by hand.
func TestClaimRegisterMessageQueueRowNamesBothErasurePaths(t *testing.T) {
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
	const claim = "User erasure deletes each session's message queues"
	for _, c := range doc.Claims {
		if c.Claim != claim {
			continue
		}
		if strings.Contains(c.Note, "does not yet") || !strings.Contains(c.Note, "tenant deletion") {
			t.Errorf("claim %q note = %q, want it to say tenant deletion erases the queues too", claim, c.Note)
		}
		return
	}
	t.Fatalf("tests/claim-map.json has no claim %q", claim)
}

// testGapsHeading matches a TEST-GAPS.md finding heading and captures its
// checkbox mark and identifier.
var testGapsHeading = regexp.MustCompile(`^### - \[( |x)\] (T-[0-9.]+) `)

// testGapsEntry returns whether the TEST-GAPS.md finding id is resolved and
// its text up to the next heading, failing the test when it is absent.
func testGapsEntry(t *testing.T, root, id string) (resolved bool, body string) {
	t.Helper()
	var in bool
	for _, line := range strings.Split(readDocPage(t, filepath.Join(root, "TEST-GAPS.md")), "\n") {
		if m := testGapsHeading.FindStringSubmatch(line); m != nil {
			if in {
				return resolved, body
			}
			if m[2] == id {
				in, resolved = true, m[1] == "x" && strings.Contains(line, "— RESOLVED ")
			}
			continue
		}
		if in && strings.HasPrefix(line, "#") {
			return resolved, body
		}
		if in {
			body += line + "\n"
		}
	}
	if !in {
		t.Fatalf("TEST-GAPS.md has no finding %s", id)
	}
	return resolved, body
}

// diagnosis: a test-gap record the data-lifecycle second half reconciled was
// reopened, closed, or lost its note: T-12.1.3 and T-12.8.4 are resolved by
// existing tests, and T-4.5.4 and T-12.8.9 stay open with the annotation that
// names the tests that cover part of them. Restore the record as the second
// half left it, or record the later change that moved it.
func TestDataLifecycleSecondHalfTestGapRecords(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct {
		id       string
		resolved bool
		want     []string
	}{
		{"T-12.1.3", true, []string{"proposal 0095", "tests/tier4_integration/erasure_job_test.go::"}},
		{"T-12.8.4", true, []string{"proposal 0095", "tests/tier9_security/erasure_isolation_test.go::"}},
		{"T-4.5.4", false, []string{"**Annotation (proposal 0095", "tests/tier2_component/stores/miniostore_test.go::"}},
		{"T-12.8.9", false, []string{"**Annotation (proposal 0095", "F-27.8.1", "tests/tier4_integration/tenant_deletion_phase4_test.go::"}},
	} {
		t.Run(c.id, func(t *testing.T) {
			resolved, body := testGapsEntry(t, root, c.id)
			if resolved != c.resolved {
				t.Errorf("TEST-GAPS.md %s resolved = %t, want %t", c.id, resolved, c.resolved)
			}
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("TEST-GAPS.md %s does not mention %s", c.id, w)
				}
			}
		})
	}
}
