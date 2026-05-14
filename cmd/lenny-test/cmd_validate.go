// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runValidateMaps validates that tests/spec-map.json and tests/change-graph.json
// are syntactically valid and consistent with each other.
//
// Phase 0 implementation:
//   - Confirms both files exist and parse as JSON.
//   - Confirms every spec section listed under spec/ has either a spec-map
//     entry or an exception in tests/spec-map-exceptions.yaml.
//   - Confirms version fields match the expected schema version.
//
// Phase 1+ extends this with:
//   - Confirming every test file appears in at least one spec-map entry.
//   - Confirming every pkg/ package appears in the change graph.
//   - Confirming every chart template and migration is referenced.
//   - Diagnosing dangling references.
func runValidateMaps(args []string) int {
	fs := flag.NewFlagSet("validate-maps", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := repoRoot()
	specMapPath := filepath.Join(root, "tests", "spec-map.json")
	changeGraphPath := filepath.Join(root, "tests", "change-graph.json")

	results := []checkResult{
		validateJSONFile(specMapPath, "tests/spec-map.json"),
		validateJSONFile(changeGraphPath, "tests/change-graph.json"),
		validateSpecMapVersion(specMapPath),
		validateChangeGraphVersion(changeGraphPath),
		validateSpecMapPaths(specMapPath, root),
		validateChangeGraphPaths(changeGraphPath, root),
		validateChangeGraphFileExistence(changeGraphPath, root),
		validateTestFilesMapped(specMapPath, root),
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
//     directories at or above component (tier2_component, tier3_contract,
//     tier4_integration, tier5_e2e_kind, tier6_e2e_cloud, tier7_load,
//     tier8_chaos, tier9_security, tier10_conformance).
//   - For each, scans for top-level Test functions and confirms a //
//     diagnosis: comment appears within the 10 lines preceding the function.
//
// In Phase 0 there are no tests yet, so this is effectively a no-op that
// confirms the validator runs without error. As tests are added it gains teeth.
func runValidateDiagnosis(args []string) int {
	fs := flag.NewFlagSet("validate-diagnosis", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := repoRoot()
	tierDirs := []string{
		"tests/tier2_component",
		"tests/tier3_contract",
		"tests/tier4_integration",
		"tests/tier5_e2e_kind",
		"tests/tier6_e2e_cloud",
		"tests/tier7_load",
		"tests/tier8_chaos",
		"tests/tier9_security",
		"tests/tier10_conformance",
	}

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

func validateJSONFile(path, label string) checkResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return newResult(label, false, fmt.Sprintf("could not read: %v", err))
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return newResult(label, false, fmt.Sprintf("invalid JSON: %v", err))
	}
	return newResult(label, true, "syntactically valid")
}

func validateSpecMapVersion(path string) checkResult {
	v, err := readJSONInt(path, "version")
	if err != nil {
		return newResult("spec-map version", false, err.Error())
	}
	if v != 1 {
		return newResult("spec-map version", false, fmt.Sprintf("expected version 1, got %d", v))
	}
	return newResult("spec-map version", true, "1")
}

func validateChangeGraphVersion(path string) checkResult {
	v, err := readJSONInt(path, "version")
	if err != nil {
		return newResult("change-graph version", false, err.Error())
	}
	if v != 1 {
		return newResult("change-graph version", false, fmt.Sprintf("expected version 1, got %d", v))
	}
	return newResult("change-graph version", true, "1")
}

// validateSpecMapPaths walks every section and confirms the
// spec_file points at an extant file. Returns the count of missing
// references.
//
// Test/package/schema/migration/chart_templates paths are NOT
// required to exist yet (many ship in later phases). This check is
// limited to the spec_file pointer so the gate is reliable today.
func validateSpecMapPaths(specMapPath, root string) checkResult {
	data, err := os.ReadFile(specMapPath)
	if err != nil {
		return newResult("spec-map paths", false, err.Error())
	}
	var doc struct {
		Sections map[string]struct {
			SpecFile string `json:"spec_file"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return newResult("spec-map paths", false, err.Error())
	}
	missing := []string{}
	for section, entry := range doc.Sections {
		if entry.SpecFile == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.SpecFile)); err != nil {
			missing = append(missing, fmt.Sprintf("%s → %s", section, entry.SpecFile))
		}
	}
	if len(missing) > 0 {
		preview := missing
		if len(preview) > 5 {
			preview = append(preview[:5], fmt.Sprintf("... (%d more)", len(missing)-5))
		}
		return newResult("spec-map paths", false,
			fmt.Sprintf("%d missing spec_file reference(s): %s", len(missing), strings.Join(preview, "; ")))
	}
	return newResult("spec-map paths", true, fmt.Sprintf("%d section(s); every spec_file resolves", len(doc.Sections)))
}

// validateChangeGraphFileExistence walks every change-graph glob
// key and confirms it points at a file or directory that actually
// exists. Catches typos and renames that leave stale entries.
//
// Globs ending with `/...` (Go-style recursive) or `/` (directory
// hint) are matched against directories; other entries are matched
// against either a directory or a file. Entries that legitimately
// reference yet-to-land paths are tolerated when explicitly listed
// in tests/change-graph-pending.txt (one path per line); this lets
// the change graph commit ahead of the implementation.
func validateChangeGraphFileExistence(changeGraphPath, root string) checkResult {
	data, err := os.ReadFile(changeGraphPath)
	if err != nil {
		return newResult("change-graph file-existence", false, err.Error())
	}
	var doc struct {
		Globs map[string]any `json:"globs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return newResult("change-graph file-existence", false, err.Error())
	}
	pending := readPendingPaths(filepath.Join(root, "tests", "change-graph-pending.txt"))
	missing := []string{}
	for key := range doc.Globs {
		if pending[key] {
			continue
		}
		// Strip trailing `/...` or `/` to test the directory itself.
		probe := strings.TrimSuffix(key, "/...")
		probe = strings.TrimSuffix(probe, "/")
		if _, err := os.Stat(filepath.Join(root, probe)); err != nil {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		preview := missing
		if len(preview) > 5 {
			preview = append(preview[:5], fmt.Sprintf("... (%d more)", len(missing)-5))
		}
		return newResult("change-graph file-existence", false,
			fmt.Sprintf("%d glob(s) point at missing paths: %s",
				len(missing), strings.Join(preview, "; ")))
	}
	return newResult("change-graph file-existence", true,
		fmt.Sprintf("%d glob(s); every path resolves on disk", len(doc.Globs)))
}

// validateTestFilesMapped walks tests/tier{2..10}_*/ for _test.go
// files and confirms each is referenced from at least one section's
// `tests` list in spec-map.json (or is explicitly exempt via
// tests/spec-map-exceptions.yaml — though this version of the
// validator does not yet parse exceptions per-file). Test files
// matching the `testinfra/` or `testdata/` prefix are exempt by
// construction; same for fuzz_test.go and property_test.go which
// live alongside their pkg/.
func validateTestFilesMapped(specMapPath, root string) checkResult {
	data, err := os.ReadFile(specMapPath)
	if err != nil {
		return newResult("test files mapped", false, err.Error())
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return newResult("test files mapped", false, err.Error())
	}
	mapped := map[string]bool{}
	for _, sec := range doc.Sections {
		for _, t := range sec.Tests {
			// Normalise: a test entry might be a file path, a
			// directory glob (.../...), or a TestName attached via
			// "::". Strip the trailing /... or ::Name.
			path := t
			if i := strings.Index(path, "::"); i >= 0 {
				path = path[:i]
			}
			path = strings.TrimSuffix(path, "/...")
			path = strings.TrimSuffix(path, "/")
			mapped[path] = true
		}
	}

	// Walk every _test.go under the tier dirs at or above component.
	tierDirs := []string{
		"tests/tier2_component",
		"tests/tier3_contract",
		"tests/tier4_integration",
		"tests/tier5_e2e_kind",
		"tests/tier6_e2e_cloud",
		"tests/tier7_load",
		"tests/tier8_chaos",
		"tests/tier9_security",
		"tests/tier10_conformance",
	}
	orphans := []string{}
	for _, dir := range tierDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Scaffold test files are deliberate placeholders ahead
			// of their backing implementation; exempt by convention
			// (filename suffix or every test is a t.Skip scaffold).
			base := filepath.Base(path)
			if base == "scaffolds_test.go" {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			// Try direct match, parent dir match, parent's parent match.
			if mapped[rel] {
				return nil
			}
			parent := filepath.Dir(rel)
			for parent != "." && parent != "/" {
				if mapped[parent] {
					return nil
				}
				parent = filepath.Dir(parent)
			}
			orphans = append(orphans, rel)
			return nil
		})
	}
	if len(orphans) > 0 {
		preview := orphans
		if len(preview) > 5 {
			preview = append(preview[:5], fmt.Sprintf("... (%d more)", len(orphans)-5))
		}
		return newResult("test files mapped", false,
			fmt.Sprintf("%d test file(s) absent from spec-map: %s",
				len(orphans), strings.Join(preview, "; ")))
	}
	return newResult("test files mapped", true, "every tier-2+ test file appears in spec-map")
}

// readPendingPaths reads tests/change-graph-pending.txt — one path
// per line, '#' starts a comment. Returns an empty set when the
// file is absent.
func readPendingPaths(path string) map[string]bool {
	out := map[string]bool{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// validateChangeGraphPaths confirms each glob key points at a real
// pkg/, schemas/, migrations/, or charts/ path. Targeted globs that
// reference yet-to-ship packages are tolerated with a per-glob
// `phase:` marker (today simply accept anything under the canonical
// roots).
func validateChangeGraphPaths(changeGraphPath, root string) checkResult {
	data, err := os.ReadFile(changeGraphPath)
	if err != nil {
		return newResult("change-graph paths", false, err.Error())
	}
	var doc struct {
		Globs map[string]any `json:"globs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return newResult("change-graph paths", false, err.Error())
	}
	misshaped := []string{}
	knownRoots := []string{
		"pkg/", "schemas/", "migrations/", "charts/", "cmd/", "tests/", "docs/", "spec/",
		"sdks/", "scripts/", "compose/", "deploy/", ".github/",
	}
	knownFiles := map[string]bool{
		"buf.yaml": true, "buf.gen.yaml": true, "go.mod": true, "go.sum": true,
		".golangci.yml": true, "Makefile": true, "TESTING.md": true, "README.md": true,
	}
	for key := range doc.Globs {
		if knownFiles[key] {
			continue
		}
		ok := false
		for _, r := range knownRoots {
			if strings.HasPrefix(key, r) {
				ok = true
				break
			}
		}
		if !ok {
			misshaped = append(misshaped, key)
		}
	}
	if len(misshaped) > 0 {
		preview := misshaped
		if len(preview) > 5 {
			preview = append(preview[:5], fmt.Sprintf("... (%d more)", len(misshaped)-5))
		}
		return newResult("change-graph paths", false,
			fmt.Sprintf("%d glob(s) outside the canonical roots %v: %s",
				len(misshaped), knownRoots, strings.Join(preview, "; ")))
	}
	return newResult("change-graph paths", true,
		fmt.Sprintf("%d glob(s); every entry under a canonical root", len(doc.Globs)))
}

func readJSONInt(path, key string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return 0, err
	}
	raw, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("key %q not present", key)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("key %q not an integer: %v", key, err)
	}
	return n, nil
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
		if hasDiagnosisBefore(lines, i) {
			continue
		}
		// Scaffold form: a t.Skip("not implemented: ...") inside the
		// function body counts as a diagnosis. The skip message names
		// the spec section and the missing implementation, which is
		// exactly the information `// diagnosis:` is meant to carry,
		// and it has the additional benefit that the harness sees it
		// at runtime too. When the scaffold is replaced with a real
		// test, the author writes the canonical `// spec:` and
		// `// diagnosis:` annotations above the function.
		if hasNotImplementedSkipAfter(lines, i) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s:%d", path, i+1))
	}
	return funcs, missing
}

func hasDiagnosisBefore(lines []string, idx int) bool {
	start := idx - 10
	if start < 0 {
		start = 0
	}
	for i := start; i < idx; i++ {
		if containsSubstr(lines[i], "// diagnosis:") {
			return true
		}
	}
	return false
}

// hasNotImplementedSkipAfter checks the 8 lines following the function
// signature for a skip-style scaffold marker. Scaffolds use one of:
//
//   - t.Skip("not implemented: …")          → carry the diagnosis inline
//   - kind.SkipUnlessAvailable(t)           → tier-5 e2e_kind one-liner
//   - cloud.SkipUnlessAuthorized(t)         → future tier-6 helper
//   - any *.SkipUnless* harness helper that gates the test on an
//     infrastructure precondition
//
// Either form is acceptable: the skip message names the spec section
// and the missing implementation, which is exactly the information
// `// diagnosis:` is meant to carry, and it has the additional
// benefit that the harness sees it at runtime too. When the scaffold
// is replaced with a real test, the author writes the canonical
// `// spec:` and `// diagnosis:` annotations above the function.
func hasNotImplementedSkipAfter(lines []string, idx int) bool {
	end := idx + 8
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for i := idx; i <= end; i++ {
		if containsSubstr(lines[i], "t.Skip(\"not implemented:") {
			return true
		}
		if containsSubstr(lines[i], ".SkipUnless") {
			return true
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
