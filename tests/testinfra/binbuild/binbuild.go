// SPDX-License-Identifier: MIT

// Package binbuild compiles a repository command once per test process and
// gives each test a copy of the binary in a directory of its own.
//
// The subprocess harnesses (tests/testinfra/gateway, opsprocess, and
// tokenservice) used to run `go build` for every test that started a
// process. Linking cmd/lenny-gateway takes about seven seconds of wall time
// and ten of CPU even with a warm build cache, and the tier-4 package
// starts a gateway in about a hundred places, so the links alone cost
// several minutes of its runtime (TEST-GAPS T-17.4.33). Build links each
// command once and hard-links the result into a per-test directory, which
// keeps what callers relied on: the binary's directory belongs to the test,
// holds the logs of the processes started from it, and is removed at the
// test's cleanup.
//
// The shared copies live in a directory named for the process ID under the
// temporary directory. A package with a TestMain calls Cleanup after m.Run
// to remove it. A process that exits without Cleanup leaves the directory
// behind, and the next process that builds through this package removes
// every such directory whose process is no longer running.
package binbuild

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// cachePrefix starts the name of every process's shared build directory;
// the process ID follows it.
const cachePrefix = "lenny-binbuild-"

// build is one command's shared binary, linked at most once per process.
type build struct {
	once sync.Once
	path string
	err  error
}

var (
	// mu guards builds and cacheDir.
	mu       sync.Mutex
	builds   = map[string]*build{}
	cacheDir string
)

// Build returns the path of a binary of the command at pkg (a path such as
// "./cmd/lenny-gateway", relative to the repository root) named name, in a
// directory of t's own that t's cleanup removes. The first call for pkg in
// the process links the binary; later calls, including concurrent ones from
// parallel tests, reuse it. A failed link fails every test that asks for
// the command, with the compiler output.
func Build(t testing.TB, pkg, name string) string {
	t.Helper()
	shared, err := sharedBinary(schematest.RepoRoot(t), pkg, name)
	if err != nil {
		t.Fatalf("binbuild: %v", err)
	}
	tmp, err := os.MkdirTemp("", name+"-it-*")
	if err != nil {
		t.Fatalf("binbuild: mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	dst := filepath.Join(tmp, name)
	if err := linkOrCopy(shared, dst); err != nil {
		t.Fatalf("binbuild: place %s in %s: %v", name, tmp, err)
	}
	return dst
}

// sharedBinary links pkg once and returns the shared binary's path.
func sharedBinary(repoRoot, pkg, name string) (string, error) {
	mu.Lock()
	b, ok := builds[pkg]
	if !ok {
		b = &build{}
		builds[pkg] = b
	}
	mu.Unlock()
	b.once.Do(func() {
		dir, err := processDir()
		if err != nil {
			b.err = err
			return
		}
		out := filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir = repoRoot
		if msg, err := cmd.CombinedOutput(); err != nil {
			b.err = fmt.Errorf("build %s: %w\n%s", pkg, err, msg)
			return
		}
		b.path = out
	})
	return b.path, b.err
}

// processDir returns this process's shared build directory, creating it on
// first use after sweeping the directories of processes that have exited.
func processDir() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if cacheDir != "" {
		return cacheDir, nil
	}
	sweep(os.TempDir())
	dir := filepath.Join(os.TempDir(), cachePrefix+strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create the shared build directory: %w", err)
	}
	cacheDir = dir
	return dir, nil
}

// sweep removes, best effort, every shared build directory under root
// whose process is no longer running. A directory whose name carries no
// process ID is left alone.
func sweep(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		pidText, ok := strings.CutPrefix(e.Name(), cachePrefix)
		if !ok || !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid == os.Getpid() || running(pid) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// running reports whether a process with pid exists. A permission error
// means the process exists under another user.
func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// linkOrCopy hard-links src to dst, and copies it when the two paths are
// on different file systems or the link is refused.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Cleanup removes this process's shared build directory. A package's
// TestMain calls it after m.Run returns; a later Build in the same process
// links its commands again.
func Cleanup() error {
	mu.Lock()
	defer mu.Unlock()
	if cacheDir == "" {
		return nil
	}
	err := os.RemoveAll(cacheDir)
	cacheDir = ""
	builds = map[string]*build{}
	return err
}
