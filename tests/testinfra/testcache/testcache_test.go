// SPDX-License-Identifier: MIT

package testcache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/testcache"
)

// TestRootIsOutsideTheSystemTempDir pins the reason this package
// exists. A whole-module `go test ./...` run holds a compiled helper
// binary or an extracted database bundle for the length of a package's
// tests, and anything that sweeps the system temp directory during that
// window deletes it mid-run. The cache root therefore has to sit
// somewhere the sweep does not reach.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: Root resolved under the system temp directory, so every
//
//	artifact this package is supposed to protect is back in
//	the path of a temp-directory sweep.
func TestRootIsOutsideTheSystemTempDir(t *testing.T) {
	if os.Getenv(testcache.EnvRoot) != "" {
		t.Skipf("%s points the cache at an operator-chosen directory", testcache.EnvRoot)
	}
	root, err := testcache.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("resolve system temp dir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve cache root: %v", err)
	}
	if resolved == tmp || strings.HasPrefix(resolved, tmp+string(os.PathSeparator)) {
		t.Errorf("cache root %s is under the system temp dir %s", resolved, tmp)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("Root did not create a directory at %s (stat err %v)", root, err)
	}
}

// TestEnvRootOverridesTheCacheRoot covers the escape hatch for a host
// with no writable user cache directory.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: The cache root ignored its override, so a sandbox that
//
//	cannot write to the user cache directory has no way to
//	relocate the cache.
func TestEnvRootOverridesTheCacheRoot(t *testing.T) {
	want := filepath.Join(t.TempDir(), "cache-root")
	t.Setenv(testcache.EnvRoot, want)

	got, err := testcache.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

// TestDirCreatesNestedNamespaces confirms a caller can namespace its
// artifacts with a multi-segment name.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: Dir did not create the nested directory it returned, so a
//
//	caller writing into it fails on a missing parent.
func TestDirCreatesNestedNamespaces(t *testing.T) {
	t.Setenv(testcache.EnvRoot, filepath.Join(t.TempDir(), "cache-root"))

	dir, err := testcache.Dir(filepath.Join("embedded-postgres", "v16-test"))
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !fi.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}

// TestLockExcludesASecondHolderUntilRelease is the property the shared
// cache depends on: whichever process reaches an unpopulated entry
// first populates it while every other one waits, rather than several
// of them extracting into the same tree at once.
//
// spec: 17.4 (test-support surfaces owned by test infrastructure)
// diagnosis: The advisory lock did not exclude a second holder.
//
//	Concurrent test binaries will populate the same cache
//	entry simultaneously and corrupt each other's extraction.
func TestLockExcludesASecondHolderUntilRelease(t *testing.T) {
	t.Setenv(testcache.EnvRoot, filepath.Join(t.TempDir(), "cache-root"))

	release, err := testcache.Lock("bundle")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		second, err := testcache.Lock("bundle")
		if err != nil {
			close(acquired)
			return
		}
		acquired <- second
	}()

	select {
	case <-acquired:
		t.Fatal("a second holder took the lock while the first still held it")
	case <-time.After(250 * time.Millisecond):
	}

	release()

	select {
	case second, ok := <-acquired:
		if !ok {
			t.Fatal("the second holder failed to take the released lock")
		}
		second()
	case <-time.After(10 * time.Second):
		t.Fatal("the second holder never took the lock after it was released")
	}
}
