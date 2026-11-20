// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The shrink-only register of the golangci-lint whole-tree run. The
// register carries, for each Go package directory, the number of findings
// each linter reports there. The whole-tree run fails the tier when a
// count rises above the register or a package and linter the register
// does not carry reports a finding, so a branch cannot add a finding off
// the lines it changes. A count that falls is written back to the register
// and also fails the run, so the author commits the lowered register with
// the change, as the section-citation ratchet does. The check is
// contributor tooling, so this file carries no specification annotation.

// golangciLintRegister is the repo-relative path of the register.
const golangciLintRegister = "tests/registers/golangci-lint-findings.yaml"

// The declaration the register carries. It is a baseline rather than an
// exception register (tests/registers/README.md): a count has no owner
// and no expiry, and its route to zero is GATE-I1.
const (
	lintRegisterKind    = "golangci-lint-finding-count-baseline"
	lintRegisterVersion = 1
)

// lintKey names one count of the register: the findings one linter
// reports in one package directory.
type lintKey struct {
	pkg    string
	linter string
}

// String renders the key for a report.
func (k lintKey) String() string { return k.pkg + " " + k.linter }

// lintFinding is one finding of the whole-tree run, with its file path
// relative to the checkout.
type lintFinding struct {
	file   string
	line   int
	col    int
	linter string
	text   string
}

// String renders the finding in golangci-lint's own text form.
func (f lintFinding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s (%s)", f.file, f.line, f.col, f.text, f.linter)
}

// key returns the register count the finding belongs to.
func (f lintFinding) key() lintKey { return lintKey{pkg: path.Dir(f.file), linter: f.linter} }

// golangciLintJSON is the part of golangci-lint's JSON report
// (--output.json.path) the register reads.
type golangciLintJSON struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
}

// parseLintReport reads golangci-lint's JSON report and returns its
// findings with each path relative to root. A finding that names a file
// outside root is an error rather than a count of another package:
// golangci-lint returns a cached finding with the path it was first
// reported under, so such a path means the cache holds another
// checkout's results, and its counts do not describe this tree.
func parseLintReport(root string, body []byte) ([]lintFinding, error) {
	var rep golangciLintJSON
	if err := json.Unmarshal(body, &rep); err != nil {
		return nil, fmt.Errorf("parse the golangci-lint JSON report: %w", err)
	}
	out := make([]lintFinding, 0, len(rep.Issues))
	var outside []string
	for _, is := range rep.Issues {
		name := is.Pos.Filename
		if filepath.IsAbs(name) {
			if rel, err := filepath.Rel(root, name); err == nil {
				name = rel
			}
		}
		name = filepath.ToSlash(filepath.Clean(name))
		if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			outside = append(outside, is.Pos.Filename)
			continue
		}
		if is.FromLinter == "" {
			return nil, fmt.Errorf("the golangci-lint JSON report carries a finding in %s with no linter", name)
		}
		out = append(out, lintFinding{file: name, line: is.Pos.Line, col: is.Pos.Column, linter: is.FromLinter, text: is.Text})
	}
	if len(outside) > 0 {
		sort.Strings(outside)
		return nil, fmt.Errorf("golangci-lint reports %d finding(s) in files outside %s, such as %s, which a cache shared with another checkout returns; clear the cache with `golangci-lint cache clean`",
			len(outside), root, outside[0])
	}
	return out, nil
}

// countLintFindings returns the number of findings per package and
// linter.
func countLintFindings(findings []lintFinding) map[lintKey]int {
	counts := map[lintKey]int{}
	for _, f := range findings {
		counts[f.key()]++
	}
	return counts
}

// lintRise is one count the register does not allow.
type lintRise struct {
	key        lintKey
	registered int
	counted    int
	// absent reports that the register carries no count for the key.
	absent bool
}

// String renders the rise, naming both counts.
func (r lintRise) String() string {
	if r.absent {
		return fmt.Sprintf("%s: %d finding(s), and %s carries no count", r.key, r.counted, golangciLintRegister)
	}
	return fmt.Sprintf("%s: %d finding(s) where %s carries %d", r.key, r.counted, golangciLintRegister, r.registered)
}

// lintComparison is the result of comparing the counts of one run with
// the register.
type lintComparison struct {
	// held is how many registered counts the run matched.
	held int
	// lowered is every count that fell, with the count the run read; a
	// count that fell to zero loses its entry.
	lowered []lintRise
	// risen is every count that rose, or that the register lacks.
	risen []lintRise
	// kept is the register the run writes back when a count fell: every
	// count that held or fell at its new value, and every count that
	// rose at its registered value, so a rise is reported rather than
	// absorbed.
	kept map[lintKey]int
}

// compareLintCounts compares the counted findings with the register.
func compareLintCounts(counted, carried map[lintKey]int) lintComparison {
	c := lintComparison{kept: map[lintKey]int{}}
	for _, k := range sortedLintKeys(counted) {
		now := counted[k]
		was, ok := carried[k]
		switch {
		case !ok:
			c.risen = append(c.risen, lintRise{key: k, counted: now, absent: true})
		case now > was:
			c.risen = append(c.risen, lintRise{key: k, registered: was, counted: now})
			c.kept[k] = was
		case now < was:
			c.lowered = append(c.lowered, lintRise{key: k, registered: was, counted: now})
			c.kept[k] = now
		default:
			c.held++
			c.kept[k] = was
		}
	}
	for _, k := range sortedLintKeys(carried) {
		if _, ok := counted[k]; !ok {
			c.lowered = append(c.lowered, lintRise{key: k, registered: carried[k]})
		}
	}
	sort.Slice(c.lowered, func(i, j int) bool { return lintKeyLess(c.lowered[i].key, c.lowered[j].key) })
	return c
}

// sortedLintKeys returns the map's keys ordered by package, then linter.
func sortedLintKeys(m map[lintKey]int) []lintKey {
	keys := make([]lintKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lintKeyLess(keys[i], keys[j]) })
	return keys
}

// lintKeyLess orders keys by package, then linter.
func lintKeyLess(a, b lintKey) bool {
	if a.pkg != b.pkg {
		return a.pkg < b.pkg
	}
	return a.linter < b.linter
}

// lintRegisterDoc is the register's schema. Packages is a pointer so a
// document with no packages block is told apart from an emptied one.
type lintRegisterDoc struct {
	Kind     string               `yaml:"kind"`
	Version  int                  `yaml:"version"`
	Packages *[]lintRegisterEntry `yaml:"packages"`
}

// lintRegisterEntry is one package's counts as the register carries
// them.
type lintRegisterEntry struct {
	Package string         `yaml:"package"`
	Linters map[string]int `yaml:"linters"`
}

// loadLintRegister reads and validates the register. A missing,
// malformed, or self-contradictory register is an error rather than an
// empty set: a load that degraded to carrying nothing would fail every
// count, and one that degraded to an unbounded count would exempt the
// rise the register exists to stop.
func loadLintRegister(file string) (map[lintKey]int, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read the golangci-lint register: %w", err)
	}
	var doc lintRegisterDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the golangci-lint register %s: %w", golangciLintRegister, err)
	}
	if doc.Kind != lintRegisterKind {
		return nil, fmt.Errorf("the golangci-lint register %s: expected kind %q, got %q", golangciLintRegister, lintRegisterKind, doc.Kind)
	}
	if doc.Version != lintRegisterVersion {
		return nil, fmt.Errorf("the golangci-lint register %s: expected version %d, got %d", golangciLintRegister, lintRegisterVersion, doc.Version)
	}
	if doc.Packages == nil {
		return nil, fmt.Errorf("the golangci-lint register %s carries no packages block", golangciLintRegister)
	}
	carried := map[lintKey]int{}
	seen := map[string]bool{}
	for _, e := range *doc.Packages {
		if strings.TrimSpace(e.Package) == "" {
			return nil, fmt.Errorf("the golangci-lint register %s carries an entry with no package", golangciLintRegister)
		}
		if seen[e.Package] {
			return nil, fmt.Errorf("the golangci-lint register %s declares package %s twice", golangciLintRegister, e.Package)
		}
		seen[e.Package] = true
		if len(e.Linters) == 0 {
			return nil, fmt.Errorf("the golangci-lint register %s carries package %s with no linter count", golangciLintRegister, e.Package)
		}
		for linter, n := range e.Linters {
			if strings.TrimSpace(linter) == "" {
				return nil, fmt.Errorf("the golangci-lint register %s carries a count with no linter in package %s", golangciLintRegister, e.Package)
			}
			if n < 1 {
				return nil, fmt.Errorf("the golangci-lint register %s carries a count of %d for %s in package %s, and a count at zero holds no entry",
					golangciLintRegister, n, linter, e.Package)
			}
			carried[lintKey{pkg: e.Package, linter: linter}] = n
		}
	}
	return carried, nil
}

// writeLintRegister writes the register holding exactly the counts
// given, dropping any count of zero. It is the one writer of the file:
// the downward rewrite and the seeding both call it.
func writeLintRegister(file string, counts map[lintKey]int) error {
	byPkg := map[string]map[string]int{}
	for k, n := range counts {
		if n < 1 {
			continue
		}
		if byPkg[k.pkg] == nil {
			byPkg[k.pkg] = map[string]int{}
		}
		byPkg[k.pkg][k.linter] = n
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	entries := make([]lintRegisterEntry, 0, len(pkgs))
	for _, p := range pkgs {
		entries = append(entries, lintRegisterEntry{Package: p, Linters: byPkg[p]})
	}
	body, err := yaml.Marshal(lintRegisterDoc{Kind: lintRegisterKind, Version: lintRegisterVersion, Packages: &entries})
	if err != nil {
		return fmt.Errorf("encode the golangci-lint register: %w", err)
	}
	if err := os.WriteFile(file, append([]byte(lintRegisterHeader), body...), 0o644); err != nil {
		return fmt.Errorf("rewrite the golangci-lint register: %w", err)
	}
	return nil
}

// lintRegisterHeader explains the register to a reader who opens it
// without the check beside them.
const lintRegisterHeader = `# SPDX-License-Identifier: MIT
#
# Per-package counts of golangci-lint findings, by linter.
#
# Each count is how many findings one linter reports in one Go package
# directory when the static tier's golangci-lint check runs over the whole
# tree (cmd/lenny-test/lint_register.go). Issue GATE-I1 tracks the
# findings, and every count reaches zero before the whole-tree run becomes
# fatal on any finding.
#
# The check fails a count that rises above the count here, and a package
# and linter with a finding and no entry, so a branch cannot add a finding
# on a line it does not change. A count that falls is written back here by
# the run that measures the fall, and that run fails so that the author
# commits the lowered count with the change. No run raises a count, and a
# finding a branch adds is fixed rather than registered.
`
