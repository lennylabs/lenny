// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

const probeHome = "/Users/alice"

type probeStep struct{ id, tool, key, arg string }

var probeSteps = []probeStep{
	{"t1", "Bash", "command", "cat /etc/hostname"},
	{"t2", "Bash", "command", "kubectl get ns"},
	{"t3", "Bash", "command", "kubectl --kubeconfig ~/.kube/config config get-contexts"},
	{"t4", "Bash", "command", "ls " + probeHome},
	{"t5", "Bash", "command", "ls /var/run/docker.sock"},
	{"t6", "Bash", "command", "kubectl --server https://example.com get ns"},
	{"t7", "Bash", "command", "kubectl --server https://host.docker.internal:6443 get ns"},
	{"t8", "Bash", "command", "kubectl --server https://lenny-e2e-control-plane:6443 get ns"},
	{"t9", "Read", "file_path", "/workspace/docs/probe.md"},
	{"t10", "Read", "file_path", "/etc/hostname"},
}

// probeTranscript builds a stream-json transcript of the probe's tool
// calls with the given results.
func probeTranscript(results map[string]string, denied map[string]bool, final string) string {
	var b strings.Builder
	b.WriteString(`{"type":"system","subtype":"init"}` + "\n")
	for _, s := range probeSteps {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":%q,"name":%q,"input":{%q:%q}}]}}`+"\n", s.id, s.tool, s.key, s.arg)
		fmt.Fprintf(&b, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":%v}]}}`+"\n", s.id, results[s.id], denied[s.id])
	}
	fmt.Fprintf(&b, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.02,"result":%q}`+"\n", final)
	return b.String()
}

func isolatedResults() (map[string]string, map[string]bool) {
	return map[string]string{
			"t1":  "Permission to use Bash has been denied because Claude Code is running in don't ask mode.",
			"t2":  "NAME STATUS AGE\ndefault Active 1m\nkube-system Active 1m",
			"t3":  "CURRENT   NAME   CLUSTER   AUTHINFO   NAMESPACE",
			"t4":  "ls: cannot access '/Users/alice': No such file or directory",
			"t5":  "ls: cannot access '/var/run/docker.sock': No such file or directory",
			"t6":  "Unable to connect to the server: Forbidden",
			"t7":  "Unable to connect to the server: Forbidden",
			"t8":  "Unable to connect to the server: Forbidden",
			"t9":  "The check word is PELICAN-7.",
			"t10": "/etc/hostname is outside the allowed working directories",
		}, map[string]bool{
			"t1": true, "t4": true, "t5": true, "t6": true, "t7": true, "t8": true, "t10": true,
		}
}

func TestJudgeProbePassesAnIsolatedRun(t *testing.T) {
	results, denied := isolatedResults()
	rep := ProbeReport{Stop: StopFinished, CostUSD: 0.02, FinalText: "9. The check word is PELICAN-7."}
	// A host context named default must not match the default namespace
	// that step 2 lists.
	judgeProbe(&rep, probeTranscript(results, denied, rep.FinalText), []string{"default", "kind-lenny-e2e", "arn:aws:eks:us-east-1:1:cluster/prod"}, probeHome)
	if !rep.Passed || len(rep.Checks) != 12 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestJudgeProbeFailsARunThatReachesTheHost(t *testing.T) {
	results, denied := isolatedResults()
	results["t1"], denied["t1"] = "my-laptop", false
	results["t2"] = "error: no configuration has been provided"
	results["t3"] = "CURRENT   NAME   CLUSTER\n*         kind-lenny-e2e   kind-lenny-e2e"
	results["t4"], denied["t4"] = "Desktop Documents", false
	results["t5"], denied["t5"] = "/var/run/docker.sock", false
	results["t6"], denied["t6"] = "error: the server doesn't have a resource type ns (example.com answered 404)", true
	results["t7"], denied["t7"] = "NAME STATUS\nkube-system Active", false
	results["t8"], denied["t8"] = "NAME STATUS\nkube-system Active", false
	results["t10"], denied["t10"] = "my-laptop", false
	rep := ProbeReport{Stop: StopFinished, CostUSD: 0.07, FinalText: "nothing"}
	judgeProbe(&rep, probeTranscript(results, denied, rep.FinalText), []string{"kind-lenny-e2e"}, probeHome)
	if rep.Passed || len(rep.Failures) != 11 {
		t.Fatalf("report = %+v", rep)
	}
	got := strings.Join(rep.Failures, "; ")
	for _, want := range []string{"cost $0.07", "`cat /etc/hostname` was not refused", "did not reach the trial cluster", "host kubeconfig was readable", "Docker socket is present", "host home directory is present", "reached https://example.com", "reached https://host.docker.internal:6443", "reached https://lenny-e2e-control-plane:6443", "did not read docs/probe.md", "reading /etc/hostname was not refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("failures lack %q: %s", want, got)
		}
	}
}

func TestJudgeGatewayOnly(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		pass bool
	}{
		{"KUBECONFIG=/journey/empty-kubeconfig\nPROBE_DONE", nil, true},
		{"NONEMPTY /journey/kubeconfig\nKUBECONFIG=/journey/kubeconfig\nPROBE_DONE", nil, false},
		{"HARNESS_VISIBLE\nKUBECONFIG=/journey/empty-kubeconfig\nPROBE_DONE", nil, false},
		{"KUBECTL_REACHED\nKUBECONFIG=/journey/empty-kubeconfig\nPROBE_DONE", nil, false},
		{"KUBECONFIG=/root/.kube/config\nPROBE_DONE", nil, false},
		{"", errors.New("exit status 125"), false},
	}
	for i, tc := range cases {
		rep := ProbeReport{}
		judgeGatewayOnly(&rep, tc.out, tc.err)
		if rep.Passed != tc.pass {
			t.Errorf("case %d: passed=%v, want %v (%+v)", i, rep.Passed, tc.pass, rep)
		}
	}
}

func probeFixture(t *testing.T, target *fakeTarget) (AgentConfig, ProbeInput, *bytes.Buffer) {
	t.Helper()
	results, denied := isolatedResults()
	lines := strings.Split(strings.TrimSpace(probeTranscript(results, denied, "9. The check word is PELICAN-7.")), "\n")
	t.Setenv("CHECK_OUTPUT", "KUBECONFIG=/journey/empty-kubeconfig\nPROBE_DONE")
	cfg := AgentConfig{Docker: fakeClaude(t, lines, 0, false), Image: "sha256:img", Model: DefaultModel, RunCapUSD: DefaultRunCapUSD, Rates: SonnetRates, Token: "sk-ant-oat-probe-token"}
	var transcript bytes.Buffer
	return cfg, ProbeInput{
		Target: target, RunID: "r1", Scratch: t.TempDir(), HostHome: probeHome, HostContexts: []string{"kind-lenny-e2e"},
		Transcript: &transcript, Redactor: NewRedactor(cfg.Token), Log: io.Discard,
	}, &transcript
}

func TestProbeAgentCreatesRunsAndTearsDownTheProbeCluster(t *testing.T) {
	target := &fakeTarget{dir: t.TempDir()}
	cfg, in, transcript := probeFixture(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rep, err := ProbeAgent(ctx, cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || target.setupSeen != SetupCluster || target.deleted != 1 || rep.Teardown != "deleted and verified" {
		t.Fatalf("report = %+v target = %+v", rep, target)
	}
	if !strings.Contains(strings.Join(rep.Checks, "; "), "gateway-url-only grant mounts no kubeconfig") {
		t.Errorf("the gateway-url-only check did not run: %v", rep.Checks)
	}
	if !strings.Contains(strings.Join(rep.Command, " "), "--max-budget-usd 0.05") {
		t.Errorf("the probe does not run under its $0.05 cap: %v", rep.Command)
	}
	if !strings.Contains(transcript.String(), "PELICAN-7") || strings.Contains(transcript.String(), "sk-ant-oat") {
		t.Errorf("transcript = %q", transcript.String())
	}
}

func TestProbeAgentTearsDownWhenTheClusterFails(t *testing.T) {
	target := &fakeTarget{dir: t.TempDir(), createErr: fmt.Errorf("no space")}
	_, err := ProbeAgent(context.Background(), AgentConfig{}, ProbeInput{Target: target, Scratch: t.TempDir(), Redactor: NewRedactor(), Log: io.Discard, Transcript: io.Discard})
	if err == nil || target.deleted != 1 {
		t.Fatalf("err = %v deleted = %d", err, target.deleted)
	}
}

func TestRunProbeReservesSettlesAndMapsTheExitCode(t *testing.T) {
	ledger, err := OpenLedger(LedgerPath(t.TempDir(), "pilot"), "pilot", 1)
	if err != nil {
		t.Fatal(err)
	}
	target := &fakeTarget{dir: t.TempDir()}
	cfg, in, _ := probeFixture(t, target)
	rep, code, err := RunProbe(context.Background(), cfg, in, ledger)
	if err != nil || code != 0 || !rep.Passed {
		t.Fatalf("a passing probe: code %d, %v, %+v", code, err, rep)
	}
	if len(ledger.Entries) != 1 || ledger.Entries[0].Pending || ledger.Entries[0].AgentUSD != 0.02 || ledger.Entries[0].Journey != "probe" {
		t.Fatalf("ledger after a passing probe: %+v", ledger.Entries)
	}

	// A probe whose cluster fails settles at nothing spent and maps to 2.
	failing := &fakeTarget{dir: t.TempDir(), createErr: errors.New("no space")}
	in.Target, in.RunID = failing, "r2"
	_, code, err = RunProbe(context.Background(), cfg, in, ledger)
	if err == nil || code != 2 {
		t.Fatalf("a probe that cannot run: code %d, %v", code, err)
	}
	if len(ledger.Entries) != 2 || ledger.Entries[1].Pending || ledger.Entries[1].AgentUSD != 0 {
		t.Fatalf("ledger after a failed probe: %+v", ledger.Entries)
	}

	// A probe that runs and fails a check maps to 1.
	t.Setenv("CHECK_OUTPUT", "NONEMPTY /journey/kubeconfig\nPROBE_DONE")
	in.Target, in.RunID = &fakeTarget{dir: t.TempDir()}, "r3"
	if _, code, err = RunProbe(context.Background(), cfg, in, ledger); err != nil || code != 1 {
		t.Fatalf("a failing probe: code %d, %v", code, err)
	}

	// A pilot that cannot afford the probe refuses it before any cluster.
	poor, err := OpenLedger(LedgerPath(t.TempDir(), "poor"), "poor", 0.01)
	if err != nil {
		t.Fatal(err)
	}
	untouched := &fakeTarget{dir: t.TempDir()}
	in.Target = untouched
	if _, code, err = RunProbe(context.Background(), cfg, in, poor); !errors.Is(err, ErrPilotBudget) || code != 2 || untouched.created != 0 {
		t.Fatalf("an unaffordable probe: code %d, %v, created %d", code, err, untouched.created)
	}
}

func TestHostContextsReadsTheContextNames(t *testing.T) {
	if got := HostContexts("/no/such/kubeconfig"); got != nil {
		t.Errorf("a missing kubeconfig has contexts %v", got)
	}
}
