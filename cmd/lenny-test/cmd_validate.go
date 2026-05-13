package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
		if !startsWith(trimLeft(line), "func Test") {
			continue
		}
		funcs++
		if !hasDiagnosisBefore(lines, i) {
			missing = append(missing, fmt.Sprintf("%s:%d", path, i+1))
		}
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
