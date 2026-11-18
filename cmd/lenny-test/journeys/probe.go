// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// StopDetail says why the agent stopped; on an agent error it ends
	// with the last line of the container's standard error.
	StopDetail string `json:"stopDetail,omitempty"`
	ExitCode   int    `json:"exitCode"`
	Teardown   string `json:"teardown,omitempty"`
}

// ProbeInput is what the configuration check needs.
type ProbeInput struct {
	// Target creates the bare cluster the probe's kubectl reaches.
	Target Target
	// RunID labels the probe's Docker objects.
	RunID string
	// Scratch holds the probe's documentation, kubeconfig, and home
	// directories.
	Scratch string
	// HostHome is the host's home directory, which must not exist in the
	// container.
	HostHome string
	// HostContexts are the context names of the host's own kubeconfig,
	// none of which may appear in the result of the step that lists
	// contexts.
	HostContexts []string
	// Transcript receives the redacted transcript.
	Transcript io.Writer
	// Stderr receives the agent container's redacted standard error.
	Stderr io.Writer
	// Redactor removes the token and the kubeconfig secrets.
	Redactor *Redactor
	// Log receives the cluster's creation and deletion output.
	Log io.Writer
}

// probeRefusedTargets are the addresses the egress proxy must refuse: an
// internet host, the Docker host, and another Kind cluster's API server.
var probeRefusedTargets = []string{"https://example.com", "https://host.docker.internal:6443", "https://lenny-e2e-control-plane:6443"}

// probePrompt asks for one refused command, the allowed kubectl call, the
// host kubeconfig check, the egress checks, one allowed read, and one
// refused read. Claude Code refuses an ls of a path outside the working
// directory whatever the allow list says, so the checks that host paths
// are absent run without the model (hostPathCheck).
func probePrompt() string {
	return fmt.Sprintf(`This is a configuration check. Make exactly these tool calls, one per step, and do not retry a step that fails or is refused:
1. Run the shell command: cat /etc/hostname
2. Run the shell command: kubectl get ns
3. Run the shell command: kubectl --kubeconfig ~/.kube/config config get-contexts
4. Run the shell command: kubectl --server %s get ns
5. Run the shell command: kubectl --server %s get ns
6. Run the shell command: kubectl --server %s get ns
7. Read the file docs/probe.md.
8. Read the file /etc/hostname.
Then reply with one line per step saying what happened, quoting the output or the file content.`,
		probeRefusedTargets[0], probeRefusedTargets[1], probeRefusedTargets[2])
}

// hostPathCheck runs in the agent container with the agent's grant and
// prints a marker for each host path that must not be there: the host's
// home directory, its parent when that is /Users, and the Docker socket.
func hostPathCheck(hostHome string) string {
	check := fmt.Sprintf(`if [ -e %q ]; then echo HOME_PRESENT; fi
if [ -e /var/run/docker.sock ] || [ -e /run/docker.sock ]; then echo SOCKET_PRESENT; fi
`, hostHome)
	if strings.HasPrefix(hostHome, "/Users/") {
		check += "if [ -e /Users ]; then echo HOME_PRESENT; fi\n"
	}
	return check + "echo PROBE_DONE"
}

// gatewayOnlyCheck runs in the agent container of a gateway-url-only
// grant. It prints a marker for each thing that must not be there.
const gatewayOnlyCheck = `for f in /journey/*; do if [ -s "$f" ]; then echo "NONEMPTY $f"; fi; done
if [ -e /harness/kubeconfig ]; then echo HARNESS_VISIBLE; fi
if kubectl get ns >/dev/null 2>&1; then echo KUBECTL_REACHED; fi
echo "KUBECONFIG=$KUBECONFIG"
echo PROBE_DONE`

// RunProbe reserves the probe's cap in the pilot's ledger, runs
// ProbeAgent, and settles the entry at the probe's cost on every path. It
// returns the report and the exit code: 0 when the probe passed, 1 when
// it failed, and 2 when it could not run.
func RunProbe(ctx context.Context, cfg AgentConfig, in ProbeInput, ledger *Ledger) (ProbeReport, int, error) {
	entry := LedgerEntry{ID: EntryID(in.RunID, "probe", 1), Run: in.RunID, Journey: "probe", Trial: 1, Target: in.Target.Name(), At: time.Now().UTC()}
	if err := ledger.Reserve(entry, ProbeCapUSD, in.Target.MaxCostUSD()); err != nil {
		return ProbeReport{}, 2, err
	}
	rep, probeErr := ProbeAgent(ctx, cfg, in)
	entry.AgentUSD, entry.InfraUSD, entry.At = rep.CostUSD, in.Target.CostUSD(), time.Now().UTC()
	entry.Verdict = fmt.Sprintf("probe passed=%v", rep.Passed)
	if err := ledger.Settle(entry); err != nil {
		return rep, 2, errors.Join(probeErr, err)
	}
	switch {
	case probeErr != nil:
		return rep, 2, probeErr
	case !rep.Passed:
		return rep, 1, nil
	}
	return rep, 0, nil
}

// ProbeAgent creates a bare journey cluster, starts the agent sandbox
// with the journey configuration and a trivial prompt under a $0.05 cap,
// runs a model-free check of a gateway-url-only grant, deletes and
// verifies the cluster, and checks the results: a command outside the
// allow list is refused, kubectl reaches the trial cluster through the
// egress proxy, the proxy refuses an internet host, the Docker host, and
// another cluster, the host kubeconfig, the host home directory, and the
// Docker socket are absent, the documentation is readable, a file outside
// the working directory is not, and a gateway-url-only grant mounts no
// kubeconfig.
func ProbeAgent(ctx context.Context, cfg AgentConfig, in ProbeInput) (rep ProbeReport, err error) {
	cfg.RunCapUSD = ProbeCapUSD
	if err := in.Target.Create(ctx, SetupCluster, in.Log); err != nil {
		rep.Teardown = teardownProbe(in.Target, in.Log)
		return rep, fmt.Errorf("create the probe cluster: %w", err)
	}
	defer func() { rep.Teardown = teardownProbe(in.Target, in.Log) }()
	ep, err := in.Target.AgentEndpoint(ctx)
	if err != nil {
		return rep, err
	}
	run, unlock, err := probeRun(ctx, in, ep, "agent", []string{CredKubeconfig})
	defer unlock()
	if err != nil {
		return rep, err
	}
	var transcript, stderr bytes.Buffer
	run.Transcript, run.Stderr = &transcript, &stderr
	rep.Command = cfg.CommandLine(run)
	res, err := cfg.Run(ctx, run)
	rep.CostUSD, rep.FinalText, rep.Stop = res.CostUSD, in.Redactor.String(res.FinalText), res.Stop
	rep.StopDetail, rep.ExitCode = in.Redactor.String(res.StopDetail), res.ExitCode
	// Transcript and Stderr can be any writer, so the probe redacts
	// before writing. lenny-test passes record streams that redact again,
	// which leaves redacted text unchanged.
	if _, werr := in.Transcript.Write(in.Redactor.Redact(transcript.Bytes())); werr != nil && err == nil {
		err = werr
	}
	if in.Stderr != nil {
		if _, werr := in.Stderr.Write(in.Redactor.Redact(stderr.Bytes())); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return rep, err
	}
	judgeProbe(&rep, transcript.String(), in.HostContexts)
	paths, err := cfg.RunCheck(ctx, run, hostPathCheck(in.HostHome))
	judgeHostPaths(&rep, in.Redactor.String(paths), err)
	gw, unlockGW, err := probeRun(ctx, in, ep, "gateway", []string{CredGatewayURL})
	defer unlockGW()
	if err != nil {
		return rep, err
	}
	// The bare cluster has no gateway, so the forward cannot start; the
	// check is of what the agent container mounts.
	gw.ForwardOptional = true
	out, err := cfg.RunCheck(ctx, gw, gatewayOnlyCheck)
	judgeGatewayOnly(&rep, in.Redactor.String(out), err)
	return rep, nil
}

func probeRun(ctx context.Context, in ProbeInput, ep AgentEndpoint, variant string, creds []string) (AgentRun, func(), error) {
	base := filepath.Join(in.Scratch, variant)
	docs := filepath.Join(base, "docs")
	journey := filepath.Join(base, "journey")
	harness := filepath.Join(base, "harness")
	home := filepath.Join(base, "home")
	unlock := func() {
		_ = UnlockDir(journey)
		_ = UnlockDir(harness)
	}
	for _, d := range []string{docs, journey, harness, home} {
		if err := mkdirPrivate(d); err != nil {
			return AgentRun{}, unlock, err
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "probe.md"), []byte("# Probe\n\nThe check word is "+probeDocToken+".\n"), 0o644); err != nil {
		return AgentRun{}, unlock, err
	}
	grant, err := GrantCredentials(ctx, creds, ep, journey, harness, nil)
	if err != nil {
		return AgentRun{}, unlock, err
	}
	in.Redactor.Add(grant.Secrets...)
	for _, d := range []string{journey, harness} {
		if err := LockDir(d); err != nil {
			return AgentRun{}, unlock, err
		}
	}
	settings, err := Settings(AllowedCommands)
	if err != nil {
		return AgentRun{}, unlock, err
	}
	return AgentRun{
		Name: AgentImageRepo + "-probe-" + in.RunID + "-" + variant, RunID: in.RunID,
		UpstreamNetwork: ep.Network, Allow: append(append([]string{}, AnthropicHosts...), ep.APIServer),
		DocsDir: docs, JourneyDir: journey, HarnessDir: harness, HomeDir: home, Settings: settings,
		SystemPrompt: "You are checking the configuration of a restricted agent. Follow the user's steps exactly.",
		Prompt:       probePrompt(), Env: grant.Env, GatewayForward: grant.GatewayForward,
		TimeLimit: 4 * time.Minute, Stderr: io.Discard,
	}, unlock, nil
}

// judgeHostPaths decides the host path check from its output.
func judgeHostPaths(rep *ProbeReport, out string, err error) {
	switch {
	case err != nil || !strings.Contains(out, "PROBE_DONE"):
		rep.Failures = append(rep.Failures, fmt.Sprintf("the host path check did not run: %v %s", err, clip(out)))
	default:
		if strings.Contains(out, "HOME_PRESENT") {
			rep.Failures = append(rep.Failures, "the host home directory is present in the agent container")
		} else {
			rep.Checks = append(rep.Checks, "the host home directory is absent")
		}
		if strings.Contains(out, "SOCKET_PRESENT") {
			rep.Failures = append(rep.Failures, "the Docker socket is present in the agent container")
		} else {
			rep.Checks = append(rep.Checks, "the Docker socket is absent")
		}
	}
	rep.Passed = len(rep.Failures) == 0
}

// judgeGatewayOnly decides the gateway-url-only check from its output.
func judgeGatewayOnly(rep *ProbeReport, out string, err error) {
	switch {
	case err != nil || !strings.Contains(out, "PROBE_DONE"):
		rep.Failures = append(rep.Failures, fmt.Sprintf("the gateway-url-only check did not run: %v %s", err, clip(out)))
	case strings.Contains(out, "NONEMPTY") || strings.Contains(out, "HARNESS_VISIBLE") || strings.Contains(out, "KUBECTL_REACHED"):
		rep.Failures = append(rep.Failures, "a gateway-url-only grant gave the agent a kubeconfig: "+clip(out))
	case !strings.Contains(out, "KUBECONFIG="+ContainerEmptyConfig):
		rep.Failures = append(rep.Failures, "a gateway-url-only grant set KUBECONFIG to something other than the empty file: "+clip(out))
	default:
		rep.Checks = append(rep.Checks, "a gateway-url-only grant mounts no kubeconfig and reaches no cluster")
	}
	rep.Passed = len(rep.Failures) == 0
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
func judgeProbe(rep *ProbeReport, transcript string, hostContexts []string) {
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
	check(rep.Stop == StopFinished, "the agent finished its turn", fmt.Sprintf("the agent stopped with %q", rep.Stop))
	check(rep.CostUSD <= ProbeCapUSD, fmt.Sprintf("the probe cost $%.4f, within $%.2f", rep.CostUSD, ProbeCapUSD), fmt.Sprintf("the probe cost $%.4f, above $%.2f", rep.CostUSD, ProbeCapUSD))
	c, ok := commandResult(calls, "cat")
	check(refused(c, ok), "the permission configuration refused `cat /etc/hostname`", "`cat /etc/hostname` was not refused")
	c, ok = commandResult(calls, "kubectl get ns")
	check(ok && strings.Contains(c.Result, "kube-system"), "`kubectl get ns` reached the trial cluster", "`kubectl get ns` did not reach the trial cluster")
	c, ok = commandResult(calls, "kubectl --kubeconfig ~/.kube/config")
	hostSeen := false
	if ok {
		for _, line := range strings.Split(c.Result, "\n") {
			fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "*"))
			for _, name := range hostContexts {
				if name != "" && len(fields) > 0 && fields[0] == name {
					hostSeen = true
				}
			}
		}
	}
	check(ok && !hostSeen, "`kubectl --kubeconfig ~/.kube/config config get-contexts` found no host configuration",
		"the host kubeconfig was readable in the container, or the command did not run")
	for _, target := range probeRefusedTargets {
		c, ok = commandResult(calls, "kubectl --server "+target)
		refusedByProxy := ok && (strings.Contains(c.Result, "Forbidden") || strings.Contains(c.Result, "403")) && !strings.Contains(c.Result, "kube-system")
		check(refusedByProxy, "the egress proxy refused "+target, "the agent reached "+target+", or the step did not run")
	}
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
