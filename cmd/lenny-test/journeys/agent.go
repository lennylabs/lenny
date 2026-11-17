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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultModel is the journey agent's model (decision 4 of change 0157).
const DefaultModel = "claude-sonnet-5-5"

// TokenEnv is the variable Claude Code reads a `claude setup-token` token
// from (Claude Code's authentication documentation, "Generate a
// long-lived token").
const TokenEnv = "CLAUDE_CODE_OAUTH_TOKEN"

// Paths inside the agent container. The working directory holds the
// read-only documentation copy, /journey holds the read-only kubeconfigs,
// and the home directory is the trial's writable scratch directory.
const (
	ContainerWorkspace      = "/workspace"
	ContainerDocs           = "/workspace/docs"
	ContainerJourneyDir     = "/journey"
	ContainerHome           = "/home/agent"
	ContainerKubeconfig     = "/journey/kubeconfig"
	ContainerEmptyConfig    = "/journey/empty-kubeconfig"
	ContainerHarnessConfig  = "/journey/harness-kubeconfig"
	ContainerGatewayURL     = "http://127.0.0.1:8080"
	containerGatewayForward = "JOURNEY_GATEWAY_FORWARD"
)

// AgentConfig configures the journey agent: Claude Code in headless mode,
// in the journey agent container, on the owner's subscription token.
type AgentConfig struct {
	// Docker is the docker executable that starts the container.
	Docker string
	// Image is the agent image reference (ImageBuilder.Ensure).
	Image string
	// Model is the model the agent runs.
	Model string
	// RunCapUSD is the per-run cost cap. The harness stops the agent when
	// its running estimate reaches it, and passes it to Claude Code's
	// own --max-budget-usd as a second stop.
	RunCapUSD float64
	// Rates price the running estimate.
	Rates Rates
	// Token is the `claude setup-token` token. It reaches the container
	// through the docker process's environment and `-e TokenEnv`, so it
	// never appears on a command line or in a record.
	Token string
	// UID and GID are the user the container runs as, the owner of the
	// mounted scratch directory.
	UID, GID int
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
	// Name is the container's name.
	Name string
	// Network is the Docker network the container joins.
	Network string
	// DocsDir is the host directory mounted read-only at ContainerDocs.
	DocsDir string
	// JourneyDir is the host directory mounted read-only at
	// ContainerJourneyDir, holding the kubeconfigs.
	JourneyDir string
	// HomeDir is the host scratch directory mounted at ContainerHome.
	HomeDir string
	// Settings is the settings JSON, passed inline to --settings.
	Settings []byte
	// SystemPrompt is appended to Claude Code's system prompt.
	SystemPrompt string
	// Prompt is the user prompt.
	Prompt string
	// Env is the container's environment beyond the image's own: the
	// credentials the scenario grants. Values reach the container through
	// the docker process's environment; the command line names only the
	// variables.
	Env map[string]string
	// GatewayForward makes the entrypoint forward the gateway into the
	// container before the agent starts.
	GatewayForward bool
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

// ClaudeArgs returns the claude arguments for a run.
func (c AgentConfig) ClaudeArgs(run AgentRun) []string {
	return []string{
		"-p", run.Prompt,
		"--model", c.Model,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--max-budget-usd", fmt.Sprintf("%.2f", c.RunCapUSD),
		"--tools", strings.Join(AgentTools, ","),
		"--disallowedTools", strings.Join(DeniedTools, ","),
		"--settings", string(run.Settings),
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

// DockerArgs returns the docker arguments that run the agent: a
// read-only root file system, no capabilities, no new privileges, the
// trial's user, the agent's network, and three mounts (the documentation
// read-only, the kubeconfigs read-only, and the scratch home directory).
// No host home directory, Docker socket, or other host path is mounted,
// and every environment variable is passed by name.
func (c AgentConfig) DockerArgs(run AgentRun) []string {
	args := []string{
		"run", "--rm", "--init", "--interactive=false",
		"--name", run.Name,
		"--network", run.Network,
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,size=512m",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--user", strconv.Itoa(c.UID) + ":" + strconv.Itoa(c.GID),
		"--mount", "type=bind,source=" + run.DocsDir + ",target=" + ContainerDocs + ",readonly",
		"--mount", "type=bind,source=" + run.JourneyDir + ",target=" + ContainerJourneyDir + ",readonly",
		"--mount", "type=bind,source=" + run.HomeDir + ",target=" + ContainerHome,
		"--workdir", ContainerWorkspace,
		"--env", "HOME=" + ContainerHome,
		"--env", TokenEnv,
	}
	for _, k := range sortedStringKeys(run.Env) {
		args = append(args, "--env", k)
	}
	if run.GatewayForward {
		args = append(args, "--env", containerGatewayForward+"=1")
	}
	args = append(args, c.Image, "claude")
	return append(args, c.ClaudeArgs(run)...)
}

// CommandLine returns the full command line for the record. It holds no
// secret: the token and the credentials pass by name.
func (c AgentConfig) CommandLine(run AgentRun) []string {
	return append([]string{c.Docker}, c.DockerArgs(run)...)
}

// dockerEnv is the docker process's environment: the harness's own, which
// locates the Docker daemon, plus the values of the variables DockerArgs
// passes by name. Docker copies only the named variables into the
// container.
func (c AgentConfig) dockerEnv(run AgentRun) []string {
	env := os.Environ()
	env = append(env, TokenEnv+"="+c.Token)
	for _, k := range sortedStringKeys(run.Env) {
		env = append(env, k+"="+run.Env[k])
	}
	return env
}

// Settings returns the settings JSON for a scenario's tools. The allow
// list names a Bash rule for each allowed command and read rules for the
// container's docs/ directory, by relative and by absolute path. In
// dontAsk mode with prompts off, any tool use the allow list does not
// cover is refused without asking.
func Settings(commands []string) ([]byte, error) {
	allow := make([]string, 0, 2*len(commands)+4)
	for _, c := range commands {
		allow = append(allow, "Bash("+c+")", "Bash("+c+" *)")
	}
	allow = append(allow, "Read(./docs/**)", "Read(/"+ContainerDocs+"/**)", "Glob", "Grep")
	doc := map[string]any{
		"permissions": map[string]any{
			"defaultMode": "dontAsk",
			"allow":       allow,
			"deny":        DeniedTools,
		},
	}
	return json.Marshal(doc)
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Run starts the agent container, streams its output into the meter and
// the transcript, and stops it when the running cost estimate reaches the
// cap, the time limit passes, or ctx is cancelled. The container is
// removed on every path out.
func (c AgentConfig) Run(ctx context.Context, run AgentRun) (AgentResult, error) {
	start := time.Now()
	limitCtx, cancelLimit := context.WithTimeout(ctx, run.TimeLimit)
	defer cancelLimit()
	capCtx, stopForCap := context.WithCancel(limitCtx)
	defer stopForCap()
	defer c.removeContainer(run.Name)

	cmd := exec.CommandContext(capCtx, c.Docker, c.DockerArgs(run)...)
	cmd.Env = c.dockerEnv(run)
	cmd.Stderr = run.Stderr
	// docker run proxies SIGTERM to the container, where tini passes it
	// to claude; the deferred removal covers a container that outlives
	// the docker process.
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

// removeContainer force-removes the agent container. It is a no-op when
// docker run --rm already removed it.
func (c AgentConfig) removeContainer(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, c.Docker, "rm", "-f", name).Run()
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
