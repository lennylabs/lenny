// SPDX-License-Identifier: MIT

// Command lenny-test is the single entry point for running Lenny's tests.
//
// It wraps `go test`, the docker-compose stack, the Kind cluster lifecycle, and
// k6 behind a uniform interface that produces a structured JSON verdict.
//
// The architecture, conventions, and selection model are documented in
// the project's testing documentation.
//
// This is the Phase 0 skeleton. It implements:
//
//   - Flag parsing and selector resolution against groups.yaml, the Go
//     import graph, the annotation index, and tests/non-go-inputs.yaml.
//   - The "list", "validate", "validate-diagnosis", and "infra" subcommands.
//   - A dry-run mode that prints the resolved selector and the tests it would
//     run, without executing them.
//   - The default run command, which currently dispatches the unit tier to
//     `go test` and writes a minimal verdict.
//
// Subsequent phases add real implementations of the higher tiers, the cached
// container daemon, the JUnit and GitHub-annotation emitters, and the
// PR-comment integration.
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
		if interruptOwned.Load() {
			// The running command handles the signal and cleans up
			// before it exits (the journeys tier deletes its cluster).
			fmt.Fprintf(os.Stderr, "\nlenny-test: interrupted (%s); tearing down before exit.\n", sig)
			return
		}
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

	// Keep every temp directory the run's processes create out of /tmp.
	// A failure leaves the inherited environment in place.
	if _, err := applyTestTempEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: warning: test temp root: %v\n", err)
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
	case "validate":
		os.Exit(runValidate(args[1:]))
	case "validate-diagnosis":
		os.Exit(runValidateDiagnosis(args[1:]))
	case "long-funcs":
		os.Exit(runLongFuncs(args[1:]))
	case "list":
		os.Exit(runList(args[1:]))
	case "infra":
		os.Exit(runInfra(args[1:]))
	case "run":
		if isJourneysTier(args[1:]) {
			os.Exit(runJourneys(args[1:]))
		}
		os.Exit(runRun(args[1:]))
	case "gate":
		os.Exit(runGate(args[1:]))
	case "secretscan":
		os.Exit(runSecretScanSubcommand(args[1:]))
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
		// Default to run. The journeys tier takes its own flags.
		if isJourneysTier(args) {
			os.Exit(runJourneys(args))
		}
		os.Exit(runRun(args))
	}
}

func printUsage() {
	fmt.Print(`lenny-test — Lenny's test selector and runner.

Usage:
  lenny-test [run] [flags]                 Run tests matching the given selectors.
  lenny-test validate [flags]              Check groups.yaml, the subsets, the flake
                                           budget, the parity matrix, the registers,
                                           tests/non-go-inputs.yaml, and the spec
                                           annotation of every component-and-up test.
  lenny-test validate-diagnosis [flags]    Verify every component-and-up test has a
                                           // diagnosis: comment.
  lenny-test long-funcs [flags]            Report functions whose body exceeds a
                                           line threshold (advisory; never fails).
  lenny-test gate [merge] [--base <ref>]   Run the merge gate in the gate container
                                           (tests/gate/Dockerfile) and write
                                           tests/results/gate-<run>.json.
  lenny-test secretscan [--root <dir>]     Run the static tier's secret scan on the
                                           committed tree at HEAD; exit 0 on PASS,
                                           1 on FAIL, 3 on UNVERIFIED.
  lenny-test list [flags]                  Print the tests that would run for a
                                           given selector, without executing them.
  lenny-test infra <up|down|status|prune>  Manage cached test infrastructure.
  lenny-test cached <status|ensure|       Manage the long-lived lenny-test-cached
                     endpoints|shutdown>   container daemon.
  lenny-test stress --test <name> [flags]  Run a single test N times to detect
                                            flakes.
  lenny-test watch [--tier <t> | --changed | --pattern <re>]
                                           Rerun tests on file changes.
  lenny-test coverage --go|--spec|        Report Go, spec-section, or diff
                       --diff <ref>        coverage (§5, §19).
  lenny-test mutation [--pkg <pat>]       Run scripts/mutation.sh and compare
                      [--threshold <f>]   against the per-package threshold.
  lenny-test baseline diff --before <a>   Diff two baseline JSON documents and
                         --after <b>    enforce the regression budget.
                            [--threshold <f>]
  lenny-test report --dir <path>          Aggregate verdict JSONs from <path>
                    [--output             into a single roll-up.
                     human|markdown|json]
  lenny-test comment --verdict <path>     Render a verdict as a Markdown PR
                     [--output <file>]    comment.
  lenny-test conformance --image <ref>     Run the conformance battery
                          --level <basic|standard|full>
                                           against a third-party runtime image.
  lenny-test preflight --cluster <path>    Check a target cluster's readiness
                                           to host Lenny (wraps preflight.sh).
  lenny-test version                       Print the version.
  lenny-test help                          This message.

Run flags:
  --group <name>           A named group from tests/groups.yaml (e.g., pr, pr-fast,
                           nightly, pre-release, phase-3.5-gate).
  --tier <t1,t2,...>       Tiers by name (static, unit, component, contract,
                           integration, e2e_kind, e2e_cloud, load_local,
                           load_kind, chaos, security, conformance, docs,
                           load_cloud). Tier numbers are not accepted. Alone,
                           runs each named tier whole. With --changed, --since,
                           --spec, or --pkg, runs that selection restricted to
                           the named tiers (unit keeps the selected packages).
  --tier journeys          The persona journey tier, which takes its own flags:
                           [--journey <name>] [--trials N] [--pilot <name>]
                           [--run-cap-usd 6] [--pilot-cap-usd 54]
                           [--skip-agent] [--probe] [--dry-run]. It creates a
                           Kind cluster per trial and runs a Claude Code agent
                           in a container on the token in
                           ~/.lenny-tmp/journeys/claude-token, so it costs
                           money; no --gate bound or --tier list reaches it.
  --gate <merge|area|release>
                           Bound the selection at the tiers of that gate.
                           Alone, runs the gate's tiers whole.
  --changed                Run tests affected by uncommitted changes (git diff).
                           Without --tier or --gate, --changed and --since run
                           only the static, unit, component, and contract
                           tiers they reach, which start no compose stack or
                           cluster.
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

`)
}
