// SPDX-License-Identifier: MIT

// Package testbin compiles a command from this module once and hands
// every test binary that asks for it the same path.
//
// Several test packages drive a real reference runtime or CLI as a
// child process, so each of them needs the command compiled before its
// tests run. Building into a per-package temp directory makes the
// module-wide unit run compile the same command once per package and
// leaves the result under the system temp directory for the length of
// that package's tests, where a temp-directory sweep can delete it
// mid-run. Building through this package puts one copy in the
// testcache directory, rebuilds it under a host-wide lock so concurrent
// test binaries do not compile it at the same moment, and publishes it
// with a rename so a process that is already executing the previous
// copy keeps a valid binary.
//
// The build itself is still a `go build`, so the Go build cache decides
// whether any compilation happens. A call that finds everything cached
// costs a fraction of a second.
package testbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/lennylabs/lenny/tests/testinfra/testcache"
)

// Build compiles pkg, an import path relative to the module root such
// as "./cmd/runtimes/delegation-echo", and returns the absolute path of
// the resulting executable. The executable lives in the shared test
// cache and is reused by every test binary in the run.
//
// Build is safe to call concurrently from separate processes.
func Build(pkg string) (string, error) {
	name := filepath.Base(pkg)
	if name == "." || name == "/" || name == "" {
		return "", fmt.Errorf("testbin: %q does not name a command", pkg)
	}
	dir, err := testcache.Dir("bin")
	if err != nil {
		return "", fmt.Errorf("testbin: %w", err)
	}
	root, err := RepoRoot()
	if err != nil {
		return "", fmt.Errorf("testbin: %w", err)
	}

	unlock, err := testcache.Lock(filepath.Join("bin", name))
	if err != nil {
		return "", fmt.Errorf("testbin: %w", err)
	}
	defer unlock()

	final := filepath.Join(dir, name)
	staging := final + ".tmp-" + strconv.Itoa(os.Getpid())
	defer func() { _ = os.Remove(staging) }()

	cmd := exec.Command("go", "build", "-o", staging, pkg)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("testbin: build %s: %w\n%s", pkg, err, out)
	}
	// Rename rather than write in place: another test binary may be
	// executing the previous copy, and replacing the directory entry
	// leaves that process on the old inode instead of truncating the
	// file underneath it.
	if err := os.Rename(staging, final); err != nil {
		return "", fmt.Errorf("testbin: publish %s: %w", final, err)
	}
	return final, nil
}

// MustBuild is Build with the error turned into a panic. TestMain has
// no *testing.T to fail against, so a build failure there has to abort
// the test binary; the panic message names the package that failed.
func MustBuild(pkg string) string {
	bin, err := Build(pkg)
	if err != nil {
		panic(err.Error())
	}
	return bin
}

// RepoRoot walks up from the working directory to the module root, the
// directory holding go.mod. Test binaries run with their package
// directory as the working directory, so every caller needs this to
// resolve a module-relative import path.
func RepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod above %s", wd)
		}
		d = parent
	}
}
