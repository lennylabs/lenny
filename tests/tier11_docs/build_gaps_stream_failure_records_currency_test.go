// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the CH-ATTACH
// stream-failure work and the tree they describe.
//
// The build loops read the claim register, BUILD-GAPS.md, and the remediation
// plan as statements about the current tree. These checks hold three of those
// statements to the code:
//
//   - The claim rows for the held CH-ATTACH stream and its failure report name
//     symbols the files they cite still declare, so a rename or a move cannot
//     leave a WIRED row pointing at nothing.
//   - The two closed stream findings stay closed only while the code that
//     closed them is in the tree, and every test file their resolutions cite
//     exists, so a reader can rerun the evidence.
//   - The findings filed for the gaps the work left stay open while the probe
//     of each gap still finds it, and the remediation-plan item is ticked with
//     the proposal that landed it.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 28.5.1 (Gateway to pod), 7.3 (Retry and Resume), 10.1.1 (Stateless
// Replicas and Per-Session Coordination)

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

// streamFailureClaims are the claim-register rows that record the held
// CH-ATTACH stream and the gateway's reaction to its failure.
var streamFailureClaims = []string{
	"`Attach` content stream",
	"Coordinating replica reports a CH-ATTACH stream failure as runtime_crash, " +
		"releases the pod with the failed disposition, and does not redial Attach",
}

// surfaceSymbol matches one `path.go` `Symbol` pair in a claim surface. The
// symbol is a function, a type, or a Type.Method.
var surfaceSymbol = regexp.MustCompile("`([\\w./-]+\\.go)` `([\\w]+(?:\\.[\\w]+)?)`")

// surfaceSymbols returns the file and symbol pairs a claim surface names.
func surfaceSymbols(surface string) [][2]string {
	var out [][2]string
	for _, m := range surfaceSymbol.FindAllStringSubmatch(surface, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// declaresSymbol reports whether file declares symbol. A bare name matches a
// function, a type, or a method of that name on any receiver, because the
// register names a method by its bare name when the file declares one
// receiver for it. Type.Method matches only a method on Type or *Type.
func declaresSymbol(file *ast.File, symbol string) bool {
	recv, name, isMethod := strings.Cut(symbol, ".")
	if !isMethod {
		name, recv = recv, ""
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == name && (!isMethod || receiverName(d) == recv) {
				return true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && !isMethod && ts.Name.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// receiverName returns the base type name of a method's receiver, or "" for a
// function.
func receiverName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	expr := d.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// claimRows returns the claim register's rows keyed by claim.
func claimRows(t testing.TB, root string) map[string]map[string]string {
	t.Helper()
	var doc struct {
		Claims []map[string]string `json:"claims"`
	}
	if err := json.Unmarshal([]byte(readDocPage(t, filepath.Join(root, "tests", "claim-map.json"))), &doc); err != nil {
		t.Fatalf("parse tests/claim-map.json: %v", err)
	}
	out := map[string]map[string]string{}
	for _, c := range doc.Claims {
		out[c["claim"]] = c
	}
	return out
}

// diagnosis: a claim-register row for the held CH-ATTACH stream or its failure
// report names a symbol its file no longer declares, or the row is missing or
// not WIRED. Regenerate tests/claim-map.json from scripts/seed-claim-register.py
// after correcting the surface there; do not hand-edit the register.
func TestStreamFailureClaimRowsNameDeclaredSymbols_spec_28_5_1(t *testing.T) {
	root := repoRoot(t)
	rows := claimRows(t, root)
	for _, claim := range streamFailureClaims {
		t.Run(claim, func(t *testing.T) {
			row, ok := rows[claim]
			if !ok {
				t.Fatalf("tests/claim-map.json has no row %q", claim)
			}
			if row["status"] != "WIRED" || row["spec_anchor"] != "#2851-gateway-to-pod" {
				t.Errorf("row %q: status %q anchor %q, want WIRED at #2851-gateway-to-pod", claim, row["status"], row["spec_anchor"])
			}
			pairs := surfaceSymbols(row["surface"])
			if len(pairs) == 0 {
				t.Fatalf("row %q: surface %q names no `file.go` `Symbol` pair", claim, row["surface"])
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
		})
	}
}

// closedStreamFinding is a closed stream finding and the declaration whose
// presence keeps it closed.
type closedStreamFinding struct {
	id     string
	file   string
	symbol string
}

// closedStreamFindings are the findings the stream-failure work closed.
var closedStreamFindings = []closedStreamFinding{
	{id: "F-7.3.28", file: "pkg/gateway/session/executor/podstream.go", symbol: "attachConn"},
	{id: "F-7.3.27", file: "pkg/gateway/sessionserver/stream_failure.go", symbol: "Server.ReportAttachStreamFailure"},
}

// citedTestFile matches a backticked repository path to a Go test file.
var citedTestFile = regexp.MustCompile("`((?:pkg|tests|cmd)/[\\w./-]+_test\\.go)`")

// diagnosis: a closed CH-ATTACH stream finding no longer matches the tree.
// Either the code that closed it was removed, so the finding must be reopened,
// or its resolution cites a test file that no longer exists, so the evidence
// cannot be rerun; update the resolution to name the current test file.
func TestClosedStreamFindingsCiteLandedCodeAndTests_spec_7_3(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range closedStreamFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open, but the stream-failure work closed it", f.line, f.heading)
			}
			if !declaresSymbol(parseGoFile(t, filepath.Join(root, filepath.FromSlash(c.file))), c.symbol) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but %s no longer declares %s; reopen it", f.line, f.heading, c.file, c.symbol)
			}
			if !strings.Contains(f.body, "**Resolution") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed with no resolution note", f.line, f.heading)
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
		})
	}
}

// filedStreamGap is a finding the stream-failure work filed for a gap it left,
// with a probe that reports whether the tree still has the gap.
type filedStreamGap struct {
	id      string
	what    string
	present func(t testing.TB, root string) bool
}

// isRunningPodAdoptsOnly matches the body of the Sweeper's adoption predicate
// when it admits only running and input_required rows.
var isRunningPodAdoptsOnly = regexp.MustCompile(
	`(?s)func isRunningPod\(row sessionstore\.Session\) bool \{\s*return \(row\.State == session\.StateRunning \|\| row\.State == session\.StateInputRequired\) && row\.PodAssignment != ""\s*\}`,
)

// filedStreamGaps lists the filed findings whose gap a declaration probe can
// observe.
var filedStreamGaps = []filedStreamGap{
	{
		id:   "F-10.1.22",
		what: "the adapter's Attach handler never reads coordination_generation",
		present: func(t testing.TB, root string) bool {
			return !strings.Contains(readDocPage(t, filepath.Join(root, "pkg", "adapter", "attach.go")), "CoordinationGeneration")
		},
	},
	{
		id:   "F-10.1.26",
		what: "the Sweeper adopts a lapsed lease only for a running or input_required session",
		present: func(t testing.TB, root string) bool {
			return sourceMatches(t, filepath.Join(root, "pkg", "gateway", "coordination", "coordination", "coordination.go"), isRunningPodAdoptsOnly)
		},
	},
	{
		id:   "F-7.3.31",
		what: "the code serves retryPolicy.mode client_only and spec/ does not define it",
		present: func(t testing.TB, root string) bool {
			served := strings.Contains(readDocPage(t, filepath.Join(root, "pkg", "api", "v1", "session", "retry_policy.go")), `"client_only"`)
			return served && !specMentions(t, root, "client_only")
		},
	},
}

// specMentions reports whether any file under spec/ contains needle.
func specMentions(t testing.TB, root, needle string) bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "spec", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob spec/*.md: %v (%d files)", err, len(paths))
	}
	for _, p := range paths {
		if strings.Contains(readDocPage(t, p), needle) {
			return true
		}
	}
	return false
}

// diagnosis: a finding the stream-failure work filed disagrees with the tree.
// Either it was closed while the gap its evidence names is still in the code,
// or the gap was fixed and the finding is still open. Close it with a
// resolution note when the gap is fixed, and reopen it when it was closed
// without a fix; do not change the code to match the record.
func TestFiledStreamFindingsMatchTheTree_spec_10_1_1(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, gap := range filedStreamGaps {
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

// streamFailurePlanItem matches the remediation-plan item for the gateway
// stream-failure proposal, ticked and naming the proposal and its
// implementation date.
var streamFailurePlanItem = regexp.MustCompile(`(?m)^- \[x\] Gateway stream-failure proposal \(proposal 0091, implemented \d{4}-\d{2}-\d{2}`)

// diagnosis: the remediation plan does not record the gateway stream-failure
// proposal as implemented, although both findings it closes are closed. Tick
// the item and record the proposal number and the implementation date.
func TestRemediationPlanRecordsTheStreamFailureProposal_spec_28_5_1(t *testing.T) {
	root := repoRoot(t)
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	if !streamFailurePlanItem.MatchString(plan) {
		t.Error("gateway-runtime-comms-remediation.md: the gateway stream-failure item is not ticked with the proposal number and implementation date")
	}
}

// diagnosis: the surface parser or the declaration probe no longer reads a
// claim surface or a Go declaration the way the checks above assume, so they
// pass or fail vacuously.
func TestStreamFailureRecordProbes_spec_28_5_1(t *testing.T) {
	got := surfaceSymbols("`pkg/a/b.go` `streamFor`, `pkg/a/c.go` `attachConn`, handler `pkg/d/e.go` `Server.Attach`")
	want := [][2]string{{"pkg/a/b.go", "streamFor"}, {"pkg/a/c.go", "attachConn"}, {"pkg/d/e.go", "Server.Attach"}}
	if len(got) != len(want) {
		t.Fatalf("surfaceSymbols = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("surfaceSymbols[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	src := "package x\ntype Server struct{}\ntype attachConn struct{}\n" +
		"func (s *Server) Attach() {}\nfunc (c attachConn) close() {}\nfunc streamFor() {}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	file := parseGoFile(t, path)
	for symbol, want := range map[string]bool{
		"streamFor":        true,
		"attachConn":       true,
		"Server.Attach":    true,
		"attachConn.close": true,
		"Attach":           true,
		"close":            true,
		"Detach":           false,
		"Server.Detach":    false,
		"Client.Attach":    false,
		"Server.close":     false,
	} {
		if got := declaresSymbol(file, symbol); got != want {
			t.Errorf("declaresSymbol(%q) = %v, want %v", symbol, got, want)
		}
	}

	if !isRunningPodAdoptsOnly.MatchString("func isRunningPod(row sessionstore.Session) bool {\n\treturn (row.State == session.StateRunning || row.State == session.StateInputRequired) && row.PodAssignment != \"\"\n}") {
		t.Error("isRunningPodAdoptsOnly misses the shipped predicate")
	}
	if isRunningPodAdoptsOnly.MatchString("func isRunningPod(row sessionstore.Session) bool {\n\treturn (row.State == session.StateRunning || row.State == session.StateInputRequired || row.State == session.StateSuspended) && row.PodAssignment != \"\"\n}") {
		t.Error("isRunningPodAdoptsOnly matches a predicate that also adopts suspended rows")
	}
	if !streamFailurePlanItem.MatchString("- [x] Gateway stream-failure proposal (proposal 0091, implemented 2026-10-07, merge on `proposal-b`),\n") {
		t.Error("streamFailurePlanItem misses a ticked item")
	}
	if streamFailurePlanItem.MatchString("- [ ] Gateway stream-failure proposal (not yet written), implemented after proposal 0090\n") {
		t.Error("streamFailurePlanItem matches an unticked item")
	}
}
