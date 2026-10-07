// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the gateway's handling of a CH-ATTACH stream that
// fails because the runtime stopped answering heartbeats between turns.
//
// The cases run on dedicated pools whose runtime is the test-only
// heartbeat-silence fixture (tests/testinfra/heartbeatsilence): an
// unmodified echo reference runtime behind a filter that stops forwarding
// heartbeats once a session sends the fixture's directive. The fixture also
// logs every session_end it forwards. On a real agent pod the adapter's
// heartbeat monitor then ends the session's stream with DEADLINE_EXCEEDED, and the
// coordinating gateway replica reports the end as runtime_crash. On a pool
// with maxConcurrentSessions: 1 the replica releases the pod with the failed
// disposition, so a recycling pool retires the pod rather than recycling it,
// and the adapter writes session_end to the runtime. A session the §7.3
// classifier moves to resume_pending reaches awaiting_client_action when its
// maxResumeWindowSeconds elapses. On a concurrent pool each failed slot is
// counted toward the whole-pod replacement trigger, and the pod is drained at
// ceil(maxConcurrentSessions / 2) failed slots. The drain deletes the pod, so a
// sibling slot whose own heartbeat deadline has not yet passed loses its stream
// to the pod's deletion rather than to a reported failure.
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

	"github.com/lennylabs/lenny/tests/testinfra/heartbeatsilence"
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

	// silenceRecyclePoolName and silenceEchoRuntimeRef name the recycling
	// pool with maxConcurrentSessions: 1 on the heartbeat-silence fixture
	// that tests/testinfra/kind/install.sh installs.
	silenceRecyclePoolName = "heartbeat-silence-recycle-pool"
	silenceEchoRuntimeRef  = "heartbeat-silence-echo-runtime"
	// silenceConcurrentPoolName and silenceConcurrentRuntimeRef name the
	// pool with maxConcurrentSessions: 2 on the heartbeat-silence fixture.
	silenceConcurrentPoolName   = "heartbeat-silence-concurrent-pool"
	silenceConcurrentRuntimeRef = "heartbeat-silence-concurrent-runtime"
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

// sendSilenceDirective delivers the heartbeat-silence fixture's directive
// and asserts the runtime echoed it, so the turn completes before the acks
// stop.
func sendSilenceDirective(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant, sessionID string) {
	t.Helper()
	resp, err := d.SendMessage(ctx, tenant, sessionID, heartbeatsilence.Directive)
	if err != nil {
		t.Fatalf("send the silence directive on %s: %v", sessionID, err)
	}
	if resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("silence directive on %s: receipt %q, want delivered (body %s)",
			sessionID, resp.DeliveryReceipt.Status, resp.Output)
	}
	assertOutputEchoes(t, "silence directive on "+sessionID, resp.Output, heartbeatsilence.Directive)
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

// awaitSessionEndLogged waits for the fixture's log line recording the
// session_end the adapter wrote to the runtime for sessionID.
func awaitSessionEndLogged(t *testing.T, log *syncBuffer, sessionID string, timeout time.Duration) {
	t.Helper()
	want := heartbeatsilence.SessionEndLogPrefix + sessionID
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
	requirePoolReadyPods(t, c, silenceRecyclePoolName, 1)

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
	created, err := d.CreateSessionWithOptions(ctx, tenant, silenceEchoRuntimeRef, sessiondriver.StartOptions{
		RetryPolicy: []byte(fmt.Sprintf(`{"maxResumeWindowSeconds":%d}`, streamFailureResumeWindow)),
	})
	if err != nil {
		t.Fatalf("create session on %s: %v", silenceEchoRuntimeRef, err)
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

// podDrainWatch records every state of one agent pod that a `kubectl get -w`
// watch reports, one line per event, so the drain-request stamp is observed
// even when the pod is deleted between two polls. Each line is the pod's
// lenny.dev/drain-request annotation and its deletionTimestamp, separated by
// podDrainWatchSep.
type podDrainWatch struct {
	pod string
	out *syncBuffer
}

const podDrainWatchSep = "|"

// watchPodDrainRequest starts the watch on pod. Start it before the failure
// that should trip the replacement trigger, so the event that stamps the
// annotation is delivered to the watch rather than missed.
func watchPodDrainRequest(t *testing.T, c *kind.Cluster, pod string) *podDrainWatch {
	t.Helper()
	w := &podDrainWatch{pod: pod, out: &syncBuffer{}}
	cmd := c.Kubectl("-n", executionModesNamespace, "get", "pod", pod, "--watch",
		"-o", `jsonpath={.metadata.annotations.lenny\.dev/drain-request}`+podDrainWatchSep+
			`{.metadata.deletionTimestamp}{"\n"}`)
	cmd.Stdout = w.out
	cmd.Stderr = &syncBuffer{}
	if err := cmd.Start(); err != nil {
		t.Fatalf("watch pod %s: %v", pod, err)
	}
	t.Cleanup(func() { stopFollow(cmd) })
	return w
}

// drainEvidence classifies the events seen so far. stamped is true once an
// event carried the drain-request stamp while the pod was not yet being
// deleted, which is the §5.2 replacement trigger acting before the
// WarmPoolController deletes the pod. deletedFirst is true when an event
// shows the pod being deleted before any event carried the stamp, which is
// a release that retired the pod without the trigger.
func (w *podDrainWatch) drainEvidence() (stamped, deletedFirst bool) {
	for _, line := range strings.Split(w.out.String(), "\n") {
		stamp, deletion, ok := strings.Cut(strings.TrimSpace(line), podDrainWatchSep)
		if !ok {
			continue
		}
		if strings.TrimSpace(stamp) != "" && strings.TrimSpace(deletion) == "" {
			return true, false
		}
		if strings.TrimSpace(deletion) != "" {
			return false, true
		}
	}
	return false, false
}

// awaitPodDrainRequested waits for the watch to report the pod carrying the
// §5.2 drain-request stamp before any deletion of the pod. It fails when the
// pod is deleted, or is gone, before the stamp was seen: a pod removed without
// the stamp was retired by a per-slot release rather than drained by the
// whole-pod replacement trigger.
func awaitPodDrainRequested(t *testing.T, c *kind.Cluster, w *podDrainWatch, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		stamped, deletedFirst := w.drainEvidence()
		if stamped {
			return
		}
		if deletedFirst || podGone(t, c, w.pod) {
			// A gone pod's final events may still be in flight to the
			// watch, so read it once more before failing.
			time.Sleep(2 * time.Second)
			if stamped, _ = w.drainEvidence(); stamped {
				return
			}
			t.Fatalf("pod %s was deleted without the lenny.dev/drain-request stamp: the failed slot retired the "+
				"concurrent pod instead of counting toward the whole-pod replacement trigger; watched states:\n%s",
				w.pod, w.out.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("pod %s carries no drain request after %s: the failed slots did not trip the whole-pod "+
				"replacement trigger; watched states:\n%s", w.pod, timeout, w.out.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// podGone reports whether the pod no longer exists.
func podGone(t *testing.T, c *kind.Cluster, pod string) bool {
	t.Helper()
	name, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "pod", pod, "--ignore-not-found", "-o", "name")
	return err == nil && strings.TrimSpace(name) == ""
}

// awaitAnySessionReported waits until at least one of the sessions has left
// running for a state the stream-failure report writes, and fails when none
// has within timeout.
func awaitAnySessionReported(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant string, ids []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for len(reportedSessions(ctx, d, tenant, ids)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no session of %v left running within %s: the slot's stream failure was not reported", ids, timeout)
		}
		time.Sleep(time.Second)
	}
}

// reportedSessions returns the sessions whose row is in a state the
// stream-failure report writes: resume_pending, or awaiting_client_action
// once the resume window has elapsed. A session the driver cannot read is
// left out.
func reportedSessions(ctx context.Context, d *sessiondriver.Driver, tenant string, ids []string) []string {
	var out []string
	for _, id := range ids {
		s, err := d.GetSession(ctx, tenant, id)
		if err != nil {
			continue
		}
		if s.State == "resume_pending" || s.State == "awaiting_client_action" {
			out = append(out, id)
		}
	}
	return out
}

// awaitPodGone waits for the pod to be deleted, which the WarmPoolController
// does once the drain request moves the pod's Sandbox to draining.
func awaitPodGone(t *testing.T, c *kind.Cluster, pod string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if podGone(t, c, pod) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pod %s still present %s after its drain request", pod, timeout)
		}
		time.Sleep(time.Second)
	}
}

// settledSlotFailures returns the reported sessions and the rise in the pod's
// lenny_slot_failure_total once two readings taken a few seconds apart agree,
// so a report still in flight when the pod went away is counted on both sides.
func settledSlotFailures(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant, pod string, ids []string, before float64) ([]string, float64) {
	t.Helper()
	read := func() ([]string, float64) {
		return reportedSessions(ctx, d, tenant, ids), slotFailureCount(t, d, pod) - before
	}
	prevIDs, prevCount := read()
	deadline := time.Now().Add(time.Minute)
	for {
		time.Sleep(3 * time.Second)
		gotIDs, gotCount := read()
		if len(gotIDs) == len(prevIDs) && gotCount == prevCount {
			return gotIDs, gotCount
		}
		if time.Now().After(deadline) {
			return gotIDs, gotCount
		}
		prevIDs, prevCount = gotIDs, gotCount
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes), 28.5.1
// (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State Machine)
// diagnosis: on a concurrent pool whose one runtime stopped answering
// heartbeats, a slot's reported stream failure was not counted toward the
// whole-pod replacement trigger, or the pod was not drained once
// ceil(maxConcurrentSessions / 2) slots had failed. Every create and delivery
// in the case goes through one gateway replica, because each replica keeps
// its own slot-health ledger; the case asserts that before it stops the acks.
// The pool sets maxConcurrentSessions: 2, so the trigger fires on the first
// failed slot and the drain deletes the pod, which can end the sibling slot's
// stream before its own heartbeat deadline; that end is a pod loss and is not
// reported. A slot-failure count that differs from the number of sessions the
// report moved out of running means a report skipped the accounting or a slot
// was counted twice; no reported session means the escalation was not
// reported; a pod deleted, or never drain-requested, without the
// lenny.dev/drain-request stamp observed first means a failed slot retired the
// pod directly or the trigger did not fire.
func TestStreamFailureConcurrentPoolDrainsAtThreshold_spec_5_2(t *testing.T) {
	c := kind.InstallLenny(t)
	requirePoolReadyPods(t, c, silenceConcurrentPoolName, 1)
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
		sess, err := d.CreateAndStart(ctx, tenant, silenceConcurrentRuntimeRef)
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("precondition not met: concurrent pool not ready: %v", err)
		}
		if err != nil {
			t.Fatalf("create session %s on %s: %v", name, silenceConcurrentRuntimeRef, err)
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
	// The watch starts before the acks stop, so the event that stamps the
	// drain request is observed even when the pod is deleted soon after.
	drain := watchPodDrainRequest(t, c, pod)

	// The silence is pod-wide, so whichever slot's heartbeat deadline passes
	// first is escalated and reported.
	sendSilenceDirective(ctx, t, d, tenant, ids[0])
	awaitAnySessionReported(ctx, t, d, tenant, ids, streamFailureEscalation)

	// maxConcurrentSessions is 2, so the trigger fires at ceil(2 / 2) = 1
	// failed slot.
	awaitPodDrainRequested(t, c, drain, 2*time.Minute)
	awaitPodGone(t, c, pod, 3*time.Minute)

	reported, rise := settledSlotFailures(ctx, t, d, tenant, pod, ids, before)
	if len(reported) == 0 {
		t.Fatalf("no session of %v is in resume_pending or awaiting_client_action after the pod drained", ids)
	}
	if rise != float64(len(reported)) {
		t.Errorf("lenny_slot_failure_total for pod %s rose by %g, want %d: each reported slot failure (%v) is counted once",
			pod, rise, len(reported), reported)
	}
}
