// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// finalizeExitOutcome is the outcome a case forces on the finalize failure
// write.
type finalizeExitOutcome int

const (
	// exitApplied lets the failure write commit.
	exitApplied finalizeExitOutcome = iota
	// exitLostTerminal commits `cancelled` just before the failure write.
	exitLostTerminal
	// exitLostNonTerminal commits `ready` just before the failure write, as an
	// ambiguous ready write that committed would.
	exitLostNonTerminal
	// exitStoreError fails the failure write without committing.
	exitStoreError
	// exitCommitThenError commits the failure write and still reports an error.
	exitCommitThenError
)

func (o finalizeExitOutcome) String() string {
	return [...]string{"applied", "lost to a terminal state", "lost to a non-terminal state", "store error", "commit then store error"}[o]
}

// finalizeExitPool is one pool column: the row's frozen scrubPolicy and the
// slot members reserved before the exit, with the end state a release of the
// session's occupancy leaves and the end state no release leaves.
type finalizeExitPool struct {
	name    string
	scrub   string
	reserve []string
	// released is the end state after the session's occupancy ends.
	releasedClaim   bool
	releasedMembers string
	// kept is the end state when nothing releases the occupancy.
	keptMembers string
}

func finalizeExitPools() []finalizeExitPool {
	return []finalizeExitPool{
		{name: "exclusive", scrub: "", releasedClaim: false, releasedMembers: "[]", keptMembers: "[]"},
		{
			name: "concurrent, alone", scrub: ScrubPolicyPerSlot, reserve: []string{"sess-1"},
			releasedClaim: false, releasedMembers: "[]", keptMembers: "[sess-1]",
		},
		{
			name: "concurrent, live sibling", scrub: ScrubPolicyPerSlot, reserve: []string{"sess-1", "sib"},
			releasedClaim: true, releasedMembers: "[sib]", keptMembers: "[sess-1 sib]",
		},
	}
}

// finalizeExitFixture is a preRunningFixture whose session store forces the
// outcome of the finalize failure write, with the session's `finalizing` row
// seeded on pod-a.
type finalizeExitFixture struct {
	*preRunningFixture
	hooks *exitHookStore
}

func newFinalizeExitFixture(t *testing.T, pool finalizeExitPool, outcome finalizeExitOutcome) *finalizeExitFixture {
	t.Helper()
	p := newPreRunningFixture(t)
	p.reserve(t, pool.reserve...)
	if err := p.store.Create(context.Background(), sessionstore.Session{
		ID: "sess-1", TenantID: "acme", RuntimeRef: "echo", State: session.StateFinalizing,
		PodAssignment: reclaimPod, ScrubPolicy: pool.scrub,
	}); err != nil {
		t.Fatalf("seed finalizing row: %v", err)
	}
	hooks := &exitHookStore{Store: p.store}
	p.srv.store = hooks
	f := &finalizeExitFixture{preRunningFixture: p, hooks: hooks}
	switch outcome {
	case exitLostTerminal:
		hooks.hooks = []*exitHook{{state: session.StateFailed, mode: hookBeforeDelegate, before: f.commit(t, session.StateCancelled)}}
	case exitLostNonTerminal:
		hooks.hooks = []*exitHook{{state: session.StateFailed, mode: hookBeforeDelegate, before: f.commit(t, session.StateReady)}}
	case exitStoreError:
		hooks.hooks = []*exitHook{{state: session.StateFailed, mode: hookFailNoCommit}}
	case exitCommitThenError:
		hooks.hooks = []*exitHook{{state: session.StateFailed, mode: hookFailAfterCommit}}
	}
	return f
}

// commit returns a callback that writes st through the unwrapped store, as
// another writer would.
func (f *finalizeExitFixture) commit(t *testing.T, st session.State) func() {
	return func() {
		if _, err := f.store.Update(context.Background(), "acme", "sess-1", func(r *sessionstore.Session) error {
			r.State = st
			return nil
		}); err != nil {
			t.Errorf("commit %s: %v", st, err)
		}
	}
}

func (f *finalizeExitFixture) state(t *testing.T) session.State {
	t.Helper()
	row, err := f.store.Get(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	return row.State
}

// watchdogPass runs one finalizing-watchdog pass over the row: a guarded
// `finalizing` → `failed` write and, when it applies, the post-terminal hook
// with fromState finalizing.
func (f *finalizeExitFixture) watchdogPass(t *testing.T) {
	t.Helper()
	from, updated, err := f.srv.writeTerminal(context.Background(), "acme", "sess-1", session.StateFailed, finalizingPrecondition)
	if err != nil {
		t.Fatalf("watchdog terminal write: %v", err)
	}
	f.srv.AfterTerminalWrite(context.Background(), from, updated, nil)
}

// assertOccupancy checks the claim, the slot set, and the lease end state.
func (f *finalizeExitFixture) assertOccupancy(t *testing.T, pool finalizeExitPool, released bool, leaseReleases int) {
	t.Helper()
	wantClaim, wantMembers := true, pool.keptMembers
	if released {
		wantClaim, wantMembers = pool.releasedClaim, pool.releasedMembers
	}
	if got := f.claimPresent(t); got != wantClaim {
		t.Errorf("claim present = %v, want %v", got, wantClaim)
	}
	if got := f.members(t); got != wantMembers {
		t.Errorf("slot members = %s, want %s", got, wantMembers)
	}
	if n := f.writes.count("drain-request"); n != 0 {
		t.Errorf("drain requests = %d, want 0", n)
	}
	if n := f.writes.count("patch-claim-status"); n != 0 {
		t.Errorf("claim status patches = %d, want 0", n)
	}
	if n := len(f.creds.released); n != leaseReleases {
		t.Errorf("ReleaseSession calls = %v, want %d", f.creds.released, leaseReleases)
	}
	for _, id := range f.creds.released {
		if id != "sess-1" {
			t.Errorf("ReleaseSession(%q), want only sess-1", id)
		}
	}
}

func finalizeExitRequest() (*httptest.ResponseRecorder, *http.Request) {
	return httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/finalize", nil)
}

// finalizeExitCell is one row-by-column cell of the finalize exit table.
type finalizeExitCell struct {
	outcome finalizeExitOutcome
	// released reports that the handler's call ends the session's pod or slot
	// occupancy.
	released      bool
	leaseReleases int
	wantState     session.State
	wantCode      int
}

// TestFinalizeExitsReleaseThroughFailureWriteHook_spec_15_1 drives each
// finalize failure exit (plan parse, prepare failure, ready-write failure)
// against each outcome of its failure write, on each pool kind, and asserts
// the end state of the claim, the slot set, and the lease. An applied failure
// write releases the pod or the slot through its post-terminal hook by the
// row's pool kind, so a concurrent-workspace pool with a live sibling keeps
// its shared claim on every exit. A lost write releases nothing but the
// lease, which the handler revokes when a terminal writer overtook the call.
// The plan-parse store error reclaims through the slot-aware call; the
// ready-write store error releases nothing, because the ready write may have
// committed, and one finalizing-watchdog pass then releases the occupancy
// exactly once. A prepare-failure write that commits and reports a store
// error runs no hook, and the handler's revoke still returns the lease.
//
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal
// states), §6.1 (Per-session credential lease lifecycle), §4.6.3
// (SandboxClaim: a release that ends the occupancy without reuse), §5.2
// (Slot assignment atomicity)
func TestFinalizeExitsReleaseThroughFailureWriteHook_spec_15_1(t *testing.T) {
	rows := []struct {
		name  string
		cells []finalizeExitCell
		run   func(t *testing.T, f *finalizeExitFixture, w http.ResponseWriter, r *http.Request)
	}{
		{
			name: "plan parse",
			cells: []finalizeExitCell{
				{exitApplied, true, 1, session.StateFailed, http.StatusInternalServerError},
				{exitLostTerminal, false, 1, session.StateCancelled, http.StatusConflict},
				{exitLostNonTerminal, false, 1, session.StateReady, http.StatusConflict},
				{exitStoreError, true, 1, session.StateFinalizing, http.StatusInternalServerError},
			},
			run: func(t *testing.T, f *finalizeExitFixture, w http.ResponseWriter, r *http.Request) {
				row, err := f.store.Get(context.Background(), "acme", "sess-1")
				if err != nil {
					t.Fatalf("get row: %v", err)
				}
				f.srv.finalizePlanParseFailed(w, r, "acme", "sess-1", row.PodAssignment, row.ScrubPolicy, errInjectedStore)
			},
		},
		{
			// The pre-Prepare pool-resolution failure: the fixture's cluster
			// holds no warm pool, so ResolvePool fails on both pool kinds. It is
			// the only pre-Prepare exit a concurrent-workspace pool reaches.
			name: "prepare failure (pool resolution)",
			cells: []finalizeExitCell{
				{exitApplied, true, 2, session.StateFailed, http.StatusServiceUnavailable},
				{exitLostTerminal, false, 1, session.StateCancelled, http.StatusConflict},
				{exitLostNonTerminal, false, 1, session.StateReady, http.StatusConflict},
				{exitStoreError, false, 1, session.StateFinalizing, http.StatusServiceUnavailable},
				{exitCommitThenError, false, 1, session.StateFailed, http.StatusServiceUnavailable},
			},
			run: func(t *testing.T, f *finalizeExitFixture, w http.ResponseWriter, r *http.Request) {
				row, err := f.store.Get(context.Background(), "acme", "sess-1")
				if err != nil {
					t.Fatalf("get row: %v", err)
				}
				_, perr := f.srv.prepareAtFinalize(context.Background(), row, workspaceplan.Plan{})
				if perr == nil {
					t.Fatal("prepareAtFinalize succeeded, want the pool-resolution failure")
				}
				if !f.claimPresent(t) || len(f.creds.released) != 0 {
					t.Errorf("prepareAtFinalize released the occupancy itself (claim present %v, leases %v), want nothing released before the failure write",
						f.claimPresent(t), f.creds.released)
				}
				f.srv.finalizePrepareFailed(w, r, "acme", "sess-1", perr)
			},
		},
		{
			name: "ready-write failure",
			cells: []finalizeExitCell{
				{exitApplied, true, 1, session.StateFailed, http.StatusInternalServerError},
				{exitLostTerminal, false, 1, session.StateCancelled, http.StatusConflict},
				{exitLostNonTerminal, false, 0, session.StateReady, http.StatusInternalServerError},
				{exitStoreError, false, 0, session.StateFinalizing, http.StatusInternalServerError},
			},
			run: func(_ *testing.T, f *finalizeExitFixture, w http.ResponseWriter, r *http.Request) {
				f.srv.finalizeReadyWriteFailed(w, r, "acme", "sess-1", errInjectedStore)
			},
		},
	}
	for _, row := range rows {
		for _, cell := range row.cells {
			for _, pool := range finalizeExitPools() {
				t.Run(row.name+"/"+cell.outcome.String()+"/"+pool.name, func(t *testing.T) {
					f := newFinalizeExitFixture(t, pool, cell.outcome)
					w, r := finalizeExitRequest()

					row.run(t, f, w, r)

					if w.Code != cell.wantCode {
						t.Errorf("status = %d, want %d; body %s", w.Code, cell.wantCode, w.Body.String())
					}
					if got := f.state(t); got != cell.wantState {
						t.Errorf("state = %q, want %q", got, cell.wantState)
					}
					f.assertOccupancy(t, pool, cell.released, cell.leaseReleases)
					if n := f.log.count("release:"); n != 0 {
						t.Errorf("executor releases = %d, want 0 (the pre-running reclaim owns the release)", n)
					}
				})
			}
		}
	}
}

// TestFinalizeReadyWriteStoreErrorReleasesOnceAfterWatchdog_spec_6_2 pins the
// ready-write exit whose failure write also reports a store error: the
// handler releases nothing, because the ready write may have committed, and
// one finalizing-watchdog pass over the still-`finalizing` row then ends the
// occupancy exactly once, by the row's pool kind.
//
// spec: §6.2 (finalize timeout), §15.1 (finalize row), §7.2 (terminal
// states), §4.6.3 (SandboxClaim: a release that ends the occupancy without
// reuse)
func TestFinalizeReadyWriteStoreErrorReleasesOnceAfterWatchdog_spec_6_2(t *testing.T) {
	for _, pool := range finalizeExitPools() {
		t.Run(pool.name, func(t *testing.T) {
			f := newFinalizeExitFixture(t, pool, exitStoreError)
			w, r := finalizeExitRequest()

			f.srv.finalizeReadyWriteFailed(w, r, "acme", "sess-1", errInjectedStore)
			f.assertOccupancy(t, pool, false, 0)

			f.watchdogPass(t)
			f.assertOccupancy(t, pool, true, 1)
			wantDeletes := 0
			if !pool.releasedClaim {
				wantDeletes = 1
			}
			if n := f.writes.count("delete-claim"); n != wantDeletes {
				t.Errorf("claim deletes = %d, want %d", n, wantDeletes)
			}
		})
	}
}

// TestFinalizePlanParseStoreErrorReclaimsByPoolKind_spec_15_1 pins the one
// handler-side reclaim left on a finalize exit: the plan-parse failure whose
// failure write reports a store error. On a concurrent-workspace pool it
// removes only the session's slot member, so a live sibling keeps the shared
// claim and no claim is deleted; on an exclusive pool it deletes the claim by
// name. A later finalizing-watchdog pass removes nothing more.
//
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §4.6.3 (SandboxClaim:
// a release that ends the occupancy without reuse), §5.2 (Slot assignment
// atomicity)
func TestFinalizePlanParseStoreErrorReclaimsByPoolKind_spec_15_1(t *testing.T) {
	for _, tc := range []struct {
		pool        finalizeExitPool
		wantDeletes int
	}{
		{pool: finalizeExitPools()[0], wantDeletes: 1},
		{pool: finalizeExitPools()[2], wantDeletes: 0},
	} {
		t.Run(tc.pool.name, func(t *testing.T) {
			f := newFinalizeExitFixture(t, tc.pool, exitStoreError)
			w, r := finalizeExitRequest()

			f.srv.finalizePlanParseFailed(w, r, "acme", "sess-1", reclaimPod, tc.pool.scrub, errInjectedStore)

			f.assertOccupancy(t, tc.pool, true, 1)
			if n := f.writes.count("delete-claim"); n != tc.wantDeletes {
				t.Errorf("claim deletes = %d, want %d", n, tc.wantDeletes)
			}
			if tc.pool.scrub == ScrubPolicyPerSlot {
				f.watchdogPass(t)
				if got := f.members(t); got != tc.pool.releasedMembers {
					t.Errorf("slot members after the watchdog pass = %s, want %s (a keyed no-op)", got, tc.pool.releasedMembers)
				}
				if !f.claimPresent(t) {
					t.Errorf("the watchdog pass deleted the shared claim under a live sibling")
				}
			}
		})
	}
}

// TestFinalizeLeaseRevokeOnlyBeforeReadyOrOnTerminal_spec_6_1 is a structural
// invariant over the sessionserver package: transitionReady, the only
// finalizing → ready write, has a single caller (handleFinalize), and every
// revokeFinalizeLease call site is reached only before that write or on a
// terminal row. The plan-parse and prepare-failure exits are called from
// handleFinalize before the ready write, and each revoke in the ready-write
// exit sits under the ready write's own refusal or a terminal-state check.
// A revoke that ran after a committed ready write would strip the lease of a
// live `ready` session.
//
// spec: §6.1 (Per-session credential lease lifecycle), §15.1 (finalize row),
// §7.2 (terminal states)
func TestFinalizeLeaseRevokeOnlyBeforeReadyOrOnTerminal_spec_6_1(t *testing.T) {
	fset := token.NewFileSet()
	files := parseSessionServerFiles(t, fset)

	readyCallers := callersOf(files, "transitionReady")
	if got := strings.Join(readyCallers, ","); got != "handleFinalize" {
		t.Fatalf("transitionReady callers = [%s], want [handleFinalize]", got)
	}

	revokeCallers := callersOf(files, "revokeFinalizeLease")
	sort.Strings(revokeCallers)
	want := "finalizePlanParseFailed,finalizePrepareFailed,finalizeReadyWriteFailed,finalizeReadyWriteFailed"
	if got := strings.Join(revokeCallers, ","); got != want {
		t.Fatalf("revokeFinalizeLease call sites by function = [%s], want [%s]", got, want)
	}

	handle := findFunc(files, "handleFinalize")
	readyPos := firstCallPos(handle, "transitionReady")
	for _, exit := range []string{"finalizePlanParseFailed", "finalizePrepareFailed"} {
		pos := firstCallPos(handle, exit)
		if pos == token.NoPos || pos >= readyPos {
			t.Errorf("handleFinalize calls %s at %v, want it before the ready write at %v",
				exit, fset.Position(pos), fset.Position(readyPos))
		}
	}

	readyExit := findFunc(files, "finalizeReadyWriteFailed")
	for _, pos := range unguardedRevokes(readyExit, fset) {
		t.Errorf("revokeFinalizeLease at %v in finalizeReadyWriteFailed is not under isPreconditionError(err) or session.IsTerminal", pos)
	}
}

func parseSessionServerFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	return files
}

// calleeName returns the bare name of a call's function: f(...) or x.f(...).
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// callersOf returns, once per call site, the name of the enclosing function
// of every call to name.
func callersOf(files []*ast.File, name string) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && calleeName(call) == name {
					out = append(out, fd.Name.Name)
				}
				return true
			})
		}
	}
	return out
}

func findFunc(files []*ast.File, name string) *ast.FuncDecl {
	for _, f := range files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name {
				return fd
			}
		}
	}
	return nil
}

func firstCallPos(fd *ast.FuncDecl, name string) token.Pos {
	pos := token.NoPos
	if fd == nil {
		return pos
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && calleeName(call) == name && pos == token.NoPos {
			pos = call.Pos()
		}
		return true
	})
	return pos
}

// unguardedRevokes returns the revokeFinalizeLease calls in fd that no
// enclosing if statement guards with the ready write's refusal or a
// terminal-state check.
func unguardedRevokes(fd *ast.FuncDecl, fset *token.FileSet) []token.Position {
	if fd == nil {
		return []token.Position{{Filename: "finalizeReadyWriteFailed not found"}}
	}
	var out []token.Position
	var stack []ast.Node
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok || calleeName(call) != "revokeFinalizeLease" {
			return true
		}
		if !guardedByTerminalCheck(stack) {
			out = append(out, fset.Position(call.Pos()))
		}
		return true
	})
	return out
}

func guardedByTerminalCheck(stack []ast.Node) bool {
	for _, n := range stack {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			continue
		}
		guarded := false
		ast.Inspect(ifs.Cond, func(c ast.Node) bool {
			call, ok := c.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch calleeName(call) {
			case "IsTerminal":
				guarded = true
			case "isPreconditionError":
				if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "err" {
					guarded = true
				}
			}
			return true
		})
		if guarded {
			return true
		}
	}
	return false
}
