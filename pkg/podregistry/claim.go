// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/podclaim/state"
)

// The claim-object helpers below create, name, release, and first bind the
// per-pod occupancy PodClaim. The gateway's pod claimer
// (pkg/gateway/core/podclaim) calls them on its claim path, and the
// Postgres-backed fallback claim path of this package calls them directly,
// so both create an identical claim. The CRD pool manager of
// pkg/podlifecycle names its claims with ClaimName.

// claimStatusFieldOwner is the gateway field manager the first binding-state
// write carries. spec: §4.6.3 grants the gateway `patch` (not `update`) on
// the `podclaims/status` subresource, so the write is a Server-Side Apply
// PATCH under this manager.
const claimStatusFieldOwner = string(ownership.Gateway)

// ClaimName is the §4.6.1 deterministic PodClaim name for a pod
// (`claim-<podName>`), so a second claim for the same pod collides at
// CREATE rather than duplicating the binding. The §4.7 recycle disposition
// driver resolves a pod's claim by this name to patch its binding state, so
// the mapping lives in one place rather than being re-derived by every
// caller.
func ClaimName(podName string) string {
	return "claim-" + podName
}

// CreateClaim creates the per-pod occupancy PodClaim (§4.6.1) that
// records the gateway's acquisition of the named AgentPod pod. The §4.6.1
// normal claim path and the Postgres-backed fallback claim path share this
// helper so both create an identical PodClaim: the CRD a fallback claim
// creates is a real object, so the lenny-podclaim-guard admission
// webhook's CREATE-time per-pod-uniqueness check guards the fallback
// exactly as it guards the normal path. The claim name is deterministic
// per pod (claim-<podName>), so a second CREATE for the same pod collides
// at CREATE rather than duplicating the binding. The claim is created with
// spec only; the gateway writes the first binding state with a subsequent
// status patch (see WriteBoundStatus).
func CreateClaim(ctx context.Context, cl client.Client, namespace, agentPodName, tenantID string) (*lennyv1.PodClaim, error) {
	claim := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClaimName(agentPodName),
			Namespace: namespace,
		},
		Spec: lennyv1.PodClaimSpec{
			// The occupancy claim names the pod and its tenant and no session:
			// the session row records which session holds the pod (§4.6.3).
			AgentPodRef: agentPodName,
			TenantID:    tenantID,
		},
	}
	if err := cl.Create(ctx, claim); err != nil {
		// The pod is acquired but its binding state is unset; the §4.6.1
		// orphan-claim garbage collection reclaims it on the
		// CREATE-before-status predicate.
		return nil, fmt.Errorf("create pod claim for %s: %w", agentPodName, err)
	}
	return claim, nil
}

// DeleteClaim deletes the per-pod occupancy PodClaim for podName. It is
// the gateway's only action when a session-mode occupancy episode ends: the
// gateway never writes AgentPod.status (§4.6.3 ownership decomposition), so it
// releases the pod by deleting the claim and the WarmPoolController projects
// the resulting occupancy phase (§4.6.1 occupancy projection). The delete is
// idempotent: a missing claim is a no-op so a double release or a claim the
// orphan GC already collected does not error.
//
// spec: §4.6.1 (occupancy projection on claim DELETE); §4.6.3 (gateway is
// not a writer of AgentPod.status; releases via the claim lifecycle).
func DeleteClaim(ctx context.Context, cl client.Client, namespace, podName string) error {
	claim := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClaimName(podName),
			Namespace: namespace,
		},
	}
	if err := cl.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("podclaim: delete per-pod claim for sandbox %s: %w", podName, err)
	}
	return nil
}

// WriteBoundStatus writes the first `bound` binding-state status patch on a
// freshly created per-pod PodClaim, stamping the transition time from the
// wall clock. The §4.6.1 Postgres-backed fallback claim path, which CREATEs
// the per-pod claim outside the in-cluster claim path, writes the same
// first binding state through it. spec: §4.6.1 (fallback claim path; first
// `bound` status patch).
func WriteBoundStatus(ctx context.Context, cl client.Client, namespace, claimName string) error {
	return WriteBoundStatusAt(ctx, cl, namespace, claimName, time.Now)
}

// WriteBoundStatusAt stamps the first binding state (`bound`) on a freshly
// created per-pod PodClaim. The claim is CREATEd with spec only, because a
// Kubernetes status subresource is not writable by the resource Create call
// (§4.6.1); this helper writes that first status. It also stamps the
// binding-state transition time from now, which the WarmPoolController
// orphan GC keys its live-binding-state reclaim predicate on (§4.6.1 /
// §4.6.3). A nil now reads the wall clock.
//
// The write is a status-subresource PATCH via Server-Side Apply under the
// gateway field manager (§4.6.3 grants the gateway `get`/`patch` on the
// `podclaims/status` subresource and no `update` verb, so the write
// must be a PATCH; controller-runtime's Status().Update issues an HTTP PUT
// the API server authorizes against `update`).
//
// spec: §4.6.1 (pod claim mechanism; first `bound` status patch);
// §4.6.3 (gateway-owned PodClaim.status binding state via the
// `patch`-only podclaims/status grant).
func WriteBoundStatusAt(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	// Idempotent: a retry after a partial success finds the claim already
	// bound and returns without re-stamping the transition time. A NotFound
	// surfaces as an error so the caller can undo the counter reservation.
	var cur lennyv1.PodClaim
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: claimName}, &cur); err != nil {
		return fmt.Errorf("podclaim: get claim %s for bound status: %w", claimName, err)
	}
	if cur.Status.Phase == string(claimstate.Bound) {
		return nil
	}
	patch := &lennyv1.PodClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "PodClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
	}
	patch.Status.Phase = string(claimstate.Bound)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := cl.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(claimStatusFieldOwner)); err != nil {
		return fmt.Errorf("podclaim: write bound status on claim %s: %w", claimName, err)
	}
	return nil
}
