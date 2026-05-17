// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// testEvent matches the `go test -json` event schema. Each line of
// stdout decodes into one of these.
type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"` // run, pause, cont, pass, bench, fail, output, skip, start
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
}

// tierResult captures what a tier executor learned from parsing
// `go test -json`. The fields map directly onto §7 verdict tier
// counts and failure entries.
type tierResult struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Failures []failureEntry
	RawOut   string // tail of stdout/stderr for diagnostics
}

// runGoTestJSON runs `go test -json <extraArgs...>` and returns the
// parsed result plus the underlying go-test exit error. The exit
// error is preserved so callers can distinguish "tests failed" from
// "harness blew up".
func runGoTestJSON(extraArgs ...string) (*tierResult, error) {
	args := append([]string{"test", "-json"}, extraArgs...)
	cmd := exec.Command("go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	result := parseGoTestJSON(stdout.Bytes(), stderr.Bytes())
	return result, runErr
}

// parseGoTestJSON walks the stream of test events from `go test -json`.
// Output that is not valid JSON (typically build errors emitted to
// stderr) is preserved in RawOut so failure messages stay actionable.
func parseGoTestJSON(stdout, stderr []byte) *tierResult {
	result := &tierResult{}
	// Per-test bookkeeping: each (package, test) tracks its current
	// status and accumulated output.
	type key struct{ pkg, test string }
	type acc struct {
		output  strings.Builder
		elapsed float64
	}
	tests := map[key]*acc{}

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	var nonJSON strings.Builder
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			nonJSON.Write(line)
			nonJSON.WriteByte('\n')
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			nonJSON.Write(line)
			nonJSON.WriteByte('\n')
			continue
		}
		if ev.Test == "" {
			// Package-level events (build errors, package summary).
			// `go test -json` emits a package-level fail with no Test
			// name when the build itself fails.
			if ev.Action == "output" && ev.Output != "" {
				nonJSON.WriteString(ev.Output)
			}
			continue
		}
		k := key{ev.Package, ev.Test}
		a, ok := tests[k]
		if !ok {
			a = &acc{}
			tests[k] = a
		}
		switch ev.Action {
		case "output":
			a.output.WriteString(ev.Output)
		case "pass":
			result.Passed++
			result.Total++
			a.elapsed = ev.Elapsed
		case "fail":
			result.Failed++
			result.Total++
			a.elapsed = ev.Elapsed
			result.Failures = append(result.Failures, failureEntryFromAccumulator(ev.Package, ev.Test, a.output.String(), ev.Elapsed))
		case "skip":
			result.Skipped++
			result.Total++
			a.elapsed = ev.Elapsed
		}
	}
	// Combine non-JSON stdout with stderr; both go into RawOut.
	tail := nonJSON.String() + string(stderr)
	if len(tail) > 16*1024 {
		tail = tail[len(tail)-16*1024:]
	}
	result.RawOut = tail
	// Sort failures deterministically: package, then test.
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Package != result.Failures[j].Package {
			return result.Failures[i].Package < result.Failures[j].Package
		}
		return result.Failures[i].Test < result.Failures[j].Test
	})
	// Best-effort spec-section enrichment: for each failure with a
	// known package + test name, scan the matching _test.go file for
	// `// spec: 11.7` annotations above the function. Errors are
	// silently swallowed; spec section attribution is incremental.
	for i := range result.Failures {
		f := &result.Failures[i]
		f.SpecSections = lookupSpecSections(f.Package, f.Test)
	}
	return result
}

// specSectionRe extracts a numeric reference like "8.2" or "11.7.3"
// from a `// spec: <ref> (<note>)` annotation. Multiple references
// per annotation are comma-separated and listed in spec-map.json.
var (
	specSectionRe   = regexp.MustCompile(`(?m)^\s*//\s*spec:\s*(.*)$`)
	sectionNumberRe = regexp.MustCompile(`\b\d+(?:\.\d+)*\b`)
)

// lookupSpecSections returns the list of spec sections attributed
// to a failing test by scanning *_test.go files in the test's
// package directory for `// spec:` annotations above the function.
// Returns nil when the package directory is unresolvable, the file
// list is empty, or no annotations are present.
func lookupSpecSections(pkg, test string) []string {
	if pkg == "" || test == "" {
		return nil
	}
	dir := packageDir(pkg)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if sections := scanFileForSpecSections(string(body), test); len(sections) > 0 {
			return sections
		}
	}
	return nil
}

// packageDir resolves a Go import path to a filesystem directory
// under the project root. Handles bare relative imports (./pkg/x)
// as well as fully qualified ones (github.com/lennylabs/lenny/pkg/x).
func packageDir(pkg string) string {
	root := repoRoot()
	const prefix = "github.com/lennylabs/lenny/"
	switch {
	case strings.HasPrefix(pkg, prefix):
		return filepath.Join(root, strings.TrimPrefix(pkg, prefix))
	case strings.HasPrefix(pkg, "./"):
		return filepath.Join(root, strings.TrimPrefix(pkg, "./"))
	default:
		// Best-effort: many failures report the bare package path.
		return filepath.Join(root, pkg)
	}
}

// scanFileForSpecSections finds `func <test>(` in body and walks
// upward through the contiguous comment block immediately above it,
// collecting every `// spec:` annotation. The walk stops at the
// first non-comment, non-blank line so annotations belonging to a
// previous function or package-level declaration do not bleed in.
// Returns the collected section IDs in document order.
func scanFileForSpecSections(body, test string) []string {
	lines := strings.Split(body, "\n")
	funcSig := "func " + test + "("
	for idx, line := range lines {
		if !strings.Contains(line, funcSig) {
			continue
		}
		// Collect the contiguous comment-or-blank block above idx.
		first := idx - 1
		for first >= 0 {
			t := strings.TrimSpace(lines[first])
			if strings.HasPrefix(t, "//") || t == "" {
				first--
				continue
			}
			break
		}
		first++
		var sections []string
		seen := map[string]bool{}
		for i := first; i < idx; i++ {
			m := specSectionRe.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			for _, s := range sectionNumberRe.FindAllString(m[1], -1) {
				if seen[s] {
					continue
				}
				seen[s] = true
				sections = append(sections, s)
			}
		}
		return sections
	}
	return nil
}

// failureEntryFromAccumulator builds a §7 failureEntry from the
// accumulated `go test -json` output for a single failing test.
// File and line come from the standard Go test output format
// `<file>:<line>: <message>`; spec sections are extracted from
// `// spec:` comments in the surrounding source (best-effort, via a
// follow-up file scan in the caller when needed).
func failureEntryFromAccumulator(pkg, test, output string, elapsed float64) failureEntry {
	out := strings.TrimRight(output, "\n")
	stdoutTail := out
	if len(stdoutTail) > 4*1024 {
		stdoutTail = "... " + stdoutTail[len(stdoutTail)-4*1024:]
	}
	file, line := parseFirstFileLine(out)
	errMsg := firstErrorLine(out)
	rerun := fmt.Sprintf("go test -run '^%s$' -count=1 %s", test, pkg)
	return failureEntry{
		Test:         test,
		Package:      pkg,
		File:         file,
		Line:         line,
		Error:        errMsg,
		DurationMS:   int64(elapsed * 1000),
		StdoutTail:   stdoutTail,
		RerunCommand: rerun,
	}
}

// parseFirstFileLine scans the `go test` output for the first line
// matching `<file>:<line>: <message>` and returns (file, line). The
// Go test framework prefixes assertion failures with this format.
func parseFirstFileLine(out string) (string, int) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// File names produced by `go test` look like
		// "foo_test.go:42:" or "    foo_test.go:42: message".
		if idx := strings.Index(line, ".go:"); idx > 0 {
			file := line[:idx+3]
			rest := line[idx+4:]
			colon := strings.IndexByte(rest, ':')
			if colon < 0 {
				continue
			}
			var ln int
			if _, err := fmt.Sscanf(rest[:colon], "%d", &ln); err != nil {
				continue
			}
			// Strip any leading whitespace from the file.
			file = strings.TrimSpace(file)
			return file, ln
		}
	}
	return "", 0
}

// firstErrorLine returns the first non-empty line of output that
// looks like an assertion message. Falls back to the first non-empty
// line when nothing matches.
func firstErrorLine(out string) string {
	var first string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if first == "" {
			first = t
		}
		if strings.Contains(t, ".go:") && strings.Contains(t, ":") {
			return t
		}
	}
	return first
}
