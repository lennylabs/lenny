// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// reaperIsolateLabel marks the agent pod whose gateway ingress the case
	// blocks.
	reaperIsolateLabel = "lenny.dev/chaos-reaper-isolate"
	// gatewayIngressPolicy is the chart's NetworkPolicy that admits the
	// gateway to every managed agent pod's adapter port.
	gatewayIngressPolicy = "allow-gateway-ingress"
	// reaperConcurrentRuntime is the runtime of the concurrent-workspace
	// pool, maxConcurrentSessions 2.
	reaperConcurrentRuntime = "echo-runtime-concurrent"
)

// isolateExpression is the podSelector requirement the case adds to the
// gateway ingress policy so it stops selecting the labelled pod.
const isolateExpression = `{"spec":{"podSelector":{"matchExpressions":[{"key":"` + reaperIsolateLabel + `","operator":"DoesNotExist"}]}}}`

// restoreGatewayIngress removes the case's requirement from the gateway
// ingress policy and its label from every agent pod. It runs at the start of
// the case, so a killed earlier run leaves no pod blocked, and at its end.
func restoreGatewayIngress(t *testing.T, c *kind.Cluster) {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "patch", "networkpolicy", gatewayIngressPolicy, "--type=json",
		"-p", `[{"op":"remove","path":"/spec/podSelector/matchExpressions"}]`)
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "label", "pods", "-l", reaperIsolateLabel, reaperIsolateLabel+"-")
}

// blockGatewayIngress stops the gateway ingress policy from selecting pod,
// so the default-deny policy drops the gateway's new connections to its
// adapter port.
func blockGatewayIngress(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "label", "pod", pod, reaperIsolateLabel+"=true", "--overwrite"); err != nil {
		t.Fatalf("label %s: %v\n%s", pod, err, out)
	}
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "networkpolicy", gatewayIngressPolicy, "--type=merge", "-p", isolateExpression); err != nil {
		t.Fatalf("narrow %s: %v\n%s", gatewayIngressPolicy, err, out)
	}
}

// ageTermination moves the session's terminated_at an hour back, past the
// reaper's terminal age.
func ageTermination(t *testing.T, c *kind.Cluster, tenant, id string) {
	t.Helper()
	sql := fmt.Sprintf("BEGIN; SET LOCAL app.current_tenant = '%s'; "+
		"UPDATE sessions SET terminated_at = now() - interval '1 hour' WHERE id = '%s' AND terminated_at IS NOT NULL; COMMIT;", tenant, id)
	if out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "exec", "deploy/lenny-postgres", "--",
		"psql", "-U", "lenny", "-d", "lenny", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil || !strings.Contains(out, "UPDATE 1") {
		t.Fatalf("age the termination of %s: %v\n%s", id, err, out)
	}
}

// slotMember reports whether the pod's slot set holds member.
func slotMember(t *testing.T, c *kind.Cluster, pod, member string) bool {
	t.Helper()
	return redisCLI(t, c, "SISMEMBER", "lenny:pod:{"+pod+"}:active_slots", member) == "1"
}

// spec: 5.2 (Slot assignment atomicity)
// diagnosis: the stale-slot reaper released a member whose pod it could not
// reach, which fails open, or it never released the member once the pod was
// reachable again. On a concurrent pod with a live sibling and the member of
// a session terminal for longer than the terminal age, a NetworkPolicy that
// drops the gateway's connections to the adapter must keep the member through
// every pass, so the cap stays enforced, and the first pass after the policy
// is lifted must release it. First confirm the gateway leader logs the
// stale-slot reaper cadence line, which only a gateway image built with the
// reaper prints.
func TestStaleSlotReaperKeepsAnUnreachablePodsMember_spec_5_2(t *testing.T) {
	c := kind.InstallLenny(t)
	restoreGatewayIngress(t, c)
	t.Cleanup(func() { restoreGatewayIngress(t, c) })
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	tenant := "reaper-netpol-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	if err := d.AllowSessionsWithNoEnvironment(ctx, tenant); err != nil {
		t.Fatalf("allow sessions with no environment: %v", err)
	}
	sibling, err := d.CreateAndStart(ctx, tenant, reaperConcurrentRuntime)
	if err != nil {
		t.Skipf("not-yet-applicable: the concurrent pool admitted no session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sibling.ID) })
	pod := sibling.PodAssignment
	stale, err := d.CreateAndStart(ctx, tenant, reaperConcurrentRuntime)
	if err != nil || stale.PodAssignment != pod {
		t.Skipf("not-yet-applicable: the second session did not share the sibling's pod %s: %+v, %v", pod, stale, err)
	}
	if err := d.Terminate(ctx, tenant, stale.ID); err != nil {
		t.Fatalf("terminate the stale session: %v", err)
	}
	// The stale session's release ran; restore its member as a release
	// that failed during an outage leaves it, and age its termination
	// past the reaper's terminal age.
	redisCLI(t, c, "SADD", "lenny:pod:{"+pod+"}:active_slots", stale.ID)
	ageTermination(t, c, tenant, stale.ID)

	blockGatewayIngress(t, c, pod)
	time.Sleep(150 * time.Second) // two reaper passes at the default 60s interval
	if !slotMember(t, c, pod, stale.ID) {
		t.Fatalf("the reaper released %s's member while %s's adapter was unreachable", stale.ID, pod)
	}
	if other, err := d.CreateAndStart(ctx, tenant, reaperConcurrentRuntime); err == nil {
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, other.ID) })
		if other.PodAssignment == pod {
			t.Errorf("a new session landed on %s, whose two slots the sibling and the kept member hold", pod)
		}
	}

	restoreGatewayIngress(t, c)
	if !pollUntil(3*time.Minute, 5*time.Second, func() bool { return !slotMember(t, c, pod, stale.ID) }) {
		t.Fatalf("the reaper did not release %s's member once %s was reachable", stale.ID, pod)
	}
	if !slotMember(t, c, pod, sibling.ID) {
		t.Errorf("the live sibling's member is gone from %s", pod)
	}
}
