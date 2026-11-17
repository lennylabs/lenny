// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.
// They run a fake claude, a shell script that prints recorded stream-json
// lines, so no model is called.

// fakeClaude writes a script that prints lines and then either exits
// with code or, when hang is set, replaces itself with a long sleep so
// only the harness's signal ends it.
func fakeClaude(t *testing.T, lines []string, code int, hang bool) string {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "stream.jsonl")
	if err := os.WriteFile(data, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := fmt.Sprintf("exit %d", code)
	if hang {
		tail = "exec sleep 30"
	}
	script := filepath.Join(dir, "claude")
	body := fmt.Sprintf("#!/bin/sh\ncat %q\n%s\n", data, tail)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// response returns a message_start and a message_delta for one response
// whose input tokens cost dollars at Sonnet rates.
func response(id string, dollars float64) []string {
	input := int64(dollars / SonnetRates.Input * 1e6)
	return []string{
		fmt.Sprintf(`{"type":"stream_event","event":{"type":"message_start","message":{"id":%q,"usage":{"input_tokens":%d,"output_tokens":1}}}}`, id, input),
		fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"usage":{"input_tokens":%d,"output_tokens":1},"content":[{"type":"text","text":"working"}]}}`, id, input),
		`{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":1}}}`,
	}
}

func agentRun(t *testing.T, limit time.Duration) (AgentRun, *bytes.Buffer) {
	t.Helper()
	var transcript bytes.Buffer
	return AgentRun{
		Workspace: t.TempDir(), SettingsPath: "settings.json", Prompt: "p", SystemPrompt: "s",
		Env: BuildEnv(os.Environ(), "", nil), TimeLimit: limit, Transcript: &transcript, Stderr: &bytes.Buffer{},
	}, &transcript
}

func TestAgentStopsWhenTheRunningCostReachesTheCap(t *testing.T) {
	var lines []string
	lines = append(lines, `{"type":"system","subtype":"init"}`)
	lines = append(lines, response("msg_1", 2.5)...)
	lines = append(lines, response("msg_2", 2.5)...)
	lines = append(lines, response("msg_3", 2.5)...)
	cfg := AgentConfig{Binary: fakeClaude(t, lines, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, transcript := agentRun(t, time.Minute)
	start := time.Now()
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopCostCap {
		t.Fatalf("stop = %s (%s), want the cost cap", res.Stop, res.StopDetail)
	}
	if res.CostReported || res.CostUSD < 6 || res.CostUSD > 7.6 {
		t.Errorf("cost = $%.4f reported=%v, want the estimate at the third response", res.CostUSD, res.CostReported)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("the harness took %s to stop the agent", time.Since(start))
	}
	if strings.Contains(transcript.String(), StreamEventType) {
		t.Error("the transcript keeps partial-message stream events")
	}
	if !strings.Contains(transcript.String(), `"type":"assistant"`) {
		t.Error("the transcript lost the assistant messages")
	}
}

func TestAgentRecordsAFinishedRunWithItsReportedCost(t *testing.T) {
	lines := append([]string{`{"type":"system","subtype":"init"}`}, response("msg_1", 0.5)...)
	lines = append(lines, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.51,"num_turns":3,"result":"Done: the pool is ready."}`)
	cfg := AgentConfig{Binary: fakeClaude(t, lines, 0, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, time.Minute)
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopFinished || !res.CostReported || res.CostUSD != 0.51 || res.NumTurns != 3 || res.FinalText != "Done: the pool is ready." {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentReportsClaudeCodesOwnBudgetStopAsTheCostCap(t *testing.T) {
	lines := []string{`{"type":"result","subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":6.02,"errors":["Reached maximum budget ($6)"]}`}
	cfg := AgentConfig{Binary: fakeClaude(t, lines, 1, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, time.Minute)
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopCostCap || res.CostUSD != 6.02 {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentStopsAtTheTimeLimit(t *testing.T) {
	cfg := AgentConfig{Binary: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, 300*time.Millisecond)
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopTimeLimit {
		t.Fatalf("stop = %s (%s), want the time limit", res.Stop, res.StopDetail)
	}
}

func TestAgentReportsAnOutsideCancellationAsInterrupted(t *testing.T) {
	cfg := AgentConfig{Binary: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := cfg.Run(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopInterrupted {
		t.Fatalf("stop = %s (%s), want interrupted", res.Stop, res.StopDetail)
	}
}

func TestAgentErrorsWithoutAResultAreAgentErrors(t *testing.T) {
	cfg := AgentConfig{Binary: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 3, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, time.Minute)
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "no result message") || !strings.Contains(res.StopDetail, "exit status 3") {
		t.Fatalf("result = %+v", res)
	}

	lines := []string{`{"type":"result","subtype":"error_during_execution","is_error":true,"total_cost_usd":0.1,"errors":["API Error: 529 overloaded"]}`}
	cfg.Binary = fakeClaude(t, lines, 1, false)
	res, err = cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "overloaded") {
		t.Fatalf("an error result: %+v", res)
	}

	cfg.Binary = filepath.Join(t.TempDir(), "no-such-claude")
	res, err = cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "start") {
		t.Fatalf("a missing binary: %+v", res)
	}
}

func TestAgentArgsRestrictTheToolsAndCapTheBudget(t *testing.T) {
	cfg := AgentConfig{Binary: "claude", Model: DefaultModel, RunCapUSD: 6}
	args := cfg.Args(AgentRun{Prompt: "goal", SystemPrompt: "persona", SettingsPath: "/t/settings.json"})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-p goal", "--model claude-sonnet-5-5", "--output-format stream-json", "--include-partial-messages",
		"--max-budget-usd 6.00", "--tools Bash,Read,Glob,Grep", "--settings /t/settings.json",
		"--permission-mode dontAsk", "--permission-prompts none", "--restricted", "--safe-mode",
		"--strict-mcp-config", "--no-session-persistence", "--append-system-prompt persona",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the command line lacks %q: %s", want, joined)
		}
	}
	i := slices.Index(args, "--disallowedTools")
	if i < 0 || !strings.Contains(args[i+1], "WebFetch") || !strings.Contains(args[i+1], "Write") {
		t.Errorf("the deny list is missing or incomplete: %v", args)
	}
	if got := cfg.CommandLine(AgentRun{}); got[0] != "claude" {
		t.Errorf("the command line starts with %q", got[0])
	}
}

func TestSettingsAllowOnlyTheScenarioCommandsAndTheDocs(t *testing.T) {
	ws := t.TempDir()
	b, err := Settings(ws, []string{"kubectl", "lenny-ctl"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Allow       []string `json:"allow"`
			Deny        []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc.Permissions
	if p.DefaultMode != "dontAsk" {
		t.Errorf("default mode = %q", p.DefaultMode)
	}
	for _, want := range []string{"Bash(kubectl)", "Bash(kubectl *)", "Bash(lenny-ctl *)", "Read(./docs/**)"} {
		if !slices.Contains(p.Allow, want) {
			t.Errorf("allow lacks %q: %v", want, p.Allow)
		}
	}
	for _, a := range p.Allow {
		if strings.HasPrefix(a, "Bash(helm") || a == "Bash" || a == "Read" {
			t.Errorf("allow grants %q, which the scenario did not ask for", a)
		}
	}
	if !slices.Contains(p.Deny, "WebFetch") {
		t.Errorf("deny lacks WebFetch: %v", p.Deny)
	}
}

func TestBuildEnvPassesOnlyTheAllowedVariablesAndTheCredentials(t *testing.T) {
	host := []string{"HOME=/home/alice", "USER=alice", "PATH=/usr/bin", "ANTHROPIC_API_KEY=sk-secret", "AWS_SECRET_ACCESS_KEY=x", "KUBECONFIG=/home/alice/.kube/config", "LANG=C"}
	env := BuildEnv(host, "/opt/bin", map[string]string{"KUBECONFIG": "/trial/kubeconfig", "LENNY_API_URL": "http://127.0.0.1:1"})
	got := strings.Join(env, "\n")
	for _, want := range []string{"HOME=/home/alice", "USER=alice", "LANG=C", "PATH=/opt/bin" + string(os.PathListSeparator) + "/usr/bin", "KUBECONFIG=/trial/kubeconfig", "LENNY_API_URL=http://127.0.0.1:1"} {
		if !strings.Contains(got, want) {
			t.Errorf("env lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "/home/alice/.kube/config"} {
		if strings.Contains(got, leak) {
			t.Errorf("env leaks %q:\n%s", leak, got)
		}
	}
}
