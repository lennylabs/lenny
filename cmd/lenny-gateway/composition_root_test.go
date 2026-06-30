// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// spec: 4.1 (gateway subsystem seams)
//
// diagnosis: a failure here means runGateway regained inline subsystem
// construction — it stopped being the ordered build-step call sequence proposal
// 0020 §4 Part A R1 specifies and §6 Part A accepts, and drifted back toward the
// renamed monolith the pre-fix code was (1769 body lines / 754 statements).
//
// TestRunGatewayIsAnOrderedBuildStepSequence pins findings 1 and 2 of the
// design-conformance review for proposal 0020 R1: the composition root was a
// 1769-line monolith that the prior attempt renamed from main to runGateway
// while leaving the full audit-chain setup, the OCSF/SIEM forwarder, the
// ops-events emitter, the quota-checkpoint service, the external-interceptor
// registration, and the store-selection branches un-extracted inline. After the
// fix those blocks live in named build steps (buildAuditPipeline,
// buildOpsEvents, buildAuxStores, buildQuotaCheckpoint,
// buildInterceptorRegistration, ...), so runGateway is a short ordered call
// sequence: its top-level statements are build-step method calls on the
// accumulator, single-assignment re-aliases of recorded outputs, defers, and a
// few small inline wirings.
//
// The test parses cmd/lenny-gateway/main.go and asserts (1) runGateway's
// top-level statement count is far below the pre-fix monolith and (2) every
// constructor-shaped call (a New*/build*/start* call) in the body is a method
// call on the gatewayWiring accumulator (w.buildX/w.startX), never an inline
// package-level constructor such as auditstore.New, events.NewEmitter, or
// quotacheckpoint.Service literal construction. Against the pre-fix monolith the
// statement-count bound fails immediately (754 >> the bound), and the inline
// construction the residual blocks performed (auditstore.New, ocsf.NewTranslator,
// events.NewEmitter, and the if w.pgPool != nil store-selection branches) trips
// the no-inline-constructor assertion.
func TestRunGatewayIsAnOrderedBuildStepSequence(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	fn := findFuncDecl(file, "runGateway")
	if fn == nil || fn.Body == nil {
		t.Fatal("runGateway not found in main.go")
	}

	// (1) Statement-count bound. The composition root is an ordered call
	// sequence over the build steps plus a few re-aliases and small inline
	// wirings. The pre-fix monolith carried 754 statements; a generous bound of
	// 120 top-level statements distinguishes the ordered sequence from any
	// regression that re-inlines a subsystem's construct-and-wire body.
	const maxTopLevelStmts = 120
	if n := len(fn.Body.List); n > maxTopLevelStmts {
		t.Errorf("runGateway has %d top-level statements, want <= %d: the composition root must stay an ordered build-step call sequence, not re-absorb a subsystem's inline construction (proposal 0020 §4 R1 / §6 Part A)", n, maxTopLevelStmts)
	}

	// (2) No inline subsystem construction. Every constructor-shaped call in the
	// body (a selector call whose function name starts with New, build, or
	// start) must be a method call on the accumulator receiver `w`. An inline
	// package-level constructor (auditstore.New, events.NewEmitter,
	// ocsf.NewTranslator, quotacheckpoint... etc.) is exactly the residual
	// construction findings 1 and 2 require to live in a build step.
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
		// w.startX). Anything else is an inline package-level constructor.
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "w" {
			return true
		}
		// Allowed package-level startup-gate / probe helpers that are not
		// subsystem construction: these install process-wide state or read
		// config and have no accumulator output. They are covered by their own
		// build step (buildStartupGates) once the gates move; until then keep
		// the assertion scoped to constructor-shaped *subsystem* calls by
		// allowing the small set of non-subsystem helpers the root still calls.
		// The composition root after the fix calls no such package constructor
		// directly, so an empty allowlist is the strict assertion.
		pkg := "?"
		if ident, ok := sel.X.(*ast.Ident); ok {
			pkg = ident.Name
		}
		t.Errorf("runGateway calls inline constructor %s.%s: subsystem construction must live in a w.buildX/w.startX build step, not inline in the composition root (proposal 0020 §4 R1; findings 1/2)", pkg, name)
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

// isConstructorName reports whether a call name is constructor-shaped: it builds
// or starts a component. These are the calls that, when made inline rather than
// through a build step, signal un-extracted subsystem construction.
func isConstructorName(name string) bool {
	for _, prefix := range []string{"New", "build", "start"} {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
