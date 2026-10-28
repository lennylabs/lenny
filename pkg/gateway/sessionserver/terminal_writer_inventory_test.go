// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// terminalWriter is one row of the terminal writer inventory: the function
// whose mutator assigns a terminal state, and, for a funnel or hook writer,
// the function that opens the terminal claim before the write, or before
// the call to the writer when the claim function is not the writer.
type terminalWriter struct {
	file   string
	writer string
	// claim is the function that calls BeginTerminal before the Update; empty
	// for a writer on the none path.
	claim string
}

// terminalWriterInventory is the terminal writer inventory as the code
// spells it. A writer whose mutator lives in a helper names the helper:
// writeTerminal is the shared mutator of expireSession, failSession,
// failFinalizing, and cancelCascadedChild; cancelNode and cancelSession are
// the mutators of cancelSubtree and the lenny/cancel_session handler;
// expireOrphan is sweepTenant's; failPreRunning is sweepPreRunning's,
// expireAwaiting is sweepAwaitingClientAction's, expireMaxAge is
// sweepMaxAge's, expireIdle is sweepIdle's, and expireSuspendedIfDue is
// sweepSuspended's; transitionTerminate is applied inside
// handleTerminalWrite's mutator.
var terminalWriterInventory = []terminalWriter{
	{"pkg/gateway/sessionserver/session_transitions.go", "handleDelete", "handleTerminalWrite"},
	{"pkg/gateway/sessionserver/session_transitions.go", "transitionTerminate", "handleTerminalWrite"},
	{"pkg/gateway/sessionserver/failure.go", "transitionToFailed", "transitionToFailed"},
	{"pkg/gateway/sessionserver/session_terminal.go", "writeTerminal", ""},
	{"pkg/gateway/sessionserver/derive.go", "persistDeriveFailure", ""},
	{"pkg/gateway/sessionserver/seal.go", "failWorkspaceSeal", ""},
	{"pkg/gateway/runtime/watchdog/watchdog.go", "failPreRunning", "failPreRunning"},
	{"pkg/gateway/runtime/watchdog/watchdog.go", "expireAwaiting", "expireAwaiting"},
	{"pkg/gateway/runtime/watchdog/watchdog.go", "expireIdle", "expireIdle"},
	{"pkg/gateway/runtime/watchdog/suspended.go", "expireSuspendedIfDue", "expireSuspendedIfDue"},
	{"pkg/gateway/runtime/watchdog/watchdog.go", "expireMaxAge", "expireMaxAge"},
	{"pkg/gateway/session/orphansession/orphansession.go", "failOrphan", "failOrphan"},
	{"pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go", "expireOrphan", "expireOrphan"},
	{"pkg/gateway/mcpfabric/mcptools/mcptools.go", "cancelNode", "cancelNode"},
	{"pkg/gateway/mcpfabric/mcptools/mcptools_register.go", "cancelSession", "cancelSession"},
	{"pkg/gateway/externalapi/admin/users.go", "cancelForRevoke", "cancelForRevoke"},
	{"pkg/gateway/environment/translator/openai_chat.go", "handleCreateCompletion", ""},
	{"pkg/gateway/environment/translator/open_responses.go", "handleCreate", ""},
	{"cmd/lenny-gateway/session_admin.go", "ForceTerminate", "ForceTerminate"},
	{"cmd/lenny-gateway/session_budget.go", "terminate", "TerminateSession"},
	{"cmd/lenny-gateway/reconciler_workers.go", "startReconcilerWorkers", "startReconcilerWorkers"},
}

// terminalValueBuilders are functions that build a session value carrying a
// terminal state without writing it: the checkpointer's ReleaseGone seam
// hands ReleaseTerminalBinding a failed-state row for a session whose row is
// gone, so the binding release applies the failed disposition. Each one is
// excluded from the writer scan and must call no store writer.
var terminalValueBuilders = []terminalWriter{
	{file: "cmd/lenny-gateway/pod_lifecycle.go", writer: "ReleaseGone"},
}

// storeWriteCall names the calls that persist or claim a terminal session
// state.
func storeWriteCall(n string) bool {
	switch n {
	case "Update", "Create", "CreateIfGeneration", "MarkTerminated", "writeTerminal", "BeginTerminal", "beginTerminal":
		return true
	}
	return false
}

// writeTerminalCallers are the hook-path writers that write through
// writeTerminal, each opening its claim before the call.
var writeTerminalCallers = []string{"cancelCascadedChild", "expireSession", "failFinalizing", "failSession"}

var terminalStateNames = map[string]bool{
	"StateCompleted": true, "StateFailed": true, "StateCancelled": true, "StateExpired": true,
}

// sessionStatePackage is the package qualifier that spells a session state.
// Other packages reuse the terminal state names for unrelated state machines,
// such as the issued-token store's impersonation row states, and a State
// field set from one of those is not a terminal session-state write.
const sessionStatePackage = "session"

// isTerminalSessionState reports whether e is a session-package terminal
// state selector, such as session.StateExpired.
func isTerminalSessionState(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || !terminalStateNames[sel.Sel.Name] {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == sessionStatePackage
}

type scannedFunc struct {
	file string
	decl *ast.FuncDecl
}

// repoScan memoizes scanRepo. Parsing every Go file under pkg/ and cmd/
// takes several seconds under -race, and the inventory tests that call it
// only read the result, so the parse runs once per test binary.
var repoScan struct {
	once  sync.Once
	funcs map[string]scannedFunc
	fset  *token.FileSet
	err   error
}

// scanRepo parses every non-test Go file under pkg/ and cmd/ and returns its
// function declarations keyed by "file func". Callers must treat the map and
// the declarations as read-only, because every caller shares one copy.
func scanRepo(t *testing.T) (map[string]scannedFunc, *token.FileSet) {
	t.Helper()
	repoScan.once.Do(func() {
		repoScan.funcs, repoScan.fset, repoScan.err = parseRepoFuncs()
	})
	if repoScan.err != nil {
		t.Fatal(repoScan.err)
	}
	return repoScan.funcs, repoScan.fset
}

// parseRepoFuncs is scanRepo's uncached parse.
func parseRepoFuncs() (map[string]scannedFunc, *token.FileSet, error) {
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	out := map[string]scannedFunc{}
	for _, dir := range []string{"pkg", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() && info.Name() == "testdata" {
				return filepath.SkipDir
			}
			if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, p, nil, 0)
			if perr != nil {
				return perr
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
					out[rel+" "+fd.Name.Name] = scannedFunc{file: rel, decl: fd}
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", dir, err)
		}
	}
	return out, fset, nil
}

// terminalAssignment reports the first position at which fn assigns a
// terminal session state (a session-package state selector), by assignment to a State field, by a State key in
// a composite literal, or, for writeTerminal, by assigning its `to`
// parameter.
func terminalAssignment(fd *ast.FuncDecl) token.Pos {
	var at token.Pos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if at.IsValid() {
			return false
		}
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				sel, ok := l.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "State" || i >= len(x.Rhs) {
					continue
				}
				switch r := x.Rhs[i].(type) {
				case *ast.SelectorExpr:
					if isTerminalSessionState(r) {
						at = x.Pos()
					}
				case *ast.Ident:
					if fd.Name.Name == "writeTerminal" && r.Name == "to" {
						at = x.Pos()
					}
				}
			}
		case *ast.KeyValueExpr:
			if k, ok := x.Key.(*ast.Ident); ok && k.Name == "State" {
				if isTerminalSessionState(x.Value) {
					at = x.Pos()
				}
			}
		}
		return true
	})
	return at
}

// firstCall returns the position of the first call in fd whose function name
// satisfies match.
func firstCall(fd *ast.FuncDecl, match func(string) bool) token.Pos {
	var at token.Pos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || at.IsValid() {
			return !at.IsValid()
		}
		name := ""
		switch f := c.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if match(name) {
			at = c.Pos()
		}
		return true
	})
	return at
}

// TestTerminalWriterInventoryMatchesTheCode_spec_7_2 asserts that the
// functions under pkg/ and cmd/ that assign a terminal session state are
// exactly the terminal writer inventory, apart from the terminal value
// builders, which call no store writer, that every terminal assignment is
// followed by MarkTerminated in the same function (for transitionTerminate and
// handleDelete, in handleTerminalWrite's mutator), and that the claim
// function of every
// funnel and hook writer calls BeginTerminal before its Update.
//
// spec: §7.2 (Interactive Session Model), §8.8 (TaskRecord and TaskResult
// Schema)
func TestTerminalWriterInventoryMatchesTheCode_spec_7_2(t *testing.T) {
	t.Parallel()
	funcs, _ := scanRepo(t)
	builders := map[string]bool{}
	for _, b := range terminalValueBuilders {
		key := b.file + " " + b.writer
		builders[key] = true
		sf, ok := funcs[key]
		if !ok {
			t.Errorf("%s: terminal value builder not found", key)
			continue
		}
		if firstCall(sf.decl, storeWriteCall).IsValid() {
			t.Errorf("%s: a terminal value builder calls a store writer; it is a terminal writer and needs an inventory row", key)
		}
	}
	var found []string
	for key, sf := range funcs {
		if !builders[key] && terminalAssignment(sf.decl).IsValid() {
			found = append(found, key)
		}
	}
	var want []string
	for _, w := range terminalWriterInventory {
		want = append(want, w.file+" "+w.writer)
	}
	sort.Strings(found)
	sort.Strings(want)
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("terminal writers in the code:\n  %s\nwant the inventory:\n  %s\nA new terminal writer needs an inventory row.",
			strings.Join(found, "\n  "), strings.Join(want, "\n  "))
	}

	isMark := func(n string) bool { return n == "MarkTerminated" }
	isClaim := func(n string) bool { return n == "BeginTerminal" || n == "beginTerminal" }
	isWrite := func(n string) bool { return n == "Update" || n == "writeTerminal" }
	for _, w := range terminalWriterInventory {
		sf, ok := funcs[w.file+" "+w.writer]
		if !ok {
			t.Errorf("%s %s: not found", w.file, w.writer)
			continue
		}
		markIn := sf.decl
		if w.writer == "transitionTerminate" || w.writer == "handleDelete" {
			markIn = funcs[w.file+" handleTerminalWrite"].decl
		}
		mark := firstCall(markIn, isMark)
		if !mark.IsValid() || (markIn == sf.decl && mark < terminalAssignment(sf.decl)) {
			t.Errorf("%s %s: the terminal assignment is not followed by MarkTerminated", w.file, w.writer)
		}
		if w.claim == "" {
			continue
		}
		cf, ok := funcs[w.file+" "+w.claim]
		if !ok {
			t.Errorf("%s %s: claim function %s not found", w.file, w.writer, w.claim)
			continue
		}
		// A claim function that is not the writer reaches the Update through
		// a call to the writer, as TerminateSession calls terminate.
		reachesWrite := func(n string) bool { return isWrite(n) || (w.claim != w.writer && n == w.writer) }
		claim, write := firstCall(cf.decl, isClaim), firstCall(cf.decl, reachesWrite)
		if !claim.IsValid() || !write.IsValid() || claim > write {
			t.Errorf("%s %s: BeginTerminal does not precede the Update", w.file, w.claim)
		}
	}
	for _, name := range writeTerminalCallers {
		sf, ok := funcs["pkg/gateway/sessionserver/session_terminal.go "+name]
		if !ok {
			sf, ok = funcs["pkg/gateway/sessionserver/usage.go "+name]
		}
		if !ok {
			t.Errorf("writeTerminal caller %s not found", name)
			continue
		}
		claim, write := firstCall(sf.decl, isClaim), firstCall(sf.decl, func(n string) bool { return n == "writeTerminal" })
		if !claim.IsValid() || !write.IsValid() || claim > write {
			t.Errorf("%s: BeginTerminal does not precede writeTerminal", name)
		}
	}
}

// TestTerminalContextTableCoversTheInventory_spec_7_1 asserts that the
// terminal pipeline context table names exactly the writers of the terminal
// writer inventory, so a writer added to the inventory fails until the
// context test covers it.
//
// spec: §7.1 (Terminal pipeline context), §7.2 (Interactive Session Model)
func TestTerminalContextTableCoversTheInventory_spec_7_1(t *testing.T) {
	t.Parallel()
	var want []string
	for _, w := range terminalWriterInventory {
		want = append(want, w.file+" "+w.writer)
	}
	sort.Strings(want)
	got := sessionserver.TerminalContextWriterKeys()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("terminal context table:\n  %s\nwant the inventory:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestTerminalAssignmentIgnoresOtherPackagesStateNames_spec_7_2 asserts that
// the writer scan counts only session-package terminal states, so a State
// field set from another package's identically named state, such as the
// issued-token store's expired impersonation row, is not reported as a
// terminal session writer, while a session-package state still is.
//
// spec: §7.2 (Interactive Session Model)
func TestTerminalAssignmentIgnoresOtherPackagesStateNames_spec_7_2(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		want       bool
	}{
		{"session key", `_ = &T{State: session.StateExpired}`, true},
		{"session assign", `r.State = session.StateFailed`, true},
		{"issued-token key", `_ = &issuedtokenstore.NotActiveError{State: issuedtokenstore.StateExpired}`, false},
		{"issued-token assign", `e.State = issuedtokenstore.StateExpired`, false},
		{"non-terminal session state", `r.State = session.StateRunning`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "package p\nfunc f() {\n" + tc.body + "\n}\n"
			f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			fd := f.Decls[0].(*ast.FuncDecl)
			if got := terminalAssignment(fd).IsValid(); got != tc.want {
				t.Errorf("terminalAssignment(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
