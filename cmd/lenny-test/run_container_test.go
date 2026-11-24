// SPDX-License-Identifier: MIT

package main

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// `lenny-test run --container` is contributor tooling (change 0172,
// decision 10), so these cases carry no specification annotation.

// TestContainerRunArgsDropOnlyTheContainerFlag checks that the arguments
// the host half passes to the container keep the selection and drop
// --container in every spelling the flag package accepts.
func TestContainerRunArgsDropOnlyTheContainerFlag(t *testing.T) {
	in := []string{"--since", "main", "--container", "--tier", "unit,component", "-container", "--container=true", "-container=false", "--containers", "--dry-run"}
	want := []string{"--since", "main", "--tier", "unit,component", "--containers", "--dry-run"}
	if got := containerRunArgs(in); !reflect.DeepEqual(got, want) {
		t.Errorf("containerRunArgs = %v; want %v", got, want)
	}
	if got := containerRunArgs(nil); len(got) != 0 {
		t.Errorf("no arguments = %v", got)
	}
}

// TestRunContainerFlagRunsTheSelectionInTheContainer checks that `run
// --container` hands its arguments to the host half and returns its exit
// code, and that a run without the flag does not.
func TestRunContainerFlagRunsTheSelectionInTheContainer(t *testing.T) {
	prev := runInContainerFn
	t.Cleanup(func() { runInContainerFn = prev })
	var gotArgs []string
	runInContainerFn = func(_ string, args []string) int {
		gotArgs = args
		return 7
	}
	args := []string{"--since", "main", "--container", "--dry-run"}
	if code := runRun(args); code != 7 {
		t.Errorf("run --container exit code %d; want the container's 7", code)
	}
	if !reflect.DeepEqual(gotArgs, args) {
		t.Errorf("the host half got %v; want %v", gotArgs, args)
	}
	gotArgs = nil
	if code := runRun([]string{"--container", "--no-such-flag"}); code != 2 || gotArgs != nil {
		t.Errorf("a bad flag beside --container = %d, %v; want a usage error before any container", code, gotArgs)
	}
}

// TestRunContainerUsesTheGateContainerSetup checks the docker run argv of
// `run --container`: the gate's mounts, caches, user, network, and Docker
// socket, with runContainerScript and the run's arguments in place of the
// gate's script, and that the gate itself still runs gateContainerScript.
func TestRunContainerUsesTheGateContainerSetup(t *testing.T) {
	h := gateHost{
		root: "/w", image: "lenny-gate:abc", imageID: "sha256:1", tmpDir: "/tmp/lenny-gate-1/w", dockerSocket: "/var/run/docker.sock",
		uid: 501, gid: 20, passArgs: []string{"--since", "main"}, script: runContainerScript, scriptName: runContainerName,
	}
	args := gateDockerRunArgs(h)
	tail := args[len(args)-6:]
	if want := []string{"sh", "-c", runContainerScript, runContainerName, "--since", "main"}; !reflect.DeepEqual(tail, want) {
		t.Errorf("argv tail = %q; want the run script and its arguments", tail)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--network host", "/w:/w", gateModVolume + ":/go/pkg/mod", gateBuildVolume + ":" + gateGoCache, "/var/run/docker.sock:" + gateContainerSocket, gateEnvUID + "=501", "GOFLAGS=-buildvcs=false"} {
		if !strings.Contains(joined, want) {
			t.Errorf("run --container argv lacks %q: %s", want, joined)
		}
	}
	h.script, h.scriptName = "", ""
	gate := gateDockerRunArgs(h)
	if got := gate[len(gate)-5 : len(gate)-2]; !reflect.DeepEqual(got, []string{"-c", gateContainerScript, "lenny-test-gate"}) {
		t.Errorf("the gate's argv no longer runs gateContainerScript: %q", got)
	}
}

// TestRunContainerScriptBuildsFromTheTreeAndDropsToTheUser checks the
// container's script: it parses, it creates and drops to the worktree's
// owner as the gate does, it builds lenny-test from the working tree and
// runs `lenny-test run` with the passed arguments, and it neither fills
// the lint cache nor runs the gate.
func TestRunContainerScriptBuildsFromTheTreeAndDropsToTheUser(t *testing.T) {
	if out, err := exec.Command("sh", "-n", "-c", runContainerScript).CombinedOutput(); err != nil {
		t.Fatalf("the script does not parse: %v: %s", err, out)
	}
	for _, want := range []string{
		gateUserSetup,
		`go build -o "$GOTMPDIR/lenny-test" ./cmd/lenny-test`,
		`exec "$GOTMPDIR/lenny-test" run "$@"`,
		`exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$run" lenny-test-run "$@"`,
		`exec sh -c "$run" lenny-test-run "$@"`,
	} {
		if !strings.Contains(runContainerScript, want) {
			t.Errorf("the run script lacks %q", want)
		}
	}
	for _, unwanted := range []string{"lint-cache-fill", "gate --here", "git archive"} {
		if strings.Contains(runContainerScript, unwanted) {
			t.Errorf("the run script holds %q, which only the gate runs", unwanted)
		}
	}
	if !strings.Contains(gateContainerScript, gateUserSetup) {
		t.Error("the gate script no longer runs the shared user setup")
	}
}
