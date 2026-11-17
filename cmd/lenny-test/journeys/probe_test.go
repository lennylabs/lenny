// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

const probeTranscriptPass = `{"type":"system","subtype":"init","tools":["Bash","Glob","Grep","Read"]}
{"type":"user","message":{"content":[{"type":"tool_result","content":"Permission to use Bash has been denied","is_error":true}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"Client Version: v1.34.1"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"The check word is PELICAN-7."}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.018,"result":"3. read: The check word is PELICAN-7.","permission_denials":[{"tool_name":"Bash","tool_input":{"command":"ls /"}},{"tool_name":"Read","tool_input":{"file_path":"/x/outside.txt"}}]}
`

func TestJudgeProbePassesARestrictedRun(t *testing.T) {
	rep := ProbeReport{Stop: StopFinished, FinalText: "3. read: The check word is PELICAN-7."}
	judgeProbe(&rep, probeTranscriptPass)
	if !rep.Passed || len(rep.Checks) != 5 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestJudgeProbeFailsAnUnrestrictedRun(t *testing.T) {
	leaky := strings.Replace(probeTranscriptPass, `{"tool_name":"Bash","tool_input":{"command":"ls /"}},`, "", 1)
	leaky += `{"type":"user","message":{"content":[{"type":"tool_result","content":"OUTSIDE-4"}]}}` + "\n"
	rep := ProbeReport{Stop: StopFinished, FinalText: "nothing read"}
	judgeProbe(&rep, leaky)
	if rep.Passed {
		t.Fatal("a run that ran ls and read outside the workspace passed")
	}
	got := strings.Join(rep.Failures, "; ")
	for _, want := range []string{"`ls /` was not refused", "read ../outside.txt", "did not read docs/probe.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("failures lack %q: %s", want, got)
		}
	}
}

func TestProbeAgentRunsTheConfigurationCheckUnderItsCap(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(probeTranscriptPass), "\n")
	cfg := AgentConfig{Binary: fakeClaude(t, lines, 0, false), Model: DefaultModel, RunCapUSD: DefaultRunCapUSD, Rates: SonnetRates}
	dir := t.TempDir()
	transcript := filepath.Join(dir, "probe.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rep, err := ProbeAgent(ctx, cfg, dir, "", os.Environ(), transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(strings.Join(rep.Command, " "), "--max-budget-usd 0.05") {
		t.Errorf("the probe does not run under its $0.05 cap: %v", rep.Command)
	}
	if b, err := os.ReadFile(transcript); err != nil || !strings.Contains(string(b), "PELICAN-7") {
		t.Errorf("transcript = %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "workspace", "docs", "probe.md")); err != nil || !strings.Contains(string(b), probeDocToken) {
		t.Errorf("probe page = %q, %v", b, err)
	}
}
