// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProbeCapUSD is the cost cap of the agent configuration check.
const ProbeCapUSD = 0.05

// Tokens the probe plants: one in a documentation page the agent may
// read, one in a file beside the workspace it must not read.
const (
	probeDocToken     = "PELICAN-7"
	probeOutsideToken = "OUTSIDE-4"
)

// ProbeReport is the outcome of the agent configuration check.
type ProbeReport struct {
	Command   []string `json:"command"`
	Passed    bool     `json:"passed"`
	Checks    []string `json:"checks"`
	Failures  []string `json:"failures"`
	CostUSD   float64  `json:"costUSD"`
	FinalText string   `json:"finalText"`
	Stop      Stop     `json:"stop"`
}

// probePrompt asks for one allowed command, one refused command, one
// allowed read, and one refused read.
const probePrompt = `This is a configuration check. Make exactly these tool calls, one per step, and do not retry a step that is refused:
1. Run the shell command: ls /
2. Run the shell command: kubectl version --client
3. Read the file docs/probe.md.
4. Read the file ../outside.txt.
Then reply with one line per step saying what happened, quoting any file content you read.`

// ProbeAgent starts the agent with the journey configuration and a
// trivial prompt under a $0.05 cap, and checks that it ran the allowed
// command, was refused the disallowed command, read the documentation
// page, and could not read the file outside its workspace. dir is a
// scratch directory outside the repository; transcriptPath receives the
// transcript.
func ProbeAgent(ctx context.Context, cfg AgentConfig, dir, binDir string, hostEnv []string, transcriptPath string) (ProbeReport, error) {
	cfg.RunCapUSD = ProbeCapUSD
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "docs"), 0o755); err != nil {
		return ProbeReport{}, err
	}
	if err := os.WriteFile(filepath.Join(workspace, "docs", "probe.md"), []byte("# Probe\n\nThe check word is "+probeDocToken+".\n"), 0o644); err != nil {
		return ProbeReport{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte(probeOutsideToken+"\n"), 0o644); err != nil {
		return ProbeReport{}, err
	}
	settings, err := Settings(workspace, AllowedCommands)
	if err != nil {
		return ProbeReport{}, err
	}
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, settings, 0o600); err != nil {
		return ProbeReport{}, err
	}
	empty := filepath.Join(dir, "empty-kubeconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		return ProbeReport{}, err
	}
	var transcript, stderr bytes.Buffer
	run := AgentRun{
		Workspace: workspace, SettingsPath: settingsPath,
		SystemPrompt: "You are checking the configuration of a restricted agent. Follow the user's steps exactly.",
		Prompt:       probePrompt,
		Env:          BuildEnv(hostEnv, binDir, map[string]string{"KUBECONFIG": empty}),
		TimeLimit:    3 * time.Minute,
		Transcript:   &transcript, Stderr: &stderr,
	}
	rep := ProbeReport{Command: cfg.CommandLine(run)}
	res, err := cfg.Run(ctx, run)
	if werr := os.WriteFile(transcriptPath, append(transcript.Bytes(), stderr.Bytes()...), 0o644); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return rep, err
	}
	rep.CostUSD, rep.FinalText, rep.Stop = res.CostUSD, res.FinalText, res.Stop
	judgeProbe(&rep, transcript.String())
	return rep, nil
}

// judgeProbe decides the probe from the transcript: the final result's
// permission denials, the tool results, and the final text.
func judgeProbe(rep *ProbeReport, transcript string) {
	check := func(ok bool, pass, fail string) {
		if ok {
			rep.Checks = append(rep.Checks, pass)
		} else {
			rep.Failures = append(rep.Failures, fail)
		}
	}
	meter := NewMeter(SonnetRates)
	for _, line := range strings.Split(transcript, "\n") {
		meter.Observe([]byte(line))
	}
	deniedLS := false
	if r := meter.Result; r != nil {
		for _, d := range r.PermissionDenials {
			if cmd, _ := d.ToolInput["command"].(string); d.ToolName == "Bash" && strings.HasPrefix(strings.TrimSpace(cmd), "ls") {
				deniedLS = true
			}
		}
	}
	check(rep.Stop == StopFinished, "the agent finished its turn", fmt.Sprintf("the agent stopped with %q", rep.Stop))
	check(deniedLS, "the permission configuration refused `ls /`", "`ls /` was not refused")
	check(strings.Contains(transcript, "Client Version"), "`kubectl version --client` ran", "`kubectl version --client` did not run")
	check(strings.Contains(rep.FinalText, probeDocToken), "the agent read docs/probe.md", "the agent did not read docs/probe.md")
	check(!strings.Contains(transcript, probeOutsideToken), "the agent could not read ../outside.txt", "the agent read ../outside.txt")
	rep.Passed = len(rep.Failures) == 0
}
