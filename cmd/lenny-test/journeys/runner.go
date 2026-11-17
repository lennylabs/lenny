// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// DefaultTrials is the number of trials per journey (decision 3 of
// change 0157).
const DefaultTrials = 3

// teardownTimeout bounds the deletion and its verification. It runs on a
// context detached from the run's, so an interrupted run still tears its
// cluster down.
const teardownTimeout = 15 * time.Minute

// Access is what the runner needs from a created cluster: the probes for
// the postconditions and the credentials for the agent.
type Access interface {
	Probe
	AgentCredentials(ctx context.Context, names []string, scratch string) (map[string]string, func(), error)
	Prepare(ctx context.Context, step string) error
	Close()
}

// Runner runs journeys, each for a number of trials, and writes their
// records under ResultsDir/RunID.
type Runner struct {
	// RepoRoot is the checkout the documentation and the chart are
	// copied from.
	RepoRoot string
	// ResultsDir is tests/results/journeys.
	ResultsDir string
	// ScratchDir holds each trial's workspace, outside the repository,
	// so Claude Code finds no project instructions above the agent's
	// working directory.
	ScratchDir string
	// RunID names this run's record directory.
	RunID string
	// Trials is the number of trials per journey.
	Trials int
	// RunCapUSD is the agent's per-run cap.
	RunCapUSD float64
	// Ledger is the pilot's ledger.
	Ledger *Ledger
	// Agent runs the journey agent.
	Agent Agent
	// Targets builds a cluster target by name.
	Targets Targets
	// NewAccess reaches a created cluster through its kubeconfig.
	NewAccess func(kubeconfig string) Access
	// BinDir holds the lenny-ctl the agent runs.
	BinDir string
	// HostEnv is the harness's environment, from which BuildEnv picks
	// the variables the agent inherits.
	HostEnv []string
	// SkipAgent creates the cluster and evaluates the postconditions
	// without starting the agent, to check a scenario's setup and
	// probes at no model cost.
	SkipAgent bool
	// Out receives progress lines.
	Out io.Writer
	// Now and Nonce are injected for tests.
	Now   func() time.Time
	Nonce func() string
	// Evaluator settings for the postconditions; zero values use the
	// defaults.
	Interval time.Duration
	Sleep    func(context.Context, time.Duration) error
}

// TrialRecord is the record of one trial, written as result.json.
type TrialRecord struct {
	Run            string                `json:"run"`
	Journey        string                `json:"journey"`
	Persona        string                `json:"persona"`
	Trial          int                   `json:"trial"`
	Target         string                `json:"target"`
	Setup          string                `json:"setup"`
	Nonce          string                `json:"nonce"`
	StartedAt      time.Time             `json:"startedAt"`
	FinishedAt     time.Time             `json:"finishedAt"`
	Command        []string              `json:"command,omitempty"`
	AgentSkipped   bool                  `json:"agentSkipped,omitempty"`
	Agent          AgentResult           `json:"agent"`
	InfraCostUSD   float64               `json:"infraCostUSD"`
	Postconditions []PostconditionResult `json:"postconditions"`
	Teardown       TeardownRecord        `json:"teardown"`
	Verdict        Verdict               `json:"verdict"`
	Dir            string                `json:"dir"`
}

// TeardownRecord says whether the cluster was deleted and the deletion
// verified.
type TeardownRecord struct {
	Deleted  bool   `json:"deleted"`
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`
}

// JourneySummary combines a journey's trials.
type JourneySummary struct {
	Journey  string   `json:"journey"`
	Persona  string   `json:"persona"`
	Outcomes []string `json:"outcomes"`
	Verdict  string   `json:"verdict"`
	AgentUSD float64  `json:"agentUSD"`
	InfraUSD float64  `json:"infraUSD"`
}

// RunSummary is the record of one run, written as summary.json.
type RunSummary struct {
	Run              string           `json:"run"`
	Pilot            string           `json:"pilot"`
	StartedAt        time.Time        `json:"startedAt"`
	FinishedAt       time.Time        `json:"finishedAt"`
	TrialsPerJourney int              `json:"trialsPerJourney"`
	RunCapUSD        float64          `json:"runCapUSD"`
	PilotCapUSD      float64          `json:"pilotCapUSD"`
	PilotSpentUSD    float64          `json:"pilotSpentUSD"`
	Journeys         []JourneySummary `json:"journeys"`
	Trials           []TrialRecord    `json:"trials"`
	Stopped          string           `json:"stopped,omitempty"`
}

// Run runs every trial of every scenario in order. It stops early when
// the ledger refuses a trial, when a teardown cannot be verified, or when
// ctx is cancelled, and records why in the summary. The summary is
// written in every case.
func (r *Runner) Run(ctx context.Context, scenarios []*Scenario) (*RunSummary, error) {
	sum := &RunSummary{
		Run: r.RunID, Pilot: r.Ledger.Pilot, StartedAt: r.now(), TrialsPerJourney: r.Trials,
		RunCapUSD: r.RunCapUSD, PilotCapUSD: r.Ledger.CapUSD,
	}
	runErr := r.runAll(ctx, scenarios, sum)
	sum.FinishedAt = r.now()
	sum.PilotSpentUSD = r.Ledger.Spent()
	sum.Journeys = summarizeJourneys(scenarios, sum.Trials)
	if err := r.writeSummary(sum); err != nil {
		return sum, errors.Join(runErr, err)
	}
	return sum, runErr
}

func (r *Runner) runAll(ctx context.Context, scenarios []*Scenario, sum *RunSummary) error {
	for _, s := range scenarios {
		for n := 1; n <= r.Trials; n++ {
			if ctx.Err() != nil {
				sum.Stopped = "interrupted"
				return ctx.Err()
			}
			scratch := filepath.Join(r.ScratchDir, s.Name, fmt.Sprintf("trial-%d", n))
			target, err := r.Targets.New(s.TargetName(), filepath.Join(scratch, "target"))
			if err != nil {
				sum.Stopped = err.Error()
				return err
			}
			if err := r.Ledger.CheckStart(r.maxAgentUSD() + target.MaxCostUSD()); err != nil {
				sum.Stopped = err.Error()
				return err
			}
			r.logf("==> journey %s, trial %d of %d", s.Name, n, r.Trials)
			rec := r.trial(ctx, s, n, target, scratch)
			sum.Trials = append(sum.Trials, rec)
			r.logf("    verdict %s: %s (agent $%.4f)", rec.Verdict.Outcome, rec.Verdict.Reason, rec.Agent.CostUSD)
			if err := r.Ledger.Record(LedgerEntry{
				Run: r.RunID, Journey: s.Name, Trial: n, Target: target.Name(),
				AgentUSD: rec.Agent.CostUSD, AgentCostReported: rec.Agent.CostReported,
				InfraUSD: rec.InfraCostUSD, Verdict: rec.Verdict.Outcome, At: r.now(),
			}); err != nil {
				sum.Stopped = "ledger: " + err.Error()
				return err
			}
			if !rec.Teardown.Verified {
				sum.Stopped = "teardown of " + s.Name + " could not be verified: " + rec.Teardown.Error
				return errors.New(sum.Stopped)
			}
		}
	}
	return nil
}

// maxAgentUSD is what the ledger reserves for the agent: the cap, or
// nothing when the agent does not run.
func (r *Runner) maxAgentUSD() float64 {
	if r.SkipAgent {
		return 0
	}
	return r.RunCapUSD
}

// trial runs one trial. The cluster is torn down on every path out of
// it, including a panic, and the teardown's outcome is part of the
// verdict.
func (r *Runner) trial(ctx context.Context, s *Scenario, n int, target Target, scratch string) (rec TrialRecord) {
	rec = TrialRecord{
		Run: r.RunID, Journey: s.Name, Persona: s.Persona, Trial: n, Target: target.Name(),
		Setup: s.Setup, Nonce: r.nonce(), StartedAt: r.now(), AgentSkipped: r.SkipAgent,
		Dir: filepath.Join(r.ResultsDir, r.RunID, s.Name, fmt.Sprintf("trial-%d", n)),
	}
	tornDown := false
	defer func() {
		if !tornDown {
			rec.Teardown = r.teardown(target, rec.Dir)
		}
	}()

	infraErr := r.prepareDirs(rec.Dir, scratch)
	if infraErr == nil {
		infraErr = r.provision(ctx, target, s, rec.Dir)
	}
	if infraErr == nil {
		infraErr = r.runAgentAndCheck(ctx, s, target, scratch, &rec)
	}
	rec.Teardown = r.teardown(target, rec.Dir)
	tornDown = true
	rec.InfraCostUSD = target.CostUSD()
	rec.FinishedAt = r.now()
	if infraErr == nil && !rec.Teardown.Verified {
		infraErr = errors.New("teardown: " + rec.Teardown.Error)
	}
	rec.Verdict = Decide(infraErr, rec.Agent.Stop, rec.Agent.StopDetail, rec.Postconditions)
	if err := writeJSON(filepath.Join(rec.Dir, "result.json"), rec); err != nil {
		r.logf("    cannot write the trial record: %v", err)
	}
	return rec
}

func (r *Runner) prepareDirs(dir, scratch string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(scratch, 0o755)
}

func (r *Runner) provision(ctx context.Context, target Target, s *Scenario, dir string) error {
	log, err := os.Create(filepath.Join(dir, "provision.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	r.logf("    creating the %s cluster (setup %s)", target.Name(), s.Setup)
	if err := target.Create(ctx, s.Setup, log); err != nil {
		return fmt.Errorf("create the %s cluster: %w (see provision.log)", target.Name(), err)
	}
	return nil
}

// runAgentAndCheck runs the agent against the created cluster and
// evaluates the postconditions. It returns an infrastructure error only
// for failures outside the agent and the system under test.
func (r *Runner) runAgentAndCheck(ctx context.Context, s *Scenario, target Target, scratch string, rec *TrialRecord) error {
	access := r.NewAccess(target.Kubeconfig())
	defer access.Close()
	vars, err := r.trialVars(s, scratch, rec.Nonce)
	if err != nil {
		return err
	}
	for _, p := range s.Prepare {
		r.logf("    preparing %s", p)
		if err := access.Prepare(ctx, p); err != nil {
			return fmt.Errorf("prepare %s: %w", p, err)
		}
	}
	if r.SkipAgent {
		rec.Agent = AgentResult{Stop: StopFinished, StopDetail: "agent skipped"}
	} else if err := r.runAgent(ctx, s, access, scratch, vars, rec); err != nil {
		return err
	}
	ev := &Evaluator{Probe: access, Vars: vars, Interval: r.Interval, Sleep: r.Sleep}
	r.logf("    evaluating %d postcondition(s)", len(s.Postconditions))
	rec.Postconditions = ev.EvaluateAll(ctx, s.Postconditions)
	return nil
}

func (r *Runner) runAgent(ctx context.Context, s *Scenario, access Access, scratch string, vars map[string]string, rec *TrialRecord) error {
	workspace := filepath.Join(scratch, "workspace")
	if err := copyTree(filepath.Join(r.RepoRoot, "docs"), filepath.Join(workspace, "docs")); err != nil {
		return fmt.Errorf("copy the documentation: %w", err)
	}
	settings, err := Settings(workspace, s.Tools)
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(scratch, "settings.json")
	if err := os.WriteFile(settingsPath, settings, 0o600); err != nil {
		return err
	}
	creds, stopCreds, err := access.AgentCredentials(ctx, s.Credentials, scratch)
	defer stopCreds()
	if err != nil {
		return fmt.Errorf("agent credentials: %w", err)
	}
	transcript, err := os.Create(filepath.Join(rec.Dir, "transcript.jsonl"))
	if err != nil {
		return err
	}
	defer transcript.Close()
	stderr, err := os.Create(filepath.Join(rec.Dir, "agent-stderr.log"))
	if err != nil {
		return err
	}
	defer stderr.Close()
	run := AgentRun{
		Workspace: workspace, SettingsPath: settingsPath,
		SystemPrompt: SystemPrompt(s), Prompt: UserPrompt(s, vars),
		Env: BuildEnv(r.HostEnv, r.BinDir, creds), TimeLimit: s.Limit(),
		Transcript: transcript, Stderr: stderr,
	}
	rec.Command = r.Agent.CommandLine(run)
	prompt := "# System prompt (appended)\n\n" + run.SystemPrompt + "\n\n# Prompt\n\n" + run.Prompt
	if err := os.WriteFile(filepath.Join(rec.Dir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rec.Dir, "settings.json"), settings, 0o644); err != nil {
		return err
	}
	r.logf("    running the agent (cap $%.2f, limit %s)", r.RunCapUSD, s.Limit())
	res, err := r.Agent.Run(ctx, run)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	rec.Agent = res
	return nil
}

func (r *Runner) trialVars(s *Scenario, scratch, nonce string) (map[string]string, error) {
	vars := map[string]string{
		"nonce":    nonce,
		"cluster":  KindClusterName,
		"imageTag": KindAliasTag,
		"journey":  s.Name,
	}
	if s.Setup == SetupPrerequisites {
		chart := filepath.Join(scratch, "chart", "lenny")
		if err := copyTree(filepath.Join(r.RepoRoot, "charts", "lenny"), chart); err != nil {
			return nil, fmt.Errorf("copy the chart: %w", err)
		}
		vars["chart"] = chart
	}
	return vars, nil
}

// teardown deletes the cluster and verifies the deletion on a context
// detached from the run's.
func (r *Runner) teardown(target Target, dir string) TeardownRecord {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	var log io.Writer = io.Discard
	if f, err := os.OpenFile(filepath.Join(dir, "teardown.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		defer f.Close()
		log = f
	}
	r.logf("    deleting the %s cluster", target.Name())
	var rec TeardownRecord
	var errs []error
	if err := target.Delete(ctx, log); err != nil {
		errs = append(errs, fmt.Errorf("delete: %w", err))
	} else {
		rec.Deleted = true
	}
	if err := target.VerifyDeleted(ctx); err != nil {
		errs = append(errs, fmt.Errorf("verify: %w", err))
	} else {
		rec.Verified = true
	}
	if err := errors.Join(errs...); err != nil {
		rec.Error = err.Error()
		fmt.Fprintln(log, rec.Error)
	}
	return rec
}

func summarizeJourneys(scenarios []*Scenario, trials []TrialRecord) []JourneySummary {
	out := make([]JourneySummary, 0, len(scenarios))
	for _, s := range scenarios {
		js := JourneySummary{Journey: s.Name, Persona: s.Persona}
		for _, t := range trials {
			if t.Journey != s.Name {
				continue
			}
			js.Outcomes = append(js.Outcomes, t.Verdict.Outcome)
			js.AgentUSD += t.Agent.CostUSD
			js.InfraUSD += t.InfraCostUSD
		}
		if len(js.Outcomes) == 0 {
			continue
		}
		js.Verdict = Summarize(js.Outcomes)
		out = append(out, js)
	}
	return out
}

func (r *Runner) writeSummary(sum *RunSummary) error {
	dir := filepath.Join(r.ResultsDir, r.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "summary.json"), sum); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(SummaryMarkdown(sum)), 0o644)
}

// SummaryMarkdown renders the run summary as the table a step's digest
// carries (decision 9 of change 0157).
func SummaryMarkdown(sum *RunSummary) string {
	s := fmt.Sprintf("Journey run `%s` (pilot `%s`), %d trial(s) per journey, run cap $%.2f, pilot cap $%.2f, pilot spent $%.4f.\n\n",
		sum.Run, sum.Pilot, sum.TrialsPerJourney, sum.RunCapUSD, sum.PilotCapUSD, sum.PilotSpentUSD)
	s += "| Journey | Persona | Trials | Verdict | Agent cost | Infrastructure cost |\n| :-- | :-- | :-- | :-- | --: | --: |\n"
	for _, j := range sum.Journeys {
		s += fmt.Sprintf("| %s | %s | %v | %s | $%.4f | $%.4f |\n", j.Journey, j.Persona, j.Outcomes, j.Verdict, j.AgentUSD, j.InfraUSD)
	}
	if sum.Stopped != "" {
		s += "\nThe run stopped early: " + sum.Stopped + "\n"
	}
	return s
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *Runner) nonce() string {
	if r.Nonce != nil {
		return r.Nonce()
	}
	return NewNonce()
}

// NewNonce returns eight random hex digits. A journey's goal and its
// postconditions name the nonce, so a postcondition matches what this
// trial's agent did and never a leftover.
func NewNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

func (r *Runner) logf(format string, args ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format+"\n", args...)
	}
}

// copyTree copies the regular files and directories under src to dst.
// Symbolic links are skipped, so a link cannot carry the agent's read
// access outside the copied documentation.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return nil
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
