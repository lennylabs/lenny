// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the gateway session-continuity invariant under
// replica loss. It pins a session onto gateway replica A, collects the
// session's durable event backlog through A, then deletes pod A entirely
// and resumes the same session through a different replica B. It asserts
// that after the coordinating replica is gone the surviving replica still
// reads the session from durable state, replays the full pre-loss event
// backlog A delivered (byte-identical, contiguous, no rewind or
// duplicate), reattaches the live stream, and drives the session to
// completion.
//
// This is the first tier-5 coverage of the replica-loss half of the
// horizontal-scaling contract. ha_streaming_replica_test.go drives a
// cross-replica handoff with both replicas alive; it never removes the
// coordinating replica, so it does not prove the invariant that survives
// the loss of the replica that created the session. The helpers this file
// uses (ensureGatewayReplicas, readyGatewayPods, streamAndCollect,
// assertContiguousFromOne, assertStrictlyIncreasing, and the
// gatewayComponentSelector const) are defined in ha_streaming_replica_test.go.

package tier5_e2e_kind_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// replicaContinuityTenant is the synthetic tenant this test bootstraps. A
// per-run suffix (below) sidesteps a stale tenant left behind by a prior
// run on the persistent e2e cluster, matching ha_streaming_replica_test.go.
const replicaContinuityTenant = "replica-continuity-tenant"

// spec: §4.1 (spec/04_system-components.md, Edge Gateway Replicas) "Key
// invariant: A client can land on any gateway replica. Session state is
// always in durable stores." + §10.1 (spec/10_gateway-internals.md,
// Horizontal Scaling) "Correctness rule: Sticky routing is an
// optimization. A client can reconnect to any replica and resume." /
// "If that replica dies, another picks up after TTL expiry." / "Gateway
// pod failure causes a broken stream and reconnect, never session loss."
// + §10.1 (Per-session SeqNum counter durability) "The SessionEvent.SeqNum
// counter ... MUST survive coordinator handoff without rewinds or
// duplicates."
//
// diagnosis: a failure means the defining gateway correctness invariant is
// broken on a real multi-replica cluster: a session does not survive the
// loss of the replica that created it. Either the surviving replica cannot
// read the session after the original coordinator is deleted (session state
// was not in durable stores, so it died with replica A), or the surviving
// replica replays a divergent or truncated event backlog (the per-session
// event chain was coordinator-local rather than reconstructed from durable
// Postgres, violating the SeqNum-survives-handoff guarantee), or the
// surviving replica cannot reattach and drive the session to completion
// (coordinator handoff after replica loss does not recover coordination).
// Any of these breaks the §4.1 "a client can land on any gateway replica"
// invariant that clients depend on when a load balancer routes them to a
// replica other than the one now gone.
func TestSessionContinuesAfterCoordinatingGatewayReplicaLoss(t *testing.T) {
	c := kind.InstallLenny(t)

	// The invariant only has meaning with at least two replicas: one to
	// create the session and lose, one to survive and resume. The chart
	// ships two; ensure it and pick two distinct Ready pods. On a cluster
	// that cannot bring up two gateway replicas this skips cleanly rather
	// than reporting a false failure.
	ensureGatewayReplicas(t, c, 2)
	pods := readyGatewayPods(t, c)
	if len(pods) < 2 {
		t.Skipf("precondition not met: need two Ready gateway replicas for the replica-loss "+
			"continuity contract, have %d (%v)", len(pods), pods)
	}
	podA, podB := pods[0], pods[1]
	t.Logf("pinning coordinating replica A=%s (to be deleted) and surviving replica B=%s", podA, podB)

	// A driver pinned to each replica: every request driverA makes lands
	// on podA, and every request driverB makes lands on podB. driverB is
	// the one that must survive podA's deletion, so it stays open for the
	// whole test; driverA is only used before the deletion.
	driverA := sessiondriver.NewKeptForTarget(t, "pod/"+podA)
	defer driverA.Close()
	driverB := sessiondriver.NewKeptForTarget(t, "pod/"+podB)
	defer driverB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Bootstrap the tenant through the surviving replica so its Close-time
	// cleanup does not depend on the replica this test deletes.
	tenant := fmt.Sprintf("%s-%d", replicaContinuityTenant, time.Now().UnixNano())
	if err := driverB.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	// Create and start the session through replica A. A becomes the
	// coordinating replica for the session and claims its agent pod.
	sess, err := driverA.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		if errors.Is(err, sessiondriver.ErrPoolNotReady) {
			t.Skipf("precondition not met: warm pool not ready, no session to continue: %v", err)
		}
		t.Fatalf("create-and-start session on replica A: %v", err)
	}
	t.Logf("created session %s on replica A in state %q", sess.ID, sess.State)

	// Capture the durable event backlog through replica A while A still
	// coordinates. Every seq here must reappear identically through B
	// after A is gone; that is the "session state is in durable stores"
	// proof.
	aEvents := streamAndCollect(t, ctx, driverA, tenant, sess.ID, 0)
	if len(aEvents.order) == 0 {
		t.Fatalf("replica A stream returned no events for a running session; "+
			"expected at least the initial status_change (session %s)", sess.ID)
	}
	assertContiguousFromOne(t, "replica A backlog", aEvents.order)
	maxSeqA := aEvents.order[len(aEvents.order)-1]
	t.Logf("replica A delivered seqs %v before deletion (cursor now %d)", aEvents.order, maxSeqA)

	// Kill the coordinating replica. Deleting pod A removes the replica
	// that created and coordinates the session; the Deployment schedules a
	// replacement, but replica B (pinned, untouched) must be able to serve
	// the session in the meantime. Wait for the pod to be fully gone so the
	// continuation below genuinely runs without the original coordinator.
	deleteGatewayPodAndWait(t, c, podA)
	t.Logf("deleted coordinating replica A=%s; continuing session %s through surviving replica B=%s",
		podA, sess.ID, podB)

	// Durable session state: replica B must still read the session created
	// on the now-deleted replica A. If this fails, session truth was not
	// externalized and died with replica A.
	if got, err := driverB.GetSession(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("surviving replica B cannot read the session created on the deleted replica A "+
			"(session state was not in durable stores): %v", err)
	} else if got.ID != sess.ID {
		t.Fatalf("surviving replica B returned session id %q, want %q", got.ID, sess.ID)
	}

	// Drive the session's terminal transition through replica B. A
	// terminate on B proves B can coordinate a session whose original
	// coordinator is gone (coordinator handoff after replica loss) and
	// advances the same durable per-session sequence.
	terminateWithHandoffRetry(t, ctx, driverB, tenant, sess.ID)

	// Full backlog replayed from replica B, from the beginning. This is the
	// same session's complete durable event chain served by the surviving
	// replica after the original coordinator was deleted.
	bFull := streamAndCollect(t, ctx, driverB, tenant, sess.ID, 0)
	if len(bFull.order) == 0 {
		t.Fatalf("surviving replica B full-backlog stream returned no events for session %s", sess.ID)
	}
	assertContiguousFromOne(t, "replica B full backlog", bFull.order)
	n := bFull.order[len(bFull.order)-1]
	if !bFull.sawComplete {
		t.Fatalf("surviving replica B full backlog (seqs %v) never delivered a session_complete event "+
			"after the session was terminated on B; the session did not reach completion after replica loss",
			bFull.order)
	}

	// History preservation across replica loss: every seq replica A
	// delivered before it was deleted must be present in replica B's
	// backlog with byte-identical type and data. A missing or divergent
	// event would mean the pre-loss chain was coordinator-local rather than
	// reconstructed from durable Postgres.
	for _, seq := range aEvents.order {
		be, ok := bFull.bySeq[seq]
		if !ok {
			t.Fatalf("seq %d delivered by replica A before its deletion is absent from surviving "+
				"replica B's backlog (history lost across replica loss)", seq)
		}
		ae := aEvents.bySeq[seq]
		if be.Type != ae.Type || string(be.Data) != string(ae.Data) {
			t.Fatalf("seq %d differs across the replica-loss boundary: A={type=%q data=%s} B={type=%q data=%s}",
				seq, ae.Type, ae.Data, be.Type, be.Data)
		}
	}

	// Reattach from the cursor last seen on the deleted replica A. The
	// surviving replica must resume from the cursor with no rewind and no
	// duplicate (nothing at or below the cursor) and no missing event (the
	// union with A's delivered seqs is the whole contiguous chain 1..N),
	// honoring the SeqNum-survives-handoff guarantee.
	bReattach := streamAndCollect(t, ctx, driverB, tenant, sess.ID, maxSeqA)
	for _, seq := range bReattach.order {
		if seq <= maxSeqA {
			t.Fatalf("reattach on surviving replica B with Last-Event-ID=%d re-delivered seq %d "+
				"(a rewind/duplicate at or below the cursor after replica loss); replayed seqs %v",
				maxSeqA, seq, bReattach.order)
		}
	}
	assertStrictlyIncreasing(t, "replica B reattach", bReattach.order)

	union := map[uint64]struct{}{}
	for _, seq := range aEvents.order {
		union[seq] = struct{}{}
	}
	for _, seq := range bReattach.order {
		union[seq] = struct{}{}
	}
	for seq := uint64(1); seq <= n; seq++ {
		if _, ok := union[seq]; !ok {
			t.Fatalf("seq %d is missing from the A-then-B reattach delivery across the replica-loss "+
				"handoff: A delivered %v before deletion, B reattach (cursor %d) delivered %v, chain length %d",
				seq, aEvents.order, maxSeqA, bReattach.order, n)
		}
	}
	t.Logf("session survived replica loss: A delivered %v before deletion, B reattach from cursor %d "+
		"delivered %v, contiguous chain 1..%d with no rewind, duplicate, or missing event",
		aEvents.order, maxSeqA, bReattach.order, n)
}

// deleteGatewayPodAndWait deletes the named gateway pod and waits for it to
// be fully removed, so a continuation on another replica genuinely runs
// without the deleted coordinator. A delete or wait failure skips the test
// as environmental rather than failing it, matching the InstallLenny
// precondition model the sibling helpers use.
func deleteGatewayPodAndWait(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	const ns = "lenny-system"
	if out, err := c.KubectlOut(t, "-n", ns, "delete", "pod", pod, "--wait=false"); err != nil {
		t.Skipf("precondition not met: cannot delete gateway pod %s: %v\n%s", pod, err, out)
	}
	if out, err := c.KubectlOut(t, "-n", ns, "wait", "--for=delete", "pod/"+pod,
		"--timeout=180s"); err != nil {
		t.Skipf("precondition not met: gateway pod %s did not terminate within the timeout: %v\n%s",
			pod, err, out)
	}
}

// terminateWithHandoffRetry issues Terminate through the surviving replica,
// retrying while coordination handoff after the loss of the original
// coordinator settles. After an abrupt or graceful loss of the coordinating
// replica, the surviving replica acquires coordination via the §10.1 lease
// path; a terminate issued before that settles can transiently fail. The
// retry window covers the lease-TTL / handoff interval bounded by the
// caller's context.
func terminateWithHandoffRetry(t *testing.T, ctx context.Context, d *sessiondriver.Driver, tenant, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for {
		if err := d.Terminate(ctx, tenant, sessionID); err == nil {
			return
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			// A terminate that never succeeds is itself the failure the
			// invariant forbids: the surviving replica could not take over
			// coordination after the original coordinator was lost.
			t.Fatalf("surviving replica could not terminate session %s after the coordinating "+
				"replica was deleted (coordination handoff after replica loss failed): %v",
				sessionID, lastErr)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while waiting for coordination handoff to terminate session %s: %v",
				sessionID, ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}
