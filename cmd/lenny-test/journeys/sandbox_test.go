// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

type sandboxCommands struct {
	calls       []string
	internal    string
	forwardLog  string
	failPrefix  string
	containers  string
	networks    string
	removedBits []string
}

func (f *sandboxCommands) run(_ context.Context, _ io.Writer, _ []string, _, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.failPrefix != "" && strings.HasPrefix(call, f.failPrefix) {
		return "", errors.New("failed: " + call)
	}
	switch {
	case strings.HasPrefix(call, "docker network inspect"):
		return f.internal + "\n", nil
	case strings.Contains(call, "cat /tmp/forward.log"):
		return f.forwardLog, nil
	case strings.HasPrefix(call, "docker ps -a"):
		return f.containers, nil
	case strings.HasPrefix(call, "docker network ls"):
		return f.networks, nil
	case strings.HasPrefix(call, "docker rm -f"), strings.HasPrefix(call, "docker network rm"):
		f.removedBits = append(f.removedBits, args[len(args)-1])
	}
	return "", nil
}

func (f *sandboxCommands) find(sub string) string {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return c
		}
	}
	return ""
}

func sandboxRun(gateway bool) AgentRun {
	return AgentRun{
		Name: "lenny-journeys-agent-r1-demo-1", RunID: "r1", UpstreamNetwork: KindNetwork,
		Allow:      []string{"api.anthropic.com:443", "lenny-journeys-control-plane:6443"},
		HarnessDir: "/s/harness", GatewayForward: gateway,
	}
}

func TestSandboxPutsTheAgentOnAnInternalNetworkBehindTheProxy(t *testing.T) {
	f := &sandboxCommands{internal: "true", forwardLog: "Forwarding from 127.0.0.1:8080 -> 8080"}
	cfg := AgentConfig{Docker: "docker", Image: "sha256:img", UID: 501, GID: 20, Exec: f.run}
	cleanup, err := cfg.setupSandbox(context.Background(), sandboxRun(true))
	if err != nil {
		t.Fatal(err)
	}
	n := namesFor(sandboxRun(true))
	if c := f.find("network create"); !strings.Contains(c, "--internal") || !strings.Contains(c, RunLabel+"=r1") || !strings.HasSuffix(c, n.network) {
		t.Errorf("network create = %q", c)
	}
	proxy := f.find("--name " + n.proxy)
	for _, want := range []string{"--network " + n.network, "--entrypoint /usr/local/bin/journey-egress-proxy", "sha256:img", "--allow api.anthropic.com:443", "--allow lenny-journeys-control-plane:6443", "--read-only", "--cap-drop ALL"} {
		if !strings.Contains(proxy, want) {
			t.Errorf("the proxy run lacks %q: %s", want, proxy)
		}
	}
	if c := f.find("network connect"); c != "docker network connect "+KindNetwork+" "+n.proxy {
		t.Errorf("network connect = %q", c)
	}
	holder := f.find("--name " + n.holder)
	for _, want := range []string{"--network " + n.network, "source=/s/harness,target=/harness,readonly", "HTTPS_PROXY=" + n.proxyURL(), "--entrypoint sleep"} {
		if !strings.Contains(holder, want) {
			t.Errorf("the holder run lacks %q: %s", want, holder)
		}
	}
	if c := f.find("exec --detach"); !strings.Contains(c, "port-forward svc/lenny-gateway") {
		t.Errorf("the forward = %q", c)
	}
	cleanup()
	got := strings.Join(f.removedBits, " ")
	for _, want := range []string{n.agent, n.holder, n.proxy, n.network} {
		if !strings.Contains(got, want) {
			t.Errorf("cleanup did not remove %s: %s", want, got)
		}
	}
}

func TestSandboxHolderMountsNoHarnessKubeconfigWithoutTheGateway(t *testing.T) {
	f := &sandboxCommands{internal: "true"}
	cfg := AgentConfig{Docker: "docker", Image: "sha256:img", Exec: f.run}
	cleanup, err := cfg.setupSandbox(context.Background(), sandboxRun(false))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if holder := f.find("-netns"); strings.Contains(holder, "/harness") || strings.Contains(holder, "HTTPS_PROXY") {
		t.Errorf("the holder of a run without the gateway mounts the harness kubeconfig: %s", holder)
	}
	if f.find("exec") != "" {
		t.Error("a forward started without the gateway grant")
	}
}

func TestSandboxRefusesANetworkThatIsNotInternalAndCleansUp(t *testing.T) {
	f := &sandboxCommands{internal: "false"}
	cfg := AgentConfig{Docker: "docker", Image: "sha256:img", Exec: f.run}
	if _, err := cfg.setupSandbox(context.Background(), sandboxRun(false)); err == nil || !strings.Contains(err.Error(), "not internal") {
		t.Fatalf("err = %v", err)
	}
	if len(f.removedBits) == 0 {
		t.Error("a failed setup left its objects")
	}
	f2 := &sandboxCommands{internal: "true", failPrefix: "docker network connect"}
	cfg.Exec = f2.run
	if _, err := cfg.setupSandbox(context.Background(), sandboxRun(false)); err == nil {
		t.Error("a proxy that cannot reach the cluster network was accepted")
	}
}

func TestSandboxFailsWhenTheGatewayForwardDoesNotStart(t *testing.T) {
	f := &sandboxCommands{internal: "true", forwardLog: "error: services \"lenny-gateway\" not found"}
	cfg := AgentConfig{Docker: "docker", Image: "sha256:img", Exec: f.run, ForwardTimeout: time.Millisecond}
	if _, err := cfg.setupSandbox(context.Background(), sandboxRun(true)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	optional := sandboxRun(true)
	optional.ForwardOptional = true
	cleanup, err := cfg.setupSandbox(context.Background(), optional)
	if err != nil {
		t.Fatalf("an optional forward that fails: %v", err)
	}
	cleanup()
}

func TestCleanupLeftoversRemovesOnlyOtherRunsObjects(t *testing.T) {
	f := &sandboxCommands{
		containers: "old-agent r0\nold-proxy r0\ncurrent-agent r2\n",
		networks:   "old-net r0\ncurrent-net r2\n",
	}
	removed, err := CleanupLeftovers(context.Background(), f.run, "docker", "r2")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(removed, ", ")
	if got != "container old-agent, container old-proxy, network old-net" {
		t.Errorf("removed = %q", got)
	}
	f.failPrefix = "docker ps"
	if _, err := CleanupLeftovers(context.Background(), f.run, "docker", "r2"); err == nil {
		t.Error("a failed listing was ignored")
	}
}

func TestRunCheckReplacesClaudeWithTheScript(t *testing.T) {
	docker := fakeClaude(t, nil, 0, false)
	t.Setenv("CHECK_OUTPUT", "KUBECONFIG=/journey/empty-kubeconfig\nPROBE_DONE")
	cfg := AgentConfig{Docker: docker, Image: "sha256:img", Model: DefaultModel, RunCapUSD: 1}
	out, err := cfg.RunCheck(context.Background(), sandboxRun(false), "echo hi")
	if err != nil || !strings.Contains(out, "PROBE_DONE") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// TestSandboxNamesResolveAsDNSLabels pins that the names the holder resolves
// through Docker's embedded DNS fit a 63-character DNS label for a run name of
// the length a pilot produces, and that distinct runs get distinct names. A
// 76-character proxy name made every pilot trial fail to start the gateway
// port-forward with "no such host".
func TestSandboxNamesResolveAsDNSLabels(t *testing.T) {
	long := AgentRun{Name: "lenny-journeys-agent-20261118T080243Z-9396c2-tenant-admin-onboarding-3"}
	other := AgentRun{Name: "lenny-journeys-agent-20261118T080243Z-9396c2-tenant-admin-onboarding-2"}
	n, m := namesFor(long), namesFor(other)
	for _, name := range []string{n.network, n.proxy, n.holder} {
		if len(name) > 63 {
			t.Errorf("%q is %d characters, over the 63-character DNS label limit", name, len(name))
		}
	}
	if n.proxy == m.proxy || n.holder == m.holder || n.network == m.network {
		t.Errorf("distinct runs share names: %+v and %+v", n, m)
	}
	if n.agent != long.Name {
		t.Errorf("agent name = %q, want the run name %q", n.agent, long.Name)
	}
}
