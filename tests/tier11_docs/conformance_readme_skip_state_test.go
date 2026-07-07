// SPDX-License-Identifier: MIT

// Tier-11 doc-consistency check for tests/tier10_conformance/README.md
// against tests/tier10_conformance/*_test.go. The README's "Current state"
// section names, per TESTING.md §12.10, which tier-10 tests are blocked on
// an undelivered dependency; that framing is only true for a test whose own
// body still calls t.Skip. When a blocked test is implemented, its t.Skip
// call is removed and the README must stop describing it as blocked, or a
// reader trusts a conformance gap that no longer exists.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 12.10 (Tier 10 — Conformance)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// conformanceReadmePath returns the path to the tier-10 conformance
// README.md relative to the repo root.
func conformanceReadmePath(root string) string {
	return filepath.Join(root, "tests", "tier10_conformance", "README.md")
}

// currentStateSection extracts the "## Current state" section of the
// tier-10 conformance README: from that heading up to (but not including)
// the next "## " heading, or the end of the document.
func currentStateSection(t *testing.T, readme string) string {
	t.Helper()
	idx := strings.Index(readme, "## Current state")
	if idx < 0 {
		t.Fatal("tests/tier10_conformance/README.md: no '## Current state' heading (renamed or removed?)")
	}
	rest := readme[idx+len("## Current state"):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return rest
}

// blockedTableRowTestNameRE matches a markdown table row whose first cell
// is a single backtick-quoted TestXxx identifier, the README's convention
// for naming a test as blocked on an undelivered dependency (see the
// "Blocked on" table this rule replaces if a test becomes blocked again).
// A test named in prose (for example "TestFidelityMatrix asserts ...")
// does not match; only a dedicated table row counts as a blocked claim.
var blockedTableRowTestNameRE = regexp.MustCompile(`(?m)^\|\s*` + "`" + `(Test[A-Za-z0-9_]+)` + "`" + `\s*\|`)

// blockedTableRowTestNames returns every TestXxx identifier named as the
// first cell of a markdown table row in body, de-duplicated and in
// encounter order.
func blockedTableRowTestNames(body string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range blockedTableRowTestNameRE.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// tier10ConformanceSource concatenates every non-generated *_test.go file
// directly under tests/tier10_conformance so the skip-state check covers
// the whole tier, not just scaffolds_test.go.
func tier10ConformanceSource(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "tests", "tier10_conformance")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var sb strings.Builder
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sb.Write(b)
		sb.WriteString("\n")
		found = true
	}
	if !found {
		t.Fatalf("no *_test.go files found under %s", dir)
	}
	return sb.String()
}

// topLevelFuncBody extracts the body of the top-level Go function named
// name from src, from its "func name(" line through the "}" that closes it
// at column zero (gofumpt/gofmt always place a top-level func's closing
// brace at the start of the line). It returns "" when the function is not
// found, so callers can distinguish "not present" from "present with an
// empty body".
func topLevelFuncBody(src, name string) string {
	marker := "func " + name + "("
	start := strings.Index(src, marker)
	if start < 0 {
		return ""
	}
	rest := src[start:]
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		if i > 0 && line == "}" {
			return strings.Join(lines[:i+1], "\n")
		}
	}
	return rest
}

// spec: 12.10
// diagnosis: tests/tier10_conformance/README.md's "Current state" section
//
//	names a tier-10 test in a blocked-table row (skip-gated on an
//	undelivered dependency per TESTING.md §12.10) while its function body in
//	the tier's *_test.go files carries no t.Skip call. A failure here means
//	the test was implemented and its skip removed, but the README table
//	row was not, so a reader trusts a conformance gap that no longer exists.
func TestConformanceReadmeNamesOnlyActuallySkippedTests(t *testing.T) {
	root := repoRoot(t)
	readme := readDocPage(t, conformanceReadmePath(root))
	currentState := currentStateSection(t, readme)
	src := tier10ConformanceSource(t, root)

	for _, name := range blockedTableRowTestNames(currentState) {
		body := topLevelFuncBody(src, name)
		if body == "" {
			t.Errorf("tests/tier10_conformance/README.md 'Current state' section names %s in a blocked-table row, but no such function exists under tests/tier10_conformance (renamed or removed?)", name)
			continue
		}
		if !strings.Contains(body, "t.Skip") {
			t.Errorf("tests/tier10_conformance/README.md 'Current state' section lists %s in a blocked-table row, but its body carries no t.Skip call; update the README to match the implemented test", name)
		}
	}
}

// spec: 12.10
// diagnosis: a tier-10 test's own body calls t.Skip unconditionally on a
//
//	named missing deliverable (a genuine coverage gap), but
//	tests/tier10_conformance/README.md's "Current state" section does not
//	list it in a blocked-table row. A failure here means a newly introduced
//	conformance gap is undocumented, leaving a reader unaware the battery is
//	incomplete.
func TestConformanceReadmeNamesEveryDirectlySkippedTest(t *testing.T) {
	root := repoRoot(t)
	readme := readDocPage(t, conformanceReadmePath(root))
	currentState := currentStateSection(t, readme)
	documented := map[string]bool{}
	for _, name := range blockedTableRowTestNames(currentState) {
		documented[name] = true
	}

	src := tier10ConformanceSource(t, root)
	var skipped []string
	for _, name := range topLevelTestFuncNames(src) {
		body := topLevelFuncBody(src, name)
		if strings.Contains(body, "t.Skip") {
			skipped = append(skipped, name)
		}
	}
	sort.Strings(skipped)
	for _, name := range skipped {
		if !documented[name] {
			t.Errorf("%s calls t.Skip directly but tests/tier10_conformance/README.md 'Current state' section does not name it; document the gap or remove the skip", name)
		}
	}
}

// topLevelTestFuncNameRE matches a top-level Go test function declaration:
// func TestXxx(t *testing.T) {
var topLevelTestFuncNameRE = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\) \{`)

// topLevelTestFuncNames returns every top-level test function name
// declared in src, in encounter order.
func topLevelTestFuncNames(src string) []string {
	var names []string
	for _, m := range topLevelTestFuncNameRE.FindAllStringSubmatch(src, -1) {
		names = append(names, m[1])
	}
	return names
}
