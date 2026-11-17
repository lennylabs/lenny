// SPDX-License-Identifier: MIT

package journeys

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DefaultModel is the journey agent's model (decision 4 of change 0157).
const DefaultModel = "claude-sonnet-5-5"

// AgentConfig configures the journey agent: Claude Code in headless mode
// on the user's existing Claude login.
type AgentConfig struct {
	// Binary is the claude executable.
	Binary string
	// Model is the model the agent runs.
	Model string
	// RunCapUSD is the per-run cost cap. The harness stops the agent when
	// its running estimate reaches it, and passes it to Claude Code's
	// own --max-budget-usd as a second stop.
	RunCapUSD float64
	// Rates price the running estimate.
	Rates Rates
}

// DeniedTools are Claude Code's tools the journey agent never gets. The
// --tools flag already limits the built-in set to Bash, Read, Glob, and
// Grep; the deny list states the refusal explicitly in the permission
// configuration as well.
var DeniedTools = []string{"Write", "Edit", "NotebookEdit", "WebFetch", "WebSearch", "Agent", "Task"}

// AgentTools are the built-in tools the agent gets. Bash is limited by
// the permission allow list to the scenario's commands; Read, Glob, and
// Grep are confined by --restricted to the working directory, which
// holds only the documentation.
var AgentTools = []string{"Bash", "Read", "Glob", "Grep"}

// AgentRun is one invocation of the agent.
type AgentRun struct {
	// Workspace is the agent's working directory. It holds a copy of
	// docs/ and nothing else.
	Workspace string
	// SettingsPath is the settings file written by WriteSettings.
	SettingsPath string
	// SystemPrompt is appended to Claude Code's system prompt.
	SystemPrompt string
	// Prompt is the user prompt.
	Prompt string
	// Env is the agent's whole environment. The harness builds it
	// explicitly (BuildEnv) rather than inheriting its own.
	Env []string
	// TimeLimit bounds the run.
	TimeLimit time.Duration
	// Transcript receives every stream-json line except partial-message
	// stream events.
	Transcript io.Writer
	// Stderr receives Claude Code's standard error.
	Stderr io.Writer
}

// AgentResult is what the harness learned from one run.
type AgentResult struct {
	Stop         Stop    `json:"stop"`
	StopDetail   string  `json:"stopDetail"`
	CostUSD      float64 `json:"costUSD"`
	CostReported bool    `json:"costReported"`
	Tokens       Tokens  `json:"tokens"`
	NumTurns     int     `json:"numTurns"`
	FinalText    string  `json:"finalText"`
	Duration     string  `json:"duration"`
}

// Agent runs the journey agent. The runner depends on this interface so
// its tests substitute a fake.
type Agent interface {
	Run(ctx context.Context, run AgentRun) (AgentResult, error)
	CommandLine(run AgentRun) []string
}

// Args returns the claude arguments for a run, without the binary.
func (c AgentConfig) Args(run AgentRun) []string {
	return []string{
		"-p", run.Prompt,
		"--model", c.Model,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--max-budget-usd", fmt.Sprintf("%.2f", c.RunCapUSD),
		"--tools", strings.Join(AgentTools, ","),
		"--disallowedTools", strings.Join(DeniedTools, ","),
		"--settings", run.SettingsPath,
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--restricted",
		"--safe-mode",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--append-system-prompt", run.SystemPrompt,
	}
}

// CommandLine returns the full command line for the record.
func (c AgentConfig) CommandLine(run AgentRun) []string {
	return append([]string{c.Binary}, c.Args(run)...)
}

// Settings returns the settings file content for a scenario's tools. The
// allow list names a Bash rule for each allowed command and read rules
// for the workspace's docs/ directory, by relative and by absolute path.
// In dontAsk mode with prompts off, any tool use the allow list does not
// cover is refused without asking.
func Settings(workspace string, commands []string) ([]byte, error) {
	allow := make([]string, 0, 2*len(commands)+4)
	for _, c := range commands {
		allow = append(allow, "Bash("+c+")", "Bash("+c+" *)")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	allow = append(allow, "Read(./docs/**)", "Read(/"+filepath.ToSlash(abs)+"/docs/**)", "Glob", "Grep")
	doc := map[string]any{
		"permissions": map[string]any{
			"defaultMode": "dontAsk",
			"allow":       allow,
			"deny":        DeniedTools,
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// hostEnvAllowed names the host variables the agent inherits. They
// locate the user's home directory and Claude login and set the locale.
// Every other variable, including any API key or cloud credential in the
// host's environment, stays out: the agent runs on the user's Claude
// login (decision 4) and receives only the credentials its scenario
// names.
var hostEnvAllowed = []string{"HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR", "SHELL", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME"}

// BuildEnv returns the agent's environment: the allowed host variables,
// a PATH with binDir first, and the credentials in creds, which the
// caller selects from what the target provides by the scenario's
// credentials list.
func BuildEnv(host []string, binDir string, creds map[string]string) []string {
	hostVals := map[string]string{}
	for _, kv := range host {
		if k, v, ok := strings.Cut(kv, "="); ok {
			hostVals[k] = v
		}
	}
	var env []string
	for _, k := range hostEnvAllowed {
		if v, ok := hostVals[k]; ok {
			env = append(env, k+"="+v)
		}
	}
	path := hostVals["PATH"]
	if binDir != "" {
		path = binDir + string(os.PathListSeparator) + path
	}
	env = append(env, "PATH="+path)
	for _, k := range sortedStringKeys(creds) {
		env = append(env, k+"="+creds[k])
	}
	return env
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Run starts Claude Code, streams its output into the meter and the
// transcript, and stops it when the running cost estimate reaches the
// cap, the time limit passes, or ctx is cancelled.
func (c AgentConfig) Run(ctx context.Context, run AgentRun) (AgentResult, error) {
	start := time.Now()
	limitCtx, cancelLimit := context.WithTimeout(ctx, run.TimeLimit)
	defer cancelLimit()
	capCtx, stopForCap := context.WithCancel(limitCtx)
	defer stopForCap()

	cmd := exec.CommandContext(capCtx, c.Binary, c.Args(run)...)
	cmd.Dir = run.Workspace
	cmd.Env = run.Env
	cmd.Stderr = run.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return AgentResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return AgentResult{Stop: StopAgentError, StopDetail: "start: " + err.Error()}, nil
	}

	meter := NewMeter(c.Rates)
	capHit, readErr := c.consume(stdout, meter, run.Transcript, stopForCap)
	waitErr := cmd.Wait()

	res := AgentResult{Duration: time.Since(start).Round(time.Second).String()}
	res.CostUSD, res.CostReported = meter.Cost()
	res.Tokens = meter.Tokens()
	if r := meter.Result; r != nil {
		res.NumTurns = r.NumTurns
		res.FinalText = r.Result
	}
	res.Stop, res.StopDetail = classifyStop(ctx, limitCtx, capHit, meter, waitErr, readErr, c.RunCapUSD)
	return res, nil
}

// consume reads stdout line by line until it closes. It returns whether
// the cost cap stopped the run.
func (c AgentConfig) consume(r io.Reader, meter *Meter, transcript io.Writer, stop func()) (bool, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	capHit := false
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if meter.Observe(line) != StreamEventType && transcript != nil {
				if _, werr := transcript.Write(line); werr != nil {
					return capHit, werr
				}
			}
			if !capHit && meter.Estimate() >= c.RunCapUSD {
				capHit = true
				stop()
			}
		}
		if errors.Is(err, io.EOF) {
			return capHit, nil
		}
		if err != nil {
			return capHit, err
		}
	}
}

// classifyStop decides why the run ended, in order of precedence: the
// cost cap (the harness's or Claude Code's), the time limit, an outside
// cancellation, a finished turn, and otherwise an agent error.
func classifyStop(parent, limitCtx context.Context, capHit bool, meter *Meter, waitErr, readErr error, capUSD float64) (Stop, string) {
	r := meter.Result
	switch {
	case capHit:
		return StopCostCap, fmt.Sprintf("the running estimate $%.4f reached the $%.2f cap and the harness stopped the agent", meter.Estimate(), capUSD)
	case r != nil && r.Subtype == ResultSubtypeBudget:
		return StopCostCap, fmt.Sprintf("Claude Code stopped at its $%.2f budget: %s", capUSD, strings.Join(r.Errors, "; "))
	case parent.Err() != nil:
		return StopInterrupted, parent.Err().Error()
	case errors.Is(limitCtx.Err(), context.DeadlineExceeded):
		return StopTimeLimit, "the agent was still running at the scenario's time limit"
	case r != nil && !r.IsError:
		return StopFinished, r.Subtype
	}
	detail := []string{}
	if r != nil {
		detail = append(detail, "result "+r.Subtype+": "+strings.Join(r.Errors, "; "))
	} else {
		detail = append(detail, "no result message")
	}
	if waitErr != nil {
		detail = append(detail, "exit: "+waitErr.Error())
	}
	if readErr != nil {
		detail = append(detail, "read: "+readErr.Error())
	}
	return StopAgentError, strings.Join(detail, "; ")
}
