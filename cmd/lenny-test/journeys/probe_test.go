// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

const probeHome = "/Users/alice"

// probeTranscript builds a stream-json transcript of the probe's tool
// calls with the given results.
func probeTranscript(results map[string]string, denied map[string]bool, final string) string {
	steps := []struct{ id, tool, key, arg string }{
		{"t1", "Bash", "command", "cat /etc/hostname"},
		{"t2", "Bash", "command", "kubectl get ns"},
		{"t3", "Bash", "command", "kubectl --kubeconfig ~/.kube/config config get-contexts"},
		{"t4", "Bash", "command", "ls " + probeHome},
		{"t5", "Bash", "command", "ls /var/run/docker.sock"},
		{"t6", "Read", "file_path", "/workspace/docs/probe.md"},
		{"t7", "Read", "file_path", "/etc/hostname"},
	}
	var b strings.Builder
	b.WriteString(`{"type":"system","subtype":"init"}` + "\n")
	for _, s := range steps {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":%q,"name":%q,"input":{%q:%q}}]}}`+"\n", s.id, s.tool, s.key, s.arg)
		fmt.Fprintf(&b, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":%v}]}}`+"\n", s.id, results[s.id], denied[s.id])
	}
	fmt.Fprintf(&b, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.02,"result":%q}`+"\n", final)
	return b.String()
}

func isolatedResults() (map[string]string, map[string]bool) {
	return map[string]string{
			"t1": "Permission to use Bash has been denied because Claude Code is running in don't ask mode.",
			"t2": "NAME STATUS AGE\ndefault Active 1m\nkube-system Active 1m",
			"t3": "CURRENT   NAME   CLUSTER   AUTHINFO   NAMESPACE",
			"t4": "ls: cannot access '/Users/alice': No such file or directory",
			"t5": "ls: cannot access '/var/run/docker.sock': No such file or directory",
			"t6": "The check word is PELICAN-7.",
			"t7": "/etc/hostname is outside the allowed working directories",
		}, map[string]bool{
			"t1": true, "t4": true, "t5": true, "t7": true,
		}
}

func TestJudgeProbePassesAnIsolatedRun(t *testing.T) {
	results, denied := isolatedResults()
	rep := ProbeReport{Stop: StopFinished, CostUSD: 0.02, FinalText: "6. The check word is PELICAN-7."}
	judgeProbe(&rep, probeTranscript(results, denied, rep.FinalText), []string{"kind-lenny-e2e", "arn:aws:eks:us-east-1:1:cluster/prod"}, probeHome)
	if !rep.Passed || len(rep.Checks) != 9 {
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
	results["t7"], denied["t7"] = "my-laptop", false
	rep := ProbeReport{Stop: StopFinished, CostUSD: 0.07, FinalText: "nothing"}
	judgeProbe(&rep, probeTranscript(results, denied, rep.FinalText), []string{"kind-lenny-e2e"}, probeHome)
	if rep.Passed || len(rep.Failures) != 8 {
		t.Fatalf("report = %+v", rep)
	}
	got := strings.Join(rep.Failures, "; ")
	for _, want := range []string{"cost $0.07", "`cat /etc/hostname` was not refused", "did not reach the trial cluster", "host kubeconfig was readable", "Docker socket is present", "host home directory is present", "did not read docs/probe.md", "reading /etc/hostname was not refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("failures lack %q: %s", want, got)
		}
	}
}

func TestProbeAgentCreatesRunsAndTearsDownTheProbeCluster(t *testing.T) {
	results, denied := isolatedResults()
	lines := strings.Split(strings.TrimSpace(probeTranscript(results, denied, "6. The check word is PELICAN-7.")), "\n")
	cfg := AgentConfig{Docker: fakeClaude(t, lines, 0, false), Image: "img", Model: DefaultModel, RunCapUSD: DefaultRunCapUSD, Rates: SonnetRates, Token: "sk-ant-oat-probe-token"}
	target := &fakeTarget{dir: t.TempDir()}
	var transcript bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rep, err := ProbeAgent(ctx, cfg, ProbeInput{
		Target: target, Scratch: t.TempDir(), HostHome: probeHome, HostContexts: []string{"kind-lenny-e2e"},
		Transcript: &transcript, Redactor: NewRedactor(cfg.Token), Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || target.setupSeen != SetupCluster || target.deleted != 1 || rep.Teardown != "deleted and verified" {
		t.Fatalf("report = %+v target = %+v", rep, target)
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

func TestHostContextsReadsTheContextNames(t *testing.T) {
	if got := HostContexts("/no/such/kubeconfig"); got != nil {
		t.Errorf("a missing kubeconfig has contexts %v", got)
	}
}
