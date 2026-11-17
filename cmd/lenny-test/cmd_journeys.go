// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/journeys"
	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// tierJourneys names the persona journey tier. It is not in allTiers: it
// is not a go test tier, it spends money on every run, and --max-tier and
// the merge gate never reach it. It runs at the area and release gates
// (TESTING.md, "Persona journeys").
const tierJourneys = "journeys"

// journeysDir holds the journey scenarios, and journeysResultsDir their
// records, which are not committed (decision 9 of change 0157).
const (
	journeysDir        = "tests/journeys"
	journeysResultsDir = "tests/results/journeys"
)

// interruptOwned is set while a command handles SIGINT and SIGTERM
// itself. The journeys tier sets it so an interrupt cancels the run's
// context and the trial in flight still deletes its cluster, instead of
// main's handler exiting the process with the cluster running.
var interruptOwned atomic.Bool

// isJourneysTier reports whether the run arguments select the journeys
// tier, which takes its own flags.
func isJourneysTier(args []string) bool {
	for i, a := range args {
		if a == "--tier="+tierJourneys || a == "-tier="+tierJourneys {
			return true
		}
		if (a == "--tier" || a == "-tier") && i+1 < len(args) && args[i+1] == tierJourneys {
			return true
		}
	}
	return false
}

type journeysFlags struct {
	journeys    string
	trials      int
	runCap      float64
	pilotCap    float64
	pilot       string
	model       string
	claudeBin   string
	skipAgent   bool
	probe       bool
	dryRun      bool
	resultsDir  string
	scenarioDir string
}

func parseJourneysFlags(args []string) (journeysFlags, error) {
	var f journeysFlags
	fs := flag.NewFlagSet("journeys", flag.ContinueOnError)
	fs.String("tier", tierJourneys, "the journeys tier")
	fs.StringVar(&f.journeys, "journey", "", "comma-separated journey names (default: every journey under tests/journeys)")
	fs.IntVar(&f.trials, "trials", journeys.DefaultTrials, "trials per journey")
	fs.Float64Var(&f.runCap, "run-cap-usd", journeys.DefaultRunCapUSD, "agent cost cap of one run, in dollars")
	fs.Float64Var(&f.pilotCap, "pilot-cap-usd", journeys.DefaultPilotCapUSD, "cost cap of the pilot, in dollars, recorded in the pilot's ledger")
	fs.StringVar(&f.pilot, "pilot", "pilot", "the pilot whose ledger the run records its cost in")
	fs.StringVar(&f.model, "model", journeys.DefaultModel, "the journey agent's model")
	fs.StringVar(&f.claudeBin, "claude", "claude", "the Claude Code executable")
	fs.BoolVar(&f.skipAgent, "skip-agent", false, "create each cluster and evaluate the postconditions without starting the agent")
	fs.BoolVar(&f.probe, "probe", false, "check the agent configuration with a trivial prompt under a $0.05 cap, and create no cluster")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the journeys and the agent command line, and run nothing")
	fs.StringVar(&f.resultsDir, "results-dir", journeysResultsDir, "where the run records and the ledger are written")
	fs.StringVar(&f.scenarioDir, "dir", journeysDir, "the directory of journey scenarios")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if f.trials < 1 {
		return f, fmt.Errorf("--trials %d is below 1", f.trials)
	}
	if f.runCap <= 0 || f.runCap > f.pilotCap {
		return f, fmt.Errorf("--run-cap-usd $%.2f must be positive and at most --pilot-cap-usd $%.2f", f.runCap, f.pilotCap)
	}
	return f, nil
}

// runJourneys runs the journeys tier: `lenny-test --tier journeys
// [--journey <name>] [--trials N]`.
func runJourneys(args []string) int {
	f, err := parseJourneysFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	root := repoRoot()
	resultsDir := absUnder(root, f.resultsDir)
	agentCfg := journeys.AgentConfig{Binary: f.claudeBin, Model: f.model, RunCapUSD: f.runCap, Rates: journeys.SonnetRates}

	interruptOwned.Store(true)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if f.probe {
		return runJourneysProbe(ctx, agentCfg, resultsDir)
	}
	all, err := journeys.LoadDir(absUnder(root, f.scenarioDir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	selected, err := journeys.Select(all, splitNonEmpty(f.journeys))
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	if f.dryRun {
		return printJourneysDryRun(selected, agentCfg, f)
	}
	return executeJourneys(ctx, root, resultsDir, selected, agentCfg, f)
}

func executeJourneys(ctx context.Context, root, resultsDir string, selected []*journeys.Scenario, agentCfg journeys.AgentConfig, f journeysFlags) int {
	lock, err := journeys.AcquireLock(resultsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	defer func() { _ = lock.Release() }()
	ledger, err := journeys.OpenLedger(journeys.LedgerPath(resultsDir, f.pilot), f.pilot, f.pilotCap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	scratch, err := testtmp.MkdirTemp("lenny-journeys-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	defer os.RemoveAll(scratch)
	binDir := filepath.Join(scratch, "bin")
	if err := buildLennyCtl(ctx, root, binDir); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: build lenny-ctl: %v\n", err)
		return 2
	}
	runID := time.Now().UTC().Format("20060102T150405Z")
	r := &journeys.Runner{
		RepoRoot: root, ResultsDir: resultsDir, ScratchDir: scratch, RunID: runID,
		Trials: f.trials, RunCapUSD: f.runCap, Ledger: ledger, Agent: agentCfg,
		Targets:   journeys.Targets{journeys.TargetKind: journeys.NewKindTarget(root)},
		NewAccess: func(kubeconfig string) journeys.Access { return journeys.NewClusterAccess(kubeconfig) },
		BinDir:    binDir, HostEnv: os.Environ(), SkipAgent: f.skipAgent, Out: os.Stdout,
	}
	fmt.Printf("lenny-test: journeys run %s, %d journey(s), %d trial(s) each, pilot %q at $%.4f of $%.2f\n",
		runID, len(selected), f.trials, f.pilot, ledger.Spent(), ledger.CapUSD)
	sum, runErr := r.Run(ctx, selected)
	fmt.Print("\n" + journeys.SummaryMarkdown(sum))
	fmt.Printf("\nRecords: %s\n", filepath.Join(resultsDir, runID))
	return journeysExitCode(sum, runErr)
}

// journeysExitCode is 0 when every journey passed, 1 when one failed or
// was unstable, 2 when the run stopped early or a journey was
// inconclusive. During the pilot a failing journey blocks nothing
// (decision 2); the exit code reports it.
func journeysExitCode(sum *journeys.RunSummary, runErr error) int {
	if runErr != nil || sum == nil {
		return 2
	}
	code := 0
	for _, j := range sum.Journeys {
		switch j.Verdict {
		case journeys.JourneyFail, journeys.JourneyUnstable:
			code = 1
		case journeys.JourneyInconclusive:
			if code == 0 {
				code = 2
			}
		}
	}
	return code
}

func runJourneysProbe(ctx context.Context, agentCfg journeys.AgentConfig, resultsDir string) int {
	scratch, err := testtmp.MkdirTemp("lenny-journeys-probe-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	defer os.RemoveAll(scratch)
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
		return 2
	}
	transcript := filepath.Join(resultsDir, "probe-"+time.Now().UTC().Format("20060102T150405Z")+".jsonl")
	rep, err := journeys.ProbeAgent(ctx, agentCfg, scratch, "", os.Environ(), transcript)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: probe: %v\n", err)
		return 2
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	fmt.Printf("Transcript: %s\n", transcript)
	if !rep.Passed {
		return 1
	}
	return 0
}

func printJourneysDryRun(selected []*journeys.Scenario, agentCfg journeys.AgentConfig, f journeysFlags) int {
	fmt.Printf("Journeys tier: %d journey(s), %d trial(s) each, run cap $%.2f, pilot %q cap $%.2f.\n\n",
		len(selected), f.trials, f.runCap, f.pilot, f.pilotCap)
	for _, s := range selected {
		fmt.Printf("%s (%s, target %s, setup %s, limit %s, %d postcondition(s))\n",
			s.Name, s.Persona, s.TargetName(), s.Setup, s.Limit(), len(s.Postconditions))
		run := journeys.AgentRun{
			Workspace: "<workspace>", SettingsPath: "<trial>/settings.json",
			SystemPrompt: journeys.SystemPrompt(s), Prompt: "<prompt.md>",
		}
		fmt.Printf("  %s\n\n", shellQuote(agentCfg.CommandLine(run)))
	}
	return 0
}

// buildLennyCtl builds the lenny-ctl the agent runs into binDir.
func buildLennyCtl(ctx context.Context, root, binDir string) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, resolveGoBin("go"), "build", "-o", filepath.Join(binDir, "lenny-ctl"), "./cmd/lenny-ctl")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func absUnder(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func shellQuote(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"$`\\*?[]{}()<>|&;#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
