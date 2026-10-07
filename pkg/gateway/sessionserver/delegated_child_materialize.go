// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DelegatedChildNotCreatedError is returned by MaterializeDelegatedChild
// when the loaded child row is not in session.StateCreated. Materialization
// claims a warm pod and assigns a credential lease, so it must run exactly
// once against a freshly committed child; a second call (or a call against a
// row that already left `created`) is a caller bug. Returning a typed error
// lets the caller distinguish it from a claim or assignment failure and fail
// closed without re-claiming a pod for an already-materialized child.
type DelegatedChildNotCreatedError struct {
	ChildID string
	State   session.State
}

func (e *DelegatedChildNotCreatedError) Error() string {
	return fmt.Sprintf("delegated child %s is in state %q, want %q for materialization",
		e.ChildID, e.State, session.StateCreated)
}

// MaterializeDelegatedChild runs the §8.2 delegation steps that allocate the
// child pod, stream the rebased exported files, and launch, for a child that
// the delegation service already committed in session.StateCreated with a
// stamped WorkspacePlan and no PodAssignment. It composes the decomposed
// create-and-start sub-phases — claimAtCreate, the §4.9 pre-claim credential
// resolution it folds in, startOnPod, and registerBinding — to claim a warm
// pod, assign the credential lease, stream the exported files through the §6.3
// binder, launch, transition the existing row StateCreated -> running via
// store.Update, and publish the bind into the shared executor Registry. It
// performs no store.Create: the row already exists. When the gateway runs
// without a pod binder (a minimal or dev-mode deployment) it skips the claim
// and launch and transitions the row directly, mirroring the top-level
// create-and-start path at mintClaimStartPersist.
//
// registerBinding is required for reachability: startOnPod returns a
// *podsession.BindResult but does not register it, and PodExecutor.streamFor
// resolves the session through the shared *podsession.Registry that
// registerBinding populates via podRegistry.Put. Without it Executor.Send
// still rejects the child as unbound. registerBinding also persists the pod
// assignment and workspace root so a fresh replica can recover the binding. As
// on the create path, recordSessionCreated and registerLeaseTree (§8.6
// lease-extension budget) run after the successful transition.
//
// On success it returns the child's post-materialization state
// (session.StateRunning) so the caller builds its task handle from the live
// transitioned state rather than the pre-materialization StateCreated snapshot.
//
// It fails closed on any failure and leaves the row non-running. On a
// pod-claim or credential-assignment failure before launch it releases the
// claimed pod via rollbackClaim and returns the router's typed sentinel
// (credrouter.ErrNoCredentialAvailable, *podsession.CredentialAssignmentError,
// *podsession.PoolWarmingError, credassign.ErrTokenServiceUnavailable, or
// credrouter.ErrUserCredentialNotFound) so the caller can map it to the same
// codes the §8.3 delegation-time pre-check emits. On a store.Update-to-running
// failure after a successful startOnPod it releases the bound pod and its
// assigned credential lease via rollbackBinding before registerBinding runs,
// mirroring the top-level create path's post-launch persist rollback, so no
// pod, lease, or registry entry leaks past the failed persist.
// spec: §8.2.
func (s *Server) MaterializeDelegatedChild(ctx context.Context, tenantID, childID string) (session.State, error) {
	row, err := s.store.Get(ctx, tenantID, childID)
	if err != nil {
		return "", fmt.Errorf("load delegated child %s: %w", childID, err)
	}
	// spec: §8.2 — materialization claims a pod and assigns a lease exactly
	// once against a freshly committed StateCreated child. A non-created row is
	// a caller bug (a double-materialization would re-claim a pod), so fail
	// closed with a typed error and claim nothing.
	if row.State != session.StateCreated {
		return "", &DelegatedChildNotCreatedError{ChildID: childID, State: row.State}
	}
	plan, err := storedWorkspacePlan(row)
	if err != nil {
		return "", fmt.Errorf("parse stored workspace plan for delegated child %s: %w", childID, err)
	}

	// When the gateway is wired with a pod binder, run the §8.2 steps 5-8 claim
	// and launch phases against a warm pod. A minimal or dev-mode gateway that
	// runs without a pod binder skips the claim exactly as the top-level
	// create-and-start path does at mintClaimStartPersist, transitioning the
	// child to running without a pod so the in-process delegation flow stays
	// exercised. spec: §8.2.
	var (
		bound       *podsession.BindResult
		createClaim *podsession.ClaimResult
		level       SessionIsolationLevel
		claimed     bool
	)
	if s.podBinder != nil {
		// spec: §8.2 step 5 / §4.9 / §7.1 step 3 — claim the warm pod and run
		// the pre-claim credential availability resolution in one phase, exactly
		// as the top-level create path does at claimAtCreate. On failure no pod
		// is claimed (the pre-check gates the claim), so the typed sentinel
		// returns directly.
		outcome, err := s.claimAtCreate(ctx, row, plan, claimRouteStart)
		if err != nil {
			return "", err
		}
		claimed = true
		level = outcome.Level
		row.ExecutionMode = level.ExecutionMode
		row.ScrubPolicy = level.ScrubPolicy
		row.ConversationContinuity = level.ConversationContinuity
		// spec: §4.9 — thread the create-time credential resolution into the
		// same-call startOnPod so the §4.9 pre-check runs exactly once before
		// the claim rather than re-resolving at the prepare dispatch, mirroring
		// mintClaimStartPersist.
		startCtx := &startContext{
			CredPools:         outcome.CredPools,
			PoolDeliveryModes: outcome.PoolDeliveryModes,
			UserCredProviders: outcome.UserCredProviders,
		}
		if outcome.Claim != nil {
			createClaim = outcome.Claim
			row.PodAssignment = createClaim.SandboxName
			row.PoolRef = createClaim.Pool
			startCtx.PodClaim = createClaim.PodClaim
		}

		// spec: §8.2 steps 6-8 — stream the stamped exported files through the
		// §6.3 binder and launch against the claimed pod. On failure release the
		// create-time claim (unless the binder already owns its release) and
		// return the typed sentinel; the row stays StateCreated, so no pod leaks.
		bound, err = s.startOnPod(ctx, row, plan, startCtx)
		if err != nil {
			if createClaimNeedsRollback(createClaim, err) {
				s.rollbackClaim(ctx, createClaim, row.ID)
			}
			return "", err
		}
	}

	if bound != nil {
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the delegated-child materialize
		// commits the row to `running` with pod_assignment through the
		// store.Update below, before registerBinding, so acquire the
		// coordination lease here, ahead of that commit, so a peer sweep does
		// not read the committed running-pod row with an unheld lease as
		// adoptable and steal it. On ErrHeld (a raced peer) fail closed:
		// release the bound pod before the commit.
		if err := s.acquireCoordinationLease(ctx, tenantID, childID); err != nil {
			s.rollbackBinding(ctx, bound)
			return "", fmt.Errorf("acquire coordination lease for delegated child %s: %w", childID, err)
		}
	}

	// spec: §8.2 step 9 / §8.8 — transition the existing StateCreated row to
	// running via store.Update (no second store.Create). A persist failure
	// after a successful launch releases the bound pod and its assigned
	// credential lease via rollbackBinding before registerBinding runs, so no
	// pod, credential lease, or registry entry leaks past the failed write.
	// spec: §4.6.1, §10.1 — it also releases the coordination lease acquired
	// above ahead of this commit; the child row stays in StateCreated
	// (non-terminal), so without the release a peer Sweeper's priorHolder
	// renew branch would keep renewing the orphaned lease for a session that
	// will never serve, holding a lease with no binding.
	updated, err := s.store.Update(ctx, tenantID, childID, func(rr *sessionstore.Session) error {
		transitionStart(rr)
		if claimed {
			rr.ExecutionMode = level.ExecutionMode
			rr.ScrubPolicy = level.ScrubPolicy
			rr.ConversationContinuity = level.ConversationContinuity
		}
		if createClaim != nil {
			rr.PoolRef = createClaim.Pool
		}
		if bound != nil {
			rr.PodAssignment = bound.SandboxName
			rr.SetupOutput = setupOutputsFromBind(bound.SetupOutputs)
		}
		return nil
	})
	if err != nil {
		if bound != nil {
			s.releaseCoordinationLease(ctx, tenantID, childID)
		}
		s.rollbackBinding(ctx, bound)
		return "", fmt.Errorf("transition delegated child %s to running: %w", childID, err)
	}
	s.recordSessionCreated(ctx, updated)
	// §8.6: register the tree's lease-extension budget. A delegated child
	// carries a ParentSessionID, so registerLeaseTree no-ops here (the root
	// registered the tree); the call mirrors the create path for uniformity.
	s.registerLeaseTree(updated)
	// Publish the BindResult into the shared podRegistry so
	// PodExecutor.streamFor resolves the child and persist the pod assignment
	// and workspace root for cross-replica recovery. Without it Executor.Send
	// still rejects the child as unbound. The coordination lease was already
	// acquired above ahead of the running-commit, so the publish is
	// unconditional rather than routed through registerBinding's self-renew
	// acquire: gating it on a second acquire would let a transient leaseStore
	// error skip the publish while the row is already committed to running and
	// this replica holds the lease, stranding a committed running child with a
	// held lease but no binding. spec: §4.6.1, §10.1.
	stampBindingGeneration(bound, updated.CoordinationGeneration)
	s.publishBinding(ctx, bound)
	return session.StateRunning, nil
}
