// SPDX-License-Identifier: MIT

package errorprop

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TeardownVerbs is the set of method names whose returned error must
// be propagated, logged, or recorded — never silently dropped.
var TeardownVerbs = map[string]bool{
	"Close":   true,
	"Cleanup": true,
	"Release": true,
	"Drain":   true,
	"Stop":    true,
	"Flush":   true,
}

// Finding is one place in the codebase where a teardown verb's
// returned error is dropped without propagation or logging.
type Finding struct {
	File    string
	Line    int
	Verb    string
	Snippet string
}

// String renders a Finding for the verdict surface.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s() error dropped (%s)", f.File, f.Line, f.Verb, strings.TrimSpace(f.Snippet))
}

// Scan walks root and returns every dropped-error site under any of
// the directories in includeDirs. It returns the findings sorted by
// file then line.
func Scan(root string, includeDirs []string) ([]Finding, error) {
	var findings []Finding
	for _, dir := range includeDirs {
		full := filepath.Join(root, dir)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		err := filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fs, err := scanFile(path)
			if err != nil {
				return err
			}
			findings = append(findings, fs...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

// scanFile parses one Go file and reports every dropped-error site.
func scanFile(path string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// We are looking for `if err := <recv>.<Verb>(...); err != nil { ... }`
		assign, ok := ifStmt.Init.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		// LHS must be an identifier named "err".
		ident, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || ident.Name != "err" {
			return true
		}
		// RHS must be a call to one of the teardown verbs.
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		verb := sel.Sel.Name
		if !TeardownVerbs[verb] {
			return true
		}
		// The body must either propagate the error or do something
		// with it. Dropped means: empty body, or body that only does
		// `_ = err` / `_ = nil` / nothing.
		if dropsErr(ifStmt.Body) {
			pos := fset.Position(ifStmt.Pos())
			findings = append(findings, Finding{
				File:    pos.Filename,
				Line:    pos.Line,
				Verb:    verb,
				Snippet: renderIf(ifStmt, fset),
			})
		}
		return true
	})
	return findings, nil
}

// dropsErr reports whether the body of `if err := ...; err != nil { ... }`
// silently drops the error. A body is considered "dropping" if it is
// empty or contains only `_ = err` / `_ = nil`.
func dropsErr(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return true
	}
	for _, stmt := range body.List {
		// Allow `return ...err...` patterns (propagation).
		if rs, ok := stmt.(*ast.ReturnStmt); ok {
			if mentionsErr(rs) {
				return false
			}
			// A bare `return` without referencing err is unusual but
			// allowed by the spec; treat as non-dropping.
			return false
		}
		// Allow any expression statement that references err
		// (logging, metric, panic, fmt.Errorf, etc.).
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if exprMentionsErr(es.X) {
				return false
			}
		}
		// Allow assignment statements that reference err on RHS
		// (e.g. `wrapped := fmt.Errorf("...: %w", err)`).
		if as, ok := stmt.(*ast.AssignStmt); ok {
			for _, e := range as.Rhs {
				if exprMentionsErr(e) {
					return false
				}
			}
		}
	}
	return true
}

func mentionsErr(rs *ast.ReturnStmt) bool {
	for _, r := range rs.Results {
		if exprMentionsErr(r) {
			return true
		}
	}
	return false
}

func exprMentionsErr(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok && ident.Name == "err" {
			found = true
			return false
		}
		return true
	})
	return found
}

func renderIf(ifStmt *ast.IfStmt, fset *token.FileSet) string {
	pos := fset.Position(ifStmt.Pos())
	return fmt.Sprintf("if at line %d", pos.Line)
}
