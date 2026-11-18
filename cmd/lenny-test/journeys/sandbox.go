// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RunLabel labels every container and network a journeys run creates for
// its agent, with the run's identifier, so a later run removes what a dead
// one left.
const RunLabel = "lenny.dev/journeys-run"

// AnthropicHosts are the hosts the agent's egress proxy allows besides the
// trial cluster's API server: the Claude API, and the claude.ai account
// and OAuth hosts that Claude Code's network requirements list for a
// claude.ai subscription token (Claude Code documentation, "Network
// access requirements"). Telemetry, error reporting, updates, and the
// other nonessential traffic are off in the image, so their hosts are not
// allowed.
var AnthropicHosts = []string{"api.anthropic.com:443", "claude.ai:443", "platform.claude.com:443"}

// proxyPort is the port the egress proxy listens on.
const proxyPort = 3128

// sandboxNames are the names of one agent run's Docker objects.
type sandboxNames struct {
	network, proxy, holder, agent string
}

// namesFor derives the run's Docker object names. The holder resolves the
// proxy by its container name through Docker's embedded DNS, and a DNS label
// holds at most 63 characters (RFC 1035, section 2.3.4), so the proxy, the
// holder, and the network take a short name derived from a hash of the run
// name rather than the run name itself, which carries the run, journey, and
// trial and exceeds the limit. The agent container keeps the run name, which
// nothing resolves; every object also carries the run label for cleanup.
func namesFor(run AgentRun) sandboxNames {
	sum := sha256.Sum256([]byte(run.Name))
	base := "lj-" + hex.EncodeToString(sum[:6])
	return sandboxNames{
		network: base + "-net",
		proxy:   base + "-proxy",
		holder:  base + "-netns",
		agent:   run.Name,
	}
}

// proxyURL is the proxy's address as the agent sees it.
func (n sandboxNames) proxyURL() string {
	return "http://" + n.proxy + ":" + strconv.Itoa(proxyPort)
}

// hardening are the options every sandbox container runs with.
func (c AgentConfig) hardening() []string {
	return []string{
		"--read-only", "--tmpfs", "/tmp:rw,nosuid,size=512m",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", strconv.Itoa(c.UID) + ":" + strconv.Itoa(c.GID),
	}
}

// setupSandbox creates the agent's network and its two companion
// containers, and returns a function that removes all three objects and
// the agent container.
//
//   - The network is internal (docker network create --internal): it has
//     no route off the host and no gateway to host.docker.internal.
//   - The egress proxy joins that network and the cluster's network, and
//     tunnels only to the allowed hosts.
//   - The network-namespace holder joins that network and owns the
//     namespace the agent shares (--network container:). When the scenario
//     grants the gateway, the harness starts a kubectl port-forward inside
//     the holder, with the harness kubeconfig mounted in the holder alone,
//     so the agent reaches the gateway at 127.0.0.1:8080 and never sees
//     that kubeconfig.
func (c AgentConfig) setupSandbox(ctx context.Context, run AgentRun) (func(), error) {
	n := namesFor(run)
	label := RunLabel + "=" + run.RunID
	cleanup := func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, name := range []string{n.agent, n.holder, n.proxy} {
			_, _ = c.exec()(cctx, nil, nil, "", c.Docker, "rm", "-f", name)
		}
		_, _ = c.exec()(cctx, nil, nil, "", c.Docker, "network", "rm", n.network)
	}
	steps := []func() error{
		func() error { return c.createInternalNetwork(ctx, n.network, label) },
		func() error { return c.startProxy(ctx, run, n, label) },
		func() error { return c.startHolder(ctx, run, n, label) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			cleanup()
			return func() {}, err
		}
	}
	if run.GatewayForward {
		if run.ForwardOptional && c.ForwardTimeout == 0 {
			c.ForwardTimeout = 5 * time.Second
		}
		if err := c.startGatewayForward(ctx, n); err != nil && !run.ForwardOptional {
			cleanup()
			return func() {}, err
		}
	}
	return cleanup, nil
}

func (c AgentConfig) createInternalNetwork(ctx context.Context, name, label string) error {
	if _, err := c.exec()(ctx, nil, nil, "", c.Docker, "network", "create", "--internal", "--label", label, name); err != nil {
		return fmt.Errorf("create the agent network: %w", err)
	}
	out, err := c.exec()(ctx, nil, nil, "", c.Docker, "network", "inspect", "--format", "{{.Internal}}", name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("the agent network %s is not internal", name)
	}
	return nil
}

func (c AgentConfig) startProxy(ctx context.Context, run AgentRun, n sandboxNames, label string) error {
	args := append([]string{"run", "--detach", "--rm", "--name", n.proxy, "--label", label, "--network", n.network},
		c.hardening()...)
	args = append(args, "--entrypoint", "/usr/local/bin/journey-egress-proxy", c.Image, "--listen", ":"+strconv.Itoa(proxyPort))
	for _, a := range run.Allow {
		args = append(args, "--allow", a)
	}
	if _, err := c.exec()(ctx, nil, nil, "", c.Docker, args...); err != nil {
		return fmt.Errorf("start the egress proxy: %w", err)
	}
	if _, err := c.exec()(ctx, nil, nil, "", c.Docker, "network", "connect", run.UpstreamNetwork, n.proxy); err != nil {
		return fmt.Errorf("connect the egress proxy to %s: %w", run.UpstreamNetwork, err)
	}
	return nil
}

func (c AgentConfig) startHolder(ctx context.Context, run AgentRun, n sandboxNames, label string) error {
	args := append([]string{"run", "--detach", "--rm", "--init", "--name", n.holder, "--label", label, "--network", n.network},
		c.hardening()...)
	if run.GatewayForward {
		args = append(args,
			"--mount", "type=bind,source="+run.HarnessDir+",target="+holderHarnessDir+",readonly",
			"--env", "HTTPS_PROXY="+n.proxyURL(), "--env", "NO_PROXY=")
	}
	args = append(args, "--entrypoint", "sleep", c.Image, "infinity")
	if _, err := c.exec()(ctx, nil, nil, "", c.Docker, args...); err != nil {
		return fmt.Errorf("start the agent's network namespace: %w", err)
	}
	return nil
}

// holderHarnessDir is where the holder mounts the harness kubeconfig.
const holderHarnessDir = "/harness"

// startGatewayForward starts the port-forward inside the holder and waits
// until kubectl reports it.
func (c AgentConfig) startGatewayForward(ctx context.Context, n sandboxNames) error {
	forward := "kubectl --kubeconfig " + holderHarnessDir + "/kubeconfig -n " + GatewayNamespace +
		" port-forward svc/" + GatewayService + " 8080:" + strconv.Itoa(GatewayPort) + " >/tmp/forward.log 2>&1"
	if _, err := c.exec()(ctx, nil, nil, "", c.Docker, "exec", "--detach", n.holder, "sh", "-c", forward); err != nil {
		return fmt.Errorf("start the gateway port-forward: %w", err)
	}
	deadline := time.Now().Add(c.forwardTimeout())
	for {
		out, _ := c.exec()(ctx, nil, nil, "", c.Docker, "exec", n.holder, "cat", "/tmp/forward.log")
		if strings.Contains(out, "Forwarding from") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the gateway port-forward did not start: %s", clip(out))
		}
		if err := sleepCtx(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (c AgentConfig) forwardTimeout() time.Duration {
	if c.ForwardTimeout > 0 {
		return c.ForwardTimeout
	}
	return 90 * time.Second
}

func (c AgentConfig) exec() CommandRunner {
	if c.Exec != nil {
		return c.Exec
	}
	return ExecRunner
}

// CleanupLeftovers removes the agent containers and networks that runs
// other than currentRun left, found by RunLabel. The caller holds the
// journeys lock, so no other live run owns them.
func CleanupLeftovers(ctx context.Context, run CommandRunner, docker, currentRun string) ([]string, error) {
	var removed []string
	out, err := run(ctx, nil, nil, "", docker, "ps", "-a", "--filter", "label="+RunLabel, "--format", `{{.Names}} {{.Label "`+RunLabel+`"}}`)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, owner, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == "" || owner == currentRun {
			continue
		}
		if _, err := run(ctx, nil, nil, "", docker, "rm", "-f", name); err != nil {
			return removed, err
		}
		removed = append(removed, "container "+name)
	}
	out, err = run(ctx, nil, nil, "", docker, "network", "ls", "--filter", "label="+RunLabel, "--format", `{{.Name}} {{.Label "`+RunLabel+`"}}`)
	if err != nil {
		return removed, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, owner, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == "" || owner == currentRun {
			continue
		}
		if _, err := run(ctx, nil, nil, "", docker, "network", "rm", name); err != nil {
			return removed, err
		}
		removed = append(removed, "network "+name)
	}
	return removed, nil
}
