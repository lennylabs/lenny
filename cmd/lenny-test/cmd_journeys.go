// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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
// is not a go test tier, it spends money on every run, and no --gate bound,
// --tier list, or merge gate reaches it. It runs at the area and release gates
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
	docker      string
	tokenFile   string
	stateDir    string
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
	fs.StringVar(&f.docker, "docker", "docker", "the docker executable that runs the agent container")
	fs.StringVar(&f.stateDir, "state-dir", "", "the directory of the pilot ledgers and the token file (default ~/.lenny-tmp/journeys); the lock stays at ~/.lenny-tmp/journeys.lock")
	fs.StringVar(&f.tokenFile, "claude-token-file", "", "the file holding the `claude setup-token` token, mode 0600 (default <state-dir>/claude-token)")
	fs.BoolVar(&f.skipAgent, "skip-agent", false, "create each cluster and evaluate the postconditions without starting the agent")
	fs.BoolVar(&f.probe, "probe", false, "check the agent container on a bare cluster with a trivial prompt under a $0.05 cap")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the journeys and the agent command line, and run nothing")
	fs.StringVar(&f.resultsDir, "results-dir", journeysResultsDir, "where the run records are written")
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
	if f.stateDir == "" {
		dir, err := journeys.DefaultStateDir()
		if err != nil {
			return f, err
		}
		f.stateDir = dir
	}
	if f.tokenFile == "" {
		f.tokenFile = filepath.Join(f.stateDir, journeys.TokenFileName)
	}
	return f, nil
}

// runJourneys runs the journeys tier: `lenny-test --tier journeys
// [--journey <name>] [--trials N]`.
func runJourneys(args []string) int {
	f, err := parseJourneysFlags(args)
	if err != nil {
		return journeysFail(err)
	}
	root := repoRoot()
	resultsDir := absUnder(root, f.resultsDir)
	agentCfg := journeys.AgentConfig{
		Docker: f.docker, Model: f.model, RunCapUSD: f.runCap, Rates: journeys.SonnetRates,
		UID: os.Getuid(), GID: os.Getgid(), Image: "<agent image>",
	}
	all, err := journeys.LoadDir(absUnder(root, f.scenarioDir))
	if err != nil {
		return journeysFail(err)
	}
	selected, err := journeys.Select(all, splitNonEmpty(f.journeys))
	if err != nil {
		return journeysFail(err)
	}
	if f.dryRun {
		return printJourneysDryRun(selected, agentCfg, f)
	}

	interruptOwned.Store(true)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !f.skipAgent || f.probe {
		tok, err := journeys.ReadTokenFile(f.tokenFile)
		if err != nil {
			return journeysFail(err)
		}
		agentCfg.Token = tok
	}
	lockPath, err := journeys.LockPath()
	if err != nil {
		return journeysFail(err)
	}
	lock, err := journeys.AcquireLock(lockPath)
	if err != nil {
		return journeysFail(err)
	}
	defer func() { _ = lock.Release() }()
	runID := journeys.NewRunID(time.Now())
	removed, err := journeys.CleanupLeftovers(ctx, journeys.ExecRunner, f.docker, runID)
	if err != nil {
		return journeysFail(fmt.Errorf("remove the containers earlier runs left: %w", err))
	}
	for _, r := range removed {
		fmt.Printf("lenny-test: journeys: removed the %s an earlier run left\n", r)
	}
	ledger, err := journeys.OpenLedger(journeys.LedgerPath(f.stateDir, f.pilot), f.pilot, f.pilotCap)
	if err != nil {
		return journeysFail(err)
	}
	reportPendingEntries(ledger)
	scratch, err := testtmp.MkdirTemp("lenny-journeys-")
	if err != nil {
		return journeysFail(err)
	}
	defer os.RemoveAll(scratch)
	if !f.skipAgent || f.probe {
		builder := &journeys.ImageBuilder{RepoRoot: root, Dir: scratch, Run: journeys.ExecRunner, Log: os.Stdout}
		fmt.Println("lenny-test: journeys: building the agent image")
		if agentCfg.Image, err = builder.Ensure(ctx); err != nil {
			return journeysFail(err)
		}
	}
	if f.probe {
		return runJourneysProbe(ctx, root, resultsDir, scratch, runID, agentCfg, ledger)
	}
	return executeJourneys(ctx, root, resultsDir, scratch, runID, selected, agentCfg, ledger, f)
}

func journeysFail(err error) int {
	fmt.Fprintf(os.Stderr, "lenny-test: journeys: %v\n", err)
	return 2
}

// reportPendingEntries names the ledger entries a crashed run left
// pending. They stay charged at the most their run could spend.
func reportPendingEntries(l *journeys.Ledger) {
	_, dead := l.PendingEntries()
	for _, e := range dead {
		fmt.Printf("lenny-test: journeys: run %s (%s, trial %d) ended without settling; it stays charged at $%.2f\n",
			e.Run, e.Journey, e.Trial, e.AgentUSD+e.InfraUSD)
	}
}

func executeJourneys(ctx context.Context, root, resultsDir, scratch, runID string, selected []*journeys.Scenario, agentCfg journeys.AgentConfig, ledger *journeys.Ledger, f journeysFlags) int {
	r := &journeys.Runner{
		RepoRoot: root, ResultsDir: resultsDir, ScratchDir: scratch, RunID: runID,
		Trials: f.trials, RunCapUSD: f.runCap, Ledger: ledger, Agent: agentCfg,
		Targets:   journeys.Targets{journeys.TargetKind: journeys.NewKindTarget(root, runID)},
		NewAccess: func(kubeconfig string) journeys.Access { return journeys.NewClusterAccess(kubeconfig) },
		Secrets:   []string{agentCfg.Token}, SkipAgent: f.skipAgent, Out: os.Stdout,
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

// runJourneysProbe runs the agent configuration check on a bare journey
// cluster; journeys.RunProbe records its cost in the pilot's ledger.
func runJourneysProbe(ctx context.Context, root, resultsDir, scratch, runID string, agentCfg journeys.AgentConfig, ledger *journeys.Ledger) int {
	if err := os.MkdirAll(resultsDir, 0o700); err != nil {
		return journeysFail(err)
	}
	// The probe's records are written through the redactor's record
	// files, like a trial's.
	red := journeys.NewRedactor(agentCfg.Token)
	transcriptPath := filepath.Join(resultsDir, "probe-"+runID+".jsonl")
	transcript, err := red.Create(transcriptPath)
	if err != nil {
		return journeysFail(err)
	}
	stderrPath := filepath.Join(resultsDir, "probe-"+runID+".stderr.log")
	stderr, err := red.Create(stderrPath)
	if err != nil {
		_ = transcript.Close()
		return journeysFail(err)
	}
	home, _ := os.UserHomeDir()
	target := journeys.NewKindTarget(root, runID)(filepath.Join(scratch, "probe", "target"))
	rep, code, probeErr := journeys.RunProbe(ctx, agentCfg, journeys.ProbeInput{
		Target: target, RunID: runID, Scratch: filepath.Join(scratch, "probe"), HostHome: home,
		HostContexts: journeys.HostContexts(filepath.Join(home, ".kube", "config")),
		Transcript:   transcript, Stderr: stderr, Redactor: red, Log: os.Stdout,
	}, ledger)
	// Closing writes each stream's held last line, so a failure there
	// leaves a record incomplete and fails the probe run.
	if err := closeProbeRecords(red, transcript, stderr, transcriptPath, stderrPath); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: probe records: %v\n", err)
		code = 2
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	fmt.Printf("Transcript: %s\nAgent standard error: %s\n", transcriptPath, stderrPath)
	if rep.Stop == journeys.StopAgentError {
		fmt.Printf("lenny-test: journeys: agent error (exit %d): %s\n", rep.ExitCode, rep.StopDetail)
	}
	if probeErr != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: journeys: probe: %v\n", probeErr)
	}
	return code
}

// closeProbeRecords closes the probe's record streams and reads the
// records back, as a run's records are (journeys.SweepRecords).
func closeProbeRecords(red *journeys.Redactor, transcript, stderr io.Closer, paths ...string) error {
	if err := errors.Join(transcript.Close(), stderr.Close()); err != nil {
		return err
	}
	rewritten, err := journeys.SweepRecords(red, paths...)
	if err != nil {
		return err
	}
	if len(rewritten) > 0 {
		return fmt.Errorf("records held credentials after they were written and were redacted again: %v", rewritten)
	}
	return nil
}

func printJourneysDryRun(selected []*journeys.Scenario, agentCfg journeys.AgentConfig, f journeysFlags) int {
	fmt.Printf("Journeys tier: %d journey(s), %d trial(s) each, run cap $%.2f, pilot %q cap $%.2f, state %s.\n\n",
		len(selected), f.trials, f.runCap, f.pilot, f.pilotCap, f.stateDir)
	for _, s := range selected {
		fmt.Printf("%s (%s, target %s, setup %s, limit %s, %d postcondition(s))\n",
			s.Name, s.Persona, s.TargetName(), s.Setup, s.Limit(), len(s.Postconditions))
		settings, err := journeys.Settings(s.Tools)
		if err != nil {
			return journeysFail(err)
		}
		run := journeys.AgentRun{
			Name: journeys.AgentImageRepo + "-<run>-" + s.Name + "-<trial>", RunID: "<run>", UpstreamNetwork: journeys.KindNetwork,
			DocsDir: "<trial>/docs", JourneyDir: "<trial>/journey", HomeDir: "<trial>/home", TimeLimit: s.Limit(),
			Settings: settings, SystemPrompt: journeys.SystemPrompt(s), Prompt: "<prompt.md>",
			Env: map[string]string{"KUBECONFIG": journeys.ContainerKubeconfig},
		}
		fmt.Printf("  %s\n", shellQuote(agentCfg.CommandLine(run)))
		fmt.Printf("  (the holder and proxy names above are hashed from the placeholder run name; a real run hashes its own run name)\n\n")
	}
	return 0
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
