// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProbeCapUSD is the cost cap of the agent configuration check.
const ProbeCapUSD = 0.05

// probeDocToken is planted in the documentation page the probe reads.
const probeDocToken = "PELICAN-7"

// ProbeReport is the outcome of the agent configuration check.
type ProbeReport struct {
	Command   []string `json:"command"`
	Passed    bool     `json:"passed"`
	Checks    []string `json:"checks"`
	Failures  []string `json:"failures"`
	CostUSD   float64  `json:"costUSD"`
	FinalText string   `json:"finalText"`
	Stop      Stop     `json:"stop"`
	Teardown  string   `json:"teardown,omitempty"`
}

// ProbeInput is what the configuration check needs.
type ProbeInput struct {
	// Target creates the bare cluster the probe's kubectl reaches.
	Target Target
	// Scratch holds the probe's documentation, kubeconfig, and home
	// directories.
	Scratch string
	// HostHome is the host's home directory, which must not exist in the
	// container.
	HostHome string
	// HostContexts are the context names of the host's own kubeconfig,
	// none of which may appear in the transcript.
	HostContexts []string
	// Transcript receives the redacted transcript.
	Transcript io.Writer
	// Redactor removes the token and the kubeconfig secrets.
	Redactor *Redactor
	// Log receives the cluster's creation and deletion output.
	Log io.Writer
}

// probePrompt asks for one refused command, the allowed kubectl calls,
// the isolation checks, one allowed read, and one refused read.
func probePrompt(hostHome string) string {
	return fmt.Sprintf(`This is a configuration check. Make exactly these tool calls, one per step, and do not retry a step that fails or is refused:
1. Run the shell command: cat /etc/hostname
2. Run the shell command: kubectl get ns
3. Run the shell command: kubectl --kubeconfig ~/.kube/config config get-contexts
4. Run the shell command: ls %s
5. Run the shell command: ls /var/run/docker.sock
6. Read the file docs/probe.md.
7. Read the file /etc/hostname.
Then reply with one line per step saying what happened, quoting the output or the file content.`, hostHome)
}

// ProbeAgent creates a bare journey cluster, starts the agent container
// with the journey configuration and a trivial prompt under a $0.05 cap,
// deletes and verifies the cluster, and checks the transcript: a command
// outside the allow list is refused, kubectl reaches the trial cluster,
// the host kubeconfig, the host home directory, and the Docker socket are
// absent, the documentation is readable, and a file outside the working
// directory is not.
func ProbeAgent(ctx context.Context, cfg AgentConfig, in ProbeInput) (rep ProbeReport, err error) {
	cfg.RunCapUSD = ProbeCapUSD
	if err := in.Target.Create(ctx, SetupCluster, in.Log); err != nil {
		rep.Teardown = teardownProbe(in.Target, in.Log)
		return rep, fmt.Errorf("create the probe cluster: %w", err)
	}
	defer func() { rep.Teardown = teardownProbe(in.Target, in.Log) }()
	run, err := probeRun(ctx, in)
	if err != nil {
		return rep, err
	}
	defer func() { _ = UnlockDir(run.JourneyDir) }()
	var transcript bytes.Buffer
	run.Transcript = &transcript
	rep.Command = cfg.CommandLine(run)
	res, err := cfg.Run(ctx, run)
	if _, werr := in.Transcript.Write(in.Redactor.Redact(transcript.Bytes())); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return rep, err
	}
	rep.CostUSD, rep.FinalText, rep.Stop = res.CostUSD, in.Redactor.String(res.FinalText), res.Stop
	judgeProbe(&rep, transcript.String(), in.HostContexts, in.HostHome)
	return rep, nil
}

func probeRun(ctx context.Context, in ProbeInput) (AgentRun, error) {
	docs := filepath.Join(in.Scratch, "docs")
	journey := filepath.Join(in.Scratch, "journey")
	home := filepath.Join(in.Scratch, "home")
	for _, d := range []string{docs, journey, home} {
		if err := mkdirPrivate(d); err != nil {
			return AgentRun{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "probe.md"), []byte("# Probe\n\nThe check word is "+probeDocToken+".\n"), 0o644); err != nil {
		return AgentRun{}, err
	}
	ep, err := in.Target.AgentEndpoint(ctx)
	if err != nil {
		return AgentRun{}, err
	}
	grant, err := GrantCredentials(ctx, []string{CredKubeconfig}, ep, journey, nil)
	if err != nil {
		return AgentRun{}, err
	}
	in.Redactor.Add(grant.Secrets...)
	if err := LockDir(journey); err != nil {
		return AgentRun{}, err
	}
	// The probe alone may run ls, to show what the container lacks.
	settings, err := Settings(append(append([]string{}, AllowedCommands...), "ls"))
	if err != nil {
		return AgentRun{}, err
	}
	return AgentRun{
		Name: AgentImageRepo + "-probe", Network: ep.Network,
		DocsDir: docs, JourneyDir: journey, HomeDir: home, Settings: settings,
		SystemPrompt: "You are checking the configuration of a restricted agent. Follow the user's steps exactly.",
		Prompt:       probePrompt(in.HostHome), Env: grant.Env, TimeLimit: 4 * time.Minute,
		Stderr: io.Discard,
	}, nil
}

func teardownProbe(t Target, log io.Writer) string {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	if err := t.Delete(ctx, log); err != nil {
		return "delete: " + err.Error()
	}
	if err := t.VerifyDeleted(ctx); err != nil {
		return "verify: " + err.Error()
	}
	return "deleted and verified"
}

// toolCall is one tool use in a transcript and its result.
type toolCall struct {
	Name    string
	Input   map[string]any
	Result  string
	IsError bool
}

// parseToolCalls pairs each tool use in a stream-json transcript with its
// result.
func parseToolCalls(transcript string) []*toolCall {
	byID := map[string]*toolCall{}
	var order []*toolCall
	for _, line := range strings.Split(transcript, "\n") {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &l) != nil {
			continue
		}
		var items []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     map[string]any  `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if json.Unmarshal(l.Message.Content, &items) != nil {
			continue
		}
		for _, it := range items {
			switch it.Type {
			case "tool_use":
				c := &toolCall{Name: it.Name, Input: it.Input}
				byID[it.ID] = c
				order = append(order, c)
			case "tool_result":
				if c, ok := byID[it.ToolUseID]; ok {
					c.Result, c.IsError = contentText(it.Content), it.IsError
				}
			}
		}
	}
	return order
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return string(raw)
}

// commandResult returns the result of the first Bash call whose command
// starts with prefix, and whether one ran.
func commandResult(calls []*toolCall, prefix string) (*toolCall, bool) {
	for _, c := range calls {
		if cmd, _ := c.Input["command"].(string); c.Name == "Bash" && strings.HasPrefix(strings.TrimSpace(cmd), prefix) {
			return c, true
		}
	}
	return nil, false
}

// judgeProbe decides the probe from the transcript.
func judgeProbe(rep *ProbeReport, transcript string, hostContexts []string, hostHome string) {
	check := func(ok bool, pass, fail string) {
		if ok {
			rep.Checks = append(rep.Checks, pass)
		} else {
			rep.Failures = append(rep.Failures, fail)
		}
	}
	calls := parseToolCalls(transcript)
	refused := func(c *toolCall, ok bool) bool {
		return ok && c.IsError && (strings.Contains(c.Result, "denied") || strings.Contains(c.Result, "outside"))
	}
	missing := func(c *toolCall, ok bool) bool {
		return ok && strings.Contains(c.Result, "No such file or directory")
	}
	check(rep.Stop == StopFinished, "the agent finished its turn", fmt.Sprintf("the agent stopped with %q", rep.Stop))
	check(rep.CostUSD <= ProbeCapUSD, fmt.Sprintf("the probe cost $%.4f, within $%.2f", rep.CostUSD, ProbeCapUSD), fmt.Sprintf("the probe cost $%.4f, above $%.2f", rep.CostUSD, ProbeCapUSD))
	c, ok := commandResult(calls, "cat")
	check(refused(c, ok), "the permission configuration refused `cat /etc/hostname`", "`cat /etc/hostname` was not refused")
	c, ok = commandResult(calls, "kubectl get ns")
	check(ok && strings.Contains(c.Result, "kube-system"), "`kubectl get ns` reached the trial cluster", "`kubectl get ns` did not reach the trial cluster")
	c, ok = commandResult(calls, "kubectl --kubeconfig ~/.kube/config")
	hostSeen := false
	for _, name := range hostContexts {
		if name != "" && strings.Contains(transcript, name) {
			hostSeen = true
		}
	}
	check(ok && !hostSeen, "`kubectl --kubeconfig ~/.kube/config config get-contexts` found no host configuration",
		"the host kubeconfig was readable in the container, or the command did not run")
	c, ok = commandResult(calls, "ls /var/run/docker.sock")
	check(missing(c, ok), "the Docker socket is absent", "the Docker socket is present, or `ls /var/run/docker.sock` did not run")
	c, ok = commandResult(calls, "ls "+hostHome)
	check(missing(c, ok), "the host home directory is absent", "the host home directory is present, or its `ls` did not run")
	check(strings.Contains(rep.FinalText, probeDocToken), "the agent read docs/probe.md", "the agent did not read docs/probe.md")
	readRefused := false
	for _, call := range calls {
		if p, _ := call.Input["file_path"].(string); call.Name == "Read" && strings.HasSuffix(p, "/etc/hostname") && call.IsError {
			readRefused = true
		}
	}
	check(readRefused, "reading /etc/hostname, outside the working directory, was refused", "reading /etc/hostname was not refused")
	rep.Passed = len(rep.Failures) == 0
}

// HostContexts returns the context names of the kubeconfig at path, or
// none when it is missing.
func HostContexts(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Contexts []struct {
			Name string `yaml:"name"`
		} `yaml:"contexts"`
	}
	if yaml.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []string
	for _, c := range doc.Contexts {
		out = append(out, c.Name)
	}
	return out
}
