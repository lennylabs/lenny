// SPDX-License-Identifier: MIT

// Package testcache locates a stable on-disk directory for test
// artifacts that must survive for the whole of a test-binary run and be
// shared between the test binaries the unit tier starts concurrently.
//
// The system temp directory is the wrong home for those artifacts. A
// whole-module `go test ./...` run keeps a compiled helper binary or an
// extracted PostgreSQL bundle alive for the length of the package's
// tests, which on a large module is tens of minutes, and any agent that
// sweeps the system temp directory during that window deletes the
// artifact out from under the running process. The failure surfaces far
// from its cause: `fork/exec /tmp/<dir>/<binary>: no such file or
// directory` for a binary the same process built minutes earlier, or an
// embedded PostgreSQL that cannot start because its extracted bin
// directory vanished mid-run. Rooting the artifacts in the user cache
// directory instead takes them out of that sweep and lets concurrent
// test binaries share one copy, which is what keeps the tier-1 unit
// run inside the TESTING.md §12.1 wall-clock target on a small host.
//
// The directory is a cache. Deleting it costs a rebuild or a re-extract
// and nothing else, so nothing in it is ever authoritative.
package testcache

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// EnvRoot names the environment variable that overrides the cache
// root. An operator running in a sandbox with no writable user cache
// directory, or one who wants the cache on a specific filesystem,
// points this at a directory of their choosing.
const EnvRoot = "LENNY_TEST_CACHE_DIR"

// Root returns the cache root, creating it when absent. It is
// $LENNY_TEST_CACHE_DIR when that is set, and <user cache
// dir>/lenny-test otherwise.
func Root() (string, error) {
	root := os.Getenv(EnvRoot)
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate user cache dir: %w", err)
		}
		root = filepath.Join(base, "lenny-test")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create test cache root %s: %w", root, err)
	}
	return root, nil
}

// Dir returns the named subdirectory of Root, creating it when absent.
// The name may contain separators, so a caller can namespace its
// artifacts (for example "embedded-postgres/v16-linux-arm64").
func Dir(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create test cache dir %s: %w", dir, err)
	}
	return dir, nil
}

// Lock takes an exclusive advisory lock on the named lock file under
// Root and returns the function that releases it. The lock is held by
// the file descriptor, so it is released if the holder dies, and it is
// visible to every process on the host. Callers use it to make the
// population of a shared cache entry happen once even when several test
// binaries reach it at the same moment.
//
// The returned release function is safe to call exactly once; callers
// normally defer it.
func Lock(name string) (func(), error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, name+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
