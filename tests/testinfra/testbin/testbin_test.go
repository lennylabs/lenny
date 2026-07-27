// SPDX-License-Identifier: MIT

package testbin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/testbin"
	"github.com/lennylabs/lenny/tests/testinfra/testcache"
)

// referenceRuntime is a command every checkout builds. The echo runtime
// is the smallest one, and it exits cleanly on empty stdin, so running
// it is a cheap way to prove the published file is executable.
const referenceRuntime = "./cmd/runtimes/echo"

// TestBuildPublishesOutsideTheSystemTempDir pins the property the
// module-wide unit run depends on. A test package that compiles a
// helper into the system temp directory keeps it there for the length
// of that package's tests, and a sweep of that directory during the run
// leaves the package spawning a binary that no longer exists.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: The compiled helper landed under the system temp
//
//	directory. Long-running packages that spawn it will fail
//	with "fork/exec ...: no such file or directory" whenever
//	that directory is swept mid-run.
func TestBuildPublishesOutsideTheSystemTempDir(t *testing.T) {
	if os.Getenv(testcache.EnvRoot) != "" {
		t.Skipf("%s points the cache at an operator-chosen directory", testcache.EnvRoot)
	}
	bin, err := testbin.Build(referenceRuntime)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("resolve system temp dir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatalf("resolve %s: %v", bin, err)
	}
	if resolved == tmp || strings.HasPrefix(resolved, tmp+string(os.PathSeparator)) {
		t.Errorf("built %s under the system temp dir %s", resolved, tmp)
	}

	// The published file is an executable that runs. echo exits cleanly
	// on an empty stdin, which is the Basic conformance behaviour.
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("running the published binary failed: %v\n%s", err, out)
	}
}

// TestBuildIsIdempotent confirms repeated calls converge on one copy
// rather than one copy per caller, which is what keeps the module-wide
// run from compiling the same command in every package that needs it.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: Two calls for the same command returned different paths,
//
//	so every test package still pays for its own copy.
func TestBuildIsIdempotent(t *testing.T) {
	first, err := testbin.Build(referenceRuntime)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	second, err := testbin.Build(referenceRuntime)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if first != second {
		t.Errorf("Build returned %q then %q, want one shared path", first, second)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("stat %s after the second build: %v", second, err)
	}
	// No staging file is left behind for the next run to trip over.
	matches, err := filepath.Glob(second + ".tmp-*")
	if err != nil {
		t.Fatalf("glob staging files: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("staging files left behind: %v", matches)
	}
}

// TestBuildRejectsAnImportPathThatNamesNoCommand covers the error path.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: Build accepted an import path with no final element, so a
//
//	caller typo produces a confusing build failure deep in the
//	go toolchain rather than an immediate, named error.
func TestBuildRejectsAnImportPathThatNamesNoCommand(t *testing.T) {
	for _, pkg := range []string{"", ".", "/"} {
		if _, err := testbin.Build(pkg); err == nil {
			t.Errorf("Build(%q) returned no error", pkg)
		}
	}
}

// TestRepoRootHoldsGoMod confirms the module-root walk that lets a test
// binary resolve a module-relative import path from its own package
// directory.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: RepoRoot did not find the module root, so every
//
//	module-relative import path a caller passes fails to build.
func TestRepoRootHoldsGoMod(t *testing.T) {
	root, err := testbin.RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("no go.mod at the reported module root %s: %v", root, err)
	}
}
