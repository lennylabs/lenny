// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// `lenny-test run --container` is contributor tooling (change 0172,
// decision 10), so these cases carry no specification annotation.

// TestRunContainerKeepsTheGateLintCacheReadOnly checks that the run's
// script points golangci-lint at the run's own cache, mounts the gate's
// lint cache directory read-only before any user code runs, empties the
// run's own fixture directory, and that the run's container mounts its
// lint volume.
func TestRunContainerKeepsTheGateLintCacheReadOnly(t *testing.T) {
	for _, want := range []string{
		`gate_lint="${GOLANGCI_LINT_CACHE:?}"`,
		"export GOLANGCI_LINT_CACHE=" + runLintCache,
		`mount -o remount,bind,ro "$gate_lint"`,
		`find "$LENNY_TEST_SHARED_TMPDIR" -mindepth 1 -delete`,
	} {
		if !strings.Contains(runContainerScript, want) {
			t.Errorf("the run script lacks %q", want)
		}
	}
	// The lock comes before each path that runs the user's code.
	root := strings.Index(runContainerScript, `if [ "$uid" = 0 ]; then`)
	rootExec := strings.Index(runContainerScript, `exec sh -c "$run"`)
	userExec := strings.Index(runContainerScript, `exec setpriv`)
	firstLock := strings.Index(runContainerScript[root:], "\tlock_gate_lint\n")
	lastLock := strings.LastIndex(runContainerScript, "lock_gate_lint\nexec setpriv")
	if firstLock < 0 || root+firstLock > rootExec || lastLock < 0 || lastLock > userExec {
		t.Errorf("the gate's lint cache is not locked before the run starts:\n%s", runContainerScript)
	}
	h := gateHost{root: "/w", image: "lenny-gate:abc", passArgs: []string{"--since", "main"}, script: runContainerScript, scriptName: runContainerName, volumes: []string{runLintVolume + ":" + runLintCache}}
	if args := strings.Join(gateDockerRunArgs(h), " "); !strings.Contains(args, "-v "+runLintVolume+":"+runLintCache) {
		t.Errorf("the run container does not mount its lint volume: %s", args)
	}
}

// TestRunContainerUsesItsOwnFixtureDirectory checks that the run's shared
// fixture directory differs from the gate's for the same worktree, so a
// gate that starts there does not empty a run in progress.
func TestRunContainerUsesItsOwnFixtureDirectory(t *testing.T) {
	gate, err := hostTmpDir(gateTmpPrefix, "/work/a")
	if err != nil {
		t.Fatal(err)
	}
	run, err := hostTmpDir(runTmpPrefix, "/work/a")
	if err != nil {
		t.Fatal(err)
	}
	if gate == run || !strings.Contains(run, runTmpPrefix) {
		t.Errorf("gate %s and run %s share a fixture directory", gate, run)
	}
	if len(run) > len(gate) {
		t.Errorf("the run's fixture directory %s is longer than the gate's %s, which leaves less room for a socket path", run, gate)
	}
}

// TestRunContainerRefusesABadSelectionOnTheHost checks that `run
// --container` refuses an unknown tier and a missing selector before it
// builds or starts a container.
func TestRunContainerRefusesABadSelectionOnTheHost(t *testing.T) {
	prev := runInContainerFn
	t.Cleanup(func() { runInContainerFn = prev })
	started := false
	runInContainerFn = func(string, []string) int {
		started = true
		return 0
	}
	if code := runRun([]string{"--container", "--tier", "bogus"}); code != 2 || started {
		t.Errorf("an unknown tier = %d, started %v; want a usage error on the host", code, started)
	}
	if code := runRun([]string{"--container"}); code != 2 || started {
		t.Errorf("no selector = %d, started %v; want a usage error on the host", code, started)
	}
}
