// SPDX-License-Identifier: MIT

// docs-tier consistency checks between the records of the CH-ATTACH
// stream-failure work and the tree they describe.
//
// The build loops read BUILD-GAPS.md and the remediation plan as statements
// about the current tree. These checks hold two of those statements to the
// code:
//
//   - The two closed stream findings stay closed only while the code that
//     closed them is in the tree, and every test file their resolutions cite
//     exists, so a reader can rerun the evidence.
//   - The findings filed for the gaps the work left stay open while the probe
//     of each gap still finds it, and the remediation-plan item is ticked with
//     the proposal that landed it.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other docs checks.
//
// spec: 28.5.1 (Gateway to pod), 7.3 (Retry and Resume), 10.1.1 (Stateless
// Replicas and Per-Session Coordination)

package docs_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// declaresSymbol reports whether file declares symbol. A bare name matches a
// function, a type, or a method of that name on any receiver, because the
// records name a method by its bare name when the file declares one receiver
// for it. Type.Method matches only a method on Type or *Type.
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

// proposalStepLabel matches a numbered build step such as "step-1" or
// "step 2". The proposal numbers its own build sequence, and the specification
// does not, so a closure record that groups its evidence by such a label names
// something a BUILD-GAPS.md reader cannot look up.
var proposalStepLabel = regexp.MustCompile(`(?i)\bstep[- ]\d+\b`)

// diagnosis: a closed CH-ATTACH stream finding no longer matches the tree.
// Either the code that closed it was removed, so the finding must be reopened,
// or its resolution cites a test file that no longer exists, so the evidence
// cannot be rerun; update the resolution to name the current test file. A
// resolution that names a proposal's numbered build step is rewritten to
// describe the commits it groups instead.
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
			if label := proposalStepLabel.FindString(f.body); label != "" {
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

// adoptionPredicate is the function in the Sweeper that decides which states
// a lapsed coordination lease is adopted in.
const adoptionPredicate = "adoptedState"

// funcMentionsSelector reports whether the body of the top-level function
// named fn in file references pkg.sel. It fails the test when file declares no
// such function, so a rename does not turn the probe vacuous.
func funcMentionsSelector(t testing.TB, file *ast.File, fn, pkg, sel string) bool {
	t.Helper()
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Recv != nil || d.Name.Name != fn || d.Body == nil {
			continue
		}
		found := false
		ast.Inspect(d.Body, func(n ast.Node) bool {
			if se, ok := n.(*ast.SelectorExpr); ok && se.Sel.Name == sel {
				if id, ok := se.X.(*ast.Ident); ok && id.Name == pkg {
					found = true
				}
			}
			return !found
		})
		return found
	}
	t.Fatalf("no top-level function %s with a body; update the probe to the predicate that decides adoption", fn)
	return false
}

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
		what: "the Sweeper's adoption predicate does not admit a starting session",
		present: func(t testing.TB, root string) bool {
			file := parseGoFile(t, filepath.Join(root, "pkg", "gateway", "coordination", "coordination", "coordination.go"))
			return !funcMentionsSelector(t, file, adoptionPredicate, "session", "StateStarting")
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
	plan := readRecord(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	if !streamFailurePlanItem.MatchString(plan) {
		t.Error("gateway-runtime-comms-remediation.md: the gateway stream-failure item is not ticked with the proposal number and implementation date")
	}
}

// diagnosis: the declaration probe no longer reads a Go declaration the way
// the checks above assume, so they pass or fail vacuously.
func TestStreamFailureRecordProbes_spec_28_5_1(t *testing.T) {
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

	predSrc := "package x\n" +
		"func adoptedState(row S) bool {\n\tswitch row.State {\n\tcase session.StateResumePending, session.StateAwaitingClientAction:\n\t\treturn true\n\t}\n\treturn podHeld(row)\n}\n" +
		"func other() { _ = session.StateStarting }\n"
	if err := os.WriteFile(path, []byte(predSrc), 0o600); err != nil {
		t.Fatalf("write predicate fixture: %v", err)
	}
	if funcMentionsSelector(t, parseGoFile(t, path), adoptionPredicate, "session", "StateStarting") {
		t.Error("funcMentionsSelector reads a selector outside the predicate's body")
	}
	predSrc = strings.Replace(predSrc, "session.StateAwaitingClientAction:", "session.StateAwaitingClientAction, session.StateStarting:", 1)
	if err := os.WriteFile(path, []byte(predSrc), 0o600); err != nil {
		t.Fatalf("write predicate fixture: %v", err)
	}
	if !funcMentionsSelector(t, parseGoFile(t, path), adoptionPredicate, "session", "StateStarting") {
		t.Error("funcMentionsSelector misses a predicate that admits starting")
	}
	if !proposalStepLabel.MatchString("and, from the step-2 test commits (cd554fe18)") || !proposalStepLabel.MatchString("Step 1 test commits") {
		t.Error("proposalStepLabel misses a numbered build-step label")
	}
	if proposalStepLabel.MatchString("and, from the stream-failure test commits (cd554fe18)") {
		t.Error("proposalStepLabel matches a resolution with no build-step label")
	}
	if !streamFailurePlanItem.MatchString("- [x] Gateway stream-failure proposal (proposal 0091, implemented 2026-10-07, merge on `proposal-b`),\n") {
		t.Error("streamFailurePlanItem misses a ticked item")
	}
	if streamFailurePlanItem.MatchString("- [ ] Gateway stream-failure proposal (not yet written), implemented after proposal 0090\n") {
		t.Error("streamFailurePlanItem matches an unticked item")
	}
}
