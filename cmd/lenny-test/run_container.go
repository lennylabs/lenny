// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"strings"
)

// This file holds `lenny-test run --container` (change 0172, decision 10):
// the `run` selection inside the gate image with the gate's container
// setup, so `run --since main` on a developer host runs the unit,
// component, and contract tiers in the environment the gate uses. The
// container gets the Docker socket, runs as the worktree's owner, and
// keeps the module and build caches in the gate's volumes, as
// gateDockerRunArgs states. It yields no merge verdict: the run writes the
// verdict `run` writes, and only `lenny-test gate` writes a gate verdict.
// The harness is contributor tooling, so this file carries no
// specification annotation.

// runLintVolume and runLintCache are the named volume and the path of
// golangci-lint's cache in a `run --container`. The run keeps its lint
// results apart from the gate's cache on the build cache volume, which a
// later gate run in the same worktree fills and reads, so the run's lint
// of uncommitted code and its tests cannot write what the gate's static
// tier reads (owner's answer to GT-11, 2026-11-20).
const (
	runLintVolume = "lenny-run-lintcache"
	runLintCache  = "/var/cache/lenny-run-lint"
)

// runTmpPrefix names the shared fixture directory of `run --container`,
// apart from the gate's, so a gate that starts in the same worktree does
// not empty the directory of a run in progress.
const runTmpPrefix = "lenny-run-"

// runContainerScript is the container's command for `run --container`. It
// starts as root, empties the run's shared fixture directory, which a
// killed earlier run leaves behind, and creates the directories the Go
// toolchain writes. It points golangci-lint at the run's own cache
// (runLintCache) and mounts the gate's lint cache directory read-only over
// itself, as gateContainerScript does after its fill, so neither the run's
// lint nor its tests write the gate's cache. When LENNY_GATE_UID names a
// user other than root, it creates that user with gateUserSetup and drops
// to it with setpriv. The user then builds lenny-test from the working
// tree, so an uncommitted edit of the harness takes part as it does in a
// `run` on the host, and runs `lenny-test run` with the arguments the host
// half passes. The gate builds its harness from an export of the commit
// and fills its lint cache before it locks it; the run does neither,
// because its result is not a merge verdict.
const runContainerScript = `set -e
if [ -n "${LENNY_TEST_SHARED_TMPDIR:-}" ]; then find "$LENNY_TEST_SHARED_TMPDIR" -mindepth 1 -delete; fi
gate_lint="${GOLANGCI_LINT_CACHE:?}"
export GOLANGCI_LINT_CACHE=` + runLintCache + `
mkdir -p "$LENNY_TEST_TMPDIR" "$GOTMPDIR" "$GOCACHE" "$gate_lint" "$GOLANGCI_LINT_CACHE" /go/pkg/mod
run='set -e
go build -o "$GOTMPDIR/lenny-test" ./cmd/lenny-test
exec "$GOTMPDIR/lenny-test" run "$@"'
lock_gate_lint() {
	mount --bind "$gate_lint" "$gate_lint"
	mount -o remount,bind,ro "$gate_lint"
}
uid="${LENNY_GATE_UID:-0}"
gid="${LENNY_GATE_GID:-0}"
if [ "$uid" = 0 ]; then
	lock_gate_lint
	exec sh -c "$run" lenny-test-run "$@"
fi
` + gateUserSetup + `lock_gate_lint
exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$run" lenny-test-run "$@"`

// runContainerName is the $0 of runContainerScript, which names the
// command in the shell's messages.
const runContainerName = "lenny-test-run"

// containerRunArgs returns the `run` arguments the host half passes to
// the container: the command line without `--container` in any spelling
// the flag package accepts, so the run inside the container runs the
// selection rather than starting another container.
func containerRunArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		name := strings.TrimLeft(a, "-")
		if strings.HasPrefix(a, "-") && (name == "container" || strings.HasPrefix(name, "container=")) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// runInContainerFn is the host half of `run --container`; tests replace
// it so the flag's routing is testable without Docker.
var runInContainerFn = runRunInContainer

// runRunInContainer runs `lenny-test run` with args inside the gate
// image, with the gate's container setup and the Docker socket, and
// returns the run's exit code. The build cache volume's size cap is the
// gate's default, or LENNY_GATE_GOCACHE_CAP_GB when it is set.
func runRunInContainer(root string, args []string) int {
	capGB, err := resolveGateGoCacheCap(false, defaultGateGoCacheCapGB, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test run: %v\n", err)
		return 2
	}
	return runInGateContainer(root, gateContainerRun{
		label:     "lenny-test run",
		script:    runContainerScript,
		name:      runContainerName,
		passArgs:  containerRunArgs(args),
		hint:      "run `lenny-test run` without --container",
		tmpPrefix: runTmpPrefix,
		volumes:   []string{runLintVolume + ":" + runLintCache},
	}, false, true, capGB)
}
