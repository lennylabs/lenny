// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// spec: 4.1 (controller subsystem composition root), 4.6.1 (warm pool controller)
//
// diagnosis: a failure here means the lenny-controller composition root
// regained inline subsystem construction — it stopped being the ordered
// build-step call sequence proposal 0020 §4 Part A R8 specifies and §6 Part A
// accepts, and drifted back toward the ~494-line monolith the pre-fix code was.
// The controllers registered against the manager, or the conditional gates that
// guard the Postgres-only and leader-only runnables, may no longer match the
// pinned wiring outcome.
//
// TestRunControllerIsAnOrderedBuildStepSequence pins proposal 0020 R8: the
// composition root was a ~494-line, 186-statement func main that built the
// manager, every store, the ops emitter, and registered every controller and
// leader-elected runnable inline. After the decomposition those blocks live in
// named build steps (buildManagerSetup, buildStores, registerCoreControllers,
// registerOptionalControllers, registerLeaderRunnables, ...) recorded on the
// controllerWiring accumulator, so runController is a short ordered call
// sequence: its top-level statements are build-step method calls on the
// accumulator, defers, and a few small inline wirings.
//
// The test parses cmd/lenny-controller/wiring.go (where runController lives
// after the R8 decomposition) and asserts (1) runController's top-level
// statement count is far below the pre-fix monolith and (2) every
// constructor-shaped call (a New*/build*/register* call) in the body is a method
// call on the controllerWiring accumulator (w.buildX/w.registerX), never an
// inline package-level constructor such as ctrl.NewManager, events.NewEmitter,
// or pgxpool.New. Against the pre-fix monolith (the ~186-statement func main the
// proposal R8 targets) the statement-count bound fails immediately, and the
// inline construction the residual blocks performed trips the
// no-inline-constructor assertion.
func TestRunControllerIsAnOrderedBuildStepSequence(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "wiring.go", nil, 0)
	if err != nil {
		t.Fatalf("parse wiring.go: %v", err)
	}

	fn := findFuncDecl(file, "runController")
	if fn == nil || fn.Body == nil {
		t.Fatal("runController not found in main.go")
	}

	// (1) Statement-count bound. The composition root is an ordered call
	// sequence over the build steps plus a few defers and small inline wirings.
	// The pre-fix monolith carried 186 statements; a bound of 40 top-level
	// statements distinguishes the ordered sequence from any regression that
	// re-inlines a subsystem's construct-and-wire body.
	const maxTopLevelStmts = 40
	if n := len(fn.Body.List); n > maxTopLevelStmts {
		t.Errorf("runController has %d top-level statements, want <= %d: the composition root must stay an ordered build-step call sequence, not re-absorb a subsystem's inline construction (proposal 0020 §4 R8 / §6 Part A)", n, maxTopLevelStmts)
	}

	// (2) No inline subsystem construction. Every constructor-shaped call in the
	// body (a selector call whose function name starts with New, build, or
	// register) must be a method call on the accumulator receiver `w`. An inline
	// package-level constructor (ctrl.NewManager, pgxpool.New, events.NewEmitter,
	// controllermetrics.NewQueueFactory, ...) is exactly the residual
	// construction R8 requires to live in a build step.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if !isConstructorName(name) {
			return true
		}
		// Allowed: a method call on the accumulator receiver w (w.buildX,
		// w.registerX). Anything else is an inline package-level constructor.
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "w" {
			return true
		}
		pkg := "?"
		if ident, ok := sel.X.(*ast.Ident); ok {
			pkg = ident.Name
		}
		t.Errorf("runController calls inline constructor %s.%s: subsystem construction must live in a w.buildX/w.registerX build step, not inline in the composition root (proposal 0020 §4 R8)", pkg, name)
		return true
	})
}

// findFuncDecl returns the named top-level function declaration, or nil.
func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

// isConstructorName reports whether a call name is constructor-shaped: it builds,
// constructs, or registers a component. These are the calls that, when made
// inline rather than through a build step, signal un-extracted subsystem
// construction in the composition root.
func isConstructorName(name string) bool {
	for _, prefix := range []string{"New", "build", "register"} {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
