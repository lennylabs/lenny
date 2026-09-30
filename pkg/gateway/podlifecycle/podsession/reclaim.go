// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"log"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// reclaimOnFailure is the body of Prepare's and Launch's reclaim closures.
// A typed §4.7.1 slot-bind refusal (IsSlotBindRefusal) means the pod is
// serving the session correctly, under another bind attempt's identity or
// with the session already started, so it is not a pod failure: the closure
// closes the connection and calls neither failPhase nor drain, and the call
// site returns the refusal. Draining there would retire a healthy pod, on
// the Prepare path the winner's pod on behalf of the loser of two concurrent
// attempts, and failPhase's session-keyed lease release would strip the
// §4.9 leases a started session authenticates with. Any other cause keeps
// the failPhase reclaim.
//
// spec: §4.7.1 (role and gateway RPC contract); §6.2 (pod state machine);
// §7.1 (normal flow).
func (b *Binder) reclaimOnFailure(ctx context.Context, sb *lennyv1.Sandbox, cl *adapterclient.Client, leaseAssigned bool, sessionID string, cause error) {
	defer cl.Close()
	if adapterclient.IsSlotBindRefusal(cause) {
		log.Printf("podsession: slot bind refused on sandbox %s for session %s; pod left undrained: %v", sb.Name, sessionID, cause)
		return
	}
	b.failPhase(ctx, sb, leaseAssigned, sessionID)
}

// failPhase reclaims a claimed pod whose setup chain aborted before the
// session was reported running. A pre-attached setup failure is a terminal
// claim disposition: the pod cannot serve the session and is retired by
// draining it (the coarse §6.2 claimed → draining → terminated edge), so
// the warm-pool sizer provisions a replacement. When leaseAssigned is true
// the finalize block had already pushed the §4.9 credential lease to the
// pod, so the reclaim also revokes the lease back to its pool (§7.1 step 23)
// rather than leaking the credential's active-session slot; leaseAssigned is
// false before assignCredentials runs, so the revoke is a no-op then (Gap 2).
// It is best-effort — the caller already returns the underlying error, and a
// drain lost to a concurrent reclamation does not change the outcome, because
// the orphaned claim is collected and the pod recycled (§4.6.1). spec: §6.2
// (claimed → draining on a failed claim disposition); §4.6.1 orphan-claim
// collection; §7.1 step 23 (lease release).
func (b *Binder) failPhase(ctx context.Context, sb *lennyv1.Sandbox, leaseAssigned bool, sessionID string) {
	if leaseAssigned {
		// spec: §7.1 step 23 — a lease assigned earlier in this phase must be
		// returned to its §4.9 pool on the reclaim, or the credential's
		// active-session counter leaks for the abandoned session.
		b.releaseCredentials(sessionID)
	}
	if err := b.drain(ctx, sb); err != nil {
		log.Printf("podsession: drain failed sandbox %s after pre-attached setup failure: %v", sb.Name, err)
	}
}

// ReclaimClaimed releases a pod claimed at /create that no live BindResult
// covers: a `created`/`finalizing`/`ready` session retired by the §4.5
// created-expiry sweeper or by /terminate, whose pod↔session binding is the
// persisted SandboxName + pool alone (§4.6). It deletes the per-pod
// SandboxClaim (returning the pod to the pool per the §4.6.1 occupancy
// projection) and revokes any §4.9 credential lease the session holds, keyed
// by sessionID (Credentials.ReleaseSession). The lease revoke is mandatory
// rather than best-effort-skipped: a `finalizing`/`ready` session always
// holds a lease assigned at finalize (§4.3), and ReleaseSession is a no-op
// for a `created` session that never assigned one, so the unconditional
// revoke fails closed without over-releasing. spec: §4.5 (proposal), §4.6
// (proposal), §7.1 step 23 (lease release); §4.6.1 (occupancy projection on
// claim DELETE).
func (b *Binder) ReclaimClaimed(ctx context.Context, sandboxName, sessionID string) error {
	// Revoke the lease first so a DELETE error does not strand the
	// credential's active-session slot; ReleaseSession is keyed by sessionID
	// and is a no-op for a session that holds no lease.
	b.releaseCredentials(sessionID)
	if err := podclaim.DeleteClaim(ctx, b.Client, b.Namespace, sandboxName); err != nil {
		return fmt.Errorf("podsession: reclaim claimed pod %s for session %s: %w", sandboxName, sessionID, err)
	}
	return nil
}

// drain releases a session-mode pod by deleting its per-pod occupancy
// SandboxClaim. The gateway does not write Sandbox.status (§4.6.3 ownership
// decomposition): the WarmPoolController is the sole writer of the coarse
// occupancy phase and projects it at the claim DELETE (§4.6.1).
// Deleting the claim is the gateway's reclaim action. The delete
// is idempotent — a claim already gone (a double release, or one the orphan
// GC collected) is a no-op.
//
// spec: §4.6.1 (occupancy projection on claim DELETE); §4.6.3 (gateway is
// not a writer of Sandbox.status). The WarmPoolController-side projection
// that consumes the claim DELETE lands in the WPC occupancy-projection step.
func (b *Binder) drain(ctx context.Context, sb *lennyv1.Sandbox) error {
	return podclaim.DeleteClaim(ctx, b.Client, b.Namespace, sb.Name)
}
