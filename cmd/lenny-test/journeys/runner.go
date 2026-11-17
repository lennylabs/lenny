// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
// the postconditions, the preparation steps, and the admin token for the
// agent's credentials.
type Access interface {
	Probe
	AdminTokenReader
	Prepare(ctx context.Context, step string) error
	Close()
}

// Runner runs journeys, each for a number of trials, and writes their
// records under ResultsDir/RunID.
type Runner struct {
	// RepoRoot is the checkout the documentation and the chart are
	// copied from.
	RepoRoot string
	// ResultsDir is tests/results/journeys. Records are written 0600 in
	// 0700 directories.
	ResultsDir string
	// ScratchDir holds each trial's documentation copy, kubeconfigs, and
	// agent home directory, outside the repository.
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
	// Secrets are values no record may show whatever the trial, such as
	// the Claude token.
	Secrets []string
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
			entry := LedgerEntry{ID: EntryID(r.RunID, s.Name, n), Run: r.RunID, Journey: s.Name, Trial: n, Target: target.Name(), At: r.now()}
			if err := r.Ledger.Reserve(entry, r.maxAgentUSD(), target.MaxCostUSD()); err != nil {
				sum.Stopped = err.Error()
				return err
			}
			r.logf("==> journey %s, trial %d of %d", s.Name, n, r.Trials)
			rec := r.trial(ctx, s, n, target, scratch)
			sum.Trials = append(sum.Trials, rec)
			r.logf("    verdict %s: %s (agent $%.4f)", rec.Verdict.Outcome, rec.Verdict.Reason, rec.Agent.CostUSD)
			if rec.Agent.Stop == StopAgentError {
				r.logf("    agent error (exit %d): %s", rec.Agent.ExitCode, rec.Agent.StopDetail)
			}
			entry.AgentUSD, entry.AgentCostReported = rec.Agent.CostUSD, rec.Agent.CostReported
			entry.InfraUSD, entry.Verdict, entry.At = rec.InfraCostUSD, rec.Verdict.Outcome, r.now()
			if err := r.Ledger.Settle(entry); err != nil {
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
// verdict. Every string the record carries is redacted before it is
// written.
func (r *Runner) trial(ctx context.Context, s *Scenario, n int, target Target, scratch string) (rec TrialRecord) {
	rec = TrialRecord{
		Run: r.RunID, Journey: s.Name, Persona: s.Persona, Trial: n, Target: target.Name(),
		Setup: s.Setup, Nonce: r.nonce(), StartedAt: r.now(), AgentSkipped: r.SkipAgent,
		Dir: filepath.Join(r.ResultsDir, r.RunID, s.Name, fmt.Sprintf("trial-%d", n)),
	}
	red := NewRedactor(r.Secrets...)
	tornDown := false
	defer func() {
		if !tornDown {
			rec.Teardown = r.teardown(target, rec.Dir, red)
		}
	}()

	infraErr := r.prepareDirs(rec.Dir, scratch)
	if infraErr == nil {
		infraErr = r.provision(ctx, target, s, rec.Dir, red)
	}
	if infraErr == nil {
		infraErr = r.runAgentAndCheck(ctx, s, target, scratch, &rec, red)
	}
	rec.Teardown = r.teardown(target, rec.Dir, red)
	tornDown = true
	rec.InfraCostUSD = target.CostUSD()
	rec.FinishedAt = r.now()
	if infraErr == nil && !rec.Teardown.Verified {
		infraErr = errors.New("teardown: " + rec.Teardown.Error)
	}
	rec.Verdict = Decide(infraErr, rec.Agent.Stop, rec.Agent.StopDetail, rec.Postconditions)
	redactRecord(&rec, red)
	if err := writeJSON(filepath.Join(rec.Dir, "result.json"), rec); err != nil {
		r.logf("    cannot write the trial record: %v", err)
	}
	return rec
}

// redactRecord removes secrets from every free-text field of a record.
func redactRecord(rec *TrialRecord, red *Redactor) {
	rec.Agent.FinalText = red.String(rec.Agent.FinalText)
	rec.Agent.StopDetail = red.String(rec.Agent.StopDetail)
	rec.Verdict.Reason = red.String(rec.Verdict.Reason)
	rec.Teardown.Error = red.String(rec.Teardown.Error)
	for i := range rec.Postconditions {
		rec.Postconditions[i].Detail = red.String(rec.Postconditions[i].Detail)
	}
	for i := range rec.Command {
		rec.Command[i] = red.String(rec.Command[i])
	}
}

func (r *Runner) prepareDirs(dir, scratch string) error {
	for _, d := range []string{r.ResultsDir, filepath.Join(r.ResultsDir, r.RunID), filepath.Dir(dir), dir, scratch} {
		if err := mkdirPrivate(d); err != nil {
			return err
		}
	}
	return nil
}

// mkdirPrivate creates dir and its parents and sets dir to 0700.
func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// createPrivate creates a record file at 0600.
func createPrivate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

func writePrivate(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// provision creates the cluster. Its output is held in memory and
// written to provision.log redacted, with the new cluster's kubeconfig
// secrets added to the redactor first, because the output streams arrive
// in chunks a secret could straddle.
func (r *Runner) provision(ctx context.Context, target Target, s *Scenario, dir string, red *Redactor) error {
	var log lockedBuffer
	r.logf("    creating the %s cluster (setup %s)", target.Name(), s.Setup)
	createErr := target.Create(ctx, s.Setup, &log)
	red.Add(KubeconfigSecrets(target.Kubeconfig())...)
	if err := writePrivate(filepath.Join(dir, "provision.log"), red.Redact(log.Bytes())); err != nil && createErr == nil {
		return err
	}
	if createErr != nil {
		return fmt.Errorf("create the %s cluster: %w (see provision.log)", target.Name(), createErr)
	}
	return nil
}

// lockedBuffer is a bytes.Buffer safe for the two output streams of a
// command.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// Bytes returns the buffered output.
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// runAgentAndCheck runs the agent against the created cluster and
// evaluates the postconditions. It returns an infrastructure error only
// for failures outside the agent and the system under test.
func (r *Runner) runAgentAndCheck(ctx context.Context, s *Scenario, target Target, scratch string, rec *TrialRecord, red *Redactor) error {
	access := r.NewAccess(target.Kubeconfig())
	defer access.Close()
	vars := r.trialVars(s, target, rec.Nonce)
	for _, p := range s.Prepare {
		r.logf("    preparing %s", p)
		if err := access.Prepare(ctx, p); err != nil {
			return fmt.Errorf("prepare %s: %w", p, err)
		}
	}
	if r.SkipAgent {
		rec.Agent = AgentResult{Stop: StopFinished, StopDetail: "agent skipped"}
	} else if err := r.runAgent(ctx, s, target, access, scratch, vars, rec, red); err != nil {
		return err
	}
	ev := &Evaluator{Probe: access, Vars: vars, Interval: r.Interval, Sleep: r.Sleep}
	r.logf("    evaluating %d postcondition(s)", len(s.Postconditions))
	rec.Postconditions = ev.EvaluateAll(ctx, s.Postconditions)
	return nil
}

// agentDirs are the host directories of one agent sandbox: the
// documentation copy, the agent's read-only kubeconfig mount, the
// holder's read-only harness kubeconfig mount, and the agent's home
// directory.
type agentDirs struct{ docs, journey, harness, home string }

func (r *Runner) prepareAgentDirs(s *Scenario, scratch string) (agentDirs, error) {
	d := agentDirs{
		docs:    filepath.Join(scratch, "docs"),
		journey: filepath.Join(scratch, "journey"),
		harness: filepath.Join(scratch, "harness"),
		home:    filepath.Join(scratch, "home"),
	}
	for _, dir := range []string{d.journey, d.harness, d.home} {
		if err := mkdirPrivate(dir); err != nil {
			return d, err
		}
	}
	if err := copyTree(filepath.Join(r.RepoRoot, "docs"), d.docs); err != nil {
		return d, fmt.Errorf("copy the documentation: %w", err)
	}
	if s.Setup == SetupPrerequisites {
		if err := copyTree(filepath.Join(r.RepoRoot, "charts", "lenny"), filepath.Join(d.home, chartDirName)); err != nil {
			return d, fmt.Errorf("copy the chart: %w", err)
		}
	}
	return d, nil
}

// chartDirName is where the operator's copy of the chart sits in the
// agent's home directory.
const chartDirName = "lenny-chart"

func (r *Runner) runAgent(ctx context.Context, s *Scenario, target Target, access Access, scratch string, vars map[string]string, rec *TrialRecord, red *Redactor) error {
	dirs, err := r.prepareAgentDirs(s, scratch)
	if err != nil {
		return err
	}
	ep, err := target.AgentEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("agent endpoint: %w", err)
	}
	grant, err := GrantCredentials(ctx, s.Credentials, ep, dirs.journey, dirs.harness, access)
	if err != nil {
		return fmt.Errorf("agent credentials: %w", err)
	}
	red.Add(grant.Secrets...)
	for _, d := range []string{dirs.journey, dirs.harness} {
		if err := LockDir(d); err != nil {
			return err
		}
		defer func(d string) { _ = UnlockDir(d) }(d)
	}
	settings, err := Settings(s.Tools)
	if err != nil {
		return err
	}
	transcript, err := createPrivate(filepath.Join(rec.Dir, "transcript.jsonl"))
	if err != nil {
		return err
	}
	defer transcript.Close()
	var stderr bytes.Buffer
	run := AgentRun{
		Name:  fmt.Sprintf("%s-%s-%s-%d", AgentImageRepo, r.RunID, s.Name, rec.Trial),
		RunID: r.RunID, UpstreamNetwork: ep.Network, Allow: append(append([]string{}, AnthropicHosts...), ep.APIServer),
		DocsDir: dirs.docs, JourneyDir: dirs.journey, HarnessDir: dirs.harness, HomeDir: dirs.home,
		Settings: settings, SystemPrompt: SystemPrompt(s), Prompt: UserPrompt(s, vars),
		Env: grant.Env, GatewayForward: grant.GatewayForward, TimeLimit: s.Limit(),
		Transcript: red.Writer(transcript), Stderr: &stderr,
	}
	rec.Command = r.Agent.CommandLine(run)
	prompt := "# System prompt (appended)\n\n" + run.SystemPrompt + "\n\n# Prompt\n\n" + run.Prompt
	if err := writePrivate(filepath.Join(rec.Dir, "prompt.md"), []byte(prompt)); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(rec.Dir, "settings.json"), settings); err != nil {
		return err
	}
	r.logf("    running the agent (cap $%.2f, limit %s)", r.RunCapUSD, s.Limit())
	res, runErr := r.Agent.Run(ctx, run)
	// The result carries the run's cost, which the ledger settles at, so
	// it is kept even when the run or the record below fails.
	rec.Agent = res
	if err := writePrivate(filepath.Join(rec.Dir, "agent-stderr.log"), red.Redact(stderr.Bytes())); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		return fmt.Errorf("agent: %w", runErr)
	}
	return nil
}

// trialVars are the values a scenario's goal, steps, environment, and
// postconditions may name: the trial's nonce, the cluster, the image tag,
// the chart path inside the agent container, and the target's facts.
func (r *Runner) trialVars(s *Scenario, target Target, nonce string) map[string]string {
	vars := map[string]string{}
	for k, v := range target.Facts() {
		vars[k] = v
	}
	vars["nonce"] = nonce
	vars["cluster"] = KindClusterName
	vars["imageTag"] = KindAliasTag
	vars["podSubnet"] = JourneyPodSubnet
	vars["serviceSubnet"] = JourneyServiceSubnet
	vars["journey"] = s.Name
	if s.Setup == SetupPrerequisites {
		vars["chart"] = ContainerHome + "/" + chartDirName
	}
	return vars
}

// teardown deletes the cluster and verifies the deletion on a context
// detached from the run's.
func (r *Runner) teardown(target Target, dir string, red *Redactor) TeardownRecord {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	var log lockedBuffer
	defer func() {
		_ = writePrivate(filepath.Join(dir, "teardown.log"), red.Redact(log.Bytes()))
	}()
	r.logf("    deleting the %s cluster", target.Name())
	var rec TeardownRecord
	var errs []error
	if err := target.Delete(ctx, &log); err != nil {
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
		fmt.Fprintln(&log, rec.Error)
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
	for _, d := range []string{r.ResultsDir, dir} {
		if err := mkdirPrivate(d); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(dir, "summary.json"), sum); err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, "summary.md"), []byte(SummaryMarkdown(sum)))
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
	return writePrivate(path, append(b, '\n'))
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

// NewRunID returns a run identifier: the UTC start time and six random
// hex digits, so two runs started in the same second differ.
func NewRunID(start time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return start.UTC().Format("20060102T150405Z") + fmt.Sprintf("-%06x", start.UnixNano()&0xffffff)
	}
	return start.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
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
