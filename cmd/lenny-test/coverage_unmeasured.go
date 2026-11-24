// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// This file holds the part of `lenny-test coverage --diff` that counts a
// changed file the coverage profile does not hold (change 0172, decision
// 8). The profile names the coverable lines of the files the unit run
// measured, so a changed file outside it would otherwise add no line to
// the check, and a diff whose changed code no unit test ran would pass
// with 0 of 0 lines. The harness is contributor tooling, so this file
// carries no specification annotation.

// unmeasuredStatementLines returns, for a changed production Go file the
// profile does not hold, the changed lines that lie inside a function
// body and hold code, which the check counts as coverable and uncovered.
// A test file, a generated file, a file of a tier's test directory (which
// the unit run never measures; its tier runs it), a file that no longer
// exists, and a file that does not parse contribute nothing.
func unmeasuredStatementLines(root, file string, ranges []lineRange) int {
	if strings.HasSuffix(file, "_test.go") || tierOfDir(path.Dir(file)) != "" {
		return 0
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil || isGeneratedGo(src) {
		return 0
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.ParseComments)
	if err != nil {
		return 0
	}
	body := bodyLines(fset, f)
	code := codeLines(src)
	n := 0
	for _, r := range ranges {
		for line := r.start; line <= r.end; line++ {
			if body[line] && code[line] {
				n++
			}
		}
	}
	return n
}

// bodyLines returns the lines strictly inside the braces of each function
// and function literal body of a file.
func bodyLines(fset *token.FileSet, f *ast.File) map[int]bool {
	out := map[int]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		var b *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			b = fn.Body
		case *ast.FuncLit:
			b = fn.Body
		}
		if b != nil {
			for line := fset.Position(b.Lbrace).Line + 1; line < fset.Position(b.Rbrace).Line; line++ {
				out[line] = true
			}
		}
		return true
	})
	return out
}

// codeLines returns the lines of src that hold something other than
// blanks and a line comment.
func codeLines(src []byte) map[int]bool {
	out := map[int]bool{}
	for i, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "//") {
			out[i+1] = true
		}
	}
	return out
}

// isGeneratedGo reports whether a Go file carries the standard
// `Code generated ... DO NOT EDIT.` header line.
func isGeneratedGo(src []byte) bool {
	for _, line := range bytes.Split(src, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("package ")) {
			return false
		}
		if bytes.HasPrefix(line, []byte("// Code generated ")) && bytes.HasSuffix(bytes.TrimSpace(line), []byte("DO NOT EDIT.")) {
			return true
		}
	}
	return false
}
