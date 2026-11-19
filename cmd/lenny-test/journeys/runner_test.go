// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

type fakeTarget struct {
	dir                     string
	createErr, deleteErr    error
	verifyErr               error
	created, deleted        int
	cost, maxCost           float64
	setupSeen               string
	verifyCalledAfterDelete bool
	// leak is written to the creation and deletion logs.
	leak string
}

func (f *fakeTarget) Name() string       { return "fake" }
func (f *fakeTarget) Kubeconfig() string { return filepath.Join(f.dir, "kubeconfig") }
func (f *fakeTarget) Create(_ context.Context, setup string, log io.Writer) error {
	_, _ = io.WriteString(log, "Authorization: Bearer provision-bearer-secret\n"+f.leak)
	f.created++
	f.setupSeen = setup
	return f.createErr
}

func (f *fakeTarget) Delete(_ context.Context, log io.Writer) error {
	_, _ = io.WriteString(log, "Authorization: Bearer teardown-bearer-secret\n"+f.leak)
	f.deleted++
	return f.deleteErr
}

func (f *fakeTarget) VerifyDeleted(context.Context) error {
	f.verifyCalledAfterDelete = f.deleted > 0
	return f.verifyErr
}
func (f *fakeTarget) CostUSD() float64 { return f.cost }
func (f *fakeTarget) Facts() map[string]string {
	return map[string]string{"echoImage": "lenny-runtime-echo:src-1@sha256:ab"}
}

func (f *fakeTarget) AgentEndpoint(context.Context) (AgentEndpoint, error) {
	return AgentEndpoint{Network: "lenny-journeys", APIServer: "lenny-journeys-control-plane:6443", Kubeconfig: []byte("users:\n- user:\n    token: kubeconfig-token-secret\n")}, nil
}
func (f *fakeTarget) MaxCostUSD() float64 { return f.maxCost }

type fakeAgent struct {
	result AgentResult
	runs   []AgentRun
	panics bool
	// leak is written to the transcript and the standard error.
	leak string
}

func (a *fakeAgent) Run(_ context.Context, run AgentRun) (AgentResult, error) {
	a.runs = append(a.runs, run)
	if a.panics {
		panic("agent exploded")
	}
	_, _ = run.Transcript.Write([]byte(`{"type":"result","note":"token: kubeconfig-token-secret Bearer abcdefghijklmnop"}` + "\n"))
	_, _ = run.Stderr.Write([]byte("LENNY_API_TOKEN=admin-token-secret\n"))
	if a.leak != "" {
		_, _ = run.Transcript.Write([]byte(a.leak))
		_, _ = run.Stderr.Write([]byte(a.leak))
	}
	return a.result, nil
}

func (a *fakeAgent) CommandLine(run AgentRun) []string { return []string{"claude", "-p", run.Prompt} }

type fakeAccess struct {
	*fakeCluster
	prepared []string
	closed   bool
}

func (a *fakeAccess) AdminToken(context.Context) (string, error) { return "admin-token-secret", nil }

func (a *fakeAccess) Prepare(_ context.Context, step string) error {
	a.prepared = append(a.prepared, step)
	return nil
}
func (a *fakeAccess) Close() { a.closed = true }

type runnerFixture struct {
	r       *Runner
	targets []*fakeTarget
	agent   *fakeAgent
	access  *fakeAccess
	results string
}

func newRunnerFixture(t *testing.T, configure func(*fakeTarget)) *runnerFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide", "page.md"), []byte("# Page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "charts", "lenny"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "charts", "lenny", "Chart.yaml"), []byte("name: lenny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx := &runnerFixture{
		agent:   &fakeAgent{result: AgentResult{Stop: StopFinished, StopDetail: "success", CostUSD: 1.5, CostReported: true}},
		access:  &fakeAccess{fakeCluster: newFakeCluster()},
		results: filepath.Join(root, "tests", "results", "journeys"),
	}
	fx.access.kubectl["get pods"] = []string{"Running"}
	ledger, err := OpenLedger(LedgerPath(fx.results, "pilot"), "pilot", DefaultPilotCapUSD)
	if err != nil {
		t.Fatal(err)
	}
	fx.r = &Runner{
		RepoRoot: root, ResultsDir: fx.results, ScratchDir: t.TempDir(), RunID: "run1",
		Trials: 3, RunCapUSD: DefaultRunCapUSD, Ledger: ledger, Agent: fx.agent,
		Targets: Targets{"fake": func(dir string) Target {
			ft := &fakeTarget{dir: dir}
			if configure != nil {
				configure(ft)
			}
			fx.targets = append(fx.targets, ft)
			return ft
		}},
		NewAccess: func(string) Access { return fx.access },
		Secrets:   []string{"sk-ant-oat-claude-token"},
		Now:       func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
		Nonce:     func() string { return "ab12cd34" },
		Sleep:     func(context.Context, time.Duration) error { return nil },
	}
	return fx
}

func fixtureScenario(setup string) *Scenario {
	return &Scenario{
		Name: "demo", Persona: "operator", Target: "fake", Setup: setup,
		Docs: []string{"docs/guide/page.md"}, Goal: "Install with the chart at {{chart}}; nonce {{nonce}}.",
		Tools: []string{"kubectl"}, Credentials: []string{CredKubeconfig},
		Postconditions: []Postcondition{{Name: "pods-running", Kubectl: []string{"get", "pods"}, Expect: Expect{Contains: "Running"}}},
	}
}

func TestRunnerRunsEachTrialAndRecordsIt(t *testing.T) {
	fx := newRunnerFixture(t, nil)
	sum, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupPrerequisites)})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Trials) != 3 || len(fx.targets) != 3 || len(fx.agent.runs) != 3 {
		t.Fatalf("trials=%d targets=%d agent runs=%d, want 3 each", len(sum.Trials), len(fx.targets), len(fx.agent.runs))
	}
	for _, ft := range fx.targets {
		if ft.created != 1 || ft.deleted != 1 || !ft.verifyCalledAfterDelete || ft.setupSeen != SetupPrerequisites {
			t.Errorf("target lifecycle = %+v", ft)
		}
	}
	if sum.Journeys[0].Verdict != JourneyPass || sum.Journeys[0].AgentUSD != 4.5 {
		t.Errorf("journey summary = %+v", sum.Journeys[0])
	}
	if fx.r.Ledger.Spent() != 4.5 || len(fx.r.Ledger.Entries) != 3 {
		t.Errorf("ledger spent $%.2f over %d entries", fx.r.Ledger.Spent(), len(fx.r.Ledger.Entries))
	}

	run := fx.agent.runs[0]
	if !strings.Contains(run.Prompt, "nonce ab12cd34") || strings.Contains(run.Prompt, "{{chart}}") {
		t.Errorf("the prompt was not expanded: %s", run.Prompt)
	}
	if !strings.Contains(run.Prompt, "/home/agent/lenny-chart") {
		t.Errorf("the prompt does not name the chart inside the container: %s", run.Prompt)
	}
	if _, err := os.Stat(filepath.Join(run.DocsDir, "guide", "page.md")); err != nil {
		t.Errorf("the documentation copy is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.HomeDir, "lenny-chart", "Chart.yaml")); err != nil {
		t.Errorf("the chart copy is missing from the agent's home: %v", err)
	}
	if strings.HasPrefix(run.DocsDir, fx.r.RepoRoot) {
		t.Errorf("the documentation copy %s is inside the repository", run.DocsDir)
	}
	if run.UpstreamNetwork != "lenny-journeys" || run.Env["KUBECONFIG"] != ContainerKubeconfig || len(run.Env) != 1 || run.RunID != "run1" {
		t.Errorf("the agent run grants %v on %q", run.Env, run.UpstreamNetwork)
	}
	if strings.Join(run.Allow, ",") != "api.anthropic.com:443,claude.ai:443,platform.claude.com:443,lenny-journeys-control-plane:6443" {
		t.Errorf("the proxy allows %v", run.Allow)
	}
	if fi, err := os.Stat(filepath.Join(run.JourneyDir, "kubeconfig")); err != nil || fi.Mode().Perm() != 0o400 {
		t.Errorf("the agent kubeconfig: %v, %v", fi, err)
	}

	trialDir := filepath.Join(fx.results, "run1", "demo", "trial-1")
	for _, f := range []string{"result.json", "transcript.jsonl", "prompt.md", "settings.json", "provision.log", "teardown.log", "agent-stderr.log"} {
		fi, err := os.Stat(filepath.Join(trialDir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("trial record %s: %v, %v; want mode 0600", f, fi, err)
		}
	}
	for _, d := range []string{fx.results, filepath.Join(fx.results, "run1"), trialDir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("record directory %s: %v, %v; want mode 0700", d, fi, err)
		}
	}
	for _, f := range []string{"transcript.jsonl", "agent-stderr.log", "provision.log", "teardown.log"} {
		b, err := os.ReadFile(filepath.Join(trialDir, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"kubeconfig-token-secret", "admin-token-secret", "abcdefghijklmnop", "provision-bearer-secret", "teardown-bearer-secret"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("%s holds the secret %q: %s", f, secret, b)
			}
		}
	}
	var rec TrialRecord
	b, err := os.ReadFile(filepath.Join(trialDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Verdict.Outcome != OutcomePass || rec.Agent.CostUSD != 1.5 || !rec.Teardown.Verified || len(rec.Command) == 0 {
		t.Errorf("result.json = %+v", rec)
	}
	for _, f := range []string{"summary.json", "summary.md"} {
		if _, err := os.Stat(filepath.Join(fx.results, "run1", f)); err != nil {
			t.Errorf("run record lacks %s: %v", f, err)
		}
	}
}

func TestRunnerTearsDownAndMarksInconclusiveWhenCreationFails(t *testing.T) {
	fx := newRunnerFixture(t, func(ft *fakeTarget) { ft.createErr = errors.New("kind: no space left") })
	fx.r.Trials = 1
	sum, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)})
	if err != nil {
		t.Fatal(err)
	}
	tr := sum.Trials[0]
	if tr.Verdict.Outcome != OutcomeInconclusive || !strings.Contains(tr.Verdict.Reason, "no space left") {
		t.Errorf("verdict = %+v", tr.Verdict)
	}
	if fx.targets[0].deleted != 1 || len(fx.agent.runs) != 0 {
		t.Errorf("deleted=%d agent runs=%d, want the cluster deleted and no agent", fx.targets[0].deleted, len(fx.agent.runs))
	}
}

func TestRunnerStopsWhenATeardownCannotBeVerified(t *testing.T) {
	fx := newRunnerFixture(t, func(ft *fakeTarget) { ft.verifyErr = errors.New("2 containers left") })
	sum, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)})
	if err == nil || !strings.Contains(sum.Stopped, "could not be verified") {
		t.Fatalf("err=%v stopped=%q", err, sum.Stopped)
	}
	if len(sum.Trials) != 1 || sum.Trials[0].Verdict.Outcome != OutcomeInconclusive {
		t.Errorf("trials = %+v", sum.Trials)
	}
	b, rerr := os.ReadFile(filepath.Join(fx.results, "run1", "summary.md"))
	if rerr != nil || !strings.Contains(string(b), "stopped early") {
		t.Errorf("summary.md = %q, %v", b, rerr)
	}
}

func TestRunnerRefusesTrialsThePilotCannotAfford(t *testing.T) {
	fx := newRunnerFixture(t, nil)
	if err := fx.r.Ledger.Record(LedgerEntry{AgentUSD: 47}); err != nil {
		t.Fatal(err)
	}
	sum, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)})
	if !errors.Is(err, ErrPilotBudget) {
		t.Fatalf("err = %v, want the pilot budget refusal", err)
	}
	// $47 spent leaves room for one $6 run; at $48.50 the next is refused.
	if len(sum.Trials) != 1 || len(fx.targets) != 2 || fx.targets[1].created != 0 {
		t.Fatalf("trials=%d targets=%d; the refused trial must create no cluster", len(sum.Trials), len(fx.targets))
	}
}

func TestRunnerReservesTheTargetCostWithTheAgentCap(t *testing.T) {
	fx := newRunnerFixture(t, func(ft *fakeTarget) { ft.maxCost = 50; ft.cost = 2 })
	_, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)})
	if !errors.Is(err, ErrPilotBudget) {
		t.Fatalf("a $50 target with a $6 agent cap fits no $54 pilot: %v", err)
	}
}

func TestRunnerRecordsInfrastructureCostSeparately(t *testing.T) {
	fx := newRunnerFixture(t, func(ft *fakeTarget) { ft.cost = 0.75 })
	fx.r.Trials = 1
	if _, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)}); err != nil {
		t.Fatal(err)
	}
	e := fx.r.Ledger.Entries[0]
	if e.AgentUSD != 1.5 || e.InfraUSD != 0.75 || fx.r.Ledger.Spent() != 2.25 {
		t.Fatalf("ledger entry = %+v", e)
	}
}

func TestRunnerTearsDownWhenTheAgentPanics(t *testing.T) {
	fx := newRunnerFixture(t, nil)
	fx.agent.panics = true
	fx.r.Trials = 1
	func() {
		defer func() { _ = recover() }()
		_, _ = fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupLenny)})
	}()
	if len(fx.targets) != 1 || fx.targets[0].deleted != 1 {
		t.Fatalf("the cluster was not deleted after a panic: %+v", fx.targets)
	}
}

func TestRunnerStopsOnCancellationAndStillTearsDown(t *testing.T) {
	fx := newRunnerFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	fx.agent.result = AgentResult{Stop: StopInterrupted, StopDetail: "context canceled"}
	fx.r.NewAccess = func(string) Access { cancel(); return fx.access }
	sum, err := fx.r.Run(ctx, []*Scenario{fixtureScenario(SetupLenny)})
	if !errors.Is(err, context.Canceled) || sum.Stopped != "interrupted" {
		t.Fatalf("err=%v stopped=%q", err, sum.Stopped)
	}
	if len(sum.Trials) != 1 || fx.targets[0].deleted != 1 || sum.Trials[0].Verdict.Outcome != OutcomeInconclusive {
		t.Fatalf("trials=%+v", sum.Trials)
	}
}

func TestRunnerSkipAgentEvaluatesWithoutTheAgentOrItsReservation(t *testing.T) {
	fx := newRunnerFixture(t, nil)
	fx.r.SkipAgent = true
	fx.r.Trials = 1
	if err := fx.r.Ledger.Record(LedgerEntry{AgentUSD: DefaultPilotCapUSD}); err != nil {
		t.Fatal(err)
	}
	s := fixtureScenario(SetupLenny)
	s.Prepare = []string{"sessions-allowed:default"}
	sum, err := fx.r.Run(context.Background(), []*Scenario{s})
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.agent.runs) != 0 || !sum.Trials[0].AgentSkipped || sum.Trials[0].Verdict.Outcome != OutcomePass {
		t.Fatalf("trial = %+v", sum.Trials[0])
	}
	if len(fx.access.prepared) != 1 || !fx.access.closed {
		t.Errorf("prepared=%v closed=%v", fx.access.prepared, fx.access.closed)
	}
}

func TestPromptsCarryTheGoalHintsAndCredentials(t *testing.T) {
	s := fixtureScenario(SetupLenny)
	s.Steps = []string{"Do {{nonce}}."}
	s.Environment = "Images are tagged {{imageTag}}."
	s.Credentials = []string{CredGatewayURL, CredAdminToken}
	p := UserPrompt(s, map[string]string{"nonce": "n1", "imageTag": "journeys", "chart": "/c"})
	for _, want := range []string{"Install with the chart at /c", "docs/guide/page.md", "1. Do n1.", "tagged journeys", "LENNY_API_URL", "LENNY_API_TOKEN"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, p)
		}
	}
	s.Credentials = nil
	if !strings.Contains(UserPrompt(s, nil), "no credentials") {
		t.Error("a scenario without credentials does not say so")
	}
	if sp := SystemPrompt(s); !strings.Contains(sp, "role of operator") || !strings.Contains(sp, "kubectl") {
		t.Errorf("system prompt = %s", sp)
	}
}

func TestParseIdentityAndPrepare(t *testing.T) {
	if tn, u, r, err := ParseIdentity("platform-admin"); err != nil || tn != "platform" || r != "platform-admin" || u == "" {
		t.Errorf("platform-admin = %q %q %q %v", tn, u, r, err)
	}
	if tn, u, r, err := ParseIdentity("acme/alice@acme.com/user,tenant-admin"); err != nil || tn != "acme" || u != "alice@acme.com" || r != "user,tenant-admin" {
		t.Errorf("explicit identity = %q %q %q %v", tn, u, r, err)
	}
	for _, bad := range []string{"", "acme/alice", "acme//user", "a/b/c/d"} {
		if _, _, _, err := ParseIdentity(bad); err == nil {
			t.Errorf("identity %q parsed", bad)
		}
	}
	if _, arg, err := ParsePrepare("sessions-allowed:acme"); err != nil || arg != "acme" {
		t.Errorf("prepare = %q %v", arg, err)
	}
	for _, bad := range []string{"sessions-allowed", "sessions-allowed:", "other:acme"} {
		if _, _, err := ParsePrepare(bad); err == nil {
			t.Errorf("preparation %q parsed", bad)
		}
	}
}
