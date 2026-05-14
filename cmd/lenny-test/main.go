// SPDX-License-Identifier: MIT

// Command lenny-test is the single entry point for running Lenny's tests.
//
// It wraps `go test`, the docker-compose stack, the Kind cluster lifecycle, and
// k6 behind a uniform interface that produces a structured JSON verdict.
//
// The architecture, conventions, and selection model are documented in
// TESTING.md at the repository root.
//
// This is the Phase 0 skeleton. It implements:
//
//   - Flag parsing and selector resolution against groups.yaml,
//     spec-map.json, and change-graph.json.
//   - The "list", "validate-maps", "validate-diagnosis", and "infra" subcommands.
//   - A dry-run mode that prints the resolved selector and the tests it would
//     run, without executing them.
//   - The default run command, which currently dispatches the unit tier to
//     `go test` and writes a minimal verdict.
//
// Subsequent phases add real implementations of the higher tiers, the cached
// container daemon, the JUnit and GitHub-annotation emitters, and the
// PR-comment integration. See TESTING.md §13 for the build sequence.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const version = "0.0.1-phase0"

func main() {
	// Surface SIGINT/SIGTERM with a clear exit message and the
	// conventional 128+signum status. Child processes are in the
	// same process group on Unix and receive the same signal, so
	// `go test`, `docker compose`, and other subprocesses tear
	// down before this handler runs.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "\nlenny-test: interrupted (%s); cleaning up.\n", sig)
		// 130 = 128 + SIGINT; 143 = 128 + SIGTERM.
		if sig == syscall.SIGTERM {
			os.Exit(143)
		}
		os.Exit(130)
	}()

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"--help"}
	}

	// Subcommands. Anything that does not match a known subcommand falls
	// through to the run command (so `lenny-test --group pr` works without
	// requiring `lenny-test run --group pr`).
	switch args[0] {
	case "help", "-h", "--help":
		printUsage()
		os.Exit(0)
	case "version", "--version":
		fmt.Printf("lenny-test %s\n", version)
		os.Exit(0)
	case "validate-maps":
		os.Exit(runValidateMaps(args[1:]))
	case "validate-diagnosis":
		os.Exit(runValidateDiagnosis(args[1:]))
	case "list":
		os.Exit(runList(args[1:]))
	case "infra":
		os.Exit(runInfra(args[1:]))
	case "run":
		os.Exit(runRun(args[1:]))
	case "stress":
		os.Exit(runStress(args[1:]))
	case "conformance":
		os.Exit(runConformanceSubcommand(args[1:]))
	case "preflight":
		os.Exit(runPreflightSubcommand(args[1:]))
	case "baseline":
		os.Exit(runBaseline(args[1:]))
	case "comment":
		os.Exit(runComment(args[1:]))
	case "mutation":
		os.Exit(runMutation(args[1:]))
	case "coverage":
		os.Exit(runCoverage(args[1:]))
	case "report":
		os.Exit(runReport(args[1:]))
	case "watch":
		os.Exit(runWatch(args[1:]))
	case "cached":
		os.Exit(runCachedSubcommand(args[1:]))
	default:
		// Default to run.
		os.Exit(runRun(args))
	}
}

func printUsage() {
	fmt.Print(`lenny-test — Lenny's test selector and runner.

Usage:
  lenny-test [run] [flags]                 Run tests matching the given selectors.
  lenny-test validate-maps [flags]         Verify spec-map.json and change-graph.json
                                           are in sync with the codebase.
  lenny-test validate-diagnosis [flags]    Verify every component-and-up test has a
                                           // diagnosis: comment.
  lenny-test list [flags]                  Print the tests that would run for a
                                           given selector, without executing them.
  lenny-test infra <up|down|status|prune>  Manage cached test infrastructure.
  lenny-test stress --test <name> [flags]  Run a single test N times to detect
                                           flakes (§17.10).
  lenny-test conformance --image <ref>     Run the §12.10 conformance battery
                          --level <basic|standard|full>
                                           against a third-party runtime image.
  lenny-test preflight --cluster <path>    Check a target cluster's readiness
                                           to host Lenny (wraps preflight.sh).
  lenny-test version                       Print the version.
  lenny-test help                          This message.

Run flags:
  --group <name>           A named group from tests/groups.yaml (e.g., pr, pr-fast,
                           nightly, pre-release, phase-3.5-gate).
  --tier <name>            A single tier (static, unit, component, contract,
                           integration, e2e_kind, e2e_cloud, load, chaos, security,
                           conformance, docs).
  --max-tier <name>        Run all tiers up to and including <name>.
  --changed                Run tests affected by uncommitted changes (git diff).
  --spec <s1,s2,...>       Run tests covering the given spec sections.
  --pkg <p1,p2,...>        Run tests covering the given source packages.
  --dry-run                Resolve the selector and print what would run; do not
                           execute.
  --continue-on-failure    Do not stop at the first failing tier.
  --output <format>        json | junit | github-annotations | human | tap
                           (default: human).
  --verdict-file <path>    Where to write the JSON verdict (default:
                           tests/results/latest.json).
  --cached                 Use the cached container daemon if available.
  --no-infra               Skip infrastructure provisioning.

See TESTING.md for the full design and TESTING_DEPENDENCIES.md for setup.
`)
}
