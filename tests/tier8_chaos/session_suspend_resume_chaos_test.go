// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos scenarios for the resume driver, idle suspension, and held
// message delivery on the lifecycle harness over the compose Postgres
// session store: coordinator kills, a crash between two effects of a
// release, a held-message read outage, and a Postgres primary partition.
// Each scenario asserts its own outcome and runs no invariant checker.
//
// spec: §10.1.2 (Coordinator Handoff Protocol), §7.3 (Resume driver), §7.2
// (Message delivery routing — seven paths, path 6), §6.2 (Pod State
// Machine), §12.3 (Postgres HA Requirements)
package tier8_chaos_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	sandboxcond "github.com/lennylabs/lenny/pkg/sandbox/condition"
	sandboxstate "github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// chaosRecoveryPasses bounds the checker passes a scenario waits for a
// recovery.
const chaosRecoveryPasses = 40

// newChaosHarness builds a harness over the compose Postgres session store.
func newChaosHarness(t *testing.T, opts lifecycleharness.Options) *lifecycleharness.Harness {
	t.Helper()
	opts.NewStore, _ = lifecycleharness.PostgresStore(t)
	opts.CredentialPool = true
	return lifecycleharness.New(t, opts)
}

// textCount counts the messages with text the session's runtimes received.
func textCount(h *lifecycleharness.Harness, id, text string) int {
	n := 0
	for _, got := range h.Cluster.ReceivedTexts(id) {
		if got == text {
			n++
		}
	}
	return n
}

// dlqLen reads the session's DLQ length.
func dlqLen(t *testing.T, h *lifecycleharness.Harness, id string) int {
	t.Helper()
	n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id)
	if err != nil {
		t.Fatalf("DLQ length: %v", err)
	}
	return n
}

// spec: 10.1.2 (Coordinator Handoff Protocol), 7.2 (Message delivery routing
// — seven paths, path 6)
// diagnosis: a coordinator killed while its session was resume_pending or
// suspended on a held pod, with a message held in the DLQ, left the message
// undelivered or in the DLQ after the peer adopted and recovered the
// session, so a held message does not survive a coordinator loss.
func TestKilledCoordinatorHeldMessageReachesThePeer_spec_10_1_2(t *testing.T) {
	t.Run("resume_pending", func(t *testing.T) {
		h := newChaosHarness(t, lifecycleharness.Options{Replicas: 2, Pods: 0})
		r1, r2 := h.Replicas[0], h.Replicas[1]
		h.Cluster.AddIdlePod(t)
		id := h.StartSession(r1)
		lifecycleharness.Report(t, r1, id, string(session.FailureRuntimeCrash))
		if st := h.Row(id).State; st != session.StateResumePending {
			t.Fatalf("after the crash with no spare pod the session is %q, want resume_pending", st)
		}
		if code, body := h.Send(r2, id, "held"); code != http.StatusOK {
			t.Fatalf("message at resume_pending = %d %v", code, body)
		}
		if n := dlqLen(t, h, id); n != 1 {
			t.Fatalf("DLQ holds %d entries, want the message", n)
		}
		h.Crash(r1)
		h.Cluster.AddIdlePod(t)
		if row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the peer left the session %q, want running", row.State)
		}
		h.Settle()
		if n := textCount(h, id, "held"); n < 1 {
			t.Errorf("the held message reached the runtime %d times, want at least once", n)
		}
		if n := dlqLen(t, h, id); n != 0 {
			t.Errorf("DLQ holds %d entries after the recovery, want none", n)
		}
	})

	t.Run("held-pod suspended", func(t *testing.T) {
		h := newChaosHarness(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r1, r2 := h.Replicas[0], h.Replicas[1]
		id := h.StartSession(r1)
		if code, body := h.Do(r1, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
			t.Fatalf("interrupt = %d %v", code, body)
		}
		h.Clock.Advance(time.Second)
		if code, body := h.Send(r2, id, "held"); code != http.StatusOK {
			t.Fatalf("message served by the non-holder = %d %v", code, body)
		}
		if n := dlqLen(t, h, id); n != 1 {
			t.Fatalf("DLQ holds %d entries, want the message", n)
		}
		h.Crash(r1)
		if row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the peer left the session %q, want running", row.State)
		}
		h.Settle()
		if n := textCount(h, id, "held"); n < 1 {
			t.Errorf("the held message reached the runtime %d times, want at least once", n)
		}
		if n := dlqLen(t, h, id); n != 0 {
			t.Errorf("DLQ holds %d entries after the recovery, want none", n)
		}
	})
}

// orphanLookup is the claim-orphan GC's session oracle over the harness
// session store.
type orphanLookup struct{ store sessionstore.Store }

func (l orphanLookup) PodHasActiveSession(ctx context.Context, pod string) (bool, error) {
	rows, err := l.store.List(ctx, lifecycleharness.Tenant, sessionstore.ListFilter{})
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.PodAssignment == pod && !session.IsTerminal(r.State) {
			return true, nil
		}
	}
	return false, nil
}

// orphanClaim returns the pod of a bound claim that no non-terminal session
// row names, or "".
func orphanClaim(t *testing.T, h *lifecycleharness.Harness) string {
	t.Helper()
	claims, err := h.Cluster.BoundClaims(context.Background())
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}
	named := map[string]bool{}
	rows, err := h.Inner.List(context.Background(), lifecycleharness.Tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, r := range rows {
		if !session.IsTerminal(r.State) && r.PodAssignment != "" {
			named[r.PodAssignment] = true
		}
	}
	for _, c := range claims {
		if !named[c] {
			return c
		}
	}
	return ""
}

// orphanedClaimsReclaimed reads the controller's lenny_orphaned_claims_total
// series for the harness pool.
func orphanedClaimsReclaimed(t *testing.T) float64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather controller metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "lenny_orphaned_claims_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "pool" && l.GetValue() == lifecycleharness.Pool {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// runClaimGC runs the controller's claim-orphan GC until the pod's claim is
// gone, aging every claim past its orphan timeout, and asserts the GC
// recorded the reclaim: the pod's Sandbox is draining and carries the
// OrphanClaimReclaimed condition, and lenny_orphaned_claims_total for the
// pool rose by one (§4.6.1 Orphaned SandboxClaim detection).
func runClaimGC(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := orphanedClaimsReclaimed(t)
	gc := &warmpool.ClaimGarbageCollector{
		Client: h.Cluster.Client, Sessions: orphanLookup{h.Inner},
		Namespaces: []string{lifecycleharness.Namespace}, OrphanTimeout: time.Millisecond,
		Interval: 50 * time.Millisecond, Now: func() time.Time { return time.Now().Add(time.Hour) },
	}
	done := make(chan struct{})
	go func() { _ = gc.Start(ctx); close(done) }()
	awaitClaimGone(t, h, pod)
	cancel()
	<-done
	assertSandboxReclaimed(t, h, pod)
	if got := orphanedClaimsReclaimed(t) - before; got != 1 {
		t.Errorf("lenny_orphaned_claims_total{pool=%q} rose by %v, want 1", lifecycleharness.Pool, got)
	}
}

// awaitClaimGone waits until the pod has no claim.
func awaitClaimGone(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
		if err != nil {
			t.Fatalf("read the claim of %s: %v", pod, err)
		}
		if !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the claim-orphan GC left the claim of %s", pod)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertSandboxReclaimed asserts the claim-orphan GC drained the pod's
// Sandbox and recorded the OrphanClaimReclaimed condition on it.
func assertSandboxReclaimed(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := h.Cluster.Client.Get(context.Background(), client.ObjectKey{Namespace: lifecycleharness.Namespace, Name: pod}, &sb); err != nil {
		t.Fatalf("read the Sandbox %s: %v", pod, err)
	}
	if sb.Status.Phase != sandboxstate.CoarseDraining {
		t.Errorf("the reclaimed Sandbox %s is %q, want %q", pod, sb.Status.Phase, sandboxstate.CoarseDraining)
	}
	if c := meta.FindStatusCondition(sb.Status.Conditions, sandboxcond.OrphanClaimReclaimed); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("the reclaimed Sandbox %s carries %s = %v, want True", pod, sandboxcond.OrphanClaimReclaimed, c)
	}
}

// spec: 7.3 (Resume driver), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: a coordinator killed right after the resume attempt's entry
// write left no orphaned claim, or a claim the controller's claim-orphan GC
// could not reclaim, or the GC reclaimed it without draining and marking
// the Sandbox or without counting the orphan; or the session did not reach running on another pod through the watchdog and
// the peer's driver; or a crash between an interrupt suspension's binding
// clear and its release left a session that could not resume.
func TestCrashBetweenEffectsLeavesAReclaimableClaim_spec_7_3(t *testing.T) {
	t.Run("after the entry write", func(t *testing.T) {
		h := newChaosHarness(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r1 := h.Replicas[0]
		id := h.StartSession(r1)
		var once sync.Once
		h.Trace.ArmMatching("Update", "entryHook", lifecycleharness.FaultCrash,
			func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) { once.Do(func() { h.Crash(r1) }) })
		crashed := h.Row(id).PodAssignment
		lifecycleharness.Report(t, r1, id, string(session.FailureRuntimeCrash))
		// The pod whose runtime crashed is retired, so the restore claims a
		// pod the session row does not name, and that claim is an orphan
		// once its coordinator dies.
		if err := h.Cluster.Retire(context.Background(), h.Cluster.PodOf(crashed)); err != nil {
			t.Fatalf("retire the crashed pod: %v", err)
		}
		// The coordinator's watchdog drives the restore; its entry write is
		// the crash point.
		for i := 0; i < 4 && !r1.Crashed(); i++ {
			h.Pass()
		}
		h.Trace.Start()
		if !r1.Crashed() {
			t.Fatalf("the coordinator's resume driver did not reach the resume attempt's entry write within 4 passes after a runtime-crash report; the session is %q", h.Row(id).State)
		}
		if row := h.Row(id); row.State != session.StateResuming {
			t.Fatalf("after the crash the session is %q, want resuming", row.State)
		}
		// spec: §7.3, §4.6.1 — the crashed coordinator issues no further
		// Kubernetes write, so the claim the attempt made outlives it with
		// no session row naming its pod, and only the claim-orphan GC
		// reclaims it.
		claimed := orphanClaim(t, h)
		if claimed == "" {
			t.Fatal("the crash after the entry write left no bound claim that no session row names; the attempt's claim was released by a replica that no longer runs")
		}
		runClaimGC(t, h, claimed)
		row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses)
		if row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
		if row.PodAssignment == claimed {
			t.Errorf("the session runs on %s, the pod whose claim the GC reclaimed", claimed)
		}
	})

	t.Run("between the suspension's clear and its release", func(t *testing.T) {
		h := newChaosHarness(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r1 := h.Replicas[0]
		id := h.StartSession(r1)
		if code, body := h.Do(r1, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
			t.Fatalf("interrupt = %d %v", code, body)
		}
		pod := h.Row(id).PodAssignment
		taken := h.Checkpoints.Taken()
		h.Trace.ArmMatching("Update", "clearSuspendedPod", lifecycleharness.FaultCrash,
			func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) { h.Crash(r1) })
		r1.Server.ReleaseSuspendedPod(context.Background(), h.Row(id))
		h.Settle()
		h.Trace.Start()
		if !r1.Crashed() {
			t.Fatal("the suspension release never cleared the pod")
		}
		if h.Checkpoints.Taken() <= taken {
			t.Error("the release cleared the pod without a checkpoint")
		}
		if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment != "" {
			t.Fatalf("after the crash the row is %q on %q, want podless suspended", row.State, row.PodAssignment)
		}
		if _, exists, err := h.Cluster.ClaimPhase(context.Background(), pod); err != nil || !exists {
			t.Fatalf("the crash left no orphan claim on %s (err %v)", pod, err)
		}
		runClaimGC(t, h, pod)
		if code, body := h.Send(h.Replicas[1], id, "after-crash"); code != http.StatusOK {
			t.Fatalf("message to the podless session = %d %v", code, body)
		}
		if row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
		h.Settle()
		if n := textCount(h, id, "after-crash"); n != 1 {
			t.Errorf("the message reached the runtime %d times, want once", n)
		}
	})
}

// spec: 6.2 (Pod State Machine), 10.1.2 (Coordinator Handoff Protocol)
// diagnosis: an adopter idle-suspended a session whose runtime was still
// handling a message, ending the turn, or it did not suspend the session
// within one idle bound, one heartbeat interval, and one watchdog tick of
// the handler returning, or it opened an Attach stream to decide.
func TestAdopterDoesNotSuspendABusyRuntime_spec_6_2(t *testing.T) {
	for _, busy := range []bool{true, false} {
		name := "no handler running"
		if busy {
			name = "handler running"
		}
		t.Run(name, func(t *testing.T) { adopterIdleSuspension(t, busy) })
	}
}

// adopterIdleSuspension kills the coordinator of a running session, with a
// message in hand when busy, and checks the adopter's idle suspension.
func adopterIdleSuspension(t *testing.T, busy bool) {
	const heartbeat = 200 * time.Millisecond
	h := newChaosHarness(t, lifecycleharness.Options{
		Replicas: 2, Pods: 8, MaxIdleSeconds: lifecycleharness.IdleWindow, HeartbeatInterval: heartbeat,
	})
	r1, r2 := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(r1)
	pod := h.Cluster.PodOf(h.Row(id).PodAssignment)
	if busy {
		pod.Runtime.Hold()
		go h.Send(r1, id, "long-turn")
		deadline := time.Now().Add(5 * time.Second)
		for pod.Runtime.Handling(id) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the runtime never received the turn")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	h.Crash(r1)
	attaches := pod.Attaches()
	for i := 0; i < 8 && h.Bound(id) != r2; i++ {
		h.Pass()
	}
	if h.Bound(id) != r2 {
		row := h.Row(id)
		lease, err := h.Leases.Get(context.Background(), lifecycleharness.Tenant, id)
		t.Fatalf("the peer did not adopt the running session: row %q on %q at generation %d, lease %+v (%v)",
			row.State, row.PodAssignment, row.CoordinationGeneration, lease, err)
	}
	if busy {
		time.Sleep(lifecycleharness.IdleWindow*time.Second + 2*heartbeat)
		for i := 0; i < 3; i++ {
			h.Pass()
		}
		if st := h.Row(id).State; st != session.StateRunning {
			t.Fatalf("the adopter moved a session whose runtime is handling a message to %q, want running", st)
		}
		pod.Runtime.Release()
	}
	time.Sleep(lifecycleharness.IdleWindow*time.Second + 2*heartbeat)
	h.Pass()
	if row := h.Row(id); row.State != session.StateSuspended {
		t.Fatalf("the session is %q, want idle-suspended within one idle bound, heartbeat, and tick of going idle", row.State)
	}
	if pod.Attaches() != attaches {
		t.Errorf("the adopter opened %d Attach streams to decide the suspension", pod.Attaches()-attaches)
	}
}

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: an outage of the held-message store (a partition that hangs
// reads) stalled or refused a message to a running session, or after the
// heal the held entry did not precede the next message or stayed in the
// DLQ. A read that hangs rather than fails holds the delivery for its whole
// timeout.
func TestHeldMessageReadOutageDoesNotStallDelivery_spec_7_2(t *testing.T) {
	var proxy *lifecycleharness.Proxy
	h := newChaosHarness(t, lifecycleharness.Options{
		Replicas: 1, Pods: 8,
		QueueAddr: func(addr string) string {
			proxy = lifecycleharness.NewProxy(t, addr)
			return proxy.Addr()
		},
	})
	r := h.Replicas[0]
	id := h.StartSession(r)
	if _, err := h.Messaging.EnqueueDLQ(context.Background(), lifecycleharness.Tenant, id, sessioninbox.Message{
		MessageID: "msg_held", Payload: []byte(`{"role":"user","content":"held-before"}`), EnqueuedAt: h.Clock.Now(),
	}, 0); err != nil {
		t.Fatalf("hold a message: %v", err)
	}
	proxy.Partition()
	start := time.Now()
	code, body := h.Send(r, id, "during-outage")
	elapsed := time.Since(start)
	if code != http.StatusOK {
		t.Errorf("message to the running session during the outage = %d %v, want 200", code, body)
	}
	if elapsed > 10*time.Second {
		t.Errorf("the message took %s during the outage, want it not to wait out the held-message read", elapsed)
	}
	proxy.Heal()
	if code, body := h.Send(r, id, "after-heal"); code != http.StatusOK {
		t.Fatalf("message after the heal = %d %v", code, body)
	}
	h.Settle()
	var order []string
	for _, text := range h.Cluster.ReceivedTexts(id) {
		if text == "held-before" || text == "after-heal" {
			order = append(order, text)
		}
	}
	if len(order) != 2 || order[0] != "held-before" {
		t.Errorf("after the heal the runtime received %v, want the held entry once before the next message", order)
	}
	if n := textCount(h, id, "during-outage"); n != 1 {
		t.Errorf("the message sent during the outage reached the runtime %d times, want once", n)
	}
	if n := dlqLen(t, h, id); n != 0 {
		t.Errorf("DLQ holds %d entries after the heal, want none", n)
	}
}

// partitionedPrimary builds a harness whose session store writes through a
// partitionable proxy to the compose Postgres primary and reads the read
// pool directly.
func partitionedPrimary(t *testing.T, opts lifecycleharness.Options) (*lifecycleharness.Harness, *lifecycleharness.Proxy) {
	t.Helper()
	_, pg := lifecycleharness.PostgresStore(t)
	cfg := pg.Pool.Config()
	proxy := lifecycleharness.NewProxy(t, net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))))
	primaryCfg := cfg.Copy()
	host, port, err := net.SplitHostPort(proxy.Addr())
	if err != nil {
		t.Fatalf("proxy address: %v", err)
	}
	portN, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("proxy port: %v", err)
	}
	primaryCfg.ConnConfig.Host = host
	primaryCfg.ConnConfig.Port = uint16(portN)
	primaryCfg.ConnConfig.ConnectTimeout = 2 * time.Second
	primary, err := pgxpool.NewWithConfig(context.Background(), primaryCfg)
	if err != nil {
		t.Fatalf("primary pool through the proxy: %v", err)
	}
	t.Cleanup(primary.Close)
	opts.CredentialPool = true
	opts.NewStore = lifecycleharness.PostgresStoreOn(primary, sessionpg.WithReadPool(pg.Pool))
	return lifecycleharness.New(t, opts), proxy
}

// spec: 7.2 (Message delivery routing — seven paths, path 6), 12.3
// (Postgres HA Requirements)
// diagnosis: a partition of the Postgres primary refused or stalled a REST
// or MCP message to a running session beyond the primary-read deadline, or
// delivery did not continue after the primary returned. A primary read
// without a deadline hangs for the partition's whole length.
func TestPrimaryPartitionDoesNotStallMessages_spec_12_3(t *testing.T) {
	h, proxy := partitionedPrimary(t, lifecycleharness.Options{Replicas: 1, Pods: 8})
	r := h.Replicas[0]
	id := h.StartSession(r)
	proxy.Partition()
	start := time.Now()
	code, body := h.Send(r, id, "rest-during")
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("REST message took %s during the partition, want it bounded by the primary-read deadline", elapsed)
	}
	if code != http.StatusOK {
		t.Errorf("REST message during the partition = %d %v, want 200: the entry row reads running", code, body)
	}
	start = time.Now()
	res := h.MCPCall(r, "lenny/send_message", `{"to":"`+id+`","message":"mcp-during"}`)
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("MCP message took %s during the partition, want it bounded by the primary-read deadline (result %v)", elapsed, res)
	}
	if res["isError"] == true || res["_error"] != nil {
		t.Errorf("MCP message during the partition = %v, want it delivered", res)
	}
	t.Logf("REST %d, MCP %v", code, res)
	proxy.Heal()
	time.Sleep(500 * time.Millisecond)
	if code, body := h.Send(r, id, "rest-after"); code != http.StatusOK {
		t.Fatalf("REST message after the heal = %d %v", code, body)
	}
	h.Settle()
	if n := textCount(h, id, "rest-after"); n != 1 {
		t.Errorf("the message after the heal reached the runtime %d times, want once", n)
	}
}

// spec: 6.2 (Pod State Machine), 7.2 (Message delivery routing — seven
// paths, path 6)
// diagnosis: during a primary partition, a message classified on a stale
// running row of a session an idle suspension had sealed reached the
// runtime or was answered something other than an error receipt with
// inbox_unavailable; or after the heal the suspension did not complete or a
// later message did not resume the session.
func TestSealedSessionMessageDuringPrimaryPartition_spec_6_2(t *testing.T) {
	h, proxy := partitionedPrimary(t, lifecycleharness.Options{Replicas: 1, Pods: 8})
	r := h.Replicas[0]
	id := h.StartSession(r)
	time.Sleep(lifecycleharness.IdleWindow*time.Second + 100*time.Millisecond)
	// The suspension seals the session and checkpoints, then its write to
	// the primary waits out the partition.
	proxy.Partition()
	r.Server.SuspendIdle(context.Background(), h.Row(id), lifecycleharness.IdleWindow)
	time.Sleep(300 * time.Millisecond)
	code, body := h.Send(r, id, "sealed")
	if n := textCount(h, id, "sealed"); n != 0 {
		t.Errorf("a message to the sealed session reached the runtime %d times", n)
	}
	if !strings.Contains(strings.ToLower(fmt.Sprint(body)), "inbox_unavailable") {
		t.Errorf("message to the sealed session during the partition = %d %v, want an error receipt with inbox_unavailable", code, body)
	}
	proxy.Heal()
	deadline := time.Now().Add(20 * time.Second)
	for h.Row(id).State != session.StateSuspended && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if st := h.Row(id).State; st != session.StateSuspended {
		t.Fatalf("after the heal the suspension left the session %q, want suspended", st)
	}
	if code, body := h.Send(r, id, "after-heal"); code != http.StatusOK {
		t.Fatalf("message after the heal = %d %v", code, body)
	}
	if row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses); row.State != session.StateRunning {
		t.Fatalf("the later message left the session %q, want running", row.State)
	}
}
