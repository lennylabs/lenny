// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// This file holds tests/non-go-inputs.yaml (change 0116, CS-6): the inputs
// no Go package owns, such as the chart, the compose stack, and the runtime
// supervisor's C sources, each mapped to the test packages or tiers that
// read, execute, or deploy it. The import graph selects every other input,
// so the list stays short, and `validate` fails a dead row so it stays
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
	// base is the list at the diff's base commit that union merged in, or
	// nil. A generated row narrows a path's selection only when the base
	// list also carries a generated row for the path (generated).
	base *nonGoInputs
}

// nonGoInput is one row: path patterns, the targets that read the paths,
// and why.
type nonGoInput struct {
	// Paths are repo-relative patterns. `*` matches within one path
	// segment, `**` matches across segments, and `?` matches one
	// character of a segment.
	Paths []string `yaml:"paths"`
	// Runs are tier names, which select the whole tier, and package
	// directories, which the selection treats as changed: the package
	// runs, and so does every package that depends on it. A helper package
	// that reads the input on behalf of its importers is listed here.
	Runs []string `yaml:"runs"`
	// Tests are package directories whose own tests read the input (change
	// 0172, GS-1). The selection runs each package's tests in the tier that
	// runs them and walks none of its dependents, because a dependent
	// reaches the input only through the package's tests, which nothing
	// imports.
	Tests []string `yaml:"tests"`
	// Generated marks a row whose paths are generated files: each is
	// regenerated from a source by a generator, or written back by a run
	// of a check, and a check of the row's targets regenerates or reads it
	// (change 0172, decision 26). A changed path that a generated row
	// matches selects the targets of the generated rows that match it and
	// nothing else: no other row that matches the path, and no package
	// that owns or embeds it. The change to the source selects its own
	// dependents, and a hand edit fails the generator's check. Its paths
	// are literal files, and its `runs` holds tier names only
	// (generatedRowProblems).
	Generated bool `yaml:"generated"`
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

// union returns a list holding the rows of both lists, either of which
// may be nil.
func (n *nonGoInputs) union(other *nonGoInputs) *nonGoInputs {
	if n == nil {
		return other
	}
	if other == nil {
		return n
	}
	out := *n
	out.Inputs = append(append([]nonGoInput{}, n.Inputs...), other.Inputs...)
	out.base = other
	return &out
}

// targetsFor returns the `runs` targets of every row with a pattern that
// matches a changed path, in row order and without duplicates. A nil list
// matches nothing.
func (n *nonGoInputs) targetsFor(p string) []string {
	return n.collect(p, func(in nonGoInput) []string { return in.Runs })
}

// testsFor returns the `tests` package directories of every row with a
// pattern that matches a changed path, in row order and without
// duplicates. A nil list matches nothing.
func (n *nonGoInputs) testsFor(p string) []string {
	return n.collect(p, func(in nonGoInput) []string { return in.Tests })
}

// generated reports whether a generated row matches the changed path, so
// the selection reads only the generated rows for it (see nonGoInput's
// Generated field). A list that union merged with the base's list narrows
// the path only when the base's list also carries a generated row for it:
// the union keeps a branch that narrows a row running what the row named
// before, and a generated row that the branch adds narrows the selection
// of the branches after it merges, not its own. A nil list matches
// nothing.
func (n *nonGoInputs) generated(p string) bool {
	if n == nil || !n.matchesGenerated(p) {
		return false
	}
	return n.base == nil || n.base.matchesGenerated(p)
}

// matchesGenerated reports whether a generated row of the list matches the
// path.
func (n *nonGoInputs) matchesGenerated(p string) bool {
	for _, in := range n.Inputs {
		if in.Generated && in.matches(p) {
			return true
		}
	}
	return false
}

// collect returns the values field gives for every row with a pattern that
// matches the path, in row order and without duplicates. When the path is
// generated (generated), only the generated rows count.
func (n *nonGoInputs) collect(p string, field func(nonGoInput) []string) []string {
	if n == nil {
		return nil
	}
	onlyGenerated := n.generated(p)
	var out []string
	for _, in := range n.Inputs {
		if !in.matches(p) || (onlyGenerated && !in.Generated) {
			continue
		}
		for _, t := range field(in) {
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

// validateNonGoInputsFile is the `validate` check of the list: it
// parses, every row has a pattern, a target, and a reason, every pattern
// matches at least one tracked file, every package target is a directory
// that holds Go files, every other `runs` target is a tier name, and every
// `tests` target is a clean repo-relative package directory that holds a
// test file and is listed once in its row. A dead row fails, so the list
// holds only rows that select something.
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
	dirs := pkgDirs{
		isPackage: func(dir string) bool { return holdsFile(filepath.Join(root, dir), ".go") },
		hasTests:  func(dir string) bool { return holdsFile(filepath.Join(root, dir), "_test.go") },
		generated: func(p string) (bool, error) { return isGeneratedInput(root, p) },
	}
	problems := nonGoInputProblems(n, tracked, dirs)
	if len(problems) > 0 {
		return newResult(name, false, strings.Join(problems, "; "))
	}
	return newResult(name, true, fmt.Sprintf("%d row(s), every pattern matches a tracked file and every target exists", len(n.Inputs)))
}

// pkgDirs answers what validate asks of a target directory.
type pkgDirs struct {
	// isPackage reports whether the directory holds a Go file.
	isPackage func(dir string) bool
	// hasTests reports whether the directory holds a Go test file.
	hasTests func(dir string) bool
	// generated reports whether a file is one a generated row may name
	// (isGeneratedInput); nil answers no for every file.
	generated func(path string) (bool, error)
}

// writeBackRegisters are the registers under tests/registers/ whose counts
// a run of a static-tier check writes back when a count falls, each with
// the check that reads and rewrites it. A generated row may name them
// beside the files a generator writes (change 0172, decision 26).
var writeBackRegisters = map[string]string{
	annotation.RatchetBaselinePath:        "the section-citation ratchet of the static tier",
	"tests/registers/line-citations.yaml": "the line-citation ratchet of the static tier",
	golangciLintRegister:                  "the golangci-lint check of the static tier",
}

// isGeneratedInput reports whether the file at p, relative to root, is one
// a generated row may name: a register in writeBackRegisters, or a file the
// generated-artifact rule of the specification tooling classifies as
// generated, by a generation marker in its header or its document metadata
// or as the output of a named producer (scope.Generated).
func isGeneratedInput(root, p string) (bool, error) {
	if _, ok := writeBackRegisters[p]; ok {
		return true, nil
	}
	d, err := scope.Generated(p, func(target string) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	})
	if err != nil {
		return false, err
	}
	return d != scope.NotGenerated, nil
}

// nonGoInputProblems returns what is wrong with each row of the list
// against the tracked files and the target directories.
func nonGoInputProblems(n *nonGoInputs, tracked []string, dirs pkgDirs) []string {
	var problems []string
	for i, in := range n.Inputs {
		row := fmt.Sprintf("row %d", i+1)
		if len(in.Paths) == 0 || len(in.Runs)+len(in.Tests) == 0 || strings.TrimSpace(in.Reason) == "" {
			problems = append(problems, row+" needs paths, a target under runs or tests, and a reason")
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
			if !dirs.isPackage(target) {
				problems = append(problems, fmt.Sprintf("%s: target %q is not a Go package directory", row, target))
			}
		}
		problems = append(problems, testsTargetProblems(row, in, dirs)...)
		if in.Generated {
			problems = append(problems, generatedRowProblems(row, in, tracked, dirs)...)
		}
	}
	return problems
}

// generatedRowProblems returns what is wrong with a generated row. Its
// narrow selection holds only for a file that a check regenerates or reads,
// so each pattern names one tracked file, without a wildcard, that is a
// generated file or a register a run writes back (isGeneratedInput); its
// `runs` names tiers only, because a package target would be walked for
// its dependents; and a file inside a Go package directory, which its
// owning package would otherwise select, has that package under `tests`,
// so the package's own tests still run.
func generatedRowProblems(row string, in nonGoInput, tracked []string, dirs pkgDirs) []string {
	var problems []string
	for _, target := range in.Runs {
		if !contains(allTiers(), target) {
			problems = append(problems, fmt.Sprintf("%s: a generated row lists %q under runs; list a tier there, and a package whose tests check the file under tests", row, target))
		}
	}
	for _, p := range in.Paths {
		if strings.ContainsAny(p, "*?") {
			problems = append(problems, fmt.Sprintf("%s: a generated row's pattern %q is not a literal file path", row, p))
			continue
		}
		if !contains(tracked, p) {
			continue // the pattern check reports a pattern that matches no tracked file
		}
		gen := false
		var err error
		if dirs.generated != nil {
			gen, err = dirs.generated(p)
		}
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", row, err))
		case !gen:
			problems = append(problems, fmt.Sprintf("%s: %q is neither a generated file nor a register a run writes back, so a generated row cannot name it", row, p))
		}
		if owner := owningPackageDir(p, dirs); owner != "" && !contains(in.Tests, owner) {
			problems = append(problems, fmt.Sprintf("%s: %q lies in the Go package %s, which a generated row lists under tests", row, p, owner))
		}
	}
	return problems
}

// owningPackageDir returns the nearest directory above p that holds a Go
// package, as the selection's package graph would own the file, or "".
func owningPackageDir(p string, dirs pkgDirs) string {
	for dir := path.Dir(p); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if dirs.isPackage(dir) {
			return dir
		}
	}
	return ""
}

// testsTargetProblems returns what is wrong with the `tests` targets of
// one row. A `tests` target names one package whose own tests run, so a
// tier name, a pattern, a path that is not clean and repo-relative, a
// directory that holds no Go package, a package with no test file, which
// would select nothing, a target listed twice, and a directory the row also
// lists under `runs` in any spelling are each refused. The last would leave
// the row's meaning to whichever form the selection reads first.
func testsTargetProblems(row string, in nonGoInput, dirs pkgDirs) []string {
	runs := map[string]bool{}
	for _, t := range in.Runs {
		runs[targetDir(t)] = true
	}
	seen := map[string]bool{}
	var problems []string
	for _, target := range in.Tests {
		dup := seen[target]
		seen[target] = true
		switch {
		case contains(allTiers(), target):
			problems = append(problems, fmt.Sprintf("%s: tests target %q is a tier name; list a tier under runs", row, target))
		case target == "" || strings.ContainsAny(target, "*?") || strings.HasSuffix(target, "/...") || path.Clean(target) != target ||
			path.IsAbs(target) || target == "." || strings.HasPrefix(target, "../"):
			problems = append(problems, fmt.Sprintf("%s: tests target %q is not a clean repo-relative package directory", row, target))
		case !dirs.isPackage(target):
			problems = append(problems, fmt.Sprintf("%s: tests target %q is not a Go package directory", row, target))
		case !dirs.hasTests(target):
			problems = append(problems, fmt.Sprintf("%s: tests target %q holds no test file, so it selects no test", row, target))
		case dup:
			problems = append(problems, fmt.Sprintf("%s: tests target %q is listed twice", row, target))
		case runs[target]:
			problems = append(problems, fmt.Sprintf("%s: target %q is under both runs and tests", row, target))
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

// holdsFile reports whether a directory holds a file whose name ends with
// suffix, such as ".go" or "_test.go".
func holdsFile(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
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
