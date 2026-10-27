// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the session suspend and
// resume work and the tree they describe.
//
// The build loops read the claim register, BUILD-GAPS.md, and the remediation
// plan as statements about the current tree. These checks hold those
// statements to the code:
//
//   - The WIRED claim rows for the resume driver, coordination-lease
//     adoption, the suspension release, idle suspension, the adapter session
//     seal, the heartbeat session fields, the backlog resume, the erasure of
//     the session message queues, the generation-scoped lease return, the
//     suspended-session lifetime, the concurrent-session count, and active
//     session age name symbols their files still declare.
//   - The "Session inbox redelivery" row stays ABSENT under cross-replica
//     message forwarding (R19) and names the DLQ hold that remains absent.
//   - The inter-replica rows carry the forward, the replica address, the
//     forward listener, and the binding-release fan-out at the status the
//     specification's landing gives them, and the generated rows they
//     replace are retired.
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
// Coordination), 28.4 (Claim register)

package tier11_docs_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// suspendResumeClaim is a claim row the suspend and resume work added and the
// §28 heading it anchors to.
type suspendResumeClaim struct {
	claim  string
	anchor string
}

// suspendResumeClaims are the WIRED claim rows of the suspend and resume work.
var suspendResumeClaims = []suspendResumeClaim{
	{"Resume driver restores a resume_pending session onto a replacement pod and re-attaches CH-ATTACH", "#2851-gateway-to-pod"},
	{"Coordination lease adoption in the recovering and pod-holding states, with no lease and a released mirror for a podless suspended session", "#register-entry-register"},
	{"Suspension release checkpoints a held-pod suspended session at maxSuspendedPodHoldSeconds and releases its pod", "#2851-gateway-to-pod"},
	{"Idle suspension seals, checkpoints, and suspends an idle running session and releases its pod or slot", "#2851-gateway-to-pod"},
	{"Adapter session seal keyed on the session's last activity", "#2851-gateway-to-pod"},
	{"SealSession unary RPC on the CH-ATTACH service", "#2851-gateway-to-pod"},
	{"UnsealSession unary RPC on the CH-ATTACH service", "#2851-gateway-to-pod"},
	{"heartbeat frame names its session in sessionId", "#2853-intra-pod"},
	{"heartbeat_ack sessionId and busy fields, with the heartbeat busy compliance check", "#2853-intra-pod"},
	{"Backlog resume of a suspended session that holds messages", "#2851-gateway-to-pod"},
	{"User erasure deletes each session's message queues", "#2857-gateway-to-store"},
	{"Pod release returns only the credential leases at or below the binding's coordination generation", "#2851-gateway-to-pod"},
	{"Suspended-session lifetime expiry", "#2857-gateway-to-store"},
	{"Concurrent-session count excludes suspended sessions, and a resume of a suspended session passes the admission check", "#2857-gateway-to-store"},
	{"Session age measured as active time", "#2857-gateway-to-store"},
}

// diagnosis: a claim-register row of the suspend and resume work is missing,
// is not WIRED at its §28 heading, or names a symbol its file no longer
// declares. Correct the row in scripts/seed-claim-register.py and regenerate
// tests/claim-map.json; do not hand-edit the register.
func TestSuspendResumeClaimRowsNameDeclaredSymbols_spec_7_3(t *testing.T) {
	root := repoRoot(t)
	rows := claimRows(t, root)
	for _, c := range suspendResumeClaims {
		t.Run(c.claim, func(t *testing.T) {
			row, ok := rows[c.claim]
			if !ok {
				t.Fatalf("tests/claim-map.json has no row %q", c.claim)
			}
			if row["status"] != "WIRED" || row["spec_anchor"] != c.anchor {
				t.Errorf("row %q: status %q anchor %q, want WIRED at %s", c.claim, row["status"], row["spec_anchor"], c.anchor)
			}
			if strings.TrimSpace(row["note"]) == "" {
				t.Errorf("row %q carries no note stating what the surface does", c.claim)
			}
			assertSurfaceDeclared(t, root, c.claim, row["surface"])
		})
	}
}

// assertSurfaceDeclared fails the test when surface names no `file.go`
// `Symbol` pair, or names a file or symbol the tree does not declare.
func assertSurfaceDeclared(t *testing.T, root, claim, surface string) {
	t.Helper()
	pairs := surfaceSymbols(surface)
	if len(pairs) == 0 {
		t.Fatalf("row %q: surface %q names no `file.go` `Symbol` pair", claim, surface)
	}
	for _, p := range pairs {
		path := filepath.Join(root, filepath.FromSlash(p[0]))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("row %q names %s, which does not exist", claim, p[0])
			continue
		}
		if !declaresSymbol(parseGoFile(t, path), p[1]) {
			t.Errorf("row %q names %s in %s, which the file does not declare", claim, p[1], p[0])
		}
	}
}

// diagnosis: the "Session inbox redelivery" claim row no longer records the
// off-holder hold. A message a non-coordinating replica receives is not
// buffered in that replica's in-process inbox: §29.3 Forwarding rule 6 holds a
// message whose forward is not delivered in the session DLQ, which the
// coordinating replica drains, and the REST and MCP message sites enqueue that
// hold. The row is WIRED and names the declared hold. Correct the
// SURFACE_OVERRIDES entry in scripts/seed-claim-register.py and regenerate.
//
// spec: 29.3 (Interactive message send), 7.2 (Interactive Session Model)
func TestSessionInboxRedeliveryRowNamesTheForwardHold_spec_29_3(t *testing.T) {
	root := repoRoot(t)
	row, ok := claimRows(t, root)["Session inbox redelivery"]
	if !ok {
		t.Fatal("tests/claim-map.json has no row \"Session inbox redelivery\"")
	}
	if row["status"] != "WIRED" || row["deferral_id"] != "" {
		t.Errorf("status %q deferral %q, want WIRED with no deferral", row["status"], row["deferral_id"])
	}
	for _, stale := range []string{"no dequeue path", "durableInbox: false", "in-process inbox", "no off-holder path"} {
		if strings.Contains(row["surface"], stale) {
			t.Errorf("surface %q still names %q, which the DLQ hold replaced", row["surface"], stale)
		}
	}
	for _, want := range []string{"forwardMessageBatch", "HoldUndeliveredForward"} {
		if !strings.Contains(row["surface"], want) {
			t.Errorf("surface %q does not name %s", row["surface"], want)
		}
	}
	for _, want := range []string{"DLQ", "Forwarding rule 6", "recovering-row TTL"} {
		if !strings.Contains(row["note"], want) {
			t.Errorf("note %q does not name %q", row["note"], want)
		}
	}
	assertSurfaceDeclared(t, root, "Session inbox redelivery", row["surface"])
}

// interReplicaClaim is a claim-register row the inter-replica forward and the
// binding-release fan-out carry, with the status, heading, and step it holds
// while the specification is ahead of the tree.
type interReplicaClaim struct {
	claim    string
	status   string
	anchor   string
	deferral string
}

// interReplicaClaims are the rows that restate the inter-replica mechanisms
// under the names §28.5.4, §28.5.7, §10.1.1, and §10.3 give them.
var interReplicaClaims = []interReplicaClaim{
	{"Off-holder request forward on CH-HOLDERFORWARD", "WIRED", "#2854-inter-replica", ""},
	{"Replica address in the REG-COORDLEASE holder value", "WIRED", "#2854-inter-replica", ""},
	{"Inter-replica forward listener with gateway peer validation (NET-072)", "WIRED", "#2854-inter-replica", ""},
	{"Session inbox redelivery", "WIRED", "#2854-inter-replica", ""},
	{"Input-wait resolution across replicas", "ABSENT", "#2854-inter-replica", "R20"},
	{"Tool-approval resolution across replicas", "WIRED", "#2854-inter-replica", ""},
	{"CH-BINDRELEASE binding-release fan-out", "WIRED", "#2857-gateway-to-store", ""},
}

// retiredInterReplicaClaims are the generated rows the restated rows above
// replace. Each one names a mechanism the specification no longer states in
// those words: a forward with no carrier, a coordinator_address field, and a
// gateway-to-gateway link entry.
var retiredInterReplicaClaims = []string{
	"Cross-replica message forward",
	"`coordinator_address` population",
	"Gateway-to-gateway transport",
}

// diagnosis: the claim register no longer carries the inter-replica rows at the
// status, heading, and step the specification's landing gives them, still
// carries a generated row the restated rows replace, or a WIRED
// CH-BINDRELEASE or off-holder forward row names a symbol the tree no longer
// declares. Correct
// RETIRED, SURFACE_OVERRIDES, or EXPLICIT in scripts/seed-claim-register.py and
// regenerate tests/claim-map.json; do not hand-edit the register.
//
// spec: 28.5.4 (Inter-replica), 28.5.7 (Gateway-to-store), 29.3 (Interactive
// message send), 10.1.1 (Stateless Replicas and Per-Session Coordination),
// 10.3 (mTLS PKI), 12.8 (Compliance Interfaces)
func TestInterReplicaClaimRowsCarryTheForwardAndBindingRelease_spec_28_5_4(t *testing.T) {
	root := repoRoot(t)
	rows := claimRows(t, root)
	for _, c := range interReplicaClaims {
		t.Run(c.claim, func(t *testing.T) {
			row, ok := rows[c.claim]
			if !ok {
				t.Fatalf("tests/claim-map.json has no row %q", c.claim)
			}
			if row["status"] != c.status || row["spec_anchor"] != c.anchor || row["deferral_id"] != c.deferral {
				t.Errorf("row %q: status %q anchor %q deferral %q, want %s at %s deferred to %q",
					c.claim, row["status"], row["spec_anchor"], row["deferral_id"], c.status, c.anchor, c.deferral)
			}
		})
	}
	for _, claim := range retiredInterReplicaClaims {
		if _, ok := rows[claim]; ok {
			t.Errorf("tests/claim-map.json still carries the retired row %q", claim)
		}
	}
	fanOut := rows["CH-BINDRELEASE binding-release fan-out"]
	if !strings.Contains(fanOut["surface"], "cmd/lenny-gateway/adminrouter.go") {
		t.Errorf("CH-BINDRELEASE surface %q does not name the propagator construction in cmd/lenny-gateway/adminrouter.go", fanOut["surface"])
	}
	assertSurfaceDeclared(t, root, "CH-BINDRELEASE binding-release fan-out", fanOut["surface"])
	forward := rows["Off-holder request forward on CH-HOLDERFORWARD"]
	assertSurfaceDeclared(t, root, "Off-holder request forward on CH-HOLDERFORWARD", forward["surface"])
}

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
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))
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

// suspendResumeDeviations is the deviations record of the proposal that
// added the off-holder running-row skip.
const suspendResumeDeviations = "proposals/0092_new_write-the-session-suspend-and-resume-proposal-that-gateway/" +
	"0092_new_write-the-session-suspend-and-resume-proposal-that-gateway.deviations.md"

// diagnosis: a test skips with "blocked:" on a BUILD-GAPS finding that is
// closed or absent, so the skip outlived its reason and hides a case that
// now has to run; remove the skip. Or the off-holder running-row skip has
// lost the deviations entry that records why a required case is skipped.
func TestBlockedSkipsNameOpenFindings_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	skips := blockedFindingSkips(t, root, "tests", "pkg", "cmd", "sdks")
	if len(skips["F-7.2.38"]) == 0 {
		t.Fatal("no test skips on F-7.2.38; the probe no longer reads the skip it was written for")
	}
	for id, files := range skips {
		f, ok := findings[id]
		switch {
		case !ok:
			t.Errorf("%v skip on %s, which BUILD-GAPS.md does not carry", files, id)
		case !f.open:
			t.Errorf("%v skip on %s, which is closed (BUILD-GAPS.md:%d); remove the skip", files, id, f.line)
		}
	}
	if !strings.Contains(readDocPage(t, filepath.Join(root, filepath.FromSlash(suspendResumeDeviations))), "F-7.2.38") {
		t.Errorf("%s records no deviation for the F-7.2.38 skip", suspendResumeDeviations)
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
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
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
	if got := blockedOnFinding.FindStringSubmatch(`t.Skip("blocked: F-7.2.38 (owner: R19) x")`); len(got) < 2 || got[1] != "F-7.2.38" {
		t.Errorf("blockedOnFinding read %v, want F-7.2.38", got)
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
