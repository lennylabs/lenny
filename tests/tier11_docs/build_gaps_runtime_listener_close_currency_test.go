// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the open findings in BUILD-GAPS.md and
// the adapter code they describe.
//
// The runtime socket listener is pod-scoped: SocketRuntimeProcess.Close tears
// down one session's share of the runtime connection and leaves the listener
// bound, and CloseListener releases it once at adapter-process exit. An open
// finding was filed while Close still ended by closing the listener, and its
// evidence quoted that statement in the present tense. A build loop working
// the open finding reads its evidence as the current tree, so a stale quote
// sends it to re-apply a fix that already landed, or to diagnose a session
// start against the closed-listener error the tree no longer produces.
//
// The check reads Close's body from the source. When the body no longer
// closes the listener, no open finding may state in the present tense that
// Close ends with the listener close. When the body does close it, the
// statement is true and the check has nothing to hold.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7.10 (sidecar deployment model), 5.2 (pod scrub and session
// recycling), 28.5.3 (runtime socket transport)

package tier11_docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// presentTenseListenerCloseClaim matches an audit sentence stating that
// SocketRuntimeProcess.Close currently ends by closing the listener. The past
// tense ("used to end with", "ended with") records history and is allowed.
var presentTenseListenerCloseClaim = regexp.MustCompile(
	"`SocketRuntimeProcess\\.Close`\\s+(?:ends|still ends|returns)\\s+with\\s+`return p\\.listener\\.Close\\(\\)`",
)

// openFinding is one unticked BUILD-GAPS finding: its heading and the text up
// to the next heading.
type openFinding struct {
	line    int
	heading string
	body    string
}

// openBuildGapsFindings returns every finding whose heading carries an
// unticked checkbox.
func openBuildGapsFindings(t testing.TB, root string) []openFinding {
	t.Helper()
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))

	var findings []openFinding
	var current *openFinding
	for i, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "### - [ ]"):
			findings = append(findings, openFinding{line: i + 1, heading: line})
			current = &findings[len(findings)-1]
		case strings.HasPrefix(line, "#"):
			current = nil
		case current != nil:
			current.body += line + "\n"
		}
	}
	if len(findings) == 0 {
		t.Fatal("BUILD-GAPS.md: no open findings found (heading format changed?)")
	}
	return findings
}

// socketCloseClosesListener reports whether the body of
// SocketRuntimeProcess.Close in the file at path calls p.listener.Close.
func socketCloseClosesListener(t testing.TB, path string) bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Close" || !receiverIs(fn, "SocketRuntimeProcess") {
			continue
		}
		return callsListenerClose(fn.Body)
	}
	t.Fatalf("%s: no SocketRuntimeProcess.Close method (renamed or moved?)", path)
	return false
}

// receiverIs reports whether fn has a pointer or value receiver of the named type.
func receiverIs(fn *ast.FuncDecl, name string) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	ident, ok := typ.(*ast.Ident)
	return ok && ident.Name == name
}

// callsListenerClose reports whether body contains a call of the form
// <x>.listener.Close().
func callsListenerClose(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Close" {
			return true
		}
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "listener" {
			found = true
		}
		return !found
	})
	return found
}

// diagnosis: an open BUILD-GAPS finding states that SocketRuntimeProcess.Close
// ends by closing the runtime listener, but the tree's Close leaves the
// pod-scoped listener bound. Annotate the finding's evidence to record that
// the listener clause is resolved, in the past tense, rather than reverting
// the code.
func TestBuildGapsOpenFindingsMatchPodScopedRuntimeListener_spec_4_7_10(t *testing.T) {
	root := repoRoot(t)
	src := filepath.Join(root, "pkg", "adapter", "socketruntime.go")
	if socketCloseClosesListener(t, src) {
		t.Skip("SocketRuntimeProcess.Close closes the listener; a present-tense audit quote of it is accurate")
	}
	for _, f := range openBuildGapsFindings(t, root) {
		if m := presentTenseListenerCloseClaim.FindString(f.body); m != "" {
			t.Errorf("BUILD-GAPS.md:%d %s\n  states %q, but SocketRuntimeProcess.Close no longer closes the pod-scoped listener (CloseListener does, at adapter exit)",
				f.line, f.heading, m)
		}
	}
}

// diagnosis: the matcher the currency check relies on no longer recognizes
// the present-tense claim, or now flags the past-tense record, so the check
// above passes or fails vacuously.
func TestBuildGapsListenerCloseClaimMatcher_spec_4_7_10(t *testing.T) {
	present := "Separately, `SocketRuntimeProcess.Close` ends with `return p.listener.Close()`, destroying"
	past := "Separately, `SocketRuntimeProcess.Close` used to end with `return p.listener.Close()`, destroying"
	if !presentTenseListenerCloseClaim.MatchString(present) {
		t.Errorf("matcher misses the present-tense claim %q", present)
	}
	if presentTenseListenerCloseClaim.MatchString(past) {
		t.Errorf("matcher flags the past-tense record %q", past)
	}
}

// diagnosis: the AST probe no longer tells a Close body that closes the
// listener from one that leaves it bound, so the currency check skips or
// runs against the wrong premise.
func TestSocketCloseListenerProbe_spec_4_7_10(t *testing.T) {
	cases := map[string]struct {
		src  string
		want bool
	}{
		"closes listener": {
			src:  "package x\ntype SocketRuntimeProcess struct{ listener interface{ Close() error } }\nfunc (p *SocketRuntimeProcess) Close() error { return p.listener.Close() }\n",
			want: true,
		},
		"leaves listener bound": {
			src:  "package x\ntype SocketRuntimeProcess struct{ listener interface{ Close() error } }\nfunc (p *SocketRuntimeProcess) Close() error { return nil }\nfunc (p *SocketRuntimeProcess) CloseListener() error { return p.listener.Close() }\n",
			want: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "x.go")
			if err := os.WriteFile(path, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if got := socketCloseClosesListener(t, path); got != tc.want {
				t.Errorf("socketCloseClosesListener = %v, want %v", got, tc.want)
			}
		})
	}
}
