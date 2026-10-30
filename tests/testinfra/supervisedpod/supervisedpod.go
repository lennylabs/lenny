// SPDX-License-Identifier: MIT

// Package supervisedpod holds the cluster helpers the runtime supervisor's
// tier-5, tier-8, tier-9, and tier-10 tests share: the supervised probe
// pools tests/testinfra/kind seeds, running a probe subcommand in a pod's
// runtime container at the agent UID, sending probe commands through a
// session, reading a container's host PID through the node's container
// runtime, and watching a pod until it is gone.
//
// spec: §4.7.10 (Runtime supervisor); §4.7.11 item 8; §5.2 (Supervised scrub
// sequence).
package supervisedpod

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// Namespace is the agent namespace the Kind install warms the pools in.
const Namespace = "lenny-agents"

// The supervised probe pools and their runtimes, seeded by
// tests/testinfra/kind/install.sh.
const (
	KeepPool          = "supervise-probe-keep-pool"
	KeepRuntime       = "supervise-probe-keep"
	PreconnectPool    = "supervise-probe-preconnect-pool"
	PreconnectRuntime = "supervise-probe-preconnect"
	PodWarmPool       = "supervise-probe-podwarm-pool"
	PodWarmRuntime    = "supervise-probe-podwarm"
	ServicePool       = "supervise-probe-service-pool"
	ServiceRuntime    = "supervise-probe-service"
	// IdlePool is a keep pool a test returns a used pod to idle on, pinned
	// to the tenant that used it (IdleUsedPod).
	IdlePool    = "supervise-probe-idle-pool"
	IdleRuntime = "supervise-probe-idle"
)

// ProbeExe is the probe binary in the probe image, which is also the
// Runtime's `command`.
const ProbeExe = "/usr/local/bin/lenny"

// DialDelay is the PROBE_DIAL_DELAY the preConnect and service probe
// Runtimes set in tests/testinfra/kind/agent-workload.yaml. Only the pod's
// warm-up launch sleeps it; a later launch in the same pod dials at once.
const DialDelay = 30 * time.Second

// RestartPool and RestartRuntime name the supervised `restart` pool and its
// probe Runtime, which tests/testinfra/kind/install.sh does not seed while
// gateway pool admission refuses recycle.runtimeProcess: restart. The cases
// written against them skip with a blocked reason until the pool is seeded.
// The pool mirrors KeepPool under recycle.runtimeProcess: restart, and its
// cleanupCommands run the probe's cleanup-record and SubDaemonChild
// subcommands, so a cleanup command leaves a background process for the
// supervised scrub to remove. spec: §5.2 (Runtime-process lifetime;
// Supervised scrub sequence).
const (
	RestartPool    = "supervise-probe-restart-pool"
	RestartRuntime = "supervise-probe-restart"
)

// ExecRuntime runs the probe binary with args in pod's runtime container,
// which runs at the agent UID, and returns its stdout and stderr.
func ExecRuntime(t testing.TB, c *kind.Cluster, pod string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"-n", Namespace, "exec", pod, "-c", "runtime", "--", ProbeExe}, args...)
	return c.KubectlOut(t, full...)
}

// Attack runs one probe attack in pod's runtime container and decodes its
// result. A failure to run the attack at all fails the test.
func Attack(t testing.TB, c *kind.Cluster, pod, attack string, args ...string) superviseprobe.AttackResult {
	t.Helper()
	out, err := ExecRuntime(t, c, pod, append([]string{superviseprobe.SubAttack, attack}, args...)...)
	if err != nil {
		t.Fatalf("run attack %s in %s: %v\n%s", attack, pod, err, out)
	}
	var r superviseprobe.AttackResult
	if err := json.Unmarshal([]byte(lastLine(out)), &r); err != nil {
		t.Fatalf("decode attack %s result %q: %v", attack, out, err)
	}
	return r
}

// Processes lists the processes of pod's runtime container.
func Processes(t testing.TB, c *kind.Cluster, pod string) []superviseprobe.Process {
	t.Helper()
	out, err := ExecRuntime(t, c, pod, superviseprobe.SubProcesses)
	if err != nil {
		t.Fatalf("list processes in %s: %v\n%s", pod, err, out)
	}
	var procs []superviseprobe.Process
	if err := json.Unmarshal([]byte(lastLine(out)), &procs); err != nil {
		t.Fatalf("decode process list %q: %v", out, err)
	}
	return procs
}

// ServingProbes returns the serving probe processes of a process list, the
// runtime processes the supervisor launched.
func ServingProbes(procs []superviseprobe.Process) []superviseprobe.Process {
	var out []superviseprobe.Process
	for _, p := range procs {
		if superviseprobe.IsServingProbe(p, ProbeExe) {
			out = append(out, p)
		}
	}
	return out
}

// lastLine is the last non-empty line of out, which drops a kubectl warning
// printed before the command's own output.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// NewTenant bootstraps a uniquely named tenant that may create sessions with
// no environment.
func NewTenant(ctx context.Context, t testing.TB, d *sessiondriver.Driver, base string) string {
	t.Helper()
	tenant := fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant %s: %v", tenant, err)
	}
	if err := d.AllowSessionsWithNoEnvironment(ctx, tenant); err != nil {
		t.Fatalf("allow sessions with no environment for %s: %v", tenant, err)
	}
	return tenant
}

// Command sends one probe command on a session and returns the reply text.
func Command(ctx context.Context, t testing.TB, d *sessiondriver.Driver, tenant, session, cmd string) string {
	t.Helper()
	resp, err := d.SendMessage(ctx, tenant, session, cmd)
	if err != nil {
		t.Fatalf("send %q to session %s: %v", cmd, session, err)
	}
	text, err := OutputText(resp.Output)
	if err != nil {
		t.Fatalf("session %s reply to %q: %v", session, cmd, err)
	}
	return text
}

// OutputText is the text of a message response's first text part.
func OutputText(output json.RawMessage) (string, error) {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(output, &parts); err != nil {
		return "", fmt.Errorf("decode output %s: %w", output, err)
	}
	for _, p := range parts {
		if p.Type == "text" {
			return p.Text, nil
		}
	}
	return "", fmt.Errorf("output %s has no text part", output)
}

// Report asks a session's probe for its Record.
func Report(ctx context.Context, t testing.TB, d *sessiondriver.Driver, tenant, session string) superviseprobe.Record {
	t.Helper()
	var r superviseprobe.Record
	Decode(t, Command(ctx, t, d, tenant, session, superviseprobe.CmdReport), &r)
	return r
}

// Action sends a probe action command and decodes its ActionResult.
func Action(ctx context.Context, t testing.TB, d *sessiondriver.Driver, tenant, session, cmd string) superviseprobe.ActionResult {
	t.Helper()
	var r superviseprobe.ActionResult
	Decode(t, Command(ctx, t, d, tenant, session, cmd), &r)
	return r
}

// Residue asks a session's probe which objects of res still exist.
func Residue(ctx context.Context, t testing.TB, d *sessiondriver.Driver, tenant, session string, res superviseprobe.Residue) superviseprobe.ResidueResult {
	t.Helper()
	arg, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encode residue: %v", err)
	}
	var r superviseprobe.ResidueResult
	Decode(t, Command(ctx, t, d, tenant, session, superviseprobe.CmdResidue+" "+string(arg)), &r)
	return r
}

// Decode unmarshals a probe reply into v, failing the test on a reply that
// is not the expected JSON.
func Decode(t testing.TB, reply string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(reply), v); err != nil {
		t.Fatalf("decode probe reply %q: %v", reply, err)
	}
}

// EnvValue returns the value of name in a NAME=VALUE list.
func EnvValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// WaitClaimPhase polls the binding state on pod's SandboxClaim until it is
// want, failing the test at timeout. The reserved state lasts only the
// §4.6.1 hold, so the poll is short.
func WaitClaimPhase(t testing.TB, c *kind.Cluster, pod, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := c.KubectlOut(t, "-n", Namespace, "get", "sandboxclaim", "claim-"+pod, "-o", "jsonpath={.status.phase}")
		if err != nil {
			t.Fatalf("read claim of %s: %v\n%s", pod, err, out)
		}
		if last = strings.TrimSpace(out); last == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("claim of %s did not reach %q within %s (last %q)", pod, want, timeout, last)
}

// ReservedKeepPod runs one session for a fresh tenant on the keep pool,
// terminates it, and waits for the pod to be reserved for the tenant, so the
// tenant's next start is placed on that pod. It returns the tenant and the
// pod. spec: §5.2 (Recycle lifecycle).
func ReservedKeepPod(ctx context.Context, t testing.TB, d *sessiondriver.Driver, c *kind.Cluster) (tenant, pod string) {
	t.Helper()
	tenant = NewTenant(ctx, t, d, "supervisor-reserved")
	s, err := d.CreateAndStart(ctx, tenant, KeepRuntime)
	if err != nil {
		t.Fatalf("start a session on %s: %v", KeepRuntime, err)
	}
	if err := d.Terminate(ctx, tenant, s.ID); err != nil {
		t.Fatalf("terminate session %s: %v", s.ID, err)
	}
	WaitClaimPhase(t, c, s.PodAssignment, "reserved", 2*time.Minute)
	return tenant, s.PodAssignment
}

// ClaimExists reports whether pod has a SandboxClaim. A pod with none is
// idle once it is Ready.
func ClaimExists(t testing.TB, c *kind.Cluster, pod string) bool {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "get", "sandboxclaim", "claim-"+pod, "--ignore-not-found", "-o", "name")
	if err != nil {
		t.Fatalf("read claim of %s: %v\n%s", pod, err, out)
	}
	return strings.TrimSpace(out) != ""
}

// requirePinnedIdleBound skips the test, blocked, when pool keeps no pinned
// idle pod. A used session-mode pod returns to idle pinned to its tenant,
// and the WarmPoolController keeps at most maxWarm minus the warm target
// (minWarm capped at maxWarm) pinned idle pods and drains the rest, so on a
// pool whose maxWarm equals its minWarm the used pod drains as soon as its
// reserved hold ends and no start can reach it in idle. The gateway pool
// store maps a pool's warmCount to both minWarm and maxWarm
// (poolscaling.PoolStoreSource), so a pool the Kind install seeds through
// the admin API has that bound at zero. spec: §5.2 (Pinned idle inventory).
func requirePinnedIdleBound(t testing.TB, c *kind.Cluster, pool string) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "get", "sandboxwarmpool", pool, "-o",
		"jsonpath={.spec.minWarm} {.spec.maxWarm}")
	if err != nil {
		t.Fatalf("read minWarm and maxWarm of %s: %v\n%s", pool, err, out)
	}
	var minWarm, maxWarm int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d", &minWarm, &maxWarm); err != nil {
		t.Fatalf("parse minWarm and maxWarm of %s from %q: %v", pool, out, err)
	}
	if pinnedIdleBound(minWarm, maxWarm) < 1 {
		t.Skipf("blocked: %s has minWarm %d and maxWarm %d, so it keeps no pinned idle pod and a used keep "+
			"pod drains when its reserved hold ends; the gateway pool store sets both from warmCount, so the "+
			"idle phase of a used keep pod is unreachable until a pool can set maxWarm above minWarm",
			pool, minWarm, maxWarm)
	}
}

// pinnedIdleBound returns how many pinned idle pods a pool with minWarm and
// maxWarm keeps: maxWarm minus the warm target, which is minWarm capped at
// maxWarm, and never below zero. spec: §5.2 (Pinned idle inventory).
func pinnedIdleBound(minWarm, maxWarm int) int {
	target := min(minWarm, maxWarm)
	return max(maxWarm-target, 0)
}

// FirstIdleInClaimOrder reports whether pod is idle (Ready, not being
// deleted, and holding no SandboxClaim) and no other pod of pool that is not
// being deleted and holds no claim sorts before it by name. The gateway's
// idle scan lists the pool's Sandboxes from the API server, which returns
// them in name order, and claims the first one the tenant pin admits, so the
// next start by pod's pinned tenant is placed on it. A warming pod that
// sorts first counts against pod, because it can turn idle before the start.
// spec: §4.6.1 (pod claim mechanism), §5.2 (Tenant pinning).
func FirstIdleInClaimOrder(t testing.TB, c *kind.Cluster, pool, pod string) bool {
	t.Helper()
	found := false
	for _, p := range PoolPods(t, c, pool) {
		if p.Deleting || ClaimExists(t, c, p.Name) {
			continue
		}
		if p.Name == pod {
			found = p.Ready
			continue
		}
		if p.Name < pod {
			return false
		}
	}
	return found
}

// IdleUsedPod returns a pod of IdlePool that served a session, returned to
// idle when its reserved hold ended, and is the first idle pod in the
// gateway's claim order, together with the tenant it is pinned to, so the
// next session start by that tenant is placed on it. A pinned pod is
// claimable only by its pinned tenant, so the caller starts that session as
// the returned tenant. The case is skipped, blocked, on a pool that keeps no
// pinned idle pod (requirePinnedIdleBound). Each attempt runs one session
// for a fresh tenant and waits for its pod to settle; an attempt whose used
// pod drained or sorts after another idle pod is followed by another.
// spec: §5.2 (Recycle lifecycle; Pinned idle inventory); §4.6.1.
func IdleUsedPod(ctx context.Context, t testing.TB, d *sessiondriver.Driver, c *kind.Cluster) (tenant, pod string) {
	t.Helper()
	requirePinnedIdleBound(t, c, IdlePool)
	const attempts = 8
	for i := range attempts {
		WaitPoolAvailable(t, c, IdlePool, 2*time.Minute)
		tenant := NewTenant(ctx, t, d, "supervisor-idle")
		s, err := d.CreateAndStart(ctx, tenant, IdleRuntime)
		if err != nil {
			t.Fatalf("start a session on %s: %v", IdleRuntime, err)
		}
		if err := d.Terminate(ctx, tenant, s.ID); err != nil {
			t.Fatalf("terminate session %s: %v", s.ID, err)
		}
		if settleFirstIdleAfterHold(t, c, s.PodAssignment, 2*time.Minute) {
			return tenant, s.PodAssignment
		}
		t.Logf("attempt %d: used pod %s did not settle as the first idle pod of %s", i+1, s.PodAssignment, IdlePool)
	}
	t.Skipf("not-yet-applicable: no used pod of %s settled as the first idle pod in claim order in %d attempts",
		IdlePool, attempts)
	return "", ""
}

// settleFirstIdleAfterHold waits up to timeout for used's reserved hold to
// end and then for used to be the first idle pod of IdlePool in claim order.
// It returns false once used is being deleted or gone, or at timeout.
func settleFirstIdleAfterHold(t testing.TB, c *kind.Cluster, used string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && ClaimExists(t, c, used) {
		time.Sleep(time.Second)
	}
	for time.Now().Before(deadline) {
		if st, found := GetPod(t, c, used); !found || st.Deleting {
			return false
		}
		if FirstIdleInClaimOrder(t, c, IdlePool, used) {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}
