// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file holds tests/non-go-inputs.yaml (change 0116, CS-6): the inputs
// no Go package owns, such as the chart, the compose stack, and the runtime
// supervisor's C sources, each mapped to the test packages or tiers that
// read, execute, or deploy it. The import graph selects every other input,
// so the list stays short, and `validate-maps` fails a dead row so it stays
// current. The harness is contributor tooling, so this file carries no
// specification annotation.

// nonGoInputsKind and nonGoInputsVersion are the header the file carries.
const (
	nonGoInputsKind    = "non-go-inputs"
	nonGoInputsVersion = 1
)

// nonGoInputs is the parsed non-Go input list.
type nonGoInputs struct {
	Kind    string       `yaml:"kind"`
	Version int          `yaml:"version"`
	Inputs  []nonGoInput `yaml:"inputs"`
}

// nonGoInput is one row: path patterns, the targets that read the paths,
// and why.
type nonGoInput struct {
	// Paths are repo-relative patterns. `*` matches within one path
	// segment, `**` matches across segments, and `?` matches one
	// character of a segment.
	Paths []string `yaml:"paths"`
	// Runs are tier names, which select the whole tier, and test package
	// directories, which select that package in the tier that runs it.
	Runs []string `yaml:"runs"`
	// Reason says how the targets read the paths.
	Reason string `yaml:"reason"`
}

// parseNonGoInputs parses the list and refuses an unknown field, so a
// misspelled key fails rather than reading as an empty row.
func parseNonGoInputs(data []byte) (*nonGoInputs, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var n nonGoInputs
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("parse %s: %w", nonGoInputsFile, err)
	}
	if n.Kind != nonGoInputsKind || n.Version != nonGoInputsVersion {
		return nil, fmt.Errorf("%s: want kind %q version %d, got kind %q version %d",
			nonGoInputsFile, nonGoInputsKind, nonGoInputsVersion, n.Kind, n.Version)
	}
	return &n, nil
}

// readNonGoInputs reads the list from the tree at root.
func readNonGoInputs(root string) (*nonGoInputs, error) {
	data, err := os.ReadFile(filepath.Join(root, nonGoInputsFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", nonGoInputsFile, err)
	}
	return parseNonGoInputs(data)
}

// targetsFor returns the targets of every row with a pattern that matches
// a changed path, in row order and without duplicates. A nil list matches
// nothing.
func (n *nonGoInputs) targetsFor(p string) []string {
	if n == nil {
		return nil
	}
	var out []string
	for _, in := range n.Inputs {
		if !in.matches(p) {
			continue
		}
		for _, t := range in.Runs {
			if !contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// matches reports whether any pattern of the row matches the path.
func (in nonGoInput) matches(p string) bool {
	for _, pattern := range in.Paths {
		if matchInputPattern(pattern, p) {
			return true
		}
	}
	return false
}

// matchInputPattern reports whether a repo-relative path matches a pattern
// of the list.
func matchInputPattern(pattern, p string) bool {
	re, err := inputPatternRegexp(pattern)
	return err == nil && re.MatchString(p)
}

// inputPatternRegexp compiles a pattern: `**` matches any run of
// characters, `*` a run within one segment, and `?` one character within a
// segment. A trailing `/**` also matches the directory's every file.
func inputPatternRegexp(pattern string) (*regexp.Regexp, error) {
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, "./") {
		return nil, fmt.Errorf("pattern %q is not a repo-relative path", pattern)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		case pattern[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// validateNonGoInputsFile is the `validate-maps` check of the list: it
// parses, every row has a pattern, a target, and a reason, every pattern
// matches at least one tracked file, every package target is a directory
// that holds Go files, and every other target is a tier name. A dead row
// fails, so the list holds only rows that select something.
func validateNonGoInputsFile(root string) checkResult {
	const name = "non-go inputs"
	n, err := readNonGoInputs(root)
	if err != nil {
		return newResult(name, false, err.Error())
	}
	tracked, err := trackedFiles(context.Background(), root)
	if err != nil {
		return newResult(name, false, err.Error())
	}
	problems := nonGoInputProblems(n, tracked, func(dir string) bool { return holdsGoFiles(filepath.Join(root, dir)) })
	if len(problems) > 0 {
		return newResult(name, false, strings.Join(problems, "; "))
	}
	return newResult(name, true, fmt.Sprintf("%d row(s), every pattern matches a tracked file and every target exists", len(n.Inputs)))
}

// nonGoInputProblems returns what is wrong with each row of the list
// against the tracked files and a test of whether a directory holds a Go
// package.
func nonGoInputProblems(n *nonGoInputs, tracked []string, isPackage func(dir string) bool) []string {
	var problems []string
	for i, in := range n.Inputs {
		row := fmt.Sprintf("row %d", i+1)
		if len(in.Paths) == 0 || len(in.Runs) == 0 || strings.TrimSpace(in.Reason) == "" {
			problems = append(problems, row+" needs paths, runs, and a reason")
		}
		for _, pattern := range in.Paths {
			re, err := inputPatternRegexp(pattern)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", row, err))
				continue
			}
			if !anyMatch(re, tracked) {
				problems = append(problems, fmt.Sprintf("%s: pattern %q matches no tracked file", row, pattern))
			}
		}
		for _, target := range in.Runs {
			if contains(allTiers(), target) {
				continue
			}
			if !strings.Contains(target, "/") {
				problems = append(problems, fmt.Sprintf("%s: target %q is not a tier name", row, target))
				continue
			}
			if !isPackage(target) {
				problems = append(problems, fmt.Sprintf("%s: target %q is not a Go package directory", row, target))
			}
		}
	}
	return problems
}

// anyMatch reports whether a pattern matches any of the paths.
func anyMatch(re *regexp.Regexp, paths []string) bool {
	for _, p := range paths {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// holdsGoFiles reports whether a directory holds a .go file.
func holdsGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// trackedFiles lists the files git tracks in the tree at root, sorted.
func trackedFiles(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git ls-files: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}
