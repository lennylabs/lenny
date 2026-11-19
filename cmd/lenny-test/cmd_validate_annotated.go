// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// noRequirementPhrase is the phrase a test's doc comment carries when the
// test verifies no requirement, which the annotation rule asks such a
// test to say in a comment (.claude/rules/spec-annotations.md, "Tests").
// The check reads the doc comment with its lines joined, so the phrase
// may wrap.
const noRequirementPhrase = "verifies no requirement"

// specMarker opens an annotation line in a doc comment.
const specMarker = "spec:"

// validateTierTestsAnnotated checks that every test function in the tiers
// validate-diagnosis covers carries a `// spec:` annotation in its doc
// comment, or says in its doc comment that it verifies no requirement.
// It replaces the spec-map check that every test file of those tiers was
// named in tests/spec-map.json (change 0116, CS-9): the annotation is now
// the record of what a test verifies. Unlike validate-diagnosis, a skip
// helper in the body does not excuse a missing annotation, because the
// coverage view and selection read the annotation whatever the test does
// at run time.
//
// The doc comment is the one the Go parser attaches to the declaration,
// which is what the annotation index reads, so a comment separated from
// the function by a blank line does not count. Besides the tier
// directories, the check reads the component-tagged test files of the
// trees whose tests run in the component tier in-package
// (tierExtraTrees).
func validateTierTestsAnnotated(root string) checkResult {
	const name = "test annotations"
	var missing []string
	funcs := 0
	check := func(path string) error {
		rel, _ := filepath.Rel(root, path)
		n, miss, err := unannotatedTestsIn(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		funcs += n
		missing = append(missing, miss...)
		return nil
	}
	for _, dir := range componentAndAboveTierDirs() {
		if err := walkTestFiles(filepath.Join(root, dir), check); err != nil {
			return newResult(name, false, err.Error())
		}
	}
	for _, dir := range tierExtraTrees(tierComponent) {
		err := walkTestFiles(filepath.Join(root, dir), func(path string) error {
			tagged, err := carriesBuildTag(path, tierBuildTag(tierComponent))
			if err != nil || !tagged {
				return err
			}
			return check(path)
		})
		if err != nil {
			return newResult(name, false, err.Error())
		}
	}
	if len(missing) > 0 {
		return newResult(name, false, fmt.Sprintf(
			"%d test function(s) carry neither a // spec: annotation nor a doc comment saying the test %s: %s",
			len(missing), noRequirementPhrase, summarizeProblems(missing),
		))
	}
	return newResult(name, true, fmt.Sprintf(
		"%d test function(s) from the component tier on; each carries a // spec: annotation or says it %s",
		funcs, noRequirementPhrase,
	))
}

// walkTestFiles calls fn for every _test.go file under base, leaving out
// testdata/ directories. A base that does not exist holds no file.
func walkTestFiles(base string, fn func(path string) error) error {
	if _, err := os.Stat(base); err != nil {
		return nil
	}
	return filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return fn(path)
	})
}

// carriesBuildTag reports whether a file's //go:build line holds with the
// given tag set.
func carriesBuildTag(path, tag string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range splitLines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false, nil
		}
		if !constraint.IsGoBuild(trimmed) {
			continue
		}
		expr, err := constraint.Parse(trimmed)
		if err != nil {
			return false, fmt.Errorf("parse the build constraint of %s: %w", path, err)
		}
		return expr.Eval(func(t string) bool { return t == tag }), nil
	}
	return false, nil
}

// unannotatedTestsIn parses one test file and returns the number of its
// top-level test functions and the `path:line Name` of each one whose doc
// comment carries neither a `// spec:` annotation with an item nor the
// no-requirement phrase. A file that does not parse is an error, so the
// check fails rather than passing over it.
func unannotatedTestsIn(path, rel string) (int, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return 0, nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	funcs := 0
	var missing []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
			continue
		}
		funcs++
		if docAnnotates(fn.Doc) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s:%d %s", rel, fset.Position(fn.Pos()).Line, fn.Name.Name))
	}
	return funcs, missing, nil
}

// docAnnotates reports whether a doc comment carries a `// spec:` line
// with at least one item, on the line itself or on the comment line after
// it, or says that the test verifies no requirement.
func docAnnotates(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	var lines []string
	for _, c := range doc.List {
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(c.Text, "//")))
	}
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, specMarker)
		if !ok {
			continue
		}
		if strings.TrimSpace(rest) != "" {
			return true
		}
		if i+1 < len(lines) && lines[i+1] != "" && !strings.Contains(strings.SplitN(lines[i+1], " ", 2)[0], ":") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(strings.Join(lines, " ")), noRequirementPhrase)
}
