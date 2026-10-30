// SPDX-License-Identifier: MIT

package binbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const fixturePkg = "./tests/testinfra/binbuild/testdata/hello"

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §6 (The lenny-test harness)
//
// TestBuildLinksOncePerProcessAndGivesEachTestItsOwnCopy pins what the
// subprocess harnesses rely on (TEST-GAPS T-17.4.33): concurrent callers
// share one link of a command, each caller gets the binary in a directory
// of its own that its cleanup removes, and the binary runs.
func TestBuildLinksOncePerProcessAndGivesEachTestItsOwnCopy(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("blocked: go not on PATH: %v", err)
	}
	t.Cleanup(func() {
		if err := Cleanup(); err != nil {
			t.Errorf("Cleanup: %v", err)
		}
	})

	const callers = 4
	paths := make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.Run("caller", func(t *testing.T) {
				paths[i] = Build(t, fixturePkg, "hello")
				out, err := exec.Command(paths[i]).Output()
				if err != nil {
					t.Fatalf("run %s: %v", paths[i], err)
				}
				if got := strings.TrimSpace(string(out)); got != "hello from binbuild" {
					t.Errorf("binary printed %q", got)
				}
			})
		}()
	}
	wg.Wait()

	dirs := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			t.Fatal("a caller got no binary")
		}
		dirs[filepath.Dir(p)] = true
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists after its test's cleanup (err=%v)", p, err)
		}
	}
	if len(dirs) != callers {
		t.Errorf("callers shared directories: %v", dirs)
	}

	mu.Lock()
	n := len(builds)
	shared := builds[fixturePkg]
	mu.Unlock()
	if n != 1 || shared == nil || shared.path == "" {
		t.Fatalf("builds = %d entries, fixture entry %+v; want one linked entry", n, shared)
	}
	if _, err := os.Stat(shared.path); err != nil {
		t.Errorf("shared binary missing before Cleanup: %v", err)
	}
	if err := Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(shared.path)); !os.IsNotExist(err) {
		t.Errorf("shared build directory survives Cleanup (err=%v)", err)
	}
}

// spec: TESTING.md §6 (The lenny-test harness)
//
// TestBuildFailsTheTestWithTheCompilerOutput pins the failure path: a
// command that does not build fails the asking test rather than handing it
// a missing binary.
func TestBuildFailsTheTestWithTheCompilerOutput(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("blocked: go not on PATH: %v", err)
	}
	t.Cleanup(func() { _ = Cleanup() })
	_, err := sharedBinary(repoRootOf(t), "./tests/testinfra/binbuild/testdata/no-such-command", "missing")
	if err == nil {
		t.Fatal("sharedBinary of a missing package returned no error")
	}
	if !strings.Contains(err.Error(), "no-such-command") {
		t.Errorf("error %q does not name the package", err)
	}
	// A second call returns the cached failure without linking again.
	_, again := sharedBinary(repoRootOf(t), "./tests/testinfra/binbuild/testdata/no-such-command", "missing")
	if again == nil || again.Error() != err.Error() {
		t.Errorf("second call = %v, want the cached %v", again, err)
	}
}

// spec: TESTING.md §6 (The lenny-test harness)
//
// TestSweepRemovesOnlyTheDirectoriesOfExitedProcesses pins the start-time
// sweep: a directory left by a process that exited is removed, while the
// directory of a running process, this process's own, and a name with no
// process ID are kept.
func TestSweepRemovesOnlyTheDirectoriesOfExitedProcesses(t *testing.T) {
	root := t.TempDir()
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Skipf("blocked: cannot run true to obtain an exited process ID: %v", err)
	}
	deadPID := exited.ProcessState.Pid()
	keep := []string{
		cachePrefix + strconv.Itoa(os.Getpid()),
		cachePrefix + "1", // init is always running
		cachePrefix + "not-a-pid",
	}
	gone := cachePrefix + strconv.Itoa(deadPID)
	for _, name := range append(append([]string(nil), keep...), gone) {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if running(deadPID) {
		t.Skipf("blocked: process ID %d was reused before the sweep ran", deadPID)
	}
	sweep(root)
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("sweep removed %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
		t.Errorf("sweep kept %s, the directory of an exited process (err=%v)", gone, err)
	}
}

// repoRootOf returns the module root, the directory go build runs in.
func repoRootOf(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}
