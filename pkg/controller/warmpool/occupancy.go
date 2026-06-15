// SPDX-License-Identifier: MIT

package warmpool

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// occupancy is the projection input the OccupancyReconciler reduces one
// Sandbox plus its per-pod SandboxClaim to. It is the level-triggered view
// that ProjectOccupancyPhase maps to the coarse Sandbox.status.phase, kept
// as a pure value so the §4.6.1 / §6.14 projection table can be exhaustively
// unit-tested without a cluster.
//
// spec: §4.6.1 (occupancy projection), §6.14 (binding-state enumeration).
type occupancy struct {
	// Current is the Sandbox's live status.phase.
	Current state.State
	// HasClaim reports whether a per-pod SandboxClaim (claim-<podName>)
	// exists for the Sandbox.
	HasClaim bool
	// Binding is the claim's status.phase binding state when HasClaim is
	// true; empty otherwise (or while a freshly created claim has not yet
	// received its first bound status patch).
	Binding claimstate.State
	// RewarmStarted reports whether the claim carries a rewarmStartedAt
	// stamp. On a recycling claim it moves the projection from claimed
	// (whole-pod scrub) to sdk_connecting (the preConnect SDK re-warm leg).
	RewarmStarted bool
}

// ProjectOccupancyPhase computes the coarse Sandbox.status.phase as a
// level-triggered projection of per-pod SandboxClaim existence, the claim's
// binding state, and the rewarm stamp (§4.6.1, §6.2, §6.14). It returns the
// projected phase and whether the projection owns the pod's phase at all:
// when ok is false the warm-fill and SDK-warm legs the Sandbox-to-Pod
// reconciler owns (warming→idle, the preConnect pre-idle sdk_connecting, idle,
// and the terminal phases) are left untouched, so the occupancy projection
// never fights the warm-fill writer for an unclaimed pod.
//
// The pool's recycle policy does not enter the projection directly: the §6.2
// state machine encodes the recycle-versus-one-session distinction in the
// phase the pod sits in at the claim DELETE. On a recycling pool the gateway
// patches the claim claimed → recycling → reserved before the hold-expiry
// DELETE, so the recycling-pool return-to-idle is the reserved → idle edge,
// while a one-session-only (recycle.enabled: false) release deletes the claim
// while the pod is still claimed and the projection drains it.
//
// The projection table (§6.5/§6.37):
//   - claim binding bound → claimed.
//   - claim binding recycling with no rewarm stamp → claimed (the whole-pod
//     scrub runs while the pod projects claimed on both pool kinds, so the
//     sdkConnectTimeoutSeconds watchdog does not run during the scrub).
//   - claim binding recycling with a rewarm stamp → sdk_connecting (the
//     preConnect SDK re-warm leg; the watchdog clock is armed from the stamp).
//   - claim binding reserved → reserved (scrubbed, SDK-warm on preConnect
//     pools, held for the pinned tenant and excluded from idle inventory).
//   - claim binding released or failed → draining (terminal disposition; the
//     pod is drained then terminated rather than returned to the pool).
//   - no claim on a reserved pod → idle (hold expiry returns the scrubbed,
//     SDK-warm pod to the pool).
//   - no claim on a claimed pod → draining (the claim DELETE on an unscrubbed
//     occupied pod retires it; returning it to idle would break the
//     scrub-before-idle invariant of §5.2).
//
// spec: §4.6.1 (occupancy projection), §6.2 (coarse pod state machine,
// recycle edges), §6.14 (SandboxClaim binding-state enumeration).
func ProjectOccupancyPhase(o occupancy) (state.State, bool) {
	if o.HasClaim {
		switch o.Binding {
		case claimstate.Bound:
			return state.Claimed, true
		case claimstate.Recycling:
			// The whole-pod scrub runs while the pod projects claimed; the
			// preConnect SDK re-warm that follows it (marked by the rewarm
			// stamp) projects sdk_connecting so only the re-warm leg is
			// measured against the sdkConnectTimeoutSeconds watchdog.
			if o.RewarmStarted {
				return state.SDKConnecting, true
			}
			return state.Claimed, true
		case claimstate.Reserved:
			return state.Reserved, true
		case claimstate.Released, claimstate.Failed:
			// Terminal claim disposition: the projection drains then
			// terminates the pod rather than returning it to the pool.
			return state.Draining, true
		default:
			// A freshly created claim with no binding-state patch yet: the
			// pod is acquired but not bound. Leave the warm-fill phase
			// untouched until the gateway writes the first bound state; the
			// orphan GC reclaims a claim stuck without a binding state.
			return "", false
		}
	}

	// No claim. The projection owns the phase only when the pod sits in an
	// occupied phase a claim previously established (claimed or reserved):
	// the claim was deleted (hold expiry, orphan GC, or release). The phase
	// the pod sits in at the DELETE selects the edge, and the §6.2 state
	// machine constrains it: the scrub-before-idle invariant means only a
	// reserved pod (scrubbed and, on preConnect pools, SDK-warm) returns to
	// idle, while an unscrubbed claimed pod retires through draining. On a
	// recycling pool the gateway patches the claim claimed → recycling →
	// reserved before the hold-expiry DELETE, so the recycling-pool
	// return-to-idle is the reserved → idle edge; a claimed pod whose claim
	// is deleted (a non-recycling release, the one-session-only invariant,
	// or an orphan-GC reclaim of a still-claimed pod that was never scrubbed)
	// drains. A pod in any warm-fill phase (warming, idle, the pre-idle
	// sdk_connecting leg) or a terminal phase is the Sandbox-to-Pod
	// reconciler's to own.
	switch o.Current {
	case state.Reserved:
		// Hold expiry on a scrubbed, SDK-warm reserved pod: the claim DELETE
		// returns it to idle with no second re-warm (the §6.2 reserved → idle
		// edge).
		return state.Idle, true
	case state.Claimed:
		// Claim deleted on an unscrubbed occupied pod: retire it through
		// draining (the §6.2 claimed → draining edge). Returning it to idle
		// would break the scrub-before-idle invariant (§5.2), because the
		// whole-pod scrub runs in the reserved-bound recycle path the pod
		// never reached.
		return state.Draining, true
	default:
		return "", false
	}
}

// OccupancyReconciler is the §4.6.1 claim-driven occupancy projection arm of
// the WarmPoolController. It reconciles Sandboxes and additionally wakes on
// SandboxClaim changes mapped to their owning Sandbox (via spec.sandboxRef,
// the deterministic claim-<podName> name), so every binding-state transition
// re-projects the Sandbox phase. It is the sole writer of the coarse
// occupancy phases the per-pod claim drives (claimed, reserved, the recycle
// sdk_connecting re-warm leg, and the claim-DELETE return to idle or
// draining); the Sandbox-to-Pod reconciler keeps the warm-fill and SDK-warm
// pre-idle legs. Both write Sandbox.status.phase under the single
// lenny-warm-pool-controller field manager, so the §4.6.3 sole-writer
// invariant holds across the two arms.
//
// The gateway never writes Sandbox.status (§4.6.3 ownership decomposition):
// it creates and deletes the claim and writes the claim binding state, and
// this projection turns that into the pod phase.
//
// spec: §4.6.1 (occupancy projection), §4.6.3 (ownership decomposition),
// §6.2 (coarse pod state machine), §6.14 (binding-state enumeration).
type OccupancyReconciler struct {
	// Client is the controller-runtime client backed by the manager cache.
	Client client.Client

	// MaxConcurrentReconciles is the §4.6.1 worker count
	// (--max-concurrent-reconciles). Zero or negative selects the
	// controller-runtime default of 1.
	MaxConcurrentReconciles int
	// QueueFactory, when set, supplies the §4.6.1 bounded,
	// depth-instrumented reconciliation work queue. A nil factory uses the
	// controller-runtime default queue.
	QueueFactory controllermetrics.QueueFactory
}

// Reconcile projects one Sandbox's coarse occupancy phase from its per-pod
// SandboxClaim. It reads the claim (if any), computes the projected phase,
// and SSA-patches it under the WPC field manager when it changes.
func (r *OccupancyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var sb lennyv1.Sandbox
	if err := r.Client.Get(ctx, req.NamespacedName, &sb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// A Sandbox being torn down is the Sandbox-to-Pod reconciler's to drive
	// to terminated; re-projecting its phase here is pointless and would
	// fight the teardown.
	if sb.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	binding, rewarm, hasClaim, err := r.observeClaim(ctx, sb.Namespace, sb.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	projected, ok := ProjectOccupancyPhase(occupancy{
		Current:       state.State(sb.Status.Phase),
		HasClaim:      hasClaim,
		Binding:       binding,
		RewarmStarted: rewarm,
	})
	if !ok || string(projected) == sb.Status.Phase {
		return ctrl.Result{}, nil
	}
	if err := r.patchPhase(ctx, &sb, projected); err != nil {
		return ctrl.Result{}, fmt.Errorf("project occupancy phase for sandbox %s: %w", sb.Name, err)
	}
	return ctrl.Result{}, nil
}

// observeClaim reads the per-pod SandboxClaim (claim-<podName>) for the
// Sandbox and returns its binding state, whether it carries a rewarm stamp,
// and whether it exists at all. A NotFound is the no-claim case (a released
// or never-claimed pod), reported as hasClaim=false rather than an error.
func (r *OccupancyReconciler) observeClaim(ctx context.Context, namespace, podName string) (claimstate.State, bool, bool, error) {
	var cl lennyv1.SandboxClaim
	key := client.ObjectKey{Namespace: namespace, Name: occupancyClaimName(podName)}
	if err := r.Client.Get(ctx, key, &cl); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, false, nil
		}
		return "", false, false, fmt.Errorf("get sandbox claim for %s: %w", podName, err)
	}
	return claimstate.State(cl.Status.Phase), cl.Status.RewarmStartedAt != nil, true, nil
}

// patchPhase SSA-applies the projected coarse phase onto the Sandbox under
// the WPC field manager, re-applying the live WPC-owned status fields
// (PodName/NodeName/PodIP) so the partial apply does not clobber them. The
// §4.6.3 retry policy applies on HTTP 409.
func (r *OccupancyReconciler) patchPhase(ctx context.Context, sb *lennyv1.Sandbox, phase state.State) error {
	key := client.ObjectKeyFromObject(sb)
	return retryOnConflictSSA(ctx, func(attempt int) error {
		live := sb
		if attempt > 0 {
			var fresh lennyv1.Sandbox
			if err := r.Client.Get(ctx, key, &fresh); err != nil {
				return client.IgnoreNotFound(err)
			}
			live = &fresh
		}
		if live.Status.Phase == string(phase) {
			return nil
		}
		patch := &lennyv1.Sandbox{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "Sandbox",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      live.Name,
				Namespace: live.Namespace,
			},
		}
		patch.Status.Phase = string(phase)
		patch.Status.PodName = live.Status.PodName
		patch.Status.NodeName = live.Status.NodeName
		patch.Status.PodIP = live.Status.PodIP
		patch.Status.ObservedGeneration = live.Generation
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	})
}

// occupancyClaimName is the deterministic SandboxClaim name for a pod
// (claim-<podName>), mirroring the gateway's podclaim.claimName so the
// projection resolves the same per-pod claim the gateway creates. It is
// duplicated here rather than imported because pkg/gateway/podclaim depends
// on this package; importing it back would create a cycle.
func occupancyClaimName(podName string) string {
	return "claim-" + podName
}

// claimToSandbox maps a SandboxClaim event to a reconcile request for its
// owning Sandbox (spec.sandboxRef), so a binding-state transition or a claim
// delete re-projects the Sandbox phase within one reconcile cycle.
func claimToSandbox(_ context.Context, obj client.Object) []reconcile.Request {
	cl, ok := obj.(*lennyv1.SandboxClaim)
	if !ok || cl.Spec.SandboxRef == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Namespace: cl.Namespace, Name: cl.Spec.SandboxRef},
	}}
}

// SetupWithManager registers the occupancy-projection reconciler. It
// reconciles Sandboxes and additionally wakes on SandboxClaim changes mapped
// to their owning Sandbox, so a claim binding-state change re-projects the
// pod phase (§4.6.1).
func (r *OccupancyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{}
	if r.MaxConcurrentReconciles > 0 {
		opts.MaxConcurrentReconciles = r.MaxConcurrentReconciles
	}
	if r.QueueFactory != nil {
		opts.NewQueue = r.QueueFactory
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&lennyv1.Sandbox{}).
		Watches(&lennyv1.SandboxClaim{}, handler.EnqueueRequestsFromMapFunc(claimToSandbox)).
		Named("warmpool-occupancy").
		WithOptions(opts).
		Complete(r)
}

// ClaimToSandboxForTest exposes the §4.6.1 claim→sandbox map function for
// the package's external tests.
func (r *OccupancyReconciler) ClaimToSandboxForTest(ctx context.Context, obj client.Object) []reconcile.Request {
	return claimToSandbox(ctx, obj)
}
