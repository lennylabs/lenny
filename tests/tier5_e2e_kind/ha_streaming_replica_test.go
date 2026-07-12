// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the HA multi-replica gateway streaming
// reconnect contract. It pins one driver per gateway replica, starts an
// SSE event stream through replica A, drives the session's terminal
// events through replica B, and reconnects the stream through replica B
// with the Last-Event-ID cursor last seen on A. It asserts the two
// replicas serve the same session and the same monotonic event backlog
// from shared externalized state, and that the reconnect on the other
// replica replays from the cursor with no duplicate and no missing
// event across the A-to-B handoff.
//
// This is the first tier-5 coverage of the cross-replica reconnect
// path. prompt_roundtrip_test.go and the other tier5 suites drive a
// single gateway replica through the load-balanced Service; the tier-4
// streaming_reconnect surface is a scaffold that defers to tier-2/3
// (tests/tier4_integration/scaffolds_test.go), and the tier-7b
// streaming-reconnect scenario is load-shaped and phase-gated. None
// asserts that a stream started on one replica reconnects and replays
// correctly when routed to another.

package tier5_e2e_kind_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// haStreamingTenant is the synthetic tenant this test bootstraps. A
// per-run suffix sidesteps a stale tenant left behind by a prior run on
// the persistent e2e cluster, matching prompt_roundtrip_test.go.
const haStreamingTenant = "ha-streaming-tenant"

// gatewayComponentSelector is the chart label the gateway Deployment
// stamps on its pods; the HA test forwards to individual pods carrying
// it to pin a driver onto one replica.
const gatewayComponentSelector = "lenny.dev/component=gateway"

// spec: §10.1 (spec/10_gateway-internals.md, Horizontal Scaling)
// "Gateway replicas are stateless proxies over externalized state" /
// "Correctness rule: Sticky routing is an optimization. A client can
// reconnect to any replica and resume." + §7.3 (spec/07_session-
// lifecycle.md, Reconnect semantics) "The gateway persists an event
// cursor per session. On reconnect, the client provides its last-seen
// cursor and the gateway replays missed events from the EventStore." +
// §7.2 (Event ordering and resume) "on the SSE transport, the
// Last-Event-ID request header serves the same purpose implicitly ...
// receive buffered events with SeqNum > resumeFromSeq".
//
// diagnosis: a failure means the HA gateway streaming-reconnect contract
// is broken on a real multi-replica cluster. Either the two replicas do
// not share session and event state (a session created on replica A is
// invisible on replica B, or B replays a divergent event backlog), or a
// reconnect on the other replica with Last-Event-ID mis-replays the
// cursor — re-delivering already-seen events (a duplicate) or skipping
// events between the cursor and the live tail (a gap in the audit
// chain). Any of these breaks the §10.1 "reconnect to any replica and
// resume" guarantee that clients depend on when the load balancer routes
// a reconnect to a different replica than the one that served the
// original stream.
func TestHAStreamingReconnectAcrossGatewayReplicas(t *testing.T) {
	c := kind.InstallLenny(t)

	// The HA contract only has meaning with at least two replicas. The
	// chart ships two; ensure it and pick two distinct Ready pods to pin
	// a driver onto each. On a cluster that cannot bring up two gateway
	// replicas this skips cleanly rather than reporting a false failure.
	ensureGatewayReplicas(t, c, 2)
	pods := readyGatewayPods(t, c)
	if len(pods) < 2 {
		t.Skipf("precondition not met: need two Ready gateway replicas for the cross-replica "+
			"reconnect contract, have %d (%v)", len(pods), pods)
	}
	podA, podB := pods[0], pods[1]
	t.Logf("pinning replica A=%s and replica B=%s", podA, podB)

	// A driver per replica: every request a driver makes lands on the
	// pod it is port-forwarded to, so the stream on A and the reconnect
	// on B genuinely traverse different replicas.
	driverA := sessiondriver.NewKeptForTarget(t, "pod/"+podA)
	defer driverA.Close()
	driverB := sessiondriver.NewKeptForTarget(t, "pod/"+podB)
	defer driverB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", haStreamingTenant, time.Now().UnixNano())
	if err := driverA.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	// Create + start the session through replica A. A becomes the
	// coordinating replica for the session.
	sess, err := driverA.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("precondition not met: warm pool not ready, no session to stream: %v", err)
		}
		t.Fatalf("create-and-start session on replica A: %v", err)
	}
	t.Logf("created session %s on replica A in state %q", sess.ID, sess.State)

	// Replica B must see the same session from shared Postgres state
	// before it can coordinate anything for it. This is the shared-
	// session half of the cross-replica contract.
	if got, err := driverB.GetSession(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("replica B cannot read the session created on replica A "+
			"(shared session state broken): %v", err)
	} else if got.ID != sess.ID {
		t.Fatalf("replica B returned session id %q, want %q", got.ID, sess.ID)
	}

	// Open the SSE stream through replica A from the beginning of the
	// retained backlog and collect whatever events exist so far.
	aEvents := streamAndCollect(t, ctx, driverA, tenant, sess.ID, 0)
	if len(aEvents.order) == 0 {
		t.Fatalf("replica A stream returned no events for a running session; "+
			"expected at least the initial status_change (session %s)", sess.ID)
	}
	assertContiguousFromOne(t, "replica A backlog", aEvents.order)
	maxSeqA := aEvents.order[len(aEvents.order)-1]
	t.Logf("replica A delivered seqs %v (cursor now %d)", aEvents.order, maxSeqA)

	// Drive the session's terminal events through replica B. A terminate
	// on B proves B can coordinate a session it did not create (shared
	// write path) and generates further monotonic events on the same
	// per-session sequence.
	if err := driverB.Terminate(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("terminate session on replica B: %v", err)
	}

	// Full backlog replayed from replica B, from the beginning. This is
	// the same session's complete event chain served by the other
	// replica out of shared externalized state.
	bFull := streamAndCollect(t, ctx, driverB, tenant, sess.ID, 0)
	if len(bFull.order) == 0 {
		t.Fatalf("replica B full-backlog stream returned no events for session %s", sess.ID)
	}
	assertContiguousFromOne(t, "replica B full backlog", bFull.order)
	n := bFull.order[len(bFull.order)-1]
	if !bFull.sawComplete {
		t.Fatalf("replica B full backlog (seqs %v) never delivered a session_complete event "+
			"after the session was terminated; the terminal event is missing from the audit chain",
			bFull.order)
	}

	// Cross-replica identity: every seq replica A delivered must be
	// present in replica B's backlog with byte-identical type and data.
	// Divergent replay across replicas would mean the buffer is not
	// shared externalized state.
	for _, seq := range aEvents.order {
		be, ok := bFull.bySeq[seq]
		if !ok {
			t.Fatalf("seq %d delivered by replica A is absent from replica B's backlog "+
				"(divergent cross-replica replay)", seq)
		}
		ae := aEvents.bySeq[seq]
		if be.Type != ae.Type || string(be.Data) != string(ae.Data) {
			t.Fatalf("seq %d differs across replicas: A={type=%q data=%s} B={type=%q data=%s}",
				seq, ae.Type, ae.Data, be.Type, be.Data)
		}
	}

	// The core reconnect assertion: reconnect the stream through replica
	// B with the Last-Event-ID cursor last seen on replica A. Replica B
	// must replay from the cursor with no duplicate (nothing at or below
	// the cursor) and no missing event (the union with A's delivered
	// seqs must be the whole contiguous chain 1..N).
	bReconnect := streamAndCollect(t, ctx, driverB, tenant, sess.ID, maxSeqA)
	for _, seq := range bReconnect.order {
		if seq <= maxSeqA {
			t.Fatalf("reconnect on replica B with Last-Event-ID=%d re-delivered seq %d "+
				"(a duplicate at or below the cursor); replayed seqs %v",
				maxSeqA, seq, bReconnect.order)
		}
	}
	assertStrictlyIncreasing(t, "replica B reconnect", bReconnect.order)

	// Union of what A delivered (1..maxSeqA) and what B replayed on
	// reconnect (>maxSeqA) must be the entire contiguous chain 1..N with
	// no gap at the handoff boundary.
	union := map[uint64]struct{}{}
	for _, seq := range aEvents.order {
		union[seq] = struct{}{}
	}
	for _, seq := range bReconnect.order {
		union[seq] = struct{}{}
	}
	for seq := uint64(1); seq <= n; seq++ {
		if _, ok := union[seq]; !ok {
			t.Fatalf("seq %d is missing from the A-then-B reconnect delivery "+
				"(gap in the audit chain across the cross-replica handoff): "+
				"A delivered %v, B reconnect (cursor %d) delivered %v, chain length %d",
				seq, aEvents.order, maxSeqA, bReconnect.order, n)
		}
	}
	t.Logf("cross-replica reconnect verified: A delivered %v, B reconnect from cursor %d delivered %v, "+
		"contiguous chain 1..%d with no duplicate or missing event",
		aEvents.order, maxSeqA, bReconnect.order, n)
}

// collectedEvents is the outcome of draining an SSE stream: the events
// keyed by sequence, the order they arrived, and whether a
// session_complete frame was seen.
type collectedEvents struct {
	bySeq       map[uint64]sessiondriver.Event
	order       []uint64
	sawComplete bool
}

// streamAndCollect opens the session event stream through the given
// driver from afterSeq and drains it until the session_complete frame
// arrives, an idle gap elapses with no further events, or an overall
// budget expires. The SSE stream stays open for a live session, so the
// idle gap is what bounds collection of a backlog that has no terminal
// frame yet.
func streamAndCollect(t *testing.T, ctx context.Context, d *sessiondriver.Driver, tenant, sessionID string, afterSeq uint64) collectedEvents {
	t.Helper()
	ch, stop, err := d.StreamEvents(ctx, tenant, sessionID, afterSeq)
	if err != nil {
		t.Fatalf("open events stream (afterSeq=%d): %v", afterSeq, err)
	}
	defer stop()

	const (
		overall = 20 * time.Second
		idle    = 3 * time.Second
	)
	out := collectedEvents{bySeq: make(map[uint64]sessiondriver.Event)}
	deadline := time.After(overall)
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			if _, dup := out.bySeq[ev.Seq]; !dup {
				out.bySeq[ev.Seq] = ev
				out.order = append(out.order, ev.Seq)
			}
			if ev.Type == "session_complete" {
				out.sawComplete = true
				return out
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idle)
		case <-idleTimer.C:
			return out
		case <-deadline:
			return out
		case <-ctx.Done():
			return out
		}
	}
}

// assertContiguousFromOne fails the test unless seqs is exactly
// 1,2,...,len (strictly increasing from the first per-session sequence
// with no gap or repeat), matching the §7.1 monotonic SeqNum guarantee.
func assertContiguousFromOne(t *testing.T, where string, seqs []uint64) {
	t.Helper()
	for i, seq := range seqs {
		want := uint64(i + 1)
		if seq != want {
			t.Fatalf("%s: seqs are not contiguous from 1: got %v, first divergence at index %d (want %d, got %d)",
				where, seqs, i, want, seq)
		}
	}
}

// assertStrictlyIncreasing fails the test unless seqs is strictly
// increasing (no duplicate, no out-of-order delivery).
func assertStrictlyIncreasing(t *testing.T, where string, seqs []uint64) {
	t.Helper()
	if !sort.SliceIsSorted(seqs, func(i, j int) bool { return seqs[i] < seqs[j] }) {
		t.Fatalf("%s: seqs are not sorted ascending: %v", where, seqs)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] == seqs[i-1] {
			t.Fatalf("%s: duplicate seq %d in %v", where, seqs[i], seqs)
		}
	}
}

// ensureGatewayReplicas scales the lenny-gateway Deployment up to want
// replicas when it currently runs fewer, waits for the rollout, and
// registers a cleanup that restores the original count. When the
// Deployment already runs at least want replicas it only waits for them
// to be Ready. A scale or wait failure skips the test as environmental
// rather than failing it, matching the InstallLenny precondition model.
func ensureGatewayReplicas(t *testing.T, c *kind.Cluster, want int) {
	t.Helper()
	const ns = "lenny-system"
	out, err := c.KubectlOut(t, "-n", ns, "get", "deploy", "lenny-gateway", "-o", "jsonpath={.spec.replicas}")
	if err != nil {
		t.Skipf("precondition not met: cannot read lenny-gateway replica count: %v\n%s", err, out)
	}
	cur, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		t.Skipf("precondition not met: unexpected lenny-gateway replica count %q: %v", out, convErr)
	}
	if cur < want {
		if scaleOut, err := c.KubectlOut(t, "-n", ns, "scale", "deploy", "lenny-gateway",
			"--replicas="+strconv.Itoa(want)); err != nil {
			t.Skipf("precondition not met: cannot scale lenny-gateway to %d: %v\n%s", want, err, scaleOut)
		}
		orig := cur
		t.Cleanup(func() {
			_, _ = c.KubectlOut(t, "-n", ns, "scale", "deploy", "lenny-gateway",
				"--replicas="+strconv.Itoa(orig))
		})
		if rollout, err := c.KubectlOut(t, "-n", ns, "rollout", "status", "deploy/lenny-gateway",
			"--timeout=120s"); err != nil {
			t.Skipf("precondition not met: lenny-gateway rollout to %d replicas did not complete: %v\n%s",
				want, err, rollout)
		}
	}
	if waitOut, err := c.KubectlOut(t, "-n", ns, "wait", "--for=condition=Ready", "pod",
		"-l", gatewayComponentSelector, "--timeout=120s"); err != nil {
		t.Skipf("precondition not met: gateway pods did not become Ready: %v\n%s", err, waitOut)
	}
}

// readyGatewayPods returns the names of the Running gateway pods. The
// caller has already waited for readiness via ensureGatewayReplicas.
func readyGatewayPods(t *testing.T, c *kind.Cluster) []string {
	t.Helper()
	const ns = "lenny-system"
	out, err := c.KubectlOut(t, "-n", ns, "get", "pods",
		"-l", gatewayComponentSelector,
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		t.Skipf("precondition not met: cannot list gateway pods: %v\n%s", err, out)
	}
	var pods []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			pods = append(pods, name)
		}
	}
	return pods
}
