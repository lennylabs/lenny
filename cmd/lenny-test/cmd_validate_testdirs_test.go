// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// testDirsKeyedRoot builds a temp git repo holding the given files, each
// written and added to the index, and a change graph carrying the given
// glob keys. It returns the root.
func testDirsKeyedRoot(t *testing.T, files, globs []string) string {
	t.Helper()
	root := t.TempDir()
	initCmd := exec.Command("git", "init", "-q")
	initCmd.Dir = root
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatalf("mkdir tests: %v", err)
	}
	for _, p := range files {
		writeChangeGraphSource(t, root, p)
	}
	trackChangeGraphSources(t, root, files...)
	globMap := map[string]any{}
	for _, g := range globs {
		globMap[g] = map[string]any{"component": []string{g + "..."}}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "globs": globMap})
	if err != nil {
		t.Fatalf("marshal change graph: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "change-graph.json"), raw, 0o644); err != nil {
		t.Fatalf("write change graph: %v", err)
	}
	return root
}

func runTestDirsKeyed(root string) checkResult {
	return validateTestDirsKeyed(filepath.Join(root, "tests", "change-graph.json"), root)
}

// TestValidateTestDirsKeyedFailsAnUnkeyedTestDirectory pins the check
// T-17.4.35 asked for: a tracked test directory under tests/tier*/ that
// no change-graph glob key covers fails validate-maps and is named,
// because `--since` would select none of its tests on its own change.
//
// spec: TESTING.md §6 (The lenny-test harness, Selection algorithm)
func TestValidateTestDirsKeyedFailsAnUnkeyedTestDirectory(t *testing.T) {
	files := []string{
		"tests/tier2_component/controllers/a_test.go",
		"tests/tier2_component/stores/b_test.go",
	}

	// One keyed and one unkeyed directory: the unkeyed one is named and
	// the keyed one is not.
	r := runTestDirsKeyed(testDirsKeyedRoot(t, files, []string{"tests/tier2_component/controllers/"}))
	expectFail(t, r, "tests/tier2_component/stores/")
	if containsSubstr(r.detail, "controllers") {
		t.Errorf("detail names a keyed directory: %s", r.detail)
	}

	// Every spelling of a covering key passes: the directory with or
	// without a trailing separator, the recursive form, and an ancestor.
	for _, keys := range [][]string{
		{"tests/tier2_component/controllers/", "tests/tier2_component/stores"},
		{"tests/tier2_component/controllers/...", "tests/tier2_component/stores/..."},
		{"tests/tier2_component/"},
	} {
		expectPass(t, runTestDirsKeyed(testDirsKeyedRoot(t, files, keys)))
	}

	// A key that only shares a string prefix with the directory, rather
	// than naming it or an ancestor, does not cover it.
	r = runTestDirsKeyed(testDirsKeyedRoot(t,
		[]string{"tests/tier2_component/leases/a_test.go"},
		[]string{"tests/tier2_component/lease"}))
	expectFail(t, r, "tests/tier2_component/leases/")

	// A nested test directory under an unkeyed parent fails on its own.
	r = runTestDirsKeyed(testDirsKeyedRoot(t,
		[]string{"tests/tier9_security/pentest/p_test.go"},
		[]string{"tests/tier9_security/other/"}))
	expectFail(t, r, "tests/tier9_security/pentest/")
}

// TestValidateTestDirsKeyedDomain pins what counts as a test directory:
// a tracked _test.go under tests/tier*/ outside testdata/. A non-test
// file, a testdata fixture, a harness directory outside the tier trees,
// and an untracked test file need no key.
//
// spec: TESTING.md §6 (The lenny-test harness, Selection algorithm)
func TestValidateTestDirsKeyedDomain(t *testing.T) {
	root := testDirsKeyedRoot(t, []string{
		"tests/tier2_component/stores/b_test.go",
		"tests/tier2_component/fixtures/helper.go",
		"tests/tier3_contract/sdks/testdata/x_test.go",
		"tests/testinfra/envtest/e_test.go",
	}, []string{"tests/tier2_component/stores/"})
	writeChangeGraphSource(t, root, "tests/tier4_integration/untracked_test.go")
	expectPass(t, runTestDirsKeyed(root))

	got := tierTestDirs([]string{
		"tests/tier2_component/stores/b_test.go",
		"tests/tier2_component/stores/c_test.go",
		"tests/tier2_component/fixtures/helper.go",
		"tests/tier3_contract/sdks/testdata/x_test.go",
		"tests/testinfra/envtest/e_test.go",
		"tests/tier4_integration/i_test.go",
	})
	want := []string{"tests/tier2_component/stores", "tests/tier4_integration"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tierTestDirs = %v; want %v", got, want)
	}
}

// TestValidateTestDirsKeyedFailsClosed pins that the check does not
// certify a tree it could not inspect: an unreadable change graph and a
// tree with no tracked tier test file both fail.
//
// spec: TESTING.md §6 (The lenny-test harness, Selection algorithm)
func TestValidateTestDirsKeyedFailsClosed(t *testing.T) {
	root := testDirsKeyedRoot(t, []string{"tests/tier2_component/stores/b_test.go"},
		[]string{"tests/tier2_component/stores/"})
	if err := os.Remove(filepath.Join(root, "tests", "change-graph.json")); err != nil {
		t.Fatal(err)
	}
	expectFail(t, runTestDirsKeyed(root), "change graph")

	root = testDirsKeyedRoot(t, []string{"tests/testinfra/envtest/e_test.go"}, nil)
	expectFail(t, runTestDirsKeyed(root), "no tracked _test.go")

	// A directory that is not a git repository cannot list tracked files.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "tests", "change-graph.json"),
		[]byte(`{"version":1,"globs":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runTestDirsKeyed(bare)
	if r.ok {
		t.Errorf("check passed outside a git repository: %s", r.detail)
	}
}

// TestRepoTestDirsAreKeyed runs the check over this tree, so an unkeyed
// test directory fails a package-scoped unit run as well as
// validate-maps.
//
// spec: TESTING.md §6 (The lenny-test harness, Selection algorithm)
func TestRepoTestDirsAreKeyed(t *testing.T) {
	root := repoRoot()
	expectPass(t, validateTestDirsKeyed(filepath.Join(root, "tests", "change-graph.json"), root))
}
