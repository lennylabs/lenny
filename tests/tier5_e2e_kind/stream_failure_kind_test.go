// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the gateway's handling of a CH-ATTACH stream that
// fails because the runtime stopped answering heartbeats between turns.
//
// The echo reference runtimes stop acking heartbeats after they echo the
// echocore silence directive. On a real agent pod the adapter's heartbeat
// monitor then ends the session's stream with DEADLINE_EXCEEDED, and the
// coordinating gateway replica reports the end as runtime_crash. On a pool
// with maxConcurrentSessions: 1 the replica releases the pod with the failed
// disposition, so a recycling pool retires the pod rather than recycling it,
// and the adapter writes session_end to the runtime. A session the §7.3
// classifier moves to resume_pending reaches awaiting_client_action when its
// maxResumeWindowSeconds elapses. On a concurrent pool each failed slot is
// counted toward the whole-pod replacement trigger, and the pod is drained at
// ceil(maxConcurrentSessions / 2) failed slots.
//
// Each escalation takes up to the adapter's heartbeat interval plus its ack
// timeout (30 s + 10 s by default), so each case allows several minutes.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH Degradation.), §7.3 (Retry and
// Resume), §6.2 (Pod State Machine), §5.2 (Pool Configuration and Execution
// Modes).
package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// streamFailureEscalation bounds the wait for the adapter's heartbeat
	// escalation and the gateway's report: one heartbeat interval, the ack
	// timeout, and margin for the report and a slow node.
	streamFailureEscalation = 3 * time.Minute
	// streamFailureResumeWindow is the session's maxResumeWindowSeconds.
	streamFailureResumeWindow = 5
)

// syncBuffer is a bytes.Buffer safe for a writer goroutine and a reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// followRuntimeLog streams the pod's runtime container log into a buffer
// until the test ends, so lines written just before the pod is deleted are
// kept.
func followRuntimeLog(t *testing.T, c *kind.Cluster, pod string) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	cmd := c.Kubectl("-n", executionModesNamespace, "logs", "-f", pod, "-c", "runtime", "--since=1s")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("follow the runtime log of %s: %v", pod, err)
	}
	t.Cleanup(func() { stopFollow(cmd) })
	return out
}

func stopFollow(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// sendSilenceDirective delivers the echocore silence directive and asserts
// the runtime echoed it, so the turn completes before the acks stop.
func sendSilenceDirective(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant, sessionID string) {
	t.Helper()
	resp, err := d.SendMessage(ctx, tenant, sessionID, echocore.HeartbeatSilenceDirective)
	if err != nil {
		t.Fatalf("send the silence directive on %s: %v", sessionID, err)
	}
	if resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("silence directive on %s: receipt %q, want delivered (body %s)",
			sessionID, resp.DeliveryReceipt.Status, resp.Output)
	}
	assertOutputEchoes(t, "silence directive on "+sessionID, resp.Output, echocore.HeartbeatSilenceDirective)
}

// awaitPodRetired waits for the pod's claim to be deleted, which the failed
// release does on the retire path, and fails when the claim ever reaches the
// recycle path's recycling or reserved state.
func awaitPodRetired(t *testing.T, c *kind.Cluster, pod string, timeout time.Duration) {
	t.Helper()
	claim := "claim-" + pod
	deadline := time.Now().Add(timeout)
	for {
		out, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "sandboxclaim", claim,
			"-o", "jsonpath={.status.phase}", "--ignore-not-found")
		phase := strings.TrimSpace(out)
		if err == nil && phase == "" {
			return
		}
		if phase == "recycling" || phase == "reserved" {
			t.Fatalf("claim %s reached %q: the pod of a session that ended in failure was recycled, "+
				"want it retired", claim, phase)
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim %s still present after %s (phase %q, err %v): the failed release did not retire the pod",
				claim, timeout, phase, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// awaitSessionEndLogged waits for the runtime's log to record the session_end
// the adapter wrote for sessionID.
func awaitSessionEndLogged(t *testing.T, log *syncBuffer, sessionID string, timeout time.Duration) {
	t.Helper()
	want := "session_end for session " + sessionID
	deadline := time.Now().Add(timeout)
	for !strings.Contains(log.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the runtime never logged %q; its log:\n%s", want, log.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State
// Machine), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: a runtime on a recycling pool with maxConcurrentSessions: 1
// stopped answering heartbeats between turns, and the gateway did not report
// it as runtime_crash, recycled the pod instead of retiring it, sent the
// runtime no session_end, or left the session in resume_pending past its
// maxResumeWindowSeconds. The session must reach resume_pending, the pod's
// claim must be deleted without passing through recycling, the runtime must
// receive session_end, and the session must reach awaiting_client_action once
// the resume window elapses with no replacement pod.
func TestStreamFailureRetiresTheExclusivePodAndWaitsOutTheResumeWindow_spec_28_5_1(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, taskModePoolName, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	tenant := uniqueName("stream-failure-exclusive")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	// The session is created, finalized, and started in three steps,
	// because POST /v1/sessions carries the retryPolicy the combined start
	// body does not.
	created, err := d.CreateSessionWithOptions(ctx, tenant, taskModeRuntimeRef, sessiondriver.StartOptions{
		RetryPolicy: []byte(fmt.Sprintf(`{"maxResumeWindowSeconds":%d}`, streamFailureResumeWindow)),
	})
	if err != nil {
		t.Fatalf("create session on %s: %v", taskModeRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, created.ID) })
	if _, err := d.Finalize(ctx, tenant, created.ID); err != nil {
		t.Fatalf("finalize session %s: %v", created.ID, err)
	}
	sess, err := d.Start(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("start session %s: %v", created.ID, err)
	}
	pod := sess.PodAssignment
	if pod == "" {
		// The pod is claimed at create, so the create response names it.
		pod = created.PodAssignment
	}
	if pod == "" {
		t.Fatal("session carries no podAssignment")
	}
	log := followRuntimeLog(t, c, pod)

	resp, err := d.SendMessage(ctx, tenant, sess.ID, "before-the-hang")
	if err != nil || resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("first message: %v %+v", err, resp)
	}
	sendSilenceDirective(ctx, t, d, tenant, sess.ID)

	got, ok := d.WaitForState(ctx, tenant, sess.ID, streamFailureEscalation, "resume_pending", "awaiting_client_action")
	if !ok {
		t.Fatalf("session %s state = %+v after %s, want resume_pending once the heartbeat escalation is reported",
			sess.ID, got, streamFailureEscalation)
	}
	awaitPodRetired(t, c, pod, 2*time.Minute)
	awaitSessionEndLogged(t, log, sess.ID, 30*time.Second)

	got, ok = d.WaitForState(ctx, tenant, sess.ID, time.Minute, "awaiting_client_action")
	if !ok {
		t.Fatalf("session %s state = %+v, want awaiting_client_action after its %ds resume window",
			sess.ID, got, streamFailureResumeWindow)
	}
}

// slotFailureCount returns the lenny_slot_failure_total samples for pod on
// the replica behind d, summed over their labels.
func slotFailureCount(t *testing.T, d *sessiondriver.Driver, pod string) float64 {
	t.Helper()
	var total float64
	for _, line := range strings.Split(t5FetchMetrics(t, d.BaseURL()), "\n") {
		if !strings.HasPrefix(line, "lenny_slot_failure_total{") || !strings.Contains(line, `"`+pod+`"`) {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(line[strings.LastIndex(line, " ")+1:], "%g", &v); err == nil {
			total += v
		}
	}
	return total
}

// requireCoordinatedBy waits for the coordination-lease mirror to name a
// replica of gatewayPod as every session's coordinator, so the slot failures
// all land in that replica's slot-health ledger.
func requireCoordinatedBy(t *testing.T, c *kind.Cluster, gatewayPod string, sessions []string) {
	t.Helper()
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("precondition not met: the e2e Postgres pod is not running")
	}
	sql := fmt.Sprintf("SELECT count(*) FROM coordination_lease WHERE session_id IN ('%s') "+
		"AND released_at IS NULL AND coordinator_replica LIKE '%s-%%';",
		strings.Join(sessions, "','"), gatewayPod)
	deadline := time.Now().Add(time.Minute)
	for {
		out := strings.TrimSpace(t5RunPsqlQuery(t, c, pgIP, "t5-stream-failure-coordinator", sql))
		if out == fmt.Sprint(len(sessions)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions %v coordinated by %s: %s of %d, want every session on the pinned replica",
				sessions, gatewayPod, out, len(sessions))
		}
		time.Sleep(2 * time.Second)
	}
}

// awaitPodDrainRequested waits for the pod to carry the §5.2 drain-request
// annotation the replacement trigger stamps, or to be gone.
func awaitPodDrainRequested(t *testing.T, c *kind.Cluster, pod string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		name, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "pod", pod, "--ignore-not-found", "-o", "name")
		if err == nil && strings.TrimSpace(name) == "" {
			return
		}
		stamp, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "pod", pod,
			"-o", `jsonpath={.metadata.annotations.lenny\.dev/drain-request}`)
		if err == nil && strings.TrimSpace(stamp) != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pod %s carries no drain request after %s: the failed slots did not trip the whole-pod "+
				"replacement trigger", pod, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes), 28.5.1
// (Gateway-to-pod), 7.3 (Retry and Resume)
// diagnosis: on a concurrent pool whose one runtime stopped answering
// heartbeats, a slot's stream failure was not counted toward the whole-pod
// replacement trigger, or the pod was not drained once
// ceil(maxConcurrentSessions / 2) slots had failed. Every create and delivery
// in the case goes through one gateway replica, because each replica keeps
// its own slot-health ledger; the case asserts that before it stops the acks.
// A slot-failure count below the number of failed slots means a slot's report
// skipped the accounting; a pod without a drain request means the trigger did
// not fire.
func TestStreamFailureConcurrentPoolDrainsAtThreshold_spec_5_2(t *testing.T) {
	c := kind.InstallLenny(t)
	requirePoolReadyPods(t, c, concurrentPoolName, 1)
	gateways := readyGatewayPods(t, c)
	if len(gateways) == 0 {
		t.Skip("precondition not met: no Ready gateway replica")
	}
	// Every request goes through one gateway pod by port-forwarding to that
	// pod rather than to the Service, so the shared deployment keeps its
	// replica count and no other test loses a replica mid-run.
	gatewayPod := gateways[0]
	d := sessiondriver.NewKeptForTarget(t, "pod/"+gatewayPod, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	tenant := uniqueName("stream-failure-concurrent")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	var ids []string
	pod := ""
	for _, name := range []string{"A", "B"} {
		sess, err := d.CreateAndStart(ctx, tenant, concurrentRuntimeRef)
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("precondition not met: concurrent pool not ready: %v", err)
		}
		if err != nil {
			t.Fatalf("create session %s on %s: %v", name, concurrentRuntimeRef, err)
		}
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
		if pod == "" {
			pod = sess.PodAssignment
		} else if sess.PodAssignment != pod {
			t.Fatalf("session %s landed on pod %q, want the first session's pod %q", name, sess.PodAssignment, pod)
		}
		ids = append(ids, sess.ID)
		resp, err := d.SendMessage(ctx, tenant, sess.ID, "open-"+name)
		if err != nil || resp.DeliveryReceipt.Status != "delivered" {
			t.Fatalf("first message on session %s: %v %+v", name, err, resp)
		}
	}
	requireCoordinatedBy(t, c, gatewayPod, ids)
	before := slotFailureCount(t, d, pod)

	sendSilenceDirective(ctx, t, d, tenant, ids[0])
	for _, id := range ids {
		if got, ok := d.WaitForState(ctx, tenant, id, streamFailureEscalation, "resume_pending", "awaiting_client_action"); !ok {
			t.Fatalf("session %s state = %+v, want resume_pending once its slot's stream failure is reported", id, got)
		}
	}
	if got := slotFailureCount(t, d, pod) - before; got != float64(len(ids)) {
		t.Errorf("lenny_slot_failure_total for pod %s rose by %g, want %d: each failed slot is counted", pod, got, len(ids))
	}
	// maxConcurrentSessions is 2, so the trigger fires at ceil(2 / 2) = 1
	// failed slot, and both slots fail together because the pod's one
	// runtime stopped acking.
	awaitPodDrainRequested(t, c, pod, 2*time.Minute)
}
