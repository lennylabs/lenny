// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// runLongFuncs reports functions whose body exceeds a line threshold, so an
// author can find candidates for the "keep functions small" guidance in
// .claude/rules/code-best-practices.md (extract a helper past ~50 lines).
//
// This check is advisory. It measures function length only and always exits 0
// on a successful scan, so it can run alongside the static tier without
// failing the build. Length is a candidate signal, not a verdict: a long
// function does not necessarily mix abstraction levels or need splitting, and
// a short one can. Confirm by reading the function. The statement count is a
// secondary signal that distinguishes control-flow-heavy bodies from large
// declarative literals (an alert table or an SLO catalog reads as one long
// return statement with very few statements).
//
// Test files (*_test.go) and generated files are excluded, matching the scope
// of the code-quality rules.
func runLongFuncs(args []string) int {
	fs := flag.NewFlagSet("long-funcs", flag.ExitOnError)
	threshold := fs.Int("threshold", 50, "report functions whose body exceeds this many lines")
	top := fs.Int("top", 40, "show at most this many findings (0 = all)")
	minStmts := fs.Int("min-stmts", 0, "only report functions with at least this many statements (filters large data literals)")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := repoRoot()
	// Scan the source trees where code-best-practices.md applies. A concern
	// that lives elsewhere is added here rather than scanned from the repo
	// root, which would pull in build artifacts and vendored trees.
	dirs := []string{"pkg", "cmd", "sdks", "migrations"}

	findings, err := scanLongFuncs(root, dirs, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test long-funcs: %v\n", err)
		return 2
	}
	if *minStmts > 0 {
		filtered := findings[:0]
		for _, f := range findings {
			if f.Stmts >= *minStmts {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].BodyLen != findings[j].BodyLen {
			return findings[i].BodyLen > findings[j].BodyLen
		}
		return findings[i].File < findings[j].File
	})

	if *jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"threshold": *threshold,
			"count":     len(findings),
			"findings":  findings,
		}, "", "  ")
		fmt.Println(string(out))
		return 0
	}

	printLongFuncs(findings, *threshold, *top)
	return 0
}

// longFunc is one reported function. BodyLen is the line span from the opening
// brace to the closing brace, inclusive; Stmts counts every statement in the
// body, including nested ones.
type longFunc struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Name    string `json:"name"`
	BodyLen int    `json:"body_len"`
	DeclLen int    `json:"decl_len"`
	Stmts   int    `json:"stmts"`
}

// scanLongFuncs parses every non-test, non-generated Go file under the given
// directories (relative to root) and returns the functions whose body exceeds
// threshold lines.
func scanLongFuncs(root string, dirs []string, threshold int) ([]longFunc, error) {
	fset := token.NewFileSet()
	var findings []longFunc

	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			// A missing source tree is not an error; skip it.
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch info.Name() {
				case "vendor", ".git", "node_modules", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if isGeneratedFileName(filepath.Base(path)) {
				return nil
			}
			fileFindings, perr := longFuncsInFile(fset, path, threshold)
			if perr != nil {
				// An unparseable file is reported on stderr and skipped so one
				// bad file does not abort the whole scan.
				fmt.Fprintf(os.Stderr, "lenny-test long-funcs: skip %s: %v\n", path, perr)
				return nil
			}
			// Report paths relative to the repo root so output is stable
			// across checkouts.
			for i := range fileFindings {
				if rel, rerr := filepath.Rel(root, fileFindings[i].File); rerr == nil {
					fileFindings[i].File = rel
				}
			}
			findings = append(findings, fileFindings...)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
	}
	return findings, nil
}

// longFuncsInFile parses one file and returns the functions over threshold.
func longFuncsInFile(fset *token.FileSet, path string, threshold int) ([]longFunc, error) {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if isGeneratedFile(f) {
		return nil, nil
	}
	var out []longFunc
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		open := fset.Position(fd.Body.Lbrace)
		closeBrace := fset.Position(fd.Body.Rbrace)
		bodyLen := closeBrace.Line - open.Line + 1
		if bodyLen <= threshold {
			continue
		}
		declStart := fset.Position(fd.Pos())
		declEnd := fset.Position(fd.End())
		out = append(out, longFunc{
			File:    path,
			Line:    declStart.Line,
			Name:    funcDeclName(fd),
			BodyLen: bodyLen,
			DeclLen: declEnd.Line - declStart.Line + 1,
			Stmts:   countBodyStmts(fd.Body),
		})
	}
	return out, nil
}

func printLongFuncs(findings []longFunc, threshold, top int) {
	var over200, over100 int
	for _, f := range findings {
		switch {
		case f.BodyLen > 200:
			over200++
		case f.BodyLen > 100:
			over100++
		}
	}
	rest := len(findings) - over200 - over100

	fmt.Printf("lenny-test long-funcs (advisory): functions whose body exceeds %d lines.\n", threshold)
	fmt.Println("Length is a candidate signal only. It does not prove a function mixes")
	fmt.Println("abstraction levels or needs splitting; confirm by reading it. STMTS")
	fmt.Println("separates control-flow-heavy bodies from large declarative literals.")
	fmt.Printf("\nFound %d functions over %d lines (%d >200, %d 101-200, %d %d-100).\n\n",
		len(findings), threshold, over200, over100, rest, threshold+1)

	if len(findings) == 0 {
		return
	}

	shown := findings
	if top > 0 && len(findings) > top {
		shown = findings[:top]
	}
	fmt.Printf("%-6s %-7s %-7s  %s\n", "BODY", "DECL", "STMTS", "FUNCTION  (file:line)")
	for _, f := range shown {
		fmt.Printf("%-6d %-7d %-7d  %s  (%s:%d)\n", f.BodyLen, f.DeclLen, f.Stmts, f.Name, f.File, f.Line)
	}
	if len(shown) < len(findings) {
		fmt.Printf("\n(showing top %d of %d; use --top 0 for all, --json for full data)\n", len(shown), len(findings))
	}
}

// funcDeclName renders a function's name, prefixing the receiver type for
// methods so two methods named the same on different types are distinct.
func funcDeclName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return "(" + receiverTypeName(fd.Recv.List[0].Type) + ")." + fd.Name.Name
	}
	return fd.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver, e.g. Store[T]
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return "?"
	}
}

// countBodyStmts counts every statement node in a function body, including
// nested ones, as a secondary signal of control-flow density.
func countBodyStmts(b *ast.BlockStmt) int {
	n := 0
	ast.Inspect(b, func(node ast.Node) bool {
		if _, ok := node.(ast.Stmt); ok {
			n++
		}
		return true
	})
	return n
}

// isGeneratedFile reports whether a parsed file carries the standard Go
// "Code generated ... DO NOT EDIT." marker in a comment before the package
// clause.
func isGeneratedFile(f *ast.File) bool {
	for _, cg := range f.Comments {
		if f.Package != token.NoPos && cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.Contains(c.Text, "Code generated") && strings.Contains(c.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

// isGeneratedFileName reports whether a base file name matches a known codegen
// output pattern (protobuf, connect, deepcopy, and other zz_generated files).
func isGeneratedFileName(base string) bool {
	switch {
	case strings.HasSuffix(base, ".pb.go"),
		strings.HasSuffix(base, ".connect.go"),
		strings.HasSuffix(base, ".gen.go"),
		strings.HasSuffix(base, "_generated.go"),
		strings.HasPrefix(base, "zz_generated"):
		return true
	}
	return false
}
