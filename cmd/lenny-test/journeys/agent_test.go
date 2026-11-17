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
// They run a fake docker, a shell script that prints recorded stream-json
// lines, so no container starts and no model is called.

// fakeClaude writes a fake docker. It logs every invocation to calls.log,
// answers the sandbox commands (network create, inspect, connect, and rm,
// detached runs, exec, and rm) at once, reports the gateway port-forward
// as started, and for the agent run records the token it received, prints
// lines, and then either exits with code or, when hang is set, replaces
// itself with a long sleep so only the harness's signal ends it. A run of
// `sh -c` in place of claude prints CHECK_OUTPUT.
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
	script := filepath.Join(dir, "docker")
	body := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
  rm) exit 0 ;;
  network) [ "$2" = inspect ] && echo true; exit 0 ;;
  exec) case "$*" in *forward.log*) echo "Forwarding from 127.0.0.1:8080 -> 8080" ;; esac; exit 0 ;;
esac
case "$*" in
  *--detach*) echo container-id; exit 0 ;;
  *" sh -c "*) echo "${CHECK_OUTPUT:-PROBE_DONE}"; exit 0 ;;
esac
printf '%%s' "$CLAUDE_CODE_OAUTH_TOKEN" > %q
cat %q
%s
`, filepath.Join(dir, "calls.log"), filepath.Join(dir, "token-seen"), data, tail)
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
		Name: "agent", RunID: "run1", UpstreamNetwork: KindNetwork, Allow: []string{"api.anthropic.com:443"},
		DocsDir: "/d", JourneyDir: "/j", HarnessDir: "/hk", HomeDir: "/h",
		Settings: []byte("{}"), Prompt: "p", SystemPrompt: "s",
		TimeLimit: limit, Transcript: &transcript, Stderr: &bytes.Buffer{},
	}, &transcript
}

func TestAgentStopsWhenTheRunningCostReachesTheCap(t *testing.T) {
	var lines []string
	lines = append(lines, `{"type":"system","subtype":"init"}`)
	lines = append(lines, response("msg_1", 2.5)...)
	lines = append(lines, response("msg_2", 2.5)...)
	lines = append(lines, response("msg_3", 2.5)...)
	cfg := AgentConfig{Docker: fakeClaude(t, lines, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
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
	cfg := AgentConfig{Docker: fakeClaude(t, lines, 0, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
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
	cfg := AgentConfig{Docker: fakeClaude(t, lines, 1, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
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
	cfg := AgentConfig{Docker: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
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
	cfg := AgentConfig{Docker: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 0, true), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
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
	cfg := AgentConfig{Docker: fakeClaude(t, []string{`{"type":"system","subtype":"init"}`}, 3, false), Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates}
	run, _ := agentRun(t, time.Minute)
	res, err := cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "no result message") || !strings.Contains(res.StopDetail, "exit status 3") {
		t.Fatalf("result = %+v", res)
	}

	lines := []string{`{"type":"result","subtype":"error_during_execution","is_error":true,"total_cost_usd":0.1,"errors":["API Error: 529 overloaded"]}`}
	cfg.Docker = fakeClaude(t, lines, 1, false)
	res, err = cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "overloaded") {
		t.Fatalf("an error result: %+v", res)
	}

	cfg.Docker = filepath.Join(t.TempDir(), "no-such-claude")
	res, err = cfg.Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopAgentError || !strings.Contains(res.StopDetail, "sandbox") {
		t.Fatalf("a missing binary: %+v", res)
	}
}

func TestAgentPassesTheTokenThroughTheEnvironmentOnly(t *testing.T) {
	lines := []string{`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.01}`}
	docker := fakeClaude(t, lines, 0, false)
	cfg := AgentConfig{Docker: docker, Image: "img", Model: DefaultModel, RunCapUSD: 6, Rates: SonnetRates, Token: "sk-ant-oat-secret-token"}
	run, _ := agentRun(t, time.Minute)
	if _, err := cfg.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	seen, err := os.ReadFile(filepath.Join(filepath.Dir(docker), "token-seen"))
	if err != nil || string(seen) != "sk-ant-oat-secret-token" {
		t.Fatalf("the docker process saw token %q, %v", seen, err)
	}
	if strings.Contains(strings.Join(cfg.CommandLine(run), " "), "sk-ant-oat") {
		t.Error("the token appears on the command line")
	}
}

func TestAgentArgsRestrictTheToolsAndCapTheBudget(t *testing.T) {
	cfg := AgentConfig{Docker: "docker", Model: DefaultModel, RunCapUSD: 6}
	args := cfg.ClaudeArgs(AgentRun{Prompt: "goal", SystemPrompt: "persona", Settings: []byte(`{"permissions":{}}`)})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-p goal", "--model claude-sonnet-5-5", "--output-format stream-json", "--include-partial-messages",
		"--max-budget-usd 6.00", "--tools Bash,Read,Glob,Grep", `--settings {"permissions":{}}`,
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
}

func TestDockerArgsIsolateTheAgentContainer(t *testing.T) {
	cfg := AgentConfig{Docker: "docker", Image: "sha256:abc", Model: DefaultModel, RunCapUSD: 6, UID: 501, GID: 20}
	run := AgentRun{
		Name: "agent-1", RunID: "run1", UpstreamNetwork: KindNetwork, DocsDir: "/s/docs", JourneyDir: "/s/journey",
		HarnessDir: "/s/harness", HomeDir: "/s/home", TimeLimit: 30 * time.Minute,
		Settings: []byte("{}"), Prompt: "p", SystemPrompt: "s", GatewayForward: true,
		Env: map[string]string{"KUBECONFIG": ContainerKubeconfig, "LENNY_API_TOKEN": "tok-secret-value"},
	}
	line := cfg.CommandLine(run)
	joined := strings.Join(line, " ")
	if line[0] != "docker" || line[1] != "run" {
		t.Fatalf("command line = %v", line)
	}
	for _, want := range []string{
		"--rm", "--init", "--name agent-1", "--label lenny.dev/journeys-run=run1",
		"--network container:agent-1-netns", "--read-only", "--cap-drop ALL",
		"--security-opt no-new-privileges", "--user 501:20",
		"type=bind,source=/s/docs,target=/workspace/docs,readonly",
		"type=bind,source=/s/journey,target=/journey,readonly",
		"type=bind,source=/s/home,target=/home/agent",
		"--workdir /workspace", "--env HOME=/home/agent", "--env CLAUDE_CODE_OAUTH_TOKEN",
		"--env HTTPS_PROXY=http://agent-1-proxy:3128", "--env HTTP_PROXY=http://agent-1-proxy:3128",
		"--env NO_PROXY= ", "--env JOURNEY_TIME_LIMIT=1800",
		"--env KUBECONFIG", "--env LENNY_API_TOKEN",
		"sha256:abc claude -p p",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the container command line lacks %q:\n%s", want, joined)
		}
	}
	home, _ := os.UserHomeDir()
	for _, leak := range []string{"docker.sock", "tok-secret-value", ".kube", ".aws", "--privileged", "-v " + home, "source=" + home + ",", "/s/harness", "--network kind", "--network " + KindNetwork} {
		if strings.Contains(joined, leak) {
			t.Errorf("the container command line holds %q:\n%s", leak, joined)
		}
	}
	mounts := 0
	for _, a := range line {
		if a == "--mount" || a == "-v" || a == "--volume" {
			mounts++
		}
	}
	if mounts != 3 {
		t.Errorf("the container has %d mounts, want the documentation, the kubeconfigs, and the home directory", mounts)
	}
	env := strings.Join(cfg.dockerEnv(run), "\n")
	if !strings.Contains(env, "LENNY_API_TOKEN=tok-secret-value") || !strings.Contains(env, TokenEnv+"=") {
		t.Errorf("the docker process lacks the named values")
	}
}

func TestSettingsAllowOnlyTheScenarioCommandsAndTheDocs(t *testing.T) {
	b, err := Settings([]string{"kubectl", "lenny-ctl"})
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
	for _, want := range []string{"Bash(kubectl)", "Bash(kubectl *)", "Bash(lenny-ctl *)", "Read(./docs/**)", "Read(//workspace/docs/**)"} {
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
