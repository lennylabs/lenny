// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
)

// runValidate runs the checks of the harness's committed selection and
// register files: the groups and their subsets, the flake budget, the
// cloud parity matrix, the shared register contract, the non-Go input
// list, and the annotation of every test in the tiers validate-diagnosis
// covers. Change 0116 (CS-9) retired the spec map and the change graph
// with the checks that read them, and renamed the command from
// validate-maps.
func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := repoRoot()
	groupsPath, subsetsPath, flakeBudgetPath, parityMatrixPath := yamlPaths(root)

	results := []checkResult{
		validateGroupsYAML(groupsPath),
		validateGroupsSubsetsYAML(subsetsPath),
		validateFlakeBudgetYAML(flakeBudgetPath),
		validateParityMatrixYAML(parityMatrixPath),
		validateRegistersDir(filepath.Join(root, registersDir), repoRegisterRules(root)),
		validateNonGoInputsFile(root),
		validateTierTestsAnnotated(root),
	}

	failed := 0
	for _, r := range results {
		if !r.ok {
			failed++
		}
	}

	if *jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"results": results,
			"failed":  failed,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		for _, r := range results {
			marker := "ok"
			if !r.ok {
				marker = "FAIL"
			}
			fmt.Printf("[%s] %s: %s\n", marker, r.name, r.detail)
		}
		fmt.Printf("\n%d checks, %d failed\n", len(results), failed)
	}

	if failed > 0 {
		return 1
	}
	return 0
}

// runValidateDiagnosis ensures every component-and-up test function has a
// // diagnosis: comment.
//
// Phase 0 implementation:
//   - Walks tests/ recursively looking for *_test.go files under tier
//     directories at or above component (component, contract,
//     integration, e2e_kind, e2e_cloud, load_kind,
//     chaos, security, conformance).
//   - For each, scans for top-level Test functions and confirms a //
//     diagnosis: comment appears within the 10 lines preceding the function.
//
// In Phase 0 there are no tests yet, so this is effectively a no-op that
// confirms the validator runs without error. As tests are added it gains teeth.
// componentAndAboveTierDirs returns the tier directories at or above
// component, relative to the repository root. validate-diagnosis and the
// annotation check of validate (validateTierTestsAnnotated) range over
// this one list, so a directory added to one is added to the other.
func componentAndAboveTierDirs() []string {
	return []string{
		"tests/component",
		"tests/contract",
		"tests/integration",
		"tests/e2e_kind",
		"tests/e2e_cloud",
		"tests/load_local",
		"tests/load_kind",
		"tests/chaos",
		"tests/security",
		"tests/conformance",
		"tests/load_cloud",
	}
}

func runValidateDiagnosis(args []string) int {
	fs := flag.NewFlagSet("validate-diagnosis", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := repoRoot()
	tierDirs := componentAndAboveTierDirs()

	totalFiles := 0
	totalFuncs := 0
	missing := []string{}

	for _, dir := range tierDirs {
		full := filepath.Join(root, dir)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			continue
		}
		_ = filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			if !hasSuffix(path, "_test.go") {
				return nil
			}
			totalFiles++
			fns, miss := scanDiagnosis(path)
			totalFuncs += fns
			missing = append(missing, miss...)
			return nil
		})
	}

	failed := len(missing)

	if *jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"total_files":  totalFiles,
			"total_funcs":  totalFuncs,
			"missing":      missing,
			"failed_count": failed,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("validate-diagnosis: scanned %d files, %d test functions, %d missing diagnosis\n",
			totalFiles, totalFuncs, failed)
		for _, m := range missing {
			fmt.Printf("  missing: %s\n", m)
		}
	}

	if failed > 0 {
		return 1
	}
	return 0
}

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	ok     bool
	name   string
	detail string
}

func newResult(name string, ok bool, detail string) checkResult {
	return checkResult{
		Name:   name,
		OK:     ok,
		Detail: detail,
		ok:     ok,
		name:   name,
		detail: detail,
	}
}

func scanDiagnosis(path string) (int, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil
	}
	lines := splitLines(string(data))
	funcs := 0
	missing := []string{}
	for i, line := range lines {
		stripped := trimLeft(line)
		if !startsWith(stripped, "func Test") {
			continue
		}
		// TestMain is the Go test-binary entrypoint, not a test case.
		// It has no assertions, so the diagnosis-comment convention
		// does not apply.
		if startsWith(stripped, "func TestMain(") {
			continue
		}
		funcs++
		// §17.2 marks both `// spec:` and `// diagnosis:` as
		// mandatory. Accept the test when either both annotations
		// are present above the function body, OR a scaffold skip
		// is inside (the skip carries the same diagnostic
		// information at runtime).
		hasSpec := hasAnnotationBefore(lines, i, "// spec:")
		hasDiag := hasAnnotationBefore(lines, i, "// diagnosis:")
		if hasSpec && hasDiag {
			continue
		}
		if hasNotImplementedSkipAfter(lines, i) {
			continue
		}
		// Report which annotation is missing for actionable output.
		missingMarker := []string{}
		if !hasSpec {
			missingMarker = append(missingMarker, "spec")
		}
		if !hasDiag {
			missingMarker = append(missingMarker, "diagnosis")
		}
		missing = append(missing, fmt.Sprintf("%s:%d (missing %s)", path, i+1, strings.Join(missingMarker, " + ")))
	}
	return funcs, missing
}

// hasAnnotationBefore returns true when the doc-comment block immediately
// preceding the function at idx contains the named annotation marker (e.g.
// "// spec:" or "// diagnosis:"). It scans upward over the contiguous run of
// comment lines (and the blank lines that separate paragraphs within a
// leading comment) that abut the function, stopping at the first line that is
// neither a comment nor blank. Bounding by the function's own doc block rather
// than a fixed line count keeps a thorough multi-paragraph `// spec:` plus
// `// diagnosis:` header from pushing one annotation out of range, while a
// trailing-blank-line gap before unrelated code still ends the scan so an
// annotation on a different declaration is never mis-attributed.
func hasAnnotationBefore(lines []string, idx int, marker string) bool {
	for i := idx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			// A blank line inside a leading comment block separates
			// paragraphs; keep scanning as long as a comment line still
			// precedes it. A blank line with no comment above it (plain code
			// gap) ends the block on the next iteration.
			if !precedingCommentExists(lines, i) {
				return false
			}
			continue
		}
		if !startsWith(trimmed, "//") {
			// The first non-comment, non-blank line ends the doc block.
			return false
		}
		if containsSubstr(lines[i], marker) {
			return true
		}
	}
	return false
}

// precedingCommentExists reports whether any line above idx, up to the first
// non-comment/non-blank line, is a comment. It lets hasAnnotationBefore treat
// a blank line as part of a leading comment block only when a comment still
// sits above it.
func precedingCommentExists(lines []string, idx int) bool {
	for i := idx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		return startsWith(trimmed, "//")
	}
	return false
}

// hasNotImplementedSkipAfter checks the 8 lines following the
// function signature for a skip-style scaffold marker. Scaffolds
// use one of:
//
//   - t.Skip("not implemented: …")        → standard scaffold
//   - t.Skipf("not implemented: …", …)    → Skipf variant
//   - t.Skip("blocked: …")                 → scaffold blocked on
//     missing infrastructure (e.g. no KMS adapter, no delegation
//     primitive, no clock-injection harness). The reason text names
//     the missing infrastructure precisely.
//   - t.Skip("phase-gated: …")            → blocked behind a phase
//   - t.Skip("flaky-time: …")              → flaky-time quarantine
//   - kind.SkipUnlessAvailable(t)         → e2e_kind one-liner
//   - cloud.SkipUnlessAuthorized(t)       → e2e_cloud helper
//   - any *.SkipUnless* harness helper that gates the test on an
//     infrastructure precondition
//
// Either form is acceptable: the skip message names the spec
// section and the missing implementation, which is exactly the
// information `// diagnosis:` is meant to carry, and it has the
// additional benefit that the harness sees it at runtime too. When
// the scaffold is replaced with a real test, the author writes the
// canonical `// spec:` and `// diagnosis:` annotations above the
// function.
func hasNotImplementedSkipAfter(lines []string, idx int) bool {
	end := idx + 8
	if end >= len(lines) {
		end = len(lines) - 1
	}
	// Both t.Skip(...) and t.Skipf(...) are acceptable. The reason
	// categories §17.9 allows come from cmd/lenny-test/skipreason,
	// which the static-tier skip-reason classifier reads as well, so the
	// two readers of the convention cannot enumerate different sets.
	prefixes := make([]string, 0, len(skipreason.Categories))
	for _, category := range skipreason.Categories {
		prefixes = append(prefixes, "\""+category)
	}
	for i := idx; i <= end; i++ {
		line := lines[i]
		if !containsSubstr(line, "t.Skip(") && !containsSubstr(line, "t.Skipf(") {
			if containsSubstr(line, ".SkipUnless") {
				return true
			}
			continue
		}
		for _, p := range prefixes {
			if containsSubstr(line, p) {
				return true
			}
		}
	}
	return false
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func trimLeft(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[i:]
		}
	}
	return ""
}

func hasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func containsSubstr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
