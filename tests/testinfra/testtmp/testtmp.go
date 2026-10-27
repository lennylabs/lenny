// SPDX-License-Identifier: MIT

// Package testtmp resolves the directory test fixtures live in: binaries a
// TestMain builds once, sockets, and other files a package reuses across
// its tests. A fixture under /tmp disappears when the host cleans /tmp
// part-way through a run, after which every later test in the package
// fails with fork/exec or a socket error (TEST-GAPS T-4.7.23), so the root
// is never under /tmp. lenny-test exports the root it resolves as TMPDIR,
// GOTMPDIR, and LENNY_TEST_TMPDIR for every process it starts, and a
// fixture site calls MkdirTemp so a plain `go test` resolves the same
// root.
package testtmp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EnvRoot names the harness root. lenny-test sets it for every process
// it starts; an operator may set it to place fixtures elsewhere.
const EnvRoot = "LENNY_TEST_TMPDIR"

// HomeDirName is the directory under the home directory that holds
// fixtures when no harness root is set and TMPDIR is under /tmp. The name
// is short so a Unix socket path under it stays below the 108-byte limit.
const HomeDirName = ".lenny-tmp"

// Resolve returns the fixture root without creating it: $LENNY_TEST_TMPDIR
// when set, else os.TempDir() when it is not /tmp or under it, else
// $HOME/.lenny-tmp. With no home directory it returns os.TempDir().
func Resolve() string {
	if r := os.Getenv(EnvRoot); r != "" {
		return r
	}
	if d := os.TempDir(); !UnderTmp(d) {
		return d
	}
	return shortRoot()
}

// shortRoot returns $HOME/.lenny-tmp, or os.TempDir() with no home
// directory.
func shortRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return filepath.Join(home, HomeDirName)
}

// UnderTmp reports whether d is /tmp or a directory below it.
func UnderTmp(d string) bool {
	d = filepath.Clean(d)
	return d == "/tmp" || strings.HasPrefix(d, "/tmp/")
}

// Root resolves the fixture root and creates it when absent.
func Root() (string, error) {
	r := Resolve()
	if err := os.MkdirAll(r, 0o700); err != nil {
		return "", err
	}
	return r, nil
}

// MkdirTemp creates a new directory under Root with os.MkdirTemp's pattern
// rules. The caller removes it.
func MkdirTemp(pattern string) (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(r, pattern)
}

// ShortDir creates a directory for a Unix socket under $HOME/.lenny-tmp,
// whatever TMPDIR names, because a socket path is capped at 108 bytes and
// an operator's TMPDIR can be long. It is removed at t's cleanup.
func ShortDir(t testing.TB, pattern string) (string, error) {
	r := shortRoot()
	if err := os.MkdirAll(r, 0o700); err != nil {
		return "", err
	}
	d, err := os.MkdirTemp(r, pattern)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d, nil
}

// Env returns the TMPDIR and GOTMPDIR entries that point a child process,
// such as the `go build` of a fixture binary, at root.
func Env(root string) []string {
	return []string{"TMPDIR=" + root, "GOTMPDIR=" + root}
}
